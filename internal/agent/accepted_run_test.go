package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
)

// newCancelTestAgent builds a DB-backed sessionAgent with no model. The
// tests here exercise the dispatch/cancel/persist paths, none of which
// reach agent.Stream, so a model is unnecessary.
func newCancelTestAgent(t *testing.T) (*sessionAgent, fakeEnv) {
	t.Helper()
	env := testEnv(t)
	sa := NewSessionAgent(SessionAgentOptions{
		Sessions: env.sessions,
		Messages: env.messages,
	}).(*sessionAgent)
	return sa, env
}

func (a *sessionAgent) acceptedCount(sessionID string) int {
	c, _ := a.acceptedRuns.Get(sessionID)
	return c
}

func (a *sessionAgent) hasPendingCancel(sessionID string) bool {
	mark, ok := a.cancelMark.Get(sessionID)
	return ok && mark > 0
}

func (a *sessionAgent) pendingCancelMark(sessionID string) uint64 {
	mark, _ := a.cancelMark.Get(sessionID)
	return mark
}

func TestAcceptedRun_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	require.Equal(t, "sid", accept.SessionID())
	require.Equal(t, 1, sa.acceptedCount("sid"))

	accept.Close()
	require.Equal(t, 0, sa.acceptedCount("sid"))

	// Repeated Close must not underflow the counter.
	accept.Close()
	accept.Close()
	require.Equal(t, 0, sa.acceptedCount("sid"))
}

func TestAcceptedRun_MultipleReservations(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	a1 := sa.BeginAccepted("sid")
	a2 := sa.BeginAccepted("sid")
	require.Equal(t, 2, sa.acceptedCount("sid"))

	a1.Close()
	require.Equal(t, 1, sa.acceptedCount("sid"))

	a2.Close()
	require.Equal(t, 0, sa.acceptedCount("sid"))
}

func TestAcceptedRun_NilSafe(t *testing.T) {
	t.Parallel()
	var accept *AcceptedRun
	require.Equal(t, "", accept.SessionID())
	// Must not panic.
	accept.Close()
}

func TestIsBusy_AcceptedRunCountsAsBusy(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	// Idle baseline: no active request, no accepted run.
	require.False(t, sa.IsBusy(),
		"agent with neither active nor accepted runs must report idle")

	// Dispatch window: the run has been accepted but its cancel has not
	// yet been registered in activeRequests. IsBusy must observe the
	// accepted reservation so the TUI's Esc-cancel path is not gated
	// off and a cancel that races the dispatch is delivered.
	accept := sa.BeginAccepted("sid")
	require.True(t, sa.IsBusy(),
		"accepted-but-not-yet-active run must report busy")

	accept.Close()
	require.False(t, sa.IsBusy(),
		"after the accepted reservation closes the agent must be idle again")
}

// TestIsBusy_AcceptedOnAnySessionCountsAsBusy guards the agent-wide
// IsBusy: a single session with an accepted-but-not-yet-active run must
// keep the agent busy even when other sessions are idle, so the global
// busy flag the TUI polls is set during the dispatch window for any
// session.
func TestIsBusy_AcceptedOnAnySessionCountsAsBusy(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	a1 := sa.BeginAccepted("s1")
	a2 := sa.BeginAccepted("s2")
	require.True(t, sa.IsBusy(),
		"any session with an accepted run must keep IsBusy true")

	a1.Close()
	require.True(t, sa.IsBusy(),
		"closing one of two accepted runs must still report busy")

	a2.Close()
	require.False(t, sa.IsBusy(),
		"closing the last accepted run must report idle")
}

// TestIsBusy_MultipleAcceptedRunsOnSameSession covers the counter:
// each BeginAccepted bumps the count and each Close decrements; IsBusy
// must stay true until the final Close.
func TestIsBusy_MultipleAcceptedRunsOnSameSession(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	a1 := sa.BeginAccepted("sid")
	a2 := sa.BeginAccepted("sid")
	require.True(t, sa.IsBusy())

	a1.Close()
	require.True(t, sa.IsBusy(),
		"second accepted reservation must keep IsBusy true")

	a2.Close()
	require.False(t, sa.IsBusy())
}

// TestIsBusy_ActiveRequestStillCountsAsBusy guards the original
// activeRequests-based path: an active request with no accepted run
// must still report busy. This locks the AND-of-OR behavior so a later
// refactor cannot regress the original gate.
func TestIsBusy_ActiveRequestStillCountsAsBusy(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	sa.activeRequests.Set("sid", func() {})
	require.True(t, sa.IsBusy(),
		"a registered active request must report busy with no accepted run")

	sa.activeRequests.Del("sid")
	require.False(t, sa.IsBusy(),
		"after the active request is cleared the agent must report idle")
}

// TestIsSessionBusy_IgnoresAcceptedRuns locks in the deliberate
// asymmetry between IsBusy (UI-facing, AND-of-OR) and IsSessionBusy
// (internal, strict): Run uses IsSessionBusy to decide queue-vs-take-
// over and must NOT see its own freshly-issued accept reservation as
// an in-progress turn — that would cause the very prompt being
// dispatched to be queued behind itself. If this asymmetry were
// changed naively, the dispatch tests TestRun_IdleCancelDoesNot
// PoisonNextPrompt and TestCancel_AcceptedAfterCancelIsNotPoisoned
// regress.
func TestIsSessionBusy_IgnoresAcceptedRuns(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	defer accept.Close()
	require.False(t, sa.IsSessionBusy("sid"),
		"IsSessionBusy must remain strict to activeRequests so Run "+
			"does not queue a prompt behind its own accept reservation")
	require.True(t, sa.IsBusy(),
		"IsBusy must observe the same accepted reservation IsSessionBusy "+
			"deliberately ignores")
}

// TestIsBusy_DuringDispatchWindowDeliversCancel is the end-to-end
// regression for the bug this commit fixes: pressing Esc during the
// race window between BeginAccepted and the goroutine registering its
// cancel in activeRequests must result in Cancel being delivered.
// IsBusy is what the TUI's `if m.isAgentBusy()` gate consults at
// internal/ui/model/ui.go:2115 before calling AgentCancel; without
// the fix, IsBusy returned false during this window and the keypress
// was silently dropped. This test simulates that exact ordering:
// BeginAccepted -> IsBusy (must be true) -> Cancel -> the pending
// cancel mark is recorded so a Run that enters the accepted-but-not-
// yet-active window observes the cancel.
func TestIsBusy_DuringDispatchWindowDeliversCancel(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	defer accept.Close()

	// Step 1: the UI sees the run as busy during the dispatch window.
	// This is the gate that was previously broken.
	require.True(t, sa.IsBusy(),
		"IsBusy must observe accepted run during dispatch window so the "+
			"TUI's Esc-cancel gate does not drop the keypress")

	// Step 2: with the gate open, AgentCancel reaches sessionAgent.Cancel
	// and records a pending cancel covering this accept sequence.
	sa.Cancel("sid")
	require.True(t, sa.hasPendingCancel("sid"),
		"Cancel during the dispatch window must record a pending cancel "+
			"so the run cancels on entry to Run")
	require.GreaterOrEqual(t, sa.pendingCancelMark("sid"), accept.seq,
		"the recorded mark must cover the accepted reservation's sequence")
}

func TestCancel_IdleDoesNotRecordPendingCancel(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	// No accepted run, no active request: a true no-op.
	sa.Cancel("sid")
	require.False(t, sa.hasPendingCancel("sid"))
}

func TestCancel_AcceptedRecordsPendingCancel(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	defer accept.Close()

	sa.Cancel("sid")
	require.True(t, sa.hasPendingCancel("sid"))
}

func TestCancel_SecondCancelWhilePendingIsNoOp(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	defer accept.Close()

	sa.Cancel("sid")
	require.True(t, sa.hasPendingCancel("sid"))

	// A second cancel while a pending cancel is already recorded must
	// remain a single pending cancel; one Run consumes exactly one.
	sa.Cancel("sid")
	require.True(t, sa.hasPendingCancel("sid"))
}

func TestRun_CancelOnEntryPersistsCanceledTurn(t *testing.T) {
	t.Parallel()
	sa, env := newCancelTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	accept := sa.BeginAccepted(sess.ID)
	// A cancel arrives in the accepted-but-not-yet-active window.
	sa.Cancel(sess.ID)
	require.True(t, sa.hasPendingCancel(sess.ID))

	result, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hello",
		Accepted:  accept,
	})
	require.NoError(t, err)
	require.Nil(t, result)

	// The pending cancel was consumed and the accept released.
	require.False(t, sa.hasPendingCancel(sess.ID))
	require.Equal(t, 0, sa.acceptedCount(sess.ID))

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, message.User, msgs[0].Role)
	assert.Equal(t, message.Assistant, msgs[1].Role)
	assert.Equal(t, message.FinishReasonCanceled, msgs[1].FinishReason())
}

func TestPersistCanceledTurn_WritesBothWhenUserMissing(t *testing.T) {
	t.Parallel()
	sa, env := newCancelTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	err = sa.persistCanceledTurn(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hello",
	}, false)
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, message.User, msgs[0].Role)
	assert.Equal(t, message.Assistant, msgs[1].Role)
	assert.Equal(t, message.FinishReasonCanceled, msgs[1].FinishReason())
}

func TestPersistCanceledTurn_WritesAssistantOnlyWhenUserCreated(t *testing.T) {
	t.Parallel()
	sa, env := newCancelTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// Simulate PrepareStep having already created the user message.
	_, err = sa.createUserMessage(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hello",
	})
	require.NoError(t, err)

	err = sa.persistCanceledTurn(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hello",
	}, true)
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, message.User, msgs[0].Role)
	assert.Equal(t, message.Assistant, msgs[1].Role)
	assert.Equal(t, message.FinishReasonCanceled, msgs[1].FinishReason())
}

func TestPersistCanceledTurn_SucceedsWithCanceledContext(t *testing.T) {
	t.Parallel()
	sa, env := newCancelTestAgent(t)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// Simulate workspace shutdown having already canceled the run
	// context. WithoutCancel must let the writes through.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = sa.persistCanceledTurn(ctx, SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hello",
	}, false)
	require.NoError(t, err)

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
}

func TestClearPendingCancel(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("sid")
	defer accept.Close()
	sa.Cancel("sid")
	require.True(t, sa.hasPendingCancel("sid"))

	sa.clearPendingCancel("sid")
	require.False(t, sa.hasPendingCancel("sid"))
}
