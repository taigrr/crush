package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// Device subject layout: crush.client.v1.<dev>.<verb>[.<arg>].
const (
	subjClientRoot      = "crush.client.v1."
	subjClientAttention = "crush.client.v1.broadcast.attention"
	bridgeQueue         = "crush-device-bridge"
	anyNode             = "any"

	bridgeTimeout          = 30 * time.Second
	maxBridgeBody          = 4 << 20
	maxInflightPerDevice   = 8
	maxStreamsPerDevice    = 16
	defaultStreamCredit    = 256
	maxStreamCredit        = 1024
	streamInterestInterval = 10 * time.Second
	streamInterestGrace    = 30 * time.Second
	streamBlockedLease     = 60 * time.Second
	maxSSELine             = 4 << 20
)

// bridgeRequest is the NATS-to-HTTP envelope a device sends on
// crush.client.v1.<dev>.api.<node>.
type bridgeRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// bridgeResponse answers a bridgeRequest.
type bridgeResponse struct {
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// streamRequest opens an event stream (crush.client.v1.<dev>.sub).
type streamRequest struct {
	Node   string         `json:"node"`
	Path   string         `json:"path"`
	Query  map[string]any `json:"query"`
	Credit int            `json:"credit"`
}

type streamAck struct {
	Stream string `json:"stream"`
	Seq    int64  `json:"seq"`
	Credit int    `json:"credit"`
}

type streamFrame struct {
	Seq  int64  `json:"seq"`
	Data string `json:"data"`
}

// deviceBridge serves device subjects from the CLIENTS account: bridged
// /v1 requests, event streams, attention fan-out, and control notices.
type deviceBridge struct {
	p      *pairingService
	nc     *nats.Conn
	mesh   *cluster.Mesh
	router *deviceRouter
	local  http.Handler
	nodeID string
	// attention is the local attention feed (a seam for tests).
	attention func(context.Context) <-chan pubsub.Event[proto.AttentionEvent]

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	subs   []*nats.Subscription

	mu       sync.Mutex
	inflight map[string]int
	streams  map[string]*deviceStream
}

func newDeviceBridge(p *pairingService, mesh *cluster.Mesh) *deviceBridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &deviceBridge{
		p: p, nc: mesh.ClientsConn(), mesh: mesh, router: newDeviceRouter(),
		local: p.srv.h.Handler, nodeID: p.node.Identity().ID, attention: p.srv.backend.AttentionEvents,
		ctx: ctx, cancel: cancel,
		inflight: map[string]int{}, streams: map[string]*deviceStream{},
	}
}

func (b *deviceBridge) start() error {
	handlers := map[string]nats.MsgHandler{
		subjClientRoot + "*.api.*": b.onAPI,
		subjClientRoot + "*.sub":   b.onSub,
		subjClientRoot + "*.unsub": b.onUnsub,
		subjClientRoot + "*.ack.*": b.onAck,
	}
	for subj, fn := range handlers {
		// A queue group makes exactly one node in the cluster answer; NATS
		// prefers the local member.
		sub, err := b.nc.QueueSubscribe(subj, bridgeQueue, fn)
		if err != nil {
			b.stop()
			return err
		}
		b.subs = append(b.subs, sub)
	}
	b.wg.Go(b.attentionLoop)
	return b.nc.Flush()
}

func (b *deviceBridge) stop() {
	for _, s := range b.subs {
		_ = s.Unsubscribe()
	}
	b.cancel()
	b.wg.Wait()
}

// device resolves the device that sent m from its subject (the device's
// NATS permissions only let it publish under its own ID) and checks it is
// still paired.
func (b *deviceBridge) device(m *nats.Msg) (pairing.Device, []string, bool) {
	tokens := strings.Split(m.Subject, ".")
	if len(tokens) < 5 {
		return pairing.Device{}, nil, false
	}
	d, ok := b.p.reg.Get(tokens[3])
	if !ok || d.Revoked() {
		return pairing.Device{}, nil, false
	}
	return d, tokens, true
}

// replyOK reports whether a reply subject belongs to the device's private
// inbox. NATS does not check reply subjects against permissions, so this
// stops a device from steering answers into another device's subjects.
func replyOK(m *nats.Msg, deviceID string) bool {
	return strings.HasPrefix(m.Reply, "_INBOX."+deviceID+".")
}

func (b *deviceBridge) acquire(deviceID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inflight[deviceID] >= maxInflightPerDevice {
		return false
	}
	b.inflight[deviceID]++
	return true
}

func (b *deviceBridge) release(deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inflight[deviceID]--; b.inflight[deviceID] <= 0 {
		delete(b.inflight, deviceID)
	}
}

func (b *deviceBridge) respond(m *nats.Msg, resp bridgeResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if len(data) > int(b.nc.MaxPayload()) {
		data, _ = json.Marshal(bridgeResponse{ID: resp.ID, Status: http.StatusRequestEntityTooLarge})
	}
	_ = b.nc.Publish(m.Reply, data)
}

func (b *deviceBridge) onAPI(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 6 || !replyOK(m, d.DeviceID) {
		return
	}
	var req bridgeRequest
	if json.Unmarshal(m.Data, &req) != nil || len(req.ID) > 128 {
		b.respond(m, bridgeResponse{ID: req.ID, Status: http.StatusBadRequest})
		return
	}
	if !b.acquire(d.DeviceID) {
		b.respond(m, bridgeResponse{ID: req.ID, Status: http.StatusTooManyRequests})
		return
	}
	b.wg.Go(func() {
		defer b.release(d.DeviceID)
		b.respond(m, b.serveAPI(d, tokens[5], req))
	})
}

// target resolves the node a request should run on to a local path:
// this node's own path, or the member proxy for another node.
func (b *deviceBridge) target(node, p string) (string, bool) {
	if node == anyNode || node == b.nodeID {
		return p, true
	}
	if _, err := b.p.node.MemberByID(node); err != nil {
		return "", false
	}
	return "/v1/nodes/" + node + p, true
}

func (b *deviceBridge) serveAPI(d pairing.Device, node string, req bridgeRequest) bridgeResponse {
	resp := bridgeResponse{ID: req.ID}
	if b.router.authorize(req.Method, req.Path, d.Scopes) == "" {
		resp.Status = http.StatusForbidden
		return resp
	}
	p, ok := b.target(node, req.Path)
	if !ok {
		resp.Status = http.StatusNotFound
		return resp
	}
	body, err := base64.RawURLEncoding.DecodeString(req.Body)
	if err != nil || len(body) > maxBridgeBody {
		resp.Status = http.StatusBadRequest
		return resp
	}
	query, err := url.ParseQuery(req.Query)
	if err != nil {
		resp.Status = http.StatusBadRequest
		return resp
	}
	ctx, cancel := context.WithTimeout(b.ctx, bridgeTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, req.Method, (&url.URL{Path: p, RawQuery: query.Encode()}).String(), bytes.NewReader(body))
	if err != nil {
		resp.Status = http.StatusBadRequest
		return resp
	}
	// Only the content type is taken from the device; identity headers
	// it might send are dropped.
	if ct := req.Headers["content-type"]; ct != "" {
		hreq.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	b.local.ServeHTTP(rec, hreq)
	if rec.Body.Len() > maxBridgeBody {
		resp.Status = http.StatusRequestEntityTooLarge
		return resp
	}
	resp.Status = rec.Code
	resp.Headers = map[string]string{}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		resp.Headers["content-type"] = ct
	}
	resp.Body = base64.RawURLEncoding.EncodeToString(rec.Body.Bytes())
	return resp
}

// deviceStream is one event stream opened by a device.
type deviceStream struct {
	id       string
	deviceID string
	subject  string
	cancel   context.CancelFunc

	mu     sync.Mutex
	cond   *sync.Cond
	acked  int64
	credit int64
	sent   int64
}

func (b *deviceBridge) onSub(m *nats.Msg) {
	d, _, ok := b.device(m)
	if !ok || !replyOK(m, d.DeviceID) {
		return
	}
	fail := func(status int) {
		data, _ := json.Marshal(map[string]any{"error": http.StatusText(status), "status": status})
		_ = b.nc.Publish(m.Reply, data)
	}
	var req streamRequest
	if json.Unmarshal(m.Data, &req) != nil {
		fail(http.StatusBadRequest)
		return
	}
	pattern := b.router.authorize(http.MethodGet, req.Path, d.Scopes)
	if !streamRoutes[pattern] {
		fail(http.StatusForbidden)
		return
	}
	node := req.Node
	if node == "" {
		node = anyNode
	}
	p, ok := b.target(node, req.Path)
	if !ok {
		fail(http.StatusNotFound)
		return
	}
	credit := int64(req.Credit)
	if credit <= 0 {
		credit = defaultStreamCredit
	}
	credit = min(credit, maxStreamCredit)

	b.mu.Lock()
	open := 0
	for _, s := range b.streams {
		if s.deviceID == d.DeviceID {
			open++
		}
	}
	if open >= maxStreamsPerDevice {
		b.mu.Unlock()
		fail(http.StatusTooManyRequests)
		return
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithCancel(b.ctx)
	s := &deviceStream{
		id: id, deviceID: d.DeviceID, cancel: cancel, credit: credit,
		subject: subjClientRoot + d.DeviceID + ".ev." + id,
	}
	s.cond = newCond(s)
	b.streams[id] = s
	b.mu.Unlock()

	query := url.Values{}
	for k, v := range req.Query {
		if k == "client_id" {
			continue
		}
		query.Set(k, strings.Trim(strings.TrimSpace(jsonScalar(v)), `"`))
	}
	// Each stream is its own client of the workspace.
	query.Set("client_id", uuid.NewString())
	data, _ := json.Marshal(map[string]string{"stream": id})
	_ = b.nc.Publish(m.Reply, data)
	b.wg.Go(func() {
		defer b.closeStream(id)
		b.runStream(ctx, s, p, query)
	})
}

func jsonScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func (b *deviceBridge) stream(m *nats.Msg, id string) *deviceStream {
	d, _, ok := b.device(m)
	if !ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.streams[id]
	if s == nil || s.deviceID != d.DeviceID {
		return nil
	}
	return s
}

func (b *deviceBridge) onUnsub(m *nats.Msg) {
	var req struct {
		Stream string `json:"stream"`
	}
	if json.Unmarshal(m.Data, &req) != nil {
		return
	}
	if s := b.stream(m, req.Stream); s != nil {
		s.cancel()
	}
}

func (b *deviceBridge) onAck(m *nats.Msg) {
	tokens := strings.Split(m.Subject, ".")
	if len(tokens) != 6 {
		return
	}
	var ack streamAck
	if json.Unmarshal(m.Data, &ack) != nil {
		return
	}
	s := b.stream(m, tokens[5])
	if s == nil {
		return
	}
	s.mu.Lock()
	if ack.Seq >= s.acked && ack.Seq < s.sent {
		s.acked = ack.Seq + 1
	}
	if ack.Credit > 0 {
		s.credit = min(int64(ack.Credit), maxStreamCredit)
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (b *deviceBridge) closeStream(id string) {
	b.mu.Lock()
	s := b.streams[id]
	delete(b.streams, id)
	b.mu.Unlock()
	if s != nil {
		s.cancel()
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// closeDevice ends every stream of a device (revocation).
func (b *deviceBridge) closeDevice(deviceID string) {
	b.mu.Lock()
	var ids []string
	for id, s := range b.streams {
		if s.deviceID == deviceID {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.closeStream(id)
	}
}

// runStream runs the SSE handler in-process and publishes each event as a
// frame. It stops when the device unsubscribes, stops listening, is
// revoked, or holds the stream at zero credit for too long.
func (b *deviceBridge) runStream(ctx context.Context, s *deviceStream, p string, query url.Values) {
	pr, pw := io.Pipe()
	w := &streamWriter{header: http.Header{}, w: pw}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, (&url.URL{Path: p, RawQuery: query.Encode()}).String(), nil)
	if err != nil {
		return
	}
	b.wg.Go(func() {
		b.local.ServeHTTP(w, req)
		_ = pw.Close()
	})
	b.wg.Go(func() { b.watchStream(ctx, s) })
	go func() {
		<-ctx.Done()
		_ = pr.CloseWithError(context.Canceled)
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), maxSSELine)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if !s.waitCredit(ctx) {
			return
		}
		s.mu.Lock()
		seq := s.sent
		s.sent++
		s.mu.Unlock()
		frame, _ := json.Marshal(streamFrame{Seq: seq, Data: data})
		if err := b.nc.Publish(s.subject, frame); err != nil {
			return
		}
	}
}

func newCond(s *deviceStream) *sync.Cond { return sync.NewCond(&s.mu) }

// waitCredit blocks while the device has too many unacknowledged frames,
// giving up after streamBlockedLease.
func (s *deviceStream) waitCredit(ctx context.Context) bool {
	deadline := time.Now().Add(streamBlockedLease)
	timer := time.AfterFunc(streamBlockedLease, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer timer.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.sent-s.acked >= s.credit {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		s.cond.Wait()
	}
	return ctx.Err() == nil
}

// watchStream ends a stream once its device is revoked or no longer
// subscribed to the frames (it left without unsubscribing).
func (b *deviceBridge) watchStream(ctx context.Context, s *deviceStream) {
	tick := time.NewTicker(streamInterestInterval)
	defer tick.Stop()
	lastInterest := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if d, ok := b.p.reg.Get(s.deviceID); !ok || d.Revoked() {
			b.closeStream(s.id)
			return
		}
		if b.mesh.ClientsInterest(s.subject) {
			lastInterest = time.Now()
		} else if time.Since(lastInterest) > streamInterestGrace {
			b.closeStream(s.id)
			return
		}
	}
}

// streamWriter is an in-process http.ResponseWriter for SSE handlers.
type streamWriter struct {
	header http.Header
	w      io.Writer
	status int
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status != http.StatusOK {
		return len(p), nil
	}
	return w.w.Write(p)
}

func (w *streamWriter) Flush() {}

// attentionLoop forwards this node's own attention events to devices.
// Events relayed from other members are skipped: their owners publish
// them, and the CLIENTS account spans the cluster.
func (b *deviceBridge) attentionLoop() {
	events := b.attention(b.ctx)
	name := b.p.node.Identity().Name
	for {
		select {
		case <-b.ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			a := ev.Payload
			if a.NodeID != "" || a.Kind == proto.AttentionClusterJoin {
				continue
			}
			a.NodeID, a.NodeName = b.nodeID, name
			data, err := json.Marshal(a)
			if err != nil {
				continue
			}
			if err := b.nc.Publish(subjClientAttention, data); err != nil {
				slog.Warn("Failed to publish device attention", "error", err)
			}
		}
	}
}

// control tells a device's connected client something, before the
// change takes effect (for "revoked", before its user is removed).
func (b *deviceBridge) control(deviceID, kind string) {
	data, _ := json.Marshal(map[string]string{"type": kind})
	if err := b.nc.Publish(subjClientRoot+deviceID+".control", data); err == nil {
		_ = b.nc.Flush()
	}
}
