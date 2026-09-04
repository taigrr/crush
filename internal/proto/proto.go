package proto

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/sessionimport"
)

// Workspace represents a running app.App workspace with its associated
// resources and state.
type Workspace struct {
	ID         string         `json:"id"`
	Path       string         `json:"path"`
	WorkingDir string         `json:"working_dir,omitempty"`
	GitBranch  string         `json:"git_branch,omitempty"`
	YOLO       bool           `json:"yolo,omitempty"`
	Isolated   bool           `json:"isolated,omitempty"`
	Debug      bool           `json:"debug,omitempty"`
	DataDir    string         `json:"data_dir,omitempty"`
	Version    string         `json:"version,omitempty"`
	Config     *config.Config `json:"config,omitempty"`
	Env        []string       `json:"env,omitempty"`
	Skills     []SkillState   `json:"skills,omitempty"`
	ClientID   string         `json:"client_id,omitempty"`
}

// Error represents an error response.
type Error struct {
	Message string `json:"message"`
	// Code, when set, identifies the condition machine-readably. See
	// the ErrorCode* constants.
	Code string `json:"code,omitempty"`
}

// RunComplete is the authoritative end-of-run signal for a session,
// emitted exactly once per top-level agent turn after all message
// updates for the turn have flushed. Clients that need a reliable
// completion contract (notably `crush run` in client/server mode)
// should listen for this event filtered by RunID (preferred) — or
// by SessionID when no RunID was supplied — and use Text and
// MessageID to reconcile any output they have already streamed from
// earlier message events. Error is non-empty when the run terminated
// with an error; Cancelled is true when terminated due to context
// cancellation.
//
// RunID echoes the value the caller set on AgentMessage.RunID. It is
// the only safe correlator when the caller's prompt was queued
// behind a busy session: another turn's RunComplete for the same
// SessionID may arrive first, and filtering by SessionID alone
// would terminate the caller before its own turn ran.
type RunComplete struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
	MessageID string `json:"message_id"`
	Text      string `json:"text,omitempty"`
	Error     string `json:"error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// ForkProgress is a progress update emitted while a conversation fork runs.
// Forking is a blocking RPC; these events drive a live progress bar in the
// client so the UI does not appear frozen.
type ForkProgress struct {
	SourceSessionID string  `json:"source_session_id"`
	Stage           string  `json:"stage"`
	Detail          string  `json:"detail,omitempty"`
	Percent         float64 `json:"percent"`
	Done            bool    `json:"done,omitempty"`
}

// SkillInfo describes a visible skill exposed to a frontend.
type SkillInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Label         string `json:"label"`
	Source        string `json:"source"`
	UserInvocable bool   `json:"user_invocable"`
}

// ReadSkillRequest is the request body for reading a skill's content.
type ReadSkillRequest struct {
	SkillID string `json:"skill_id"`
}

// ReadSkillResponse is the response for reading a skill's content.
type ReadSkillResponse struct {
	Content []byte          `json:"content"`
	Result  SkillReadResult `json:"result"`
}

// SkillReadResult holds metadata about a skill returned alongside its
// content.
type SkillReadResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Builtin     bool   `json:"builtin"`
}

// ConfigChanged is published whenever the workspace's configuration is
// mutated by a backend operation. Clients react by re-fetching the
// workspace snapshot so cached config stays in sync across subscribers.
type ConfigChanged struct {
	WorkspaceID string `json:"workspace_id"`
}

// CurrentSession is the request body for the per-client
// current-session endpoint. An empty SessionID clears the entry.
type CurrentSession struct {
	SessionID string `json:"session_id"`
}

type SessionImportRequest struct {
	Paths  []string `json:"paths"`
	Source string   `json:"source,omitempty"`
}

type SessionImportSources = sessionimport.SourceInfo

type SessionImportCandidate = sessionimport.Candidate

type SessionImportResult = sessionimport.Result

// AgentInfo represents information about the agent.
type AgentInfo struct {
	IsBusy   bool                 `json:"is_busy"`
	IsReady  bool                 `json:"is_ready"`
	Model    catwalk.Model        `json:"model"`
	ModelCfg config.SelectedModel `json:"model_cfg"`
}

// IsZero checks if the AgentInfo is zero-valued.
func (a AgentInfo) IsZero() bool {
	return !a.IsBusy && !a.IsReady && a.Model.ID == ""
}

// AgentMessage represents a message sent to the agent.
//
// RunID, when non-empty, is echoed back on the [RunComplete] event
// emitted for the resulting turn. Callers that need to correlate a
// specific SendMessage with its terminal event (notably
// `crush run`, which may attach to a busy session whose currently
// running turn finishes first) should set it to a fresh unique
// value before the request. Server-side propagation flows through
// agent.WithRunID on the request context into the
// SessionAgentCall; it is preserved across the busy-session queue.
// When empty the resulting RunComplete carries an empty RunID and
// callers must fall back to SessionID-only filtering, which
// remains correct only when no other turns are in flight for the
// same session.
type AgentMessage struct {
	SessionID string `json:"session_id"`
	// ClientID identifies the client that initiated the turn. The
	// server uses it to route per-client resources (notably the editor
	// bridge) to the right client. Empty means "no specific client".
	ClientID    string       `json:"client_id,omitempty"`
	RunID       string       `json:"run_id,omitempty"`
	Prompt      string       `json:"prompt"`
	Attachments []Attachment `json:"attachments,omitempty"`
	// Steer marks a mid-turn steering message. On a busy session the
	// prompt is queued as usual and the session's soft interrupt is
	// raised so long-running tools that opt in (bash, job_output) wrap
	// up early — returning their work as a background job rather than
	// being cancelled — and the queued prompt is folded into the turn
	// at the next step. Combine with an empty RunID to fold rather than
	// wait for a dedicated turn. On an idle session it is a normal
	// prompt.
	Steer bool `json:"steer,omitempty"`
	// SwarmParts, when set, replaces the default TextContent user
	// message with one or more [SwarmMessage] parts. Used by
	// [Backend.SwarmSend] so the receiving session records
	// structured sender metadata (color, animal, workspace) instead
	// of a plain text prefix. The Prompt field must still be set to
	// the concatenated user-visible text so downstream code that
	// treats prompts as strings (queue drop notifications, run
	// logs) keeps working.
	SwarmParts []SwarmMessage `json:"swarm_parts,omitempty"`
}

// ShellCommandRequest represents a request to run a shell command directly.
type ShellCommandRequest struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
	// ClientID identifies the client that initiated the command so the
	// server can run it in that client's launch directory. Multiple
	// clients can share one workspace (subdirectories or sibling git
	// worktrees collapsing to the same project root); without it the
	// command would run in whichever client created the workspace first.
	ClientID string `json:"client_id,omitempty"`
}

// ShellCommandResponse represents the result of a direct shell command.
type ShellCommandResponse struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// SetGoalRequest sets (or replaces) the autonomous goal for a session. A
// blank Condition clears any active goal.
type SetGoalRequest struct {
	SessionID string `json:"session_id"`
	Condition string `json:"condition"`
}

// SetWorkingDirRequest sets the working directory tools run in for a
// session. The directory is expected to be absolute and resolved by the
// caller.
type SetWorkingDirRequest struct {
	WorkingDir string `json:"working_dir"`
}

// SessionOverview is a lightweight, cross-workspace session view for the
// session picker. IsBusy is only meaningful for sessions in an attached
// workspace (the server can only know live run state for workspaces it
// hosts); for unattached workspaces it is always false.
type SessionOverview struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	WorkingDir string `json:"working_dir,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
	IsBusy     bool   `json:"is_busy"`
	Unread     bool   `json:"unread"`
	// Color and Animal are the session's swarm identity (see
	// [session.Session.Color] / .Animal). Empty when the session
	// has not yet been backfilled; the picker/sidebar should fall
	// back to no color square in that case.
	Color  string `json:"color,omitempty"`
	Animal string `json:"animal,omitempty"`
	// Favorite pins the session to the top of the sidebar inbox (below
	// sessions blocked on a permission prompt). It is read from the
	// session's database (attached or detached) so it persists.
	Favorite bool `json:"favorite,omitempty"`
	// SpawnedBySessionID is the swarm lineage of the session (see
	// [session.Session.SpawnedBySessionID]): the session that created
	// it via `swarm new`. Empty for human/client-opened sessions. The
	// sidebar uses it to nest workers under their spawner.
	SpawnedBySessionID string `json:"spawned_by_session_id,omitempty"`
}

// WorkspaceOverview groups a workspace's sessions for the picker. Attached
// is true when the workspace is currently hosted by the server (live busy
// state is available); false workspaces are known only from the registry
// and their sessions are read from their database read-only.
type WorkspaceOverview struct {
	Root        string            `json:"root"`
	DataDir     string            `json:"data_dir"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Attached    bool              `json:"attached"`
	Sessions    []SessionOverview `json:"sessions"`
}

// GoalStatus reports the active autonomous goal for a session.
type GoalStatus struct {
	Active    bool   `json:"active"`
	Condition string `json:"condition"`
	Turns     int    `json:"turns"`
	MaxTurns  int    `json:"max_turns"`
}

// EmbeddingStatus reports the embedding index state for a workspace.
type EmbeddingStatus struct {
	Enabled  bool `json:"enabled"`
	Embedded int  `json:"embedded"` // messages with a vector under the active model
	Total    int  `json:"total"`    // embeddable messages (embedded + pending)
}

// SearchHistoryParams configures a history search RPC. It mirrors
// historysearch.Options but crosses the client/server boundary. Scope is
// "user" (default) or "all"; Semantic overrides the hybrid_search config
// default for this call (nil = use config).
type SearchHistoryParams struct {
	Query    string `json:"query"`
	Scope    string `json:"scope,omitempty"`
	Semantic *bool  `json:"semantic,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	// AllWorkspaces opts into cross-workspace fan-out: when true the
	// backend also searches every other known workspace (attached and
	// registry-detached) and merges the results. When false (the
	// default) only the requested workspace is searched — the fast path.
	AllWorkspaces bool `json:"all_workspaces,omitempty"`
	// Offset is reserved: session-level pagination is not yet supported
	// (results collapse per session after a message-level candidate
	// fetch, so a message offset would not align to session boundaries).
	// It is currently ignored by the backend.
	Offset int `json:"offset,omitempty"`
}

// SessionHit is one per-session search result: the search collapses the
// underlying per-message hits by session (best-hit-wins), keeping the
// top-scoring hit's snippet and metadata as the session's representative.
// Every hit is tagged with its originating workspace so a cross-workspace
// palette can group and route results (in single-workspace mode all hits
// carry the same tag).
type SessionHit struct {
	SessionID     string    `json:"session_id"`
	SessionTitle  string    `json:"session_title"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceRoot string    `json:"workspace_root"`
	Score         float64   `json:"score"`
	Match         string    `json:"match"`
	Snippet       string    `json:"snippet"`
	MessageID     string    `json:"message_id"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
}

// SearchHistoryResult is a ranked page of per-session hits.
type SearchHistoryResult struct {
	Hits         []SessionHit `json:"hits"`
	Total        int          `json:"total"`
	SemanticUsed bool         `json:"semantic_used"`
}

// PeekMessagesParams identifies a session in a possibly-foreign
// workspace to preview. Root, not a workspace ID, is the key: a
// workspace the caller has never attached to has no ID yet, only a
// filesystem root (matching [WorkspaceOverview.Root]).
type PeekMessagesParams struct {
	Root      string `json:"root"`
	SessionID string `json:"session_id"`
}

// PeekSessionInfoResult carries a session's metadata and history files
// read from a possibly-foreign workspace, without switching the caller's
// own workspace. It backs the session sidebar's live preview so the right
// info-sidebar (title, swarm identity, working dir, cost/tokens, modified
// files) reflects the highlighted session alongside its previewed messages.
type PeekSessionInfoResult struct {
	Session Session `json:"session"`
	Files   []File  `json:"files"`
}

// AgentSession represents a session with its busy status.
type AgentSession struct {
	Session
	IsBusy bool `json:"is_busy"`
}

// IsZero checks if the AgentSession is zero-valued.
func (a AgentSession) IsZero() bool {
	return a.ID == "" && !a.IsBusy
}

// PermissionAction represents an action taken on a permission request.
type PermissionAction string

const (
	PermissionAllow           PermissionAction = "allow"
	PermissionAllowForSession PermissionAction = "allow_session"
	PermissionDeny            PermissionAction = "deny"
)

// MarshalText implements the [encoding.TextMarshaler] interface.
func (p PermissionAction) MarshalText() ([]byte, error) {
	return []byte(p), nil
}

// UnmarshalText implements the [encoding.TextUnmarshaler] interface.
func (p *PermissionAction) UnmarshalText(text []byte) error {
	*p = PermissionAction(text)
	return nil
}

// PermissionGrant represents a permission grant request.
type PermissionGrant struct {
	Permission PermissionRequest `json:"permission"`
	Action     PermissionAction  `json:"action"`
}

// PermissionGrantResponse is the server's response to a permission
// grant call. Resolved is true when this call resolved the pending
// request, and false when the request had already been resolved by a
// previous caller (e.g., another client in a multi-subscriber UI). A
// false value is not an error.
type PermissionGrantResponse struct {
	Resolved bool `json:"resolved"`
}

// PermissionSkipRequest represents a request to skip permission prompts.
type PermissionSkipRequest struct {
	Skip bool `json:"skip"`
}

// PermissionSysadminRequest represents a request to toggle ephemeral
// sysadmin mode (allowing the bash tool's sysadmin command filter to be
// bypassed).
type PermissionSysadminRequest struct {
	Sysadmin bool `json:"sysadmin"`
}

// LSPEventType represents the type of LSP event.
type LSPEventType string

const (
	LSPEventStateChanged       LSPEventType = "state_changed"
	LSPEventDiagnosticsChanged LSPEventType = "diagnostics_changed"
)

// MarshalText implements the [encoding.TextMarshaler] interface.
func (e LSPEventType) MarshalText() ([]byte, error) {
	return []byte(e), nil
}

// UnmarshalText implements the [encoding.TextUnmarshaler] interface.
func (e *LSPEventType) UnmarshalText(data []byte) error {
	*e = LSPEventType(data)
	return nil
}

// LSPEvent represents an event in the LSP system.
type LSPEvent struct {
	Type            LSPEventType    `json:"type"`
	Name            string          `json:"name"`
	State           lsp.ServerState `json:"state"`
	Error           error           `json:"error,omitempty"`
	DiagnosticCount int             `json:"diagnostic_count,omitempty"`
}

// MarshalJSON implements the [json.Marshaler] interface.
func (e LSPEvent) MarshalJSON() ([]byte, error) {
	type Alias LSPEvent
	return json.Marshal(&struct {
		Error string `json:"error,omitempty"`
		Alias
	}{
		Error: func() string {
			if e.Error != nil {
				return e.Error.Error()
			}
			return ""
		}(),
		Alias: Alias(e),
	})
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (e *LSPEvent) UnmarshalJSON(data []byte) error {
	type Alias LSPEvent
	aux := &struct {
		Error string `json:"error,omitempty"`
		Alias
	}{
		Alias: Alias(*e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*e = LSPEvent(aux.Alias)
	if aux.Error != "" {
		e.Error = errors.New(aux.Error)
	}
	return nil
}

// LSPClientInfo holds information about an LSP client's state.
type LSPClientInfo struct {
	Name            string          `json:"name"`
	State           lsp.ServerState `json:"state"`
	Error           error           `json:"error,omitempty"`
	DiagnosticCount int             `json:"diagnostic_count,omitempty"`
	ConnectedAt     time.Time       `json:"connected_at"`
}

// MarshalJSON implements the [json.Marshaler] interface.
func (i LSPClientInfo) MarshalJSON() ([]byte, error) {
	type Alias LSPClientInfo
	return json.Marshal(&struct {
		Error string `json:"error,omitempty"`
		Alias
	}{
		Error: func() string {
			if i.Error != nil {
				return i.Error.Error()
			}
			return ""
		}(),
		Alias: Alias(i),
	})
}

// UnmarshalJSON implements the [json.Unmarshaler] interface.
func (i *LSPClientInfo) UnmarshalJSON(data []byte) error {
	type Alias LSPClientInfo
	aux := &struct {
		Error string `json:"error,omitempty"`
		Alias
	}{
		Alias: Alias(*i),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*i = LSPClientInfo(aux.Alias)
	if aux.Error != "" {
		i.Error = errors.New(aux.Error)
	}
	return nil
}
