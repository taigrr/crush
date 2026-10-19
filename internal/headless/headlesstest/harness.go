// Package headlesstest provides an in-process Crush server with a
// scripted agent for end-to-end tests of the headless surfaces
// (`crush run`, `crush session wait|status|answer|cancel`, `crush mcp`).
//
// The server, HTTP API, backend, permission service, session store,
// message store, and event stream are all real; only the LLM turn is
// replaced by [Agent], which records an assistant message, optionally
// runs a permission-gated tool call, and publishes the authoritative RunComplete
// exactly like the production coordinator.
package headlesstest

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/fantasy"
)

// Harness is a running server with one workspace whose agent is scripted.
type Harness struct {
	Server    *httptest.Server
	Host      string
	Cwd       string
	Workspace *proto.Workspace
	Backend   *backend.Workspace
	Agent     *Agent
	Client    *client.Client

	backend *backend.Backend
}

// New starts a server, creates a real workspace for a temp directory over
// HTTP, and installs a scripted [Agent] into it. HOME and XDG_* are
// redirected to temp dirs, so callers must not run in parallel.
func New(t *testing.T) *Harness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	srv := server.NewServer(nil, "tcp", "127.0.0.1:0")
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	u, err := url.Parse(hs.URL)
	require.NoError(t, err)

	cwd := t.TempDir()
	c, err := client.NewClient(cwd, "tcp", u.Host)
	require.NoError(t, err)

	ws, err := c.CreateWorkspace(t.Context(), proto.Workspace{Path: cwd, DataDir: t.TempDir()})
	require.NoError(t, err)

	bws, err := srv.Backend().GetWorkspace(ws.ID)
	require.NoError(t, err)
	a := &Agent{app: bws.App, accepted: make(map[string]int), busy: make(map[string]int), pendingCancel: make(map[string]bool), cancels: make(map[string][]context.CancelFunc), Reply: "done"}
	bws.AgentCoordinator = a

	h := &Harness{
		Server:    hs,
		Host:      u.Host,
		Cwd:       cwd,
		Workspace: ws,
		Backend:   bws,
		Agent:     a,
		Client:    c,
		backend:   srv.Backend(),
	}
	h.ReleaseOnCleanup(t)
	return h
}

// ReleaseOnCleanup closes every workspace and database the server opened
// when the test ends, before temp directories created so far are
// removed. Windows cannot delete a database file that is still open, so
// tests that create more project directories after [New] must call this
// again afterwards.
func (h *Harness) ReleaseOnCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		h.backend.ShutdownWorkspaces()
		db.ResetPool()
	})
}

// NewClient returns another client for the harness server rooted at the
// harness directory.
func (h *Harness) NewClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.NewClient(h.Cwd, "tcp", h.Host)
	require.NoError(t, err)
	return c
}

// WaitIdle blocks until the scripted agent has no run in flight.
func (h *Harness) WaitIdle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return !h.Agent.IsBusy() }, 5*time.Second, 10*time.Millisecond)
}

// Agent is a scripted [agent.Coordinator].
type Agent struct {
	app *app.App

	// Reply is the assistant text recorded for each turn.
	Reply string
	// AskPermission, when set, makes each turn request permission for a
	// bash tool call before replying. A denial ends the turn the way the
	// real tools do: the denied tool result is the last thing recorded.
	AskPermission bool
	// Hold, when non-nil, blocks each turn until it is closed (or the
	// run is cancelled).
	Hold chan struct{}

	mu sync.Mutex
	// accepted counts runs between BeginAccepted and RunAccepted, which
	// the real coordinator also reports as busy.
	accepted map[string]int
	busy     map[string]int
	// pendingCancel records a Cancel that arrived while a run was
	// accepted but not yet running, so it takes effect on entry, as
	// the real coordinator's cancel-on-entry does.
	pendingCancel map[string]bool
	cancels       map[string][]context.CancelFunc
	policy        []permission.SessionPolicy
}

// Policies returns the run permission policy each turn ran under, in
// order ("" when the turn carried none).
func (a *Agent) Policies() []permission.SessionPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]permission.SessionPolicy(nil), a.policy...)
}

func (a *Agent) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return a.RunAccepted(ctx, nil, sessionID, prompt, attachments...)
}

func (a *Agent) RunAccepted(ctx context.Context, _ *agent.AcceptedRun, sessionID, prompt string, _ ...message.Attachment) (*fantasy.AgentResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	if a.accepted[sessionID] > 0 {
		a.accepted[sessionID]--
	}
	a.busy[sessionID]++
	a.cancels[sessionID] = append(a.cancels[sessionID], cancel)
	if a.pendingCancel[sessionID] {
		delete(a.pendingCancel, sessionID)
		cancel()
	}
	a.policy = append(a.policy, permission.PolicyFromContext(ctx))
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.busy[sessionID]--
		a.mu.Unlock()
	}()

	if _, err := a.app.Messages.Create(context.WithoutCancel(ctx), sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: prompt}},
	}); err != nil {
		return nil, err
	}

	reply := a.Reply
	cancelled := false
	denied := false
	if a.AskPermission {
		// Mirror a real tool turn: an assistant message carrying the
		// tool call, then a tool message with its result.
		toolCallID := fmt.Sprintf("tc-%d", time.Now().UnixNano())
		if _, err := a.app.Messages.Create(context.WithoutCancel(ctx), sessionID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Running echo"},
				message.ToolCall{ID: toolCallID, Name: "bash", Input: `{"command":"echo hello"}`, Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: time.Now().Unix()},
			},
		}); err != nil {
			return nil, err
		}
		granted, err := a.app.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID:   sessionID,
			ToolCallID:  toolCallID,
			ToolName:    "bash",
			Action:      "execute",
			Description: "echo hello",
			Path:        a.app.WorkingDir(),
		})
		result := message.ToolResult{ToolCallID: toolCallID, Name: "bash", Content: "hello"}
		switch {
		case err != nil:
			cancelled = true
			result.IsError, result.Content = true, "cancelled"
		case !granted:
			denied = true
			result.IsError, result.Content = true, tools.PermissionDeniedMessage
		}
		if _, err := a.app.Messages.Create(context.WithoutCancel(ctx), sessionID, message.CreateMessageParams{
			Role:  message.Tool,
			Parts: []message.ContentPart{result},
		}); err != nil {
			return nil, err
		}
	}
	if a.Hold != nil && !cancelled && !denied {
		select {
		case <-a.Hold:
		case <-ctx.Done():
			cancelled = true
		}
	}

	var msg message.Message
	if denied {
		// A denied tool call stops the turn: no further assistant text.
		msgs, err := a.app.Messages.List(context.WithoutCancel(ctx), sessionID)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.Role == message.Assistant {
				msg = m
			}
		}
		reply = msg.Content().Text
	} else {
		reason := message.FinishReasonEndTurn
		if cancelled {
			reason = message.FinishReasonCanceled
		}
		var err error
		msg, err = a.app.Messages.Create(context.WithoutCancel(ctx), sessionID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: reply},
				message.Finish{Reason: reason, Time: time.Now().Unix()},
			},
		})
		if err != nil {
			return nil, err
		}
	}

	rc := notify.RunComplete{SessionID: sessionID, RunID: agent.RunIDFromContext(ctx), MessageID: msg.ID, Text: reply}
	if cancelled {
		rc.Cancelled = true
		rc.Error = context.Canceled.Error()
	}
	a.app.RunCompletions().PublishMustDeliver(context.WithoutCancel(ctx), pubsub.UpdatedEvent, rc)
	agent.MarkRunCompletePublished(ctx)
	if cancelled {
		return nil, context.Canceled
	}
	return &fantasy.AgentResult{}, nil
}

func (a *Agent) BeginAccepted(sessionID string) *agent.AcceptedRun {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accepted[sessionID]++
	return nil
}

func (a *Agent) Cancel(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.cancels[sessionID] {
		c()
	}
	if a.accepted[sessionID] > 0 {
		a.pendingCancel[sessionID] = true
	}
}

func (a *Agent) CancelAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, list := range a.cancels {
		for _, c := range list {
			c()
		}
	}
}

func (a *Agent) IsSessionBusy(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busy[sessionID]+a.accepted[sessionID] > 0
}

func (a *Agent) IsBusy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range a.busy {
		if n > 0 {
			return true
		}
	}
	for _, n := range a.accepted {
		if n > 0 {
			return true
		}
	}
	return false
}

func (a *Agent) QueuedPrompts(string) int                           { return 0 }
func (a *Agent) QueuedPromptsList(string) []string                  { return nil }
func (a *Agent) ClearQueue(string)                                  {}
func (a *Agent) DropQueued(string, string) bool                     { return false }
func (a *Agent) Summarize(context.Context, string) error            { return nil }
func (a *Agent) RegenerateTitle(context.Context, string) error      { return nil }
func (a *Agent) Model() agent.Model                                 { return agent.Model{} }
func (a *Agent) UpdateModels(context.Context) error                 { return nil }
func (a *Agent) UpdateModelsWhenIdle(context.Context) (bool, error) { return false, nil }
func (a *Agent) SetGoal(string, string)                             {}
func (a *Agent) ClearGoal(string)                                   {}

func (a *Agent) GoalStatus(string) (string, int, int, bool) {
	return "", 0, 0, false
}
