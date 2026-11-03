package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/historysearch"
)

func TestHistoryResultToProto(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	res := historysearch.CrossResult{
		Hits: []historysearch.CrossHit{
			{Root: "/a", SessionID: "s1", SessionTitle: "One", Match: embedding.MatchBoth, Snippet: "best", MessageID: "m1", Role: "user", CreatedAt: now, Score: 0.9},
			{Root: "/b", SessionID: "s2", Match: embedding.MatchExact, MessageID: "m2", Score: 0.5},
		},
		Total: 2, SemanticUsed: true, Mode: embedding.SearchModeHybrid,
	}
	out := HistoryResultToProto(res, func(root string) string { return map[string]string{"/a": "ws-a"}[root] })
	require.Equal(t, 2, out.Total)
	require.True(t, out.SemanticUsed)
	require.Equal(t, "hybrid", out.Mode)
	require.Empty(t, out.FallbackReason)
	require.Equal(t, "ws-a", out.Hits[0].WorkspaceID)
	require.Equal(t, "/a", out.Hits[0].WorkspaceRoot)
	require.Equal(t, "both", out.Hits[0].Match)
	require.Equal(t, "m1", out.Hits[0].MessageID)
	require.InDelta(t, 0.9, out.Hits[0].Score, 1e-9)
	require.Empty(t, out.Hits[1].WorkspaceID, "detached roots have no workspace ID")

	text := HistoryResultToProto(historysearch.CrossResult{Mode: embedding.SearchModeText, FallbackReason: embedding.FallbackNoEmbedder}, nil)
	require.Equal(t, "text", text.Mode)
	require.Equal(t, "no_embedder", text.FallbackReason)
	require.NotNil(t, text.Hits)
	require.Empty(t, text.Hits)
}
