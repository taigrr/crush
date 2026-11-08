package config

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorktreeRootCollapsesLinkedWorktree(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	main := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git(main, "init", "-q")
	git(main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	linked := filepath.Join(t.TempDir(), "linked")
	git(main, "worktree", "add", "-q", linked)

	want := normalizePath(main)
	require.Equal(t, want, worktreeRoot(main))
	require.Equal(t, want, worktreeRoot(linked))
	require.Equal(t, want, worktreeRoot(filepath.Join(linked)))
	require.Empty(t, worktreeRoot(t.TempDir()))
	require.False(t, isInsideWorktree(t.TempDir()))
}
