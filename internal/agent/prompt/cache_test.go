package prompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestCachedGitStatusMatchesUncached(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	want := getGitStatus(ctx, dir)
	require.Equal(t, want, cachedGitStatus(ctx, dir))
	require.Equal(t, want, cachedGitStatus(ctx, dir))
}

func TestCachedGitStatusInvalidatesOnHeadAndIndex(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	before := cachedGitStatus(ctx, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	gitRun(t, dir, "add", "b.txt")
	staged := cachedGitStatus(ctx, dir)
	require.NotEqual(t, before, staged)
	require.Equal(t, getGitStatus(ctx, dir), staged)

	gitRun(t, dir, "commit", "-q", "-m", "second")
	committed := cachedGitStatus(ctx, dir)
	require.Contains(t, committed, "second")
	require.Equal(t, getGitStatus(ctx, dir), committed)

	gitRun(t, dir, "checkout", "-q", "-b", "other")
	require.Contains(t, cachedGitStatus(ctx, dir), "Current branch: other")
}

func TestCachedGitStatusTTL(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	base := time.Now()
	c := newInputCache[string]()
	c.now = func() time.Time { return base }
	c.refingerprint = gitStatusCache.refingerprint
	calls := 0
	get := func() string {
		return c.get(dir, func() string { return gitFingerprint(dir) }, func() string {
			calls++
			return getGitStatus(ctx, dir)
		})
	}
	get()
	get()
	require.Equal(t, 1, calls)
	base = base.Add(inputCacheTTL)
	get()
	require.Equal(t, 2, calls)
}

func TestCachedDiscoverSkillsInvalidates(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name string) {
		d := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: test skill "+name+"\n---\nbody\n"), 0o644))
	}
	writeSkill("alpha")
	paths := []string{root}
	first := cachedDiscoverSkills(paths)
	require.Len(t, first, 1)
	require.Len(t, cachedDiscoverSkills(paths), 1)

	writeSkill("beta")
	require.Len(t, cachedDiscoverSkills(paths), 2)
}

func TestInputCacheCoalescesConcurrentMisses(t *testing.T) {
	c := newInputCache[int]()
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, 16)
	for i := range results {
		wg.Go(func() {
			results[i] = c.get("id", func() string { return "k" }, func() int {
				calls.Add(1)
				<-release
				return 42
			})
		})
	}
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	for _, r := range results {
		require.Equal(t, 42, r)
	}
}

func TestInputCacheBounded(t *testing.T) {
	c := newInputCache[int]()
	for i := range inputCacheMaxEntries * 4 {
		c.get(strconv.Itoa(i), func() string { return "k" }, func() int { return i })
	}
	require.LessOrEqual(t, len(c.entries), inputCacheMaxEntries)
	require.Equal(t, 7, c.get("7x", func() string { return "k" }, func() int { return 7 }))
}

func TestCachedGitStatusLinkedWorktree(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	ctx := context.Background()
	require.Contains(t, cachedGitStatus(ctx, wt), "Current branch: side")
	require.Contains(t, cachedGitStatus(ctx, dir), "Current branch: main")

	require.NoError(t, os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b"), 0o644))
	gitRun(t, wt, "add", ".")
	gitRun(t, wt, "commit", "-q", "-m", "side commit")
	require.Equal(t, getGitStatus(ctx, wt), cachedGitStatus(ctx, wt))

	// A commit on main via the main checkout updates the shared ref.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "main commit")
	require.Equal(t, getGitStatus(ctx, dir), cachedGitStatus(ctx, dir))
	require.Equal(t, getGitStatus(ctx, wt), cachedGitStatus(ctx, wt))
}

func TestCachedDiscoverSkillsEditsAndDeletes(t *testing.T) {
	root := t.TempDir()
	write := func(name, desc string) {
		d := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: "+desc+"\n---\nbody\n"), 0o644))
	}
	write("alpha", "one")
	write("beta", "two")
	paths := []string{root}
	require.Len(t, cachedDiscoverSkills(paths), 2)

	write("alpha", "a much longer description")
	got := cachedDiscoverSkills(paths)
	require.Len(t, got, 2)
	descs := []string{got[0].Description, got[1].Description}
	require.Contains(t, descs, "a much longer description")

	require.NoError(t, os.RemoveAll(filepath.Join(root, "beta")))
	require.Len(t, cachedDiscoverSkills(paths), 1)

	other := t.TempDir()
	require.Empty(t, cachedDiscoverSkills([]string{other}))
	require.Len(t, cachedDiscoverSkills(paths), 1)
}

func TestInputCacheLRU(t *testing.T) {
	c := newInputCache[int]()
	for i := range inputCacheMaxEntries {
		c.get(strconv.Itoa(i), func() string { return "k" }, func() int { return i })
	}
	c.get("0", func() string { return "k" }, func() int { return -1 })
	c.get("new", func() string { return "k" }, func() int { return 1 })
	require.Equal(t, 0, c.get("0", func() string { return "k" }, func() int { return -1 }))
	require.Equal(t, -1, c.get("1", func() string { return "k" }, func() int { return -1 }))
}
