package embedding

import (
	"context"
	"fmt"
	"sort"
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
}

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
	semanticOn := s.semanticEnabled(opts.Semantic)
	var semantic []ranked
	semanticUsed := false
	if semanticOn {
		sem, err := s.semanticRanks(ctx, query, docs)
		if err == nil {
			semantic = sem
			semanticUsed = true
		}
		// On error we silently degrade to substring-only (spec §6.1).
	}

	fusedList := reciprocalRankFusion(exact, semantic)

	total := len(fusedList)
	result := SearchResult{Total: total, Offset: offset, SemanticUsed: semanticUsed}
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

func (s *service) semanticEnabled(override *bool) bool {
	if s.cfg == nil {
		return false
	}
	if override != nil {
		return *override
	}
	return s.cfg.Hybrid
}

// semanticRanks embeds the query and ranks the candidate docs that have
// stored vectors by cosine similarity (descending).
func (s *service) semanticRanks(ctx context.Context, query string, docs []Document) ([]ranked, error) {
	qvec, err := s.embedText(ctx, query)
	if err != nil {
		return nil, err
	}
	vectors, err := s.store.listBySignature(ctx, s.signature, "")
	if err != nil {
		return nil, err
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
	for _, d := range docs {
		vec, ok := vecByID[d.SourceID]
		if !ok {
			continue
		}
		sim := cosineSimilarity(qvec, vec)
		// Only treat a doc as a semantic match above a small floor, so
		// unrelated documents that merely have a stored vector don't get
		// tagged "semantic"/"both" or feed noise into the fusion.
		if sim < semanticFloor {
			continue
		}
		scoredList = append(scoredList, scored{id: d.SourceID, score: sim})
	}
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].score != scoredList[j].score {
			return scoredList[i].score > scoredList[j].score
		}
		return scoredList[i].id < scoredList[j].id
	})

	out := make([]ranked, len(scoredList))
	for i, sc := range scoredList {
		out[i] = ranked{key: sc.id, rank: i + 1}
	}
	return out, nil
}
