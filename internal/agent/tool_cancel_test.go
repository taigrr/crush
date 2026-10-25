package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/fantasy"
)

// blockingTool runs until its context is done and reports how it ended.
type blockingTool struct {
	fantasy.AgentTool
	started chan struct{}
}

func (b *blockingTool) Run(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	close(b.started)
	<-ctx.Done()
	return fantasy.ToolResponse{}, ctx.Err()
}

func TestCancelToolCalls_ConvertsToResultAndKeepsTurn(t *testing.T) {
	t.Parallel()
	sa := &sessionAgent{}
	tool := &blockingTool{started: make(chan struct{})}
	wrapped := wrapToolsWithUserCancel([]fantasy.AgentTool{tool}, &sa.toolCancels, "s1")[0]

	turnCtx, cancelTurn := context.WithCancel(t.Context())
	defer cancelTurn()

	type outcome struct {
		resp fantasy.ToolResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := wrapped.Run(turnCtx, fantasy.ToolCall{ID: "call-1"})
		done <- outcome{resp, err}
	}()
	<-tool.started

	require.Zero(t, sa.CancelToolCalls("other-session"), "cancel is scoped to the session")
	require.Equal(t, 1, sa.CancelToolCalls("s1"))

	select {
	case got := <-done:
		require.NoError(t, got.err, "a user tool-cancel must not fail the turn")
		require.True(t, got.resp.IsError)
		require.Equal(t, toolCanceledResponse, got.resp.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not return after cancel")
	}
	require.NoError(t, turnCtx.Err(), "the turn context stays alive")
	require.Zero(t, sa.CancelToolCalls("s1"), "finished calls are unregistered")
}

func TestCancelToolCalls_TurnCancelPassesThrough(t *testing.T) {
	t.Parallel()
	sa := &sessionAgent{}
	tool := &blockingTool{started: make(chan struct{})}
	wrapped := wrapToolsWithUserCancel([]fantasy.AgentTool{tool}, &sa.toolCancels, "s1")[0]

	turnCtx, cancelTurn := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := wrapped.Run(turnCtx, fantasy.ToolCall{ID: "call-1"})
		errCh <- err
	}()
	<-tool.started
	cancelTurn()
	require.True(t, errors.Is(<-errCh, context.Canceled), "a turn cancel is not rewritten as a user tool-cancel")
}

func newPromoteTestAgent(t *testing.T) (*sessionAgent, <-chan pubsub.Event[notify.RunComplete]) {
	t.Helper()
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)
	sa := &sessionAgent{
		activeRequests: csync.NewMap[string, context.CancelFunc](),
		messageQueue:   csync.NewMap[string, []SessionAgentCall](),
		dispatchMu:     csync.NewMap[string, *sync.Mutex](),
		cancelMark:     csync.NewMap[string, uint64](),
		runComplete:    broker,
		idleCh:         make(chan struct{}),
	}
	return sa, broker.Subscribe(t.Context())
}

func TestPromoteNextQueuedToBTW(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	sa, events := newPromoteTestAgent(t)
	sa.messageQueue.Set(sessionID, []SessionAgentCall{{SessionID: sessionID, RunID: "run-0", Prompt: "x"}})
	require.False(t, sa.PromoteNextQueuedToBTW(sessionID), "no active turn to fold into")
	sa.messageQueue.Del(sessionID)

	startTurn(sa, sessionID, "active-run", "")
	require.False(t, sa.PromoteNextQueuedToBTW(sessionID), "nothing queued")

	sa.messageQueue.Set(sessionID, []SessionAgentCall{
		{SessionID: sessionID, Prompt: "already an aside"},
		{SessionID: sessionID, RunID: "run-1", Prompt: "first"},
		{SessionID: sessionID, RunID: "run-2", Prompt: "second"},
	})

	require.True(t, sa.PromoteNextQueuedToBTW(sessionID))
	q, _ := sa.messageQueue.Get(sessionID)
	require.Len(t, q, 3)
	require.Empty(t, q[1].RunID)
	require.Equal(t, "[btw] first", q[1].Prompt)
	require.Equal(t, "run-2", q[2].RunID, "only the next waiting prompt is promoted")

	select {
	case ev := <-events:
		require.Equal(t, "run-1", ev.Payload.RunID)
		require.False(t, ev.Payload.Cancelled)
		require.Equal(t, promotedRunText, ev.Payload.Text)
	case <-time.After(5 * time.Second):
		t.Fatal("promoted prompt's RunID never got a terminal RunComplete")
	}

	// The promoted prompt now folds into the active turn's next step,
	// while the remaining RunID prompt still waits for its own turn.
	fold, _ := sa.drainQueueForStep(sessionID, permission.SessionPolicy(""))
	require.Len(t, fold, 2)
	require.Equal(t, "[btw] first", fold[1].Prompt)
	require.Equal(t, []string{"second"}, sa.QueuedPromptsList(sessionID))
}

func startTurn(sa *sessionAgent, sessionID, runID string, policy permission.SessionPolicy) {
	sa.activeRequests.Set(sessionID, func() {})
	sa.activeTurns.set(sessionID, activeTurn{runID: runID, policy: policy})
}

func TestPromoteNextQueuedToBTW_SkipsPromptsTheTurnWouldNotFold(t *testing.T) {
	t.Parallel()
	const sessionID = "s1"
	sa, _ := newPromoteTestAgent(t)
	startTurn(sa, sessionID, "active-run", "")
	sa.cancelMark.Set(sessionID, 5)

	sa.messageQueue.Set(sessionID, []SessionAgentCall{
		{SessionID: sessionID, RunID: "active-run", Prompt: "summarize continuation"},
		{SessionID: sessionID, RunID: "yolo-run", Prompt: "other policy", PermissionPolicy: permission.SessionPolicy("yolo")},
		{SessionID: sessionID, RunID: "canceled-run", Prompt: "covered by cancel", acceptSeq: 3},
		{SessionID: sessionID, RunID: "ok-run", Prompt: "eligible", acceptSeq: 9},
	})

	require.True(t, sa.PromoteNextQueuedToBTW(sessionID))
	q, _ := sa.messageQueue.Get(sessionID)
	require.Equal(t, "active-run", q[0].RunID, "the turn's own continuation is never promoted")
	require.Equal(t, "yolo-run", q[1].RunID, "a prompt under another policy would not fold")
	require.Equal(t, "canceled-run", q[2].RunID, "a cancel-covered prompt would be dropped, not folded")
	require.Empty(t, q[3].RunID)
	require.Equal(t, "[btw] eligible", q[3].Prompt)

	require.False(t, sa.PromoteNextQueuedToBTW(sessionID), "no eligible prompt remains")
}

// completingTool finishes successfully even though it is canceled.
type completingTool struct {
	fantasy.AgentTool
	started chan struct{}
	release chan struct{}
}

func (c *completingTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	close(c.started)
	<-c.release
	return fantasy.NewTextResponse("wrote the file"), nil
}

func TestCancelToolCalls_KeepsResultOfToolThatFinished(t *testing.T) {
	t.Parallel()
	sa := &sessionAgent{}
	tool := &completingTool{started: make(chan struct{}), release: make(chan struct{})}
	wrapped := wrapToolsWithUserCancel([]fantasy.AgentTool{tool}, &sa.toolCancels, "s1")[0]

	type outcome struct {
		resp fantasy.ToolResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := wrapped.Run(t.Context(), fantasy.ToolCall{ID: "call-1"})
		done <- outcome{resp, err}
	}()
	<-tool.started
	require.Equal(t, 1, sa.CancelToolCalls("s1"))
	close(tool.release)

	got := <-done
	require.NoError(t, got.err)
	require.False(t, got.resp.IsError)
	require.Equal(t, "wrote the file", got.resp.Content, "a completed tool's real result is kept")
}
