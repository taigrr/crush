package embedding

import (
	"context"
	"fmt"
	"os"

	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/bedrock"
	"github.com/taigrr/fantasy/providers/openai"
)

// ProviderParams carries the resolved provider settings needed to build
// an embedding model, decoupled from config.ConfigStore so the package
// has no dependency on config internals.
type ProviderParams struct {
	// Type is the provider family ("bedrock", "openai", "openai-compat").
	Type string
	// APIKey is the resolved API key (may be empty for Bedrock, which
	// can fall back to AWS_BEARER_TOKEN_BEDROCK or the default chain).
	APIKey string
	// BaseURL optionally overrides the provider endpoint.
	BaseURL string
}

// buildEmbeddingModel constructs a fantasy.EmbeddingModel for the given
// provider and model id. Only providers whose fantasy implementation
// supports embeddings are accepted; others return an error.
func buildEmbeddingModel(ctx context.Context, p ProviderParams, modelID string) (fantasy.EmbeddingModel, error) {
	var provider fantasy.Provider
	var err error

	switch p.Type {
	case bedrock.Name:
		var opts []bedrock.Option
		switch {
		case p.APIKey != "":
			opts = append(opts, bedrock.WithAPIKey(p.APIKey))
		case os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "":
			opts = append(opts, bedrock.WithAPIKey(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")))
		}
		if p.BaseURL == "" {
			p.BaseURL = os.Getenv("AWS_ENDPOINT_URL_BEDROCK")
		}
		if p.BaseURL != "" {
			opts = append(opts, bedrock.WithBaseURL(p.BaseURL))
		}
		provider, err = bedrock.New(opts...)
	case openai.Name, "openai-compat", "":
		opts := []openai.Option{}
		if p.APIKey != "" {
			opts = append(opts, openai.WithAPIKey(p.APIKey))
		}
		if p.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(p.BaseURL))
		}
		provider, err = openai.New(opts...)
	default:
		return nil, fmt.Errorf("embedding: provider type %q does not support embeddings", p.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("embedding: failed to build provider: %w", err)
	}

	ep, ok := provider.(fantasy.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("embedding: provider %q does not implement embeddings", p.Type)
	}
	return ep.EmbeddingModel(ctx, modelID)
}
