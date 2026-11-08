package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServableRoot(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.False(t, servableRoot(home))
	require.False(t, servableRoot("/"))
	require.False(t, servableRoot(filepath.Join(home, "Library")))
	require.False(t, servableRoot(filepath.Join(home, "Library", "Containers", "x")))
	require.True(t, servableRoot(filepath.Join(home, "code", "project")))
	require.True(t, servableRoot(filepath.Join(home, "Libraryish")))
}
