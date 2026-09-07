package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultSTTWSUsesWSS(t *testing.T) {
	t.Parallel()
	url, err := DefaultConfig().STTWSURL()
	require.NoError(t, err)
	require.Equal(t, "wss://api.x.ai/v1/stt", url)
}

func TestSchemeLessAndWSSBases(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"api.x.ai", "wss://api.x.ai", "HTTPS://api.x.ai"} {
		cfg := DefaultConfig()
		cfg.APIBase = base
		url, err := cfg.STTWSURL()
		require.NoError(t, err, base)
		require.Equal(t, "wss://api.x.ai/v1/stt", url, base)
	}
}

func TestV1BaseDedupesDefaultPath(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.APIBase = "https://proxy.example.com/v1"
	url, err := cfg.STTWSURL()
	require.NoError(t, err)
	require.Equal(t, "wss://proxy.example.com/v1/stt", url)
}

func TestXaiV1BasePreservesPrefix(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.APIBase = "https://proxy.example.com/xai/v1"
	url, err := cfg.STTWSURL()
	require.NoError(t, err)
	require.Equal(t, "wss://proxy.example.com/xai/v1/stt", url)
}

func TestRejectsPlaintextBases(t *testing.T) {
	t.Parallel()
	for _, base := range []string{
		"http://localhost:8080",
		"ws://localhost:8080",
		"HTTP://localhost:8080",
		"Ws://localhost:8080",
	} {
		cfg := DefaultConfig()
		cfg.APIBase = base
		_, err := cfg.STTWSURL()
		require.Error(t, err, base)
	}
}

func TestSTTURLIncludesQueryParams(t *testing.T) {
	t.Parallel()
	url, err := buildSTTWSURL(DefaultConfig())
	require.NoError(t, err)
	require.Contains(t, url, "sample_rate=16000")
	require.Contains(t, url, "encoding=pcm")
	require.Contains(t, url, "language=en")
}

func TestSTTURLResolvesAutoToConcreteLanguage(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Language = "auto"
	url, err := buildSTTWSURL(cfg)
	require.NoError(t, err)
	require.NotContains(t, url, "language=auto")
	require.Contains(t, url, "language=")
}

func TestSTTURLPassesThroughCatalogLanguage(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Language = "ja"
	url, err := buildSTTWSURL(cfg)
	require.NoError(t, err)
	require.Contains(t, url, "language=ja")
}
