// Package mcpserver exposes Crush's headless run loop as a Model Context
// Protocol server, so another coding agent (Claude Code, clawdbot,
// Cursor, ...) can start Crush runs, wait for them, answer their
// permission prompts and questions, and review their changes through
// tool calls.
//
// The server is a thin adapter: every tool delegates to the same
// headless helpers used by `crush run` and `crush session`, talking to
// the regular Crush server over its HTTP API.
package mcpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/taigrr/crush/internal/diff"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/version"
)

// Client is the subset of the Crush server client used by the tools.
type Client interface {
	headless.StatusClient
	headless.AnswerClient
	headless.SessionLister
	SubscribeEvents(ctx context.Context, workspaceID string) (<-chan any, error)
	SendAgentMessage(ctx context.Context, workspaceID string, msg proto.AgentMessage) error
	CancelAgentSession(ctx context.Context, workspaceID, sessionID string) error
	CreateSessionWithModel(ctx context.Context, workspaceID, title, modelRef string) (*proto.Session, error)
	ResolveSwarmAddress(ctx context.Context, address string) (proto.SwarmTarget, error)
}

// Connector returns a ready-to-use client and workspace for a project
// directory. Implementations should cache connections and keep the
// workspace attached for the lifetime of the MCP server.
type Connector interface {
	Connect(ctx context.Context, dir string) (Client, *proto.Workspace, error)
}

// Options configures [New].
type Options struct {
	// DefaultCwd is used when a tool call does not pass cwd.
	DefaultCwd string
	// DefaultMode is the permission mode for crush_start when the caller
	// does not pick one, and the most permissive mode a caller may pick:
	// with the default delegate, a caller can ask for delegate or deny
	// but cannot turn its own runs into yolo.
	DefaultMode headless.PermissionMode
	// DefaultWait and MaxWait bound crush_wait. MaxWait should stay below
	// the calling client's own tool timeout.
	DefaultWait time.Duration
	MaxWait     time.Duration
	// ProgressInterval is how often a long wait sends a progress
	// notification when the caller asked for progress.
	ProgressInterval time.Duration
}

const (
	defaultWait             = 5 * time.Minute
	maxWait                 = 9 * time.Minute
	defaultProgressInterval = 15 * time.Second
	maxDiffBytes            = 200 * 1024
)

// Instructions is sent to MCP clients on initialization.
const Instructions = `Crush is a coding agent with LSP diagnostics, skills, MCP servers, hooks, snapshots, and multi-session swarm support. Use it to delegate self-contained coding tasks.

Workflow:
1. crush_start starts a run and returns immediately with a session_id.
2. crush_wait waits for it (up to ~9 minutes per call). Call it again while status is "timeout".
3. If status is "blocked", the run is waiting on a permission request or question listed in "pending". Ask the user if needed, then call crush_respond (set wait=true to keep going).
4. When status is "completed", review "text", "files_changed", and "diagnostics" (LSP errors left in changed files), and crush_diff for the exact changes.

Follow-ups go to the same session with crush_start and the session parameter. Sessions can also be referenced by their swarm address (e.g. "coral-otter-3b9e"). A person can watch or take over any session with its attach_command.`

// New builds the MCP server.
func New(conn Connector, opts Options) *mcp.Server {
	if opts.DefaultMode == "" {
		opts.DefaultMode = headless.ModeDelegate
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = maxWait
	}
	if opts.DefaultWait <= 0 {
		opts.DefaultWait = min(defaultWait, opts.MaxWait)
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = defaultProgressInterval
	}
	t := &tools{conn: conn, opts: opts}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "crush",
		Title:   "Crush",
		Version: version.Version,
	}, &mcp.ServerOptions{Instructions: Instructions})

	mcp.AddTool(s, &mcp.Tool{
		Name: "crush_start",
		Description: "Start a Crush coding run in the background and return its session. " +
			"Pass session to send a follow-up to an existing session instead of starting a new one. " +
			"Then call crush_wait.",
	}, t.start)
	mcp.AddTool(s, &mcp.Tool{
		Name: "crush_wait",
		Description: "Wait for a Crush session's run to finish or block, and return the result " +
			"(status, final text, files changed, LSP diagnostics, usage; files and usage are session totals). " +
			"Status \"timeout\" means it is still running: call again. " +
			"Status \"blocked\" means it needs crush_respond.",
	}, t.wait)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crush_status",
		Description: "Report whether a Crush session is running, blocked (with what it is waiting on), or idle (with its last result), without waiting.",
	}, t.status)
	mcp.AddTool(s, &mcp.Tool{
		Name: "crush_respond",
		Description: "Answer what a blocked Crush session is waiting on: a permission decision " +
			"(allow, allow_session, deny) or an answer to its question. Set wait=true to wait for the run afterwards.",
	}, t.respond)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crush_cancel",
		Description: "Cancel a Crush session's running turn.",
	}, t.cancel)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crush_list",
		Description: "List Crush sessions for a project, most recently updated first.",
	}, t.list)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "crush_diff",
		Description: "Show the unified diff of every file a Crush session changed, from before its first edit to now.",
	}, t.diff)
	return s
}

type tools struct {
	conn Connector
	opts Options
}

// StartInput is the input of crush_start.
type StartInput struct {
	Prompt         string `json:"prompt" jsonschema:"the task or follow-up for Crush"`
	Cwd            string `json:"cwd,omitempty" jsonschema:"project directory to run in (defaults to the directory crush mcp was started in)"`
	Session        string `json:"session,omitempty" jsonschema:"continue this session (ID, hash prefix, or swarm address) instead of creating one"`
	Model          string `json:"model,omitempty" jsonschema:"model for a new session: a configured role name, provider/model, or a model ID"`
	PermissionMode string `json:"permission_mode,omitempty" jsonschema:"delegate (default: tool permissions and questions wait for crush_respond), deny (deny anything not pre-approved), or yolo (approve everything; only if the server allows it)"`
	Title          string `json:"title,omitempty" jsonschema:"title for a new session"`
}

// SessionInput identifies a session.
type SessionInput struct {
	Session string `json:"session" jsonschema:"session ID, hash prefix, or swarm address"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"project directory of the session (defaults to the directory crush mcp was started in; other projects are found by swarm address)"`
}

// WaitInput is the input of crush_wait.
type WaitInput struct {
	SessionInput
	TimeoutSeconds int `json:"timeout_seconds,omitempty" jsonschema:"how long to wait before returning status timeout (default 300, capped by the server, 540 unless configured lower)"`
}

// RespondInput is the input of crush_respond.
type RespondInput struct {
	SessionInput
	Decision       string   `json:"decision,omitempty" jsonschema:"for a permission request: allow, allow_session, or deny"`
	Answer         []string `json:"answer,omitempty" jsonschema:"for a question: the chosen option(s) or free-text reply"`
	CancelQuestion bool     `json:"cancel_question,omitempty" jsonschema:"dismiss the pending question without answering"`
	RequestID      string   `json:"request_id,omitempty" jsonschema:"which pending item to resolve when several are pending"`
	Wait           bool     `json:"wait,omitempty" jsonschema:"after answering, wait for the run to finish or block again"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" jsonschema:"with wait, how long to wait (default 300, capped by the server)"`
}

// RespondOutput is the output of crush_respond.
type RespondOutput struct {
	Answer headless.AnswerResult `json:"answer"`
	// Result is set when wait was requested.
	Result *headless.Result `json:"result,omitempty"`
}

// CancelOutput is the output of crush_cancel.
type CancelOutput struct {
	SessionID string `json:"session_id"`
	WasBusy   bool   `json:"was_busy"`
}

// ListInput is the input of crush_list.
type ListInput struct {
	Cwd   string `json:"cwd,omitempty" jsonschema:"project directory (defaults to the directory crush mcp was started in)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of sessions to return (default 20)"`
}

// SessionSummary is one entry of crush_list.
type SessionSummary struct {
	SessionID     string `json:"session_id"`
	Address       string `json:"address,omitempty"`
	Title         string `json:"title"`
	Busy          bool   `json:"busy"`
	UpdatedAt     int64  `json:"updated_at"`
	AttachCommand string `json:"attach_command"`
}

// ListOutput is the output of crush_list.
type ListOutput struct {
	Cwd      string           `json:"cwd"`
	Sessions []SessionSummary `json:"sessions"`
}

// FileDiff is one file of crush_diff.
type FileDiff struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
	Diff      string `json:"diff"`
}

// DiffOutput is the output of crush_diff.
type DiffOutput struct {
	SessionID string     `json:"session_id"`
	Files     []FileDiff `json:"files"`
	Truncated bool       `json:"truncated,omitempty"`
}

func (t *tools) start(ctx context.Context, _ *mcp.CallToolRequest, in StartInput) (*mcp.CallToolResult, headless.Result, error) {
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, headless.Result{}, errors.New("prompt is required")
	}
	mode := t.opts.DefaultMode
	if in.PermissionMode != "" {
		var err error
		if mode, err = headless.ParsePermissionMode(in.PermissionMode); err != nil {
			return nil, headless.Result{}, err
		}
		if !t.opts.DefaultMode.Allows(mode) {
			return nil, headless.Result{}, fmt.Errorf(
				"permission_mode %q is not allowed: this server allows at most %q (start crush mcp with --permission-mode %s to permit it)",
				mode, t.opts.DefaultMode, mode)
		}
	}

	var (
		c    Client
		ws   *proto.Workspace
		sess *proto.Session
		err  error
	)
	if in.Session != "" {
		if in.Model != "" {
			return nil, headless.Result{}, errors.New("model can only be set for a new session")
		}
		c, ws, sess, err = t.resolve(ctx, in.Cwd, in.Session)
	} else {
		c, ws, err = t.connect(ctx, in.Cwd)
		if err == nil {
			title := cmp.Or(strings.TrimSpace(in.Title), "headless")
			sess, err = c.CreateSessionWithModel(ctx, ws.ID, title, in.Model)
		}
	}
	if err != nil {
		return nil, headless.Result{}, err
	}

	runID := newRunID()
	if err := c.SendAgentMessage(ctx, ws.ID, proto.AgentMessage{
		SessionID:        sess.ID,
		RunID:            runID,
		Prompt:           in.Prompt,
		PermissionPolicy: mode.Policy(),
	}); err != nil {
		return nil, headless.Result{}, err
	}

	cwd := workspaceCwd(ws)
	res := headless.NewResult(headless.StatusRunning)
	res.SessionID = sess.ID
	res.Address = headless.SessionAddress(*sess)
	res.WorkspaceID = ws.ID
	res.RunID = runID
	res.Cwd = cwd
	res.AttachCommand = headless.AttachCommand(cwd, sess.ID)
	return nil, res, nil
}

func (t *tools) wait(ctx context.Context, req *mcp.CallToolRequest, in WaitInput) (*mcp.CallToolResult, headless.Result, error) {
	c, ws, sess, err := t.resolve(ctx, in.Cwd, in.Session)
	if err != nil {
		return nil, headless.Result{}, err
	}
	return nil, t.waitFor(ctx, req, c, ws, sess, in.TimeoutSeconds), nil
}

func (t *tools) waitFor(ctx context.Context, req *mcp.CallToolRequest, c Client, ws *proto.Workspace, sess *proto.Session, timeoutSeconds int) headless.Result {
	timeout := t.opts.DefaultWait
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	timeout = min(timeout, t.opts.MaxWait)

	stop := t.startProgress(ctx, req, sess.ID)
	outcome := headless.WaitSession(ctx, c, ws.ID, sess.ID, timeout, nil)
	stop()

	enrichCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return headless.Finish(enrichCtx, c, headless.Baseline{}, ws.ID, sess.ID, workspaceCwd(ws), "", outcome)
}

// startProgress sends periodic progress notifications while a wait is in
// flight, when the caller asked for them. They keep clients that reset
// their tool timeout on progress from giving up on a long run.
func (t *tools) startProgress(ctx context.Context, req *mcp.CallToolRequest, sessionID string) func() {
	if req == nil || req.Session == nil || req.Params == nil {
		return func() {}
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(t.opts.ProgressInterval)
		defer ticker.Stop()
		started := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				elapsed := time.Since(started).Round(time.Second)
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      elapsed.Seconds(),
					Message:       fmt.Sprintf("Crush session %s still running (%s)", sessionID, elapsed),
				})
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (t *tools) status(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, headless.SessionStatus, error) {
	c, ws, sess, err := t.resolve(ctx, in.Cwd, in.Session)
	if err != nil {
		return nil, headless.SessionStatus{}, err
	}
	st, err := headless.BuildStatus(ctx, c, ws.ID, workspaceCwd(ws), *sess)
	return nil, st, err
}

func (t *tools) respond(ctx context.Context, req *mcp.CallToolRequest, in RespondInput) (*mcp.CallToolResult, RespondOutput, error) {
	c, ws, sess, err := t.resolve(ctx, in.Cwd, in.Session)
	if err != nil {
		return nil, RespondOutput{}, err
	}
	ans := headless.AnswerRequest{
		RequestID:      in.RequestID,
		Permission:     proto.PermissionAction(in.Decision),
		Answers:        in.Answer,
		CancelQuestion: in.CancelQuestion,
	}
	res, err := headless.Answer(ctx, c, ws.ID, sess.ID, ans)
	if err != nil {
		return nil, RespondOutput{}, err
	}
	out := RespondOutput{Answer: res}
	if in.Wait {
		r := t.waitFor(ctx, req, c, ws, sess, in.TimeoutSeconds)
		out.Result = &r
	}
	return nil, out, nil
}

func (t *tools) cancel(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, CancelOutput, error) {
	c, ws, sess, err := t.resolve(ctx, in.Cwd, in.Session)
	if err != nil {
		return nil, CancelOutput{}, err
	}
	busy := false
	if info, err := c.GetAgentSessionInfo(ctx, ws.ID, sess.ID); err == nil && info != nil {
		busy = info.IsBusy
	}
	if err := c.CancelAgentSession(ctx, ws.ID, sess.ID); err != nil {
		return nil, CancelOutput{}, err
	}
	return nil, CancelOutput{SessionID: sess.ID, WasBusy: busy}, nil
}

func (t *tools) list(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
	c, ws, err := t.connect(ctx, in.Cwd)
	if err != nil {
		return nil, ListOutput{}, err
	}
	sessions, err := c.ListSessions(ctx, ws.ID)
	if err != nil {
		return nil, ListOutput{}, err
	}
	sessions = slices.DeleteFunc(sessions, func(s proto.Session) bool { return s.ParentSessionID != "" })
	slices.SortFunc(sessions, func(a, b proto.Session) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	cwd := workspaceCwd(ws)
	out := ListOutput{Cwd: cwd, Sessions: make([]SessionSummary, 0, len(sessions))}
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, SessionSummary{
			SessionID:     s.ID,
			Address:       headless.SessionAddress(s),
			Title:         s.Title,
			Busy:          s.IsBusy,
			UpdatedAt:     s.UpdatedAt,
			AttachCommand: headless.AttachCommand(cwd, s.ID),
		})
	}
	return nil, out, nil
}

func (t *tools) diff(ctx context.Context, _ *mcp.CallToolRequest, in SessionInput) (*mcp.CallToolResult, DiffOutput, error) {
	c, ws, sess, err := t.resolve(ctx, in.Cwd, in.Session)
	if err != nil {
		return nil, DiffOutput{}, err
	}
	files, err := c.ListSessionHistoryFiles(ctx, ws.ID, sess.ID)
	if err != nil {
		return nil, DiffOutput{}, err
	}
	return nil, sessionDiff(sess.ID, workspaceCwd(ws), files), nil
}

// sessionDiff diffs, per path, the earliest recorded version (the
// content before the session's first edit) against the latest one.
func sessionDiff(sessionID, cwd string, files []proto.File) DiffOutput {
	type span struct{ first, last proto.File }
	spans := make(map[string]*span)
	for _, f := range files {
		sp, ok := spans[f.Path]
		if !ok {
			spans[f.Path] = &span{first: f, last: f}
			continue
		}
		if f.Version < sp.first.Version {
			sp.first = f
		}
		if f.Version > sp.last.Version {
			sp.last = f
		}
	}
	paths := make([]string, 0, len(spans))
	for p := range spans {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	out := DiffOutput{SessionID: sessionID, Files: []FileDiff{}}
	budget := maxDiffBytes
	for _, p := range paths {
		sp := spans[p]
		if sp.first.Content == sp.last.Content {
			continue
		}
		name := displayPath(cwd, p)
		unified, add, rem := diff.GenerateDiff(sp.first.Content, sp.last.Content, name)
		fd := FileDiff{Path: p, Additions: add, Removals: rem, Diff: unified}
		if len(unified) > budget {
			fd.Diff = ""
			out.Truncated = true
		} else {
			budget -= len(unified)
		}
		out.Files = append(out.Files, fd)
	}
	return out
}

func (t *tools) connect(ctx context.Context, cwd string) (Client, *proto.Workspace, error) {
	dir := cmp.Or(strings.TrimSpace(cwd), t.opts.DefaultCwd)
	if dir == "" {
		return nil, nil, errors.New("cwd is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	return t.conn.Connect(ctx, abs)
}

// resolve finds a session by reference in the cwd's workspace, falling
// back to a server-wide swarm lookup so sessions in other projects can
// be addressed without knowing their directory.
func (t *tools) resolve(ctx context.Context, cwd, ref string) (Client, *proto.Workspace, *proto.Session, error) {
	c, ws, err := t.connect(ctx, cwd)
	if err != nil {
		return nil, nil, nil, err
	}
	sess, elsewhere, err := headless.LocateSession(ctx, c, ws.ID, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if elsewhere == nil {
		return c, ws, sess, nil
	}
	if c, ws, err = t.conn.Connect(ctx, elsewhere.Path); err != nil {
		return nil, nil, nil, err
	}
	sess, err = c.GetSession(ctx, ws.ID, elsewhere.SessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	return c, ws, sess, nil
}

func workspaceCwd(ws *proto.Workspace) string {
	if ws.WorkingDir != "" {
		return ws.WorkingDir
	}
	return ws.Path
}

func newRunID() string { return uuid.New().String() }

// displayPath shows p relative to cwd when it is inside it. Both sides
// are symlink-resolved so /var vs /private/var style aliases match.
func displayPath(cwd, p string) string {
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(resolveDir(cwd), filepath.Join(resolveDir(filepath.Dir(p)), filepath.Base(p))); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func resolveDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}
