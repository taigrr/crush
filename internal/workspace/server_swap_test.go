package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// fakeServerSwap scripts a client's view of a graceful server update:
// server "A" serves workspace ws-A, then drains (refusing prompts), then
// disappears; server "B" comes up and hands out ws-B for the same path.
type fakeServerSwap struct {
	mu       sync.Mutex
	phase    string // "a", "draining", "gap", "b"
	streamA  chan any
	sent     []string
	sentTo   []string
	creates  []proto.Workspace
	subCalls int
}

func (f *fakeServerSwap) setPhase(p string) {
	f.mu.Lock()
	f.phase = p
	f.mu.Unlock()
}

func (f *fakeServerSwap) subscribe(ctx context.Context, id string) (<-chan any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subCalls++
	switch {
	case (f.phase == "a" || f.phase == "draining") && id == "ws-A":
		return f.streamA, nil
	case f.phase == "b" && id == "ws-B":
		// Stays open until the caller cancels, like a real SSE stream.
		ch := make(chan any)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch, nil
	case f.phase == "b":
		return nil, errors.New("status code 404")
	default:
		return nil, errors.New("connection refused")
	}
}

func (f *fakeServerSwap) create(_ context.Context, ws proto.Workspace) (*proto.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, ws)
	switch f.phase {
	case "a", "draining":
		// Like the real server: the returned Path is the resolved
		// project root, whatever launch cwd the request carried.
		return &proto.Workspace{ID: "ws-A", Path: "/proj", WorkingDir: ws.Path}, nil
	case "b":
		return &proto.Workspace{ID: "ws-B", Path: "/proj", WorkingDir: ws.Path}, nil
	}
	return nil, errors.New("connection refused")
}

func (f *fakeServerSwap) send(_ context.Context, id string, p heldPrompt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.phase {
	case "draining":
		return client.ErrServerDraining
	case "gap":
		return errors.New("connection refused")
	}
	f.sent = append(f.sent, p.prompt)
	f.sentTo = append(f.sentTo, id)
	return nil
}

// TestServerSwap_HoldsPromptsAndReattachesByPath is the client half of a
// graceful update: a prompt refused by the draining server is held (not
// lost, not surfaced as an error), the stream drop is reported as
// Updating, the client re-attaches by path to the replacement server's
// fresh workspace id, and the held prompt is delivered there.
func TestServerSwap_HoldsPromptsAndReattachesByPath(t *testing.T) {
	t.Parallel()

	f := &fakeServerSwap{phase: "a", streamA: make(chan any)}
	w := NewClientWorkspace(nil, proto.Workspace{ID: "ws-A", Path: "/proj"})
	w.SetCreationArgs(proto.Workspace{Path: "/proj/sub", DataDir: "/data", YOLO: true, Debug: true, Env: []string{"NVIM=/tmp/nvim"}})
	w.SetReconnectDelayForTest(10 * time.Millisecond)
	w.subscribeEventsFn = f.subscribe
	w.createWorkspaceFn = f.create
	w.sendMessageFn = f.send

	rec := &connEventRecorder{}
	var heldMu sync.Mutex
	var heldEvents []HeldPromptsEvent
	send := func(msg tea.Msg) {
		rec.send(msg)
		if ev, ok := msg.(pubsub.Event[HeldPromptsEvent]); ok {
			heldMu.Lock()
			heldEvents = append(heldEvents, ev.Payload)
			heldMu.Unlock()
		}
	}
	done := make(chan struct{})
	go func() {
		w.subscribeLoop(send)
		close(done)
	}()
	t.Cleanup(func() {
		w.stopSubscribeLoop()
		<-done
	})

	require.Eventually(t, func() bool { return w.ConnectionState() == ConnectionStateConnected }, 2*time.Second, 5*time.Millisecond)

	// Normal send while A is healthy.
	require.NoError(t, w.AgentRun(context.Background(), "s1", "first"))

	// A drains: the prompt is held, not failed.
	f.setPhase("draining")
	err := w.AgentRun(context.Background(), "s1", "second")
	require.ErrorIs(t, err, ErrServerUpdating)
	require.ErrorIs(t, w.AgentRunBTW(context.Background(), "s1", "aside"), ErrServerUpdating)
	require.Equal(t, 2, w.HeldPrompts())

	// A exits: the stream closes; nothing answers for a moment.
	f.setPhase("gap")
	close(f.streamA)
	require.Eventually(t, func() bool { return w.ConnectionState() == ConnectionStateUpdating }, 2*time.Second, 5*time.Millisecond,
		"drop after a draining refusal must be reported as Updating")

	// A prompt sent during the gap (transport error, stream down) is
	// held too, behind the earlier ones.
	require.ErrorIs(t, w.AgentRun(context.Background(), "s1", "in the gap"), ErrServerUpdating)
	require.Equal(t, 3, w.HeldPrompts())

	// B comes up with a new workspace id for the same path.
	f.setPhase("b")
	require.Eventually(t, func() bool { return w.ConnectionState() == ConnectionStateConnected }, 3*time.Second, 5*time.Millisecond)
	require.Equal(t, "ws-B", w.workspaceID(), "client must re-attach by path and adopt the new id")

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.sent) == 4
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, 0, w.HeldPrompts())
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{"first", "second", "[btw] aside", "in the gap"}, f.sent)
	require.Equal(t, []string{"ws-A", "ws-B", "ws-B", "ws-B"}, f.sentTo, "held prompts go to the new server's workspace")
	require.NotEmpty(t, f.creates)
	for _, c := range f.creates {
		require.Equal(t, "/proj/sub", c.Path, "re-attach must use the client's launch cwd, not the resolved root")
		require.Equal(t, "/data", c.DataDir, "re-attach must carry --data-dir")
		require.True(t, c.YOLO, "re-attach must carry --yolo")
		require.True(t, c.Debug, "re-attach must carry --debug")
		require.Equal(t, []string{"NVIM=/tmp/nvim"}, c.Env, "re-attach must carry the client env")
	}
	heldMu.Lock()
	defer heldMu.Unlock()
	require.Len(t, heldEvents, 1)
	require.Equal(t, 3, heldEvents[0].Sent)
	require.NoError(t, heldEvents[0].Err)
	require.Contains(t, rec.snapshot(), ConnectionStateUpdating)
}

// TestFlushHeldPrompts_KeepsForeignAndReportsFailed: prompts held for a
// workspace the client has since switched away from stay held; a prompt
// the new server rejects outright is returned with its text (for the
// editor) rather than silently dropped; prompts behind a still-draining
// server stay held in order.
func TestFlushHeldPrompts_KeepsForeignAndReportsFailed(t *testing.T) {
	t.Parallel()
	w := NewClientWorkspace(nil, proto.Workspace{ID: "ws-B", Path: "/proj"})
	var sent []string
	w.sendMessageFn = func(_ context.Context, _ string, p heldPrompt) error {
		switch p.prompt {
		case "bad":
			return errors.New("400 empty prompt")
		case "drains":
			return client.ErrServerDraining
		}
		sent = append(sent, p.prompt)
		return nil
	}
	w.held = []heldPrompt{
		{path: "/other", sessionID: "x", prompt: "foreign"},
		{path: "/proj", sessionID: "s", prompt: "ok"},
		{path: "/proj", sessionID: "s", prompt: "bad"},
		{path: "/proj", sessionID: "s", prompt: "drains"},
		{path: "/proj", sessionID: "s", prompt: "after"},
	}
	n, failed, kept := w.flushHeldPrompts(context.Background())
	require.Equal(t, 1, n)
	require.Equal(t, 1, kept, "the foreign-path prompt is reported as kept elsewhere")
	require.Equal(t, []string{"ok"}, sent)
	require.Len(t, failed, 1)
	require.Equal(t, "bad", failed[0].Prompt)
	require.Equal(t, 3, w.HeldPrompts(), "foreign, drains, and after stay held")
	require.Equal(t, "foreign", w.held[0].prompt)
	require.Equal(t, "drains", w.held[1].prompt)
	require.Equal(t, "after", w.held[2].prompt)
	require.Equal(t, ConnectionStateUpdating, w.stateAfterDrop(true), "a draining refusal keeps the updating flag")
}

// TestHold_UpdatingOnlyForDraining: a prompt held because the stream is
// down (a crash) must not make the UI claim the server is updating; a
// draining refusal does. A foreign-path hold does not put the current
// workspace into the "already holding" order-preserving branch.
func TestHold_UpdatingOnlyForDraining(t *testing.T) {
	t.Parallel()
	w := NewClientWorkspace(nil, proto.Workspace{ID: "ws", Path: "/proj"})
	w.connState = ConnectionStateReconnecting
	w.sendMessageFn = func(context.Context, string, heldPrompt) error {
		return errors.New("dial unix: connection refused")
	}
	require.ErrorIs(t, w.AgentRun(context.Background(), "s", "crash"), ErrServerUpdating)
	require.Equal(t, ConnectionStateReconnecting, w.stateAfterDrop(true), "a plain drop is not an update")

	// A stale hold for another workspace does not count as "already
	// holding here": the send is still attempted.
	w.held = append(w.held, heldPrompt{path: "/other", prompt: "x"})
	sends := 0
	w.sendMessageFn = func(context.Context, string, heldPrompt) error {
		sends++
		return client.ErrServerDraining
	}
	require.ErrorIs(t, w.AgentRun(context.Background(), "s", "again"), ErrServerUpdating)
	require.Equal(t, 0, sends, "with a hold already pending for this workspace, order is preserved without a send")
	w.held = []heldPrompt{{path: "/other", prompt: "x"}}
	require.ErrorIs(t, w.AgentRun(context.Background(), "s", "third"), ErrServerUpdating)
	require.Equal(t, 1, sends, "a foreign hold alone does not suppress the send")
	require.Equal(t, ConnectionStateUpdating, w.stateAfterDrop(true))
}

// TestReattachByPath_FollowsSwitchAndYieldsToConcurrentSwitch: after a
// SwitchWorkspace the re-attach targets the switched-to root with the
// original flags; a switch that lands while a re-attach is in flight
// wins over the re-attach result.
func TestReattachByPath_FollowsSwitchAndYieldsToConcurrentSwitch(t *testing.T) {
	t.Parallel()
	w := NewClientWorkspace(nil, proto.Workspace{ID: "ws-1", Path: "/proj"})
	w.SetCreationArgs(proto.Workspace{Path: "/proj/sub", DataDir: "/data", YOLO: true})
	var creates []proto.Workspace
	w.createWorkspaceFn = func(_ context.Context, ws proto.Workspace) (*proto.Workspace, error) {
		creates = append(creates, ws)
		return &proto.Workspace{ID: "ws-" + ws.Path, Path: ws.Path}, nil
	}

	// Simulate SwitchWorkspace's bookkeeping (it needs a real client).
	w.mu.Lock()
	w.ws = proto.Workspace{ID: "ws-2", Path: "/elsewhere"}
	w.creationArgs.Path = "/elsewhere"
	w.mu.Unlock()

	require.NoError(t, w.reattachByPath(context.Background()))
	require.Len(t, creates, 1)
	require.Equal(t, "/elsewhere", creates[0].Path, "re-attach follows the switch, not the launch cwd")
	require.Equal(t, "/data", creates[0].DataDir, "flags still replayed")
	require.True(t, creates[0].YOLO)

	// A switch landing mid-request: the result must be discarded.
	w.createWorkspaceFn = func(_ context.Context, ws proto.Workspace) (*proto.Workspace, error) {
		w.mu.Lock()
		w.ws = proto.Workspace{ID: "ws-3", Path: "/third"}
		w.mu.Unlock()
		return &proto.Workspace{ID: "stale", Path: ws.Path}, nil
	}
	require.Error(t, w.reattachByPath(context.Background()))
	require.Equal(t, "ws-3", w.workspaceID(), "the concurrent switch must not be clobbered")
}

// TestAgentRunBTW_GoesThroughSendHook: a steer must use the same hook a
// normal prompt does, so a fake (and the drain hold path) sees it. A
// second func the fakes do not override would call the real client and
// hang the test suite against a server that is not there.
func TestAgentRunBTW_GoesThroughSendHook(t *testing.T) {
	t.Parallel()
	w := NewClientWorkspace(nil, proto.Workspace{ID: "ws", Path: "/proj"})
	var got []heldPrompt
	w.sendMessageFn = func(_ context.Context, _ string, p heldPrompt) error {
		got = append(got, p)
		return nil
	}
	require.NoError(t, w.AgentRunBTW(context.Background(), "s", "turn left"))
	require.Len(t, got, 1)
	require.True(t, got[0].steer, "AgentRunBTW must send a steer")
	require.Equal(t, "[btw] turn left", got[0].prompt)
	require.Empty(t, got[0].runID, "a steer folds into the active turn, so it carries no RunID")
}
