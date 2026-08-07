package backend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/proto"
)

func TestTagHits(t *testing.T) {
	t.Parallel()

	now := time.Unix(10, 0)
	in := []embedding.Hit{
		{Score: 0.5, Match: embedding.MatchExact, SessionID: "s1", SessionTitle: "One", SourceID: "m1", Role: "user", CreatedAt: now, Snippet: "hi"},
	}
	out := tagHits(in, "wsid", "/root")
	require.Len(t, out, 1)
	require.Equal(t, "wsid", out[0].WorkspaceID)
	require.Equal(t, "/root", out[0].WorkspaceRoot)
	require.Equal(t, "s1", out[0].SessionID)
	require.Equal(t, "m1", out[0].MessageID)
	require.Equal(t, "exact", out[0].Match)
	require.InDelta(t, 0.5, out[0].Score, 1e-9)
}

func TestMergeCrossWorkspaceHits_ReRanksAndTagsAcrossWorkspaces(t *testing.T) {
	t.Parallel()

	hits := []proto.SessionHit{
		{WorkspaceRoot: "/a", WorkspaceID: "a", SessionID: "s1", Score: 0.4, Snippet: "a-s1-lo"},
		{WorkspaceRoot: "/b", WorkspaceID: "b", SessionID: "s1", Score: 0.9, Snippet: "b-s1"},
		{WorkspaceRoot: "/a", WorkspaceID: "a", SessionID: "s1", Score: 0.7, Snippet: "a-s1-hi"},
		{WorkspaceRoot: "/a", WorkspaceID: "a", SessionID: "s2", Score: 0.6, Snippet: "a-s2"},
	}

	res := mergeCrossWorkspaceHits(hits, true, 50)

	require.True(t, res.SemanticUsed)
	// 3 distinct (root, session): (/b,s1), (/a,s1), (/a,s2).
	require.Equal(t, 3, res.Total)
	require.Len(t, res.Hits, 3)

	// Global re-rank by score desc.
	require.Equal(t, "/b", res.Hits[0].WorkspaceRoot)
	require.InDelta(t, 0.9, res.Hits[0].Score, 1e-9)

	// Same session id in two workspaces is NOT conflated; /a's s1 keeps
	// its best (0.7) representative, distinct from /b's s1.
	require.Equal(t, "/a", res.Hits[1].WorkspaceRoot)
	require.Equal(t, "s1", res.Hits[1].SessionID)
	require.Equal(t, "a-s1-hi", res.Hits[1].Snippet)

	require.Equal(t, "s2", res.Hits[2].SessionID)
}

func TestMergeCrossWorkspaceHits_CapsToSessionLimit(t *testing.T) {
	t.Parallel()

	hits := []proto.SessionHit{
		{WorkspaceRoot: "/a", SessionID: "s1", Score: 0.9},
		{WorkspaceRoot: "/a", SessionID: "s2", Score: 0.8},
		{WorkspaceRoot: "/b", SessionID: "s3", Score: 0.7},
	}
	res := mergeCrossWorkspaceHits(hits, false, 2)

	require.False(t, res.SemanticUsed)
	// Total counts all distinct sessions found; hits capped to the limit.
	require.Equal(t, 3, res.Total)
	require.Len(t, res.Hits, 2)
	require.Equal(t, "s1", res.Hits[0].SessionID)
	require.Equal(t, "s2", res.Hits[1].SessionID)
}

func TestMergeCrossWorkspaceHits_Empty(t *testing.T) {
	t.Parallel()

	res := mergeCrossWorkspaceHits(nil, false, 50)
	require.Equal(t, 0, res.Total)
	require.Empty(t, res.Hits)
	require.False(t, res.SemanticUsed)
}

// TestMergeCrossWorkspaceHits_DeterministicTieBreak asserts that hits with
// IDENTICAL fused scores (the common RRF case across workspaces) produce a
// stable ordering and stable cap survivors across repeated merges, and
// regardless of input arrival order. Ties break by root, then session id.
func TestMergeCrossWorkspaceHits_DeterministicTieBreak(t *testing.T) {
	t.Parallel()

	// All the same score → ordering is decided entirely by the tie-break.
	base := []proto.SessionHit{
		{WorkspaceRoot: "/b", SessionID: "s2", Score: 0.5, MessageID: "m"},
		{WorkspaceRoot: "/a", SessionID: "s2", Score: 0.5, MessageID: "m"},
		{WorkspaceRoot: "/a", SessionID: "s1", Score: 0.5, MessageID: "m"},
		{WorkspaceRoot: "/b", SessionID: "s1", Score: 0.5, MessageID: "m"},
	}
	want := []struct{ root, id string }{
		{"/a", "s1"}, {"/a", "s2"}, {"/b", "s1"}, {"/b", "s2"},
	}

	// Repeated merges of the same input are identical.
	var firstOrder []string
	for range 5 {
		res := mergeCrossWorkspaceHits(append([]proto.SessionHit(nil), base...), false, 50)
		require.Len(t, res.Hits, 4)
		order := make([]string, len(res.Hits))
		for i, h := range res.Hits {
			require.Equal(t, want[i].root, h.WorkspaceRoot)
			require.Equal(t, want[i].id, h.SessionID)
			order[i] = h.WorkspaceRoot + h.SessionID
		}
		if firstOrder == nil {
			firstOrder = order
		} else {
			require.Equal(t, firstOrder, order)
		}
	}

	// A different arrival order yields the same result.
	shuffled := []proto.SessionHit{base[3], base[0], base[2], base[1]}
	res := mergeCrossWorkspaceHits(shuffled, false, 50)
	for i, h := range res.Hits {
		require.Equal(t, want[i].root, h.WorkspaceRoot)
		require.Equal(t, want[i].id, h.SessionID)
	}

	// The cap keeps the deterministic top-2 (/a s1, /a s2), never a
	// random pair.
	capped := mergeCrossWorkspaceHits(append([]proto.SessionHit(nil), base...), false, 2)
	require.Len(t, capped.Hits, 2)
	require.Equal(t, 4, capped.Total)
	require.Equal(t, "/a", capped.Hits[0].WorkspaceRoot)
	require.Equal(t, "s1", capped.Hits[0].SessionID)
	require.Equal(t, "/a", capped.Hits[1].WorkspaceRoot)
	require.Equal(t, "s2", capped.Hits[1].SessionID)
}
