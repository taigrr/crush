package checkpoint

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/stretchr/testify/require"
)

func writeIgnoreFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		".gitignore":              "node_modules\n# c\n\n*.log\n",
		".git/info/exclude":       "secret\n",
		"a/.gitignore":            "build/\n!keep.log\n",
		"a/b/.gitignore":          "tmp\n",
		"node_modules/.gitignore": "never-read\n",
		"a/build/.gitignore":      "never-read\n",
		"c/d/x.txt":               "x",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

func requireMatchesFresh(t *testing.T, dir string) {
	t.Helper()
	want, err := gitignore.ReadPatterns(osfs.New(dir), nil)
	require.NoError(t, err)
	got, err := readIgnorePatterns(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func bumpMtime(t *testing.T, p string) {
	t.Helper()
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(p, future, future))
}

func TestReadIgnorePatternsMatchesGoGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIgnoreFixture(t, dir)
	requireMatchesFresh(t, dir)
	requireMatchesFresh(t, dir) // Cache hit.
}

func TestReadIgnorePatternsInvalidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIgnoreFixture(t, dir)
	requireMatchesFresh(t, dir)

	// Edit.
	p := filepath.Join(dir, "a", "b", ".gitignore")
	require.NoError(t, os.WriteFile(p, []byte("tmp\nother\n"), 0o644))
	bumpMtime(t, p)
	requireMatchesFresh(t, dir)

	// Add in an existing dir.
	p = filepath.Join(dir, "c", "d", ".gitignore")
	require.NoError(t, os.WriteFile(p, []byte("*.txt\n"), 0o644))
	requireMatchesFresh(t, dir)

	// Add in a new dir.
	p = filepath.Join(dir, "e", "f", ".gitignore")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("z\n"), 0o644))
	requireMatchesFresh(t, dir)

	// Remove.
	require.NoError(t, os.Remove(filepath.Join(dir, "a", ".gitignore")))
	requireMatchesFresh(t, dir)
	got, err := readIgnorePatterns(dir)
	require.NoError(t, err)
	// Now-unignored a/build/.gitignore must be picked up.
	want, _ := gitignore.ReadPatterns(osfs.New(dir), nil)
	require.Equal(t, want, got)

	// Exclude file edit.
	p = filepath.Join(dir, ".git", "info", "exclude")
	require.NoError(t, os.WriteFile(p, []byte("secret\nmore\n"), 0o644))
	requireMatchesFresh(t, dir)
}

func TestInitRepoFirstSnapshotWaitsForIgnoreRevalidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(body), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	write("a/\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bb"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "x.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bb", "y.txt"), []byte("y"), 0o644))

	_, err := InitRepoAt(dir, dir, DefaultConfig())
	require.NoError(t, err)
	require.True(t, hasCachedIgnore(dir))

	write("bb/\n")
	r, err := InitRepoAt(dir, dir, DefaultConfig())
	require.NoError(t, err)
	require.NotNil(t, r.loadIgnorer)
	hash, err := r.CreateSnapshot("first")
	require.NoError(t, err)
	require.NotNil(t, r.ignorer)
	require.True(t, r.ignorer.Match([]string{"bb"}, true))
	require.False(t, r.ignorer.Match([]string{"a"}, true))

	out := t.TempDir()
	require.NoError(t, r.RestoreSnapshot(hash, out))
	require.FileExists(t, filepath.Join(out, "a", "x.txt"))
	require.NoFileExists(t, filepath.Join(out, "bb", "y.txt"))
}

func TestIgnoreCacheBounded(t *testing.T) {
	var s ignoreCacheStore
	for i := range ignoreCacheMaxEntries * 3 {
		s.Store(filepath.Join("/x", strconv.Itoa(i)), &ignoreEntry{})
	}
	require.LessOrEqual(t, s.Len(), ignoreCacheMaxEntries)
	s.Store("/keep", &ignoreEntry{})
	_, ok := s.Load("/keep")
	require.True(t, ok)
}

func TestReadIgnorePatternsErrorNotSticky(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing")
	_, err := readIgnorePatterns(dir)
	require.Error(t, err)
	require.False(t, hasCachedIgnore(dir))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("x\n"), 0o644))
	requireMatchesFresh(t, dir)
	require.True(t, hasCachedIgnore(dir))
}

func TestReadIgnorePatternsNewNestedIgnore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIgnoreFixture(t, dir)
	requireMatchesFresh(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c", "d", ".gitignore"), []byte("x.txt\n"), 0o644))
	requireMatchesFresh(t, dir)
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "a", "b")))
	requireMatchesFresh(t, dir)
}

func TestIgnoreCacheLRU(t *testing.T) {
	var s ignoreCacheStore
	for i := range ignoreCacheMaxEntries {
		s.Store(filepath.Join("/x", strconv.Itoa(i)), &ignoreEntry{})
	}
	_, ok := s.Load(filepath.Join("/x", "0"))
	require.True(t, ok)
	s.Store("/new", &ignoreEntry{})
	_, ok = s.Load(filepath.Join("/x", "0"))
	require.True(t, ok)
	_, ok = s.Load(filepath.Join("/x", "1"))
	require.False(t, ok)
	require.Equal(t, ignoreCacheMaxEntries, s.Len())
}
