package checkpoint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newProjectDir returns a fresh temp directory pre-seeded with a stub
// .git directory so [checkpoint.InitRepo] accepts it. The guard added
// by the home-directory protection refuses to snapshot any path that
// is not a git repo, so tests must opt in to "looks like a project"
// explicitly.
func newProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	return dir
}
