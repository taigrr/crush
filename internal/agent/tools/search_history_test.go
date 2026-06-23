package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
)

func TestFormatHistoryHits(t *testing.T) {
	t.Parallel()
	res := embedding.SearchResult{
		Total:        2,
		Offset:       0,
		SemanticUsed: true,
		Hits: []embedding.Hit{
			{
				Rank: 1, Match: embedding.MatchBoth,
				SessionID: "abc12345-rest", SessionTitle: "deploy chat",
				SourceID: "m1", Role: "user",
				CreatedAt: time.Unix(1, 0), Snippet: "needle",
			},
			{
				Rank: 2, Match: embedding.MatchExact,
				SessionID: "xyz98765-rest", SessionTitle: "",
				SourceID: "m2", Role: "assistant",
				CreatedAt: time.Unix(2, 0), Snippet: "needle",
			},
		},
	}
	out := formatHistoryHits("needle", res)
	require.Contains(t, out, "Matches 1-2 of 2 for \"needle\" (hybrid):")
	require.Contains(t, out, "deploy chat")
	require.Contains(t, out, "(untitled)")
	// Full session ids and message ids so they line up with list_sessions.
	require.Contains(t, out, "session abc12345-rest · message m1")
	require.Contains(t, out, "session xyz98765-rest · message m2")
	// Match-type badges.
	require.Contains(t, out, "{both}")
	require.Contains(t, out, "{exact}")
}

func TestFormatHistoryHitsPagination(t *testing.T) {
	t.Parallel()
	res := embedding.SearchResult{
		Total:  5,
		Offset: 2,
		Hits: []embedding.Hit{
			{Rank: 3, Match: embedding.MatchExact, SessionID: "s1", SourceID: "m", Role: "user", CreatedAt: time.Unix(1, 0), Snippet: "needle"},
		},
	}
	out := formatHistoryHits("needle", res)
	require.Contains(t, out, "Matches 3-3 of 5")
	require.Contains(t, out, "substring") // SemanticUsed false
	require.Contains(t, out, "offset=3")
}
