package model

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/workspace"
)

// cwdWorkspace records working-dir and BTW calls for /cwd tests.
type cwdWorkspace struct {
	workspace.Workspace

	ready       bool
	busy        bool
	setDirCalls []string
	setDirErr   error
	btwCalls    []string
}

func (w *cwdWorkspace) Config() *config.Config { return nil }
func (w *cwdWorkspace) AgentIsReady() bool     { return w.ready }
func (w *cwdWorkspace) AgentIsBusy() bool      { return w.busy }

// AgentIsSessionBusy mirrors the workspace flag: these fixtures model a
// single-session workspace.
func (w *cwdWorkspace) AgentIsSessionBusy(string) bool { return w.busy }

func (w *cwdWorkspace) ConnectionState() workspace.ConnectionState {
	return workspace.ConnectionStateConnected
}

func (w *cwdWorkspace) AgentSetWorkingDir(_ string, dir string) error {
	if w.setDirErr != nil {
		return w.setDirErr
	}
	w.setDirCalls = append(w.setDirCalls, dir)
	return nil
}

func (w *cwdWorkspace) AgentRunBTW(_ context.Context, _ string, prompt string) error {
	w.btwCalls = append(w.btwCalls, prompt)
	return nil
}

func (w *cwdWorkspace) AgentRunAside(_ context.Context, _ string, prompt string) error {
	w.btwCalls = append(w.btwCalls, prompt)
	return nil
}

func (w *cwdWorkspace) AgentRun(context.Context, string, string, ...message.Attachment) error {
	return nil
}

func newCwdTestUI(t *testing.T, ws workspace.Workspace) *UI {
	t.Helper()
	ui := newSendTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}
	return ui
}

// TestHandleCwd_ExplicitAbsolutePathPersists verifies an absolute path is
// persisted verbatim (after cleaning).
func TestHandleCwd_ExplicitAbsolutePathPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ws := &cwdWorkspace{ready: true}
	ui := newCwdTestUI(t, ws)

	cmd := ui.handleCwd(dir)
	require.NotNil(t, cmd)
	drainCmd(cmd)

	require.Equal(t, []string{filepath.Clean(dir)}, ws.setDirCalls)
}

// TestHandleCwd_NoArgUsesTerminalCwd verifies an empty argument resolves to
// the process working directory.
func TestHandleCwd_NoArgUsesTerminalCwd(t *testing.T) {
	t.Parallel()
	ws := &cwdWorkspace{ready: true}
	ui := newCwdTestUI(t, ws)

	cmd := ui.handleCwd("")
	require.NotNil(t, cmd)
	drainCmd(cmd)

	require.Len(t, ws.setDirCalls, 1)
	require.True(t, filepath.IsAbs(ws.setDirCalls[0]))
}

// TestHandleCwd_NonexistentPathErrors verifies a missing directory is
// rejected before any persistence.
func TestHandleCwd_NonexistentPathErrors(t *testing.T) {
	t.Parallel()
	ws := &cwdWorkspace{ready: true}
	ui := newCwdTestUI(t, ws)

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	cmd := ui.handleCwd(missing)
	require.NotNil(t, cmd)
	drainCmd(cmd)

	require.Empty(t, ws.setDirCalls, "must not persist a nonexistent directory")
}

// TestHandleCwd_InformsModelOnlyWhenBusy verifies the model is told via a
// BTW aside only while a turn is active; idle relies on the next turn's
// system prompt.
func TestHandleCwd_InformsModelOnlyWhenBusy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	idle := &cwdWorkspace{ready: true, busy: false}
	ui := newCwdTestUI(t, idle)
	drainCmd(ui.handleCwd(dir))
	require.Empty(t, idle.btwCalls, "idle must not fold a BTW aside")

	busy := &cwdWorkspace{ready: true, busy: true}
	ui2 := newCwdTestUI(t, busy)
	drainCmd(ui2.handleCwd(dir))
	require.Len(t, busy.btwCalls, 1, "busy must inform the model")
	require.Contains(t, busy.btwCalls[0], filepath.Clean(dir))
}

// drainCmd synchronously executes a tea.Cmd, recursing into any BatchMsg
// it produces, so the workspace side effects run before assertions.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmd(c)
		}
	}
}
