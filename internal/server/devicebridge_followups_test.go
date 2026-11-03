package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/fantasy"
)

// fixedEmbedder maps texts to fixed vectors.
type fixedEmbedder struct{ vecs map[string][]float32 }

func (f *fixedEmbedder) Embed(_ context.Context, call fantasy.EmbeddingCall) (*fantasy.EmbeddingResponse, error) {
	out := make([]fantasy.Embedding, len(call.Input))
	for i, in := range call.Input {
		out[i] = f.vecs[in]
	}
	return &fantasy.EmbeddingResponse{Embeddings: out}, nil
}
func (f *fixedEmbedder) Provider() string { return "fake" }
func (f *fixedEmbedder) Model() string    { return "fake" }

// TestDeviceBridgeSearchAndMove drives history search and session move
// through a fake device over the device websocket. Every paired device
// has full access, so a stored viewer record searches and moves too;
// only the route allow-list refuses.
func TestDeviceBridgeSearchAndMove(t *testing.T) {
	isolateHome(t)
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	dev := newFakeDevice(t, ph.id, func(o ...nats.Option) (*nats.Conn, error) { return ph.connect(t, o...) })
	ws, bws, alpha, _ := newBridgeWorkspace(t, srv, c)
	needle, err := bws.Messages.Create(t.Context(), alpha.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "the flaky needle test"}}})
	require.NoError(t, err)
	base := "/v1/workspaces/" + ws.ID
	off := false

	search := func(t *testing.T, params proto.SearchHistoryParams) proto.SearchHistoryResult {
		t.Helper()
		resp := dev.post(base+"/history/search", params)
		require.Equal(t, http.StatusOK, resp.Status, resp.Error)
		var res proto.SearchHistoryResult
		require.NoError(t, json.Unmarshal(resp.JSON, &res))
		return res
	}
	requireHit := func(t *testing.T) {
		t.Helper()
		res := search(t, proto.SearchHistoryParams{Query: "needle", Semantic: &off})
		require.NotEmpty(t, res.Hits)
		require.Equal(t, alpha.ID, res.Hits[0].SessionID)
		require.Equal(t, ws.ID, res.Hits[0].WorkspaceID)
		require.Equal(t, "exact", res.Hits[0].Match)
		require.False(t, res.SemanticUsed)
	}
	move := func() bridgeResponse {
		return dev.post("/v1/sessions/move", proto.MoveSessionRequest{WorkspaceID: ws.ID, SessionID: alpha.ID, TargetNode: "no-such-node"})
	}
	requireMoveRouted := func(t *testing.T) {
		t.Helper()
		resp := move()
		if resp.Error != nil {
			require.NotEqual(t, codeDeviceScopeDenied, resp.Error.Code)
			require.NotEqual(t, codeRouteNotAllowed, resp.Error.Code)
		}
	}

	t.Run("search", requireHit)
	t.Run("text fallback without an embedder", func(t *testing.T) {
		res := search(t, proto.SearchHistoryParams{Query: "needle"})
		require.Equal(t, "text", res.Mode)
		require.Equal(t, "no_embedder", res.FallbackReason)
		require.False(t, res.SemanticUsed)
		require.Equal(t, "exact", res.Hits[0].Match)
	})
	t.Run("hybrid with an embedder", func(t *testing.T) {
		emb := bws.SetHistoryEmbedderForTest(
			embedding.Params{Configured: true, Model: "fake", Hybrid: true, Signature: "sig-e2e"},
			&fixedEmbedder{vecs: map[string][]float32{"needle": {1, 0}, "the flaky needle test": {1, 0}}},
		)
		require.NoError(t, emb.Embed(t.Context(), embedding.SourceMessage, needle.ID, alpha.ID, "the flaky needle test"))
		res := search(t, proto.SearchHistoryParams{Query: "needle", Scope: "all", Limit: 5})
		require.Equal(t, "hybrid", res.Mode)
		require.Empty(t, res.FallbackReason)
		require.True(t, res.SemanticUsed)
		require.Equal(t, alpha.ID, res.Hits[0].SessionID)
		require.Equal(t, "both", res.Hits[0].Match)

		res = search(t, proto.SearchHistoryParams{Query: "needle", Semantic: &off})
		require.Equal(t, "text", res.Mode)
		require.Equal(t, "disabled_by_request", res.FallbackReason)
	})
	t.Run("move reaches the handler", requireMoveRouted)

	setDeviceRole(t, srv, ph.id, pairing.RoleViewer)
	t.Run("viewer record searches", requireHit)
	t.Run("viewer record moves", requireMoveRouted)

	t.Run("never routes denied", func(t *testing.T) {
		for _, p := range []string{base + "/embeddings/backfill", "/v1/sessions/batch", base + "/config/remove", "/v1/migrate/execute"} {
			resp := dev.post(p, map[string]any{})
			require.Equal(t, http.StatusForbidden, resp.Status, p)
			require.Equal(t, codeRouteNotAllowed, resp.Error.Code, p)
		}
	})
	t.Run("untagged route denied", func(t *testing.T) {
		resp := dev.get("/v1/nodes/" + ph.nodeID + "/v1/workspaces")
		require.Equal(t, http.StatusNotFound, resp.Status)
		require.Equal(t, codeNotFound, resp.Error.Code)
	})
}
