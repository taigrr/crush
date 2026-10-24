package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/csync"
)

func newQueueTestAgent() *sessionAgent {
	return &sessionAgent{
		activeRequests: csync.NewMap[string, context.CancelFunc](),
		messageQueue:   csync.NewMap[string, []SessionAgentCall](),
		dispatchMu:     csync.NewMap[string, *sync.Mutex](),
		idleCh:         make(chan struct{}),
	}
}

// TestEnqueueIfActive_OrderedAgainstDispatch reproduces the stranded-prompt
// window: the active turn has already released its active request and is
// draining the queue under the dispatch lock. A prompt that checked "busy"
// before that release must not be appended after the drain; it has to wait
// for the lock, then see the session as idle and run itself.
func TestEnqueueIfActive_OrderedAgainstDispatch(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	sa := newQueueTestAgent()
	sa.activeRequests.Set(sessionID, func() {})

	mu := sa.sessionMu(sessionID)
	mu.Lock()

	result := make(chan bool, 1)
	go func() {
		result <- sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "follow-up"})
	}()

	select {
	case <-result:
		t.Fatal("enqueueIfActive must wait for the dispatch lock")
	case <-time.After(50 * time.Millisecond):
	}

	// The turn finishes: release the active request and drain an empty
	// queue, as dispatchNextQueued does, then drop the lock.
	sa.clearActiveRequest(sessionID)
	_, queued := sa.messageQueue.Get(sessionID)
	require.False(t, queued)
	mu.Unlock()

	select {
	case enqueued := <-result:
		require.False(t, enqueued, "the call must observe the idle session and run itself")
	case <-time.After(5 * time.Second):
		t.Fatal("enqueueIfActive never returned")
	}
	require.Zero(t, sa.QueuedPrompts(sessionID), "nothing may be stranded in an idle session's queue")
}

func TestEnqueueIfActive_QueuesBehindActiveTurn(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	sa := newQueueTestAgent()
	require.False(t, sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "idle"}))

	sa.activeRequests.Set(sessionID, func() {})
	require.True(t, sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "a"}))
	require.True(t, sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "b"}))
	require.Equal(t, []string{"a", "b"}, sa.QueuedPromptsList(sessionID))
}

// TestEnqueueCall_ConcurrentAppendsAreNotLost guards the read-modify-write:
// concurrent enqueues on one session must all land.
func TestEnqueueCall_ConcurrentAppendsAreNotLost(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	const n = 200
	sa := newQueueTestAgent()
	sa.activeRequests.Set(sessionID, func() {})

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "p"})
		})
	}
	wg.Wait()
	require.Equal(t, n, sa.QueuedPrompts(sessionID))
}

// TestClearQueue_ConcurrentWithEnqueue checks a clear never deletes a
// prompt it did not observe: every enqueued prompt is either cleared or
// still queued, never silently dropped.
func TestClearQueue_ConcurrentWithEnqueue(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	const n = 200
	sa := newQueueTestAgent()
	sa.activeRequests.Set(sessionID, func() {})

	var cleared int
	var clearedMu sync.Mutex
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			sa.enqueueIfActive(SessionAgentCall{SessionID: sessionID, Prompt: "p"})
		})
	}
	wg.Go(func() {
		for range 20 {
			mu := sa.sessionMu(sessionID)
			mu.Lock()
			q, _ := sa.messageQueue.Take(sessionID)
			mu.Unlock()
			clearedMu.Lock()
			cleared += len(q)
			clearedMu.Unlock()
		}
	})
	wg.Wait()
	require.Equal(t, n, cleared+sa.QueuedPrompts(sessionID))
}
