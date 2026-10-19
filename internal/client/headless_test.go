package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return c
}

func TestGetPendingPrompts(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workspaces/ws/sessions/s1/pending", r.URL.Path)
		_ = json.NewEncoder(w).Encode(proto.PendingPrompts{
			Permissions: []proto.PermissionRequest{{ID: "p1", ToolName: "bash", Params: map[string]any{}}},
			Questions:   []proto.QuestionRequest{{ID: "q1", Kind: proto.QuestionKindYesNo}},
		})
	})
	got, err := c.GetPendingPrompts(t.Context(), "ws", "s1")
	require.NoError(t, err)
	require.Len(t, got.Permissions, 1)
	require.Equal(t, "p1", got.Permissions[0].ID)
	require.Equal(t, proto.QuestionKindYesNo, got.Questions[0].Kind)

	c = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err = c.GetPendingPrompts(t.Context(), "ws", "s1")
	require.ErrorContains(t, err, "status code 404")
}

func TestSendAgentMessageStampsClientIDAndPolicy(t *testing.T) {
	t.Parallel()

	var got proto.AgentMessage
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workspaces/ws/agent", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusAccepted)
	})
	err := c.SendAgentMessage(t.Context(), "ws", proto.AgentMessage{
		SessionID: "s", RunID: "r", Prompt: "p", ClientID: "spoofed", PermissionPolicy: proto.PermissionPolicyDeny,
	})
	require.NoError(t, err)
	require.Equal(t, c.ClientID(), got.ClientID, "the client always identifies itself")
	require.Equal(t, proto.PermissionPolicyDeny, got.PermissionPolicy)
	require.Equal(t, "r", got.RunID)

	c = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(proto.Error{Message: "invalid permission policy"})
	})
	err = c.SendAgentMessage(t.Context(), "ws", proto.AgentMessage{SessionID: "s", Prompt: "p"})
	require.ErrorContains(t, err, "status code 400: invalid permission policy")
}

func TestCreateSessionWithModel(t *testing.T) {
	t.Parallel()

	var got proto.Session
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_ = json.NewEncoder(w).Encode(proto.Session{ID: "new", Title: got.Title, ModelRef: got.ModelRef})
	})
	sess, err := c.CreateSessionWithModel(t.Context(), "ws", "t", "scout")
	require.NoError(t, err)
	require.Equal(t, "scout", got.ModelRef)
	require.Equal(t, "scout", sess.ModelRef)

	c = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(proto.Error{Message: "invalid session model: ghost"})
	})
	_, err = c.CreateSessionWithModel(t.Context(), "ws", "t", "ghost")
	require.ErrorContains(t, err, "invalid session model: ghost")
}

func TestResolveSwarmAddress(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/swarm/resolve", r.URL.Path)
		require.Equal(t, "coral-polar bear-3b9e", r.URL.Query().Get("address"))
		_ = json.NewEncoder(w).Encode(proto.SwarmTarget{WorkspaceID: "ws", SessionID: "s", Address: "coral-polar bear-3b9e", Path: "/p"})
	})
	got, err := c.ResolveSwarmAddress(t.Context(), "coral-polar bear-3b9e")
	require.NoError(t, err)
	require.Equal(t, proto.SwarmTarget{WorkspaceID: "ws", SessionID: "s", Address: "coral-polar bear-3b9e", Path: "/p"}, got)

	c = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(proto.Error{Message: "swarm address matches multiple sessions"})
	})
	_, err = c.ResolveSwarmAddress(t.Context(), "coral-otter")
	require.ErrorContains(t, err, "matches multiple sessions")
}
