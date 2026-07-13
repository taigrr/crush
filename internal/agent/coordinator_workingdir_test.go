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
		t.Parallel()
		c := &coordinator{cfg: cfg}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("disabled worktree service falls back to root", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{disable: true, enabled: "/wt"}}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("no session in context falls back to root", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{enabled: "/wt/feature"}}
		require.Equal(t, root, c.workingDir(t.Context()))
	})

	t.Run("no active worktree falls back to root", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{}}
		require.Equal(t, root, c.workingDir(ctxWithSession("s1")))
	})

	t.Run("active worktree path used for the session", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg, worktrees: &fakeWorktrees{enabled: "/wt/feature"}}
		require.Equal(t, "/wt/feature", c.workingDir(ctxWithSession("s1")))
	})
}

func ctxWithSessionCwd(sessionID, cwd string) context.Context {
	ctx := context.Background()
	if sessionID != "" {
		ctx = context.WithValue(ctx, tools.SessionIDContextKey, sessionID)
	}
	return tools.WithWorkingDir(ctx, cwd)
}

// TestCoordinatorWorkingDir_RequestCwd is the regression test for the
// sibling-worktree bug: monorepo and monorepo4 are linked git worktrees
// that collapse to the same project root, so they share one workspace and
// one coordinator. The coordinator's effectiveWorkingDir is baked in by
// whichever client created the workspace first; without a per-turn cwd a
// client launched from monorepo would have its tools run in monorepo4
// (or vice versa). workingDir must prefer the requesting client's cwd.
func TestCoordinatorWorkingDir_RequestCwd(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)

	t.Run("request cwd beats baked-in effective dir", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg, effectiveWorkingDir: "/Users/tai/code/nds/monorepo4"}
		// No request cwd: fall back to the workspace-global dir.
		require.Equal(t, "/Users/tai/code/nds/monorepo4", c.workingDir(context.Background()))
		// Request from a different sibling worktree wins.
		require.Equal(t,
			"/Users/tai/code/nds/monorepo",
			c.workingDir(ctxWithSessionCwd("", "/Users/tai/code/nds/monorepo")),
			"tools must run in the cwd the requesting client launched from")
	})

	t.Run("full precedence: config < effective < request", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{cfg: cfg}
		require.Equal(t, cfg.WorkingDir(), c.workingDir(context.Background()))
		c.effectiveWorkingDir = "/proj/wt-a"
		require.Equal(t, "/proj/wt-a", c.workingDir(context.Background()))
		require.Equal(t, "/proj/wt-b", c.workingDir(ctxWithSessionCwd("", "/proj/wt-b")))
	})

	t.Run("active managed worktree still wins over request cwd", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{
			cfg:                 cfg,
			effectiveWorkingDir: "/proj",
			worktrees:           &fakeWorktrees{enabled: "/proj/.crush/worktrees/feat-x"},
		}
		require.Equal(t,
			"/proj/.crush/worktrees/feat-x",
			c.workingDir(ctxWithSessionCwd("s1", "/proj/some/other/dir")),
			"an explicit /worktree switch beats the shell launch dir")
	})

	t.Run("worktrees enabled but none active uses request cwd", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{
			cfg:                 cfg,
			effectiveWorkingDir: "/proj/wt-first",
			worktrees:           &fakeWorktrees{},
		}
		require.Equal(t, "/proj/wt-current",
			c.workingDir(ctxWithSessionCwd("s1", "/proj/wt-current")))
	})

	// Regression: a recorded session working dir must NOT override the
	// worktree system. On worktree-enabled workspaces the worktree owns the
	// per-session dir (active worktree wins, else the live request cwd), so
	// `cd`-following keeps working. The recorded working dir only applies to
	// non-worktree workspaces.
	t.Run("worktree workspace ignores recorded session working dir", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		wcfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		sess, err := env.sessions.Create(t.Context(), "wt")
		require.NoError(t, err)
		require.NoError(t, env.sessions.SetWorkingDir(t.Context(), sess.ID, "/recorded/dir"))

		c := &coordinator{cfg: wcfg, sessions: env.sessions, worktrees: &fakeWorktrees{}}
		// Worktrees enabled, none active: the live request cwd wins, not
		// the recorded dir.
		require.Equal(t, "/proj/live",
			c.workingDir(ctxWithSessionCwd(sess.ID, "/proj/live")))
	})

	t.Run("non-worktree workspace uses recorded session working dir", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		wcfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		sess, err := env.sessions.Create(t.Context(), "plain")
		require.NoError(t, err)
		require.NoError(t, env.sessions.SetWorkingDir(t.Context(), sess.ID, "/recorded/dir"))

		c := &coordinator{cfg: wcfg, sessions: env.sessions}
		// No worktrees: the recorded dir is authoritative over the live
		// request cwd (resume-from-different-client case).
		require.Equal(t, "/recorded/dir",
			c.workingDir(ctxWithSessionCwd(sess.ID, "/proj/live")))
	})
}
