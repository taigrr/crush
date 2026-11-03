package historysearch

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

func TestResolveLimits(t *testing.T) {
	t.Parallel()
	sl, cl := ResolveLimits(0)
	require.Equal(t, DefaultSessionLimit, sl)
	require.Equal(t, max(DefaultSessionLimit*candidateFactor, minCandidates), cl)
	sl, _ = ResolveLimits(-5)
	require.Equal(t, DefaultSessionLimit, sl)
	sl, cl = ResolveLimits(3)
	require.Equal(t, 3, sl)
	require.Equal(t, minCandidates, cl)
	sl, cl = ResolveLimits(1_000_000)
	require.Equal(t, MaxSessionLimit, sl)
	require.Equal(t, MaxSessionLimit*candidateFactor, cl)
}

func TestMergeHits(t *testing.T) {
	t.Parallel()
	hits := []CrossHit{
		{Root: "/a", SessionID: "s1", Score: 0.4, Snippet: "a-s1-lo"},
		{Root: "/b", SessionID: "s1", Score: 0.9, Snippet: "b-s1"},
		{Root: "/a", SessionID: "s1", Score: 0.7, Snippet: "a-s1-hi"},
		{Root: "/a", SessionID: "s2", Score: 0.6, Snippet: "a-s2"},
	}
	out, total := MergeHits(hits, 50)
	require.Equal(t, 3, total)
	require.Equal(t, "/b", out[0].Root)
	require.Equal(t, "a-s1-hi", out[1].Snippet, "same session ID in two roots is not conflated; best hit wins")
	require.Equal(t, "s2", out[2].SessionID)

	capped, total := MergeHits(hits, 2)
	require.Equal(t, 3, total)
	require.Len(t, capped, 2)

	empty, total := MergeHits(nil, 50)
	require.Zero(t, total)
	require.Empty(t, empty)

	// Ties break by root, then session, whatever the arrival order.
	tied := []CrossHit{
		{Root: "/b", SessionID: "s2", Score: 0.5, MessageID: "m"},
		{Root: "/a", SessionID: "s2", Score: 0.5, MessageID: "m"},
		{Root: "/a", SessionID: "s1", Score: 0.5, MessageID: "m"},
		{Root: "/b", SessionID: "s1", Score: 0.5, MessageID: "m"},
	}
	want := []string{"/a s1", "/a s2", "/b s1", "/b s2"}
	for _, in := range [][]CrossHit{tied, {tied[3], tied[0], tied[2], tied[1]}} {
		out, _ := MergeHits(in, 50)
		got := make([]string, len(out))
		for i, h := range out {
			got[i] = h.Root + " " + h.SessionID
		}
		require.Equal(t, want, got)
	}
}

// vecEmbedder maps texts to fixed vectors (or fails).
type vecEmbedder struct {
	vecs map[string][]float32
	err  error
}

func (f *vecEmbedder) Embed(_ context.Context, call fantasy.EmbeddingCall) (*fantasy.EmbeddingResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]fantasy.Embedding, len(call.Input))
	for i, in := range call.Input {
		out[i] = f.vecs[in]
	}
	return &fantasy.EmbeddingResponse{Embeddings: out}, nil
}
func (f *vecEmbedder) Provider() string { return "fake" }
func (f *vecEmbedder) Model() string    { return "fake" }

// TestSearchAcrossModes covers hybrid search with a fake embedder and
// each text-only fallback, through the function every surface (the /v1
// route, the TUI, and `crush search`) calls.
func TestSearchAcrossModes(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	conn, err := db.Connect(ctx, db.TempDataDir(t))
	require.NoError(t, err)
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)
	sess, err := sessions.Create(ctx, "Auth work")
	require.NoError(t, err)
	exact, err := messages.Create(ctx, sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "token rotation is flaky"}}})
	require.NoError(t, err)
	other, err := sessions.Create(ctx, "Infra")
	require.NoError(t, err)
	near, err := messages.Create(ctx, other.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "credentials expire hourly"}}})
	require.NoError(t, err)

	model := &vecEmbedder{vecs: map[string][]float32{
		"token rotation":            {1, 0, 0},
		"token rotation is flaky":   {0.9, 0.1, 0},
		"credentials expire hourly": {0.95, 0.05, 0},
	}}
	params := embedding.Params{Configured: true, Model: "fake", Hybrid: true, Signature: "sig-test"}
	emb := embedding.Build(q, params)
	embedding.SetModelForTest(emb, model)
	require.NoError(t, emb.Embed(ctx, embedding.SourceMessage, exact.ID, sess.ID, "token rotation is flaky"))
	require.NoError(t, emb.Embed(ctx, embedding.SourceMessage, near.ID, other.ID, "credentials expire hourly"))

	src := func(e embedding.Service) []Source {
		return []Source{{Root: "/w", Messages: messages, Sessions: sessions, Emb: e}}
	}
	on, off := true, false

	res, err := SearchAcross(ctx, src(emb), "token rotation", ScopeUser, nil, 0, 10)
	require.NoError(t, err)
	require.Equal(t, embedding.SearchModeHybrid, res.Mode)
	require.Empty(t, res.FallbackReason)
	require.True(t, res.SemanticUsed)
	matches := map[string]embedding.MatchType{}
	for _, h := range res.Hits {
		matches[h.SessionID] = h.Match
	}
	require.Equal(t, embedding.MatchBoth, matches[sess.ID])
	require.Equal(t, embedding.MatchSemantic, matches[other.ID], "the vector signal finds what substring cannot")

	for _, tc := range []struct {
		name     string
		emb      embedding.Service
		semantic *bool
		reason   embedding.FallbackReason
		used     bool
	}{
		{"no embedder", embedding.Build(q, embedding.Params{}), nil, embedding.FallbackNoEmbedder, false},
		{"no embedder, forced on", embedding.Build(q, embedding.Params{}), &on, embedding.FallbackNoEmbedder, false},
		{"disabled by request", emb, &off, embedding.FallbackDisabledByRequest, false},
		{"disabled by config", withModel(embedding.Build(q, embedding.Params{Configured: true, Model: "fake", Signature: "sig-test"}), model), nil, embedding.FallbackDisabledByConfig, false},
		{"no embeddings", withModel(embedding.Build(q, embedding.Params{Configured: true, Model: "fake", Hybrid: true, Signature: "other-sig"}), model), nil, embedding.FallbackNoEmbeddings, true},
		{"embed error", withModel(embedding.Build(q, params), &vecEmbedder{err: errors.New("offline")}), nil, embedding.FallbackEmbedError, false},
	} {
		res, err := SearchAcross(ctx, src(tc.emb), "token rotation", ScopeUser, tc.semantic, 0, 10)
		require.NoError(t, err, tc.name)
		require.Equal(t, embedding.SearchModeText, res.Mode, tc.name)
		require.Equal(t, tc.reason, res.FallbackReason, tc.name)
		require.Equal(t, tc.used, res.SemanticUsed, tc.name)
		require.Len(t, res.Hits, 1, tc.name)
		require.Equal(t, embedding.MatchExact, res.Hits[0].Match, tc.name)
	}

	// Across sources, any hybrid source makes the result hybrid.
	mixed, err := SearchAcross(ctx, []Source{
		{Root: "/text", Messages: messages, Sessions: sessions, Emb: embedding.Build(q, embedding.Params{})},
		{Root: "/hybrid", Messages: messages, Sessions: sessions, Emb: emb},
	}, "token rotation", ScopeUser, nil, 0, 10)
	require.NoError(t, err)
	require.Equal(t, embedding.SearchModeHybrid, mixed.Mode)
	require.Empty(t, mixed.FallbackReason)
}

func withModel(e embedding.Service, m fantasy.EmbeddingModel) embedding.Service {
	embedding.SetModelForTest(e, m)
	return e
}
