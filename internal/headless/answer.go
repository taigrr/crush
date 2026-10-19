package headless

import (
	"context"
	"errors"
	"fmt"

	"github.com/taigrr/crush/internal/proto"
)

// AnswerClient is the subset of the server client needed to resolve a
// pending permission request or question.
type AnswerClient interface {
	GetPendingPrompts(ctx context.Context, workspaceID, sessionID string) (proto.PendingPrompts, error)
	GrantPermission(ctx context.Context, workspaceID string, req proto.PermissionGrant) (bool, error)
	AnswerQuestion(ctx context.Context, workspaceID string, req proto.QuestionAnswer) (bool, error)
}

// AnswerRequest describes how to resolve one pending prompt. Exactly one
// of Permission, Answers, or CancelQuestion must be set.
type AnswerRequest struct {
	// RequestID selects the pending permission request or question. It
	// may be omitted when exactly one prompt of the matching kind is
	// pending.
	RequestID string
	// Permission resolves a permission request.
	Permission proto.PermissionAction
	// Answers answers a question: the chosen option(s), or the free-text
	// reply as a single element. Yes/no questions take "yes" or "no".
	Answers []string
	// CancelQuestion dismisses a question without answering it.
	CancelQuestion bool
}

// AnswerKind says what kind of prompt an answer resolved.
type AnswerKind string

const (
	AnswerKindPermission AnswerKind = "permission"
	AnswerKindQuestion   AnswerKind = "question"
)

// EventAnswer is the type of an [AnswerResult].
const EventAnswer EventType = "answer"

// AnswerResult reports the outcome of [Answer].
type AnswerResult struct {
	Type      EventType  `json:"type"`
	Kind      AnswerKind `json:"kind"`
	RequestID string     `json:"request_id"`
	SessionID string     `json:"session_id"`
	// Resolved is false when someone else (another client) answered the
	// prompt first; that is not an error.
	Resolved bool `json:"resolved"`
}

// ErrNothingPending is returned by [Answer] when the session has no
// prompt of the requested kind.
var ErrNothingPending = errors.New("nothing is pending")

// Answer resolves a pending permission request or question raised by
// sessionID or one of its sub-agent sessions.
func Answer(ctx context.Context, c AnswerClient, workspaceID, sessionID string, req AnswerRequest) (AnswerResult, error) {
	set := 0
	if req.Permission != "" {
		set++
	}
	if len(req.Answers) > 0 {
		set++
	}
	if req.CancelQuestion {
		set++
	}
	if set != 1 {
		return AnswerResult{}, errors.New("specify exactly one of a permission decision, an answer, or a question cancellation")
	}
	switch req.Permission {
	case "", proto.PermissionAllow, proto.PermissionAllowForSession, proto.PermissionDeny:
	default:
		return AnswerResult{}, fmt.Errorf("invalid permission decision %q", req.Permission)
	}

	pending, err := c.GetPendingPrompts(ctx, workspaceID, sessionID)
	if err != nil {
		return AnswerResult{}, err
	}

	if req.Permission != "" {
		perm, err := pick(pending.Permissions, req.RequestID, "permission request", func(p proto.PermissionRequest) string { return p.ID })
		if err != nil {
			return AnswerResult{}, err
		}
		resolved, err := c.GrantPermission(ctx, workspaceID, proto.PermissionGrant{Permission: perm, Action: req.Permission})
		if err != nil {
			return AnswerResult{}, err
		}
		return AnswerResult{Type: EventAnswer, Kind: AnswerKindPermission, RequestID: perm.ID, SessionID: perm.SessionID, Resolved: resolved}, nil
	}

	q, err := pick(pending.Questions, req.RequestID, "question", func(q proto.QuestionRequest) string { return q.ID })
	if err != nil {
		return AnswerResult{}, err
	}
	resolved, err := c.AnswerQuestion(ctx, workspaceID, proto.QuestionAnswer{
		ID:         q.ID,
		SessionID:  q.SessionID,
		ToolCallID: q.ToolCallID,
		Selected:   req.Answers,
		Cancelled:  req.CancelQuestion,
	})
	if err != nil {
		return AnswerResult{}, err
	}
	return AnswerResult{Type: EventAnswer, Kind: AnswerKindQuestion, RequestID: q.ID, SessionID: q.SessionID, Resolved: resolved}, nil
}

func pick[T any](items []T, id, kind string, idOf func(T) string) (T, error) {
	var zero T
	if id != "" {
		for _, it := range items {
			if idOf(it) == id {
				return it, nil
			}
		}
		return zero, fmt.Errorf("%w: no pending %s with ID %q", ErrNothingPending, kind, id)
	}
	switch len(items) {
	case 0:
		return zero, fmt.Errorf("%w: no %s is pending", ErrNothingPending, kind)
	case 1:
		return items[0], nil
	default:
		return zero, fmt.Errorf("%d %ss are pending; pass the request ID to choose one", len(items), kind)
	}
}
