package config

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetProviderState() {
	providerOnce = sync.Once{}
	providerList = nil
}

func TestProviders_ReturnsEmbedded(t *testing.T) {
	resetProviderState()
	defer resetProviderState()

	cfg := &Config{
		Options: &Options{},
	}

	providers, err := Providers(cfg)
	require.NoError(t, err)
	require.NotNil(t, providers)
	require.Greater(t, len(providers), 5, "Expected embedded providers")
}

func TestProviders_CalledMultipleTimesReturnsSame(t *testing.T) {
	resetProviderState()
	defer resetProviderState()

	cfg := &Config{
		Options: &Options{},
	}

	providers1, err := Providers(cfg)
	require.NoError(t, err)

	providers2, err := Providers(cfg)
	require.NoError(t, err)

	require.Equal(t, len(providers1), len(providers2))
}

// Regression: disable_default_providers must suppress the embedded
// catalog here, not just in setupProviders. When it didn't, the model
// picker still listed every embedded provider (anthropic, bedrock, ...)
// and choosing one of their models prompted for an API key the user
// never configured.
func TestProviders_DisableDefaultProvidersSuppressesCatalog(t *testing.T) {
	resetProviderState()
	defer resetProviderState()

	providers, err := Providers(&Config{
		Options: &Options{DisableDefaultProviders: true},
	})
	require.NoError(t, err)
	require.Empty(t, providers, "embedded catalog must be suppressed when disabled")
}

// Callers pass configs from several layers; a nil config or nil Options
// must not panic and must fall back to the default catalog.
func TestProviders_NilSafe(t *testing.T) {
	resetProviderState()
	defer resetProviderState()

	providers, err := Providers(nil)
	require.NoError(t, err)
	require.NotEmpty(t, providers)

	providers, err = Providers(&Config{})
	require.NoError(t, err)
	require.NotEmpty(t, providers)
}
