package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/server/devicescope"
	"github.com/taigrr/crush/internal/swarm"
)

var errResponseTooLarge = errors.New("bridged response too large")

// Device subject layout: crush.client.v1.<dev>.<verb>[.<arg>]. Forwarded
// requests travel member to member on crush.client.fwd.<node>, in the
// mesh's global account, which device connections can never reach.
const (
	subjClientRoot      = "crush.client.v1."
	subjClientAttention = "crush.client.v1.broadcast.attention"
	subjClientFwd       = "crush.client.fwd."
	bridgeQueue         = "crush-device-bridge"
	anyNode             = "any"

	verbInfo      = "info"
	verbAPI       = "api"
	verbSub       = "sub"
	verbUnsub     = "unsub"
	verbAck       = "ack"
	verbEvents    = "ev"
	verbControl   = "control"
	verbAttention = "attention"

	bridgeTimeout    = 30 * time.Second
	minBridgeTimeout = time.Second
	// handlerGrace lets a handler that outlived its device's timeout
	// finish, so an idempotent retry learns the real outcome.
	handlerGrace = 30 * time.Second
	// fwdGrace is how much longer the entry node waits for the owner than
	// the owner's own timeout, so the owner's 504 wins over a 502.
	fwdGrace             = 2 * time.Second
	maxBridgeBody        = 4 << 20
	maxInflightPerDevice = 8
	maxRequestID         = 128
	rateLimitRetry       = 250 * time.Millisecond
	unreachableRetry     = 5 * time.Second
	// atFormat is RFC 3339 in UTC with millisecond precision.
	atFormat = "2006-01-02T15:04:05.000Z"

	msgUnsupportedVersion = "unsupported version"
)

func wireNow() string { return time.Now().UTC().Format(atFormat) }

// deviceBridge serves device subjects from the CLIENTS account: bridged
// /v1 requests, event streams, attention fan-out, and control notices.
type deviceBridge struct {
	p      *pairingService
	nc     *nats.Conn
	mesh   *cluster.Mesh
	router *deviceRouter
	local  http.Handler
	nodeID string
	idem   *idemCache
	// attention is the local attention feed (a seam for tests).
	attention func(context.Context) <-chan pubsub.Event[proto.AttentionEvent]
	// swarmOf returns a session's swarm address, or "" (a seam for tests).
	swarmOf func(ctx context.Context, workspaceID, sessionID string) string
	// sessionExists reports whether a workspace here has a session (a seam
	// for tests).
	sessionExists func(ctx context.Context, workspaceID, sessionID string) (bool, error)
	// lookup resolves a swarm address to its owner (a seam for tests).
	lookup func(ctx context.Context, address string) (backend.SwarmLookupResult, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	subs   []*nats.Subscription

	mu       sync.Mutex
	tune     streamTuning
	stopped  bool
	inflight map[string]int
	streams  map[string]*deviceStream
	// devices cancels a device's in-flight requests on revocation.
	devices map[string]*deviceWork
}

// deviceWork is one device's in-flight bridged requests.
type deviceWork struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newDeviceBridge(p *pairingService, mesh *cluster.Mesh) *deviceBridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &deviceBridge{
		p: p, nc: mesh.ClientsConn(), mesh: mesh, router: newDeviceRouter(),
		local: p.srv.h.Handler, nodeID: p.node.Identity().ID, attention: p.srv.backend.AttentionEvents,
		idem: newIdemCache(), lookup: p.srv.backend.LookupSwarmAddress,
		ctx: ctx, cancel: cancel, tune: defaultStreamTuning(),
		inflight: map[string]int{}, streams: map[string]*deviceStream{}, devices: map[string]*deviceWork{},
	}
	b.swarmOf = b.sessionAddress
	b.sessionExists = b.hasSession
	return b
}

// spawn runs fn on the bridge's wait group unless the bridge is stopping,
// so NATS callbacks still in flight during stop never add work after
// Wait has begun.
func (b *deviceBridge) spawn(fn func()) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	b.wg.Go(fn)
	return true
}

func (b *deviceBridge) start() error {
	// Requests, hellos, and stream opens go to one node (a queue group;
	// NATS prefers the local member). Acks and unsubscribes go to every
	// node, because only the node that opened a stream knows it; the
	// others ignore the unknown stream ID.
	queued := map[string]nats.MsgHandler{
		subjClientRoot + "*." + verbAPI + ".*": b.onAPI,
		subjClientRoot + "*." + verbSub:        b.onSub,
		subjClientRoot + "*." + verbInfo:       b.onInfo,
	}
	broadcast := map[string]nats.MsgHandler{
		subjClientRoot + "*." + verbUnsub:      b.onUnsub,
		subjClientRoot + "*." + verbAck + ".*": b.onAck,
	}
	for subj, fn := range queued {
		sub, err := b.nc.QueueSubscribe(subj, bridgeQueue, fn)
		if err != nil {
			b.stop()
			return err
		}
		b.subs = append(b.subs, sub)
	}
	for subj, fn := range broadcast {
		sub, err := b.nc.Subscribe(subj, fn)
		if err != nil {
			b.stop()
			return err
		}
		b.subs = append(b.subs, sub)
	}
	fwd, err := b.mesh.Subscribe(subjClientFwd+b.nodeID, b.onFwd)
	if err != nil {
		b.stop()
		return err
	}
	b.subs = append(b.subs, fwd)
	b.spawn(b.attentionLoop)
	if err := b.mesh.Conn().Flush(); err != nil {
		return err
	}
	return b.nc.Flush()
}

func (b *deviceBridge) stop() {
	for _, s := range b.subs {
		_ = s.Unsubscribe()
	}
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.cancel()
	b.wg.Wait()
}

// work returns the context for a device's bridged requests.
func (b *deviceBridge) work(deviceID string) context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.devices[deviceID]
	if w == nil {
		ctx, cancel := context.WithCancel(b.ctx)
		w = &deviceWork{ctx: ctx, cancel: cancel}
		b.devices[deviceID] = w
	}
	return w.ctx
}

// device resolves the device that sent m from its subject (the device's
// NATS permissions only let it publish under its own ID) and checks it is
// still paired.
func (b *deviceBridge) device(m *nats.Msg) (pairing.Device, []string, bool) {
	tokens := strings.Split(m.Subject, ".")
	if len(tokens) < 5 {
		return pairing.Device{}, nil, false
	}
	d, ok := b.activeDevice(tokens[3])
	return d, tokens, ok
}

func (b *deviceBridge) activeDevice(deviceID string) (pairing.Device, bool) {
	d, ok := b.p.reg.Get(deviceID)
	if !ok || d.Revoked() {
		return pairing.Device{}, false
	}
	return d, true
}

func (b *deviceBridge) active(deviceID string) bool {
	_, ok := b.activeDevice(deviceID)
	return ok
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

func rateLimited() *bridgeError {
	return &bridgeError{Code: codeRateLimited, Message: "too many requests in flight", RetryAfterMS: rateLimitRetry.Milliseconds()}
}

func unavailable(msg string) *bridgeError { return newBridgeError(codeUnavailable, msg) }

// encode marshals a reply, replacing anything over the connection's
// payload limit with a payload_too_large error.
func (b *deviceBridge) encode(nc *nats.Conn, v any, id string) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(errorResponse(id, b.nodeID, newBridgeError(codeInternal, "cannot encode reply")))
	}
	if nc != nil && int64(len(data)) > nc.MaxPayload() {
		data, _ = json.Marshal(errorResponse(id, b.nodeID, newBridgeError(codePayloadTooLarge, "reply exceeds the payload limit")))
	}
	return data
}

// reply answers a device request on its inbox (the caller checked
// replyOK).
func (b *deviceBridge) reply(m *nats.Msg, v any, id string) {
	_ = b.nc.Publish(m.Reply, b.encode(b.nc, v, id))
}

func (b *deviceBridge) onInfo(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 5 || !replyOK(m, d.DeviceID) {
		return
	}
	b.reply(m, infoReply{
		V: wireVersion, Versions: []int{wireVersion}, NodeID: b.nodeID, NodeName: b.p.node.Identity().Name,
		DeviceID: d.DeviceID, Role: string(pairing.DefaultRole), Scopes: d.EffectiveScopes(), Features: slices.Clone(bridgeFeatures),
		Limits: b.limits(),
	}, "")
}

func (b *deviceBridge) limits() bridgeLimits {
	b.mu.Lock()
	t := b.tune
	b.mu.Unlock()
	return bridgeLimits{
		MaxRequestBytes: maxBridgeBody, MaxResponseBytes: maxBridgeBody,
		MaxInflightRequests: maxInflightPerDevice, MaxStreams: maxStreamsPerDevice, MaxCredit: maxStreamCredit,
		RequestTimeoutMS: bridgeTimeout.Milliseconds(), HeartbeatMS: t.heartbeat.Milliseconds(),
		ResumeWindowMS: t.idle.Milliseconds(), ResumeBufferFrames: t.ring,
	}
}

// parseRequest decodes and validates a request envelope. The returned
// request carries its ID whenever it could be read.
func parseRequest(data []byte) (bridgeRequest, *bridgeError) {
	var req bridgeRequest
	if err := json.Unmarshal(data, &req); err != nil {
		var idOnly struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &idOnly)
		if len(idOnly.ID) <= maxRequestID {
			req.ID = idOnly.ID
		}
		return req, newBridgeError(codeBadRequest, "malformed envelope")
	}
	switch {
	case len(req.ID) > maxRequestID:
		req.ID = ""
		return req, newBridgeError(codeBadRequest, "id must be at most 128 bytes")
	case req.V != 0 && req.V != wireVersion:
		return req, newBridgeError(codeBadRequest, msgUnsupportedVersion)
	case req.ID == "":
		return req, newBridgeError(codeBadRequest, "id is required")
	case len(req.JSON) > 0 && req.Body != "":
		return req, newBridgeError(codeBadRequest, "use json or body, not both")
	case req.Method == http.MethodGet && (len(req.JSON) > 0 || req.Body != ""):
		return req, newBridgeError(codeBadRequest, "GET requests carry no body")
	case req.IdempotencyKey != "" && !validIdemKey(req.IdempotencyKey):
		return req, newBridgeError(codeBadRequest, "idempotency_key must be 1 to 64 of [A-Za-z0-9_-]")
	}
	return req, nil
}

func validIdemKey(k string) bool {
	if k == "" || len(k) > maxIdempotencyKey {
		return false
	}
	for _, c := range []byte(k) {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

func (req bridgeRequest) timeout() time.Duration {
	if req.TimeoutMS <= 0 {
		return bridgeTimeout
	}
	return min(max(time.Duration(req.TimeoutMS)*time.Millisecond, minBridgeTimeout), bridgeTimeout)
}

// body decodes the request body (inline JSON or base64url).
func (req bridgeRequest) body() ([]byte, *bridgeError) {
	if len(req.JSON) > 0 {
		if len(req.JSON) > maxBridgeBody {
			return nil, newBridgeError(codePayloadTooLarge, "body exceeds 4 MiB")
		}
		return req.JSON, nil
	}
	if base64.RawURLEncoding.DecodedLen(len(req.Body)) > maxBridgeBody {
		return nil, newBridgeError(codePayloadTooLarge, "body exceeds 4 MiB")
	}
	body, err := base64.RawURLEncoding.DecodeString(req.Body)
	if err != nil {
		return nil, newBridgeError(codeBadRequest, "body must be base64url without padding")
	}
	return body, nil
}

// contentType is the request's content type: JSON for inline bodies,
// otherwise only what the device named (identity headers are dropped).
func (req bridgeRequest) contentType() string {
	if len(req.JSON) > 0 {
		return "application/json"
	}
	return req.Headers["content-type"]
}

// wantsJSON reports whether replies may inline JSON bodies: v1 clients
// and anyone who sent one. Phase 2a clients keep base64 bodies.
func (req bridgeRequest) wantsJSON() bool { return req.V >= wireVersion || len(req.JSON) > 0 }

// parseQuery accepts the v1 object form ({"k": "v"} or {"k": ["a"]}) and
// the phase 2a string form, and strips client_id (the bridge owns
// client identity).
func parseQuery(raw json.RawMessage) (url.Values, *bridgeError) {
	out := url.Values{}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	bad := newBridgeError(codeBadRequest, "query must be an object of strings or string lists")
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, bad
		}
		q, err := url.ParseQuery(s)
		if err != nil {
			return nil, bad
		}
		out = q
	} else {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return nil, bad
		}
		for k, v := range m {
			var list []any
			if json.Unmarshal(v, &list) != nil {
				var one any
				if json.Unmarshal(v, &one) != nil {
					return nil, bad
				}
				list = []any{one}
			}
			for _, item := range list {
				s, ok := jsonScalar(item)
				if !ok {
					return nil, bad
				}
				out.Add(k, s)
			}
		}
	}
	out.Del("client_id")
	return out, nil
}

func jsonScalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}

func (b *deviceBridge) onAPI(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 6 || !replyOK(m, d.DeviceID) {
		return
	}
	req, berr := parseRequest(m.Data)
	if berr != nil {
		resp := errorResponse(req.ID, b.nodeID, berr)
		if berr.Message == msgUnsupportedVersion {
			resp.Versions = []int{wireVersion}
		}
		b.reply(m, resp, req.ID)
		return
	}
	if !b.acquire(d.DeviceID) {
		b.reply(m, errorResponse(req.ID, b.nodeID, rateLimited()), req.ID)
		return
	}
	ctx := b.work(d.DeviceID)
	if !b.spawn(func() {
		defer b.release(d.DeviceID)
		resp, done := b.route(ctx, d, tokens[5], req)
		// A device revoked while the request ran gets no answer.
		if ctx.Err() == nil && b.active(d.DeviceID) {
			b.reply(m, resp, req.ID)
		}
		// The slot stays taken until the handler really returned.
		<-done
	}) {
		b.release(d.DeviceID)
		b.reply(m, errorResponse(req.ID, b.nodeID, unavailable("node is shutting down")), req.ID)
	}
}

var closedDone = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// route checks a request on the entry node and runs it here or on the
// node that owns it. done is closed once no handler on this node is
// still running for it.
func (b *deviceBridge) route(ctx context.Context, d pairing.Device, node string, req bridgeRequest) (bridgeResponse, <-chan struct{}) {
	fail := func(e *bridgeError) (bridgeResponse, <-chan struct{}) {
		return errorResponse(req.ID, b.nodeID, e), closedDone
	}
	if _, berr := b.check(d, req); berr != nil {
		return fail(berr)
	}
	owner, _, berr := b.resolveNode(ctx, node, req.Target)
	if berr != nil {
		return fail(berr)
	}
	if owner == b.nodeID {
		return b.execute(ctx, d, req)
	}
	req.Target = nil
	var resp bridgeResponse
	if berr := b.forward(ctx, d.DeviceID, owner, fwdAPI, req, &resp, req.timeout()+fwdGrace); berr != nil {
		return fail(berr)
	}
	return resp, closedDone
}

// check authorizes req for d: route scope, stream routes refused, body
// limits and body rules. Every node that handles the request runs it.
func (b *deviceBridge) check(d pairing.Device, req bridgeRequest) (string, *bridgeError) {
	pattern, berr := b.router.authorize(req.Method, req.Path, d.EffectiveScopes())
	if berr != nil {
		return "", berr
	}
	if streamRoutes[pattern] {
		return "", newBridgeError(codeRouteNotAllowed, "event streams are opened with sub")
	}
	if _, berr := parseQuery(req.Query); berr != nil {
		return "", berr
	}
	body, berr := req.body()
	if berr != nil {
		return "", berr
	}
	if _, berr := applyBodyRules(pattern, d, body); berr != nil {
		return "", berr
	}
	return pattern, nil
}

// resolveNode picks the node a request runs on: the subject's node, or
// for "any" the request's target (a node or a swarm address), else this
// node. For a swarm target it also returns the session it names.
func (b *deviceBridge) resolveNode(ctx context.Context, node string, target *bridgeTarget) (string, string, *bridgeError) {
	if node != anyNode {
		if target != nil && (target.Node != "" || target.Swarm != "") {
			return "", "", newBridgeError(codeBadRequest, "target is only valid on api.any")
		}
		owner, berr := b.memberNode(node)
		return owner, "", berr
	}
	if target == nil || (target.Node == "" && target.Swarm == "") {
		return b.nodeID, "", nil
	}
	if target.Node != "" && target.Swarm != "" {
		return "", "", newBridgeError(codeBadRequest, "target names a node or a swarm address, not both")
	}
	if target.Node != "" {
		owner, berr := b.memberNode(target.Node)
		return owner, "", berr
	}
	res, err := b.lookup(ctx, target.Swarm)
	switch {
	case err == nil:
	case errors.Is(err, backend.ErrSwarmAddressInvalid):
		return "", "", newBridgeError(codeBadRequest, "invalid swarm address")
	case errors.Is(err, backend.ErrSwarmAddressAmbiguous):
		return "", "", newBridgeError(codeBadRequest, "swarm address is ambiguous; use the color-animal-xxxx form")
	case errors.Is(err, backend.ErrSwarmAddressNotFound):
		return "", "", newBridgeError(codeNodeNotFound, "no session has that swarm address")
	default:
		return "", "", newBridgeError(codeInternal, "swarm lookup failed")
	}
	owner := b.nodeID
	if res.NodeID != "" {
		var berr *bridgeError
		if owner, berr = b.memberNode(res.NodeID); berr != nil {
			return "", "", berr
		}
	}
	return owner, res.SessionID, nil
}

func (b *deviceBridge) memberNode(node string) (string, *bridgeError) {
	if node == b.nodeID {
		return node, nil
	}
	if _, err := b.p.node.MemberByID(node); err != nil {
		return "", newBridgeError(codeNodeNotFound, "unknown node")
	}
	return node, nil
}

// forward sends a device message to its owning node over the mesh and
// decodes the owner's reply into out.
func (b *deviceBridge) forward(ctx context.Context, deviceID, owner string, kind fwdKind, req, out any, wait time.Duration) *bridgeError {
	raw, err := json.Marshal(req)
	if err != nil {
		return newBridgeError(codeInternal, "cannot encode request")
	}
	data, err := json.Marshal(fwdEnvelope{V: wireVersion, Kind: kind, DeviceID: deviceID, EntryNode: b.nodeID, Request: raw})
	if err != nil {
		return newBridgeError(codeInternal, "cannot encode request")
	}
	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	msg, err := b.mesh.Request(cctx, subjClientFwd+owner, data)
	if err != nil {
		e := newBridgeError(codeNodeUnreachable, "node "+owner+" did not answer")
		e.RetryAfterMS = unreachableRetry.Milliseconds()
		return e
	}
	if json.Unmarshal(msg.Data, out) != nil {
		return newBridgeError(codeInternal, "bad reply from node "+owner)
	}
	return nil
}

// onFwd serves a device message forwarded by another member. The owner
// re-checks the device and its scopes against its own registry, so an
// entry node with a stale (or wrong) view cannot grant more than the
// owner would.
func (b *deviceBridge) onFwd(m *nats.Msg) {
	if m.Reply == "" {
		return
	}
	respond := func(v any, id string) { _ = m.Respond(b.encode(b.mesh.Conn(), v, id)) }
	var env fwdEnvelope
	if json.Unmarshal(m.Data, &env) != nil {
		respond(errorResponse("", b.nodeID, newBridgeError(codeInternal, "malformed forward")), "")
		return
	}
	if env.V != wireVersion {
		resp := errorResponse("", b.nodeID, newBridgeError(codeInternal, "node "+b.nodeID+" does not speak this forward version"))
		resp.Versions = []int{wireVersion}
		respond(resp, "")
		return
	}
	d, ok := b.activeDevice(env.DeviceID)
	switch env.Kind {
	case fwdAPI:
		req, berr := parseRequest(env.Request)
		if berr == nil && !ok {
			berr = newBridgeError(codeDeviceRevoked, "device is revoked or unknown")
		}
		if berr != nil {
			respond(errorResponse(req.ID, b.nodeID, berr), req.ID)
			return
		}
		if !b.acquire(d.DeviceID) {
			respond(errorResponse(req.ID, b.nodeID, rateLimited()), req.ID)
			return
		}
		ctx := b.work(d.DeviceID)
		if !b.spawn(func() {
			defer b.release(d.DeviceID)
			resp, done := b.execute(ctx, d, req)
			if ctx.Err() != nil {
				// Revoked mid-request: tell the entry node rather than
				// leaving it to time out (it never relays this).
				resp = errorResponse(req.ID, b.nodeID, newBridgeError(codeDeviceRevoked, "device is revoked or unknown"))
			}
			respond(resp, req.ID)
			<-done
		}) {
			b.release(d.DeviceID)
			respond(errorResponse(req.ID, b.nodeID, unavailable("node is shutting down")), req.ID)
		}
	case fwdSub:
		var req streamRequest
		if json.Unmarshal(env.Request, &req) != nil {
			respond(streamFailure(req.ID, b.nodeID, newBridgeError(codeBadRequest, "malformed envelope")), req.ID)
			return
		}
		if !ok {
			respond(streamFailure(req.ID, b.nodeID, newBridgeError(codeDeviceRevoked, "device is revoked or unknown")), req.ID)
			return
		}
		// Never replay a resume buffer on the mesh dispatch goroutine.
		if !b.acquire(d.DeviceID) {
			respond(streamFailure(req.ID, b.nodeID, rateLimited()), req.ID)
			return
		}
		if !b.spawn(func() {
			defer b.release(d.DeviceID)
			respond(b.openStream(d, req), req.ID)
		}) {
			b.release(d.DeviceID)
			respond(streamFailure(req.ID, b.nodeID, unavailable("node is shutting down")), req.ID)
		}
	}
}

// execute runs req on this node's /v1 handler. It returns when the
// handler answered or the request timed out; done is closed when the
// handler has returned.
func (b *deviceBridge) execute(ctx context.Context, d pairing.Device, req bridgeRequest) (bridgeResponse, <-chan struct{}) {
	fail := func(e *bridgeError) (bridgeResponse, <-chan struct{}) {
		return errorResponse(req.ID, b.nodeID, e), closedDone
	}
	pattern, berr := b.check(d, req)
	if berr != nil {
		return fail(berr)
	}
	query, _ := parseQuery(req.Query)
	raw, _ := req.body()
	body, _ := applyBodyRules(pattern, d, raw)

	var entry *idemEntry
	key := ""
	if req.Method == http.MethodPost && req.IdempotencyKey != "" {
		key = req.IdempotencyKey
		e, leader, conflict := b.idem.begin(d.DeviceID, key, idemFingerprint(req.Method, req.Path, query.Encode(), raw))
		if conflict {
			return fail(newBridgeError(codeIdempotencyConflict, "idempotency_key was used for a different request"))
		}
		if !leader {
			return b.awaitReplay(ctx, req, e)
		}
		entry = e
	}
	forget := func() {
		if entry != nil {
			b.idem.finish(d.DeviceID, key, entry, bridgeResponse{}, false)
		}
	}

	timeout := req.timeout()
	hctx, cancel := context.WithTimeout(ctx, timeout+handlerGrace)
	hreq, err := http.NewRequestWithContext(hctx, req.Method, (&url.URL{Path: req.Path, RawQuery: query.Encode()}).String(), bytes.NewReader(body))
	if err != nil {
		cancel()
		forget()
		return fail(newBridgeError(codeBadRequest, "invalid request"))
	}
	if ct := req.contentType(); ct != "" {
		hreq.Header.Set("Content-Type", ct)
	}
	done := make(chan struct{})
	result := make(chan bridgeResponse, 1)
	if !b.spawn(func() {
		defer close(done)
		defer cancel()
		rec := &cappedRecorder{header: http.Header{}, limit: maxBridgeBody}
		b.local.ServeHTTP(rec, hreq)
		resp := b.build(req, rec)
		if entry != nil {
			// Only an answer the handler finished is remembered; a
			// revoked device's or a stopping node's work is forgotten.
			b.idem.finish(d.DeviceID, key, entry, resp, ctx.Err() == nil)
		}
		result <- resp
	}) {
		cancel()
		forget()
		return fail(unavailable("node is shutting down"))
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-result:
		return resp, done
	case <-timer.C:
		return errorResponse(req.ID, b.nodeID, newBridgeError(codeTimeout, "request timed out; it may still complete")), done
	case <-ctx.Done():
		return errorResponse(req.ID, b.nodeID, unavailable("request cancelled")), done
	}
}

// awaitReplay answers a retry of an idempotent request with the first
// attempt's response, waiting for it if it is still running.
func (b *deviceBridge) awaitReplay(ctx context.Context, req bridgeRequest, e *idemEntry) (bridgeResponse, <-chan struct{}) {
	timer := time.NewTimer(req.timeout())
	defer timer.Stop()
	select {
	case <-e.done:
	case <-timer.C:
		return errorResponse(req.ID, b.nodeID, newBridgeError(codeTimeout, "the first attempt is still running")), closedDone
	case <-ctx.Done():
		return errorResponse(req.ID, b.nodeID, unavailable("request cancelled")), closedDone
	}
	if !e.ok {
		return errorResponse(req.ID, b.nodeID, unavailable("the first attempt did not complete")), closedDone
	}
	resp := e.resp
	resp.ID, resp.Replayed = req.ID, true
	if req.wantsJSON() && len(resp.JSON) == 0 && resp.Body != "" {
		resp = rewrapJSON(resp)
	}
	return resp, closedDone
}

// rewrapJSON moves a base64 JSON body inline.
func rewrapJSON(resp bridgeResponse) bridgeResponse {
	body, err := base64.RawURLEncoding.DecodeString(resp.Body)
	if err == nil && isJSONType(resp.Headers["content-type"]) && json.Valid(body) {
		resp.JSON, resp.Body = body, ""
	}
	return resp
}

func isJSONType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// build turns a recorded handler response into a response envelope.
func (b *deviceBridge) build(req bridgeRequest, rec *cappedRecorder) bridgeResponse {
	resp := bridgeResponse{V: wireVersion, ID: req.ID, NodeID: b.nodeID}
	if rec.overflow {
		e := newBridgeError(codePayloadTooLarge, "response exceeds 4 MiB; paginate")
		resp.Status, resp.Error = e.status(), e
		return resp
	}
	resp.Status = rec.status
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	ct := rec.header.Get("Content-Type")
	if ct != "" {
		resp.Headers = map[string]string{"content-type": ct}
	}
	body := rec.body.Bytes()
	if req.wantsJSON() && isJSONType(ct) && json.Valid(body) {
		resp.JSON = bytes.TrimSpace(body)
	} else if len(body) > 0 {
		resp.Body = base64.RawURLEncoding.EncodeToString(body)
	}
	if resp.Status >= http.StatusBadRequest {
		var perr proto.Error
		_ = json.Unmarshal(body, &perr)
		resp.Error = &bridgeError{Code: codeForStatus(resp.Status), Message: perr.Message}
	}
	return resp
}

// cappedRecorder buffers a handler's response up to limit bytes and
// drops the rest, so a large answer cannot exhaust memory.
type cappedRecorder struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	limit    int
	overflow bool
}

func (r *cappedRecorder) Header() http.Header { return r.header }

func (r *cappedRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *cappedRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.overflow || r.body.Len()+len(p) > r.limit {
		r.overflow = true
		return 0, errResponseTooLarge
	}
	return r.body.Write(p)
}

// deviceStreams lists the streams a device has open on this node.
func (b *deviceBridge) deviceStreams(deviceID string) []*deviceStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*deviceStream
	for _, s := range b.streams {
		if s.deviceID == deviceID {
			out = append(out, s)
		}
	}
	return out
}

// closeDevice ends every stream and in-flight request of a device
// (revocation).
func (b *deviceBridge) closeDevice(deviceID string) {
	b.mu.Lock()
	if w := b.devices[deviceID]; w != nil {
		w.cancel()
		delete(b.devices, deviceID)
	}
	b.mu.Unlock()
	b.idem.forget(deviceID)
	for _, s := range b.deviceStreams(deviceID) {
		s.end(endRevoked)
	}
}

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
			b.publishAttention(a)
		}
	}
}

// attentionWire is the attention notice. It also carries the
// proto.AttentionEvent fields at the top level, so phase 2a clients that
// read the legacy broadcast as a bare AttentionEvent keep working.
type attentionWire struct {
	proto.AttentionEvent
	attentionNotice
}

const attentionType = "attention"

func (b *deviceBridge) publishAttention(a proto.AttentionEvent) {
	event, err := json.Marshal(a)
	if err != nil {
		return
	}
	n := attentionWire{AttentionEvent: a, attentionNotice: attentionNotice{
		V: wireVersion, Type: attentionType, At: wireNow(), Event: event,
	}}
	if a.SessionID != "" && b.swarmOf != nil {
		ctx, cancel := context.WithTimeout(b.ctx, time.Second)
		n.Swarm = b.swarmOf(ctx, a.WorkspaceID, a.SessionID)
		cancel()
	}
	blocked := a.Kind == proto.AttentionBlockedPermission || a.Kind == proto.AttentionBlockedQuestion
	if data, err := json.Marshal(n); err == nil {
		if err := b.nc.Publish(subjClientAttention, data); err != nil {
			slog.Warn("Failed to publish device attention", "error", err)
		}
	}
	for _, d := range b.p.reg.List() {
		if d.Revoked() || !slices.Contains(d.EffectiveScopes(), devicescope.SessionsRead) {
			continue
		}
		subj := subjClientRoot + d.DeviceID + "." + verbAttention
		if b.mesh != nil && !b.mesh.ClientsInterest(subj) {
			continue
		}
		n.Actionable = blocked && slices.Contains(d.EffectiveScopes(), devicescope.PermissionsAnswer)
		data, err := json.Marshal(n)
		if err != nil {
			continue
		}
		_ = b.nc.Publish(subj, data)
	}
}

// hasSession reports whether workspace workspaceID on this node has the
// session. An unknown workspace has no sessions.
func (b *deviceBridge) hasSession(ctx context.Context, workspaceID, sessionID string) (bool, error) {
	ws, err := b.p.srv.backend.GetWorkspace(workspaceID)
	switch {
	case errors.Is(err, backend.ErrWorkspaceNotFound):
		return false, nil
	case err != nil:
		return false, err
	case ws.App == nil || ws.Sessions == nil:
		return false, errors.New("workspace is not ready")
	}
	if _, err := ws.Sessions.Get(ctx, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// sessionAddress is a session's swarm address from its stored identity.
func (b *deviceBridge) sessionAddress(ctx context.Context, workspaceID, sessionID string) string {
	ws, err := b.p.srv.backend.GetWorkspace(workspaceID)
	if err != nil || ws.App == nil || ws.Sessions == nil {
		return ""
	}
	s, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil || s.Color == "" || s.Animal == "" {
		return ""
	}
	return swarm.FormatAddress(swarm.Identity{Color: s.Color, Animal: s.Animal}, sessionID)
}

// control publishes a control notice to one device and flushes it, so
// it is on the wire before whatever it announces takes effect.
func (b *deviceBridge) control(n controlNotice) {
	if b.publishControl(n) {
		_ = b.nc.Flush()
	}
}

func (b *deviceBridge) publishControl(n controlNotice) bool {
	n.V = wireVersion
	if n.At == "" {
		n.At = wireNow()
	}
	data, err := json.Marshal(n)
	if err != nil {
		return false
	}
	return b.nc.Publish(subjClientRoot+n.DeviceID+"."+verbControl, data) == nil
}

// revoked tells a device it was revoked and ends its streams and
// requests at once.
func (b *deviceBridge) revoked(d pairing.Device) {
	n := controlNotice{Type: controlRevoked, DeviceID: d.DeviceID, Reason: revokeReasonOperator, ByNode: d.UpdatedBy}
	if d.RevokedAt != nil {
		n.At = d.RevokedAt.UTC().Format(atFormat)
	}
	b.control(n)
	b.closeDevice(d.DeviceID)
}

// scopesChanged tells a device its permissions changed and ends streams
// it may no longer read.
func (b *deviceBridge) scopesChanged(d pairing.Device) {
	scopes := d.EffectiveScopes()
	b.control(controlNotice{Type: controlScopesChanged, DeviceID: d.DeviceID, Role: string(pairing.DefaultRole), Scopes: scopes})
	if slices.Contains(scopes, devicescope.SessionsRead) {
		return
	}
	for _, s := range b.deviceStreams(d.DeviceID) {
		s.end(endError)
	}
}

// reauth asks every active device to reconnect (trust changed).
func (b *deviceBridge) reauth(reason string) {
	// Publish to everyone, then flush once, off the caller's goroutine
	// (a store watcher).
	b.spawn(func() {
		for _, d := range b.p.reg.List() {
			if !d.Revoked() {
				b.publishControl(controlNotice{Type: controlReauth, DeviceID: d.DeviceID, Reason: reason})
			}
		}
		_ = b.nc.Flush()
	})
}
