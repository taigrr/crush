package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func gitCLIBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestReadGitBranchMatchesGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fast path disabled on windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub", "deep"), 0o755))
	runGit(t, repo, "init", "-q", "-b", "feat/x")

	plain := filepath.Join(root, "plain")
	require.NoError(t, os.MkdirAll(plain, 0o755))

	check := func(dir string) {
		t.Helper()
		got, ok := readGitBranch(dir)
		if !ok {
			return
		}
		require.Equal(t, gitCLIBranch(t, dir), got, dir)
		require.Equal(t, gitCLIBranch(t, dir), getGitBranch(dir), dir)
	}

	got, ok := readGitBranch(repo)
	require.True(t, ok)
	require.Equal(t, "feat/x", got)
	check(repo)
	check(filepath.Join(repo, "sub", "deep"))
	check(plain)

	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "a")
	check(repo)
	runGit(t, repo, "worktree", "add", "-q", "-b", "wt-branch", filepath.Join(root, "wt"))
	got, ok = readGitBranch(filepath.Join(root, "wt"))
	require.True(t, ok)
	require.Equal(t, "wt-branch", got)
	check(filepath.Join(root, "wt"))

	runGit(t, repo, "checkout", "-q", "--detach")
	got, ok = readGitBranch(repo)
	require.True(t, ok)
	require.Empty(t, got)
	check(repo)

	_, ok = readGitBranch(filepath.Join(repo, ".git"))
	require.False(t, ok)
	require.Equal(t, gitCLIBranch(t, filepath.Join(repo, ".git")), getGitBranch(filepath.Join(repo, ".git")))

	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	_, ok = readGitBranch(plain)
	require.False(t, ok)
}
