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
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

func bridgeCall(t *testing.T, nc *nats.Conn, deviceID, node, method, path string, body any) bridgeResponse {
	t.Helper()
	req := bridgeRequest{ID: uuid.NewString(), Method: method, Path: path, Headers: map[string]string{}}
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req.Body = base64.RawURLEncoding.EncodeToString(data)
		req.Headers["content-type"] = "application/json"
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	msg, err := nc.Request("crush.client.v1."+deviceID+".api."+node, data, 10*time.Second)
	require.NoError(t, err)
	var resp bridgeResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	require.Equal(t, req.ID, resp.ID)
	return resp
}

func TestDeviceRouterAllowList(t *testing.T) {
	t.Parallel()
	r := newDeviceRouter()
	viewer := pairing.RoleViewer.Scopes()
	operator := pairing.RoleOperator.Scopes()
	require.NotEmpty(t, r.authorize("GET", "/v1/workspace-overviews", viewer))
	require.NotEmpty(t, r.authorize("GET", "/v1/workspaces/w/sessions/s/messages", viewer))
	require.Empty(t, r.authorize("POST", "/v1/workspaces/w/agent", viewer), "viewers cannot prompt")
	require.NotEmpty(t, r.authorize("POST", "/v1/workspaces/w/agent", operator))
	require.NotEmpty(t, r.authorize("POST", "/v1/workspaces/w/permissions/grant", operator))
	require.Empty(t, r.authorize("POST", "/v1/workspaces/w/permissions/skip", operator), "yolo needs permissions:always")
	require.Empty(t, r.authorize("POST", "/v1/workspaces/w/agent/sessions/s/shell", pairing.RoleAdmin.Scopes()), "shell is opt-in")
	for _, denied := range []string{
		"/v1/workspaces/w/config/provider-key", "/v1/workspaces/w/config/set", "/v1/cluster/token",
		"/v1/cluster/rotate", "/v1/debug/logs", "/v1/nodes/x/v1/workspaces", "/v1/transfer/import",
		"/v1/workspaces/w/../../cluster/token", "/v1/workspaces//w", "/v1/workspaces/w%2Fx", "/v1/cluster/devices",
	} {
		require.Empty(t, r.authorize("POST", denied, pairing.RoleAdmin.Scopes()), denied)
		require.Empty(t, r.authorize("GET", denied, pairing.RoleAdmin.Scopes()), denied)
	}
}

func TestDeviceBridgeAPI(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	nc, err := ph.connect(t)
	require.NoError(t, err)
	defer nc.Close()

	resp := bridgeCall(t, nc, ph.id, anyNode, "GET", "/v1/workspace-overviews", nil)
	require.Equal(t, http.StatusOK, resp.Status)
	body, err := base64.RawURLEncoding.DecodeString(resp.Body)
	require.NoError(t, err)
	require.True(t, json.Valid(body))
	require.Equal(t, "application/json", resp.Headers["content-type"])

	// Explicit owner: this node.
	resp = bridgeCall(t, nc, ph.id, ph.nodeID, "GET", "/v1/cluster/dashboard", nil)
	require.Equal(t, http.StatusOK, resp.Status)

	// Forbidden and unknown routes, and unknown nodes.
	require.Equal(t, http.StatusForbidden, bridgeCall(t, nc, ph.id, anyNode, "GET", "/v1/cluster/token", nil).Status)
	require.Equal(t, http.StatusForbidden, bridgeCall(t, nc, ph.id, anyNode, "POST", "/v1/workspaces/w/config/provider-key", map[string]string{"key": "x"}).Status)
	require.Equal(t, http.StatusNotFound, bridgeCall(t, nc, ph.id, "no-such-node", "GET", "/v1/workspace-overviews", nil).Status)
	// Authorized but the workspace does not exist: the handler answers.
	got := bridgeCall(t, nc, ph.id, anyNode, "POST", "/v1/workspaces/"+uuid.NewString()+"/agent", map[string]string{"prompt": "hi"})
	require.NotEqual(t, http.StatusForbidden, got.Status)
}

func TestDeviceBridgeRepliesOnlyToOwnInbox(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	alice, bob := pairPhone(t, srv, c), pairPhone(t, srv, c)
	an, err := alice.connect(t)
	require.NoError(t, err)
	defer an.Close()
	bn, err := bob.connect(t)
	require.NoError(t, err)
	defer bn.Close()

	stolen := make(chan *nats.Msg, 1)
	_, err = bn.Subscribe("_INBOX."+bob.id+".>", func(m *nats.Msg) { stolen <- m })
	require.NoError(t, err)
	require.NoError(t, bn.Flush())
	req, _ := json.Marshal(bridgeRequest{ID: "x", Method: "GET", Path: "/v1/workspace-overviews"})
	require.NoError(t, an.PublishRequest("crush.client.v1."+alice.id+".api.any", "_INBOX."+bob.id+".steal", req))
	require.NoError(t, an.Flush())
	select {
	case <-stolen:
		t.Fatal("bridge answered into another device's inbox")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestDeviceBridgeStreamsAndControl(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	nc, err := ph.connect(t)
	require.NoError(t, err)
	defer nc.Close()

	control := make(chan string, 2)
	_, err = nc.Subscribe("crush.client.v1."+ph.id+".control", func(m *nats.Msg) { control <- string(m.Data) })
	require.NoError(t, err)

	// Open the global events stream through the bridge.
	sub, _ := json.Marshal(map[string]any{"node": "any", "path": "/v1/events", "query": map[string]any{"wire": 2}, "credit": 4})
	reply, err := nc.Request("crush.client.v1."+ph.id+".sub", sub, 5*time.Second)
	require.NoError(t, err)
	var opened struct {
		Stream string `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(reply.Data, &opened))
	require.NotEmpty(t, opened.Stream)
	frames := make(chan streamFrame, 16)
	_, err = nc.Subscribe("crush.client.v1."+ph.id+".ev."+opened.Stream, func(m *nats.Msg) {
		var f streamFrame
		if json.Unmarshal(m.Data, &f) == nil {
			frames <- f
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	require.Eventually(t, func() bool {
		srv.backend.PublishClusterJoin("req-1", "joiner")
		select {
		case f := <-frames:
			return strings.Contains(f.Data, "cluster_join")
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)

	// A denied stream path is refused.
	bad, _ := json.Marshal(map[string]any{"path": "/v1/workspace-overviews"})
	reply, err = nc.Request("crush.client.v1."+ph.id+".sub", bad, 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, string(reply.Data), "403")

	// Unsubscribing ends the stream on the node.
	unsub, _ := json.Marshal(map[string]string{"stream": opened.Stream})
	require.NoError(t, nc.Publish("crush.client.v1."+ph.id+".unsub", unsub))
	b := srv.pairing.bridge.Load()
	require.Eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.streams) == 0
	}, 5*time.Second, 20*time.Millisecond)

	// Revocation is announced on the control subject before the drop.
	_, err = c.RevokeDevice(t.Context(), ph.id)
	require.NoError(t, err)
	select {
	case msg := <-control:
		require.JSONEq(t, `{"type":"revoked"}`, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("no revoked control notice")
	}
}

func TestDeviceBridgeStreamCredit(t *testing.T) {
	t.Parallel()
	s := &deviceStream{credit: 2}
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
}

func TestDeviceBridgeAttentionFanOut(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	nc, err := ph.connect(t)
	require.NoError(t, err)
	defer nc.Close()
	got := make(chan proto.AttentionEvent, 4)
	_, err = nc.Subscribe(subjClientAttention, func(m *nats.Msg) {
		var a proto.AttentionEvent
		if json.Unmarshal(m.Data, &a) == nil {
			got <- a
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	feed := make(chan pubsub.Event[proto.AttentionEvent], 4)
	b := &deviceBridge{
		p: srv.pairing, nc: srv.mesh.ClientsConn(), nodeID: "node-x",
		attention: func(context.Context) <-chan pubsub.Event[proto.AttentionEvent] { return feed },
	}
	b.ctx, b.cancel = context.WithCancel(t.Context())
	defer b.cancel()
	b.wg.Go(b.attentionLoop)

	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: proto.AttentionClusterJoin}}
	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: "busy", NodeID: "other"}}
	feed <- pubsub.Event[proto.AttentionEvent]{Payload: proto.AttentionEvent{Kind: "blocked_permission", SessionID: "s1"}}
	select {
	case a := <-got:
		require.Equal(t, "s1", a.SessionID, "joins and relayed events are skipped")
		require.Equal(t, "node-x", a.NodeID)
	case <-time.After(5 * time.Second):
		t.Fatal("no attention event")
	}
}

func TestDeviceBridgeHardening(t *testing.T) {
	t.Parallel()
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)

	// One fake /v1 handler, installed before any bridged request, so the
	// bridge never sees it change.
	const (
		echoQuery = iota
		hugeBody
	)
	var mode atomic.Int32
	seen := make(chan string, 1)
	b := srv.pairing.bridge.Load()
	b.local = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == hugeBody {
			chunk := make([]byte, 1<<20)
			for range 8 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			return
		}
		seen <- r.URL.Query().Get("client_id") + "|" + r.URL.Query().Get("limit")
		_, _ = w.Write([]byte("[]"))
	})

	nc, err := ph.connect(t)
	require.NoError(t, err)
	defer nc.Close()

	// Event streams never run through the buffered API path, and only GET
	// and POST are routable.
	require.Equal(t, http.StatusForbidden, bridgeCall(t, nc, ph.id, anyNode, "GET", "/v1/events", nil).Status)
	require.Equal(t, http.StatusForbidden, bridgeCall(t, nc, ph.id, anyNode, "HEAD", "/v1/workspace-overviews", nil).Status)

	// A device cannot choose the client identity of a bridged request.
	reqID := uuid.NewString()
	data, _ := json.Marshal(bridgeRequest{ID: reqID, Method: "GET", Path: "/v1/workspace-overviews", Query: "client_id=victim&limit=5"})
	msg, err := nc.Request("crush.client.v1."+ph.id+".api.any", data, 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, string(msg.Data), reqID)
	require.Equal(t, "|5", <-seen)

	// Oversized answers are refused, not buffered without bound.
	mode.Store(hugeBody)
	require.Equal(t, http.StatusRequestEntityTooLarge, bridgeCall(t, nc, ph.id, anyNode, "GET", "/v1/workspace-overviews", nil).Status)

	// Revocation cancels in-flight work.
	ctx := b.work(ph.id)
	b.closeDevice(ph.id)
	require.Error(t, ctx.Err())
}
