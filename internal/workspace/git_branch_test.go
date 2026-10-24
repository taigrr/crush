package workspace

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGitBranchForDirIsCachedAndAsync(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitInit := exec.CommandContext(t.Context(), "git", "init", "-b", "feature-x")
	gitInit.Dir = dir
	require.NoError(t, gitInit.Run())

	w := &ClientWorkspace{}
	require.Empty(t, w.GitBranchForDir(""))

	// The first call never blocks on git; it starts a background refresh
	// and the branch shows up on a later call.
	_ = w.GitBranchForDir(dir)
	require.Eventually(t, func() bool {
		return w.GitBranchForDir(dir) == "feature-x"
	}, 5*time.Second, 10*time.Millisecond)

	// A fresh entry is served from cache without starting another refresh.
	w.branchMu.Lock()
	refreshing := w.branches[dir].refreshing
	w.branchMu.Unlock()
	require.False(t, refreshing)
}
