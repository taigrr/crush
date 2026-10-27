package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectSharesConnectionAcrossSymlinkAliases(t *testing.T) {
	t.Cleanup(ResetPool)
	real := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(real, 0o755))
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(real, alias))

	a, err := Connect(t.Context(), filepath.Join(real, ".crush"), WithDataDirLock(true))
	require.NoError(t, err)
	b, err := Connect(t.Context(), filepath.Join(alias, ".crush"), WithDataDirLock(true))
	require.NoError(t, err, "a symlinked alias must reuse the pooled connection, not fight its own lock")
	require.Same(t, a, b)
	require.NoError(t, Release(filepath.Join(alias, ".crush")))
	require.NoError(t, Release(filepath.Join(real, ".crush")))
}
