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
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// fakeRunClient is a scripted [structuredRunClient]. When SendMessage is
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

	sentPrompt string
	sentRunID  string
	cancelled  []string
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

func (f *fakeRunClient) SendMessage(_ context.Context, _, _, runID, prompt string, _ ...message.Attachment) error {
	f.mu.Lock()
	f.sentPrompt = prompt
	f.sentRunID = runID
	f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.script != nil {
		for _, ev := range f.script(runID) {
			f.events <- ev
		}
	}
	return nil
}

func (f *fakeRunClient) GetAgentSessionInfo(context.Context, string, string) (*proto.AgentSession, error) {
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
	var out bytes.Buffer
	sess := &proto.Session{ID: "sess-aaaa-bbbb", Color: "blue", Animal: "otter"}
	err := runStructured(ctx, c, testRunWorkspace(), sess, "do the thing", format, &out)
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

func TestWorkspaceCwd(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/wd", workspaceCwd(&proto.Workspace{Path: "/p", WorkingDir: "/wd"}))
	require.Equal(t, "/p", workspaceCwd(&proto.Workspace{Path: "/p"}))
}
