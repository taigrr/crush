package proto

// Session-addressed routes for paired devices. They name a session by its
// ID (plus, optionally, its workspace root) instead of a runtime workspace
// ID, which changes whenever a workspace is re-attached and does not exist
// for detached workspaces.

// ErrCodeSessionNotOnNode is the [Error.Code] of a 404 for a session that
// is not owned by the answering node; the client should re-route to the
// session's node.
const ErrCodeSessionNotOnNode = "session_not_on_node"

// ErrCodeUnknownRoot is the [Error.Code] of a 404 for a workspace root
// that is not in the node's registry of known workspaces.
const ErrCodeUnknownRoot = "unknown_root"

// ErrCodeInvalidCursor is the [Error.Code] of a 400 for a malformed or
// unknown paging cursor.
const ErrCodeInvalidCursor = "invalid_cursor"

// DeviceSessionOverview is one session of GET /v1/session-overviews.
type DeviceSessionOverview struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updated_at"`
	IsBusy    bool   `json:"is_busy"`
	Unread    bool   `json:"unread"`
	Color     string `json:"color"`
	Animal    string `json:"animal"`
	Favorite  bool   `json:"favorite"`
	NodeID    string `json:"node_id"`
	NodeName  string `json:"node_name"`
	Root      string `json:"root"`
	// DataDir is omitted from slim rows (fields=row).
	DataDir string `json:"data_dir,omitempty"`
	// WorkspaceID is set only while the workspace is attached; it is a
	// runtime handle, not a stable identifier.
	WorkspaceID string `json:"workspace_id,omitempty"`
	Attached    bool   `json:"attached"`
} //@name proto.DeviceSessionOverview

// NodeUnavailable reports a cluster node (or, with Root set, one of its
// workspaces) whose sessions could not be read.
type NodeUnavailable struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Root     string `json:"root,omitempty"`
	Error    string `json:"error"`
} //@name proto.NodeUnavailable

// Query parameters of GET /v1/session-overviews.
const (
	SessionOverviewsParamLimit  = "limit"
	SessionOverviewsParamCursor = "cursor"
	SessionOverviewsParamLocal  = "local"
	SessionOverviewsParamSince  = "since"
	SessionOverviewsParamFields = "fields"
)

// SessionOverviewFields selects which overview fields are returned.
type SessionOverviewFields string

// SessionOverviewFieldsRow returns slim rows: only what a session list
// row shows (no data_dir).
const SessionOverviewFieldsRow SessionOverviewFields = "row"

// SessionOverviewFieldsCursor returns no rows, only the sync cursor: what
// a node needs to record a member's change log position.
const SessionOverviewFieldsCursor SessionOverviewFields = "cursor"

// SessionTombstone names a session that left a node's list (deleted,
// archived, or moved to another node) since a sync cursor.
type SessionTombstone struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Root   string `json:"root"`
} //@name proto.SessionTombstone

// DeviceSessionOverviewPage is the GET /v1/session-overviews response.
//
// Without since it is one page of the full list; SyncCursor is the
// position the list was read at (keep the first page's). With since it
// is a delta: Sessions are the rows created or changed since the cursor,
// Deleted the rows that left, and SyncCursor the new position.
type DeviceSessionOverviewPage struct {
	Sessions []DeviceSessionOverview `json:"sessions"`
	// NextCursor fetches the next page; null on the last page.
	NextCursor  *string           `json:"next_cursor"`
	Total       int               `json:"total"`
	Unavailable []NodeUnavailable `json:"unavailable"`
	// SyncCursor is an opaque position for a later since request. Absent
	// from servers that predate delta sync, and on a reset.
	SyncCursor string `json:"sync_cursor,omitempty"`
	// HasMore is set when NextCursor is.
	HasMore bool `json:"has_more,omitempty"`
	// Deleted lists tombstones (delta responses only).
	Deleted []SessionTombstone `json:"deleted,omitzero"`
	// Reset says the since cursor is unusable as a whole (malformed, from
	// a restarted node, or older than the retained change window): do a
	// full fetch. Sessions and Deleted are empty.
	Reset bool `json:"reset,omitempty"`
	// ResetNodes are nodes whose full list is included in Sessions: the
	// client replaces every row of those nodes (after the last page).
	ResetNodes []string `json:"reset_nodes,omitzero"`
	// StaleNodes are nodes that could not be asked; their rows and cursor
	// positions are unchanged.
	StaleNodes []string `json:"stale_nodes,omitzero"`
} //@name proto.DeviceSessionOverviewPage

// DeviceSession is the GET /v1/sessions/{sid} response.
type DeviceSession struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	UpdatedAt    int64  `json:"updated_at"`
	IsBusy       bool   `json:"is_busy"`
	NodeID       string `json:"node_id"`
	Root         string `json:"root"`
	DataDir      string `json:"data_dir"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	Attached     bool   `json:"attached"`
	MessageCount int    `json:"message_count"`
} //@name proto.DeviceSession

// DeviceSessionMessages is the GET /v1/sessions/{sid}/messages response:
// one page of messages in chronological order.
type DeviceSessionMessages struct {
	Messages []Message `json:"messages"`
	// NextBefore is the `before` value for the next (older) page; null
	// when this page reaches the first message.
	NextBefore  *string `json:"next_before"`
	WorkspaceID string  `json:"workspace_id,omitempty"`
} //@name proto.DeviceSessionMessages

// DeviceSessionOpenParams is the POST /v1/sessions/{sid}/open body.
type DeviceSessionOpenParams struct {
	Root string `json:"root"`
} //@name proto.DeviceSessionOpenParams

// DeviceSessionOpen is the POST /v1/sessions/{sid}/open response.
type DeviceSessionOpen struct {
	WorkspaceID string `json:"workspace_id"`
	SessionID   string `json:"session_id"`
	Root        string `json:"root"`
} //@name proto.DeviceSessionOpen

// NodeSessionHit is a [SessionHit] tagged with the node and workspace
// root it came from.
type NodeSessionHit struct {
	SessionHit
	NodeID string `json:"node_id"`
	Root   string `json:"root"`
} //@name proto.NodeSessionHit

// NodeSearchHistoryParams is the POST /v1/history/search body.
type NodeSearchHistoryParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Scope string `json:"scope,omitempty"`
} //@name proto.NodeSearchHistoryParams

// NodeSearchHistoryResult is the POST /v1/history/search response.
type NodeSearchHistoryResult struct {
	Hits           []NodeSessionHit `json:"hits"`
	Total          int              `json:"total"`
	SemanticUsed   bool             `json:"semantic_used"`
	Mode           string           `json:"mode,omitempty" enums:"hybrid,text"`
	FallbackReason string           `json:"fallback_reason,omitempty"`
} //@name proto.NodeSearchHistoryResult

// WorkspaceRoot is one registry-known workspace root of this node.
type WorkspaceRoot struct {
	Root         string `json:"root"`
	DataDir      string `json:"data_dir"`
	Attached     bool   `json:"attached"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	LastUsedAt   int64  `json:"last_used_at"`
	SessionCount int    `json:"session_count"`
} //@name proto.WorkspaceRoot

// WorkspaceRoots is the GET /v1/workspace-roots response.
type WorkspaceRoots struct {
	Roots []WorkspaceRoot `json:"roots"`
} //@name proto.WorkspaceRoots

// DeviceCreateSessionParams is the POST /v1/sessions body.
type DeviceCreateSessionParams struct {
	Root   string `json:"root"`
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
	// ClientID is stamped by the device bridge; any device-supplied value
	// is replaced.
	ClientID string `json:"client_id,omitempty"`
} //@name proto.DeviceCreateSessionParams

// DeviceCreatedSession is the POST /v1/sessions response.
type DeviceCreatedSession struct {
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	Root        string `json:"root"`
	NodeID      string `json:"node_id"`
} //@name proto.DeviceCreatedSession
