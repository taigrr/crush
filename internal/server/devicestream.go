package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/pubsub"
)

const (
	maxStreamsPerDevice = 16
	defaultStreamCredit = 256
	maxStreamCredit     = 1024
	maxSSELine          = 4 << 20
	// maxStreamSessions bounds a stream's message → session map, used to
	// attribute (and filter) message deltas.
	maxStreamSessions = 4096
	streamOpenTimeout = 10 * time.Second
	// maxRingBytes bounds a stream's resume buffer in bytes as well as
	// frames, since one frame can be megabytes.
	maxRingBytes   = 4 << 20
	querySessionID = "session_id"
)

// streamTuning holds the stream timings (shortened by tests).
type streamTuning struct {
	// heartbeat is the longest a stream stays silent.
	heartbeat time.Duration
	// idle ends a stream nobody listens to; it is also how long a
	// detached stream stays resumable.
	idle time.Duration
	// blocked ends a stream held at zero credit.
	blocked time.Duration
	// tick is how often streams check interest and heartbeats.
	tick time.Duration
	// ring is how many recent frames are kept for resume.
	ring int
}

func defaultStreamTuning() streamTuning {
	return streamTuning{heartbeat: 15 * time.Second, idle: 60 * time.Second, blocked: 60 * time.Second, tick: 5 * time.Second, ring: 512}
}

// setTuning replaces the timings for streams opened afterwards.
func (b *deviceBridge) setTuning(t streamTuning) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tune = t
}

type ringFrame struct {
	seq  int64
	data []byte
}

// deviceStream is one event stream opened by a device. Frames are
// numbered from 0, never dropped or reordered; the last tune.ring
// frames are kept so a device that lost some can resume.
type deviceStream struct {
	b        *deviceBridge
	id       string
	deviceID string
	subject  string
	key      string
	session  string
	tune     streamTuning
	cancel   context.CancelFunc

	mu           sync.Mutex
	cond         *sync.Cond
	acked        int64
	credit       int64
	sent         int64
	ring         []ringFrame
	ringBytes    int
	lastFrame    time.Time
	lastInterest time.Time
	reason       string
	sessions     map[string]string
	sessionOrder []string
}

func newCond(s *deviceStream) *sync.Cond { return sync.NewCond(&s.mu) }

func streamFailure(id, node string, e *bridgeError) streamOpened {
	return streamOpened{V: wireVersion, ID: id, Status: e.status(), NodeID: node, Error: e}
}

// streamOwner is the node that opened a stream (stream IDs are
// "<node id>_<random>").
func streamOwner(id string) string {
	i := strings.LastIndexByte(id, '_')
	if i <= 0 {
		return ""
	}
	return id[:i]
}

func newStreamID(node string) string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return node + "_" + hex.EncodeToString(buf[:])
}

func (b *deviceBridge) onSub(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 5 || !replyOK(m, d.DeviceID) {
		return
	}
	var req streamRequest
	if json.Unmarshal(m.Data, &req) != nil {
		b.reply(m, streamFailure("", b.nodeID, newBridgeError(codeBadRequest, "malformed envelope")), "")
		return
	}
	// Opening a stream may resolve a swarm address and wait on another
	// node, so it takes a request slot like any bridged call.
	if !b.acquire(d.DeviceID) {
		b.reply(m, streamFailure(req.ID, b.nodeID, rateLimited()), req.ID)
		return
	}
	ctx := b.work(d.DeviceID)
	if !b.spawn(func() {
		defer b.release(d.DeviceID)
		opened := b.routeStream(ctx, d, req)
		if ctx.Err() == nil && b.active(d.DeviceID) {
			b.reply(m, opened, req.ID)
		}
	}) {
		b.release(d.DeviceID)
		b.reply(m, streamFailure(req.ID, b.nodeID, unavailable("node is shutting down")), req.ID)
	}
}

// routeStream checks a stream open on the entry node and opens it here
// or on the node that will run it (the session owner, or for a resume
// the node that opened the stream).
func (b *deviceBridge) routeStream(ctx context.Context, d pairing.Device, req streamRequest) streamOpened {
	fail := func(e *bridgeError) streamOpened { return streamFailure(req.ID, b.nodeID, e) }
	if req.V != 0 && req.V != wireVersion {
		return fail(newBridgeError(codeBadRequest, msgUnsupportedVersion))
	}
	if len(req.ID) > maxRequestID {
		return fail(newBridgeError(codeBadRequest, "id must be at most 128 bytes"))
	}
	pattern, berr := b.streamPattern(d, req.Path)
	if berr != nil {
		return fail(berr)
	}
	query, berr := parseQuery(req.Query)
	if berr != nil {
		return fail(berr)
	}
	var owner string
	if req.Resume != nil {
		owner = streamOwner(req.Resume.Stream)
		if owner == "" {
			return fail(newBridgeError(codeResumeUnavailable, "unknown stream"))
		}
		if _, berr := b.memberNode(owner); berr != nil {
			return fail(newBridgeError(codeResumeUnavailable, "unknown stream"))
		}
	} else {
		node := req.Node
		if node == "" {
			node = anyNode
		}
		var sessionID string
		owner, sessionID, berr = b.resolveNode(ctx, node, req.Target)
		if berr != nil {
			return fail(berr)
		}
		// A swarm target names one session: filter to it.
		if sessionID != "" && query.Get(querySessionID) == "" && pattern == routeWorkspaceEvents {
			query.Set(querySessionID, sessionID)
			raw, _ := json.Marshal(map[string][]string(query))
			req.Query = raw
		}
	}
	req.Target, req.Node = nil, ""
	if owner == b.nodeID {
		return b.openStream(d, req)
	}
	var opened streamOpened
	if berr := b.forward(ctx, d.DeviceID, owner, fwdSub, req, &opened, streamOpenTimeout); berr != nil {
		return fail(berr)
	}
	return opened
}

// streamPattern authorizes a stream path for d.
func (b *deviceBridge) streamPattern(d pairing.Device, p string) (string, *bridgeError) {
	pattern, berr := b.router.authorize(http.MethodGet, p, d.EffectiveScopes())
	if berr != nil {
		return "", berr
	}
	if !streamRoutes[pattern] {
		return "", newBridgeError(codeRouteNotAllowed, "not an event stream route")
	}
	return pattern, nil
}

// streamKey identifies what a stream reads, so a resume must ask for the
// same thing.
func streamKey(p string, q url.Values) string {
	return p + "?" + q.Encode()
}

// openStream opens or resumes a stream on this node. The owner re-checks
// the device's scopes here, whichever node the device asked.
func (b *deviceBridge) openStream(d pairing.Device, req streamRequest) streamOpened {
	fail := func(e *bridgeError) streamOpened { return streamFailure(req.ID, b.nodeID, e) }
	pattern, berr := b.streamPattern(d, req.Path)
	if berr != nil {
		return fail(berr)
	}
	query, berr := parseQuery(req.Query)
	if berr != nil {
		return fail(berr)
	}
	session := query.Get(querySessionID)
	// A filter for a session this node does not have would just be
	// silent; refuse it. Only the node that runs a workspace stream can
	// tell (global streams carry other nodes' sessions too).
	if session != "" && req.Resume == nil && pattern == routeWorkspaceEvents {
		if berr := b.checkSession(workspaceOf(req.Path), session); berr != nil {
			return fail(berr)
		}
	}
	keyQuery := maps.Clone(query)
	keyQuery.Del(querySessionID)
	key := streamKey(req.Path, keyQuery)
	credit := int64(req.Credit)
	if credit <= 0 {
		credit = defaultStreamCredit
	}
	credit = min(credit, maxStreamCredit)

	if req.Resume != nil {
		b.mu.Lock()
		s := b.streams[req.Resume.Stream]
		b.mu.Unlock()
		// A resume must read the same thing; it may omit a session filter
		// the entry node derived from a swarm target.
		if s == nil || s.deviceID != d.DeviceID || s.key != key || (session != "" && session != s.session) {
			return fail(newBridgeError(codeResumeUnavailable, "stream is gone"))
		}
		if !s.resume(req.Resume.AfterSeq, credit) {
			return fail(newBridgeError(codeResumeUnavailable, "frames after after_seq are no longer buffered"))
		}
		return streamOpened{V: wireVersion, ID: req.ID, Status: http.StatusOK, NodeID: b.nodeID, Stream: s.id, FirstSeq: req.Resume.AfterSeq + 1}
	}

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return fail(unavailable("node is shutting down"))
	}
	open := 0
	for _, s := range b.streams {
		if s.deviceID == d.DeviceID {
			open++
		}
	}
	if open >= maxStreamsPerDevice {
		b.mu.Unlock()
		e := rateLimited()
		e.Message = "too many open streams"
		return fail(e)
	}
	id := newStreamID(b.nodeID)
	ctx, cancel := context.WithCancel(b.ctx)
	now := time.Now()
	s := &deviceStream{
		b: b, id: id, deviceID: d.DeviceID, key: key, tune: b.tune, cancel: cancel, credit: credit,
		subject:   subjClientRoot + d.DeviceID + "." + verbEvents + "." + id,
		lastFrame: now, lastInterest: now, sessions: map[string]string{},
	}
	s.session = session
	s.cond = newCond(s)
	b.streams[id] = s
	b.mu.Unlock()

	handlerQuery := maps.Clone(query)
	handlerQuery.Del(querySessionID)
	// Each stream is its own client of the workspace.
	handlerQuery.Set("client_id", uuid.NewString())
	if !b.spawn(func() {
		defer b.closeStream(id)
		b.runStream(ctx, s, req.Path, handlerQuery)
	}) {
		b.closeStream(id)
		return fail(unavailable("node is shutting down"))
	}
	return streamOpened{V: wireVersion, ID: req.ID, Status: http.StatusOK, NodeID: b.nodeID, Stream: id}
}

// workspaceOf is the {id} of a /v1/workspaces/{id}/... path.
func workspaceOf(p string) string {
	rest, _ := strings.CutPrefix(p, "/v1/workspaces/")
	id, _, _ := strings.Cut(rest, "/")
	return id
}

// checkSession reports whether a workspace on this node has the session.
func (b *deviceBridge) checkSession(workspaceID, sessionID string) *bridgeError {
	if b.sessionExists == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(b.ctx, streamOpenTimeout)
	defer cancel()
	ok, err := b.sessionExists(ctx, workspaceID, sessionID)
	switch {
	case err != nil:
		return newBridgeError(codeInternal, "cannot look up the session")
	case !ok:
		return newBridgeError(codeNotFound, "no such session in this workspace")
	}
	return nil
}

// stream returns the device's stream id on this node, or nil.
func (b *deviceBridge) stream(deviceID, id string) *deviceStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.streams[id]
	if s == nil || s.deviceID != deviceID {
		return nil
	}
	return s
}

func (b *deviceBridge) onUnsub(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 5 {
		return
	}
	var req unsubRequest
	if json.Unmarshal(m.Data, &req) != nil {
		return
	}
	if s := b.stream(d.DeviceID, req.Stream); s != nil {
		s.end(endClosed)
	}
	// Every node sees an unsubscribe; only the stream's node answers (or
	// any node, for a stream ID no member owns, since closing is
	// idempotent).
	if m.Reply == "" || !replyOK(m, d.DeviceID) {
		return
	}
	owner := streamOwner(req.Stream)
	if owner != b.nodeID {
		if _, berr := b.memberNode(owner); owner != "" && berr == nil {
			return
		}
	}
	b.reply(m, bridgeResponse{V: wireVersion, ID: req.ID, Status: http.StatusOK, NodeID: b.nodeID}, req.ID)
}

func (b *deviceBridge) onAck(m *nats.Msg) {
	d, tokens, ok := b.device(m)
	if !ok || len(tokens) != 6 {
		return
	}
	var ack streamAck
	if json.Unmarshal(m.Data, &ack) != nil {
		return
	}
	s := b.stream(d.DeviceID, tokens[5])
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
	s.lastInterest = time.Now()
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (b *deviceBridge) closeStream(id string) {
	b.mu.Lock()
	s := b.streams[id]
	delete(b.streams, id)
	b.mu.Unlock()
	if s != nil {
		s.end(endClosed)
	}
}

// end stops the stream with reason (the first reason wins).
func (s *deviceStream) end(reason string) {
	s.mu.Lock()
	if s.reason == "" {
		s.reason = reason
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	s.cancel()
}

// errFrameTooLarge reports an event that cannot fit in one NATS message.
var errFrameTooLarge = errors.New("stream frame exceeds the payload limit")

// encodeFrame stamps and encodes a frame, refusing one that could not be
// published (it would leave a gap no resume could fill).
func (s *deviceStream) encodeFrame(f streamFrame) ([]byte, error) {
	f.V, f.Stream, f.At = wireVersion, s.id, wireNow()
	data, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.b.nc.MaxPayload() {
		return nil, errFrameTooLarge
	}
	return data, nil
}

// publishLocked sends one encoded frame. Callers hold s.mu, which keeps
// frames (live, replayed, heartbeats) in order; publishing only buffers.
func (s *deviceStream) publishLocked(data []byte) {
	if s.b.nc.Publish(s.subject, data) == nil {
		s.lastFrame = time.Now()
	}
}

// controlFrameLocked sends a heartbeat or end frame (they repeat the last
// event seq and consume no credit).
func (s *deviceStream) controlFrameLocked(kind, reason string) {
	if data, err := s.encodeFrame(streamFrame{Seq: s.sent - 1, Type: kind, Reason: reason}); err == nil {
		s.publishLocked(data)
	}
}

// resume replays the frames after afterSeq and treats them as unacked.
func (s *deviceStream) resume(afterSeq, credit int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason != "" || afterSeq < -1 || afterSeq >= s.sent {
		return false
	}
	oldest := s.sent - int64(len(s.ring))
	if afterSeq+1 < oldest {
		return false
	}
	for _, f := range s.ring {
		if f.seq > afterSeq {
			s.publishLocked(f.data)
		}
	}
	s.acked = max(s.acked, afterSeq+1)
	s.credit = credit
	s.lastInterest = time.Now()
	s.cond.Broadcast()
	return true
}

// remember keeps f for resume, dropping the oldest frames beyond
// tune.ring frames or maxRingBytes.
func (s *deviceStream) remember(f ringFrame) {
	if s.tune.ring <= 0 {
		return
	}
	s.ring = append(s.ring, f)
	s.ringBytes += len(f.data)
	drop := 0
	for len(s.ring)-drop > s.tune.ring || (s.ringBytes > maxRingBytes && len(s.ring)-drop > 1) {
		s.ringBytes -= len(s.ring[drop].data)
		s.ring[drop] = ringFrame{}
		drop++
	}
	if drop > 0 {
		s.ring = slices.Clone(s.ring[drop:])
	}
}

// runStream runs the SSE handler in-process and publishes each event as a
// frame. It stops when the device unsubscribes, stops listening, is
// revoked, or holds the stream at zero credit for too long, and always
// ends with an "end" frame.
func (b *deviceBridge) runStream(ctx context.Context, s *deviceStream, p string, query url.Values) {
	pr, pw := io.Pipe()
	w := &streamWriter{header: http.Header{}, w: pw}
	defer s.finish(w)
	// Unblock the scanner when the stream is cancelled, and the handler
	// (blocked writing to the pipe) whenever this function returns.
	context.AfterFunc(ctx, func() { _ = pr.CloseWithError(context.Canceled) })
	defer pr.CloseWithError(io.ErrClosedPipe)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, (&url.URL{Path: p, RawQuery: query.Encode()}).String(), nil)
	if err != nil {
		return
	}
	if !b.spawn(func() {
		b.local.ServeHTTP(w, req)
		_ = pw.Close()
	}) {
		return
	}
	if !b.spawn(func() { s.watch(ctx) }) {
		s.end(endNodeShutdown)
		return
	}

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), maxSSELine)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		s.mu.Lock()
		sid, scoped := s.sessionOf(data)
		s.mu.Unlock()
		// A filtered stream drops other sessions' events, and session
		// events it cannot attribute (a delta whose message it never saw).
		if s.session != "" && sid != s.session && (sid != "" || scoped) {
			continue
		}
		if !s.waitCredit(ctx) {
			return
		}
		s.mu.Lock()
		if s.reason != "" {
			s.mu.Unlock()
			return
		}
		frame, err := s.encodeFrame(streamFrame{Seq: s.sent, Type: frameEvent, SessionID: sid, Data: data})
		if err != nil {
			s.mu.Unlock()
			s.end(endError)
			return
		}
		s.remember(ringFrame{seq: s.sent, data: frame})
		s.sent++
		s.publishLocked(frame)
		s.mu.Unlock()
	}
}

// finish sends the stream's end frame.
func (s *deviceStream) finish(w *streamWriter) {
	s.mu.Lock()
	reason := s.reason
	switch {
	case reason != "":
	case s.b.ctx.Err() != nil:
		reason = endNodeShutdown
	case w.statusCode() != 0 && w.statusCode() != http.StatusOK:
		reason = endError
	default:
		reason = endClosed
	}
	if s.reason == "" {
		s.reason = reason
	}
	s.cond.Broadcast()
	s.controlFrameLocked(frameEnd, reason)
	s.mu.Unlock()
	// Flush outside the lock: it waits on the server, and acks for every
	// stream share one dispatch goroutine.
	_ = s.b.nc.Flush()
}

// waitCredit blocks while the device has too many unacknowledged frames,
// ending the stream after tune.blocked.
func (s *deviceStream) waitCredit(ctx context.Context) bool {
	timer := time.AfterFunc(s.tune.blocked, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer timer.Stop()
	deadline := time.Now().Add(s.tune.blocked)
	s.mu.Lock()
	for s.sent-s.acked >= s.credit && s.reason == "" {
		if ctx.Err() != nil {
			s.mu.Unlock()
			return false
		}
		if !time.Now().Before(deadline) {
			s.mu.Unlock()
			s.end(endCreditTimeout)
			return false
		}
		s.cond.Wait()
	}
	ok := s.reason == "" && ctx.Err() == nil
	s.mu.Unlock()
	return ok
}

// watch ends a stream once its device is revoked or nobody has listened
// for tune.idle, and keeps it alive with heartbeats.
func (s *deviceStream) watch(ctx context.Context) {
	tick := time.NewTicker(s.tune.tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if !s.b.active(s.deviceID) {
			s.end(endRevoked)
			return
		}
		now := time.Now()
		interest := s.b.mesh == nil || s.b.mesh.ClientsInterest(s.subject)
		s.mu.Lock()
		if interest {
			s.lastInterest = now
		}
		idle := now.Sub(s.lastInterest) > s.tune.idle
		if !idle && now.Sub(s.lastFrame) >= s.tune.heartbeat {
			s.controlFrameLocked(frameHeartbeat, "")
		}
		s.mu.Unlock()
		if idle {
			s.end(endIdle)
			return
		}
	}
}

// sessionOf returns the session an SSE payload concerns, or "", and
// whether the payload is about a session at all (a message delta always
// is, even when its message is unknown). Message deltas carry only a
// message ID, so the stream remembers which session each message it has
// seen belongs to. Callers hold s.mu.
func (s *deviceStream) sessionOf(data string) (string, bool) {
	var env struct {
		Type    pubsub.PayloadType `json:"type"`
		Payload json.RawMessage    `json:"payload"`
	}
	if json.Unmarshal([]byte(data), &env) != nil {
		return "", true
	}
	if env.Type == pubsub.PayloadTypeMessageDelta {
		var delta struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(env.Payload, &delta)
		return s.sessions[delta.ID], true
	}
	var inner struct {
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(env.Payload, &inner) != nil {
		return "", !globalPayloads[env.Type]
	}
	var p struct {
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Message   struct {
			SessionID string `json:"session_id"`
		} `json:"message"`
	}
	_ = json.Unmarshal(inner.Payload, &p)
	switch env.Type {
	case pubsub.PayloadTypeSession:
		return p.ID, true
	case pubsub.PayloadTypeMessage:
		if p.ID != "" && p.SessionID != "" {
			s.rememberSession(p.ID, p.SessionID)
		}
		return p.SessionID, true
	}
	if p.SessionID != "" {
		return p.SessionID, true
	}
	if p.Message.SessionID != "" {
		return p.Message.SessionID, true
	}
	// Only known workspace-wide events pass a session filter
	// unattributed; anything else is treated as some other session's.
	return "", !globalPayloads[env.Type]
}

// globalPayloads are the stream events that concern no session.
var globalPayloads = map[pubsub.PayloadType]bool{
	pubsub.PayloadTypeLSPEvent: true, pubsub.PayloadTypeMCPEvent: true, pubsub.PayloadTypeSkillEvent: true,
	pubsub.PayloadTypeSkillsEvent: true, pubsub.PayloadTypeConfigChanged: true,
}

func (s *deviceStream) rememberSession(messageID, sessionID string) {
	if _, ok := s.sessions[messageID]; ok {
		return
	}
	if len(s.sessionOrder) >= maxStreamSessions {
		delete(s.sessions, s.sessionOrder[0])
		s.sessionOrder = slices.Delete(s.sessionOrder, 0, 1)
	}
	s.sessions[messageID] = sessionID
	s.sessionOrder = append(s.sessionOrder, messageID)
}

// streamWriter is an in-process http.ResponseWriter for SSE handlers.
type streamWriter struct {
	header http.Header
	w      io.Writer
	mu     sync.Mutex
	status int
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = status
	}
}

func (w *streamWriter) statusCode() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.statusCode() != http.StatusOK {
		return len(p), nil
	}
	return w.w.Write(p)
}

func (w *streamWriter) Flush() {}
