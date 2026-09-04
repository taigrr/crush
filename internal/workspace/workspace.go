// Package workspace defines the Workspace interface used by all
// frontends (TUI, CLI) to interact with a running workspace. Two
// implementations exist: one wrapping a local app.App instance and one
// wrapping the HTTP client SDK.
package workspace

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/catwalk/pkg/catwalk"
	mcptools "github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/fork"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/sessionimport"
	"github.com/taigrr/crush/internal/skills"
	"github.com/taigrr/crush/internal/worktree"
)

// LSPClientInfo holds information about an LSP client's state. This is
// the frontend-facing type; implementations translate from the
// underlying app or proto representation.
type LSPClientInfo struct {
	Name            string
	State           lsp.ServerState
	Error           error
	DiagnosticCount int
	ConnectedAt     time.Time
}

// LSPEventType represents the type of LSP event.
type LSPEventType string

const (
	LSPEventStateChanged       LSPEventType = "state_changed"
	LSPEventDiagnosticsChanged LSPEventType = "diagnostics_changed"
)

// LSPEvent represents an LSP event forwarded to the TUI.
type LSPEvent struct {
	Type            LSPEventType
	Name            string
	State           lsp.ServerState
	Error           error
	DiagnosticCount int
}

// ConnectionState describes a client's connection to the server's live
// event stream. It distinguishes a connection that has never been
// established yet (the agent may still be initializing) from one that
// was established and then dropped and is now being retried.
type ConnectionState int

const (
	// ConnectionStateConnecting means the client has not yet
	// established its first event-stream connection to the server.
	ConnectionStateConnecting ConnectionState = iota
	// ConnectionStateConnected means the event stream is up and
	// events are flowing normally.
	ConnectionStateConnected
	// ConnectionStateReconnecting means the client was previously
	// connected, the stream dropped, and it is now retrying with
	// backoff.
	ConnectionStateReconnecting
	// ConnectionStateUpdating is Reconnecting with a known cause: the
	// server refused a prompt because it is draining for a binary
	// update, so the drop that follows is the old server exiting and
	// the retry will land on its replacement. Prompts sent meanwhile
	// are held client-side and delivered once reconnected.
	ConnectionStateUpdating
)

// String returns a short human-readable label for the state.
func (s ConnectionState) String() string {
	switch s {
	case ConnectionStateConnected:
		return "connected"
	case ConnectionStateReconnecting:
		return "reconnecting"
	case ConnectionStateUpdating:
		return "updating"
	default:
		return "connecting"
	}
}

// HeldPromptsEvent is pushed to the TUI after a reconnect when prompts
// held during a server update have been (re)delivered. Sent counts the
// prompts accepted by the new server. Failed lists prompts the new
// server rejected for a reason other than draining, with their text so
// the UI can put them back in the editor; Err is the first such error.
type HeldPromptsEvent struct {
	Sent   int
	Failed []FailedPrompt
	Err    error
	// KeptElsewhere counts prompts still held for a workspace other
	// than the one this client is attached to; they are delivered if
	// the client switches back.
	KeptElsewhere int
}

// FailedPrompt is a held prompt that could not be redelivered.
type FailedPrompt struct {
	SessionID   string
	Prompt      string
	Attachments []message.Attachment
	Err         error
}

// ConnectionEvent is pushed to the TUI whenever the client's
// connection state to the server changes. Unlike LSP/MCP events, this
// is synthesized entirely client-side (by definition, no server
// events can arrive while the connection is down).
type ConnectionEvent struct {
	State ConnectionState
	Err   error
}

// AgentModel holds the model information exposed to the UI.
type AgentModel struct {
	CatwalkCfg catwalk.Model
	ModelCfg   config.SelectedModel
}

// Milestone holds milestone information exposed to the UI.
type Milestone struct {
	ID           string
	SessionID    string
	TurnNumber   int64
	ShortSummary string
	FullSummary  string
	CreatedAt    int64
}

// Workspace is the main abstraction consumed by the TUI and CLI. It
// groups every operation a frontend needs to perform against a running
// workspace, regardless of whether the workspace is in-process or
// remote.
type Workspace interface {
	// Sessions
	CreateSession(ctx context.Context, title string) (session.Session, error)
	GetSession(ctx context.Context, sessionID string) (session.Session, error)
	ListSessions(ctx context.Context) ([]session.Session, error)
	ListArchivedSessions(ctx context.Context) ([]session.Session, error)
	ListSessionImportSources(ctx context.Context) ([]sessionimport.SourceInfo, error)
	DiscoverSessionImports(ctx context.Context, source sessionimport.Source) ([]sessionimport.Candidate, error)
	ImportSessions(ctx context.Context, paths []string, from sessionimport.Source) ([]sessionimport.Result, error)
	SaveSession(ctx context.Context, sess session.Session) (session.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	ArchiveSession(ctx context.Context, sessionID string) error
	UnarchiveSession(ctx context.Context, sessionID string) error
	MarkSessionSeen(ctx context.Context, sessionID string) error
	// ArchiveSessionInWorkspace archives a session in an explicit
	// workspace, which may be one other than the attached workspace —
	// including a DETACHED (registry-known but not running) workspace.
	// When workspaceID is empty the workspace is resolved by root. Used by
	// the cross-workspace bulk archive in the session sidebar.
	ArchiveSessionInWorkspace(ctx context.Context, workspaceID, root, sessionID string) error
	// MarkSessionSeenInWorkspace marks a session read in an explicit
	// workspace (attached or detached), mirroring
	// ArchiveSessionInWorkspace. When workspaceID is empty the workspace is
	// resolved by root.
	MarkSessionSeenInWorkspace(ctx context.Context, workspaceID, root, sessionID string) error
	// SetSessionFavoriteInWorkspace pins or unpins a session in an
	// explicit workspace (attached or detached), mirroring
	// MarkSessionSeenInWorkspace. When workspaceID is empty the workspace
	// is resolved by root.
	SetSessionFavoriteInWorkspace(ctx context.Context, workspaceID, root, sessionID string, favorite bool) error
	CreateAgentToolSessionID(messageID, toolCallID string) string
	ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool)
	// SetCurrentSession reports the session this client is currently
	// viewing. Empty sessionID clears the entry (e.g. landing screen).
	// In single-client local mode this is a no-op. In client/server
	// mode it informs the server's per-client presence map so other
	// observers can compute attached-client counts per session.
	SetCurrentSession(ctx context.Context, sessionID string) error

	// SwitchWorkspace re-targets this client at the workspace rooted at
	// path, attaching it on the server if needed and reconnecting the
	// event subscription. The previously attached workspace keeps running.
	SwitchWorkspace(ctx context.Context, path string) error
	// ListWorkspaceOverviews returns all known workspaces (attached and
	// registry-known) with their sessions for the cross-workspace picker.
	ListWorkspaceOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error)

	// Messages
	ListMessages(ctx context.Context, sessionID string) ([]message.Message, error)
	ListUserMessages(ctx context.Context, sessionID string) ([]message.Message, error)
	ListAllUserMessages(ctx context.Context) ([]message.Message, error)
	// PeekMessages returns a session's messages from any known workspace
	// (attached or registry-detached) identified by root, WITHOUT
	// switching this client's own workspace. Used by the session
	// sidebar's live preview so previewing a session outside the
	// currently-attached workspace doesn't require a heavy full switch.
	PeekMessages(ctx context.Context, root, sessionID string) ([]message.Message, error)
	// PeekSessionInfo returns a session's metadata and history files from
	// any known workspace (attached or registry-detached) identified by
	// root, WITHOUT switching this client's own workspace. It is the
	// sidebar-data companion to PeekMessages: the session sidebar's live
	// preview uses it to reflect the highlighted session's title, swarm
	// identity, working dir, cost/tokens, and modified files.
	PeekSessionInfo(ctx context.Context, root, sessionID string) (session.Session, []history.File, error)

	// Agent
	AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error
	// AgentRunBTW sends a mid-turn steer: the message is folded into the
	// active turn at the next step and long-running tools are asked to
	// wrap up early so it lands sooner. On an idle session it is a
	// normal prompt.
	AgentRunBTW(ctx context.Context, sessionID, prompt string) error
	// AgentRunAside folds a message into the active turn at the next
	// step boundary without hurrying the current step along. Use it for
	// low-urgency notices (e.g. "the working directory changed") that
	// should not cut a running command short.
	AgentRunAside(ctx context.Context, sessionID, prompt string) error
	// AgentSoftInterrupt asks the tools running in the session's current
	// step to wrap up early without cancelling them (a running shell
	// command becomes a background job) and lets the turn continue.
	AgentSoftInterrupt(sessionID string)
	// AgentBackgroundTool asks one in-flight tool call to hand its work
	// back as a background job so the turn continues. Errors when the
	// call is unknown, finished, or cannot be backgrounded.
	AgentBackgroundTool(ctx context.Context, sessionID, toolCallID string) error
	AgentRunShellCommand(ctx context.Context, sessionID, command string) (proto.ShellCommandResponse, error)
	AgentCancel(sessionID string)
	// AgentCancelAll cancels every in-flight agent run in the workspace,
	// regardless of session. Used when the client is not focused on the
	// busy session (e.g. after detach/reattach).
	AgentCancelAll()
	AgentIsBusy() bool
	AgentIsSessionBusy(sessionID string) bool
	AgentModel() AgentModel
	AgentIsReady() bool
	// AgentReadiness reports whether the coder agent is initialized,
	// distinguishing "server reachable and says not ready" (err == nil,
	// ready == false) from "could not reach the server" (err != nil).
	// Callers that must not lose user input on a transient network blip
	// should use this instead of AgentIsReady.
	AgentReadiness(ctx context.Context) (ready bool, err error)
	AgentQueuedPrompts(sessionID string) int
	AgentQueuedPromptsList(sessionID string) []string
	AgentClearQueue(sessionID string)
	AgentSummarize(ctx context.Context, sessionID string) error
	// AgentGenerateTitle regenerates a session's title on demand.
	AgentGenerateTitle(ctx context.Context, sessionID string) error
	// Goal controls the autonomous /goal feature.
	AgentSetGoal(sessionID, condition string) error
	// AgentSetWorkingDir records the directory tools run in for a session.
	AgentSetWorkingDir(sessionID, dir string) error
	AgentClearGoal(sessionID string) error
	AgentGoalStatus(sessionID string) (proto.GoalStatus, error)
	UpdateAgentModel(ctx context.Context) error
	InitCoderAgent(ctx context.Context) error
	GetDefaultSmallModel(providerID string) config.SelectedModel

	// ServerVersion reports the version information of the backend the
	// frontend is talking to. Frontends use it to detect a client/server
	// version mismatch (e.g. when another client restarted the shared
	// server with a different binary).
	ServerVersion(ctx context.Context) (proto.VersionInfo, error)

	// Permissions
	//
	// PermissionGrant, PermissionGrantPersistent, and PermissionDeny
	// return true if the call resolved the pending request and false if
	// it had already been resolved by another subscriber (or is no
	// longer pending). A false return is not an error; the modal can
	// still close locally because the resolution will arrive via the
	// PermissionNotification event stream regardless of which client
	// won the race.
	PermissionGrant(perm permission.PermissionRequest) bool
	PermissionGrantPersistent(perm permission.PermissionRequest) bool
	PermissionDeny(perm permission.PermissionRequest) bool
	PermissionSkipRequests() bool
	PermissionSetSkipRequests(skip bool)
	PermissionSysadminMode() bool
	PermissionSetSysadminMode(enabled bool)

	// Questions
	//
	// QuestionAnswer resolves a pending question (or reports it as
	// cancelled via ans.Cancelled). It returns true if the call
	// resolved the pending request and false if it had already been
	// resolved by another subscriber (or is no longer pending). A
	// false return is not an error; the dialog can still close
	// locally because the resolution will arrive via the question
	// notification event stream regardless of which client won the
	// race.
	QuestionAnswer(ans question.Answer) bool

	// FileTracker
	FileTrackerRecordRead(ctx context.Context, sessionID, path string)
	FileTrackerLastReadTime(ctx context.Context, sessionID, path string) time.Time
	FileTrackerListReadFiles(ctx context.Context, sessionID string) ([]string, error)

	// History
	ListSessionHistory(ctx context.Context, sessionID string) ([]history.File, error)

	// LSP
	LSPStart(ctx context.Context, path string)
	LSPStopAll(ctx context.Context)
	LSPGetStates() map[string]LSPClientInfo
	LSPGetDiagnosticCounts(name string) lsp.DiagnosticCounts

	// Skills
	SkillsGetStates() []*skills.SkillState

	// Config (read-only data)
	Config() *config.Config
	WorkingDir() string
	BaseDir() string             // Project base directory (not worktree-aware)
	EffectiveWorkingDir() string // Launch cwd; may differ from BaseDir() for linked worktrees
	GitBranch() string
	GitBranchForDir(dir string) string // Branch for a specific dir (e.g. an attached session's working dir)
	Resolver() config.VariableResolver

	// Session context for worktree-aware working directory
	SetActiveSessionID(sessionID string)
	ActiveSessionID() string

	// Config mutations (proxied to server in client mode)
	ReloadConfig(ctx context.Context) error
	UpdatePreferredModel(scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error
	SetCompactMode(scope config.Scope, enabled bool) error
	SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error
	SetConfigField(scope config.Scope, key string, value any) error
	RemoveConfigField(scope config.Scope, key string) error
	// EmbedPendingCount reports how many past messages would be embedded
	// by a backfill under the active embedding model.
	EmbedPendingCount(ctx context.Context) (int, error)
	// EmbedBackfill embeds past messages lacking a vector and returns the
	// count embedded.
	EmbedBackfill(ctx context.Context) (int, error)
	// EmbedStatus reports the embedding index state for progress display.
	EmbedStatus(ctx context.Context) (proto.EmbeddingStatus, error)
	// SearchHistory runs hybrid history search and returns per-session
	// hits tagged with their originating workspace.
	SearchHistory(ctx context.Context, params proto.SearchHistoryParams) (proto.SearchHistoryResult, error)
	ImportCopilot() (*oauth.Token, bool)
	RefreshOAuthToken(ctx context.Context, scope config.Scope, providerID string) error

	// Project lifecycle
	ProjectNeedsInitialization() (bool, error)
	MarkProjectInitialized() error
	InitializePrompt() (string, error)
	ListSkills(ctx context.Context) ([]skills.CatalogEntry, error)
	ReadSkill(ctx context.Context, skillID string) ([]byte, skills.SkillReadResult, error)

	// MCP operations (server-side in client mode)
	MCPGetStates() map[string]mcptools.ClientInfo
	MCPRefreshPrompts(ctx context.Context, name string)
	MCPRefreshResources(ctx context.Context, name string)
	RefreshMCPTools(ctx context.Context, name string)
	ReadMCPResource(ctx context.Context, name, uri string) ([]MCPResourceContents, error)
	GetMCPPrompt(clientID, promptID string, args map[string]string) (string, error)
	EnableDockerMCP(ctx context.Context) error
	DisableDockerMCP() error
	MCPAuthenticate(ctx context.Context, name string) error
	MCPPendingAuth() []mcptools.PendingAuthServer
	MCPAuthURL(name string) string

	// Snapshots
	SnapshotsEnabled() bool
	ListSnapshots(ctx context.Context, sessionID string) ([]*checkpoint.Snapshot, error)
	GetSnapshot(ctx context.Context, snapshotID string) (*checkpoint.Snapshot, error)
	GetSnapshotByMessage(ctx context.Context, messageID string) (*checkpoint.Snapshot, error)
	RestoreSnapshot(ctx context.Context, snapshotID string) error
	DiffFromCurrentSnapshot(ctx context.Context, snapshotID string) (string, error)

	// Worktrees
	WorktreesEnabled() bool
	ListWorktrees(ctx context.Context, sessionID string) ([]*worktree.Worktree, error)
	ListAllWorktrees(ctx context.Context) ([]*worktree.Worktree, error)
	GetWorktree(ctx context.Context, worktreeID string) (*worktree.Worktree, error)
	GetActiveWorktree(ctx context.Context, sessionID string) (*worktree.Worktree, error)
	CreateWorktree(ctx context.Context, sessionID, name, fromSnapshotID string) (*worktree.Worktree, error)
	SwitchWorktree(ctx context.Context, sessionID, worktreeID string) error
	DeleteWorktree(ctx context.Context, worktreeID string) error
	MergeWorktree(ctx context.Context, worktreeID, targetBranch string, rebase bool) error
	ListGitBranches(ctx context.Context) ([]string, error)

	// Forks
	ForkConversation(ctx context.Context, params fork.ForkParams) (*fork.ForkResult, error)

	// Milestones
	ListMilestones(ctx context.Context, sessionID string) ([]Milestone, error)

	// Garbage Collection
	SnapshotGC(ctx context.Context) (int64, error)
	SnapshotStats(ctx context.Context) (*checkpoint.Stats, error)

	// Events
	Subscribe(program *tea.Program)
	Shutdown()
	// ConnectionState reports the current state of the event-stream
	// connection to the server. It complements the pushed
	// ConnectionEvent (via Subscribe) with a pull-based snapshot for
	// initial paint before any event has arrived.
	ConnectionState() ConnectionState
}

// MCPResourceContents holds the contents of an MCP resource.
type MCPResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     []byte `json:"blob,omitempty"`
}
