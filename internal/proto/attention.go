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
}
