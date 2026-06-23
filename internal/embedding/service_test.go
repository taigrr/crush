package embedding

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/fantasy"
)

// fakeEmbedder returns a fixed vector per input text via a lookup, so
// cosine ordering is deterministic in tests.
type fakeEmbedder struct {
	vecs map[string][]float32
}

func (f *fakeEmbedder) Embed(_ context.Context, call fantasy.EmbeddingCall) (*fantasy.EmbeddingResponse, error) {
	out := make([]fantasy.Embedding, len(call.Input))
	for i, in := range call.Input {
		out[i] = f.vecs[in]
	}
	return &fantasy.EmbeddingResponse{Embeddings: out}, nil
}
func (f *fakeEmbedder) Provider() string { return "fake" }
func (f *fakeEmbedder) Model() string    { return "fake" }

func newTestService(t *testing.T, hybrid bool) *service {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	q := db.New(conn)
	return &service{
		store:     newStore(q),
		cfg:       &Config{Model: "fake", Hybrid: hybrid},
		signature: "sig-1",
	}
}

func TestSearchHybridFusion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestService(t, true)

	// Query vector and three doc vectors. doc "m2" is closest to query.
	s.model = &fakeEmbedder{vecs: map[string][]float32{
		"token rotation": {1, 0, 0},
		"about tokens":   {0.9, 0.1, 0}, // closest to query
		"deploy steps":   {0, 1, 0},     // orthogonal
		"random chatter": {0, 0, 1},     // orthogonal
	}}

	// Store vectors under the active signature.
	require.NoError(t, s.store.upsert(ctx, s.signature, SourceMessage, "m1", "sess", 0, []float32{0.9, 0.1, 0}))
	require.NoError(t, s.store.upsert(ctx, s.signature, SourceMessage, "m2", "sess", 0, []float32{0, 1, 0}))
	require.NoError(t, s.store.upsert(ctx, s.signature, SourceMessage, "m3", "sess", 0, []float32{0, 0, 1}))

	docs := []Document{
		{SourceType: SourceMessage, SourceID: "m1", SessionID: "sess", Body: "about tokens"},
		{SourceType: SourceMessage, SourceID: "m2", SessionID: "sess", Body: "deploy steps"},
		{SourceType: SourceMessage, SourceID: "m3", SessionID: "sess", Body: "random chatter token rotation"},
	}

	res, err := s.Search(ctx, "token rotation", docs, SearchOptions{Limit: 10})
	require.NoError(t, err)
	require.True(t, res.SemanticUsed)

	// m3 matches exact substring ("token rotation"); m1 is the closest
	// semantic match. Both should appear; m3 (exact) and m1 (semantic)
	// outrank m2 (neither strong).
	ids := map[string]Hit{}
	for _, h := range res.Hits {
		ids[h.SourceID] = h
	}
	require.Contains(t, ids, "m3")
	require.Equal(t, MatchExact, ids["m3"].Match)
	require.Contains(t, ids, "m1")
	require.Equal(t, MatchSemantic, ids["m1"].Match)
	// Ranks are 1-based and sequential.
	require.Equal(t, 1, res.Hits[0].Rank)
}

func TestSearchSubstringOnlyWhenDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestService(t, false) // hybrid off
	s.model = &fakeEmbedder{}     // should never be called

	docs := []Document{
		{SourceType: SourceMessage, SourceID: "m1", Body: "exact needle here"},
		{SourceType: SourceMessage, SourceID: "m2", Body: "unrelated"},
	}
	res, err := s.Search(ctx, "needle", docs, SearchOptions{Limit: 10})
	require.NoError(t, err)
	require.False(t, res.SemanticUsed)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "m1", res.Hits[0].SourceID)
	require.Equal(t, MatchExact, res.Hits[0].Match)
}

func TestSearchPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestService(t, false)

	docs := []Document{
		{SourceID: "a", Body: "needle 1"},
		{SourceID: "b", Body: "needle 2"},
		{SourceID: "c", Body: "needle 3"},
	}
	res, err := s.Search(ctx, "needle", docs, SearchOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	require.Equal(t, 3, res.Total)
	require.Len(t, res.Hits, 1)
	require.Equal(t, 3, res.Hits[0].Rank)
}

func TestReconcileDropsStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestService(t, false)

	require.NoError(t, s.store.upsert(ctx, "sig-1", SourceMessage, "m1", "s", 0, []float32{1, 0}))
	require.NoError(t, s.store.upsert(ctx, "old-sig", SourceMessage, "m2", "s", 0, []float32{0, 1}))

	require.NoError(t, s.Reconcile(ctx))

	active, total, err := s.Counts(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), active)
	require.Equal(t, int64(1), total) // stale row dropped
}
