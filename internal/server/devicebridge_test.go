package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/swarm"
)

const deviceWait = 10 * time.Second

// fakeDevice is a paired phone speaking the v1 wire format over the
// device websocket, as rush does.
type fakeDevice struct {
	t         *testing.T
	id        string
	nc        *nats.Conn
	closed    chan struct{}
	frames    chan streamFrame
	control   chan controlNotice
	attention chan map[string]any
	legacy    chan map[string]any
}

func newFakeDevice(t *testing.T, id string, connect func(...nats.Option) (*nats.Conn, error)) *fakeDevice {
	t.Helper()
	f := &fakeDevice{
		t: t, id: id, closed: make(chan struct{}),
		frames: make(chan streamFrame, 256), control: make(chan controlNotice, 16),
		attention: make(chan map[string]any, 16), legacy: make(chan map[string]any, 16),
	}
	nc, err := connect(nats.ClosedHandler(func(*nats.Conn) { close(f.closed) }))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	f.nc = nc
	// Subscribe to everything addressed to the device before any request,
	// as the spec requires.
	_, err = nc.Subscribe(subjClientRoot+id+".>", func(m *nats.Msg) {
		switch strings.Split(m.Subject, ".")[4] {
		case verbEvents:
			var fr streamFrame
			if json.Unmarshal(m.Data, &fr) == nil {
				f.frames <- fr
			}
		case verbControl:
			var n controlNotice
			if json.Unmarshal(m.Data, &n) == nil {
				f.control <- n
			}
		case verbAttention:
			var a map[string]any
			if json.Unmarshal(m.Data, &a) == nil {
				f.attention <- a
			}
		}
	})
	require.NoError(t, err)
	_, err = nc.Subscribe(subjClientAttention, func(m *nats.Msg) {
		var a map[string]any
		if json.Unmarshal(m.Data, &a) == nil {
			f.legacy <- a
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())
	return f
}

func (f *fakeDevice) request(subject string, v, out any) {
	f.t.Helper()
	data, err := json.Marshal(v)
	require.NoError(f.t, err)
	msg, err := f.nc.Request(subject, data, deviceWait)
	require.NoError(f.t, err, subject)
	require.NoError(f.t, json.Unmarshal(msg.Data, out), string(msg.Data))
}

func (f *fakeDevice) call(node string, req bridgeRequest) bridgeResponse {
	f.t.Helper()
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.V == 0 {
		req.V = wireVersion
	}
	var resp bridgeResponse
	f.request(subjClientRoot+f.id+"."+verbAPI+"."+node, req, &resp)
	require.Equal(f.t, req.ID, resp.ID)
	return resp
}

func (f *fakeDevice) get(p string) bridgeResponse {
	f.t.Helper()
	return f.call(anyNode, bridgeRequest{Method: http.MethodGet, Path: p})
}

func (f *fakeDevice) post(p string, body any) bridgeResponse {
	f.t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(f.t, err)
	return f.call(anyNode, bridgeRequest{Method: http.MethodPost, Path: p, JSON: raw})
}

func (f *fakeDevice) sub(req streamRequest) streamOpened {
	f.t.Helper()
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	req.V = wireVersion
	var opened streamOpened
	f.request(subjClientRoot+f.id+"."+verbSub, req, &opened)
	return opened
}

func (f *fakeDevice) ack(stream string, seq int64, credit int) {
	f.t.Helper()
	data, _ := json.Marshal(streamAck{V: wireVersion, Stream: stream, Seq: seq, Credit: credit})
	require.NoError(f.t, f.nc.Publish(subjClientRoot+f.id+"."+verbAck+"."+stream, data))
}

// frame waits for the next frame of stream matching want.
func (f *fakeDevice) frame(stream string, want func(streamFrame) bool) streamFrame {
	f.t.Helper()
	deadline := time.After(deviceWait)
	for {
		select {
		case fr := <-f.frames:
			if fr.Stream == stream && (want == nil || want(fr)) {
				return fr
			}
		case <-deadline:
			f.t.Fatalf("no matching frame on stream %s", stream)
		}
	}
}

func (f *fakeDevice) waitControl(kind string) controlNotice {
	f.t.Helper()
	deadline := time.After(deviceWait)
	for {
		select {
		case n := <-f.control:
			if n.Type == kind {
				return n
			}
		case <-deadline:
			f.t.Fatalf("no %s control notice", kind)
		}
	}
}

func isolateHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

// newBridgeWorkspace creates a real workspace with two sessions.
func newBridgeWorkspace(t *testing.T, srv *Server, c *client.Client) (*proto.Workspace, *backend.Workspace, *proto.Session, *proto.Session) {
	t.Helper()
	ctx := t.Context()
	ws, err := c.CreateWorkspace(ctx, proto.Workspace{Path: t.TempDir(), ClientID: "c1"})
	require.NoError(t, err)
	// Close the workspace databases before the temp dirs go (Windows
	// cannot delete open files).
	t.Cleanup(func() {
		srv.backend.ShutdownWorkspaces()
		db.ResetPool()
	})
	a, err := c.CreateSession(ctx, ws.ID, "alpha")
	require.NoError(t, err)
	b, err := c.CreateSession(ctx, ws.ID, "beta")
	require.NoError(t, err)
	bws, err := srv.backend.GetWorkspace(ws.ID)
	require.NoError(t, err)
	return ws, bws, a, b
}

// setDeviceRole rewrites a device record on one node only (as if that
// node had a different view of it).
func setDeviceRole(t *testing.T, srv *Server, id string, role pairing.Role) {
	t.Helper()
	d, ok := srv.pairing.reg.Get(id)
	require.True(t, ok)
	d.Role, d.Scopes, d.Rev, d.UpdatedAt = role, role.Scopes(), d.Rev+1, time.Now().UTC()
	changed, err := srv.pairing.reg.Merge(d)
	require.NoError(t, err)
	require.True(t, changed)
}

func TestDeviceBridgeEndToEnd(t *testing.T) {
	isolateHome(t)
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	b := srv.pairing.bridge.Load()
	b.setTuning(streamTuning{heartbeat: 300 * time.Millisecond, idle: 30 * time.Second, blocked: 30 * time.Second, tick: 100 * time.Millisecond, ring: 512})
	dev := newFakeDevice(t, ph.id, func(o ...nats.Option) (*nats.Conn, error) { return ph.connect(t, o...) })
	ws, bws, alpha, beta := newBridgeWorkspace(t, srv, c)
	base := "/v1/workspaces/" + ws.ID

	t.Run("info", func(t *testing.T) {
		var info infoReply
		dev.request(subjClientRoot+ph.id+"."+verbInfo, map[string]any{"v": 1, "client": "test"}, &info)
		require.Equal(t, wireVersion, info.V)
		require.Equal(t, ph.nodeID, info.NodeID)
		require.Equal(t, ph.id, info.DeviceID)
		require.Equal(t, string(pairing.RoleAdmin), info.Role, "every device reports full access")
		require.ElementsMatch(t, pairing.AllScopes(), info.Scopes)
		require.Subset(t, info.Features, bridgeFeatures)
		require.Equal(t, maxBridgeBody, info.Limits.MaxRequestBytes)
		require.Equal(t, 512, info.Limits.ResumeBufferFrames)
	})

	t.Run("list sessions", func(t *testing.T) {
		resp := dev.call(anyNode, bridgeRequest{Method: http.MethodGet, Path: base + "/sessions", Query: json.RawMessage(`{"limit":"50"}`)})
		require.Equal(t, http.StatusOK, resp.Status, resp.Error)
		require.Equal(t, ph.nodeID, resp.NodeID)
		require.Equal(t, "application/json", resp.Headers["content-type"])
		require.Empty(t, resp.Body, "v1 replies inline JSON")
		var sessions []proto.Session
		require.NoError(t, json.Unmarshal(resp.JSON, &sessions))
		require.Len(t, sessions, 2)

		// A phase 2a envelope (no v, string query, base64 body) still works.
		data, _ := json.Marshal(map[string]any{"id": "legacy", "method": "GET", "path": base + "/sessions", "query": "limit=5"})
		msg, err := dev.nc.Request(subjClientRoot+ph.id+".api.any", data, deviceWait)
		require.NoError(t, err)
		var legacy bridgeResponse
		require.NoError(t, json.Unmarshal(msg.Data, &legacy))
		require.Equal(t, http.StatusOK, legacy.Status)
		raw, err := base64.RawURLEncoding.DecodeString(legacy.Body)
		require.NoError(t, err)
		require.True(t, json.Valid(raw))
	})

	t.Run("refusals", func(t *testing.T) {
		resp := dev.post(base+"/config/provider-key", map[string]string{"key": "x"})
		require.Equal(t, http.StatusForbidden, resp.Status)
		require.Equal(t, codeRouteNotAllowed, resp.Error.Code, "never routes are unreachable")
		resp = dev.post("/v1/sessions/batch", map[string]any{"op": "archive"})
		require.Equal(t, codeRouteNotAllowed, resp.Error.Code, "never routes stay blocked for full-access devices")
		require.Equal(t, codeRouteNotAllowed, dev.post(base+"/config/remove", map[string]string{"key": "x"}).Error.Code)
		require.Equal(t, codeRouteNotAllowed, dev.get(base+"/embeddings/status").Error.Code)
		require.Equal(t, codeRouteNotAllowed, dev.get("/v1/cluster/token").Error.Code)
		require.Equal(t, codeNotFound, dev.get("/v1/nodes/"+ph.nodeID+"/v1/workspaces").Error.Code)
		require.Equal(t, codeRouteNotAllowed, dev.get(base+"/events").Error.Code, "streams only through sub")
		require.Equal(t, codeMethodNotAllowed, dev.call(anyNode, bridgeRequest{Method: "DELETE", Path: base}).Error.Code)
		require.Equal(t, codeBadRequest, dev.get("/v1/workspaces/"+ws.ID+"/../../cluster/token").Error.Code)
		require.Equal(t, codeNodeNotFound, dev.call("no-such-node", bridgeRequest{Method: "GET", Path: "/v1/workspaces"}).Error.Code)
		require.Equal(t, codeBadRequest, dev.call(ph.nodeID, bridgeRequest{Method: "GET", Path: "/v1/workspaces", Target: &bridgeTarget{Node: ph.nodeID}}).Error.Code)
		require.Equal(t, codeBadRequest, dev.call(anyNode, bridgeRequest{Method: "GET", Path: "/v1/workspaces", JSON: json.RawMessage(`{}`)}).Error.Code)
		require.Equal(t, codeBadRequest, dev.call(anyNode, bridgeRequest{Method: "POST", Path: base + "/agent", JSON: json.RawMessage(`{}`), Body: "e30"}).Error.Code)
		resp = dev.call(anyNode, bridgeRequest{V: 7, Method: "GET", Path: "/v1/workspaces"})
		require.Equal(t, codeBadRequest, resp.Error.Code)
		require.Equal(t, []int{wireVersion}, resp.Versions)
		var bad bridgeResponse
		dev.request(subjClientRoot+ph.id+".api.any", "not an envelope", &bad)
		require.Equal(t, codeBadRequest, bad.Error.Code)
		// A /v1 error keeps the handler's status and gets a code.
		resp = dev.get("/v1/workspaces/" + uuid.NewString())
		require.Equal(t, http.StatusNotFound, resp.Status)
		require.Equal(t, codeNotFound, resp.Error.Code)
	})

	t.Run("send prompt", func(t *testing.T) {
		coord := newScriptedCoordinator(bws.App)
		bws.AgentCoordinator = coord
		t.Cleanup(coord.CancelAll)
		req := bridgeRequest{Method: http.MethodPost, Path: base + "/agent", IdempotencyKey: "p-1", JSON: mustJSON(t, proto.AgentMessage{SessionID: alpha.ID, Prompt: "run the tests"})}
		resp := dev.call(anyNode, req)
		require.Equal(t, http.StatusAccepted, resp.Status, resp.Error)
		select {
		case <-coord.entered:
		case <-time.After(deviceWait):
			t.Fatal("prompt never reached the agent")
		}
		// A retry with the same key replays instead of prompting again.
		again := dev.call(anyNode, req)
		require.True(t, again.Replayed)
		require.Equal(t, resp.Status, again.Status)
		select {
		case <-coord.entered:
			t.Fatal("an idempotent retry ran the prompt twice")
		case <-time.After(300 * time.Millisecond):
		}
		req.JSON = mustJSON(t, proto.AgentMessage{SessionID: alpha.ID, Prompt: "something else"})
		require.Equal(t, codeIdempotencyConflict, dev.call(anyNode, req).Error.Code)
		// Every device has full access, so it may set a turn's policy.
		resp = dev.post(base+"/agent", proto.AgentMessage{SessionID: alpha.ID, Prompt: "x", PermissionPolicy: proto.PermissionPolicyAllow})
		if resp.Error != nil {
			require.NotEqual(t, codeDeviceScopeDenied, resp.Error.Code)
		}
	})

	t.Run("message deltas and resume", func(t *testing.T) {
		opened := dev.sub(streamRequest{Path: base + "/events", Query: json.RawMessage(`{"wire":"2","session_id":"` + alpha.ID + `"}`), Credit: 64})
		require.Equal(t, http.StatusOK, opened.Status, opened.Error)
		require.True(t, strings.HasPrefix(opened.Stream, ph.nodeID+"_"))
		dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameHeartbeat })

		ctx := t.Context()
		_, err := bws.Messages.Create(ctx, beta.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "other session"}}})
		require.NoError(t, err)
		_, err = bws.Messages.Create(ctx, alpha.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello alpha"}}})
		require.NoError(t, err)
		first := dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameEvent && strings.Contains(f.Data, "hello alpha") })
		require.Equal(t, alpha.ID, first.SessionID)
		_, err = bws.Messages.Create(ctx, alpha.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "second"}}})
		require.NoError(t, err)
		second := dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameEvent && strings.Contains(f.Data, "second") })
		require.Greater(t, second.Seq, first.Seq)
		dev.ack(opened.Stream, second.Seq, 64)

		// Every frame was for alpha.
		s := b.stream(ph.id, opened.Stream)
		require.NotNil(t, s)
		s.mu.Lock()
		for _, f := range s.ring {
			var fr streamFrame
			require.NoError(t, json.Unmarshal(f.data, &fr))
			require.Equal(t, alpha.ID, fr.SessionID)
			require.NotContains(t, fr.Data, "other session")
		}
		s.mu.Unlock()

		// Resume replays everything after first.Seq on the same stream.
		resumed := dev.sub(streamRequest{Path: base + "/events", Query: json.RawMessage(`{"wire":"2","session_id":"` + alpha.ID + `"}`), Resume: &streamResume{Stream: opened.Stream, AfterSeq: first.Seq}})
		require.Equal(t, http.StatusOK, resumed.Status, resumed.Error)
		require.Equal(t, opened.Stream, resumed.Stream)
		require.Equal(t, first.Seq+1, resumed.FirstSeq)
		replayed := dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameEvent && f.Seq == first.Seq+1 })
		require.Contains(t, replayed.Data, "second")

		// A different query cannot resume it, nor can a forgotten seq.
		bad := dev.sub(streamRequest{Path: base + "/events", Query: json.RawMessage(`{"wire":"1"}`), Resume: &streamResume{Stream: opened.Stream, AfterSeq: first.Seq}})
		require.Equal(t, codeResumeUnavailable, bad.Error.Code)
		bad = dev.sub(streamRequest{Path: base + "/events", Resume: &streamResume{Stream: ph.nodeID + "_feedface", AfterSeq: 0}})
		require.Equal(t, codeResumeUnavailable, bad.Error.Code)

		var closed bridgeResponse
		dev.request(subjClientRoot+ph.id+"."+verbUnsub, unsubRequest{V: 1, ID: "u1", Stream: opened.Stream}, &closed)
		require.Equal(t, http.StatusOK, closed.Status)
		end := dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameEnd })
		require.Equal(t, endClosed, end.Reason)
		require.Eventually(t, func() bool { return b.stream(ph.id, opened.Stream) == nil }, deviceWait, 20*time.Millisecond)

		require.Equal(t, codeRouteNotAllowed, dev.sub(streamRequest{Path: base + "/sessions"}).Error.Code)

		// A filter for a session the workspace does not have is refused,
		// not left silent; so is an unknown workspace.
		unknown := dev.sub(streamRequest{Path: base + "/events", Query: json.RawMessage(`{"session_id":"` + uuid.NewString() + `"}`)})
		require.Equal(t, http.StatusNotFound, unknown.Status)
		require.Equal(t, codeNotFound, unknown.Error.Code)
		unknown = dev.sub(streamRequest{Path: "/v1/workspaces/" + uuid.NewString() + "/events", Query: json.RawMessage(`{"session_id":"` + alpha.ID + `"}`)})
		require.Equal(t, codeNotFound, unknown.Error.Code)
	})

	t.Run("permission answer and attention", func(t *testing.T) {
		granted := make(chan bool, 1)
		go func() {
			ok, _ := bws.Permissions.Request(context.Background(), permission.CreatePermissionRequest{
				SessionID: alpha.ID, ToolCallID: "call_9", ToolName: "bash", Action: "execute", Description: "echo hi", Path: bws.Path,
			})
			granted <- ok
		}()
		deadline := time.After(deviceWait)
		var att map[string]any
		for att == nil {
			select {
			case a := <-dev.attention:
				if a["kind"] == string(proto.AttentionBlockedPermission) {
					att = a
				}
			case <-deadline:
				t.Fatal("no per-device attention event")
			}
		}
		require.Equal(t, attentionType, att["type"])
		require.Equal(t, true, att["actionable"])
		event := att["event"].(map[string]any)
		require.Equal(t, alpha.ID, event["session_id"])
		require.Equal(t, ph.nodeID, event["node_id"])
		select {
		case legacy := <-dev.legacy:
			require.NotEmpty(t, legacy["kind"], "the legacy broadcast is still a bare AttentionEvent")
		case <-time.After(deviceWait):
			t.Fatal("no legacy attention broadcast")
		}

		var pending proto.PendingPrompts
		require.Eventually(t, func() bool {
			resp := dev.get(base + "/sessions/" + alpha.ID + "/pending")
			return resp.Status == http.StatusOK && json.Unmarshal(resp.JSON, &pending) == nil && len(pending.Permissions) == 1
		}, deviceWait, 50*time.Millisecond)
		resp := dev.call(anyNode, bridgeRequest{
			Method: http.MethodPost, Path: base + "/permissions/grant", IdempotencyKey: "g-call_9",
			JSON: mustJSON(t, proto.PermissionGrant{Action: proto.PermissionAllow, Permission: pending.Permissions[0]}),
		})
		require.Equal(t, http.StatusOK, resp.Status, resp.Error)
		select {
		case ok := <-granted:
			require.True(t, ok)
		case <-time.After(deviceWait):
			t.Fatal("permission never resolved")
		}
	})

	t.Run("reply subject spoofing", func(t *testing.T) {
		other := pairPhone(t, srv, c)
		on, err := other.connect(t)
		require.NoError(t, err)
		defer on.Close()
		stolen := make(chan *nats.Msg, 1)
		_, err = on.Subscribe("_INBOX."+other.id+".>", func(m *nats.Msg) { stolen <- m })
		require.NoError(t, err)
		require.NoError(t, on.Flush())
		req, _ := json.Marshal(bridgeRequest{V: 1, ID: "x", Method: "GET", Path: "/v1/workspace-overviews"})
		for _, verb := range []string{"api.any", verbInfo, verbSub} {
			require.NoError(t, dev.nc.PublishRequest(subjClientRoot+ph.id+"."+verb, "_INBOX."+other.id+".steal", req))
		}
		require.NoError(t, dev.nc.Flush())
		select {
		case <-stolen:
			t.Fatal("bridge answered into another device's inbox")
		case <-time.After(500 * time.Millisecond):
		}
	})

	t.Run("role edits change nothing", func(t *testing.T) {
		setDeviceRole(t, srv, ph.id, pairing.RoleViewer)
		select {
		case n := <-dev.control:
			require.NotEqual(t, controlScopesChanged, n.Type, "no scopes_changed for a role edit")
		case <-time.After(300 * time.Millisecond):
		}
		var info infoReply
		dev.request(subjClientRoot+ph.id+"."+verbInfo, map[string]any{"v": 1}, &info)
		require.Equal(t, string(pairing.RoleAdmin), info.Role)
		require.ElementsMatch(t, pairing.AllScopes(), info.Scopes, "a stored viewer record still has full access")
		resp := dev.post(base+"/sessions", proto.Session{Title: "from a viewer record"})
		require.Less(t, resp.Status, 300, resp.Error)
	})

	t.Run("revoked notice then disconnect", func(t *testing.T) {
		opened := dev.sub(streamRequest{Path: "/v1/events"})
		require.Equal(t, http.StatusOK, opened.Status, opened.Error)
		_, err := c.RevokeDevice(t.Context(), ph.id)
		require.NoError(t, err)
		n := dev.waitControl(controlRevoked)
		require.Equal(t, ph.id, n.DeviceID)
		require.Equal(t, revokeReasonOperator, n.Reason)
		require.Equal(t, ph.nodeID, n.ByNode)
		require.NotEmpty(t, n.At)
		end := dev.frame(opened.Stream, func(f streamFrame) bool { return f.Type == frameEnd })
		require.Equal(t, endRevoked, end.Reason)
		select {
		case <-dev.closed:
		case <-time.After(deviceWait):
			t.Fatal("revoked device stayed connected")
		}
	})
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// TestDeviceBridgeCrossNode: a device attached to node A reaches a
// session on node B by node ID and by swarm address, and B re-checks the
// device and the route allow-list itself.
func TestDeviceBridgeCrossNode(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	a, ac := newClusterTestServer(t)
	bn, bc := newClusterTestServer(t)
	pairServers(t, ctx, a, bn, ac, bc)
	bID := bn.cluster.Identity().ID

	key := newDeviceKey(t)
	poll := pairFlowA(t, a, ac, key)
	id := poll.Device.DeviceID
	require.Eventually(t, func() bool { _, ok := bn.pairing.reg.Get(id); return ok }, 15*time.Second, 50*time.Millisecond)
	dev := newFakeDevice(t, id, func(o ...nats.Option) (*nats.Conn, error) { return deviceConn(t, a, key, o...) })

	ws, bws, sess, _ := newBridgeWorkspace(t, bn, bc)
	path := "/v1/workspaces/" + ws.ID + "/sessions"

	resp := dev.call(bID, bridgeRequest{Method: http.MethodGet, Path: path})
	require.Equal(t, http.StatusOK, resp.Status, resp.Error)
	require.Equal(t, bID, resp.NodeID, "B answered")
	var sessions []proto.Session
	require.NoError(t, json.Unmarshal(resp.JSON, &sessions))
	require.Len(t, sessions, 2)

	resp = dev.call(anyNode, bridgeRequest{Method: http.MethodGet, Path: path, Target: &bridgeTarget{Node: bID}})
	require.Equal(t, bID, resp.NodeID)

	s, err := bws.Sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	if s.Color == "" {
		s, err = bws.EnsureSwarmIdentity(ctx, s)
		require.NoError(t, err)
	}
	addr := swarm.FormatAddress(swarm.Identity{Color: s.Color, Animal: s.Animal}, s.ID)
	resp = dev.call(anyNode, bridgeRequest{Method: http.MethodGet, Path: path + "/" + sess.ID + "/messages", Target: &bridgeTarget{Swarm: addr}})
	require.Equal(t, http.StatusOK, resp.Status, resp.Error)
	require.Equal(t, bID, resp.NodeID)
	require.Equal(t, codeBadRequest, dev.call(anyNode, bridgeRequest{Method: "GET", Path: path, Target: &bridgeTarget{Swarm: "not an address"}}).Error.Code)

	// A stream on B, opened through A, filtered to the swarm target.
	opened := dev.sub(streamRequest{Path: "/v1/workspaces/" + ws.ID + "/events", Target: &bridgeTarget{Swarm: addr}})
	require.Equal(t, http.StatusOK, opened.Status, opened.Error)
	require.Equal(t, bID, opened.NodeID)
	require.True(t, strings.HasPrefix(opened.Stream, bID+"_"))
	require.Eventually(t, func() bool {
		_, err := bws.Messages.Create(ctx, sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "from b"}}})
		require.NoError(t, err)
		select {
		case fr := <-dev.frames:
			return fr.Stream == opened.Stream && fr.Type == frameEvent && fr.SessionID == sess.ID
		case <-time.After(300 * time.Millisecond):
			return false
		}
	}, deviceWait, 10*time.Millisecond)
	// The owning node decides whether the filtered session exists.
	missing := dev.sub(streamRequest{Node: bID, Path: "/v1/workspaces/" + ws.ID + "/events", Query: json.RawMessage(`{"session_id":"` + uuid.NewString() + `"}`)})
	require.Equal(t, codeNotFound, missing.Error.Code)
	require.Equal(t, bID, missing.NodeID)

	var closed bridgeResponse
	dev.request(subjClientRoot+id+"."+verbUnsub, unsubRequest{ID: "u", Stream: opened.Stream}, &closed)
	require.Equal(t, http.StatusOK, closed.Status)
	require.Equal(t, bID, closed.NodeID, "only the stream's node answers")

	// B re-checks the route allow-list itself: a never route and an
	// untagged path sent straight to B's forward subject (skipping A's
	// check) are refused by B. A stored viewer role on B changes nothing.
	setDeviceRole(t, bn, id, pairing.RoleViewer)
	forwardToB := func(method, p string, body json.RawMessage) bridgeResponse {
		t.Helper()
		req, _ := json.Marshal(bridgeRequest{V: 1, ID: uuid.NewString(), Method: method, Path: p, JSON: body})
		env, _ := json.Marshal(fwdEnvelope{V: 1, Kind: fwdAPI, DeviceID: id, EntryNode: a.cluster.Identity().ID, Request: req})
		msg, err := a.mesh.Request(ctx, subjClientFwd+bID, env)
		require.NoError(t, err)
		var out bridgeResponse
		require.NoError(t, json.Unmarshal(msg.Data, &out))
		return out
	}
	denied := forwardToB(http.MethodPost, "/v1/workspaces/"+ws.ID+"/config/remove", json.RawMessage(`{"key":"x"}`))
	require.Equal(t, http.StatusForbidden, denied.Status)
	require.Equal(t, codeRouteNotAllowed, denied.Error.Code)
	require.Equal(t, bID, denied.NodeID)
	denied = forwardToB(http.MethodGet, "/v1/nodes/"+a.cluster.Identity().ID+"/v1/workspaces", nil)
	require.Equal(t, codeNotFound, denied.Error.Code)
	require.Equal(t, bID, denied.NodeID)
	allowed := forwardToB(http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, allowed.Status, "a viewer record on B still has full access")
	fwdReq, _ := json.Marshal(bridgeRequest{V: 1, ID: "f1", Method: "GET", Path: path})
	var denied2 bridgeResponse

	// A forward for an unknown device is refused by the owner.
	env, _ := json.Marshal(fwdEnvelope{V: 1, Kind: fwdAPI, DeviceID: "nobody", Request: fwdReq})
	msg, err := a.mesh.Request(ctx, subjClientFwd+bID, env)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(msg.Data, &denied2))
	require.Equal(t, codeDeviceRevoked, denied2.Error.Code)

	// A member that does not answer gives node_unreachable.
	ab := a.pairing.bridge.Load()
	var out bridgeResponse
	berr := ab.forward(ctx, id, "silent-node", fwdAPI, bridgeRequest{ID: "z"}, &out, 500*time.Millisecond)
	require.Equal(t, codeNodeUnreachable, berr.Code)
}

func TestDeviceBridgeStreamCredit(t *testing.T) {
	t.Parallel()
	s := &deviceStream{credit: 2, tune: defaultStreamTuning(), cancel: func() {}}
	s.cond = newCond(s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.True(t, s.waitCredit(ctx))
	s.sent = 2
	done := make(chan bool, 1)
	go func() { done <- s.waitCredit(ctx) }()
	select {
	case <-done:
		t.Fatal("a stream without credit must wait")
	case <-time.After(100 * time.Millisecond):
	}
	s.mu.Lock()
	s.acked = 1
	s.cond.Broadcast()
	s.mu.Unlock()
	require.True(t, <-done)
	cancel()
	s.sent = 10
	require.False(t, s.waitCredit(ctx), "cancelled streams stop")

	// Held at zero credit past the lease, the stream ends.
	blocked := &deviceStream{credit: 1, sent: 1, tune: streamTuning{blocked: 50 * time.Millisecond}, cancel: func() {}}
	blocked.cond = newCond(blocked)
	require.False(t, blocked.waitCredit(t.Context()))
	require.Equal(t, endCreditTimeout, blocked.reason)
}

func TestDeviceStreamResumeBuffer(t *testing.T) {
	t.Parallel()
	srv, _ := initPhoneNode(t)
	b := srv.pairing.bridge.Load()
	s := &deviceStream{b: b, id: "n_1", subject: "crush.client.v1.dev.ev.n_1", tune: streamTuning{ring: 4}, cancel: func() {}, credit: 100}
	s.cond = newCond(s)
	for i := range int64(10) {
		s.mu.Lock()
		s.sent++
		s.remember(ringFrame{seq: i, data: []byte("x")})
		s.mu.Unlock()
	}
	require.Len(t, s.ring, 4, "the buffer is bounded")
	require.False(t, s.resume(4, 10), "frame 5 was evicted")
	require.True(t, s.resume(5, 10))
	require.True(t, s.resume(9, 10), "nothing to replay is fine")
	require.False(t, s.resume(10, 10), "cannot resume past the end")
	require.Equal(t, int64(10), s.acked)
	s.end(endClosed)
	require.False(t, s.resume(9, 10), "ended streams cannot resume")
}

func TestDeviceStreamSessionOf(t *testing.T) {
	t.Parallel()
	s := &deviceStream{sessions: map[string]string{}}
	check := func(data, want string, scoped bool) {
		t.Helper()
		got, isScoped := s.sessionOf(data)
		require.Equal(t, want, got, data)
		require.Equal(t, scoped, isScoped, data)
	}
	check(`{"type":"message","payload":{"type":"created","payload":{"id":"m1","session_id":"s1"}}}`, "s1", true)
	check(`{"type":"message_delta","payload":{"id":"m1","appends":[]}}`, "s1", true)
	check(`{"type":"message_delta","payload":{"id":"m2"}}`, "", true)
	check(`{"type":"session","payload":{"type":"updated","payload":{"id":"s2"}}}`, "s2", true)
	check(`{"type":"agent_event","payload":{"type":"x","payload":{"message":{"session_id":"s3"}}}}`, "s3", true)
	check(`{"type":"attention_event","payload":{"type":"x","payload":{"session_id":"s4"}}}`, "s4", true)
	check(`{"type":"lsp_event","payload":{"type":"x","payload":{"name":"gopls"}}}`, "", false)
	check(`garbage`, "", true)
	check(`{"type":"new_kind","payload":{"type":"x","payload":{"other":"s9"}}}`, "", true)
	check(`{"type":"config_changed","payload":{"type":"x","payload":{}}}`, "", false)
}

func TestDeviceBridgeCheckSession(t *testing.T) {
	t.Parallel()
	b := &deviceBridge{ctx: t.Context()}
	require.Nil(t, b.checkSession("w", "s"), "no lookup, no refusal")
	b.sessionExists = func(_ context.Context, ws, sid string) (bool, error) { return ws == "w" && sid == "s", nil }
	require.Nil(t, b.checkSession("w", "s"))
	require.Equal(t, codeNotFound, b.checkSession("w", "x").Code)
	require.Equal(t, codeNotFound, b.checkSession("other", "s").Code)
	b.sessionExists = func(context.Context, string, string) (bool, error) { return false, context.DeadlineExceeded }
	require.Equal(t, codeInternal, b.checkSession("w", "s").Code)
	require.Equal(t, "ws_1", workspaceOf("/v1/workspaces/ws_1/events"))
}

func TestDeviceStreamRingBounds(t *testing.T) {
	t.Parallel()
	s := &deviceStream{tune: streamTuning{ring: 512}}
	big := make([]byte, 1<<20)
	for i := range int64(10) {
		s.remember(ringFrame{seq: i, data: big})
	}
	require.LessOrEqual(t, s.ringBytes, maxRingBytes)
	require.Equal(t, int64(9), s.ring[len(s.ring)-1].seq)
	require.Equal(t, maxRingBytes/len(big), len(s.ring))
}

func TestDeviceBridgeAttentionFiltering(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	op, viewer := pairPhone(t, srv, c), pairPhone(t, srv, c)
	setDeviceRole(t, srv, viewer.id, pairing.RoleViewer)
	opDev := newFakeDevice(t, op.id, func(o ...nats.Option) (*nats.Conn, error) { return op.connect(t, o...) })
	viewDev := newFakeDevice(t, viewer.id, func(o ...nats.Option) (*nats.Conn, error) { return viewer.connect(t, o...) })

	feed := make(chan pubsub.Event[proto.AttentionEvent], 4)
	b := &deviceBridge{
		p: srv.pairing, nc: srv.mesh.ClientsConn(), mesh: srv.mesh, nodeID: "node-x",
		attention: func(context.Context) <-chan pubsub.Event[proto.AttentionEvent] { return feed },
		swarmOf:   func(context.Context, string, string) string { return "aliceblue-tiger-1a2b" },
	}
	b.ctx, b.cancel = context.WithCancel(t.Context())
	defer b.cancel()
	b.wg.Go(b.attentionLoop)

	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: proto.AttentionClusterJoin}}
	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: proto.AttentionBusy, NodeID: "other"}}
	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: proto.AttentionBlockedPermission, SessionID: "s1", ToolCallID: "t1"}}
	for _, tc := range []struct {
		dev        *fakeDevice
		actionable bool
	}{{opDev, true}, {viewDev, true}} {
		select {
		case a := <-tc.dev.attention:
			require.Equal(t, "s1", a["session_id"], "joins and relayed events are skipped")
			require.Equal(t, "node-x", a["node_id"])
			require.Equal(t, "aliceblue-tiger-1a2b", a["swarm"])
			require.Equal(t, tc.actionable, a["actionable"])
			require.Equal(t, float64(1), a["v"])
		case <-time.After(deviceWait):
			t.Fatal("no attention event")
		}
	}
}

func TestDeviceBridgeHardening(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)

	// One fake /v1 handler, installed before any bridged request, so the
	// bridge never sees it change.
	const (
		echo = iota
		hugeBody
		slow
		held
	)
	var mode atomic.Int32
	var calls atomic.Int32
	seen := make(chan string, 4)
	b := srv.pairing.bridge.Load()
	release, hold := make(chan struct{}), make(chan struct{})
	b.local = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch mode.Load() {
		case held:
			<-hold
			return
		case hugeBody:
			chunk := make([]byte, 1<<20)
			for range 8 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			return
		case slow:
			select {
			case <-release:
			case <-r.Context().Done():
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"late":true}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen <- r.URL.Query().Get("client_id") + "|" + r.URL.Query().Get("limit") + "|" + r.Header.Get("X-Forwarded-For") + "|" + toString(body["client_id"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	dev := newFakeDevice(t, ph.id, func(o ...nats.Option) (*nats.Conn, error) { return ph.connect(t, o...) })

	// A device cannot choose the client identity of a bridged request, nor
	// smuggle headers.
	resp := dev.call(anyNode, bridgeRequest{Method: "GET", Path: "/v1/workspace-overviews", Query: json.RawMessage(`{"client_id":"victim","limit":["5"]}`), Headers: map[string]string{"x-forwarded-for": "1.2.3.4"}})
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "|5||", <-seen)
	resp = dev.post("/v1/workspaces/w/agent", map[string]string{"prompt": "hi", "client_id": "victim"})
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "|||device:"+ph.id, <-seen)

	// Oversized answers and bodies are refused, not buffered without bound.
	mode.Store(hugeBody)
	require.Equal(t, codePayloadTooLarge, dev.get("/v1/workspace-overviews").Error.Code)
	big := base64.RawURLEncoding.EncodeToString(make([]byte, maxBridgeBody+1))
	require.Equal(t, codePayloadTooLarge, dev.call(anyNode, bridgeRequest{Method: "POST", Path: "/v1/workspaces/w/sessions", Body: big}).Error.Code)

	// Timeouts answer 504, keep the slot until the handler returns, and a
	// retry with the same key gets the real outcome once it is known.
	mode.Store(slow)
	before := calls.Load()
	req := bridgeRequest{Method: "POST", Path: "/v1/workspaces/w/sessions", JSON: json.RawMessage(`{}`), TimeoutMS: 1000, IdempotencyKey: "slow-1"}
	resp = dev.call(anyNode, req)
	require.Equal(t, codeTimeout, resp.Error.Code)
	close(release)
	require.Eventually(t, func() bool {
		r := dev.call(anyNode, req)
		return r.Replayed && r.Status == http.StatusOK && strings.Contains(string(r.JSON), "late")
	}, deviceWait, 50*time.Millisecond)
	require.Equal(t, before+1, calls.Load(), "the slow POST ran once")

	// At most 8 requests in flight per device.
	mode.Store(held)
	results := make(chan bridgeResponse, maxInflightPerDevice+1)
	for range maxInflightPerDevice + 1 {
		go func() {
			data, _ := json.Marshal(bridgeRequest{V: 1, ID: uuid.NewString(), Method: "GET", Path: "/v1/workspaces"})
			msg, err := dev.nc.Request(subjClientRoot+ph.id+".api.any", data, deviceWait)
			if err != nil {
				return
			}
			var r bridgeResponse
			_ = json.Unmarshal(msg.Data, &r)
			results <- r
		}()
	}
	select {
	case r := <-results:
		require.Equal(t, codeRateLimited, r.Error.Code)
		require.Positive(t, r.Error.RetryAfterMS)
	case <-time.After(deviceWait):
		t.Fatal("the ninth request was not refused")
	}
	close(hold)

	// Revocation cancels in-flight work.
	ctx := b.work(ph.id)
	b.closeDevice(ph.id)
	require.Error(t, ctx.Err())

	// A request or stream racing the revocation (registry already revoked,
	// closeDevice not yet run) never gets a live context.
	_, err := srv.pairing.reg.Revoke(ph.id, "n", time.Now())
	require.NoError(t, err)
	require.Error(t, b.work(ph.id).Err())
	d, ok := srv.pairing.reg.Get(ph.id)
	require.True(t, ok)
	opened := b.openStream(d, streamRequest{ID: "r", Path: "/v1/workspaces/w/events"})
	require.NotNil(t, opened.Error)
	require.Equal(t, codeDeviceRevoked, opened.Error.Code)
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
