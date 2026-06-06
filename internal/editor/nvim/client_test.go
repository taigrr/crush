package nvim

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/editor"
)

func TestEnvLookup_Precedence(t *testing.T) {
	env := []string{"FOO=bar", "NVIM_LISTEN_ADDRESS=/tmp/legacy", "NVIM=/tmp/preferred"}
	require.Equal(t, "/tmp/preferred", detectAddressFrom(envLookup(env)))

	env = []string{"NVIM_LISTEN_ADDRESS=/tmp/legacy"}
	require.Equal(t, "/tmp/legacy", detectAddressFrom(envLookup(env)))

	require.Equal(t, "", detectAddressFrom(envLookup([]string{"FOO=bar"})))
	require.Equal(t, "", detectAddressFrom(envLookup(nil)))
}

func TestNewFromEnv_NoEnv_ReturnsNotOK(t *testing.T) {
	b, ok := NewFromEnv([]string{"FOO=bar"})
	require.False(t, ok)
	require.Nil(t, b)
}

func TestNewFromEnv_BadAddress_ReturnsNotOK(t *testing.T) {
	b, ok := NewFromEnv([]string{"NVIM=/tmp/neocrush-bridge-test-does-not-exist-"})
	require.False(t, ok)
	require.Nil(t, b)
}

// Confirm Noop satisfies the interface and matches our error contract.
func TestNoopBridge(t *testing.T) {
	var b editor.Bridge = editor.Noop{}
	require.False(t, b.Available())

	_, err := b.Context(t.Context())
	require.ErrorIs(t, err, editor.ErrUnavailable)

	require.ErrorIs(t, b.ShowLocations(t.Context(), "t", []editor.Location{{Filename: "x", Line: 1}}), editor.ErrUnavailable)
	// Best-effort no-ops must not error.
	require.NoError(t, b.FlashEdit(t.Context(), "x", 0, 1))
	require.NoError(t, b.NotifyFileChanged(t.Context(), "x"))
	require.NoError(t, b.Close())
}
