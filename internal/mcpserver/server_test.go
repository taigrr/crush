package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/headless/headlesstest"
	"github.com/taigrr/crush/internal/mcpserver"
	"github.com/taigrr/crush/internal/proto"
)

// mapConnector serves fixed connections keyed by (symlink-resolved) dir.
type mapConnector struct {
	mu    sync.Mutex
	conns map[string]conn
	calls atomic.Int32
}

type conn struct {
	c  *client.Client
	ws *proto.Workspace
}

func (m *mapConnector) add(dir string, c *client.Client, ws *proto.Workspace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[canonical(dir)] = conn{c, ws}
}

func (m *mapConnector) Connect(_ context.Context, dir string) (mcpserver.Client, *proto.Workspace, error) {
	m.calls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	cn, ok := m.conns[canonical(dir)]
	if !ok {
		return nil, nil, errors.New("unknown project " + dir)
	}
	return cn.c, cn.ws, nil
}

func canonical(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

type env struct {
	h        *headlesstest.Harness
	conn     *mapConnector
	session  *mcp.ClientSession
	progress atomic.Int32
}

func newEnv(t *testing.T, opts mcpserver.Options) *env {
	t.Helper()
	h := headlesstest.New(t)
	conn := &mapConnector{conns: map[string]conn{}}
	conn.add(h.Cwd, h.Client, h.Workspace)
	if opts.DefaultCwd == "" {
		opts.DefaultCwd = h.Cwd
	}
	e := &env{h: h, conn: conn}

	srv := mcpserver.New(conn, opts)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })

	cl := mcp.NewClient(&mcp.Implementation{Name: "test-agent"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) {
			e.progress.Add(1)
		},
	})
	cs, err := cl.Connect(t.Context(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	e.session = cs
	return e
}

// call invokes a tool and decodes its structured output into a map.
func (e *env) call(t *testing.T, name string, args any) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	res, err := e.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	if res.IsError {
		return nil, res
	}
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out, res
}

func errorText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestMCP_ListsTools(t *testing.T) {
	e := newEnv(t, mcpserver.Options{})
	res, err := e.session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		require.NotEmpty(t, tool.Description, tool.Name)
		require.NotNil(t, tool.InputSchema, tool.Name)
	}
	require.ElementsMatch(t, []string{
		"crush_start", "crush_wait", "crush_status", "crush_respond",
		"crush_cancel", "crush_list", "crush_diff",
	}, names)

	init := e.session.InitializeResult()
	require.Equal(t, "crush", init.ServerInfo.Name)
	require.Contains(t, init.Instructions, "crush_start")
}

func TestMCP_DelegatedRunRoundTrip(t *testing.T) {
	e := newEnv(t, mcpserver.Options{})
	e.h.Agent.AskPermission = true
	e.h.Agent.Reply = "ran it"

	started, _ := e.call(t, "crush_start", map[string]any{"prompt": "run echo", "title": "mcp task"})
	require.Equal(t, "running", started["status"])
	sessionID := started["session_id"].(string)
	address := started["address"].(string)
	require.NotEmpty(t, address)
	require.Contains(t, started["attach_command"], sessionID)

	waited, _ := e.call(t, "crush_wait", map[string]any{"session": address, "timeout_seconds": 10})
	require.Equal(t, "blocked", waited["status"], "delegate is the default MCP permission mode")
	perms := waited["pending"].(map[string]any)["permissions"].([]any)
	require.Len(t, perms, 1)
	require.Equal(t, "echo hello", perms[0].(map[string]any)["description"])

	status, _ := e.call(t, "crush_status", map[string]any{"session": sessionID})
	require.Equal(t, "blocked", status["state"])
	require.Equal(t, "mcp task", status["title"])

	responded, _ := e.call(t, "crush_respond", map[string]any{
		"session": sessionID, "decision": "allow", "wait": true, "timeout_seconds": 10,
	})
	answer := responded["answer"].(map[string]any)
	require.Equal(t, "permission", answer["kind"])
	require.Equal(t, true, answer["resolved"])
	result := responded["result"].(map[string]any)
	require.Equal(t, "completed", result["status"])
	require.Equal(t, "ran it", result["text"])

	e.h.WaitIdle(t)
	status, _ = e.call(t, "crush_status", map[string]any{"session": sessionID})
	require.Equal(t, "idle", status["state"])
	require.Equal(t, "ran it", status["last"].(map[string]any)["text"])
}

func TestMCP_YOLOStartAndFollowUp(t *testing.T) {
	e := newEnv(t, mcpserver.Options{DefaultMode: headless.ModeYOLO})
	e.h.Agent.AskPermission = true

	started, _ := e.call(t, "crush_start", map[string]any{"prompt": "first", "permission_mode": "yolo"})
	sessionID := started["session_id"].(string)
	waited, _ := e.call(t, "crush_wait", map[string]any{"session": sessionID})
	require.Equal(t, "completed", waited["status"])

	e.h.WaitIdle(t)
	followUp, _ := e.call(t, "crush_start", map[string]any{"prompt": "second", "session": sessionID, "permission_mode": "deny"})
	require.Equal(t, sessionID, followUp["session_id"], "a follow-up reuses the session")
	waited, _ = e.call(t, "crush_wait", map[string]any{"session": sessionID})
	require.Equal(t, "permission_denied", waited["status"])

	listed, _ := e.call(t, "crush_list", map[string]any{})
	sessions := listed["sessions"].([]any)
	require.Len(t, sessions, 1)
	require.Equal(t, sessionID, sessions[0].(map[string]any)["session_id"])
}

func TestMCP_WaitTimeoutSendsProgressAndCancel(t *testing.T) {
	e := newEnv(t, mcpserver.Options{ProgressInterval: 20 * time.Millisecond, DefaultMode: headless.ModeYOLO})
	e.h.Agent.Hold = make(chan struct{})
	t.Cleanup(func() { close(e.h.Agent.Hold) })

	started, _ := e.call(t, "crush_start", map[string]any{"prompt": "long task", "permission_mode": "yolo"})
	sessionID := started["session_id"].(string)

	params := &mcp.CallToolParams{Name: "crush_wait", Arguments: map[string]any{"session": sessionID, "timeout_seconds": 1}}
	params.SetProgressToken("tok-1")
	res, err := e.session.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, res.IsError)
	b, _ := json.Marshal(res.StructuredContent)
	var waited map[string]any
	require.NoError(t, json.Unmarshal(b, &waited))
	require.Equal(t, "timeout", waited["status"])
	require.Positive(t, e.progress.Load(), "long waits report progress to clients that ask for it")

	cancelled, _ := e.call(t, "crush_cancel", map[string]any{"session": sessionID})
	require.Equal(t, true, cancelled["was_busy"])
	waited, _ = e.call(t, "crush_wait", map[string]any{"session": sessionID, "timeout_seconds": 10})
	require.Equal(t, "cancelled", waited["status"])
}

func TestMCP_MaxWaitCapsTimeout(t *testing.T) {
	e := newEnv(t, mcpserver.Options{MaxWait: 50 * time.Millisecond, DefaultMode: headless.ModeYOLO})
	e.h.Agent.Hold = make(chan struct{})
	t.Cleanup(func() { close(e.h.Agent.Hold) })

	started, _ := e.call(t, "crush_start", map[string]any{"prompt": "x", "permission_mode": "yolo"})
	begin := time.Now()
	waited, _ := e.call(t, "crush_wait", map[string]any{"session": started["session_id"], "timeout_seconds": 3600})
	require.Equal(t, "timeout", waited["status"])
	require.Less(t, time.Since(begin), 5*time.Second)
}

func TestMCP_CrossProjectSessionBySwarmAddress(t *testing.T) {
	e := newEnv(t, mcpserver.Options{})

	otherDir := t.TempDir()
	other := e.h.NewClient(t)
	ows, err := other.CreateWorkspace(t.Context(), proto.Workspace{Path: otherDir, DataDir: t.TempDir()})
	require.NoError(t, err)
	e.conn.add(otherDir, other, ows)
	sess, err := other.CreateSession(t.Context(), ows.ID, "elsewhere")
	require.NoError(t, err)

	status, _ := e.call(t, "crush_status", map[string]any{"session": headless.SessionAddress(*sess)})
	require.Equal(t, sess.ID, status["session_id"], "sessions in other projects are found by swarm address")
	require.Equal(t, ows.ID, status["workspace_id"])
	require.Equal(t, "elsewhere", status["title"])
}

func TestMCP_Diff(t *testing.T) {
	e := newEnv(t, mcpserver.Options{})
	sess, err := e.h.Client.CreateSession(t.Context(), e.h.Workspace.ID, "diff")
	require.NoError(t, err)

	path := filepath.Join(e.h.Cwd, "main.go")
	hist := e.h.Backend.History
	_, err = hist.Create(t.Context(), sess.ID, path, "package main\n")
	require.NoError(t, err)
	_, err = hist.CreateVersion(t.Context(), sess.ID, path, "package main\n\nfunc main() {}\n")
	require.NoError(t, err)
	unchanged := filepath.Join(e.h.Cwd, "same.go")
	_, err = hist.Create(t.Context(), sess.ID, unchanged, "x")
	require.NoError(t, err)

	out, _ := e.call(t, "crush_diff", map[string]any{"session": sess.ID})
	files := out["files"].([]any)
	require.Len(t, files, 1, "files whose content did not change are omitted")
	f := files[0].(map[string]any)
	require.Equal(t, path, f["path"])
	require.EqualValues(t, 2, f["additions"])
	require.Contains(t, f["diff"], "+func main() {}")
	require.Contains(t, f["diff"], "a/main.go", "paths inside the project are shown relative")
}

func TestMCP_Errors(t *testing.T) {
	e := newEnv(t, mcpserver.Options{})

	_, res := e.call(t, "crush_start", map[string]any{"prompt": "  "})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "prompt is required")

	_, res = e.call(t, "crush_start", map[string]any{"prompt": "x", "permission_mode": "yolo"})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), `permission_mode "yolo" is not allowed`, "a caller cannot raise its own permissions")

	_, res = e.call(t, "crush_start", map[string]any{"prompt": "x", "permission_mode": "maybe"})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "invalid permission mode")

	_, res = e.call(t, "crush_start", map[string]any{"prompt": "x", "session": "nope", "model": "m"})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "model can only be set for a new session")

	_, res = e.call(t, "crush_status", map[string]any{"session": "no-such-session"})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "not found")

	_, res = e.call(t, "crush_list", map[string]any{"cwd": t.TempDir()})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "unknown project")

	sess, err := e.h.Client.CreateSession(t.Context(), e.h.Workspace.ID, "idle")
	require.NoError(t, err)
	_, res = e.call(t, "crush_respond", map[string]any{"session": sess.ID, "decision": "allow"})
	require.True(t, res.IsError)
	require.Contains(t, errorText(res), "nothing is pending")
}
