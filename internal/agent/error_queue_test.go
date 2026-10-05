package agent

import (
	"context"
	"errors"
	"net/http"
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

// erroringGatedStreamModel blocks its first Stream call until its gate is
// released and then fails with a provider error, simulating a request that
// dies mid-turn for a non-cancel reason. Subsequent calls (the queued
// follow-up's recursive run) stream a clean completion immediately.
type erroringGatedStreamModel struct {
	gate    chan struct{}
	entered chan struct{}
	calls   atomic.Int64
}

func (m *erroringGatedStreamModel) Provider() string { return "fake" }
func (m *erroringGatedStreamModel) Model() string    { return "fake-model" }

func (m *erroringGatedStreamModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *erroringGatedStreamModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if m.calls.Add(1) == 1 {
		close(m.entered)
		select {
		case <-m.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// A 4xx is not retried by fantasy, so the turn fails promptly.
		return nil, &fantasy.ProviderError{StatusCode: http.StatusBadRequest, Title: "boom", Message: "upstream rejected the request"}
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

func (m *erroringGatedStreamModel) GenerateObject(ctx context.Context, call fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *erroringGatedStreamModel) StreamObject(ctx context.Context, call fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// TestRun_ProviderErrorHandsOffToQueuedFollowUp proves that a turn ending
// in a provider error does not strand prompts queued behind it: the
// failed turn persists its error finish and publishes its own RunComplete
// carrying the error, and the queued follow-up still runs to completion
// with its own RunComplete instead of sitting in the queue forever.
func TestRun_ProviderErrorHandsOffToQueuedFollowUp(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	large := &erroringGatedStreamModel{
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
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

	// Let the main turn fail with a provider error.
	close(large.gate)

	select {
	case <-mainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("main run never returned")
	}
	require.Equal(t, 0, sa.QueuedPrompts(sess.ID), "the queue must drain into the recursive run, not sit stuck after an error")
	require.False(t, sa.IsSessionBusy(sess.ID))

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
	require.True(t, ok, "the failed turn must publish its own RunComplete")
	require.NotEmpty(t, main.Error, "the failed turn's RunComplete must carry the provider error")
	require.False(t, main.Cancelled)

	follow, ok := got["run-follow"]
	require.True(t, ok,
		"the queued prompt must still run and publish its own RunComplete after the error unwinds")
	require.Empty(t, follow.Error)
	require.False(t, follow.Cancelled)
	require.Equal(t, "done", follow.Text, "the queued prompt ran as its own turn")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	var erroredAssistants, completedAssistants, follows int
	for _, m := range msgs {
		switch m.Role {
		case message.Assistant:
			switch m.FinishReason() {
			case message.FinishReasonError:
				erroredAssistants++
			case message.FinishReasonEndTurn:
				completedAssistants++
			}
		case message.User:
			if m.Content().String() == "follow" {
				follows++
			}
		}
	}
	require.Equal(t, 1, erroredAssistants, "the main turn must persist with an error finish")
	require.Equal(t, 1, completedAssistants, "the follow-up turn must complete normally")
	require.Equal(t, 1, follows, "the follow-up prompt is its own user turn")
}
