package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	fang "charm.land/fang/v2"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// fakeRunClient is a scripted [headlessClient]. When SendAgentMessage is
// called it replays the events returned by script (which receives the
// caller's RunID so it can emit a correlated RunComplete).
type fakeRunClient struct {
	mu sync.Mutex

	events    chan any
	script    func(runID string) []any
	sendErr   error
	subErr    error
	busy      bool
	sessions  []proto.Session
	sessCalls int
	files     [][]proto.File
	fileCalls int

	pending  proto.PendingPrompts
	messages []proto.Message

	sentPrompt string
	sentRunID  string
	sentPolicy proto.PermissionPolicy
	cancelled  []string
	granted    []proto.PermissionGrant
	dropped    []string
	queued     bool
	answered   []proto.QuestionAnswer
}

func newFakeRunClient(script func(runID string) []any) *fakeRunClient {
	return &fakeRunClient{
		events:   make(chan any, 64),
		script:   script,
		sessions: []proto.Session{{ID: "sess-aaaa-bbbb"}},
		files:    [][]proto.File{nil},
	}
}

func (f *fakeRunClient) SubscribeEvents(context.Context, string) (<-chan any, error) {
	if f.subErr != nil {
		return nil, f.subErr
	}
	return f.events, nil
}

func (f *fakeRunClient) SendAgentMessage(_ context.Context, _ string, msg proto.AgentMessage) error {
	f.mu.Lock()
	f.sentPrompt = msg.Prompt
	f.sentRunID = msg.RunID
	f.sentPolicy = msg.PermissionPolicy
	f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.mu.Lock()
	busy := f.busy
	f.mu.Unlock()
	if !busy {
		// Like the real server, an idle session's turn starts with the
		// caller's user message.
		f.events <- pubsub.Event[proto.Message]{Payload: proto.Message{
			ID: "user-" + msg.RunID, SessionID: msg.SessionID, Role: proto.User,
			Parts: []proto.ContentPart{proto.TextContent{Text: msg.Prompt}},
		}}
	}
	if f.script != nil {
		for _, ev := range f.script(msg.RunID) {
			if step, ok := ev.(func()); ok {
				// Run the step once everything before it was consumed,
				// to model server state changing mid-run.
				for len(f.events) > 0 {
					time.Sleep(time.Millisecond)
				}
				step()
				continue
			}
			f.events <- ev
		}
	}
	return nil
}

func (f *fakeRunClient) DropQueuedRun(_ context.Context, _, _, runID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, runID)
	return f.queued, nil
}

func (f *fakeRunClient) GetPendingPrompts(context.Context, string, string) (proto.PendingPrompts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending, nil
}

func (f *fakeRunClient) ListMessages(context.Context, string, string) ([]proto.Message, error) {
	return f.messages, nil
}

func (f *fakeRunClient) GetAgentSessionInfo(context.Context, string, string) (*proto.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &proto.AgentSession{IsBusy: f.busy}, nil
}

func (f *fakeRunClient) CancelAgentSession(_ context.Context, _, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, sessionID)
	return nil
}

func (f *fakeRunClient) GetSession(context.Context, string, string) (*proto.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[min(f.sessCalls, len(f.sessions)-1)]
	f.sessCalls++
	return &s, nil
}

func (f *fakeRunClient) ListSessionHistoryFiles(context.Context, string, string) ([]proto.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files := f.files[min(f.fileCalls, len(f.files)-1)]
	f.fileCalls++
	return files, nil
}

func (f *fakeRunClient) GetLSPs(context.Context, string) (map[string]proto.LSPClientInfo, error) {
	return nil, nil
}

func (f *fakeRunClient) GetLSPDiagnostics(context.Context, string, string) (map[protocol.DocumentURI][]protocol.Diagnostic, error) {
	return nil, nil
}

func testRunWorkspace() *proto.Workspace {
	return &proto.Workspace{
		ID:         "ws-1",
		Path:       "/repo",
		WorkingDir: "/repo/sub",
		Config: &config.Config{Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeLarge: {Provider: "anthropic", Model: "claude"},
		}},
	}
}

func asstMsg(id, text string, parts ...proto.ContentPart) pubsub.Event[proto.Message] {
	return pubsub.Event[proto.Message]{Payload: proto.Message{
		ID: id, SessionID: "sess-aaaa-bbbb", Role: proto.Assistant,
		Parts: append([]proto.ContentPart{proto.TextContent{Text: text}}, parts...),
	}}
}

func decodeLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), sc.Text())
		lines = append(lines, m)
	}
	return lines
}

func runStructuredForTest(t *testing.T, ctx context.Context, c *fakeRunClient, format headless.OutputFormat) ([]map[string]any, error) {
	t.Helper()
	return runHeadlessForTest(t, ctx, c, runOptions{format: format, mode: headless.ModeYOLO})
}

func runHeadlessForTest(t *testing.T, ctx context.Context, c *fakeRunClient, opts runOptions) ([]map[string]any, error) {
	t.Helper()
	var out bytes.Buffer
	opts.prompt = "do the thing"
	sess := &proto.Session{ID: "sess-aaaa-bbbb", Color: "blue", Animal: "otter"}
	err := runHeadless(ctx, c, testRunWorkspace(), sess, opts, &out)
	if !opts.format.IsStructured() {
		return []map[string]any{{"stdout": out.String()}}, err
	}
	return decodeLines(t, &out), err
}

func TestRunStructured_JSONCompleted(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(func(runID string) []any {
		return []any{
			asstMsg("m1", "Working"),
			pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{
				SessionID: "sess-aaaa-bbbb", RunID: runID, MessageID: "m1", Text: "Working. Done!",
			}},
		}
	})
	c.sessions = []proto.Session{
		{ID: "sess-aaaa-bbbb", PromptTokens: 10, Cost: 1},
		{ID: "sess-aaaa-bbbb", PromptTokens: 25, CompletionTokens: 5, Cost: 1.5, Color: "blue", Animal: "otter"},
	}
	c.files = [][]proto.File{
		{{ID: "old", Path: "/repo/old.go"}},
		{{ID: "old", Path: "/repo/old.go"}, {ID: "new", Path: "/repo/new.go"}},
	}

	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatJSON)
	require.NoError(t, err)
	require.Len(t, lines, 1, "json mode writes exactly one object")

	res := lines[0]
	require.Equal(t, "result", res["type"])
	require.Equal(t, "completed", res["status"])
	require.EqualValues(t, 0, res["exit_code"])
	require.Equal(t, "Working. Done!", res["text"])
	require.Equal(t, "sess-aaaa-bbbb", res["session_id"])
	require.Equal(t, "blue-otter-bbbb", res["address"])
	require.Equal(t, "ws-1", res["workspace_id"])
	require.Equal(t, "/repo/sub", res["cwd"])
	require.Equal(t, c.sentRunID, res["run_id"])
	require.Equal(t, "m1", res["message_id"])
	require.Equal(t, "crush --cwd /repo/sub --session sess-aaaa-bbbb", res["attach_command"])
	require.Equal(t, []any{"/repo/new.go"}, res["files_changed"])
	require.Equal(t, map[string]any{"prompt_tokens": 15.0, "completion_tokens": 5.0, "cost_usd": 0.5}, res["usage"])
	require.Equal(t, "do the thing", c.sentPrompt)
	require.Equal(t, proto.PermissionPolicyAllow, c.sentPolicy, "yolo mode sends the allow policy")
}

func TestRunStructured_StreamJSONEventOrder(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(func(runID string) []any {
		return []any{
			asstMsg("m1", "Let me look", proto.ToolCall{ID: "tc1", Name: "ls", Input: `{"path":"."}`, Finished: true}),
			pubsub.Event[proto.Message]{Payload: proto.Message{
				ID: "t1", SessionID: "sess-aaaa-bbbb", Role: proto.Tool,
				Parts: []proto.ContentPart{proto.ToolResult{ToolCallID: "tc1", Name: "ls", Content: "main.go"}},
			}},
			asstMsg("m2", "Found main.go"),
			pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, MessageID: "m2", Text: "Found main.go"}},
		}
	})

	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatStreamJSON)
	require.NoError(t, err)

	var types []string
	for _, l := range lines {
		types = append(types, l["type"].(string))
	}
	require.Equal(t, []string{"init", "text", "tool_call", "tool_result", "text", "result"}, types)

	init := lines[0]
	require.Equal(t, "sess-aaaa-bbbb", init["session_id"])
	require.Equal(t, "blue-otter-bbbb", init["address"])
	require.Equal(t, "anthropic", init["provider"])
	require.Equal(t, "claude", init["model"])
	require.Equal(t, c.sentRunID, init["run_id"])

	require.Equal(t, map[string]any{"path": "."}, lines[2]["input"])
	require.Equal(t, "main.go", lines[3]["content"])
	require.Equal(t, "Found main.go", lines[5]["text"])
}

func TestRunStructured_FailureStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		events   func(runID string) []any
		status   string
		exitCode int
		errText  string
	}{
		{
			name: "agent error",
			events: func(runID string) []any {
				return []any{pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, Error: "rate limited"}}}
			},
			status: "error", exitCode: headless.ExitAgentError, errText: "rate limited",
		},
		{
			name: "agent event error",
			events: func(runID string) []any {
				return []any{pubsub.Event[proto.AgentEvent]{Payload: proto.AgentEvent{RunID: runID, Error: errors.New("bad api key")}}}
			},
			status: "error", exitCode: headless.ExitAgentError, errText: "bad api key",
		},
		{
			name: "cancelled elsewhere",
			events: func(runID string) []any {
				return []any{pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, Cancelled: true}}}
			},
			status: "cancelled", exitCode: headless.ExitCancelled, errText: "cancelled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newFakeRunClient(tc.events)
			lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatJSON)
			require.Error(t, err)
			require.Equal(t, tc.exitCode, headless.ExitCode(err))
			require.True(t, headless.IsSilent(err), "the result object already reports the error")
			require.EqualError(t, err, tc.errText)
			require.Len(t, lines, 1)
			require.Equal(t, tc.status, lines[0]["status"])
			require.EqualValues(t, tc.exitCode, lines[0]["exit_code"])
		})
	}
}

func TestRunStructured_SendFailure(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.sendErr = errors.New("workspace closing")
	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatJSON)
	require.Equal(t, headless.ExitFailure, headless.ExitCode(err))
	require.Len(t, lines, 1)
	require.Equal(t, "error", lines[0]["status"])
	require.EqualValues(t, headless.ExitFailure, lines[0]["exit_code"])
	require.Equal(t, "failed to send message: workspace closing", lines[0]["error"])
}

func TestRunStructured_SubscribeFailure(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.subErr = errors.New("no stream")
	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatStreamJSON)
	require.Equal(t, headless.ExitFailure, headless.ExitCode(err))
	require.Len(t, lines, 1)
	require.Equal(t, "no stream", lines[0]["error"])
	require.Empty(t, c.sentPrompt, "nothing is sent when the event stream is unavailable")
}

func TestRunStructured_StreamClosed(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.busy = true // nothing is sent on the closed stream
	close(c.events)
	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatJSON)
	require.Equal(t, headless.ExitFailure, headless.ExitCode(err))
	require.Contains(t, lines[0]["error"], "event stream closed")
}

func TestRunStructured_InterruptCancelsServerRun(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	lines, err := runStructuredForTest(t, ctx, c, headless.FormatJSON)
	require.Equal(t, headless.ExitCancelled, headless.ExitCode(err))
	require.Equal(t, "cancelled", lines[0]["status"])
	require.Equal(t, "interrupted", lines[0]["error"])
	require.Equal(t, []string{"sess-aaaa-bbbb"}, c.cancelled)
}

func TestRunStructured_BusySessionSkipsForeignOutput(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(func(runID string) []any {
		return []any{
			asstMsg("prev", "output of the turn ahead of us"),
			pubsub.Event[proto.Message]{Payload: proto.Message{
				ID: "u1", SessionID: "sess-aaaa-bbbb", Role: proto.User,
				Parts: []proto.ContentPart{proto.TextContent{Text: "do the thing"}},
			}},
			asstMsg("m1", "ours"),
			pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, MessageID: "m1", Text: "ours"}},
		}
	})
	c.busy = true

	lines, err := runStructuredForTest(t, t.Context(), c, headless.FormatStreamJSON)
	require.NoError(t, err)
	var texts []string
	for _, l := range lines {
		if l["type"] == "text" {
			texts = append(texts, l["text"].(string))
		}
	}
	require.Equal(t, []string{"ours"}, texts)
}

func TestReportRunError(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, reportRunError(&out, headless.FormatJSON, nil))
	require.Empty(t, out.String())

	plain := errors.New("no providers configured")
	require.Same(t, plain, reportRunError(&out, headless.FormatText, plain), "text mode passes errors through")
	require.Empty(t, out.String())

	silent := &headless.ExitError{Code: headless.ExitAgentError, Silent: true}
	require.Same(t, error(silent), reportRunError(&out, headless.FormatJSON, silent), "already-reported errors are not reported twice")
	require.Empty(t, out.String())

	err := reportRunError(&out, headless.FormatStreamJSON, plain)
	require.True(t, headless.IsSilent(err))
	require.Equal(t, headless.ExitFailure, headless.ExitCode(err))
	require.ErrorIs(t, err, plain)

	lines := decodeLines(t, &out)
	require.Len(t, lines, 1)
	require.Equal(t, "result", lines[0]["type"])
	require.Equal(t, "error", lines[0]["status"])
	require.Equal(t, "no providers configured", lines[0]["error"])
	require.EqualValues(t, headless.ExitFailure, lines[0]["exit_code"])
}

func TestRunCmdOutputFormatFlag(t *testing.T) {
	t.Parallel()

	f := runCmd.Flags().Lookup("output-format")
	require.NotNil(t, f)
	require.Equal(t, "o", f.Shorthand)
	require.Equal(t, "text", f.DefValue)
}

func TestErrorHandlerSkipsSilentErrors(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	errorHandler(&buf, fang.Styles{}, &headless.ExitError{Code: 5, Err: errors.New("x"), Silent: true})
	require.Empty(t, buf.String())

	errorHandler(&buf, fang.Styles{}, errors.New("visible failure"))
	require.Contains(t, buf.String(), "visible failure")
}

func TestCancelServerRun(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	cancelServerRun(c, "ws", "sess")
	require.Equal(t, []string{"sess"}, c.cancelled)
}

func TestInterruptRun(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.queued = true
	interruptRun(c, "ws", "sess", "run-1", true)
	require.Equal(t, []string{"run-1"}, c.dropped)
	require.Empty(t, c.cancelled, "a prompt still queued is dropped, not the active turn cancelled")

	c = newFakeRunClient(nil)
	interruptRun(c, "ws", "sess", "run-1", false)
	require.Empty(t, c.cancelled, "a turn that is not ours is never cancelled")

	c = newFakeRunClient(nil)
	interruptRun(c, "ws", "sess", "run-1", true)
	require.Equal(t, []string{"sess"}, c.cancelled, "our own active turn is cancelled")
}

func TestWorkspaceCwd(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/wd", workspaceCwd(&proto.Workspace{Path: "/p", WorkingDir: "/wd"}))
	require.Equal(t, "/p", workspaceCwd(&proto.Workspace{Path: "/p"}))
}

func (f *fakeRunClient) GetAgentSessionQueuedPrompts(context.Context, string, string) (int, error) {
	return 0, nil
}

func (f *fakeRunClient) GrantPermission(_ context.Context, _ string, req proto.PermissionGrant) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted = append(f.granted, req)
	f.pending.Permissions = nil
	return true, nil
}

func (f *fakeRunClient) AnswerQuestion(_ context.Context, _ string, req proto.QuestionAnswer) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, req)
	f.pending.Questions = nil
	return true, nil
}

func (f *fakeRunClient) setPending(p proto.PendingPrompts) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = p
}

func pendingBash() proto.PendingPrompts {
	return proto.PendingPrompts{Permissions: []proto.PermissionRequest{{
		ID: "perm-1", SessionID: "sess-aaaa-bbbb", ToolCallID: "tc1", ToolName: "bash",
		Action: "execute", Description: "rm -rf build",
	}}}
}

func TestRunHeadless_DelegateBlocksOnPermission(t *testing.T) {
	t.Parallel()

	var c *fakeRunClient
	c = newFakeRunClient(func(runID string) []any {
		return []any{
			asstMsg("m1", "I need to clean the build dir"),
			func() { c.setPending(pendingBash()) },
			pubsub.Event[proto.PermissionRequest]{Payload: pendingBash().Permissions[0]},
		}
	})

	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatStreamJSON, mode: headless.ModeDelegate})
	require.Equal(t, headless.ExitBlocked, headless.ExitCode(err))
	require.Equal(t, proto.PermissionPolicyPrompt, c.sentPolicy)
	require.Empty(t, c.cancelled, "a blocked delegated run stays alive for 'crush session answer'")

	var types []string
	for _, l := range lines {
		types = append(types, l["type"].(string))
	}
	require.Equal(t, []string{"init", "text", "blocked", "result"}, types)

	res := lines[len(lines)-1]
	require.Equal(t, "blocked", res["status"])
	require.EqualValues(t, headless.ExitBlocked, res["exit_code"])
	require.Contains(t, res["error"], "rm -rf build")
	pending := res["pending"].(map[string]any)
	perms := pending["permissions"].([]any)
	require.Len(t, perms, 1)
	require.Equal(t, "perm-1", perms[0].(map[string]any)["id"])
}

func TestRunHeadless_YOLOIgnoresPermissionEvents(t *testing.T) {
	t.Parallel()

	var c *fakeRunClient
	c = newFakeRunClient(func(runID string) []any {
		c.setPending(pendingBash())
		return []any{
			pubsub.Event[proto.PermissionRequest]{Payload: pendingBash().Permissions[0]},
			pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, Text: "done"}},
		}
	})
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, mode: headless.ModeYOLO})
	require.NoError(t, err)
	require.Equal(t, "completed", lines[0]["status"])
}

func TestRunHeadless_DenyReportsPermissionDenied(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(func(runID string) []any {
		return []any{
			pubsub.Event[proto.PermissionNotification]{Payload: proto.PermissionNotification{
				SessionID: "sess-aaaa-bbbb", ToolCallID: "tc1", Denied: true,
			}},
			pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, Text: "I could not run that."}},
		}
	})
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, mode: headless.ModeDeny})
	require.Equal(t, headless.ExitPermissionDenied, headless.ExitCode(err))
	require.Equal(t, proto.PermissionPolicyDeny, c.sentPolicy)
	require.Equal(t, "permission_denied", lines[0]["status"])
	require.Equal(t, "I could not run that.", lines[0]["text"])
}

func TestRunHeadless_TimeoutLeavesSharedRunGoing(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, timeout: 50 * time.Millisecond})
	require.Equal(t, headless.ExitTimeout, headless.ExitCode(err))
	require.Equal(t, "timeout", lines[0]["status"])
	require.Empty(t, c.cancelled)
}

func TestRunHeadless_TimeoutCancelsIsolatedRun(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, timeout: 50 * time.Millisecond, isolated: true})
	require.Equal(t, headless.ExitTimeout, headless.ExitCode(err))
	require.Contains(t, lines[0]["error"], "cancelled because it was isolated")
	require.Equal(t, []string{"sess-aaaa-bbbb"}, c.cancelled)
}

func TestRunHeadless_Detach(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, detach: true, mode: headless.ModeDelegate})
	require.NoError(t, err)
	require.Len(t, lines, 1)
	res := lines[0]
	require.Equal(t, "running", res["status"])
	require.EqualValues(t, 0, res["exit_code"])
	require.Equal(t, "sess-aaaa-bbbb", res["session_id"])
	require.Equal(t, "blue-otter-bbbb", res["address"])
	require.Equal(t, c.sentRunID, res["run_id"])
	require.Equal(t, "do the thing", c.sentPrompt)
	require.Equal(t, proto.PermissionPolicyPrompt, c.sentPolicy)
}

func TestRunHeadless_DetachSendFailure(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(nil)
	c.sendErr = errors.New("closing")
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatJSON, detach: true})
	require.Equal(t, headless.ExitFailure, headless.ExitCode(err))
	require.Equal(t, "failed to send message: closing", lines[0]["error"])
}

func TestRunHeadless_TextMode(t *testing.T) {
	t.Parallel()

	c := newFakeRunClient(func(runID string) []any {
		return []any{pubsub.Event[proto.RunComplete]{Payload: proto.RunComplete{RunID: runID, Text: "  final answer \n"}}}
	})
	lines, err := runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatText, timeout: time.Minute})
	require.NoError(t, err)
	require.Equal(t, "final answer\n", lines[0]["stdout"])

	c = newFakeRunClient(nil)
	lines, err = runHeadlessForTest(t, t.Context(), c, runOptions{format: headless.FormatText, detach: true})
	require.NoError(t, err)
	require.Equal(t, "sess-aaaa-bbbb\n", lines[0]["stdout"], "a detached text run prints the session ID")
}

func TestWriteResultTextErrors(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	res := headless.NewResult(headless.StatusBlocked)
	res.SessionID = "S"
	res.Error = "waiting for permission: rm"
	err := writeResult(&buf, headless.FormatText, nil, res)
	require.Equal(t, headless.ExitBlocked, headless.ExitCode(err))
	require.False(t, headless.IsSilent(err), "text mode errors are printed by the CLI")
	require.EqualError(t, err, "waiting for permission: rm (see 'crush session status S')")

	res = headless.NewResult(headless.StatusPermissionDenied)
	err = writeResult(&buf, headless.FormatText, nil, res)
	require.EqualError(t, err, "permission_denied")
}

func TestRunOptionsNormalize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       runOptions
		wantErr  string
		isolated bool
		modelRef string
		large    string
	}{
		{name: "defaults stay shared", in: runOptions{}},
		{name: "model becomes a session model ref", in: runOptions{largeModel: "anthropic/claude"}, modelRef: "anthropic/claude"},
		{name: "small model forces a private workspace", in: runOptions{smallModel: "haiku"}, isolated: true},
		{name: "model on an existing session forces a private workspace", in: runOptions{largeModel: "m", useLast: true}, isolated: true, large: "m"},
		{name: "small model cannot detach", in: runOptions{smallModel: "haiku", detach: true}, wantErr: "cannot be combined with --detach"},
		{name: "small model cannot delegate", in: runOptions{smallModel: "haiku", mode: headless.ModeDelegate}, wantErr: "needs a private workspace"},
		{name: "isolated cannot detach", in: runOptions{isolated: true, detach: true}, wantErr: "--detach cannot be combined with --isolated"},
		{name: "isolated cannot delegate", in: runOptions{isolated: true, mode: headless.ModeDelegate}, wantErr: "delegate cannot be combined with --isolated"},
		{name: "negative timeout", in: runOptions{timeout: -time.Second}, wantErr: "--timeout must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := tc.in
			err := o.normalize()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.isolated, o.isolated)
			require.Equal(t, tc.modelRef, o.modelRef)
			require.Equal(t, tc.large, o.largeModel)
		})
	}
}

func TestRunOptionsUsesLegacyTextStream(t *testing.T) {
	t.Parallel()

	require.True(t, runOptions{format: headless.FormatText, mode: headless.ModeYOLO}.usesLegacyTextStream())
	require.False(t, runOptions{format: headless.FormatText, mode: headless.ModeDeny}.usesLegacyTextStream(), "deny can end in permission_denied")
	require.False(t, runOptions{format: headless.FormatJSON}.usesLegacyTextStream())
	require.False(t, runOptions{format: headless.FormatText, detach: true}.usesLegacyTextStream())
	require.False(t, runOptions{format: headless.FormatText, timeout: time.Second}.usesLegacyTextStream())
	require.False(t, runOptions{format: headless.FormatText, mode: headless.ModeDelegate}.usesLegacyTextStream())
}

func TestInitEventUsesSessionModelRef(t *testing.T) {
	t.Parallel()

	ws := testRunWorkspace()
	ev := initEvent(ws, &proto.Session{ID: "s", ModelRef: "openai/gpt-6"}, "r", "/c")
	require.Equal(t, "openai", ev.Provider)
	require.Equal(t, "gpt-6", ev.Model)

	ev = initEvent(ws, &proto.Session{ID: "s", ModelRef: "scout"}, "r", "/c")
	require.Empty(t, ev.Provider)
	require.Equal(t, "scout", ev.Model)

	ev = initEvent(ws, &proto.Session{ID: "s"}, "r", "/c")
	require.Equal(t, "anthropic", ev.Provider)
	require.Equal(t, "claude", ev.Model)
}

func TestRunCmdHeadlessFlags(t *testing.T) {
	t.Parallel()

	for name, def := range map[string]string{
		"permission-mode": "yolo",
		"detach":          "false",
		"timeout":         "0s",
		"isolated":        "false",
	} {
		f := runCmd.Flags().Lookup(name)
		require.NotNil(t, f, name)
		require.Equal(t, def, f.DefValue, name)
	}
}
