package headless

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// fakeWatchClient implements every client interface used by the
// headless helpers.
type fakeWatchClient struct {
	mu        sync.Mutex
	busy      bool
	busyCalls int
	pending   proto.PendingPrompts
	messages  []proto.Message
	msgErr    error
	events    chan any
	subErr    error
	queued    int
	grants    []proto.PermissionGrant
	answers   []proto.QuestionAnswer
	resolved  bool
}

func newFakeWatchClient() *fakeWatchClient {
	return &fakeWatchClient{events: make(chan any, 16), resolved: true}
}

func (f *fakeWatchClient) setBusy(b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = b
}

func (f *fakeWatchClient) GetSession(context.Context, string, string) (*proto.Session, error) {
	return &proto.Session{ID: "S"}, nil
}

func (f *fakeWatchClient) ListSessionHistoryFiles(context.Context, string, string) ([]proto.File, error) {
	return nil, nil
}

func (f *fakeWatchClient) GetLSPs(context.Context, string) (map[string]proto.LSPClientInfo, error) {
	return nil, nil
}

func (f *fakeWatchClient) GetLSPDiagnostics(context.Context, string, string) (map[protocol.DocumentURI][]protocol.Diagnostic, error) {
	return nil, nil
}

func (f *fakeWatchClient) GetAgentSessionInfo(context.Context, string, string) (*proto.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busyCalls++
	return &proto.AgentSession{IsBusy: f.busy}, nil
}

func (f *fakeWatchClient) GetPendingPrompts(context.Context, string, string) (proto.PendingPrompts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending, nil
}

func (f *fakeWatchClient) ListMessages(context.Context, string, string) ([]proto.Message, error) {
	return f.messages, f.msgErr
}

func (f *fakeWatchClient) SubscribeEvents(context.Context, string) (<-chan any, error) {
	return f.events, f.subErr
}

func (f *fakeWatchClient) GetAgentSessionQueuedPrompts(context.Context, string, string) (int, error) {
	return f.queued, nil
}

func (f *fakeWatchClient) GrantPermission(_ context.Context, _ string, req proto.PermissionGrant) (bool, error) {
	f.grants = append(f.grants, req)
	return f.resolved, nil
}

func (f *fakeWatchClient) AnswerQuestion(_ context.Context, _ string, req proto.QuestionAnswer) (bool, error) {
	f.answers = append(f.answers, req)
	return f.resolved, nil
}

func asst(id, text string, parts ...proto.ContentPart) proto.Message {
	return proto.Message{ID: id, SessionID: "S", Role: proto.Assistant, Parts: append([]proto.ContentPart{proto.TextContent{Text: text}}, parts...)}
}

func TestWatch_RunCompletes(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R", MessageID: "m", Text: "ok"}}
	out := Watch(t.Context(), c, WatchRequest{WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "R", nil), Events: c.events})
	require.Equal(t, StatusCompleted, out.Status)
	require.Equal(t, "ok", out.Text)
	require.Zero(t, c.busyCalls, "a run-scoped watch never needs to poll busy state")
}

func TestWatch_FollowSessionAcrossQueuedTurns(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.setBusy(true)
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "S", MessageID: "m1", Text: "first"}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		c.setBusy(false)
		c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "S", MessageID: "m2", Text: "second"}}
	}()
	out := Watch(t.Context(), c, WatchRequest{WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events})
	require.Equal(t, "second", out.Text, "the wait lasts until the queued follow-up finishes too")
}

func TestWatch_StopOnBlock(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.pending = proto.PendingPrompts{Questions: []proto.QuestionRequest{{ID: "q", Prompt: "Which?"}}}
	c.events <- pubsub.Event[proto.QuestionRequest]{Payload: proto.QuestionRequest{ID: "q"}}

	var seen *proto.PendingPrompts
	out := Watch(t.Context(), c, WatchRequest{
		WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "R", nil), Events: c.events,
		StopOnBlock: true,
		OnBlocked:   func(p proto.PendingPrompts) { seen = &p },
	})
	require.Equal(t, StatusBlocked, out.Status)
	require.Equal(t, "waiting for an answer: Which?", out.Error)
	require.NotNil(t, out.Pending)
	require.NotNil(t, seen)
}

func TestWatch_PromptEventForAnotherSessionDoesNotBlock(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.events <- pubsub.Event[proto.PermissionRequest]{Payload: proto.PermissionRequest{ID: "p", SessionID: "OTHER"}}
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R"}}
	out := Watch(t.Context(), c, WatchRequest{
		WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "R", nil), Events: c.events, StopOnBlock: true,
	})
	require.Equal(t, StatusCompleted, out.Status, "pending prompts are re-checked for our session only")
}

func TestWatch_TimeoutInterruptAndFailures(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	out := Watch(t.Context(), c, WatchRequest{Tracker: NewTracker("S", "R", nil), Events: c.events, Timeout: 20 * time.Millisecond})
	require.Equal(t, StatusTimeout, out.Status)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out = Watch(ctx, c, WatchRequest{Tracker: NewTracker("S", "R", nil), Events: c.events})
	require.Equal(t, StatusCancelled, out.Status)
	require.True(t, out.Interrupted)

	sendErr := make(chan error, 1)
	sendErr <- errors.New("nope")
	out = Watch(t.Context(), c, WatchRequest{Tracker: NewTracker("S", "R", nil), Events: c.events, SendErr: sendErr})
	require.Equal(t, StatusError, out.Status)
	require.Equal(t, ExitFailure, out.ExitCode)
	require.Equal(t, "failed to send message: nope", out.Error)

	closed := make(chan any)
	close(closed)
	out = Watch(t.Context(), c, WatchRequest{Tracker: NewTracker("S", "R", nil), Events: closed})
	require.Equal(t, ExitFailure, out.ExitCode)
	require.Contains(t, out.Error, "event stream closed")

	rec := &recorder{err: errors.New("pipe")}
	c.events <- pubsub.Event[proto.Message]{Payload: asst("m", "x")}
	out = Watch(t.Context(), c, WatchRequest{Tracker: NewTracker("S", "R", rec), Events: c.events})
	require.Equal(t, ExitFailure, out.ExitCode)
	require.Equal(t, "pipe", out.Error)
}

func TestWatch_SuccessfulSendKeepsWaiting(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	sendErr := make(chan error, 1)
	sendErr <- nil
	go func() {
		time.Sleep(20 * time.Millisecond)
		c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R", Text: "done"}}
	}()
	out := Watch(t.Context(), c, WatchRequest{Tracker: NewTracker("S", "R", nil), Events: c.events, SendErr: sendErr})
	require.Equal(t, StatusCompleted, out.Status)
}

func TestWatch_CheckIdle(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.messages = []proto.Message{asst("a1", "done earlier", proto.Finish{Reason: proto.FinishReasonEndTurn})}
	out := Watch(t.Context(), c, WatchRequest{SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events, CheckIdle: true})
	require.Equal(t, StatusCompleted, out.Status)
	require.Equal(t, "done earlier", out.Text)

	c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "p", Description: "write go.mod"}}}
	out = Watch(t.Context(), c, WatchRequest{SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events, CheckIdle: true, StopOnBlock: true})
	require.Equal(t, StatusBlocked, out.Status)
	require.Equal(t, "waiting for permission: write go.mod", out.Error)
}

func TestLastOutcome(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		msgs   []proto.Message
		status Status
		text   string
		err    string
	}{
		{name: "empty session", status: StatusCompleted},
		{
			name:   "end turn",
			msgs:   []proto.Message{asst("a", "first"), {Role: proto.User}, asst("b", "latest", proto.Finish{Reason: proto.FinishReasonEndTurn})},
			status: StatusCompleted, text: "latest",
		},
		{
			name:   "cancelled",
			msgs:   []proto.Message{asst("a", "partial", proto.Finish{Reason: proto.FinishReasonCanceled})},
			status: StatusCancelled, text: "partial", err: "cancelled",
		},
		{
			name:   "error without message",
			msgs:   []proto.Message{asst("a", "", proto.Finish{Reason: proto.FinishReasonError})},
			status: StatusError, err: "agent run failed",
		},
		{
			name: "denied tool ends the turn",
			msgs: []proto.Message{
				asst("a", "Running it", proto.ToolCall{ID: "tc", Name: "bash"}, proto.Finish{Reason: proto.FinishReasonToolUse}),
				{Role: proto.Tool, Parts: []proto.ContentPart{proto.ToolResult{ToolCallID: "tc", IsError: true, Content: tools.PermissionDeniedMessage}}},
			},
			status: StatusPermissionDenied, text: "Running it", err: "permission denied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newFakeWatchClient()
			c.messages = tc.msgs
			out := LastOutcome(t.Context(), c, "ws", "S")
			require.Equal(t, tc.status, out.Status)
			require.Equal(t, tc.text, out.Text)
			require.Equal(t, tc.err, out.Error)
		})
	}

	c := newFakeWatchClient()
	c.msgErr = errors.New("db locked")
	out := LastOutcome(t.Context(), c, "ws", "S")
	require.Equal(t, ExitFailure, out.ExitCode)
	require.Contains(t, out.Error, "db locked")
}

func TestWaitSession(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.subErr = errors.New("no sse")
	out := WaitSession(t.Context(), c, "ws", "S", 0, nil)
	require.Equal(t, ExitFailure, out.ExitCode)

	c = newFakeWatchClient()
	c.setBusy(true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out = WaitSession(ctx, c, "ws", "S", 0, nil)
	require.Equal(t, StatusTimeout, out.Status, "interrupting a wait never reports the run as cancelled")
	require.Contains(t, out.Error, "still going")

	c = newFakeWatchClient()
	c.setBusy(true)
	c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "p"}}}
	rec := &recorder{}
	out = WaitSession(t.Context(), c, "ws", "S", 0, rec)
	require.Equal(t, StatusBlocked, out.Status)
	require.Len(t, rec.events, 1)
	require.Equal(t, EventBlocked, rec.events[0].(BlockedEvent).Type)
}

func TestFinish(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	pending := &proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "p"}}}
	res := Finish(t.Context(), c, Baseline{}, "ws", "S", "/cwd", "R", Outcome{
		Status: StatusBlocked, MessageID: "m", Text: "t", Error: "e", Pending: pending,
	})
	require.Equal(t, StatusBlocked, res.Status)
	require.Equal(t, ExitBlocked, res.ExitCode)
	require.Equal(t, "R", res.RunID)
	require.Equal(t, "/cwd", res.Cwd)
	require.Equal(t, pending, res.Pending)
	require.Equal(t, "crush --cwd /cwd --session S", res.AttachCommand)

	res = Finish(t.Context(), c, Baseline{}, "ws", "S", "", "", Outcome{Status: StatusError, ExitCode: ExitFailure})
	require.Equal(t, ExitFailure, res.ExitCode, "an explicit exit code overrides the status default")
}

func TestStatusExitCodesForNewStatuses(t *testing.T) {
	t.Parallel()

	require.Equal(t, ExitBlocked, StatusBlocked.ExitCode())
	require.Equal(t, ExitPermissionDenied, StatusPermissionDenied.ExitCode())
	require.Equal(t, ExitTimeout, StatusTimeout.ExitCode())
	require.Equal(t, ExitOK, StatusRunning.ExitCode())
}

func TestTracker_DenialMarksPermissionDenied(t *testing.T) {
	t.Parallel()

	tr := NewTracker("S", "R", nil)
	_, _ = tr.Handle(pubsub.Event[proto.PermissionNotification]{Payload: proto.PermissionNotification{SessionID: "OTHER", Denied: true}})
	out, _ := tr.Handle(pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R"}})
	require.Equal(t, StatusCompleted, out.Status, "denials in other sessions do not affect us")

	_, _ = tr.Handle(pubsub.Event[proto.PermissionNotification]{Payload: proto.PermissionNotification{SessionID: "S", Granted: true}})
	out, _ = tr.Handle(pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R"}})
	require.Equal(t, StatusCompleted, out.Status)

	_, _ = tr.Handle(pubsub.Event[proto.PermissionNotification]{Payload: proto.PermissionNotification{SessionID: "S", Denied: true}})
	out, _ = tr.Handle(pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R"}})
	require.Equal(t, StatusPermissionDenied, out.Status)

	out, _ = tr.Handle(pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R"}})
	require.Equal(t, StatusCompleted, out.Status, "the denial flag resets after each run")

	_, _ = tr.Handle(pubsub.Event[proto.PermissionNotification]{Payload: proto.PermissionNotification{SessionID: "S", Denied: true}})
	out, _ = tr.Handle(pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R", Cancelled: true}})
	require.Equal(t, StatusCancelled, out.Status, "cancelling while a prompt is open is a cancel, not a denial")
}

func TestWatch_ReconcilesMissedDenialFromHistory(t *testing.T) {
	t.Parallel()

	denied := []proto.Message{
		asst("a1", "Running it", proto.ToolCall{ID: "tc", Name: "bash"}, proto.Finish{Reason: proto.FinishReasonToolUse}),
		{Role: proto.Tool, Parts: []proto.ContentPart{proto.ToolResult{ToolCallID: "tc", IsError: true, Content: tools.PermissionDeniedMessage}}},
	}

	c := newFakeWatchClient()
	c.messages = denied
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "S", MessageID: "a1", Text: "Running it"}}
	out := Watch(t.Context(), c, WatchRequest{WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events})
	require.Equal(t, StatusPermissionDenied, out.Status, "a denial notification sent before we subscribed is recovered from history")
	require.Equal(t, "Running it", out.Text)

	c = newFakeWatchClient()
	c.messages = denied
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "S", MessageID: "later", Text: "ok"}}
	out = Watch(t.Context(), c, WatchRequest{WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events})
	require.Equal(t, StatusCompleted, out.Status, "an older denial does not taint a newer completed turn")
}

func TestWatch_ReturnsWhenBusyClearsWithoutAnotherEvent(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.setBusy(true)
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{SessionID: "S", MessageID: "m", Text: "done"}}
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.setBusy(false)
	}()
	out := Watch(t.Context(), c, WatchRequest{WorkspaceID: "ws", SessionID: "S", Tracker: NewTracker("S", "", nil), Events: c.events, Timeout: 5 * time.Second})
	require.Equal(t, StatusCompleted, out.Status, "the run is released just after its terminal event; the wait must not hang")
	require.Equal(t, "done", out.Text)
}

func TestWatch_IgnoresPromptsBeforeOurTurnStarts(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "tui-prompt"}}}
	tr := NewTracker("S", "R", nil)
	tr.AwaitUserPrompt("ours")
	c.events <- pubsub.Event[proto.PermissionRequest]{Payload: proto.PermissionRequest{ID: "tui-prompt", SessionID: "S"}}
	c.events <- pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: "R", Text: "done"}}
	out := Watch(t.Context(), c, WatchRequest{SessionID: "S", Tracker: tr, Events: c.events, StopOnBlock: true})
	require.Equal(t, StatusCompleted, out.Status, "a prompt raised by the turn ahead of ours is not ours to report")
	require.True(t, NewTracker("S", "R", nil).Started())
}

func TestWatch_RechecksPromptsWhenOurTurnStarts(t *testing.T) {
	t.Parallel()

	c := newFakeWatchClient()
	c.pending = proto.PendingPrompts{Permissions: []proto.PermissionRequest{{ID: "ours", Description: "rm build"}}}
	tr := NewTracker("S", "R", nil)
	tr.AwaitUserPrompt("go")
	// The permission event overtakes our user message on the SSE stream.
	c.events <- pubsub.Event[proto.PermissionRequest]{Payload: proto.PermissionRequest{ID: "ours", SessionID: "S"}}
	c.events <- pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: "u", SessionID: "S", Role: proto.User, Parts: []proto.ContentPart{proto.TextContent{Text: "go"}},
	}}
	out := Watch(t.Context(), c, WatchRequest{SessionID: "S", Tracker: tr, Events: c.events, StopOnBlock: true, Timeout: 2 * time.Second})
	require.Equal(t, StatusBlocked, out.Status, "a prompt that arrived before our user message is still reported")
}
