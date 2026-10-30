package headless

import (
	"context"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// SessionState is the coarse state reported by `crush session status`.
type SessionState string

const (
	// StateRunning means the agent is working on the session.
	StateRunning SessionState = "running"
	// StateBlocked means the session waits on a permission decision or a
	// question; see [SessionStatus.Pending].
	StateBlocked SessionState = "blocked"
	// StateIdle means nothing is running; [SessionStatus.Last] describes
	// the most recent turn.
	StateIdle SessionState = "idle"
)

// SessionStatus is a point-in-time snapshot of a session.
type SessionStatus struct {
	Type          EventType             `json:"type"`
	State         SessionState          `json:"state"`
	SessionID     string                `json:"session_id"`
	ShortID       string                `json:"short_id,omitempty"`
	Address       string                `json:"address,omitempty"`
	Title         string                `json:"title,omitempty"`
	WorkspaceID   string                `json:"workspace_id"`
	Cwd           string                `json:"cwd,omitempty"`
	QueuedPrompts int                   `json:"queued_prompts"`
	Pending       *proto.PendingPrompts `json:"pending,omitempty"`
	Last          *LastTurn             `json:"last,omitempty"`
	Usage         Usage                 `json:"usage"`
	AttachCommand string                `json:"attach_command"`
}

// LastTurn summarizes the most recent completed turn of an idle session.
type LastTurn struct {
	Status    Status `json:"status"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text"`
	Error     string `json:"error,omitempty"`
}

// StatusClient is the subset of the server client needed to build a
// [SessionStatus].
type StatusClient interface {
	WatchClient
	GetAgentSessionQueuedPrompts(ctx context.Context, workspaceID, sessionID string) (int, error)
}

// BuildStatus reports the current state of sess.
func BuildStatus(ctx context.Context, c StatusClient, workspaceID, cwd string, sess proto.Session) (SessionStatus, error) {
	st := SessionStatus{
		Type:          EventStatus,
		State:         StateIdle,
		SessionID:     sess.ID,
		ShortID:       session.ShortID(sess.ID),
		Address:       SessionAddress(sess),
		Title:         sess.Title,
		WorkspaceID:   workspaceID,
		Cwd:           cwd,
		AttachCommand: AttachCommand(cwd, sess.ID),
		Usage: Usage{
			PromptTokens:     sess.PromptTokens,
			CompletionTokens: sess.CompletionTokens,
			CostUSD:          sess.Cost,
		},
	}

	pending, err := c.GetPendingPrompts(ctx, workspaceID, sess.ID)
	if err != nil {
		return st, err
	}
	info, err := c.GetAgentSessionInfo(ctx, workspaceID, sess.ID)
	if err != nil {
		return st, err
	}
	if queued, err := c.GetAgentSessionQueuedPrompts(ctx, workspaceID, sess.ID); err == nil {
		st.QueuedPrompts = queued
	}

	switch {
	case !pending.IsEmpty():
		st.State = StateBlocked
		st.Pending = &pending
	case info != nil && info.IsBusy:
		st.State = StateRunning
	default:
		last := LastOutcome(ctx, c, workspaceID, sess.ID)
		st.Last = &LastTurn{Status: last.Status, MessageID: last.MessageID, Text: last.Text, Error: last.Error}
	}
	return st, nil
}
