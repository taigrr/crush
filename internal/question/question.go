package question

import (
	"context"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/pubsub"
)

// Kind discriminates the shape of a question and, therefore, how the
// UI should render it and how the answer should be interpreted.
type Kind string

const (
	// KindSingleChoice presents Options and expects exactly one
	// selected option back.
	KindSingleChoice Kind = "single_choice"
	// KindMultipleChoice presents Options and expects zero or more
	// selected options back.
	KindMultipleChoice Kind = "multiple_choice"
	// KindFreeText expects an arbitrary text answer. Options is unused.
	KindFreeText Kind = "free_text"
	// KindYesNo expects a boolean answer, surfaced as "yes"/"no" on
	// the wire. Options is unused.
	KindYesNo Kind = "yes_no"
)

// CreateQuestionRequest is the input to [Service.Ask].
type CreateQuestionRequest struct {
	SessionID  string
	ToolCallID string
	Kind       Kind
	// Prompt is the question text shown to the user.
	Prompt string
	// Options lists the choices for KindSingleChoice and
	// KindMultipleChoice. Ignored for other kinds.
	Options []string
}

// Request is a pending question awaiting an answer from a client.
type Request struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"session_id"`
	ToolCallID string   `json:"tool_call_id"`
	Kind       Kind     `json:"kind"`
	Prompt     string   `json:"prompt"`
	Options    []string `json:"options,omitempty"`
}

// Answer is the response to a [Request], submitted by a client via
// [Service.Answer]. Selected holds the chosen option(s) for choice
// kinds, the free-text response body for KindFreeText, or "yes"/"no"
// for KindYesNo. Cancelled is true when the user dismissed the dialog
// without answering (e.g. pressed Escape); Selected is ignored in that
// case.
type Answer struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"session_id"`
	ToolCallID string   `json:"tool_call_id"`
	Selected   []string `json:"selected,omitempty"`
	Cancelled  bool     `json:"cancelled,omitempty"`
}

// Notification reports the resolution of a pending question so
// non-answering subscribers (e.g. other clients sharing the
// workspace) can dismiss a now-stale dialog.
type Notification struct {
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id"`
	Answered   bool   `json:"answered"`
	Cancelled  bool   `json:"cancelled"`
}

// Service asks the user structured questions and waits for an answer.
// It mirrors permission.Service's Request/Grant/Deny shape: Ask blocks
// the calling goroutine (an agent tool) until Answer resolves the
// pending request, ctx is cancelled, or the request is externally
// cancelled.
type Service interface {
	pubsub.Subscriber[Request]
	// Ask publishes a question request and blocks until a client
	// answers it (via Answer), ctx is cancelled, or the caller aborts.
	// Callers are responsible for checking whether an interactive
	// client is actually attached before calling Ask; Ask itself has
	// no way to detect that and will block indefinitely if nothing
	// ever answers.
	Ask(ctx context.Context, opts CreateQuestionRequest) (Answer, error)
	// Answer resolves a pending question. It returns true if this call
	// resolved the pending request, false if it was already resolved
	// or is unknown (not an error: multiple clients may race to answer
	// the same question).
	Answer(answer Answer) bool
	// CancelAll resolves every still-pending question as cancelled,
	// publishing a resolution notification for each. Called on workspace
	// teardown so no agent goroutine is left blocked in Ask and no
	// client is left showing a zombie question dialog.
	CancelAll()
	// RepublishPending re-emits the request event for every still-pending
	// question in the given session so a client that just switched to (or
	// re-attached to) the workspace surfaces the prompt (switch-to-grant).
	RepublishPending(sessionID string)
	// PendingSessions lists the sessions that currently have a question
	// blocked waiting for a client to answer it.
	PendingSessions() []string
	SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[Notification]
}

type questionService struct {
	*pubsub.Broker[Request]

	notificationBroker *pubsub.Broker[Notification]
	// pendingRequests maps a request ID to its in-flight state. The
	// full Request is stored (not just the answer channel) so
	// resolution notifications are always built from the trusted,
	// server-minted request rather than client-supplied fields — a
	// client cannot route a resolution to a different session by
	// answering with a valid ID but the wrong SessionID/ToolCallID.
	pendingRequests *csync.Map[string, pendingQuestion]
}

// pendingQuestion is the in-flight state for a published question.
type pendingQuestion struct {
	req    Request
	respCh chan Answer
}

// NewQuestionService creates a new question [Service].
func NewQuestionService() Service {
	return &questionService{
		Broker:             pubsub.NewBroker[Request](),
		notificationBroker: pubsub.NewBroker[Notification](),
		pendingRequests:    csync.NewMap[string, pendingQuestion](),
	}
}

func (s *questionService) Ask(ctx context.Context, opts CreateQuestionRequest) (Answer, error) {
	req := Request{
		ID:         uuid.New().String(),
		SessionID:  opts.SessionID,
		ToolCallID: opts.ToolCallID,
		Kind:       opts.Kind,
		Prompt:     opts.Prompt,
		Options:    opts.Options,
	}

	// Register the pending request and publish WITHOUT holding any
	// lock across the blocking select. The service is per-workspace
	// but a workspace runs multiple sessions concurrently; holding a
	// service-wide lock across the wait (as an earlier version did)
	// would wedge every other session's question behind this one and
	// prevent their requests from even publishing. Mirrors
	// permission.Service.Request, which blocks with no lock held so
	// concurrent per-session prompts are independent.
	respCh := make(chan Answer, 1)
	s.pendingRequests.Set(req.ID, pendingQuestion{req: req, respCh: respCh})
	defer s.pendingRequests.Del(req.ID)

	s.Publish(pubsub.CreatedEvent, req)

	select {
	case <-ctx.Done():
		// The run was cancelled while the question was still pending.
		// Resolve it as cancelled so any open dialog on clients viewing
		// this session is dismissed rather than left hanging open.
		s.resolve(req.ID, nil, true)
		return Answer{Cancelled: true}, ctx.Err()
	case answer := <-respCh:
		return answer, nil
	}
}

// resolve atomically removes the pending entry for id and, if it was
// still pending, publishes exactly one [Notification] built from the
// STORED request (never client input) and forwards the answer to the
// waiter. It returns true if this call resolved the request.
func (s *questionService) resolve(id string, selected []string, cancelled bool) bool {
	p, ok := s.pendingRequests.Take(id)
	if !ok {
		return false
	}

	s.notificationBroker.Publish(pubsub.CreatedEvent, Notification{
		SessionID:  p.req.SessionID,
		ToolCallID: p.req.ToolCallID,
		Answered:   !cancelled,
		Cancelled:  cancelled,
	})

	// respCh is buffered (cap 1) with at most one sender per request
	// because Take removes the entry under the map lock, so this send
	// never blocks. The forwarded answer carries the trusted request's
	// routing fields, not the caller's.
	p.respCh <- Answer{
		ID:         p.req.ID,
		SessionID:  p.req.SessionID,
		ToolCallID: p.req.ToolCallID,
		Selected:   selected,
		Cancelled:  cancelled,
	}
	return true
}

func (s *questionService) Answer(answer Answer) bool {
	// Validate routing against the stored request before resolving so a
	// client cannot dismiss another session's question by supplying a
	// valid ID with mismatched SessionID/ToolCallID. Empty fields are
	// treated as "not asserted" and skipped.
	p, ok := s.pendingRequests.Get(answer.ID)
	if !ok {
		return false
	}
	if answer.SessionID != "" && answer.SessionID != p.req.SessionID {
		return false
	}
	if answer.ToolCallID != "" && answer.ToolCallID != p.req.ToolCallID {
		return false
	}
	return s.resolve(answer.ID, answer.Selected, answer.Cancelled)
}

func (s *questionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[Notification] {
	return s.notificationBroker.Subscribe(ctx)
}

// CancelAll resolves every still-pending question as cancelled. Each
// entry is resolved through the same atomic Take path as Answer, so a
// concurrent real answer races safely (first wins). Used on workspace
// teardown to unblock waiting agent goroutines and clear zombie dialogs.
func (s *questionService) CancelAll() {
	for id := range s.pendingRequests.Seq2() {
		s.resolve(id, nil, true)
	}
}

// PendingSessions lists the sessions with a still-pending question.
func (s *questionService) PendingSessions() []string {
	seen := make(map[string]struct{})
	for _, p := range s.pendingRequests.Seq2() {
		seen[p.req.SessionID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// RepublishPending re-emits the request event for every still-pending
// question in the given session so a newly-subscribed client surfaces it.
func (s *questionService) RepublishPending(sessionID string) {
	for _, p := range s.pendingRequests.Seq2() {
		if p.req.SessionID == sessionID {
			s.Publish(pubsub.CreatedEvent, p.req)
		}
	}
}
