package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/workspace"
)

// cancelWorkspace is a workspace stub that records agent cancel calls and
// lets tests control readiness and per-session busy state.
type cancelWorkspace struct {
	workspace.Workspace

	ready         bool
	sessionBusy   map[string]bool
	queued        map[string]int
	cancelCalls   []string
	cancelAllHits int
	clearQueue    []string
}

func newCancelWorkspace() *cancelWorkspace {
	return &cancelWorkspace{
		ready:       true,
		sessionBusy: map[string]bool{},
		queued:      map[string]int{},
	}
}

func (w *cancelWorkspace) Config() *config.Config { return nil }

func (w *cancelWorkspace) AgentIsReady() bool { return w.ready }

func (w *cancelWorkspace) AgentIsSessionBusy(sessionID string) bool {
	return w.sessionBusy[sessionID]
}

func (w *cancelWorkspace) AgentQueuedPrompts(sessionID string) int {
	return w.queued[sessionID]
}

func (w *cancelWorkspace) AgentCancel(sessionID string) {
	w.cancelCalls = append(w.cancelCalls, sessionID)
}

func (w *cancelWorkspace) AgentCancelAll() {
	w.cancelAllHits++
}

func (w *cancelWorkspace) AgentClearQueue(sessionID string) {
	w.clearQueue = append(w.clearQueue, sessionID)
}

func newCancelTestUI(t *testing.T, ws workspace.Workspace) *UI {
	t.Helper()
	ui := newSendTestUI(t, ws)
	return ui
}

// TestCancelAgent_FirstPressArmsSecondCancelsFocusedSession verifies the
// two-press flow: the first Esc arms the canceling state and returns a
// timer; the second cancels the focused session (which is the busy one).
func TestCancelAgent_FirstPressArmsSecondCancelsFocusedSession(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.sessionBusy["S1"] = true
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	cmd := ui.cancelAgent()
	require.True(t, ui.isCanceling, "first press must arm canceling")
	require.NotNil(t, cmd, "first press must start the cancel timer")
	require.Empty(t, ws.cancelCalls, "first press must not cancel yet")

	ui.cancelAgent()
	require.False(t, ui.isCanceling, "second press must disarm")
	require.Equal(t, []string{"S1"}, ws.cancelCalls,
		"second press must cancel the busy focused session")
	require.Zero(t, ws.cancelAllHits, "focused-session cancel must not fall back to CancelAll")
}

// TestCancelAgent_NoSessionFallsBackToCancelAll covers the detach/reattach
// case: the client has no focused session but a run is still in flight.
// Esc-Esc must fall back to a workspace-wide cancel rather than no-op.
func TestCancelAgent_NoSessionFallsBackToCancelAll(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ui := newCancelTestUI(t, ws)
	ui.session = nil

	cmd := ui.cancelAgent()
	require.True(t, ui.isCanceling)
	require.NotNil(t, cmd)

	ui.cancelAgent()
	require.False(t, ui.isCanceling)
	require.Empty(t, ws.cancelCalls, "must not target a specific session when none is focused")
	require.Equal(t, 1, ws.cancelAllHits, "must fall back to CancelAll")
}

// TestCancelAgent_FocusedOnDifferentSessionFallsBackToCancelAll covers a
// client focused on an idle session while another session's run is still
// going. The workspace-wide fallback must stop it.
func TestCancelAgent_FocusedOnDifferentSessionFallsBackToCancelAll(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.sessionBusy["S2"] = true // busy run belongs to another session
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	ui.cancelAgent()
	ui.cancelAgent()
	require.Empty(t, ws.cancelCalls,
		"focused session S1 is not busy, so no per-session cancel")
	require.Equal(t, 1, ws.cancelAllHits, "must fall back to CancelAll for the other session")
}

// TestCancelAgent_NotReadyIsNoOp verifies Esc does nothing while the agent
// is not ready.
func TestCancelAgent_NotReadyIsNoOp(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.ready = false
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	cmd := ui.cancelAgent()
	require.Nil(t, cmd)
	require.False(t, ui.isCanceling)
	require.Empty(t, ws.cancelCalls)
	require.Zero(t, ws.cancelAllHits)
}

// TestCancelAgent_ExpandedQueueClearsInsteadOfArming verifies that Esc
// clears an expanded prompt queue before entering the cancel flow.
func TestCancelAgent_ExpandedQueueClearsInsteadOfArming(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.queued["S1"] = 2
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}
	ui.pillsExpanded = true

	cmd := ui.cancelAgent()
	require.Nil(t, cmd, "clearing the queue must not start a cancel timer")
	require.False(t, ui.isCanceling)
	require.Equal(t, []string{"S1"}, ws.clearQueue)
	require.Empty(t, ws.cancelCalls)
}

// TestCancelAgent_StaleTimerDoesNotDisarmNewerCycle is the regression for
// the intermittent Esc bug: a leftover cancelTimerExpiredMsg from an
// earlier arm must not disarm a later arming cycle. The generation guard
// makes the stale message a no-op.
func TestCancelAgent_StaleTimerDoesNotDisarmNewerCycle(t *testing.T) {
	t.Parallel()

	ws := newCancelWorkspace()
	ws.sessionBusy["S1"] = true
	ui := newCancelTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}

	// Arm (gen 1), then confirm cancel (bumps gen to 2).
	ui.cancelAgent()
	staleGen := ui.cancelGen
	ui.cancelAgent()

	// Start a fresh arm cycle (gen 3).
	ui.cancelAgent()
	require.True(t, ui.isCanceling)

	// The stale timer from the first arm fires now.
	ui.handleCancelTimerExpired(cancelTimerExpiredMsg{gen: staleGen})
	require.True(t, ui.isCanceling,
		"a stale timer must not disarm the current cancel cycle")

	// The current cycle's own timer disarms it.
	ui.handleCancelTimerExpired(cancelTimerExpiredMsg{gen: ui.cancelGen})
	require.False(t, ui.isCanceling,
		"the matching-generation timer must disarm the cycle")
}
