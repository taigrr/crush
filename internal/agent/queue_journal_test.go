package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
)

// recordingJournal captures every SaveQueue snapshot so a test can
// assert what the persisted queue looked like after each mutation.
type recordingJournal struct {
	mu    sync.Mutex
	saves []map[string][]journal.QueuedPrompt
}

func (j *recordingJournal) SaveQueue(_ context.Context, sessionID string, entries []journal.QueuedPrompt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := make([]journal.QueuedPrompt, len(entries))
	copy(cp, entries)
	j.saves = append(j.saves, map[string][]journal.QueuedPrompt{sessionID: cp})
	return nil
}

func (j *recordingJournal) last(sessionID string) []journal.QueuedPrompt {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.saves) - 1; i >= 0; i-- {
		if q, ok := j.saves[i][sessionID]; ok {
			return q
		}
	}
	return nil
}

// TestQueueJournal_WritesThroughOnEnqueueAndDispatch proves the queue
// journal mirrors the in-memory queue: enqueueing a follow-up behind an
// active turn persists it, and the hand-off that dequeues it persists
// the (now empty) queue.
func TestQueueJournal_WritesThroughOnEnqueueAndDispatch(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	j := &recordingJournal{}

	large := &cancelableGatedStreamModel{entered: make(chan struct{})}
	small := &finishStreamModel{text: "title"}
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:   Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:   Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
		RunComplete:  broker,
		QueueJournal: j,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-large.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-follow", Prompt: "follow"})
	require.NoError(t, err)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID))

	persisted := j.last(sess.ID)
	require.Len(t, persisted, 1, "enqueue must be journaled")
	require.Equal(t, "follow", persisted[0].Prompt)
	require.Equal(t, "run-follow", persisted[0].RunID)

	sa.Cancel(sess.ID)
	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID))
	require.Empty(t, j.last(sess.ID), "dequeue on hand-off must journal the emptied queue")
}

// TestQueueJournal_PauseLeavesQueueForNextServer proves that once
// dispatch is paused (a draining server) a finished turn does not hand
// off to the queued follow-up, the follow-up stays journaled, and
// DetachQueueJournal makes the subsequent teardown-time clear invisible
// to the journal.
func TestQueueJournal_PauseLeavesQueueForNextServer(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	j := &recordingJournal{}

	large := &cancelableGatedStreamModel{entered: make(chan struct{})}
	small := &finishStreamModel{text: "title"}
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:   Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:   Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
		RunComplete:  broker,
		QueueJournal: j,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-large.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-follow", Prompt: "follow"})
	require.NoError(t, err)
	require.Len(t, sa.BusySessions(), 1)

	sa.PauseQueueDispatch()
	sa.Cancel(sess.ID)
	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}

	require.Eventually(t, func() bool { return !sa.IsBusy() }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID), "paused dispatch must leave the follow-up queued")
	require.Len(t, j.last(sess.ID), 1, "the follow-up must still be journaled")
	require.Empty(t, sa.BusySessions())

	sa.DetachQueueJournal()
	sa.ClearQueue(sess.ID)
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID))
	require.Len(t, j.last(sess.ID), 1, "a teardown clear after detach must not erase the journal")
}

// TestQueueJournal_DeferCallSurvivesFinishingTurn covers the drain-time
// swarm delivery path: a prompt deferred onto a session whose active
// turn is ending must end up journaled alongside the prompt that was
// already queued, regardless of how the two interleave.
func TestQueueJournal_DeferCallSurvivesFinishingTurn(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	j := &recordingJournal{}

	large := &cancelableGatedStreamModel{entered: make(chan struct{})}
	small := &finishStreamModel{text: "title"}
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:   Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:   Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:       true,
		Sessions:     env.sessions,
		Messages:     env.messages,
		RunComplete:  broker,
		QueueJournal: j,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-main", Prompt: "main"})
		mainDone <- runErr
	}()
	select {
	case <-large.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-follow", Prompt: "follow"})
	require.NoError(t, err)

	sa.PauseQueueDispatch()
	sa.Cancel(sess.ID)
	sa.deferCall(SessionAgentCall{SessionID: sess.ID, Prompt: "deferred swarm message"}, false)
	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}

	require.Equal(t, 2, sa.QueuedPrompts(sess.ID))
	persisted := j.last(sess.ID)
	require.Len(t, persisted, 2)
	require.Equal(t, "follow", persisted[0].Prompt)
	require.Equal(t, "deferred swarm message", persisted[1].Prompt)
}

// TestDeferCall_TailRunsAfterHeadInOrder mirrors queue replay on a new
// server: two prompts are deferred onto an idle session's queue and a
// head is dispatched normally. The head runs first and hands off to the
// deferred prompts in order, each as its own turn.
func TestDeferCall_TailRunsAfterHeadInOrder(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	large := &finishStreamModel{text: "done"}
	small := &finishStreamModel{text: "title"}
	var dispatchMu sync.Mutex
	var dispatched []string
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:  Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:  Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:      true,
		Sessions:    env.sessions,
		Messages:    env.messages,
		RunComplete: broker,
		OnQueueDispatch: func(c SessionAgentCall) {
			dispatchMu.Lock()
			dispatched = append(dispatched, c.RunID)
			dispatchMu.Unlock()
		},
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	sa.deferCall(SessionAgentCall{SessionID: sess.ID, RunID: "run-2", Prompt: "second"}, false)
	sa.deferCall(SessionAgentCall{SessionID: sess.ID, RunID: "run-3", Prompt: "third"}, false)
	require.Equal(t, []string{"second", "third"}, sa.QueuedPromptsList(sess.ID))

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, RunID: "run-1", Prompt: "first", Accepted: sa.BeginAccepted(sess.ID)})
	require.NoError(t, err)
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID))
	dispatchMu.Lock()
	require.Equal(t, []string{"run-2", "run-3"}, dispatched, "each queued call reports its dispatch as it leaves the queue")
	dispatchMu.Unlock()

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var users []string
	for _, m := range msgs {
		if m.Role == message.User {
			users = append(users, m.Content().Text)
		}
	}
	require.Equal(t, []string{"first", "second", "third"}, users)
}

// TestDeferCall_WithRunIDIsNotFoldedIntoActiveTurn: a deferred call that
// carries a run id must stay queued (for the next server) rather than be
// folded into the still-streaming turn on this one.
func TestDeferCall_WithRunIDIsNotFoldedIntoActiveTurn(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel: Model{Model: &finishStreamModel{text: "x"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel: Model{Model: &finishStreamModel{text: "t"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:     true, Sessions: env.sessions, Messages: env.messages,
	}).(*sessionAgent)
	sa.PauseQueueDispatch()
	sa.deferCall(SessionAgentCall{SessionID: "s", RunID: "run-deferred", Prompt: "deferred"}, false)
	sa.deferCall(SessionAgentCall{SessionID: "s", Prompt: "aside"}, false)

	fold, canceled, _ := sa.drainQueueForStep("s")
	require.Empty(t, canceled)
	require.Len(t, fold, 1, "only the id-less aside folds")
	require.Equal(t, "aside", fold[0].Prompt)
	require.Equal(t, []string{"deferred"}, sa.QueuedPromptsList("s"), "the id-bearing deferred call stays queued")
}

// TestDeferCall_FrontInsertsAtHead: RequeueFront puts a call ahead of an
// already-deferred tail and the journal reflects the order.
func TestDeferCall_FrontInsertsAtHead(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	j := &recordingJournal{}
	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel: Model{Model: &finishStreamModel{text: "x"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel: Model{Model: &finishStreamModel{text: "t"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:     true, Sessions: env.sessions, Messages: env.messages,
		QueueJournal: j,
	}).(*sessionAgent)
	sa.deferCall(SessionAgentCall{SessionID: "s", RunID: "t1", Prompt: "tail-1"}, false)
	sa.deferCall(SessionAgentCall{SessionID: "s", RunID: "t2", Prompt: "tail-2"}, false)
	sa.deferCall(SessionAgentCall{SessionID: "s", RunID: "h", Prompt: "head"}, true)
	require.Equal(t, []string{"head", "tail-1", "tail-2"}, sa.QueuedPromptsList("s"))
	persisted := j.last("s")
	require.Len(t, persisted, 3)
	require.Equal(t, "head", persisted[0].Prompt)
	require.Equal(t, "h", persisted[0].RunID)
}
