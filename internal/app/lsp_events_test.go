package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/lsp"
)

// TestLSPStatesArePerWorkspace guards against LSP state leaking between
// workspaces hosted by one server: each App must report only the LSP
// clients it started.
func TestLSPStatesArePerWorkspace(t *testing.T) {
	t.Parallel()
	first := NewForTest(t.Context())
	second := NewForTest(t.Context())

	first.lsp.updateLSPState("gopls", lsp.StateReady, nil, nil, 0)

	require.Contains(t, first.GetLSPStates(), "gopls")
	require.Empty(t, second.GetLSPStates(), "another workspace's LSP must not appear here")
}
