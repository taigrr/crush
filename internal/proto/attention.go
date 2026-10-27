package proto

// AttentionKind classifies a cross-workspace attention transition
// carried on the global attention channel.
type AttentionKind string

const (
	// AttentionBusy: the session started an agent turn.
	AttentionBusy AttentionKind = "busy"
	// AttentionIdle: the session's agent turn finished.
	AttentionIdle AttentionKind = "idle"
	// AttentionBlockedPermission: the session is blocked awaiting a
	// permission response.
	AttentionBlockedPermission AttentionKind = "blocked_permission"
	// AttentionBlockedQuestion: the session is blocked awaiting a
	// question answer.
	AttentionBlockedQuestion AttentionKind = "blocked_question"
	// AttentionResolved: a previously-signalled block was resolved
	// (granted/denied/answered/cancelled, or the workspace tore down).
	AttentionResolved AttentionKind = "resolved"
	// AttentionClusterJoin: a node asked to join this server's cluster and
	// awaits approval. Detail carries the node name; ToolCallID the
	// join request ID. Not tied to a workspace or session.
	AttentionClusterJoin AttentionKind = "cluster_join"
	// AttentionOverviews: the cluster-wide session list changed (a remote
	// node's sessions were refreshed); clients should re-read overviews.
	AttentionOverviews AttentionKind = "overviews"
)

// AttentionEvent is a narrow, cross-workspace signal published on the
// backend's global attention channel and streamed to clients over the
// observe-only GET /v1/events endpoint. It carries only what a client
// needs to surface a background session's state (busy dot, red
// pending indicator, red/green window border) and to switch-to-grant:
// the originating workspace (id + root, so the client can re-target it)
// and session, plus the tool call id so a resolution can be matched
// against a cached prompt.
//
// It deliberately does NOT carry message content, params, or prompts:
// those still travel on the focused workspace's per-workspace stream and
// surface only when the client switches to that session.
type AttentionEvent struct {
	WorkspaceID   string        `json:"workspace_id"`
	WorkspaceRoot string        `json:"workspace_root"`
	SessionID     string        `json:"session_id"`
	ToolCallID    string        `json:"tool_call_id,omitempty"`
	Kind          AttentionKind `json:"kind"`
	// Detail is a short human-readable label (e.g. the joining node name).
	Detail string `json:"detail,omitempty"`
	// NodeID and NodeName identify the originating cluster node. Empty
	// means the server the client is attached to.
	NodeID   string `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`
}
