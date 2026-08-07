package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
)

func TestResolveSearchLimits(t *testing.T) {
	t.Parallel()

	// Non-positive → default.
	sl, cl := ResolveSearchLimits(0)
	require.Equal(t, searchDefaultSessionLimit, sl)
	require.Equal(t, max(searchDefaultSessionLimit*searchSessionCandidateFactor, searchMinCandidates), cl)

	sl, _ = ResolveSearchLimits(-5)
	require.Equal(t, searchDefaultSessionLimit, sl)

	// Small positive → floored candidate window.
	sl, cl = ResolveSearchLimits(3)
	require.Equal(t, 3, sl)
	require.Equal(t, searchMinCandidates, cl)

	// Hostile huge limit → clamped, candidate bounded.
	sl, cl = ResolveSearchLimits(1_000_000)
	require.Equal(t, searchMaxSessionLimit, sl)
	require.Equal(t, searchMaxSessionLimit*searchSessionCandidateFactor, cl)
}

func TestCollapseToSessions(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	res := embedding.SearchResult{
		SemanticUsed: true,
		Total:        4,
		Hits: []embedding.Hit{
			{Rank: 1, Score: 0.9, Match: embedding.MatchBoth, SessionID: "s1", SessionTitle: "One", SourceID: "m1", Role: "user", CreatedAt: now, Snippet: "best s1"},
			{Rank: 2, Score: 0.8, Match: embedding.MatchExact, SessionID: "s2", SessionTitle: "Two", SourceID: "m2", Role: "assistant", CreatedAt: now, Snippet: "best s2"},
			{Rank: 3, Score: 0.5, Match: embedding.MatchExact, SessionID: "s1", SessionTitle: "One", SourceID: "m3", Role: "user", CreatedAt: now, Snippet: "worse s1"},
			{Rank: 4, Score: 0.4, Match: embedding.MatchSemantic, SessionID: "s3", SessionTitle: "Three", SourceID: "m4", Role: "user", CreatedAt: now, Snippet: "best s3"},
		},
	}

	out := collapseToSessions(res, 50)

	require.True(t, out.SemanticUsed)
	require.Equal(t, 3, out.Total)
	require.Len(t, out.Hits, 3)

	// Order preserved (already ranked), one row per session, best hit wins.
	require.Equal(t, "s1", out.Hits[0].SessionID)
	require.Equal(t, "m1", out.Hits[0].MessageID)
	require.Equal(t, "best s1", out.Hits[0].Snippet)
	require.InDelta(t, 0.9, out.Hits[0].Score, 1e-9)
	require.Equal(t, "s2", out.Hits[1].SessionID)
	require.Equal(t, "s3", out.Hits[2].SessionID)

	// Workspace tags are not stamped here; the backend does that.
	require.Empty(t, out.Hits[0].WorkspaceID)
}

func TestCollapseToSessionsEmpty(t *testing.T) {
	t.Parallel()

	out := collapseToSessions(embedding.SearchResult{}, 50)
	require.Equal(t, 0, out.Total)
	require.Empty(t, out.Hits)
	require.False(t, out.SemanticUsed)
}

func TestCollapseToSessionsCapsToSessionLimit(t *testing.T) {
	t.Parallel()

	now := time.Unix(1, 0)
	hits := make([]embedding.Hit, 0, 6)
	for i := range 6 {
		hits = append(hits, embedding.Hit{
			Rank:      i + 1,
			Score:     float64(6 - i),
			Match:     embedding.MatchExact,
			SessionID: string(rune('a' + i)),
			SourceID:  "m",
			CreatedAt: now,
		})
	}
	out := collapseToSessions(embedding.SearchResult{Hits: hits}, 3)

	// Six distinct sessions found, but capped to the limit; Total still
	// reports the distinct count found in the candidate window.
	require.Equal(t, 6, out.Total)
	require.Len(t, out.Hits, 3)
	require.Equal(t, "a", out.Hits[0].SessionID)
	require.Equal(t, "c", out.Hits[2].SessionID)
}
