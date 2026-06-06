package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/worktree"
)

// fakeWorktrees is a minimal worktree.Service for resolver tests. It
// embeds the interface (nil) so unused methods panic if called, and
// overrides only what workingDir() touches.
type fakeWorktrees struct {
	worktree.Service
	enabled string // path returned by GetActive; "" => no active worktree
	disable bool
}

func (f *fakeWorktrees) IsEnabled() bool { return !f.disable }

func (f *fakeWorktrees) GetActive(_ context.Context, sessionID string) (*worktree.Worktree, error) {
	if f.enabled == "" {
		return nil, worktree.ErrNoActiveWorktree
	}
	return &worktree.Worktree{SessionID: sessionID, Path: f.enabled}, nil
}

func TestCoordinatorWorkingDir(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	root := cfg.WorkingDir()

	ctxWithSession := func(id string) context.Context {
		return context.WithValue(t.Context(), tools.SessionIDContextKey, id)
	}

	t.Run("nil worktree service falls back to root", func(t *testing.T) {
		c := &coordinator{cfg: cfg}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("disabled worktree service falls back to root", func(t *testing.T) {
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{disable: true, enabled: "/wt"}}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("no session in context falls back to root", func(t *testing.T) {
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{enabled: "/wt/feature"}}
		require.Equal(t, root, c.workingDir(t.Context()))
	})

	t.Run("no active worktree falls back to root", func(t *testing.T) {
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{}}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("active worktree path used for the session", func(t *testing.T) {
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{enabled: "/wt/feature"}}
		require.Equal(t, "/wt/feature", c.workingDir(ctxWithSession("s1")))
	})
}
