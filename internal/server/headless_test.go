package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
)

func getPending(t *testing.T, h *agentE2EHarness, sessionID string) (int, proto.PendingPrompts) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspace.ID+"/sessions/"+sessionID+"/pending", nil)
	require.NoError(t, err)
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out proto.PendingPrompts
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	}
	return resp.StatusCode, out
}

func TestHandleGetWorkspaceSessionPending(t *testing.T) {
	t.Parallel()
	h := newAgentE2EHarness(t)

	status, pending := getPending(t, h, "s1")
	require.Equal(t, http.StatusOK, status)
	require.True(t, pending.IsEmpty())
	require.NotNil(t, pending.Permissions, "empty lists are encoded as []")

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	requests := h.app.Permissions.Subscribe(ctx)
	go func() {
		_, _ = h.app.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s1", ToolCallID: "tc1", ToolName: "bash", Action: "execute",
			Description: "make test", Path: t.TempDir(),
		})
	}()
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("permission request was never published")
	}

	status, pending = getPending(t, h, "s1")
	require.Equal(t, http.StatusOK, status)
	require.Len(t, pending.Permissions, 1)
	require.Equal(t, "make test", pending.Permissions[0].Description)
	require.Equal(t, "tc1", pending.Permissions[0].ToolCallID)

	_, pending = getPending(t, h, "s2")
	require.True(t, pending.IsEmpty(), "other sessions see nothing")
}

func TestHandleGetWorkspaceSessionPending_UnknownWorkspace(t *testing.T) {
	t.Parallel()
	h := newAgentE2EHarness(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		h.httpSrv.URL+"/v1/workspaces/nope/sessions/s1/pending", nil)
	require.NoError(t, err)
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandlePostWorkspaceAgent_InvalidPermissionPolicy(t *testing.T) {
	t.Parallel()
	h := newAgentE2EHarness(t)

	body, err := json.Marshal(proto.AgentMessage{SessionID: "s1", Prompt: "hi", PermissionPolicy: "always"})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		h.httpSrv.URL+"/v1/workspaces/"+h.workspace.ID+"/agent", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var e proto.Error
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	require.Contains(t, e.Message, "invalid permission policy")
	require.Zero(t, h.coord.runStarts.Load(), "the run is rejected before dispatch")
}

func TestHandleGetSwarmResolve_Errors(t *testing.T) {
	// The not-found path probes the workspace registry, so point it at an
	// empty home instead of the developer's real one.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h := &e2eHarness{}
	h.installServer(t)

	for query, want := range map[string]int{
		"":                            http.StatusBadRequest,
		"?address=":                   http.StatusBadRequest,
		"?address=%21":                http.StatusBadRequest,
		"?address=ghost-nothing-ffff": http.StatusNotFound,
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.httpSrv.URL+"/v1/swarm/resolve"+query, nil)
		require.NoError(t, err)
		resp, err := h.httpSrv.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, want, resp.StatusCode, query)
	}
}
