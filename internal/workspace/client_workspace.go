package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/log"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/worktree"
)

// ClientWorkspace implements the Workspace interface by delegating all
// operations to a remote server via the client SDK. It caches the
// proto.Workspace returned at creation time and refreshes it after
// config-mutating operations.
// worktreeCacheTTL is how long a cached worktree path remains valid.
// The cache is also invalidated on any worktree mutation.
const worktreeCacheTTL = 1 * time.Minute

type ClientWorkspace struct {
	client *client.Client

	mu              sync.RWMutex
	ws              proto.Workspace
	activeSessionID string

	// Subscription state for runtime workspace switching. program is the
	// bubbletea program events are forwarded to; subCancel cancels the
	// current event stream; switchRequested tells the subscribe loop the
	// stream closed because of a switch (reconnect) rather than an
	// unexpected drop.
	subMu           sync.Mutex
	program         *tea.Program
	subCancel       context.CancelFunc
	switchRequested bool
	connState       ConnectionState

	// stopped is closed by Shutdown to stop the reconnect loop from
	// sleeping through further backoff delays once the workspace is
	// being torn down.
	stopped  chan struct{}
	stopOnce sync.Once

	// reconnectNow is pulsed (non-blocking send) by SwitchWorkspace to
	// abort an in-progress backoff sleep so the loop reconnects to the
	// now-updated workspace immediately, rather than waiting out a
	// stale backoff timer (up to reconnectMaxDelay) left over from a
	// dropped connection. Buffered so a pulse sent while the loop is
	// not sleeping is not lost; drained at the top of each iteration
	// so it only affects the sleep that follows the switch.
	reconnectNow chan struct{}

	// subscribeEventsFn is normally client.SubscribeEvents; overridable
	// in tests to simulate connection drops without a real server.
	subscribeEventsFn func(ctx context.Context, id string) (<-chan any, error)

	// subscribeGlobalEventsFn is normally client.SubscribeGlobalEvents;
	// overridable in tests. It feeds the observe-only global attention
	// stream. globalSubCancel cancels the in-flight global stream on
	// shutdown.
	subscribeGlobalEventsFn func(ctx context.Context) (<-chan any, error)
	globalSubCancel         context.CancelFunc

	// reconnectDelayOverride, when non-zero, replaces reconnectBaseDelay
	// for the reconnect backoff. Used by tests to avoid multi-second
	// sleeps.
	reconnectDelayOverride time.Duration

	// backoffObserver, when non-nil, is called with each delay passed
	// to waitBackoff. Test-only hook for asserting the backoff
	// progression (e.g. that a switch resets it to the base delay).
	backoffObserver func(time.Duration)

	// Cached active worktree to avoid HTTP round-trips on every
	// WorkingDir() call. cachedWorktreeValid distinguishes "checked
	// and no worktree" from "never checked".
	cachedWorktree      *worktree.Worktree
	cachedWorktreeValid bool
	cachedWorktreeTime  time.Time

	// agentState memoizes the agent/session status RPCs the TUI polls on
	// every Update and View; see agentStateCache.
	agentState agentStateCache

	// heldMu guards held and updating. held is the list of prompts
	// that could not be handed to a server (refused as draining, or
	// sent while the stream was down); they are redelivered, in order,
	// once the event stream reconnects. updating is set only when a
	// server actually reported draining, so the reconnect state says
	// "updating" for an update and plain "reconnecting" for a crash.
	heldMu   sync.Mutex
	held     []heldPrompt
	updating bool

	// creationArgs is the exact POST /v1/workspaces body this client
	// used to attach, kept so a re-attach after a server swap carries
	// the same data dir, yolo/debug flags, env (editor bridge), and
	// launch cwd instead of the server's resolved snapshot. Guarded by
	// mu. Zero until SetCreationArgs; reattachByPath then derives what
	// it can from the snapshot. SwitchWorkspace rewrites Path to the
	// switched-to root so a later re-attach follows the switch.
	creationArgs proto.Workspace

	// createWorkspaceFn is normally client.CreateWorkspace; overridable
	// in tests to simulate a server swap that hands out a fresh
	// workspace id for the same path.
	createWorkspaceFn func(ctx context.Context, ws proto.Workspace) (*proto.Workspace, error)
	// sendMessageFn dispatches a prompt (or steer) to the server; the
	// default routes to client.SendMessage or client.SteerMessage by
	// p.steer. Overridable in tests to simulate a draining server; it is
	// the single hook every send and held-prompt replay goes through, so
	// a fake catches steers too.
	sendMessageFn func(ctx context.Context, id string, p heldPrompt) error
}

// heldPrompt is a prompt the client is holding until the server that
// refused it (draining for an update) has been replaced.
type heldPrompt struct {
	// path is the workspace root the prompt was addressed to. A held
	// prompt is delivered only while the client is still attached to a
	// workspace at that path; if the user switched workspaces during
	// the update it is dropped rather than sent to the wrong one.
	path        string
	sessionID   string
	runID       string
	prompt      string
	attachments []message.Attachment
	// steer marks a mid-turn steer (see Client.SteerMessage): sent with
	// no RunID and with the soft interrupt raised on the target's step.
	steer bool
}

// ErrServerUpdating is returned by AgentRun/AgentRunBTW when the prompt
// could not be handed to a server right now — it refused the prompt
// because it is draining for an update, or the event stream is down (the
// old server has exited and its replacement is not up yet). The prompt
// has NOT been lost: it is held and delivered automatically once the
// client reconnects.
var ErrServerUpdating = errors.New("server is updating; your message is held and will be sent when it reconnects")

// NewClientWorkspace creates a new ClientWorkspace that proxies all
// operations through the given client SDK. The ws parameter is the
// proto.Workspace snapshot returned by the server at creation time.
func NewClientWorkspace(c *client.Client, ws proto.Workspace) *ClientWorkspace {
	if ws.Config != nil {
		ws.Config.SetupAgents()
	}
	w := &ClientWorkspace{
		client:       c,
		ws:           ws,
		stopped:      make(chan struct{}),
		reconnectNow: make(chan struct{}, 1),
	}
	w.subscribeEventsFn = c.SubscribeEvents
	w.subscribeGlobalEventsFn = c.SubscribeGlobalEvents
	w.createWorkspaceFn = c.CreateWorkspace
	w.sendMessageFn = func(ctx context.Context, id string, p heldPrompt) error {
		if p.steer {
			return c.SteerMessage(ctx, id, p.sessionID, p.prompt)
		}
		return c.SendMessage(ctx, id, p.sessionID, p.runID, p.prompt, p.attachments...)
	}
	return w
}

// SetCreationArgs records the request this client attached with so a
// re-attach after a server swap can replay it verbatim.
func (w *ClientWorkspace) SetCreationArgs(args proto.Workspace) {
	w.mu.Lock()
	w.creationArgs = args
	w.mu.Unlock()
}

// refreshWorkspace re-fetches the workspace from the server, updating
// the cached snapshot. Called after config-mutating operations.
func (w *ClientWorkspace) refreshWorkspace() {
	updated, err := w.client.GetWorkspace(context.Background(), w.workspaceID())
	if err != nil {
		slog.Error("Failed to refresh workspace", "error", err)
		return
	}
	if updated.Config != nil {
		updated.Config.SetupAgents()
	}
	w.mu.Lock()
	w.ws = *updated
	w.mu.Unlock()
}

// cached returns a snapshot of the cached workspace.
func (w *ClientWorkspace) cached() proto.Workspace {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ws
}

// workspaceID returns the cached workspace ID.
func (w *ClientWorkspace) workspaceID() string {
	return w.cached().ID
}

// -- Sessions --

func (w *ClientWorkspace) CreateSession(ctx context.Context, title string) (session.Session, error) {
	sess, err := w.client.CreateSession(ctx, w.workspaceID(), title)
	if err != nil {
		return session.Session{}, err
	}
	return protoToSession(*sess), nil
}

func (w *ClientWorkspace) GetSession(ctx context.Context, sessionID string) (session.Session, error) {
	sess, err := w.client.GetSession(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return session.Session{}, err
	}
	return protoToSession(*sess), nil
}

func (w *ClientWorkspace) ListSessions(ctx context.Context) ([]session.Session, error) {
	protoSessions, err := w.client.ListSessions(ctx, w.workspaceID())
	if err != nil {
		return nil, err
	}
	sessions := make([]session.Session, len(protoSessions))
	for i, s := range protoSessions {
		sessions[i] = protoToSession(s)
	}
	return sessions, nil
}

func (w *ClientWorkspace) ListSessionImportSources(ctx context.Context) ([]sessionimport.SourceInfo, error) {
	return w.client.ListSessionImportSources(ctx)
}

func (w *ClientWorkspace) DiscoverSessionImports(ctx context.Context, source sessionimport.Source) ([]sessionimport.Candidate, error) {
	return w.client.DiscoverSessionImports(ctx, string(source))
}

func (w *ClientWorkspace) ImportSessions(ctx context.Context, paths []string, from sessionimport.Source) ([]sessionimport.Result, error) {
	return w.client.ImportSessions(ctx, w.workspaceID(), paths, string(from))
}

func (w *ClientWorkspace) SaveSession(ctx context.Context, sess session.Session) (session.Session, error) {
	saved, err := w.client.SaveSession(ctx, w.workspaceID(), sessionToProto(sess))
	if err != nil {
		return session.Session{}, err
	}
	return protoToSession(*saved), nil
}

func (w *ClientWorkspace) DeleteSession(ctx context.Context, sessionID string) error {
	return w.client.DeleteSession(ctx, w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) ArchiveSession(ctx context.Context, sessionID string) error {
	return w.client.ArchiveSession(ctx, w.workspaceID(), "", sessionID)
}

// ArchiveSessionInWorkspace archives a session in an explicit workspace,
// which may be a workspace other than the one this client is attached to
// (including a detached, registry-known workspace routed by root). When
// workspaceID is empty the server resolves the workspace from root.
func (w *ClientWorkspace) ArchiveSessionInWorkspace(ctx context.Context, workspaceID, root, sessionID string) error {
	return w.client.ArchiveSession(ctx, workspaceID, root, sessionID)
}

func (w *ClientWorkspace) UnarchiveSession(ctx context.Context, sessionID string) error {
	return w.client.UnarchiveSession(ctx, w.workspaceID(), "", sessionID)
}

func (w *ClientWorkspace) MarkSessionSeen(ctx context.Context, sessionID string) error {
	return w.client.MarkSessionSeen(ctx, w.workspaceID(), "", sessionID)
}

// MarkSessionSeenInWorkspace marks a session read in an explicit workspace,
// which may differ from the attached one (including a detached workspace
// routed by root). When workspaceID is empty the server resolves the
// workspace from root.
func (w *ClientWorkspace) MarkSessionSeenInWorkspace(ctx context.Context, workspaceID, root, sessionID string) error {
	return w.client.MarkSessionSeen(ctx, workspaceID, root, sessionID)
}

// SetSessionFavoriteInWorkspace pins or unpins a session in an explicit
// workspace, which may differ from the attached one (including a detached
// workspace routed by root). When workspaceID is empty the server resolves
// the workspace from root.
func (w *ClientWorkspace) SetSessionFavoriteInWorkspace(ctx context.Context, workspaceID, root, sessionID string, favorite bool) error {
	return w.client.SetSessionFavorite(ctx, workspaceID, root, sessionID, favorite)
}

func (w *ClientWorkspace) ListArchivedSessions(ctx context.Context) ([]session.Session, error) {
	protoSessions, err := w.client.ListArchivedSessions(ctx, w.workspaceID())
	if err != nil {
		return nil, err
	}
	sessions := make([]session.Session, len(protoSessions))
	for i, s := range protoSessions {
		sessions[i] = protoToSession(s)
	}
	return sessions, nil
}

func (w *ClientWorkspace) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("%s$$%s", messageID, toolCallID)
}

func (w *ClientWorkspace) ParseAgentToolSessionID(sessionID string) (string, string, bool) {
	parts := strings.Split(sessionID, "$$")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// SetCurrentSession reports the session this client is currently
// viewing to the server. Empty sessionID clears the entry. Errors
// are propagated to the caller; the TUI logs and ignores them since
// the presence record is a hint, not correctness-critical state.
func (w *ClientWorkspace) SetCurrentSession(ctx context.Context, sessionID string) error {
	return w.client.SetCurrentSession(ctx, w.workspaceID(), sessionID)
}

// -- Messages --

func (w *ClientWorkspace) ListMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	msgs, err := w.client.ListMessages(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return nil, err
	}
	return protoToMessages(msgs), nil
}

func (w *ClientWorkspace) ListUserMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	msgs, err := w.client.ListUserMessages(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return nil, err
	}
	return protoToMessages(msgs), nil
}

func (w *ClientWorkspace) ListAllUserMessages(ctx context.Context) ([]message.Message, error) {
	msgs, err := w.client.ListAllUserMessages(ctx, w.workspaceID())
	if err != nil {
		return nil, err
	}
	return protoToMessages(msgs), nil
}

// PeekMessages returns a session's messages from the workspace rooted at
// root — attached or registry-detached, this client's own workspace or a
// foreign one — without switching this client's workspace.
func (w *ClientWorkspace) PeekMessages(ctx context.Context, root, sessionID string) ([]message.Message, error) {
	msgs, err := w.client.PeekMessages(ctx, root, sessionID)
	if err != nil {
		return nil, err
	}
	return protoToMessages(msgs), nil
}

// PeekSessionInfo returns a session's metadata and history files from the
// workspace rooted at root — attached or registry-detached, this client's
// own workspace or a foreign one — without switching this client's
// workspace. Backs the session sidebar's live preview of the right
// info-sidebar.
func (w *ClientWorkspace) PeekSessionInfo(ctx context.Context, root, sessionID string) (session.Session, []history.File, error) {
	res, err := w.client.PeekSessionInfo(ctx, root, sessionID)
	if err != nil {
		return session.Session{}, nil, err
	}
	return protoToSession(res.Session), protoToFiles(res.Files), nil
}

// EmbedPendingCount reports how many past messages a backfill would embed.
func (w *ClientWorkspace) EmbedPendingCount(ctx context.Context) (int, error) {
	return w.client.EmbeddingsPending(ctx, w.workspaceID())
}

// EmbedBackfill embeds past messages lacking a vector under the active
// embedding model, returning the count embedded.
func (w *ClientWorkspace) EmbedBackfill(ctx context.Context) (int, error) {
	return w.client.BackfillEmbeddings(ctx, w.workspaceID())
}

// EmbedStatus reports the embedding index state for progress display.
func (w *ClientWorkspace) EmbedStatus(ctx context.Context) (proto.EmbeddingStatus, error) {
	return w.client.EmbeddingStatus(ctx, w.workspaceID())
}

// SearchHistory runs hybrid history search over this workspace and
// returns per-session hits.
func (w *ClientWorkspace) SearchHistory(ctx context.Context, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error) {
	return w.client.SearchHistory(ctx, w.workspaceID(), params)
}

// -- Agent --

func (w *ClientWorkspace) AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error {
	// A non-empty RunID ensures the queued message is kept for its
	// own dedicated turn by drainQueueForStep rather than being
	// folded silently into the current streaming step. The TUI
	// does not consume notify.RunComplete for completion detection
	// (it observes message events directly), so the RunComplete
	// event that fires is harmlessly ignored.
	w.agentState.invalidate()
	return w.sendOrHold(ctx, heldPrompt{
		path:        w.workspacePath(),
		sessionID:   sessionID,
		runID:       uuid.New().String(),
		prompt:      prompt,
		attachments: attachments,
	})
}

// AgentRunBTW sends a "by the way" aside message that is folded into the
// current streaming step at the earliest opportunity rather than queued for
// its own dedicated turn. It is sent as a steer: no RunID, so
// drainQueueForStep folds it into the active step context, plus the
// session's soft interrupt so long-running tools (bash, job_output) wrap
// up early and the model sees the message without waiting for them.
func (w *ClientWorkspace) AgentRunBTW(ctx context.Context, sessionID, prompt string) error {
	w.agentState.invalidate()
	return w.sendOrHold(ctx, heldPrompt{path: w.workspacePath(), sessionID: sessionID, prompt: "[btw] " + prompt, steer: true})
}

// AgentRunAside folds a message into the active turn at the next step
// boundary. The empty RunID makes drainQueueForStep fold it rather than
// give it a turn of its own; unlike AgentRunBTW it does not raise the
// soft interrupt, so running tools finish at their own pace.
func (w *ClientWorkspace) AgentRunAside(ctx context.Context, sessionID, prompt string) error {
	return w.client.SendMessage(ctx, w.workspaceID(), sessionID, "", "[btw] "+prompt)
}

// AgentSoftInterrupt raises the session's soft interrupt with no message
// attached: a running shell command is handed back to the model as a
// background job and the turn continues.
func (w *ClientWorkspace) AgentSoftInterrupt(sessionID string) {
	_ = w.client.SoftInterruptAgentSession(context.Background(), w.workspaceID(), sessionID)
}

// AgentBackgroundTool moves a single in-flight tool call to the
// background; see Client.BackgroundAgentToolCall.
func (w *ClientWorkspace) AgentBackgroundTool(ctx context.Context, sessionID, toolCallID string) error {
	return w.client.BackgroundAgentToolCall(ctx, w.workspaceID(), sessionID, toolCallID)
}

// workspacePath returns the root of the workspace this client is
// currently attached to.
func (w *ClientWorkspace) workspacePath() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ws.Path
}

// sendOrHold sends p, or — when the server refuses it because it is
// draining for an update — holds it for redelivery after the reconnect
// and returns ErrServerUpdating so the caller can tell the user the
// message is parked rather than failed. Holding is the only correct
// response: the server has not accepted the prompt, and the turn it
// would have run cannot start until the replacement server is up.
//
// The same holds during the gap between the old server exiting and the
// new one answering: the event stream is down (connState != Connected),
// a send fails at the transport, and the editor has already been
// cleared, so the only way not to lose the text is to hold it.
func (w *ClientWorkspace) sendOrHold(ctx context.Context, p heldPrompt) error {
	if w.ConnectionState() != ConnectionStateConnected && w.heldFor(p.path) > 0 {
		// Already holding for this workspace: preserve order rather
		// than racing the reconnect with a send that would land ahead
		// of earlier ones.
		return w.hold(p, false)
	}
	err := w.sendMessageFn(ctx, w.workspaceID(), p)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, client.ErrServerDraining):
		return w.hold(p, true)
	case w.ConnectionState() != ConnectionStateConnected:
		slog.Info("Send failed while the event stream is down; holding prompt", "session_id", p.sessionID, "error", err)
		return w.hold(p, false)
	}
	return err
}

// hold parks p for redelivery after the next reconnect. draining marks
// that a server explicitly reported an update in progress.
func (w *ClientWorkspace) hold(p heldPrompt, draining bool) error {
	w.heldMu.Lock()
	w.held = append(w.held, p)
	w.updating = w.updating || draining
	n := len(w.held)
	w.heldMu.Unlock()
	slog.Info("Holding prompt for redelivery after reconnect", "draining", draining, "session_id", p.sessionID, "held", n)
	return ErrServerUpdating
}

// HeldPrompts reports how many prompts are parked awaiting redelivery,
// for any workspace.
func (w *ClientWorkspace) HeldPrompts() int {
	w.heldMu.Lock()
	defer w.heldMu.Unlock()
	return len(w.held)
}

// heldFor reports how many prompts are parked for the workspace at path.
func (w *ClientWorkspace) heldFor(path string) int {
	w.heldMu.Lock()
	defer w.heldMu.Unlock()
	n := 0
	for _, p := range w.held {
		if p.path == path {
			n++
		}
	}
	return n
}

// flushHeldPrompts redelivers prompts held during a server update, in
// order, against the (re)connected server. Prompts held for a workspace
// other than the one this client is attached to now (the user switched
// meanwhile) stay held until the client returns there. A prompt the new
// server also refuses as draining (a second update in quick succession)
// is kept, along with everything queued behind it for the same
// workspace; any other failure is returned with the prompt's text so the
// UI can hand it back to the user, and the prompt is dropped so a
// permanently bad one cannot wedge the queue.
func (w *ClientWorkspace) flushHeldPrompts(ctx context.Context) (sent int, failed []FailedPrompt, keptElsewhere int) {
	w.heldMu.Lock()
	pending := w.held
	w.held = nil
	w.updating = false
	w.heldMu.Unlock()
	if len(pending) == 0 {
		return 0, nil, 0
	}
	wsID, wsPath := w.workspaceID(), w.workspacePath()
	var keep []heldPrompt
	draining := false
	for _, p := range pending {
		if p.path != wsPath {
			keep = append(keep, p)
			keptElsewhere++
			continue
		}
		if draining {
			keep = append(keep, p)
			continue
		}
		err := w.sendMessageFn(ctx, wsID, p)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, client.ErrServerDraining):
			draining = true
			keep = append(keep, p)
		default:
			slog.Error("Failed to redeliver held prompt after server update", "session_id", p.sessionID, "error", err)
			failed = append(failed, FailedPrompt{SessionID: p.sessionID, Prompt: p.prompt, Attachments: p.attachments, Err: err})
		}
	}
	if len(keep) > 0 {
		w.heldMu.Lock()
		w.held = append(keep, w.held...)
		w.updating = w.updating || draining
		w.heldMu.Unlock()
	}
	return sent, failed, keptElsewhere
}

// reattachByPath re-creates this client's claim on the workspace rooted
// at the same path. After a server swap the replacement server knows
// nothing of the old workspace id, so subscribing to it 404s forever;
// CreateWorkspace is first-wins by resolved path and hands back the id
// the new server assigned (or the same id, if the old server is in fact
// still there). Sessions live in the workspace database and keep their
// ids, so the active session is preserved.
//
// The request replays the client's original creation args (data dir,
// yolo/debug, env, launch cwd) so the re-attached workspace is
// configured identically; a client that has since SwitchWorkspace'd
// re-attaches to the switched-to root instead. If a switch lands while
// the request is in flight, the result is discarded so it cannot
// clobber the switch (the loop reconnects to the switched-to workspace
// on its own).
func (w *ClientWorkspace) reattachByPath(ctx context.Context) error {
	w.mu.RLock()
	req := w.creationArgs
	snapshot := w.ws
	w.mu.RUnlock()
	if req.Path == "" {
		// No recorded args (a caller other than the CLI built this
		// workspace): fall back to the server's resolved root with the
		// per-process flags its snapshot reports.
		req = proto.Workspace{Path: snapshot.Path, DataDir: snapshot.DataDir, Debug: snapshot.Debug, YOLO: snapshot.YOLO}
	}
	if req.Path == "" {
		return errors.New("workspace has no path to re-attach by")
	}
	created, err := w.createWorkspaceFn(ctx, req)
	if err != nil {
		return err
	}
	if created.Config != nil {
		created.Config.SetupAgents()
	}
	w.subMu.Lock()
	switched := w.switchRequested
	w.subMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if switched || w.ws.Path != snapshot.Path {
		// A SwitchWorkspace landed while the request was in flight;
		// its result must not be clobbered. The loop reconnects to the
		// switched-to workspace on its own.
		return errors.New("workspace switched during re-attach; deferring to the switch")
	}
	changed := w.ws.ID != created.ID
	w.ws = *created
	w.cachedWorktree = nil
	w.cachedWorktreeValid = false
	if changed {
		slog.Info("Re-attached to workspace on replacement server", "path", created.Path, "workspace_id", created.ID)
	}
	return nil
}

func (w *ClientWorkspace) AgentRunShellCommand(ctx context.Context, sessionID, command string) (proto.ShellCommandResponse, error) {
	return w.client.RunShellCommand(ctx, w.workspaceID(), sessionID, command)
}

func (w *ClientWorkspace) AgentCancel(sessionID string) {
	w.agentState.invalidate()
	_ = w.client.CancelAgentSession(context.Background(), w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) AgentCancelAll() {
	w.agentState.invalidate()
	_ = w.client.CancelAgent(context.Background(), w.workspaceID())
}

func (w *ClientWorkspace) AgentIsBusy() bool {
	info := w.agentInfo(context.Background())
	return info != nil && info.IsBusy
}

func (w *ClientWorkspace) AgentIsSessionBusy(sessionID string) bool {
	return w.sessionBusy(context.Background(), sessionID)
}

func (w *ClientWorkspace) AgentModel() AgentModel {
	info, err := w.client.GetAgentInfo(context.Background(), w.workspaceID())
	if err != nil {
		return AgentModel{}
	}
	return AgentModel{
		CatwalkCfg: info.Model,
		ModelCfg:   info.ModelCfg,
	}
}

func (w *ClientWorkspace) AgentIsReady() bool {
	info := w.agentInfo(context.Background())
	return info != nil && info.IsReady
}

func (w *ClientWorkspace) AgentReadiness(ctx context.Context) (bool, error) {
	info, err := w.client.GetAgentInfo(ctx, w.workspaceID())
	if err != nil {
		return false, err
	}
	return info.IsReady, nil
}

func (w *ClientWorkspace) AgentQueuedPrompts(sessionID string) int {
	return w.sessionQueued(context.Background(), sessionID)
}

func (w *ClientWorkspace) AgentQueuedPromptsList(sessionID string) []string {
	prompts, err := w.client.GetAgentSessionQueuedPromptsList(context.Background(), w.workspaceID(), sessionID)
	if err != nil {
		return nil
	}
	return prompts
}

func (w *ClientWorkspace) AgentClearQueue(sessionID string) {
	w.agentState.invalidate()
	_ = w.client.ClearAgentSessionQueuedPrompts(context.Background(), w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) AgentSetGoal(sessionID, condition string) error {
	return w.client.SetAgentSessionGoal(context.Background(), w.workspaceID(), sessionID, condition)
}

func (w *ClientWorkspace) AgentSetWorkingDir(sessionID, dir string) error {
	return w.client.SetAgentSessionWorkingDir(context.Background(), w.workspaceID(), sessionID, dir)
}

func (w *ClientWorkspace) AgentClearGoal(sessionID string) error {
	return w.client.ClearAgentSessionGoal(context.Background(), w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) AgentGoalStatus(sessionID string) (proto.GoalStatus, error) {
	return w.client.GetAgentSessionGoal(context.Background(), w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) AgentSummarize(ctx context.Context, sessionID string) error {
	return w.client.AgentSummarizeSession(ctx, w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) AgentGenerateTitle(ctx context.Context, sessionID string) error {
	return w.client.AgentGenerateTitle(ctx, w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) UpdateAgentModel(ctx context.Context) error {
	return w.client.UpdateAgent(ctx, w.workspaceID())
}

func (w *ClientWorkspace) InitCoderAgent(ctx context.Context) error {
	return w.client.InitiateAgentProcessing(ctx, w.workspaceID())
}

func (w *ClientWorkspace) ServerVersion(ctx context.Context) (proto.VersionInfo, error) {
	vi, err := w.client.VersionInfo(ctx)
	if err != nil {
		return proto.VersionInfo{}, err
	}
	return *vi, nil
}

func (w *ClientWorkspace) GetDefaultSmallModel(providerID string) config.SelectedModel {
	model, err := w.client.GetDefaultSmallModel(context.Background(), w.workspaceID(), providerID)
	if err != nil {
		return config.SelectedModel{}
	}
	return *model
}

// -- Permissions --

func (w *ClientWorkspace) PermissionGrant(perm permission.PermissionRequest) bool {
	resolved, _ := w.client.GrantPermission(context.Background(), w.workspaceID(), proto.PermissionGrant{
		Permission: proto.PermissionRequest{
			ID:          perm.ID,
			SessionID:   perm.SessionID,
			ToolCallID:  perm.ToolCallID,
			ToolName:    perm.ToolName,
			Description: perm.Description,
			Action:      perm.Action,
			Path:        perm.Path,
			Params:      perm.Params,
		},
		Action: proto.PermissionAllow,
	})
	return resolved
}

func (w *ClientWorkspace) PermissionGrantPersistent(perm permission.PermissionRequest) bool {
	resolved, _ := w.client.GrantPermission(context.Background(), w.workspaceID(), proto.PermissionGrant{
		Permission: proto.PermissionRequest{
			ID:          perm.ID,
			SessionID:   perm.SessionID,
			ToolCallID:  perm.ToolCallID,
			ToolName:    perm.ToolName,
			Description: perm.Description,
			Action:      perm.Action,
			Path:        perm.Path,
			Params:      perm.Params,
		},
		Action: proto.PermissionAllowForSession,
	})
	return resolved
}

func (w *ClientWorkspace) PermissionDeny(perm permission.PermissionRequest) bool {
	resolved, _ := w.client.GrantPermission(context.Background(), w.workspaceID(), proto.PermissionGrant{
		Permission: proto.PermissionRequest{
			ID:          perm.ID,
			SessionID:   perm.SessionID,
			ToolCallID:  perm.ToolCallID,
			ToolName:    perm.ToolName,
			Description: perm.Description,
			Action:      perm.Action,
			Path:        perm.Path,
			Params:      perm.Params,
		},
		Action: proto.PermissionDeny,
	})
	return resolved
}

// -- Questions --

func (w *ClientWorkspace) QuestionAnswer(ans question.Answer) bool {
	resolved, _ := w.client.AnswerQuestion(context.Background(), w.workspaceID(), proto.QuestionAnswer{
		ID:         ans.ID,
		SessionID:  ans.SessionID,
		ToolCallID: ans.ToolCallID,
		Selected:   ans.Selected,
		Cancelled:  ans.Cancelled,
	})
	return resolved
}

func (w *ClientWorkspace) PermissionSkipRequests() bool {
	skip, err := w.client.GetPermissionsSkipRequests(context.Background(), w.workspaceID())
	if err != nil {
		return false
	}
	return skip
}

func (w *ClientWorkspace) PermissionSetSkipRequests(skip bool) {
	_ = w.client.SetPermissionsSkipRequests(context.Background(), w.workspaceID(), skip)
}

func (w *ClientWorkspace) PermissionSysadminMode() bool {
	enabled, err := w.client.GetPermissionsSysadminMode(context.Background(), w.workspaceID())
	if err != nil {
		return false
	}
	return enabled
}

func (w *ClientWorkspace) PermissionSetSysadminMode(enabled bool) {
	_ = w.client.SetPermissionsSysadminMode(context.Background(), w.workspaceID(), enabled)
}

// -- FileTracker --

func (w *ClientWorkspace) FileTrackerRecordRead(ctx context.Context, sessionID, path string) {
	_ = w.client.FileTrackerRecordRead(ctx, w.workspaceID(), sessionID, path)
}

func (w *ClientWorkspace) FileTrackerLastReadTime(ctx context.Context, sessionID, path string) time.Time {
	t, err := w.client.FileTrackerLastReadTime(ctx, w.workspaceID(), sessionID, path)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (w *ClientWorkspace) FileTrackerListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return w.client.FileTrackerListReadFiles(ctx, w.workspaceID(), sessionID)
}

// -- History --

func (w *ClientWorkspace) ListSessionHistory(ctx context.Context, sessionID string) ([]history.File, error) {
	files, err := w.client.ListSessionHistoryFiles(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return nil, err
	}
	return protoToFiles(files), nil
}

// -- LSP --

func (w *ClientWorkspace) LSPStart(ctx context.Context, path string) {
	_ = w.client.LSPStart(ctx, w.workspaceID(), path)
}

func (w *ClientWorkspace) LSPStopAll(ctx context.Context) {
	_ = w.client.LSPStopAll(ctx, w.workspaceID())
}

func (w *ClientWorkspace) LSPGetStates() map[string]LSPClientInfo {
	states, err := w.client.GetLSPs(context.Background(), w.workspaceID())
	if err != nil {
		return nil
	}
	result := make(map[string]LSPClientInfo, len(states))
	for k, v := range states {
		result[k] = LSPClientInfo{
			Name:            v.Name,
			State:           v.State,
			Error:           v.Error,
			DiagnosticCount: v.DiagnosticCount,
			ConnectedAt:     v.ConnectedAt,
		}
	}
	return result
}

func (w *ClientWorkspace) LSPGetDiagnosticCounts(name string) lsp.DiagnosticCounts {
	diags, err := w.client.GetLSPDiagnostics(context.Background(), w.workspaceID(), name)
	if err != nil {
		return lsp.DiagnosticCounts{}
	}
	var counts lsp.DiagnosticCounts
	for _, fileDiags := range diags {
		for _, d := range fileDiags {
			switch d.Severity {
			case protocol.SeverityError:
				counts.Error++
			case protocol.SeverityWarning:
				counts.Warning++
			case protocol.SeverityInformation:
				counts.Information++
			case protocol.SeverityHint:
				counts.Hint++
			}
		}
	}
	return counts
}

func (w *ClientWorkspace) SkillsGetStates() []*skills.SkillState {
	states, err := w.client.SkillsGetStates(context.Background(), w.workspaceID())
	if err != nil {
		return nil
	}
	result := make([]*skills.SkillState, len(states))
	for i, s := range states {
		result[i] = &skills.SkillState{
			Name:  s.Name,
			Path:  s.Path,
			State: skills.DiscoveryState(s.State),
			Err:   s.Error,
		}
	}
	return result
}

// -- Config (read-only) --

func (w *ClientWorkspace) Config() *config.Config {
	return w.cached().Config
}

func (w *ClientWorkspace) WorkingDir() string {
	// If there's an active session with an active worktree, use the worktree path.
	w.mu.RLock()
	sessionID := w.activeSessionID
	cached := w.cachedWorktree
	cachedValid := w.cachedWorktreeValid
	cachedAt := w.cachedWorktreeTime
	w.mu.RUnlock()

	if sessionID == "" {
		return w.EffectiveWorkingDir()
	}

	// Return cached result if still fresh (including cached "no worktree").
	if cachedValid && time.Since(cachedAt) < worktreeCacheTTL {
		if cached != nil {
			return cached.Path
		}
		return w.EffectiveWorkingDir()
	}

	wt, err := w.client.GetActiveWorktree(context.Background(), w.workspaceID(), sessionID)

	w.mu.Lock()
	w.cachedWorktree = wt
	w.cachedWorktreeValid = true
	w.cachedWorktreeTime = time.Now()
	w.mu.Unlock()

	if err != nil || wt == nil {
		return w.EffectiveWorkingDir()
	}
	return wt.Path
}

// BaseDir returns the project base directory (not worktree-aware).
func (w *ClientWorkspace) BaseDir() string {
	return w.cached().Path
}

// EffectiveWorkingDir returns the cwd the user launched Crush from. For
// user-created linked worktrees this differs from BaseDir() (the project
// root hosting .crush/). Falls back to BaseDir() when not set.
func (w *ClientWorkspace) EffectiveWorkingDir() string {
	if wd := w.cached().WorkingDir; wd != "" {
		return wd
	}
	return w.cached().Path
}

// GitBranch returns the current git branch name, or empty if not in a git repo.
// Fetches live from git using WorkingDir (which is worktree-aware).
// invalidateWorktreeCache clears the cached active worktree so the next
// WorkingDir() call fetches fresh data from the server.
func (w *ClientWorkspace) invalidateWorktreeCache() {
	w.mu.Lock()
	w.cachedWorktree = nil
	w.cachedWorktreeValid = false
	w.cachedWorktreeTime = time.Time{}
	w.mu.Unlock()
}

func (w *ClientWorkspace) GitBranch() string {
	return w.GitBranchForDir(w.WorkingDir())
}

// GitBranchForDir returns the current git branch for the given directory,
// or empty when dir is blank or not inside a git repository. Display code
// uses this to show the branch of an attached session's own working
// directory, which may differ from this client's launch cwd.
func (w *ClientWorkspace) GitBranchForDir(dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.CommandContext(context.Background(), "git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (w *ClientWorkspace) Resolver() config.VariableResolver {
	return config.IdentityResolver()
}

// SetActiveSessionID sets the current session ID for worktree-aware working directory.
func (w *ClientWorkspace) SetActiveSessionID(sessionID string) {
	w.mu.Lock()
	w.activeSessionID = sessionID
	w.cachedWorktree = nil
	w.cachedWorktreeValid = false
	w.cachedWorktreeTime = time.Time{}
	w.mu.Unlock()
}

// ActiveSessionID returns the current session ID.
func (w *ClientWorkspace) ActiveSessionID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.activeSessionID
}

// -- Config mutations --

func (w *ClientWorkspace) UpdatePreferredModel(scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error {
	err := w.client.UpdatePreferredModel(context.Background(), w.workspaceID(), scope, modelType, model)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

func (w *ClientWorkspace) SetCompactMode(scope config.Scope, enabled bool) error {
	err := w.client.SetCompactMode(context.Background(), w.workspaceID(), scope, enabled)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

func (w *ClientWorkspace) SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error {
	err := w.client.SetProviderAPIKey(context.Background(), w.workspaceID(), scope, providerID, apiKey)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

func (w *ClientWorkspace) SetConfigField(scope config.Scope, key string, value any) error {
	err := w.client.SetConfigField(context.Background(), w.workspaceID(), scope, key, value)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

func (w *ClientWorkspace) RemoveConfigField(scope config.Scope, key string) error {
	err := w.client.RemoveConfigField(context.Background(), w.workspaceID(), scope, key)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

func (w *ClientWorkspace) ImportCopilot() (*oauth.Token, bool) {
	token, ok, err := w.client.ImportCopilot(context.Background(), w.workspaceID())
	if err != nil {
		return nil, false
	}
	if ok {
		w.refreshWorkspace()
	}
	return token, ok
}

func (w *ClientWorkspace) RefreshOAuthToken(ctx context.Context, scope config.Scope, providerID string) error {
	err := w.client.RefreshOAuthToken(ctx, w.workspaceID(), scope, providerID)
	if err == nil {
		w.refreshWorkspace()
	}
	return err
}

// -- Project lifecycle --

func (w *ClientWorkspace) ProjectNeedsInitialization() (bool, error) {
	return w.client.ProjectNeedsInitialization(context.Background(), w.workspaceID())
}

func (w *ClientWorkspace) MarkProjectInitialized() error {
	return w.client.MarkProjectInitialized(context.Background(), w.workspaceID())
}

func (w *ClientWorkspace) InitializePrompt() (string, error) {
	return w.client.GetInitializePrompt(context.Background(), w.workspaceID())
}

// -- MCP operations --

func (w *ClientWorkspace) MCPGetStates() map[string]mcp.ClientInfo {
	states, err := w.client.MCPGetStates(context.Background(), w.workspaceID())
	if err != nil {
		return nil
	}
	result := make(map[string]mcp.ClientInfo, len(states))
	for k, v := range states {
		result[k] = mcp.ClientInfo{
			Name:  v.Name,
			State: mcp.State(v.State),
			Error: v.Error,
			Counts: mcp.Counts{
				Tools:     v.ToolCount,
				Prompts:   v.PromptCount,
				Resources: v.ResourceCount,
			},
			ConnectedAt: v.ConnectedAt,
		}
	}
	return result
}

func (w *ClientWorkspace) MCPRefreshPrompts(ctx context.Context, name string) {
	_ = w.client.MCPRefreshPrompts(ctx, w.workspaceID(), name)
}

func (w *ClientWorkspace) MCPRefreshResources(ctx context.Context, name string) {
	_ = w.client.MCPRefreshResources(ctx, w.workspaceID(), name)
}

func (w *ClientWorkspace) RefreshMCPTools(ctx context.Context, name string) {
	_ = w.client.RefreshMCPTools(ctx, w.workspaceID(), name)
}

func (w *ClientWorkspace) MCPAuthenticate(ctx context.Context, name string) error {
	return w.client.MCPAuthenticate(ctx, w.workspaceID(), name)
}

func (w *ClientWorkspace) MCPPendingAuth() []mcp.PendingAuthServer {
	pending, err := w.client.MCPPendingAuth(context.Background(), w.workspaceID())
	if err != nil {
		return nil
	}
	result := make([]mcp.PendingAuthServer, 0, len(pending))
	for _, p := range pending {
		result = append(result, mcp.PendingAuthServer{Name: p.Name, URL: p.URL})
	}
	return result
}

func (w *ClientWorkspace) MCPAuthURL(name string) string {
	url, err := w.client.MCPAuthURL(context.Background(), w.workspaceID(), name)
	if err != nil {
		return ""
	}
	return url
}

func (w *ClientWorkspace) ReadMCPResource(ctx context.Context, name, uri string) ([]MCPResourceContents, error) {
	contents, err := w.client.ReadMCPResource(ctx, w.workspaceID(), name, uri)
	if err != nil {
		return nil, err
	}
	result := make([]MCPResourceContents, len(contents))
	for i, c := range contents {
		result[i] = MCPResourceContents{
			URI:      c.URI,
			MIMEType: c.MIMEType,
			Text:     c.Text,
			Blob:     c.Blob,
		}
	}
	return result, nil
}

func (w *ClientWorkspace) GetMCPPrompt(clientID, promptID string, args map[string]string) (string, error) {
	return w.client.GetMCPPrompt(context.Background(), w.workspaceID(), clientID, promptID, args)
}

func (w *ClientWorkspace) EnableDockerMCP(ctx context.Context) error {
	return w.client.EnableDockerMCP(ctx, w.workspaceID())
}

func (w *ClientWorkspace) DisableDockerMCP() error {
	return w.client.DisableDockerMCP(context.Background(), w.workspaceID())
}

func (w *ClientWorkspace) ReloadConfig(ctx context.Context) error {
	return w.client.ReloadConfig(ctx, w.workspaceID())
}

// -- Snapshots --

func (w *ClientWorkspace) SnapshotsEnabled() bool {
	enabled, err := w.client.SnapshotsEnabled(context.Background(), w.workspaceID())
	if err != nil {
		return false
	}
	return enabled
}

func (w *ClientWorkspace) ListSnapshots(ctx context.Context, sessionID string) ([]*checkpoint.Snapshot, error) {
	return w.client.ListSnapshots(ctx, w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) GetSnapshot(ctx context.Context, snapshotID string) (*checkpoint.Snapshot, error) {
	return w.client.GetSnapshot(ctx, w.workspaceID(), snapshotID)
}

func (w *ClientWorkspace) GetSnapshotByMessage(ctx context.Context, messageID string) (*checkpoint.Snapshot, error) {
	return w.client.GetSnapshotByMessage(ctx, w.workspaceID(), messageID)
}

func (w *ClientWorkspace) RestoreSnapshot(ctx context.Context, snapshotID string) error {
	return w.client.RestoreSnapshot(ctx, w.workspaceID(), snapshotID)
}

func (w *ClientWorkspace) DiffFromCurrentSnapshot(ctx context.Context, snapshotID string) (string, error) {
	return w.client.DiffFromCurrentSnapshot(ctx, w.workspaceID(), snapshotID)
}

// -- Worktrees --

func (w *ClientWorkspace) WorktreesEnabled() bool {
	enabled, err := w.client.WorktreesEnabled(context.Background(), w.workspaceID())
	if err != nil {
		return false
	}
	return enabled
}

func (w *ClientWorkspace) ListWorktrees(ctx context.Context, sessionID string) ([]*worktree.Worktree, error) {
	return w.client.ListWorktrees(ctx, w.workspaceID(), sessionID)
}

func (w *ClientWorkspace) ListAllWorktrees(ctx context.Context) ([]*worktree.Worktree, error) {
	return w.client.ListAllWorktrees(ctx, w.workspaceID())
}

func (w *ClientWorkspace) GetWorktree(ctx context.Context, worktreeID string) (*worktree.Worktree, error) {
	return w.client.GetWorktree(ctx, w.workspaceID(), worktreeID)
}

func (w *ClientWorkspace) GetActiveWorktree(ctx context.Context, sessionID string) (*worktree.Worktree, error) {
	w.mu.RLock()
	cached := w.cachedWorktree
	cachedValid := w.cachedWorktreeValid
	cachedAt := w.cachedWorktreeTime
	currentSession := w.activeSessionID
	w.mu.RUnlock()

	// If asking about a different session than the active one, bypass cache.
	if sessionID != currentSession {
		return w.client.GetActiveWorktree(ctx, w.workspaceID(), sessionID)
	}

	// Return cached result if still fresh.
	if cachedValid && time.Since(cachedAt) < worktreeCacheTTL {
		if cached == nil {
			return nil, fmt.Errorf("no active worktree")
		}
		return cached, nil
	}

	wt, err := w.client.GetActiveWorktree(ctx, w.workspaceID(), sessionID)

	w.mu.Lock()
	w.cachedWorktree = wt
	w.cachedWorktreeValid = true
	w.cachedWorktreeTime = time.Now()
	w.mu.Unlock()

	return wt, err
}

func (w *ClientWorkspace) CreateWorktree(ctx context.Context, sessionID, name, fromSnapshotID string) (*worktree.Worktree, error) {
	wt, err := w.client.CreateWorktree(ctx, w.workspaceID(), sessionID, name, fromSnapshotID)
	if err == nil {
		w.invalidateWorktreeCache()
	}
	return wt, err
}

func (w *ClientWorkspace) SwitchWorktree(ctx context.Context, sessionID, worktreeID string) error {
	err := w.client.SwitchWorktree(ctx, w.workspaceID(), sessionID, worktreeID)
	if err == nil {
		w.invalidateWorktreeCache()
	}
	return err
}

func (w *ClientWorkspace) DeleteWorktree(ctx context.Context, worktreeID string) error {
	err := w.client.DeleteWorktree(ctx, w.workspaceID(), worktreeID)
	if err == nil {
		w.invalidateWorktreeCache()
	}
	return err
}

func (w *ClientWorkspace) MergeWorktree(ctx context.Context, worktreeID, targetBranch string, rebase bool) error {
	return w.client.MergeWorktree(ctx, w.workspaceID(), worktreeID, targetBranch, rebase)
}

func (w *ClientWorkspace) ListGitBranches(ctx context.Context) ([]string, error) {
	return w.client.ListGitBranches(ctx, w.workspaceID())
}

// -- Forks --

func (w *ClientWorkspace) ForkConversation(ctx context.Context, params fork.ForkParams) (*fork.ForkResult, error) {
	return w.client.ForkConversation(ctx, w.workspaceID(), params)
}

// -- Garbage Collection --

func (w *ClientWorkspace) SnapshotGC(ctx context.Context) (int64, error) {
	return w.client.SnapshotGC(ctx, w.workspaceID())
}

func (w *ClientWorkspace) SnapshotStats(ctx context.Context) (*checkpoint.Stats, error) {
	return w.client.SnapshotStats(ctx, w.workspaceID())
}

// -- Lifecycle --

// reconnectBaseDelay and reconnectMaxDelay bound the exponential
// backoff used when the event stream drops unexpectedly (as opposed
// to a deliberate SwitchWorkspace-triggered reconnect, which happens
// immediately).
const (
	reconnectBaseDelay = 500 * time.Millisecond
	reconnectMaxDelay  = 15 * time.Second
)

// nextBackoff doubles the delay, capped at reconnectMaxDelay.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectMaxDelay {
		return reconnectMaxDelay
	}
	return d
}

// initialBackoff returns the starting backoff delay, honoring
// reconnectDelayOverride when set (tests).
func (w *ClientWorkspace) initialBackoff() time.Duration {
	if w.reconnectDelayOverride > 0 {
		return w.reconnectDelayOverride
	}
	return reconnectBaseDelay
}

func (w *ClientWorkspace) Subscribe(program *tea.Program) {
	defer log.RecoverPanic("ClientWorkspace.Subscribe", func() {
		slog.Info("TUI subscription panic: attempting graceful shutdown")
		program.Quit()
	})

	w.subMu.Lock()
	w.program = program
	w.subMu.Unlock()

	// Observe the server's global, cross-workspace attention stream in
	// parallel with the focused workspace's event stream. This is how a
	// background workspace's permission/question prompt (and busy/idle)
	// reaches this client even when it is not the attached workspace —
	// without it, a prompt from a workspace the user switched away from
	// would reach no stream on this client and block forever.
	go w.globalSubscribeLoop(program.Send)

	w.subscribeLoop(program.Send)
}

// globalSubscribeLoop consumes the server's observe-only global
// attention stream and forwards each attention event to the TUI. It
// reconnects with a fixed delay on drop (the stream is not tied to a
// workspace, so there is no switch/backoff interplay); it exits when the
// workspace is shut down. Errors are logged and retried — a missing
// global stream degrades gracefully to the previous per-workspace-only
// behavior rather than crashing the client.
func (w *ClientWorkspace) globalSubscribeLoop(send func(tea.Msg)) {
	for {
		select {
		case <-w.stopped:
			return
		default:
		}

		ctx, cancel := context.WithCancel(context.Background())
		w.subMu.Lock()
		w.globalSubCancel = cancel
		w.subMu.Unlock()

		evc, err := w.subscribeGlobalEventsFn(ctx)
		if err != nil {
			cancel()
			select {
			case <-w.stopped:
				return
			case <-time.After(w.initialBackoff()):
				continue
			}
		}
		for ev := range evc {
			if ae, ok := ev.(pubsub.Event[proto.AttentionEvent]); ok && send != nil {
				send(ae)
			}
		}
		cancel()

		select {
		case <-w.stopped:
			return
		case <-time.After(w.initialBackoff()):
		}
	}
}

// subscribeLoop is the reconnect loop, split out from Subscribe so
// tests can drive it with a plain send func instead of a real
// *tea.Program.
//
// Each iteration subscribes to the current workspace and consumes
// events until the stream closes. A close caused by a workspace
// switch (SwitchWorkspace cancels the stream and sets
// switchRequested) reconnects to the now-current workspace
// immediately; a close for any other reason (dropped connection,
// server restart, or the initial connect failing because the
// server/agent isn't up yet) retries with exponential backoff and
// reports ConnectionStateReconnecting (if it was ever connected
// before) or keeps reporting ConnectionStateConnecting (if not) until
// it succeeds. The loop only returns once the workspace is shut down.
func (w *ClientWorkspace) subscribeLoop(send func(tea.Msg)) {
	everConnected := false
	backoff := w.initialBackoff()
	for {
		ctx, cancel := context.WithCancel(context.Background())
		w.subMu.Lock()
		select {
		case <-w.stopped:
			// stopSubscribeLoop may have already fired and cancelled a
			// stale (or nil) subCancel before we got here; checking
			// stopped in the same critical section we store subCancel
			// in guarantees we never open a connection nothing will
			// ever be able to cancel.
			w.subMu.Unlock()
			cancel()
			return
		default:
		}
		w.subCancel = cancel
		w.switchRequested = false
		w.subMu.Unlock()

		// Drain any pending switch pulse so it only ever aborts the
		// backoff sleep that follows the switch, not a later one.
		select {
		case <-w.reconnectNow:
		default:
		}

		wsID := w.workspaceID()
		evc, err := w.subscribeEventsFn(ctx, wsID)
		if err != nil {
			cancel()
			slog.Error("Failed to subscribe to events", "error", err)
			if everConnected && !w.isStopped() {
				// The server may have been replaced (graceful update):
				// the new one does not know our workspace id. Re-attach
				// by path; if that works, retry the subscribe at once
				// against the new id instead of backing off on a 404.
				// Bounded well below the server's data-dir lock wait so
				// a request the server is still honouring is not
				// abandoned midway by a departed client.
				reCtx, reCancel := context.WithTimeout(context.Background(), 20*time.Second)
				reErr := w.reattachByPath(reCtx)
				reCancel()
				if reErr == nil && w.workspaceID() != wsID {
					continue
				}
			}
			action, changed := w.prepReconnect(w.stateAfterDrop(everConnected))
			switch action {
			case reconnectStop:
				return
			case reconnectSwitch:
				// A switch landed during the connect attempt: reconnect
				// to the new workspace with a fresh backoff, not the
				// grown delay from this failed connect.
				backoff = w.initialBackoff()
				continue
			}
			if changed && send != nil {
				send(pubsub.Event[ConnectionEvent]{
					Type:    pubsub.UpdatedEvent,
					Payload: ConnectionEvent{State: w.stateAfterDrop(everConnected), Err: err},
				})
			}
			switch w.waitBackoff(backoff) {
			case backoffStopped:
				return
			case backoffSwitched:
				// A switch aborted the sleep: reconnect immediately to
				// the new workspace with a fresh backoff, not the grown
				// delay carried over from this failed connect.
				backoff = w.initialBackoff()
			default:
				backoff = nextBackoff(backoff)
			}
			continue
		}

		w.setConnState(send, ConnectionStateConnected, nil)
		if everConnected {
			// Off the loop goroutine so consumeEvents starts draining
			// the fresh stream immediately; the reconnect chores make
			// their own HTTP calls and must not back the SSE buffer up.
			go w.afterReconnect(send)
		}
		everConnected = true
		backoff = w.initialBackoff()

		// Send synthetic state-changed events to trigger UI refresh now
		// that subscription is established. This ensures the UI gets fresh
		// MCP/LSP states even if the actual state-change events were
		// published before this subscription connected.
		send(pubsub.Event[mcp.Event]{
			Type:    pubsub.UpdatedEvent,
			Payload: mcp.Event{Type: mcp.EventStateChanged},
		})
		send(pubsub.Event[LSPEvent]{
			Type:    pubsub.UpdatedEvent,
			Payload: LSPEvent{Type: LSPEventStateChanged},
		})

		w.consumeEvents(evc, send)
		cancel()

		// The stream ended. Decide, atomically, whether this was a
		// deliberate stop (return), a workspace switch (reconnect now),
		// or a genuine drop (report Reconnecting and back off). Holding
		// subMu across the stop/switch check and the connState update
		// closes the window where Shutdown/SwitchWorkspace could
		// otherwise race a spurious "Reconnecting" flash.
		dropState := w.stateAfterDrop(true)
		action, changed := w.prepReconnect(dropState)
		switch action {
		case reconnectStop:
			return
		case reconnectSwitch:
			// A switch caused the stream to end: reconnect to the new
			// workspace with a fresh backoff. (backoff is already the
			// base delay here after a successful connect, but reset for
			// symmetry with the connect-error site so the invariant
			// holds on every path.)
			backoff = w.initialBackoff()
			continue
		}
		if changed && send != nil {
			send(pubsub.Event[ConnectionEvent]{
				Type:    pubsub.UpdatedEvent,
				Payload: ConnectionEvent{State: dropState},
			})
		}
		switch w.waitBackoff(backoff) {
		case backoffStopped:
			return
		case backoffSwitched:
			// A switch aborted the sleep: reconnect immediately to the
			// new workspace with a fresh backoff, not the grown delay
			// carried over from this drop.
			backoff = w.initialBackoff()
		default:
			backoff = nextBackoff(backoff)
		}
	}
}

// isStopped reports whether Shutdown has been called.
func (w *ClientWorkspace) isStopped() bool {
	select {
	case <-w.stopped:
		return true
	default:
		return false
	}
}

// reconnectAction is the decision prepReconnect returns for how the
// subscribe loop should proceed after a failed/dropped stream.
type reconnectAction int

const (
	// reconnectBackoff: report the reconnect state (if changed) and
	// wait out the backoff before retrying.
	reconnectBackoff reconnectAction = iota
	// reconnectStop: a deliberate shutdown is in progress; exit.
	reconnectStop
	// reconnectSwitch: a workspace switch was requested; reconnect
	// immediately without reporting Reconnecting or backing off.
	reconnectSwitch
)

// prepReconnect decides, under subMu, how the loop should proceed
// after a stream failure/drop and — for the backoff case — updates the
// cached connState to the given state, reporting whether it changed so
// the caller can push an event. Doing the stop/switch check and the
// connState mutation in one critical section prevents a deliberate
// stop or switch from racing a spurious "Reconnecting" flash.
func (w *ClientWorkspace) prepReconnect(state ConnectionState) (action reconnectAction, changed bool) {
	w.subMu.Lock()
	defer w.subMu.Unlock()
	select {
	case <-w.stopped:
		return reconnectStop, false
	default:
	}
	if w.switchRequested {
		return reconnectSwitch, false
	}
	changed = w.connState != state
	w.connState = state
	return reconnectBackoff, changed
}

// stateAfterDrop returns the ConnectionState to report when a
// connection attempt fails, depending on whether the client had ever
// successfully connected before. A drop that follows the server
// refusing a prompt as draining is reported as Updating so the UI can
// say why.
func (w *ClientWorkspace) stateAfterDrop(everConnected bool) ConnectionState {
	if !everConnected {
		return ConnectionStateConnecting
	}
	w.heldMu.Lock()
	updating := w.updating
	w.heldMu.Unlock()
	if updating {
		return ConnectionStateUpdating
	}
	return ConnectionStateReconnecting
}

// afterReconnect runs once a dropped event stream is back up: it
// re-announces the session this client is viewing (presence is
// per-server and the replacement server has never heard from us; it
// also drives RepublishPending, so a blocked permission prompt would
// stay invisible without it) and redelivers any prompts held while the
// server was updating. Runs on its own goroutine with its own deadlines.
func (w *ClientWorkspace) afterReconnect(send func(tea.Msg)) {
	// Whatever busy/queue status was cached belongs to the old stream
	// (possibly the old server); the UI must re-read it.
	w.agentState.invalidate()
	if sid := w.ActiveSessionID(); sid != "" && w.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := w.client.SetCurrentSession(ctx, w.workspaceID(), sid)
		cancel()
		if err != nil {
			slog.Warn("Failed to re-report current session after reconnect; a pending prompt for it may not resurface until the session is reopened",
				"session_id", sid, "error", err)
		}
	}
	if w.HeldPrompts() == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sent, failed, kept := w.flushHeldPrompts(ctx)
	if send != nil && (sent > 0 || len(failed) > 0 || kept > 0) {
		ev := HeldPromptsEvent{Sent: sent, Failed: failed, KeptElsewhere: kept}
		if len(failed) > 0 {
			ev.Err = failed[0].Err
		}
		send(pubsub.Event[HeldPromptsEvent]{Type: pubsub.UpdatedEvent, Payload: ev})
	}
}

// setConnState updates the cached connection state and, if a send
// func is attached, pushes a ConnectionEvent so the UI can react
// immediately rather than waiting for the next poll.
func (w *ClientWorkspace) setConnState(send func(tea.Msg), state ConnectionState, err error) {
	w.subMu.Lock()
	changed := w.connState != state
	w.connState = state
	w.subMu.Unlock()

	if !changed || send == nil {
		return
	}
	send(pubsub.Event[ConnectionEvent]{
		Type:    pubsub.UpdatedEvent,
		Payload: ConnectionEvent{State: state, Err: err},
	})
}

// ConnectionState reports the current state of the event-stream
// connection to the server.
func (w *ClientWorkspace) ConnectionState() ConnectionState {
	w.subMu.Lock()
	defer w.subMu.Unlock()
	return w.connState
}

// backoffResult reports why waitBackoff returned.
type backoffResult int

const (
	// backoffElapsed: the full delay elapsed; retry with a further
	// advanced backoff.
	backoffElapsed backoffResult = iota
	// backoffSwitched: a reconnect was requested (e.g. a workspace
	// switch) and short-circuited the sleep; the caller should treat
	// this as a fresh reconnect and reset the backoff to the base
	// delay rather than advancing it.
	backoffSwitched
	// backoffStopped: the workspace was shut down during the sleep;
	// the caller should exit.
	backoffStopped
)

// waitBackoff sleeps for d, reporting why it woke: backoffElapsed when
// the delay elapses, backoffSwitched when a reconnect was requested
// via reconnectNow (e.g. a workspace switch) and short-circuited the
// sleep, or backoffStopped — without sleeping the full duration — if
// the workspace is shut down in the meantime. Distinguishing a switch
// wake from a timer wake lets the caller restart the backoff from the
// base delay for a switch (a fresh reconnect) instead of carrying over
// the grown delay from the previous drop.
func (w *ClientWorkspace) waitBackoff(d time.Duration) backoffResult {
	if w.backoffObserver != nil {
		w.backoffObserver(d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return backoffElapsed
	case <-w.reconnectNow:
		return backoffSwitched
	case <-w.stopped:
		return backoffStopped
	}
}

// SwitchWorkspace re-targets this client at the workspace rooted at path,
// attaching it on the server if it is not already (dedup is first-wins by
// resolved path). It updates the cached workspace snapshot and reconnects
// the event subscription to the new workspace so live events flow from it.
// The previously attached workspace is left running on the server (its
// runs continue); only this client's view moves.
func (w *ClientWorkspace) SwitchWorkspace(ctx context.Context, path string) error {
	// Replay this client's flags (data dir, yolo, debug, env) for the
	// new root so the switched-to workspace — and any later re-attach
	// to it after a server swap — is configured the way this client was
	// launched, not with server defaults.
	w.mu.RLock()
	req := w.creationArgs
	w.mu.RUnlock()
	req.Path = path
	created, err := w.client.CreateWorkspace(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to attach workspace %q: %w", path, err)
	}
	if created.Config != nil {
		created.Config.SetupAgents()
	}

	w.mu.Lock()
	sameWorkspace := w.ws.ID == created.ID
	w.ws = *created
	w.activeSessionID = ""
	w.cachedWorktree = nil
	w.cachedWorktreeValid = false
	// A later re-attach (server swap) must follow the switch.
	w.creationArgs = req
	w.mu.Unlock()
	w.agentState.invalidate()

	if sameWorkspace {
		// Already viewing this workspace; nothing to reconnect.
		return nil
	}

	// Signal the subscribe loop to reconnect to the new workspace.
	w.requestSwitch()
	return nil
}

// requestSwitch tells the subscribe loop that the current stream is
// closing because of a deliberate workspace switch (not a drop), and
// nudges it to reconnect immediately. It marks switchRequested,
// cancels the live stream so consumeEvents returns, and pulses
// reconnectNow so that if the loop is instead sleeping out a backoff
// (from an earlier dropped connection) that sleep aborts at once
// rather than waiting out a stale timer (up to reconnectMaxDelay).
func (w *ClientWorkspace) requestSwitch() {
	w.subMu.Lock()
	w.switchRequested = true
	cancel := w.subCancel
	w.subMu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case w.reconnectNow <- struct{}{}:
	default:
	}
}

// ListWorkspaceOverviews returns all known workspaces (attached and
// registry-known) with their sessions for the cross-workspace picker.
func (w *ClientWorkspace) ListWorkspaceOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	return w.client.ListWorkspaceOverviews(ctx)
}

// consumeEvents drives the workspace event loop. It is split out from
// Subscribe so tests can drive it without a real *tea.Program.
// ConfigChanged events trigger a workspace refresh; all other events
// are translated into domain types and forwarded to send.
func (w *ClientWorkspace) consumeEvents(evc <-chan any, send func(tea.Msg)) {
	for ev := range evc {
		if _, ok := ev.(pubsub.Event[proto.ConfigChanged]); ok {
			w.refreshWorkspace()
			continue
		}
		// Drop cached agent status before the UI sees the event, so the
		// Update that handles it re-reads fresh state.
		if invalidatesAgentState(ev) {
			w.agentState.invalidate()
		}
		translated := translateEvent(ev)
		if translated != nil && send != nil {
			send(translated)
		}
	}
}

func (w *ClientWorkspace) Shutdown() {
	w.stopSubscribeLoop()
	_ = w.client.DeleteWorkspace(context.Background(), w.workspaceID())
}

// stopSubscribeLoop signals the reconnect loop to stop: it closes the
// stopped channel (so any pending backoff sleep returns immediately)
// and cancels the current event-stream context (so a blocked
// consumeEvents read unblocks too).
func (w *ClientWorkspace) stopSubscribeLoop() {
	w.subMu.Lock()
	w.stopOnce.Do(func() { close(w.stopped) })
	cancel := w.subCancel
	globalCancel := w.globalSubCancel
	w.subMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if globalCancel != nil {
		globalCancel()
	}
}

// translateEvent converts proto-typed SSE events into the domain types
// that the TUI's Update() method expects.
func translateEvent(ev any) tea.Msg {
	switch e := ev.(type) {
	case pubsub.Event[proto.LSPEvent]:
		return pubsub.Event[LSPEvent]{
			Type: e.Type,
			Payload: LSPEvent{
				Type:            LSPEventType(e.Payload.Type),
				Name:            e.Payload.Name,
				State:           e.Payload.State,
				Error:           e.Payload.Error,
				DiagnosticCount: e.Payload.DiagnosticCount,
			},
		}
	case pubsub.Event[proto.MCPEvent]:
		return pubsub.Event[mcp.Event]{
			Type: e.Type,
			Payload: mcp.Event{
				Type:  protoToMCPEventType(e.Payload.Type),
				Name:  e.Payload.Name,
				State: mcp.State(e.Payload.State),
				Error: e.Payload.Error,
				Counts: mcp.Counts{
					Tools:     e.Payload.ToolCount,
					Prompts:   e.Payload.PromptCount,
					Resources: e.Payload.ResourceCount,
				},
			},
		}
	case pubsub.Event[proto.SkillEvent]:
		return pubsub.Event[skills.Event]{
			Type:    e.Type,
			Payload: protoToSkillEvent(e.Payload),
		}
	case pubsub.Event[proto.PermissionRequest]:
		return pubsub.Event[permission.PermissionRequest]{
			Type: e.Type,
			Payload: permission.PermissionRequest{
				ID:          e.Payload.ID,
				SessionID:   e.Payload.SessionID,
				ToolCallID:  e.Payload.ToolCallID,
				ToolName:    e.Payload.ToolName,
				Description: e.Payload.Description,
				Action:      e.Payload.Action,
				Path:        e.Payload.Path,
				Params:      e.Payload.Params,
			},
		}
	case pubsub.Event[proto.PermissionNotification]:
		return pubsub.Event[permission.PermissionNotification]{
			Type: e.Type,
			Payload: permission.PermissionNotification{
				SessionID:  e.Payload.SessionID,
				ToolCallID: e.Payload.ToolCallID,
				Granted:    e.Payload.Granted,
				Denied:     e.Payload.Denied,
			},
		}
	case pubsub.Event[proto.QuestionRequest]:
		return pubsub.Event[question.Request]{
			Type: e.Type,
			Payload: question.Request{
				ID:         e.Payload.ID,
				SessionID:  e.Payload.SessionID,
				ToolCallID: e.Payload.ToolCallID,
				Kind:       question.Kind(e.Payload.Kind),
				Prompt:     e.Payload.Prompt,
				Options:    e.Payload.Options,
			},
		}
	case pubsub.Event[proto.QuestionNotification]:
		return pubsub.Event[question.Notification]{
			Type: e.Type,
			Payload: question.Notification{
				SessionID:  e.Payload.SessionID,
				ToolCallID: e.Payload.ToolCallID,
				Answered:   e.Payload.Answered,
				Cancelled:  e.Payload.Cancelled,
			},
		}
	case pubsub.Event[proto.Message]:
		return pubsub.Event[message.Message]{
			Type:    e.Type,
			Payload: protoToMessage(e.Payload),
		}
	case pubsub.Event[proto.Session]:
		return pubsub.Event[session.Session]{
			Type:    e.Type,
			Payload: protoToSession(e.Payload),
		}
	case pubsub.Event[proto.File]:
		return pubsub.Event[history.File]{
			Type:    e.Type,
			Payload: protoToFile(e.Payload),
		}
	case pubsub.Event[proto.AgentEvent]:
		n := notify.Notification{
			SessionID:    e.Payload.SessionID,
			SessionTitle: e.Payload.SessionTitle,
			RunID:        e.Payload.RunID,
			Type:         notify.Type(e.Payload.Type),
		}
		if e.Payload.Error != nil {
			n.Message = e.Payload.Error.Error()
		}
		return pubsub.Event[notify.Notification]{
			Type:    e.Type,
			Payload: n,
		}
	case pubsub.Event[proto.RunComplete]:
		// Translate the wire-level proto.RunComplete back into the
		// agent's domain notify.RunComplete. Without this case the
		// default branch below warns on every run completion in the
		// server-mode TUI, even though the TUI itself doesn't act
		// on RunComplete — converting silently keeps the workspace
		// event bridge symmetric with the server-side wrapEvent.
		return pubsub.Event[notify.RunComplete]{
			Type: e.Type,
			Payload: notify.RunComplete{
				SessionID: e.Payload.SessionID,
				RunID:     e.Payload.RunID,
				MessageID: e.Payload.MessageID,
				Text:      e.Payload.Text,
				Error:     e.Payload.Error,
				Cancelled: e.Payload.Cancelled,
			},
		}
	case pubsub.Event[proto.ForkProgress]:
		return pubsub.Event[fork.ForkProgress]{
			Type: e.Type,
			Payload: fork.ForkProgress{
				SourceSessionID: e.Payload.SourceSessionID,
				Stage:           e.Payload.Stage,
				Detail:          e.Payload.Detail,
				Percent:         e.Payload.Percent,
				Done:            e.Payload.Done,
			},
		}
	default:
		slog.Warn("Unknown event type in translateEvent", "type", fmt.Sprintf("%T", ev))
		return nil
	}
}

func protoToMCPEventType(t proto.MCPEventType) mcp.EventType {
	switch t {
	case proto.MCPEventStateChanged:
		return mcp.EventStateChanged
	case proto.MCPEventToolsListChanged:
		return mcp.EventToolsListChanged
	case proto.MCPEventPromptsListChanged:
		return mcp.EventPromptsListChanged
	case proto.MCPEventResourcesListChanged:
		return mcp.EventResourcesListChanged
	default:
		return mcp.EventStateChanged
	}
}

// protoToSession converts a wire-level proto.Session into the domain
// session.Session. Fields that exist only on the wire (computed-on-read
// signals like IsBusy, and any future presence counters) are
// intentionally dropped here: session.Session models persisted state,
// not transient runtime signals. UI features that need those signals
// should either extend session.Session or read them from the proto
// payload directly before this conversion runs.
func protoToSession(s proto.Session) session.Session {
	return session.Session{
		ID:               s.ID,
		ParentSessionID:  s.ParentSessionID,
		Title:            s.Title,
		SummaryMessageID: s.SummaryMessageID,
		MessageCount:     s.MessageCount,
		PromptTokens:     s.PromptTokens,
		CompletionTokens: s.CompletionTokens,
		Cost:             s.Cost,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		Color:            s.Color,
		Animal:           s.Animal,
		ModelRef:         s.ModelRef,
		WorkingDir:       s.WorkingDir,

		SpawnedBySessionID:   s.SpawnedBySessionID,
		SpawnedByWorkspaceID: s.SpawnedByWorkspaceID,
	}
}

func protoToFile(f proto.File) history.File {
	return history.File{
		ID:        f.ID,
		SessionID: f.SessionID,
		Path:      f.Path,
		Content:   f.Content,
		Version:   f.Version,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

func protoToMessage(m proto.Message) message.Message {
	msg := message.Message{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      message.MessageRole(m.Role),
		Model:     m.Model,
		Provider:  m.Provider,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}

	for _, p := range m.Parts {
		switch v := p.(type) {
		case proto.TextContent:
			msg.Parts = append(msg.Parts, message.TextContent{Text: v.Text})
		case proto.SwarmMessage:
			msg.Parts = append(msg.Parts, message.SwarmMessage{
				Text:              v.Text,
				Body:              v.Body,
				SenderSessionID:   v.SenderSessionID,
				SenderColor:       v.SenderColor,
				SenderAnimal:      v.SenderAnimal,
				SenderWorkspaceID: v.SenderWorkspaceID,
				BTW:               v.BTW,
				RequireReply:      v.RequireReply,
			})
		case proto.ReasoningContent:
			msg.Parts = append(msg.Parts, message.ReasoningContent{
				Thinking:   v.Thinking,
				Signature:  v.Signature,
				StartedAt:  v.StartedAt,
				FinishedAt: v.FinishedAt,
			})
		case proto.ToolCall:
			msg.Parts = append(msg.Parts, message.ToolCall{
				ID:       v.ID,
				Name:     v.Name,
				Input:    v.Input,
				Finished: v.Finished,
			})
		case proto.ToolResult:
			msg.Parts = append(msg.Parts, message.ToolResult{
				ToolCallID: v.ToolCallID,
				Name:       v.Name,
				Content:    v.Content,
				Data:       v.Data,
				MIMEType:   v.MIMEType,
				Metadata:   v.Metadata,
				IsError:    v.IsError,
			})
		case proto.Finish:
			msg.Parts = append(msg.Parts, message.Finish{
				Reason:  message.FinishReason(v.Reason),
				Time:    v.Time,
				Message: v.Message,
				Details: v.Details,
			})
		case proto.ImageURLContent:
			msg.Parts = append(msg.Parts, message.ImageURLContent{URL: v.URL, Detail: v.Detail})
		case proto.BinaryContent:
			msg.Parts = append(msg.Parts, message.BinaryContent{Path: v.Path, MIMEType: v.MIMEType, Data: v.Data})
		}
	}

	return msg
}

func protoToMessages(msgs []proto.Message) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		out[i] = protoToMessage(m)
	}
	return out
}

func protoToFiles(files []proto.File) []history.File {
	out := make([]history.File, len(files))
	for i, f := range files {
		out[i] = protoToFile(f)
	}
	return out
}

func sessionToProto(s session.Session) proto.Session {
	return proto.Session{
		ID:               s.ID,
		ParentSessionID:  s.ParentSessionID,
		Title:            s.Title,
		SummaryMessageID: s.SummaryMessageID,
		MessageCount:     s.MessageCount,
		PromptTokens:     s.PromptTokens,
		CompletionTokens: s.CompletionTokens,
		Cost:             s.Cost,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		Color:            s.Color,
		Animal:           s.Animal,
		ModelRef:         s.ModelRef,
		WorkingDir:       s.WorkingDir,

		SpawnedBySessionID:   s.SpawnedBySessionID,
		SpawnedByWorkspaceID: s.SpawnedByWorkspaceID,
	}
}

func protoToSkillEvent(e proto.SkillEvent) skills.Event {
	states := make([]*skills.SkillState, len(e.States))
	for i, s := range e.States {
		states[i] = &skills.SkillState{
			Name:  s.Name,
			Path:  s.Path,
			State: skills.DiscoveryState(s.State),
			Err:   s.Error,
		}
	}
	return skills.Event{States: states}
}

func (w *ClientWorkspace) ListSkills(ctx context.Context) ([]skills.CatalogEntry, error) {
	entries, err := w.client.ListSkills(ctx, w.workspaceID())
	if err != nil {
		return nil, err
	}
	result := make([]skills.CatalogEntry, len(entries))
	for i, entry := range entries {
		result[i] = skills.CatalogEntry{
			ID:          entry.ID,
			Name:        entry.Name,
			Description: entry.Description,
			Label:       entry.Label,
			Source:      skills.SourceType(entry.Source),
		}
	}
	return result, nil
}

func (w *ClientWorkspace) ReadSkill(ctx context.Context, skillID string) ([]byte, skills.SkillReadResult, error) {
	resp, err := w.client.ReadSkill(ctx, w.workspaceID(), skillID)
	if err != nil {
		return nil, skills.SkillReadResult{}, err
	}
	return resp.Content, skills.SkillReadResult{
		Name:        resp.Result.Name,
		Description: resp.Result.Description,
		Source:      skills.SourceType(resp.Result.Source),
		Builtin:     resp.Result.Builtin,
	}, nil
}

func (w *ClientWorkspace) ListMilestones(ctx context.Context, sessionID string) ([]Milestone, error) {
	milestones, err := w.client.ListMilestones(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]Milestone, len(milestones))
	for i, m := range milestones {
		result[i] = Milestone{
			ID:           m.ID,
			SessionID:    m.SessionID,
			TurnNumber:   m.TurnNumber,
			ShortSummary: m.ShortSummary,
			FullSummary:  m.FullSummary,
			CreatedAt:    m.CreatedAt,
		}
	}
	return result, nil
}
