package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSTTWSURL(t *testing.T) {
	t.Parallel()
	for base, want := range map[string]string{
		"https://api.x.ai":     "wss://api.x.ai/v1/stt",
		"api.x.ai":             "wss://api.x.ai/v1/stt",
		"https://api.x.ai/v1":  "wss://api.x.ai/v1/stt",
		"https://proxy/xai/v1": "wss://proxy/xai/v1/stt",
	} {
		got, err := Config{APIBase: base, STTWSPath: "/v1/stt"}.STTWSURL()
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}
	for _, base := range []string{"http://localhost:8080", "ws://localhost:8080"} {
		_, err := Config{APIBase: base, STTWSPath: "/v1/stt"}.STTWSURL()
		var ve *Error
		require.ErrorAs(t, err, &ve, base)
		require.Equal(t, ErrConfig, ve.Kind, "plaintext must be rejected, never downgraded")
	}
}

func TestSTTURLNeverSendsAutoLanguage(t *testing.T) {
	t.Parallel()
	u, err := buildSTTWSURL(Config{Language: "auto"}.Normalize())
	require.NoError(t, err)
	require.NotContains(t, u, "language=auto")
	require.Contains(t, u, "language=")
}
