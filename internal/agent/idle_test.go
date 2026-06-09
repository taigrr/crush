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
