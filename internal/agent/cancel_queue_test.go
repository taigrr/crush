package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/fantasy"
)

// cancelableGatedStreamModel blocks its first Stream call until the run
// context is canceled and then returns ctx.Err(), simulating a real
// provider that aborts mid-request. Subsequent calls (the queued
// follow-up's recursive run) stream a clean completion immediately, so
// the test can distinguish the canceled turn from the one that follows
// it in the queue.
type cancelableGatedStreamModel struct {
	entered chan struct{}
	calls   atomic.Int64
}

func (m *cancelableGatedStreamModel) Provider() string { return "fake" }
func (m *cancelableGatedStreamModel) Model() string    { return "fake-model" }

func (m *cancelableGatedStreamModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *cancelableGatedStreamModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if m.calls.Add(1) == 1 {
		close(m.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *cancelableGatedStreamModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *cancelableGatedStreamModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// TestCancel_HandsOffToQueuedFollowUp is the end-to-end proof that
// canceling an active turn no longer drops a follow-up prompt queued
// behind it: the canceled turn must persist as canceled, and the queued
// follow-up must still run to completion and publish its own
// RunComplete, instead of being wiped by Cancel or left stuck in the
// queue.
func TestCancel_HandsOffToQueuedFollowUp(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	large := &cancelableGatedStreamModel{entered: make(chan struct{})}
	small := &finishStreamModel{text: "title"}

	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:  Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:  Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:      true,
		Sessions:    env.sessions,
		Messages:    env.messages,
		RunComplete: broker,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	ch := broker.Subscribe(subCtx)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			RunID:     "run-main",
			Prompt:    "main",
		})
		mainDone <- runErr
	}()

	select {
	case <-large.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}
	require.True(t, sa.IsSessionBusy(sess.ID), "main run must be active before enqueueing the follow-up")

	res, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		RunID:     "run-follow",
		Prompt:    "follow",
	})
	require.NoError(t, err)
	require.Nil(t, res, "a busy-session follow-up must enqueue and return (nil, nil)")
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID), "the follow-up must be queued, not folded")

	sa.Cancel(sess.ID)

	// The raw return value of the outer Run call reflects the tail of
	// the recursive hand-off (the queued follow-up), not the canceled
	// turn's own outcome — same as the existing success-path hand-off.
	// The authoritative per-turn outcome is the RunComplete event
	// keyed by RunID, asserted below.
	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID), "the queue must drain into the recursive run, not sit stuck")

	got := map[string]notify.RunComplete{}
	deadline := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got[ev.Payload.RunID] = ev.Payload
		case <-deadline:
			t.Fatalf("timed out waiting for both RunCompletes; got %v", got)
		}
	}

	main, ok := got["run-main"]
	require.True(t, ok, "the canceled turn must publish its own RunComplete")
	require.True(t, main.Cancelled)

	follow, ok := got["run-follow"]
	require.True(t, ok,
		"the queued prompt must still run and publish its own RunComplete after the cancel unwinds")
	require.Empty(t, follow.Error)
	require.False(t, follow.Cancelled)
	require.Equal(t, "done", follow.Text, "the queued prompt ran as its own turn, not dropped by Cancel")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var canceledAssistants, completedAssistants, follows int
	for _, m := range msgs {
		switch m.Role {
		case message.Assistant:
			switch m.FinishReason() {
			case message.FinishReasonCanceled:
				canceledAssistants++
			case message.FinishReasonEndTurn:
				completedAssistants++
			}
		case message.User:
			if m.Content().String() == "follow" {
				follows++
			}
		}
	}
	require.Equal(t, 1, canceledAssistants, "the main turn must persist as canceled")
	require.Equal(t, 1, completedAssistants, "the follow-up turn must complete normally")
	require.Equal(t, 1, follows, "the follow-up prompt is its own user turn")
}

// TestCancelAll_DropsQueueInsteadOfHandingOff proves CancelAll (unlike a
// single Cancel) still fully stops a session: it must not let a canceled
// turn hand off to a queued follow-up, since that would keep the session
// busy right after CancelAll was asked to stop everything.
func TestCancelAll_DropsQueueInsteadOfHandingOff(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	large := &cancelableGatedStreamModel{entered: make(chan struct{})}
	small := &finishStreamModel{text: "title"}

	sa := NewSessionAgent(SessionAgentOptions{
		LargeModel:  Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		SmallModel:  Model{Model: small, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
		IsYolo:      true,
		Sessions:    env.sessions,
		Messages:    env.messages,
		RunComplete: broker,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	ch := broker.Subscribe(subCtx)

	mainDone := make(chan error, 1)
	go func() {
		_, runErr := sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			RunID:     "run-main",
			Prompt:    "main",
		})
		mainDone <- runErr
	}()

	select {
	case <-large.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never entered Stream")
	}

	res, err := sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		RunID:     "run-follow",
		Prompt:    "follow",
	})
	require.NoError(t, err)
	require.Nil(t, res)
	require.Equal(t, 1, sa.QueuedPrompts(sess.ID), "the follow-up must be queued")

	sa.CancelAll()

	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}

	require.Eventually(t, func() bool {
		return !sa.IsBusy()
	}, 5*time.Second, 10*time.Millisecond, "CancelAll must leave the workspace idle")
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID), "CancelAll must drop the queue, not hand off to it")
	require.EqualValues(t, 1, large.calls.Load(), "the queued follow-up must never actually run")

	follow, ok := waitForRunComplete(t, ch, "run-follow")
	require.True(t, ok, "the dropped follow-up must still publish a terminal RunComplete")
	require.True(t, follow.Cancelled)
}

// waitForRunComplete drains ch until it observes a RunComplete for runID
// or the deadline elapses.
func waitForRunComplete(t *testing.T, ch <-chan pubsub.Event[notify.RunComplete], runID string) (notify.RunComplete, bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Payload.RunID == runID {
				return ev.Payload, true
			}
		case <-deadline:
			return notify.RunComplete{}, false
		}
	}
}

// TestDispatchNextQueued_DeadParentContextDropsQueue proves that when the
// parent context is already done (e.g. workspace shutdown canceled it,
// not just the finishing turn's own genCtx), a queued follow-up is
// dropped via the detached-context publish instead of being handed off
// to a recursive Run call that would fail before its own
// RunComplete-publishing defer is installed — which would otherwise
// leave a RunID-bearing caller (e.g. `crush run`) hanging forever.
func TestDispatchNextQueued_DeadParentContextDropsQueue(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	sa := NewSessionAgent(SessionAgentOptions{
		Sessions:    env.sessions,
		Messages:    env.messages,
		RunComplete: broker,
	}).(*sessionAgent)

	const sessionID = "dead-parent-ctx"
	sa.messageQueue.Set(sessionID, []SessionAgentCall{
		{SessionID: sessionID, RunID: "run-follow", Prompt: "follow"},
	})

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	ch := broker.Subscribe(subCtx)

	deadCtx, deadCancel := context.WithCancel(t.Context())
	deadCancel() // simulate a workspace shutdown: the parent is already done.

	var skipRunComplete bool
	result, err := sa.dispatchNextQueued(deadCtx, SessionAgentCall{SessionID: sessionID, RunID: "run-main"}, nil, nil, context.Canceled, &skipRunComplete)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)

	require.Equal(t, 0, sa.QueuedPrompts(sessionID), "the queue must be dropped, not handed off to a dead context")

	follow, ok := waitForRunComplete(t, ch, "run-follow")
	require.True(t, ok, "the dropped queued prompt must still publish a terminal RunComplete")
	require.True(t, follow.Cancelled)
}
