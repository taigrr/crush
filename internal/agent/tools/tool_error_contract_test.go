package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/filetracker"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/fantasy"
)

// TestToolErrorContract verifies that user-facing tool failures return a
// tool response with IsError=true and a nil error, so the fantasy agent
// loop can surface the failure as a tool_result and the model can react.
// A non-nil error from tool.Run is treated as fatal by fantasy and ends
// the turn — that path is reserved for cancellation / panic recovery only.
func TestToolErrorContract(t *testing.T) {
	t.Parallel()

	type toolCase struct {
		name  string
		tool  fantasy.AgentTool
		call  fantasy.ToolCall
		ctx   func() context.Context
		match string
	}

	workingDir := t.TempDir()
	wd := func(context.Context) string { return workingDir }
	perms := &mockPermissionService{}
	hist := &mockHistoryService{}
	ft := mockFileTrackerService{}

	sessionCtx := func() context.Context {
		return context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	}
	emptyCtx := func() context.Context { return context.Background() }

	mustJSON := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return string(b)
	}

	cases := []toolCase{
		{
			name:  "edit missing session id",
			tool:  NewEditTool(nil, perms, hist, ft, wd),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c1", Name: EditToolName, Input: mustJSON(EditParams{FilePath: "new.txt", NewString: "hi"})},
			match: "session ID is required",
		},
		{
			name:  "edit old_string not found",
			tool:  NewEditTool(nil, perms, hist, ft, wd),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c2", Name: EditToolName, Input: mustJSON(EditParams{FilePath: filepath.Join(workingDir, "nope.txt"), OldString: "a", NewString: "b"})},
			match: "file not found",
		},
		{
			name:  "multiedit missing file_path",
			tool:  NewMultiEditTool(nil, perms, hist, ft, wd),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c3", Name: MultiEditToolName, Input: mustJSON(MultiEditParams{})},
			match: "file_path is required",
		},
		{
			name:  "view missing file_path",
			tool:  NewViewTool(nil, perms, ft, nil, wd),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c4", Name: ViewToolName, Input: mustJSON(ViewParams{})},
			match: "file_path is required",
		},
		{
			name:  "view nonexistent file",
			tool:  NewViewTool(nil, perms, ft, nil, wd),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c5", Name: ViewToolName, Input: mustJSON(ViewParams{FilePath: filepath.Join(workingDir, "missing.txt")})},
			match: "File not found",
		},
		{
			name:  "write missing session id",
			tool:  NewWriteTool(nil, perms, hist, ft, wd),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c6", Name: WriteToolName, Input: mustJSON(WriteParams{FilePath: "x.txt", Content: "hi"})},
			match: "session_id is required",
		},
		{
			name:  "todos missing session id",
			tool:  NewTodosTool(nil),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c7", Name: TodosToolName, Input: mustJSON(TodosParams{})},
			match: "session ID is required",
		},
		{
			name:  "rename missing symbol",
			tool:  NewRenameTool(nil, perms, wd),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c8", Name: RenameToolName, Input: mustJSON(RenameParams{NewName: "foo"})},
			match: "symbol is required",
		},
		{
			name:  "bash missing session id",
			tool:  NewBashTool(perms, wd, &config.Attribution{}, "test-model"),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c9", Name: BashToolName, Input: mustJSON(BashParams{Command: "echo hi", Description: "x"})},
			match: "session ID is required",
		},
		{
			name:  "download missing session id",
			tool:  NewDownloadTool(perms, wd, nil),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c10", Name: DownloadToolName, Input: mustJSON(DownloadParams{URL: "http://example.invalid/", FilePath: "x.bin"})},
			match: "session ID is required",
		},
		{
			name:  "fetch missing session id",
			tool:  NewFetchTool(perms, wd, nil),
			ctx:   emptyCtx,
			call:  fantasy.ToolCall{ID: "c11", Name: FetchToolName, Input: mustJSON(FetchParams{URL: "http://example.invalid/", Format: "text"})},
			match: "session ID is required",
		},
		{
			name:  "editor_context no editor attached",
			tool:  NewEditorContextTool(),
			ctx:   sessionCtx,
			call:  fantasy.ToolCall{ID: "c12", Name: EditorContextToolName, Input: "{}"},
			match: "Editor bridge is not available",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := tc.tool.Run(tc.ctx(), tc.call)
			require.NoError(t, err, "tool must not return a non-nil error for recoverable failures")
			require.True(t, resp.IsError, "response must set IsError=true so the model sees a tool_result")
			require.Contains(t, resp.Content, tc.match)
		})
	}
}

// permCancelService returns (false, context.Canceled) from Request, which
// mirrors what the real permission service returns when the incoming
// context is cancelled mid-prompt. Tools MUST propagate that as a fatal
// non-nil error so fantasy tears the turn down instead of feeding the
// model a "permission request failed" tool_result it will retry.
type permCancelService struct{ *mockPermissionService }

func (permCancelService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return false, context.Canceled
}

// TestToolPermissionCancellationIsFatal guards the reverted permReqErr
// sites: if the permission service returns a non-nil error (only ever
// ctx.Err() in the real implementation), the tool must return that error
// so the run loop can end the turn.
func TestToolPermissionCancellationIsFatal(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	wd := func(context.Context) string { return workingDir }
	perms := permCancelService{mockPermissionService: &mockPermissionService{}}
	hist := &mockHistoryService{}
	ft := mockFileTrackerService{}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "s1")

	tool := NewEditTool(nil, perms, hist, ft, wd)
	input, err := json.Marshal(EditParams{FilePath: filepath.Join(workingDir, "new.txt"), NewString: "hi"})
	require.NoError(t, err)
	_, err = tool.Run(ctx, fantasy.ToolCall{ID: "cancel", Name: EditToolName, Input: string(input)})
	require.ErrorIs(t, err, context.Canceled, "permission-service cancellation must be propagated as a fatal error")
}

// historyCreateFailService returns an error from Create so we can exercise
// the "file was written but recording its history failed" path. Setting
// failGet also makes GetByPathAndSession fail — needed to drive the
// existing-file paths in edit/multiedit/write into the Create fallback.
type historyCreateFailService struct {
	*mockHistoryService
	failGet bool
}

func (historyCreateFailService) Create(ctx context.Context, sessionID, path, content string) (history.File, error) {
	return history.File{}, fmt.Errorf("boom")
}

func (h historyCreateFailService) GetByPathAndSession(ctx context.Context, path, sessionID string) (history.File, error) {
	if h.failGet {
		return history.File{}, fmt.Errorf("no history row")
	}
	return history.File{Path: path, Content: ""}, nil
}

// TestFileMutationHistoryFailureIsRecoverable pins ALL six state-mutation-
// then-history-fails paths across edit/multiedit/write. The file mutation
// already landed on disk, so the tool must surface a recoverable tool
// result (err==nil, IsError=true) that tells the model the file was
// written — otherwise the model would blindly retry the same edit.
func TestFileMutationHistoryFailureIsRecoverable(t *testing.T) {
	t.Parallel()

	const wantMsg = "file was written but recording its history failed"

	type step struct {
		name    string
		newTool func(workingDir string, perms permission.Service, hist history.Service, ft filetracker.Service) fantasy.AgentTool
		toolID  string
		// setup writes the pre-existing file (if any) and returns the
		// json-encoded params.
		setup func(t *testing.T, workingDir string) (target, input string)
		// wantContent is the exact bytes the tool should have written
		// to `target` by the time the history call fails.
		wantContent string
	}

	steps := []step{
		{
			name: "edit createNewFile",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewEditTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: EditToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "new.txt")
				b, err := json.Marshal(EditParams{FilePath: target, NewString: "hello"})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "hello",
		},
		{
			name: "edit replaceContent",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewEditTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: EditToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "replace.txt")
				require.NoError(t, os.WriteFile(target, []byte("aaa BBB ccc"), 0o644))
				b, err := json.Marshal(EditParams{FilePath: target, OldString: "BBB", NewString: "YYY"})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "aaa YYY ccc",
		},
		{
			name: "edit deleteContent",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewEditTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: EditToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "delete.txt")
				require.NoError(t, os.WriteFile(target, []byte("keep DROP keep"), 0o644))
				b, err := json.Marshal(EditParams{FilePath: target, OldString: " DROP", NewString: ""})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "keep keep",
		},
		{
			name: "multiedit newFile",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewMultiEditTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: MultiEditToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "multi-new.txt")
				b, err := json.Marshal(MultiEditParams{FilePath: target, Edits: []MultiEditOperation{{OldString: "", NewString: "seed"}}})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "seed",
		},
		{
			name: "multiedit existingFile",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewMultiEditTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: MultiEditToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "multi-existing.txt")
				require.NoError(t, os.WriteFile(target, []byte("foo bar baz"), 0o644))
				b, err := json.Marshal(MultiEditParams{FilePath: target, Edits: []MultiEditOperation{{OldString: "bar", NewString: "BAR"}}})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "foo BAR baz",
		},
		{
			name: "write",
			newTool: func(wd string, p permission.Service, h history.Service, ft filetracker.Service) fantasy.AgentTool {
				return NewWriteTool(nil, p, h, ft, func(context.Context) string { return wd })
			},
			toolID: WriteToolName,
			setup: func(t *testing.T, wd string) (string, string) {
				target := filepath.Join(wd, "written.txt")
				b, err := json.Marshal(WriteParams{FilePath: target, Content: "written-content"})
				require.NoError(t, err)
				return target, string(b)
			},
			wantContent: "written-content",
		},
	}

	for _, s := range steps {
		s := s
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			workingDir := t.TempDir()
			perms := &mockPermissionService{}
			// failGet=true forces the existing-file paths through the
			// Create fallback so the history-fail branch executes.
			// It's harmless for creation paths (they don't call Get).
			hist := historyCreateFailService{mockHistoryService: &mockHistoryService{}, failGet: true}
			ft := mockFileTrackerService{}
			ctx := context.WithValue(context.Background(), SessionIDContextKey, "s1")

			tool := s.newTool(workingDir, perms, hist, ft)
			target, input := s.setup(t, workingDir)

			resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "hist-" + s.name, Name: s.toolID, Input: input})
			require.NoError(t, err, "history failure must be recoverable")
			require.True(t, resp.IsError, "response must have IsError=true")
			require.Contains(t, resp.Content, wantMsg)

			// The mutation must have landed BEFORE the history call
			// failed; otherwise the model would retry against a stale
			// filesystem view.
			got, statErr := os.ReadFile(target)
			require.NoError(t, statErr, "target file must exist on disk")
			require.Equal(t, s.wantContent, string(got), "target file must contain the tool's payload")
		})
	}
}

// stubSwarmBackend lets a test drive one method to failure while keeping
// the rest inert. It intentionally does not implement session.Service —
// callers only need LookupAddress cancellation coverage for the top-level
// swarm.go site; the Get/Send/CreateSessionInWorkspace sites share the
// same wrap+ErrorIs guard.
type stubSwarmBackend struct {
	lookupErr error
}

func (s stubSwarmBackend) LookupAddress(ctx context.Context, addr string) (SwarmLookupResult, error) {
	return SwarmLookupResult{}, s.lookupErr
}

func (stubSwarmBackend) Send(ctx context.Context, senderSessionID string, target SwarmLookupResult, part message.SwarmMessage) (string, error) {
	return "sent", nil
}

func (stubSwarmBackend) CreateSessionInWorkspace(ctx context.Context, workspaceID, title string) (session.Session, error) {
	return session.Session{}, nil
}

func (stubSwarmBackend) ArchiveSessionInWorkspace(ctx context.Context, workspaceID, sessionID string) error {
	return nil
}

// stubSessionsForSwarm implements just enough of session.Service for the
// swarm tool's sender-lookup step to succeed.
type stubSessionsForSwarm struct {
	session.Service
	sess session.Session
}

func (s stubSessionsForSwarm) Get(ctx context.Context, id string) (session.Session, error) {
	return s.sess, nil
}

// TestSwarmContextCancellationIsFatal guards the swarm.go wrap: when the
// backend returns context.Canceled from LookupAddress, the tool must
// propagate the error so the run loop tears down the turn.
func TestSwarmContextCancellationIsFatal(t *testing.T) {
	t.Parallel()

	be := stubSwarmBackend{lookupErr: context.Canceled}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")
	resp, err := runSwarm(ctx, be, sess, cfg, "ws", SwarmParams{
		Address: "someone-else",
		Prompt:  "hello",
	})
	require.ErrorIs(t, err, context.Canceled, "swarm must propagate context cancellation as fatal")
	require.Zero(t, resp, "response must be zero-valued when returning fatal error")
}

// Keep the small single-target sanity check too — it's cheap and pins
// exactly one path (edit.createNewFile) as a regression tripwire.
func TestEditHistoryFailureIsRecoverable(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	wd := func(context.Context) string { return workingDir }
	perms := &mockPermissionService{}
	hist := historyCreateFailService{mockHistoryService: &mockHistoryService{}}
	ft := mockFileTrackerService{}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "s1")

	tool := NewEditTool(nil, perms, hist, ft, wd)
	target := filepath.Join(workingDir, "newfile.txt")
	input, err := json.Marshal(EditParams{FilePath: target, NewString: "hello"})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "hist", Name: EditToolName, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "file was written but recording its history failed")

	_, statErr := os.Stat(target)
	require.NoError(t, statErr)
}

// TestNetworkToolCancellationIsFatal verifies that when the parent
// context is cancelled mid-request, the fetch tool propagates the
// cancellation as a fatal error rather than swallowing it into a
// recoverable "failed to fetch URL: context canceled" tool_result that
// the model would happily retry. The same guard pattern is used in
// download.go and sourcegraph.go on identical call sites; testing one
// tool is sufficient to pin the shared idiom.
func TestNetworkToolCancellationIsFatal(t *testing.T) {
	t.Parallel()

	// A server that blocks forever so client.Do only returns once the
	// context is cancelled from underneath it.
	blockCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh
	}))
	t.Cleanup(func() {
		close(blockCh)
		srv.Close()
	})

	workingDir := t.TempDir()
	wd := func(context.Context) string { return workingDir }
	tool := NewFetchTool(&mockPermissionService{}, wd, srv.Client())

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), SessionIDContextKey, "s1"))
	// Cancel shortly after the request starts. Timer is generous
	// enough to avoid races under -race but short enough to keep the
	// test snappy.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	input, err := json.Marshal(FetchParams{URL: srv.URL, Format: "text"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "cancel", Name: FetchToolName, Input: string(input)})
	require.ErrorIs(t, err, context.Canceled, "parent-ctx cancellation during client.Do must be fatal")
	require.Zero(t, resp, "fatal cancellation must return a zero-valued ToolResponse")
}
