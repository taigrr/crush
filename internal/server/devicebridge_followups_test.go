package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server/devicescope"
)

// setDeviceScopes rewrites a device's scopes on one node, keeping its
// role.
func setDeviceScopes(t *testing.T, srv *Server, id string, scopes []string) {
	t.Helper()
	d, ok := srv.pairing.reg.Get(id)
	require.True(t, ok)
	d.Scopes, d.Rev, d.UpdatedAt = scopes, d.Rev+1, time.Now().UTC()
	changed, err := srv.pairing.reg.Merge(d)
	require.NoError(t, err)
	require.True(t, changed)
}

// TestDeviceBridgeSearchAndMove drives history search and session move
// through a fake device over the device websocket.
func TestDeviceBridgeSearchAndMove(t *testing.T) {
	isolateHome(t)
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	dev := newFakeDevice(t, ph.id, func(o ...nats.Option) (*nats.Conn, error) { return ph.connect(t, o...) })
	ws, bws, alpha, _ := newBridgeWorkspace(t, srv, c)
	_, err := bws.Messages.Create(t.Context(), alpha.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "the flaky needle test"}}})
	require.NoError(t, err)
	searchPath := "/v1/workspaces/" + ws.ID + "/history/search"
	off := false
	search := proto.SearchHistoryParams{Query: "needle", Semantic: &off}

	requireHit := func(t *testing.T) {
		t.Helper()
		resp := dev.post(searchPath, search)
		require.Equal(t, http.StatusOK, resp.Status, resp.Error)
		var res proto.SearchHistoryResult
		require.NoError(t, json.Unmarshal(resp.JSON, &res))
		require.NotEmpty(t, res.Hits)
		require.Equal(t, alpha.ID, res.Hits[0].SessionID)
		require.Equal(t, ws.ID, res.Hits[0].WorkspaceID)
		require.False(t, res.SemanticUsed)
	}

	t.Run("operator can move", func(t *testing.T) {
		require.Contains(t, pairing.RoleOperator.Scopes(), devicescope.SessionsMove)
		resp := dev.post("/v1/sessions/move", proto.MoveSessionRequest{SessionID: alpha.ID, TargetNode: "no-such-node"})
		require.NotEqual(t, http.StatusForbidden, resp.Status, "operators pass the scope check")
		if resp.Error != nil {
			require.NotEqual(t, codeDeviceScopeDenied, resp.Error.Code)
			require.NotEqual(t, codeRouteNotAllowed, resp.Error.Code)
		}
	})

	t.Run("search as operator", requireHit)

	setDeviceRole(t, srv, ph.id, pairing.RoleViewer)

	t.Run("search as viewer", requireHit)

	t.Run("viewer cannot move", func(t *testing.T) {
		resp := dev.post("/v1/sessions/move", proto.MoveSessionRequest{SessionID: alpha.ID, TargetNode: "no-such-node"})
		require.Equal(t, http.StatusForbidden, resp.Status)
		require.Equal(t, codeDeviceScopeDenied, resp.Error.Code)
		require.Equal(t, devicescope.SessionsMove, resp.Error.Scope)
	})

	setDeviceScopes(t, srv, ph.id, []string{})

	t.Run("search needs sessions:read", func(t *testing.T) {
		resp := dev.post(searchPath, search)
		require.Equal(t, http.StatusForbidden, resp.Status)
		require.Equal(t, codeDeviceScopeDenied, resp.Error.Code)
		require.Equal(t, devicescope.SessionsRead, resp.Error.Scope)
	})
}
