package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/env"
	"github.com/taigrr/fantasy/providers/google"
)

func TestConfig_configureProvidersGoogleBuildTag(t *testing.T) {
	knownProviders := []catwalk.Provider{
		{
			ID:          catwalk.InferenceProviderGemini,
			Type:        catwalk.TypeGoogle,
			APIKey:      "$GEMINI_API_KEY",
			APIEndpoint: "https://generativelanguage.googleapis.com",
			Models:      []catwalk.Model{{ID: "gemini-pro"}},
		},
		{
			ID:     catwalk.InferenceProviderVertexAI,
			Type:   catwalk.TypeVertexAI,
			Models: []catwalk.Model{{ID: "gemini-pro"}},
		},
		{
			ID:          catwalk.InferenceProviderAnthropic,
			Type:        catwalk.TypeAnthropic,
			APIKey:      "$ANTHROPIC_API_KEY",
			APIEndpoint: "https://api.anthropic.com/v1",
			Models:      []catwalk.Model{{ID: "claude"}},
		},
	}

	cfg := &Config{}
	cfg.setDefaults("/tmp", "")
	cfg.Providers.Set("my-gemini", ProviderConfig{
		Type:    catwalk.TypeGoogle,
		APIKey:  "key",
		BaseURL: "https://example.com",
		Models:  []catwalk.Model{{ID: "gemini-custom"}},
	})
	env := env.NewFromMap(map[string]string{
		"GEMINI_API_KEY":    "test-key",
		"ANTHROPIC_API_KEY": "test-key",
		"VERTEXAI_PROJECT":  "test-project",
		"VERTEXAI_LOCATION": "us-central1",
	})
	resolver := NewShellVariableResolver(env)
	require.NoError(t, cfg.configureProviders(testStore(cfg), env, resolver, knownProviders))

	_, hasAnthropic := cfg.Providers.Get(string(catwalk.InferenceProviderAnthropic))
	require.True(t, hasAnthropic, "non-Google providers must always be kept")

	_, hasGemini := cfg.Providers.Get(string(catwalk.InferenceProviderGemini))
	_, hasVertex := cfg.Providers.Get(string(catwalk.InferenceProviderVertexAI))
	_, hasCustom := cfg.Providers.Get("my-gemini")
	require.Equal(t, google.Enabled, hasGemini, "gemini presence must follow the fantasy_google build tag")
	require.Equal(t, google.Enabled, hasVertex, "vertex presence must follow the fantasy_google build tag")
	require.Equal(t, google.Enabled, hasCustom, "custom google-type provider presence must follow the fantasy_google build tag")
}
