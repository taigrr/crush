package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/chat"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/workspace"
)

// steerWorkspace records steer, background, and soft-interrupt calls for
// the mid-turn steering and backgroundify UI paths.
type steerWorkspace struct {
	workspace.Workspace

	busy           bool
	btwCalls       []string
	runCalls       []string
	backgroundCall []string
	backgroundErr  error
	softInterrupts []string
}

func (w *steerWorkspace) Config() *config.Config { return nil }
func (w *steerWorkspace) AgentIsReady() bool     { return true }
func (w *steerWorkspace) AgentIsBusy() bool      { return w.busy }

func (w *steerWorkspace) AgentIsSessionBusy(string) bool { return w.busy }

func (w *steerWorkspace) AgentReadiness(context.Context) (bool, error) { return true, nil }

func (w *steerWorkspace) ConnectionState() workspace.ConnectionState {
	return workspace.ConnectionStateConnected
}

func (w *steerWorkspace) AgentRunBTW(_ context.Context, _ string, prompt string) error {
	w.btwCalls = append(w.btwCalls, prompt)
	return nil
}

func (w *steerWorkspace) AgentRun(_ context.Context, _ string, prompt string, _ ...message.Attachment) error {
	w.runCalls = append(w.runCalls, prompt)
	return nil
}

func (w *steerWorkspace) AgentBackgroundTool(_ context.Context, _ string, toolCallID string) error {
	if w.backgroundErr != nil {
		return w.backgroundErr
	}
	w.backgroundCall = append(w.backgroundCall, toolCallID)
	return nil
}

func (w *steerWorkspace) AgentSoftInterrupt(sessionID string) {
	w.softInterrupts = append(w.softInterrupts, sessionID)
}

func (w *steerWorkspace) FileTrackerRecordRead(context.Context, string, string) {}
func (w *steerWorkspace) LSPStart(context.Context, string)                      {}

func newSteerTestUI(t *testing.T, ws workspace.Workspace) *UI {
	t.Helper()
	ui := newSendTestUI(t, ws)
	ui.session = &session.Session{ID: "S1"}
	ui.chat = NewChat(ui.com)
	ui.keyMap = DefaultKeyMap()
	ui.dialog = dialog.NewOverlay()
	return ui
}

// runningBashItem builds a bash tool item whose input has fully arrived
// but which has no result yet, i.e. a command that is executing.
func runningBashItem(ui *UI, id string) chat.ToolMessageItem {
	tc := message.ToolCall{ID: id, Name: tools.BashToolName, Input: `{"command":"sleep 100"}`, Finished: true}
	return chat.NewToolMessageItem(ui.com.Styles, "msg-"+id, tc, nil, false)
}

// collectInfoMsgs drains a command tree and returns every InfoMsg it
// produced, in order.
func collectInfoMsgs(cmd tea.Cmd) []util.InfoMsg {
	var out []util.InfoMsg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				walk(sub)
			}
		case util.InfoMsg:
			out = append(out, msg)
		}
	}
	walk(cmd)
	return out
}

// TestSteerOrSend_BusyFoldsIntoTurn verifies alt+enter while the agent is
// working goes through the steer path (RunID-less aside) rather than the
// queue.
func TestSteerOrSend_BusyFoldsIntoTurn(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	drainCmd(ui.steerOrSend("use tabs not spaces"))

	require.Equal(t, []string{"use tabs not spaces"}, ws.btwCalls)
	require.Empty(t, ws.runCalls, "a steer must not be queued as its own turn")
}

// TestSteerOrSend_IdleSendsNormally verifies the steer key degrades to a
// normal send when there is no active turn to interject into.
func TestSteerOrSend_IdleSendsNormally(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: false}
	ui := newSteerTestUI(t, ws)

	drainCmd(ui.steerOrSend("hello"))

	require.Empty(t, ws.btwCalls)
	require.Equal(t, []string{"hello"}, ws.runCalls)
}

// TestSteerOrSend_AttachmentsFallBackToQueue verifies a steer carrying
// attachments is queued as a normal turn (steers are text-only) and the
// user is warned.
func TestSteerOrSend_AttachmentsFallBackToQueue(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	att := message.Attachment{FilePath: "/tmp/x.txt", FileName: "x.txt", MimeType: "text/plain", Content: []byte("x")}
	infos := collectInfoMsgs(ui.steerOrSend("see file", att))

	require.Empty(t, ws.btwCalls)
	require.Equal(t, []string{"see file"}, ws.runCalls)
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeWarn, infos[0].Type)
}

// TestBackgroundRunningBash_TargetsInFlightCall verifies the background
// key resolves the executing bash tool call and asks the server to
// background exactly that call.
func TestBackgroundRunningBash_TargetsInFlightCall(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	finished := chat.NewToolMessageItem(ui.com.Styles, "m0",
		message.ToolCall{ID: "tc-old", Name: tools.BashToolName, Input: `{"command":"ls"}`, Finished: true},
		&message.ToolResult{ToolCallID: "tc-old", Content: "ok"}, false)
	ui.chat.SetMessages(finished, runningBashItem(ui, "tc-live"))

	require.True(t, ui.hasRunningBash())
	cmd := ui.backgroundRunningBash()
	require.NotNil(t, cmd)
	infos := collectInfoMsgs(cmd)

	require.Equal(t, []string{"tc-live"}, ws.backgroundCall)
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeInfo, infos[0].Type)
}

// TestBackgroundRunningBash_NoRunningCallFallsThrough verifies the key is
// not consumed when nothing is running, so the editor keeps its own
// binding for it.
func TestBackgroundRunningBash_NoRunningCallFallsThrough(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	// A streaming (unfinished) call and a non-bash running call must both
	// be ignored.
	streaming := chat.NewToolMessageItem(ui.com.Styles, "m1",
		message.ToolCall{ID: "tc-stream", Name: tools.BashToolName, Finished: false}, nil, false)
	view := chat.NewToolMessageItem(ui.com.Styles, "m2",
		message.ToolCall{ID: "tc-view", Name: tools.ViewToolName, Input: `{"file_path":"/f"}`, Finished: true}, nil, false)
	ui.chat.SetMessages(streaming, view)

	require.False(t, ui.hasRunningBash())
	require.Nil(t, ui.backgroundRunningBash())
	require.Empty(t, ws.backgroundCall)

	// Idle agent: never consumed even with a stale running-looking item.
	ws.busy = false
	ui.chat.SetMessages(runningBashItem(ui, "tc-stale"))
	require.Nil(t, ui.backgroundRunningBash())
}

// TestBackgroundKeyPress_ConsumedOnlyWhileBashRuns drives the real key
// handler: alt+b backgrounds the running command, and is left to the
// textarea otherwise.
func TestBackgroundKeyPress_ConsumedOnlyWhileBashRuns(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)
	ui.chat.SetMessages(runningBashItem(ui, "tc-live"))

	press := tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}
	drainCmd(ui.handleKeyPressMsg(press))
	require.Equal(t, []string{"tc-live"}, ws.backgroundCall)

	ws.backgroundCall = nil
	ui.chat.SetMessages()
	drainCmd(ui.handleKeyPressMsg(press))
	require.Empty(t, ws.backgroundCall, "no running bash: key must fall through")
}

// TestSoftInterruptTurn verifies /bg raises the session soft interrupt
// while busy and only warns when idle.
func TestSoftInterruptTurn(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	infos := collectInfoMsgs(ui.softInterruptTurn())
	require.Equal(t, []string{"S1"}, ws.softInterrupts)
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeInfo, infos[0].Type)

	ws.busy = false
	ws.softInterrupts = nil
	infos = collectInfoMsgs(ui.softInterruptTurn())
	require.Empty(t, ws.softInterrupts)
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeWarn, infos[0].Type)
}

// TestBgSlashCommandDispatches verifies "/bg" and its alias route to the
// soft interrupt.
func TestBgSlashCommandDispatches(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)

	for _, verb := range []string{"/bg", "/background"} {
		cmd, handled, consume := ui.dispatchSlash(verb)
		require.True(t, handled, verb)
		require.True(t, consume, verb)
		drainCmd(cmd)
	}
	require.Equal(t, []string{"S1", "S1"}, ws.softInterrupts)
}

// TestQueueList_LabelsSteerItems verifies steer asides in the expanded
// queue get a tag and lose their transport prefix.
func TestQueueList_LabelsSteerItems(t *testing.T) {
	t.Parallel()
	ui := newSteerTestUI(t, &steerWorkspace{})
	out := queueList([]string{"[btw] rename it", "then run tests"}, ui.com.Styles)

	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "steer")
	require.Contains(t, lines[0], "rename it")
	require.NotContains(t, lines[0], "[btw]")
	require.NotContains(t, lines[1], "steer")
	require.Contains(t, lines[1], "then run tests")
}

// helpKeys flattens ShortHelp/FullHelp output into the displayed key
// labels.
func helpKeys(groups ...[]key.Binding) []string {
	var out []string
	for _, group := range groups {
		for _, b := range group {
			out = append(out, b.Help().Key)
		}
	}
	return out
}

// TestHelp_ShowsBackgroundAndSteerWhileBusy verifies the help overlays
// advertise the new keys only when they can do something.
func TestHelp_ShowsBackgroundAndSteerWhileBusy(t *testing.T) {
	t.Parallel()
	ws := &steerWorkspace{busy: true}
	ui := newSteerTestUI(t, ws)
	ui.chat.SetMessages(runningBashItem(ui, "tc-live"))

	short := helpKeys(ui.ShortHelp())
	require.Contains(t, short, chat.BackgroundToolKey, "short help must show background key while bash runs")
	require.Contains(t, short, "alt+enter", "short help must show steer key while busy")

	full := helpKeys(ui.FullHelp()...)
	require.Contains(t, full, chat.BackgroundToolKey)
	require.Contains(t, full, "alt+enter")

	ws.busy = false
	short = helpKeys(ui.ShortHelp())
	require.NotContains(t, short, chat.BackgroundToolKey, "idle: no background key")
	require.NotContains(t, short, "alt+enter", "idle: no steer key")
}

// TestBashHint_AppearsAfterDelay verifies the in-chat "alt+b to
// background" hint is rendered only once the command has run long enough.
func TestBashHint_AppearsAfterDelay(t *testing.T) {
	t.Parallel()
	ui := newSteerTestUI(t, &steerWorkspace{busy: true})
	tc := message.ToolCall{ID: "tc", Name: tools.BashToolName, Input: `{"command":"sleep 100"}`, Finished: true}
	renderer := &chat.BashToolRenderContext{}

	fresh := renderer.RenderTool(ui.com.Styles, 100, &chat.ToolRenderOpts{
		ToolCall: tc, Status: chat.ToolStatusRunning, RunningSince: time.Now(),
	})
	require.NotContains(t, fresh, chat.BackgroundToolKey)

	old := renderer.RenderTool(ui.com.Styles, 100, &chat.ToolRenderOpts{
		ToolCall: tc, Status: chat.ToolStatusRunning, RunningSince: time.Now().Add(-chat.BackgroundHintDelay - time.Second),
	})
	require.Contains(t, old, chat.BackgroundToolKey)
	require.Contains(t, old, "to background")

	done := renderer.RenderTool(ui.com.Styles, 100, &chat.ToolRenderOpts{
		ToolCall: tc, Status: chat.ToolStatusSuccess,
		Result:       &message.ToolResult{ToolCallID: "tc", Content: "out"},
		RunningSince: time.Now().Add(-time.Minute),
	})
	require.NotContains(t, done, chat.BackgroundToolKey, "finished call must not show the hint")
}

// TestBashItem_KeepsAnimatingWhileRunning verifies a bash item whose input
// has finished but has no result yet still requests animation ticks, so
// the time-gated background hint gets a chance to be rendered.
func TestBashItem_KeepsAnimatingWhileRunning(t *testing.T) {
	t.Parallel()
	ui := newSteerTestUI(t, &steerWorkspace{busy: true})

	item := runningBashItem(ui, "tc-live")
	animatable, ok := item.(chat.Animatable)
	require.True(t, ok)
	require.NotNil(t, animatable.StartAnimation(), "running bash must keep ticking")

	item.SetResult(&message.ToolResult{ToolCallID: "tc-live", Content: "done"})
	require.Nil(t, animatable.StartAnimation(), "a result must stop the ticks")
}
