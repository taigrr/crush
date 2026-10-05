package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/session"
)

// TestIsSessionBusy_IgnoresSiblingSessions verifies that a run in another
// session of the same workspace does not make the open session look busy,
// while the workspace-wide helper still reports it.
func TestIsSessionBusy_IgnoresSiblingSessions(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.sessionBusy["OTHER"] = true
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	require.False(t, ui.isSessionBusy(), "sibling session run must not mark open session busy")
	require.True(t, ui.isWorkspaceBusy(), "workspace flag must still see the sibling run")

	ws.sessionBusy["S1"] = true
	require.True(t, ui.isSessionBusy(), "open session run must be reported")
}

// TestIsSessionBusy_FallsBackToWorkspaceWithoutSession verifies the
// session-scoped helper uses the workspace flag when no session is open.
func TestIsSessionBusy_FallsBackToWorkspaceWithoutSession(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.workspaceBusy = true
	ui := newCancelTestUI(t, ws)
	ui.session = nil

	require.True(t, ui.isSessionBusy())
	require.True(t, ui.isWorkspaceBusy())

	ws.ready = false
	require.False(t, ui.isSessionBusy(), "not-ready agent is never busy")
	require.False(t, ui.isWorkspaceBusy(), "not-ready agent is never busy")
}

// TestContinueTurn_AllowedWhileSiblingSessionBusy verifies /continue is not
// refused just because another session in the workspace is streaming.
func TestContinueTurn_AllowedWhileSiblingSessionBusy(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.sessionBusy["OTHER"] = true
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	cmd := ui.continueTurn()
	require.NotNil(t, cmd)
	require.Nil(t, cmd(), "continue must dispatch rather than warn")

	ws.sessionBusy["S1"] = true
	cmd = ui.continueTurn()
	require.NotNil(t, cmd)
	require.NotNil(t, cmd(), "continue must warn when the open session is busy")
}
