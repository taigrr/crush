package embedding

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/db"
)

// Document is a candidate supplied by the caller (e.g. the message
// service). The embedding package owns the vector math and fusion; the
// caller owns the corpus.
type Document struct {
	SourceType   SourceType
	SourceID     string
	SessionID    string
	SessionTitle string
	Role         string
	CreatedAt    time.Time
	Body         string
}

// Hit is one ranked search result, shared by the agent tool, the CLI,
// and the TUI dialog.
type Hit struct {
	Rank         int       `json:"rank"`
	Score        float64   `json:"score"`
	Match        MatchType `json:"match"`
	SourceType   string    `json:"source_type"`
	SourceID     string    `json:"source_id"`
	SessionID    string    `json:"session_id"`
	SessionTitle string    `json:"session_title"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	Snippet      string    `json:"snippet"`
}

// SearchOptions tune a single search.
type SearchOptions struct {
	Limit  int
	Offset int
	// Semantic overrides the config default for this call: nil = use
	// config (hybrid_search), true/false force on/off.
	Semantic *bool
}

// Service stores embeddings and runs hybrid search.
type Service interface {
	// Enabled reports whether an embedder is configured.
	Enabled() bool
	// Signature returns the active embedding-space signature.
	Signature() string
	// Embed computes and stores a vector for one document's body under
	// the active signature. No-op when embeddings are disabled.
	Embed(ctx context.Context, src SourceType, sourceID, sessionID, text string) error
	// HasVector reports whether a vector already exists for the source
	// under the active signature.
	HasVector(ctx context.Context, src SourceType, sourceID string) (bool, error)
	// Search runs hybrid (substring + semantic) search over docs and
	// returns a fused, paginated ranking.
	Search(ctx context.Context, query string, docs []Document, opts SearchOptions) (SearchResult, error)
	// Reconcile drops vectors that don't match the active signature.
	Reconcile(ctx context.Context) error
	// PendingDocs returns the subset of docs that have no vector under
	// the active signature (i.e. what Backfill would embed). Empty when
	// embeddings are disabled.
	PendingDocs(ctx context.Context, docs []Document) ([]Document, error)
	// Backfill embeds every doc lacking a vector under the active
	// signature. It reports progress via the optional callback (done,
	// total) after each embed and stops early if ctx is cancelled,
	// returning the count embedded so far. No-op when disabled.
	Backfill(ctx context.Context, docs []Document, progress func(done, total int)) (int, error)
	// Counts returns (matching active signature, total) for status.
	Counts(ctx context.Context) (active int64, total int64, err error)
}

// SearchResult is a page of hits plus pagination metadata.
type SearchResult struct {
	Hits   []Hit
	Total  int
	Offset int
	// SemanticUsed reports whether the semantic signal actually
	// participated (false when disabled, no embedder, or it errored).
	SemanticUsed bool
	// Mode is the search mode actually used.
	Mode SearchMode
	// FallbackReason says why Mode is SearchModeText ("" when hybrid).
	FallbackReason FallbackReason
}

// SearchMode is how a search was actually ranked.
type SearchMode string

// Search modes.
const (
	// SearchModeHybrid fused the substring and vector signals.
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeText used the substring signal only.
	SearchModeText SearchMode = "text"
)

// FallbackReason explains a SearchModeText result.
type FallbackReason string

// Fallback reasons.
const (
	// FallbackNoEmbedder: no embedder is configured.
	FallbackNoEmbedder FallbackReason = "no_embedder"
	// FallbackDisabledByRequest: the caller turned the vector signal off.
	FallbackDisabledByRequest FallbackReason = "disabled_by_request"
	// FallbackDisabledByConfig: no override and hybrid search is off.
	FallbackDisabledByConfig FallbackReason = "disabled_by_config"
	// FallbackNoEmbeddings: no candidate document has a stored vector.
	FallbackNoEmbeddings FallbackReason = "no_embeddings"
	// FallbackEmbedError: embedding the query (or loading vectors) failed.
	FallbackEmbedError FallbackReason = "embed_error"
)

type service struct {
	store     *store
	cfg       *Config
	provider  ProviderParams
	signature string

	mu    sync.Mutex
	model fantasy.EmbeddingModel
}

// New builds a Service. When cfg is nil (no embedder configured) the
// service is inert: Embed is a no-op and Search degrades to substring.
// signature is the active embedding-space signature, computed by the
// caller from config.EmbeddingConfig so the algorithm lives in one
// place.
func New(q db.Querier, cfg *Config, provider ProviderParams, signature string) Service {
	return &service{
		store:     newStore(q),
		cfg:       cfg,
		provider:  provider,
		signature: signature,
	}
}

// Config mirrors the runtime fields the service needs from
// config.EmbeddingConfig, keeping this package a leaf (no import of
// internal/config).
type Config struct {
	Model      string
	Dimensions int64
	Hybrid     bool
}

func (s *service) Enabled() bool     { return s.cfg != nil }
func (s *service) Signature() string { return s.signature }

func (s *service) ensureModel(ctx context.Context) (fantasy.EmbeddingModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.model != nil {
		return s.model, nil
	}
	m, err := buildEmbeddingModel(ctx, s.provider, s.cfg.Model)
	if err != nil {
		return nil, err
	}
	s.model = m
	return m, nil
}

// embedText returns a single vector for text under the active model.
func (s *service) embedText(ctx context.Context, text string) ([]float32, error) {
	m, err := s.ensureModel(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := m.Embed(ctx, fantasy.EmbeddingCall{
		Input:      []string{text},
		Dimensions: s.cfg.Dimensions,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("embedding: model returned no vector")
	}
	return resp.Embeddings[0], nil
}

func (s *service) Embed(ctx context.Context, src SourceType, sourceID, sessionID, text string) error {
	if s.cfg == nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	vec, err := s.embedText(ctx, text)
	if err != nil {
		return err
	}
	return s.store.upsert(ctx, s.signature, src, sourceID, sessionID, 0, vec)
}

func (s *service) HasVector(ctx context.Context, src SourceType, sourceID string) (bool, error) {
	if s.cfg == nil {
		return false, nil
	}
	return s.store.has(ctx, s.signature, src, sourceID, 0)
}

func (s *service) Reconcile(ctx context.Context) error {
	if s.cfg == nil {
		return nil
	}
	return s.store.dropStale(ctx, s.signature)
}

// PendingDocs returns docs with no vector under the active signature.
func (s *service) PendingDocs(ctx context.Context, docs []Document) ([]Document, error) {
	if s.cfg == nil {
		return nil, nil
	}
	// Pull the set of already-embedded source ids once, rather than a
	// per-doc HasVector round trip.
	embedded, err := s.store.sourceIDSet(ctx, s.signature)
	if err != nil {
		return nil, err
	}
	var pending []Document
	for _, d := range docs {
		if strings.TrimSpace(d.Body) == "" {
			continue
		}
		if _, ok := embedded[d.SourceID]; ok {
			continue
		}
		pending = append(pending, d)
	}
	return pending, nil
}

// Backfill embeds every doc lacking a vector under the active signature.
func (s *service) Backfill(ctx context.Context, docs []Document, progress func(done, total int)) (int, error) {
	if s.cfg == nil {
		return 0, nil
	}
	pending, err := s.PendingDocs(ctx, docs)
	if err != nil {
		return 0, err
	}
	total := len(pending)
	done := 0
	for _, d := range pending {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := s.Embed(ctx, d.SourceType, d.SourceID, d.SessionID, d.Body); err != nil {
			// Skip individual failures; backfill is best-effort and
			// resumable (PendingDocs will return the misses next run).
			continue
		}
		done++
		if progress != nil {
			progress(done, total)
		}
	}
	return done, nil
}

func (s *service) Counts(ctx context.Context) (int64, int64, error) {
	active, err := s.store.countBySignature(ctx, s.signature)
	if err != nil {
		return 0, 0, err
	}
	total, err := s.store.countTotal(ctx)
	if err != nil {
		return 0, 0, err
	}
	return active, total, nil
}

// Search fuses substring and (optionally) semantic ranks over docs.
func (s *service) Search(ctx context.Context, query string, docs []Document, opts SearchOptions) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := max(opts.Offset, 0)

	// Exact (substring) ranks, preserving caller order (recency).
	needle := strings.ToLower(query)
	byID := make(map[string]Document, len(docs))
	var exact []ranked
	for _, d := range docs {
		byID[d.SourceID] = d
		idx := strings.Index(strings.ToLower(d.Body), needle)
		if idx < 0 {
			continue
		}
		exact = append(exact, ranked{key: d.SourceID, rank: len(exact) + 1})
	}

	// Semantic ranks, if enabled and available.
	semanticOn, reason := s.semanticEnabled(opts.Semantic)
	var semantic []ranked
	semanticUsed := false
	if semanticOn {
		sem, withVectors, err := s.semanticRanks(ctx, query, docs)
		switch {
		case err != nil:
			// Degrade to substring-only (spec §6.1).
			reason = FallbackEmbedError
		case withVectors == 0:
			semanticUsed = true
			reason = FallbackNoEmbeddings
		default:
			semantic = sem
			semanticUsed = true
		}
	}
	mode := SearchModeHybrid
	if reason != "" {
		mode = SearchModeText
	}

	fusedList := reciprocalRankFusion(exact, semantic)

	total := len(fusedList)
	result := SearchResult{Total: total, Offset: offset, SemanticUsed: semanticUsed, Mode: mode, FallbackReason: reason}
	if offset >= total {
		return result, nil
	}
	end := min(offset+limit, total)
	page := fusedList[offset:end]

	hits := make([]Hit, 0, len(page))
	for i, f := range page {
		d, ok := byID[f.key]
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			Rank:         offset + i + 1,
			Score:        f.score,
			Match:        f.match,
			SourceType:   string(d.SourceType),
			SourceID:     d.SourceID,
			SessionID:    d.SessionID,
			SessionTitle: d.SessionTitle,
			Role:         d.Role,
			CreatedAt:    d.CreatedAt,
			Snippet:      snippet(d.Body, query),
		})
	}
	result.Hits = hits
	return result, nil
}

// semanticEnabled reports whether to run the vector signal, and if not,
// why.
func (s *service) semanticEnabled(override *bool) (bool, FallbackReason) {
	switch {
	case s.cfg == nil:
		return false, FallbackNoEmbedder
	case override != nil && !*override:
		return false, FallbackDisabledByRequest
	case override == nil && !s.cfg.Hybrid:
		return false, FallbackDisabledByConfig
	}
	return true, ""
}

// semanticRanks embeds the query and ranks the candidate docs that have
// stored vectors by cosine similarity (descending). It also reports how
// many candidates have a vector at all.
func (s *service) semanticRanks(ctx context.Context, query string, docs []Document) ([]ranked, int, error) {
	qvec, err := s.embedText(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	vectors, err := s.store.listBySignature(ctx, s.signature, "")
	if err != nil {
		return nil, 0, err
	}
	vecByID := make(map[string][]float32, len(vectors))
	for _, v := range vectors {
		vecByID[v.SourceID] = v.Vec
	}

	type scored struct {
		id    string
		score float64
	}
	var scoredList []scored
	withVectors := 0
	for _, d := range docs {
		vec, ok := vecByID[d.SourceID]
		if !ok {
			continue
		}
		withVectors++
		sim := cosineSimilarity(qvec, vec)
		// Only treat a doc as a semantic match above a small floor, so
		// unrelated documents that merely have a stored vector don't get
		// tagged "semantic"/"both" or feed noise into the fusion.
		if sim < semanticFloor {
			continue
		}
		scoredList = append(scoredList, scored{id: d.SourceID, score: sim})
	}
	slices.SortFunc(scoredList, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.id, b.id))
	})

	out := make([]ranked, len(scoredList))
	for i, sc := range scoredList {
		out[i] = ranked{key: sc.id, rank: i + 1}
	}
	return out, withVectors, nil
}
