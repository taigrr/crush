package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/csync"
)

// newIdleTestAgent builds a minimal sessionAgent with only the fields the
// idle-signal machinery needs.
func newIdleTestAgent() *sessionAgent {
	return &sessionAgent{
		activeRequests: csync.NewMap[string, context.CancelFunc](),
		softInterrupts: csync.NewMap[string, chan struct{}](),
		dispatchMu:     csync.NewMap[string, *sync.Mutex](),
		idleCh:         make(chan struct{}),
	}
}

func TestWaitForIdle_ReturnsImmediatelyWhenNotBusy(t *testing.T) {
	t.Parallel()
	a := newIdleTestAgent()
	require.NoError(t, a.WaitForIdle(context.Background()))
}

func TestWaitForIdle_WakesWhenActiveRequestCleared(t *testing.T) {
	t.Parallel()
	a := newIdleTestAgent()

	// Mark the session busy by registering an active request.
	a.activeRequests.Set("s1", func() {})
	require.True(t, a.IsBusy())

	done := make(chan error, 1)
	go func() { done <- a.WaitForIdle(context.Background()) }()

	// Give the waiter a moment to block on the idle channel, then clear.
	time.Sleep(20 * time.Millisecond)
	a.clearActiveRequest("s1")

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForIdle did not return after active request cleared")
	}
}

func TestWaitForIdle_RespectsContextCancel(t *testing.T) {
	t.Parallel()
	a := newIdleTestAgent()
	a.activeRequests.Set("s1", func() {})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.WaitForIdle(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForIdle did not return after context cancel")
	}
}

// TestClearActiveRequest_ConcurrentWaiters ensures every waiter is woken by a
// single clear (channel close broadcasts to all receivers).
func TestClearActiveRequest_ConcurrentWaiters(t *testing.T) {
	t.Parallel()
	a := newIdleTestAgent()
	a.activeRequests.Set("s1", func() {})

	const waiters = 5
	var wg sync.WaitGroup
	wg.Add(waiters)
	for range waiters {
		go func() {
			defer wg.Done()
			_ = a.WaitForIdle(context.Background())
		}()
	}

	time.Sleep(20 * time.Millisecond)
	a.clearActiveRequest("s1")

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("not all waiters woke after clearActiveRequest")
	}
}

// TestWaitForIdle_WakesWhenAcceptedRunReleased covers a run that is
// accepted (dispatched) but never becomes active: canceled on entry, or
// failed in coordinator.run before sessionAgent.Run. IsBusy counts the
// reservation, so WaitForIdle blocks; releasing the reservation via
// AcceptedRun.Close must wake it. Before endAccepted signaled idleCh the
// waiter slept until an unrelated active run ended or its ctx expired.
func TestWaitForIdle_WakesWhenAcceptedRunReleased(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	accept := sa.BeginAccepted("s1")
	require.True(t, sa.IsBusy(), "an accepted reservation counts as busy")

	done := make(chan error, 1)
	go func() { done <- sa.WaitForIdle(context.Background()) }()

	time.Sleep(20 * time.Millisecond)
	accept.Close()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForIdle did not wake when the accepted run was released")
	}
}

// TestEndAccepted_OnlyLastReleaseSignals pins the counter semantics: with
// two reservations on one session, releasing one keeps the agent busy and
// must not wake waiters into a false idle; releasing the last one does.
func TestEndAccepted_OnlyLastReleaseSignals(t *testing.T) {
	t.Parallel()
	sa, _ := newCancelTestAgent(t)

	first := sa.BeginAccepted("s1")
	second := sa.BeginAccepted("s1")

	done := make(chan error, 1)
	go func() { done <- sa.WaitForIdle(context.Background()) }()

	time.Sleep(20 * time.Millisecond)
	first.Close()
	select {
	case <-done:
		t.Fatal("WaitForIdle returned while a reservation remained")
	case <-time.After(50 * time.Millisecond):
	}
	require.True(t, sa.IsBusy())

	second.Close()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForIdle did not wake on the last release")
	}
}

// TestWaitForIdle_NoLostWakeupWhenReleaseRacesBusyCheck exercises the
// ordering WaitForIdle depends on: the idle channel is captured before the
// busy check, so a release that lands in between closes the captured
// channel and the waiter re-checks instead of sleeping on the replacement.
// It hammers the race with many iterations; a lost wakeup shows up as a
// waiter that never returns within the deadline.
func TestWaitForIdle_NoLostWakeupWhenReleaseRacesBusyCheck(t *testing.T) {
	t.Parallel()
	a := newIdleTestAgent()

	for i := range 500 {
		a.activeRequests.Set("s1", func() {})
		done := make(chan error, 1)
		go func() { done <- a.WaitForIdle(context.Background()) }()
		go a.clearActiveRequest("s1")
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: WaitForIdle lost the wakeup", i)
		}
	}
}
