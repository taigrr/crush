package transfer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckWritable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, CheckWritable(dir))
	require.NoError(t, CheckWritable(filepath.Join(dir, "a", "b", "c")))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "the probe must clean up after itself")

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	require.ErrorIs(t, CheckWritable(filepath.Join(file, "x")), ErrNotWritable)

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	ro := filepath.Join(dir, "ro")
	require.NoError(t, os.Mkdir(ro, 0o555))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	require.ErrorIs(t, CheckWritable(filepath.Join(ro, "Store1", "proj")), ErrNotWritable)
}
