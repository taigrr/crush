package worktree

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/taigrr/crush/internal/db"
)

// newGitProject creates a temp dir initialized as a git repo with one
// committed file, and returns its path. Tests that exercise the
// git-backed worktree service need a real repository.
func newGitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	run("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

func newWorktreeService(t *testing.T, projectDir string) Service {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(0)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.ExecContext(t.Context(), `
		CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS worktrees (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			base_snapshot_id TEXT,
			is_active INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		);
	`)
	require.NoError(t, err)
	_, err = conn.ExecContext(t.Context(), "INSERT INTO sessions (id) VALUES ('s1')")
	require.NoError(t, err)

	svc, err := NewService(ServiceConfig{Enabled: true, ProjectDir: projectDir}, db.New(conn), conn, nil)
	require.NoError(t, err)
	return svc
}

func TestService_CreateUsesRealGitWorktree(t *testing.T) {
	t.Parallel()
	projectDir := newGitProject(t)
	svc := newWorktreeService(t, projectDir)
	ctx := context.Background()

	wt, err := svc.Create(ctx, "s1", "feature-x", "")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(projectDir, ".crush", "worktrees", "feature-x"), wt.Path)

	// It must be a real linked worktree: the directory has a .git file
	// (not a dir) and git lists it.
	gitFile := filepath.Join(wt.Path, ".git")
	info, err := os.Stat(gitFile)
	require.NoError(t, err, "worktree must contain a .git link")
	require.False(t, info.IsDir(), ".git in a linked worktree is a file, not a directory")

	// The committed file is present (worktree checks out HEAD).
	_, err = os.Stat(filepath.Join(wt.Path, "README.md"))
	require.NoError(t, err)

	// git worktree list (from the project) includes the new path.
	out, err := exec.CommandContext(t.Context(), "git", "-C", projectDir, "worktree", "list").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), wt.Path)

	// A branch named after the worktree now exists.
	branchOut, err := exec.CommandContext(t.Context(), "git", "-C", projectDir, "branch", "--list", "feature-x").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(branchOut), "feature-x")
}

func TestService_CreateRejectsNonGitProject(t *testing.T) {
	t.Parallel()
	svc := newWorktreeService(t, t.TempDir()) // not a git repo
	_, err := svc.Create(context.Background(), "s1", "feature-x", "")
	require.ErrorIs(t, err, ErrNotGitRepo)
}

func TestService_DeleteRemovesGitWorktree(t *testing.T) {
	t.Parallel()
	projectDir := newGitProject(t)
	svc := newWorktreeService(t, projectDir)
	ctx := context.Background()

	wt, err := svc.Create(ctx, "s1", "feature-y", "")
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, wt.ID))

	// Directory gone.
	_, err = os.Stat(wt.Path)
	require.True(t, os.IsNotExist(err), "worktree dir should be removed")

	// git no longer tracks it.
	out, err := exec.CommandContext(t.Context(), "git", "-C", projectDir, "worktree", "list").CombinedOutput()
	require.NoError(t, err)
	require.NotContains(t, string(out), wt.Path)
}

func TestService_MergeBringsWorktreeCommitIntoTarget(t *testing.T) {
	t.Parallel()
	projectDir := newGitProject(t)
	svc := newWorktreeService(t, projectDir)
	ctx := context.Background()

	wt, err := svc.Create(ctx, "s1", "feature-z", "")
	require.NoError(t, err)

	// Commit a new file inside the worktree on its branch.
	newFile := filepath.Join(wt.Path, "feature.txt")
	require.NoError(t, os.WriteFile(newFile, []byte("from worktree\n"), 0o644))
	gitWT := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = wt.Path
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	gitWT("add", ".")
	gitWT("commit", "-m", "add feature")

	// Merge feature-z into main.
	require.NoError(t, svc.Merge(ctx, wt.ID, "main", false))

	// The merged file now exists in the main project checkout, and we
	// were restored to main.
	_, err = os.Stat(filepath.Join(projectDir, "feature.txt"))
	require.NoError(t, err, "merge must bring the worktree commit into the main checkout")

	branch, err := exec.CommandContext(t.Context(), "git", "-C", projectDir, "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, "main", string(trimNL(branch)))
}

func TestService_MergeRefusesDirtyWorkingTree(t *testing.T) {
	t.Parallel()
	projectDir := newGitProject(t)
	svc := newWorktreeService(t, projectDir)
	ctx := context.Background()

	wt, err := svc.Create(ctx, "s1", "feature-d", "")
	require.NoError(t, err)

	// Dirty the main checkout.
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("dirty\n"), 0o644))

	err = svc.Merge(ctx, wt.ID, "main", false)
	require.ErrorIs(t, err, ErrDirtyWorkingTree)
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
