package fsext

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGlobContext(t *testing.T) {
	t.Run("cancelled context stops the walk and returns ctx error", func(t *testing.T) {
		testDir := t.TempDir()
		for i := range 50 {
			dir := filepath.Join(testDir, "d", string(rune('a'+i%26)), "sub")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		matches, truncated, err := GlobGitignoreAwareContext(ctx, "**/*.txt", testDir, 0)
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, truncated)
		require.Empty(t, matches)
	})

	t.Run("does not follow directory symlinks", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks require privileges on windows")
		}
		testDir := t.TempDir()
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.go"), []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(testDir, "here.go"), []byte("x"), 0o644))
		require.NoError(t, os.Symlink(outside, filepath.Join(testDir, "link")))

		matches, _, err := GlobGitignoreAwareContext(context.Background(), "**/*.go", testDir, 0)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{filepath.Join(testDir, "here.go")}, matches)
	})
}
