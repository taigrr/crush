package agent

import (
	"os"
	"slices"
	"strconv"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/bedrock"
	"github.com/taigrr/fantasy/providers/vercel"
)

func (a *sessionAgent) getCacheControlOptions() fantasy.ProviderOptions {
	if t, _ := strconv.ParseBool(os.Getenv("CRUSH_DISABLE_ANTHROPIC_CACHE")); t {
		return fantasy.ProviderOptions{}
	}
	return fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		bedrock.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		vercel.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
}

// useExtendedContext returns true if the current request should use extended (1M) context mode.
// This is based on the model's context_mode setting and the current session state.
func (a *sessionAgent) useExtendedContext(sessionID string, model Model) bool {
	if !model.CatwalkCfg.Supports1MContext {
		return false
	}
	switch model.ModelCfg.ContextMode {
	case config.ContextModeExtended:
		return true
	case config.ContextModeDynamic:
		inExtended, _ := a.extendedContextMode.Get(sessionID)
		return inExtended
	default:
		return false
	}
}

// IsExtendedContext returns whether the given session is currently using
// extended (1M) context mode.
func (a *sessionAgent) IsExtendedContext(sessionID string) bool {
	model := a.largeModel.Get()
	return a.useExtendedContext(sessionID, model)
}

// addExtendedContextBeta injects the 1M context beta flag into the provider options.
// It returns a new ProviderOptions map with the beta flag added for anthropic/bedrock providers.
func addExtendedContextBeta(opts fantasy.ProviderOptions) fantasy.ProviderOptions {
	if opts == nil {
		opts = fantasy.ProviderOptions{}
	}

	// Helper to add beta flag to anthropic options.
	addBeta := func(existing *anthropic.ProviderOptions) *anthropic.ProviderOptions {
		if existing == nil {
			return &anthropic.ProviderOptions{Betas: []string{extendedContextBetaFlag}}
		}
		// Avoid duplicate beta flags.
		if slices.Contains(existing.Betas, extendedContextBetaFlag) {
			return existing
		}
		existing.Betas = append(existing.Betas, extendedContextBetaFlag)
		return existing
	}

	// Add beta flag for both anthropic and bedrock providers.
	if existing, ok := opts[anthropic.Name].(*anthropic.ProviderOptions); ok {
		opts[anthropic.Name] = addBeta(existing)
	} else {
		opts[anthropic.Name] = addBeta(nil)
	}

	if existing, ok := opts[bedrock.Name].(*anthropic.ProviderOptions); ok {
		opts[bedrock.Name] = addBeta(existing)
	} else {
		opts[bedrock.Name] = addBeta(nil)
	}

	return opts
}
