package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/taigrr/crush/internal/embedding"
)

// EmbeddingConfig selects the single, global text-embedding model used
// for vector and hybrid history search. It is intentionally NOT part of
// [Config.Models]: embeddings are a singular, global choice (not a
// per-task selection) and must never be overridden per workspace, since
// a per-project embedder would fragment the embedding space and produce
// incomparable vectors.
type EmbeddingConfig struct {
	// Provider is the provider id, matching a key in the providers
	// config (same semantics as SelectedModel.Provider).
	Provider string `json:"provider" jsonschema:"required,description=The embedding model provider id that matches a key in the providers config,example=bedrock"`
	// Model is the embedding model id as used by the provider API.
	Model string `json:"model" jsonschema:"required,description=The embedding model id as used by the provider API,example=amazon.titan-embed-text-v2:0"`
	// Dimensions optionally requests a specific output dimensionality for
	// models that support it. Zero means the model default.
	Dimensions int64 `json:"dimensions,omitempty" jsonschema:"description=Requested output vector dimensions for models that support it,example=1024"`
	// Normalize requests unit-normalized vectors (so cosine reduces to a
	// dot product) for models that support it.
	Normalize bool `json:"normalize,omitempty" jsonschema:"description=Request unit-normalized embedding vectors"`
	// HybridSearch controls whether search surfaces fuse the semantic
	// (vector) signal with substring matching. Nil means the default
	// (true when an embedder is configured). When false, search is pure
	// substring and no query embeddings are computed. Toggling this does
	// NOT invalidate stored vectors (it is not part of the signature).
	HybridSearch *bool `json:"hybrid_search,omitempty" jsonschema:"description=Fuse semantic vector search with substring search (default true)"`
}

// Signature returns the embedding-space identity: a stable hash of the
// fields that determine vector comparability (provider, model,
// dimensions, normalize). Any change invalidates all stored vectors.
// HybridSearch is deliberately excluded.
func (e *EmbeddingConfig) Signature() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(e.Provider)
	b.WriteByte('\n')
	b.WriteString(e.Model)
	b.WriteByte('\n')
	b.WriteString(strconv.FormatInt(e.Dimensions, 10))
	b.WriteByte('\n')
	b.WriteString(strconv.FormatBool(e.Normalize))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// HybridEnabled reports whether the semantic signal should participate
// in search. It defaults to true when an embedder is configured and the
// flag is unset.
func (e *EmbeddingConfig) HybridEnabled() bool {
	if e == nil {
		return false
	}
	if e.HybridSearch == nil {
		return true
	}
	return *e.HybridSearch
}

// Validate checks the embedding config for required fields.
func (e *EmbeddingConfig) Validate() error {
	if e == nil {
		return nil
	}
	if e.Provider == "" {
		return fmt.Errorf("embedding.provider is required")
	}
	if e.Model == "" {
		return fmt.Errorf("embedding.model is required")
	}
	if e.Dimensions < 0 {
		return fmt.Errorf("embedding.dimensions must be non-negative")
	}
	return nil
}

// EmbeddingProviderParams resolves the provider type, API key, and base
// URL for the configured embedder from the store's providers and
// variable resolver. Returns ok=false when no embedder is configured or
// its provider is unknown/disabled.
func (s *ConfigStore) EmbeddingProviderParams() (typ, apiKey, baseURL string, ok bool) {
	cfg := s.config
	if cfg == nil || cfg.Embedding == nil {
		return "", "", "", false
	}
	pc, found := cfg.Providers.Get(cfg.Embedding.Provider)
	if !found {
		return "", "", "", false
	}
	apiKey, _ = s.Resolve(pc.APIKey)
	baseURL, _ = s.Resolve(pc.BaseURL)
	typ = string(pc.Type)
	if typ == "" {
		typ = cfg.Embedding.Provider
	}
	return typ, apiKey, baseURL, true
}

// EmbeddingParams resolves everything the embedding package needs to
// build its Service from the active config.
func (s *ConfigStore) EmbeddingParams() embedding.Params {
	cfg := s.config
	if cfg == nil || cfg.Embedding == nil {
		return embedding.Params{Configured: false}
	}
	typ, apiKey, baseURL, ok := s.EmbeddingProviderParams()
	if !ok {
		return embedding.Params{Configured: false}
	}
	return embedding.Params{
		Configured: true,
		Model:      cfg.Embedding.Model,
		Dimensions: cfg.Embedding.Dimensions,
		Hybrid:     cfg.Embedding.HybridEnabled(),
		Signature:  cfg.Embedding.Signature(),
		Provider: embedding.ProviderParams{
			Type:    typ,
			APIKey:  apiKey,
			BaseURL: baseURL,
		},
	}
}
