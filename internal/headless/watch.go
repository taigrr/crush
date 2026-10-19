package headless

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// WatchClient is the subset of the server client needed to follow a
// session until its run ends or blocks.
type WatchClient interface {
	ReportClient
	GetAgentSessionInfo(ctx context.Context, workspaceID, sessionID string) (*proto.AgentSession, error)
	GetPendingPrompts(ctx context.Context, workspaceID, sessionID string) (proto.PendingPrompts, error)
	ListMessages(ctx context.Context, workspaceID, sessionID string) ([]proto.Message, error)
}

// WatchRequest configures [Watch].
type WatchRequest struct {
	WorkspaceID string
	SessionID   string
	// Tracker interprets the event stream. Its RunID decides whether a
	// specific run (non-empty) or the session as a whole (empty) is
	// followed. In the latter case Watch keeps waiting across queued
	// follow-up turns until the session is idle.
	Tracker *Tracker
	// Events is the workspace event stream, subscribed before the
	// prompt was sent so no event is missed.
	Events <-chan any
	// SendErr, when non-nil, delivers the result of the send call.
	SendErr <-chan error
	// Timeout stops waiting after the given duration (0 = no limit).
	Timeout time.Duration
	// StopOnBlock returns [StatusBlocked] as soon as the session waits
	// on a permission decision or question.
	StopOnBlock bool
	// CheckIdle inspects the session before waiting and returns its last
	// outcome right away when nothing is running. Used when attaching to
	// a session that may already be done.
	CheckIdle bool
	// OnBlocked, when set, is called with the pending prompts before a
	// blocked outcome is returned.
	OnBlocked func(proto.PendingPrompts)
}

// Watch follows a session until its run ends, blocks (when requested),
// the timeout elapses, the request fails, or ctx is done.
func Watch(ctx context.Context, c WatchClient, req WatchRequest) Outcome {
	blocked := func() (Outcome, bool) {
		// Until our own turn starts, a prompt on the session belongs to
		// the turn ahead of us (for example a TUI user's), not to us.
		if !req.StopOnBlock || !req.Tracker.Started() {
			return Outcome{}, false
		}
		pending, err := c.GetPendingPrompts(ctx, req.WorkspaceID, req.SessionID)
		if err != nil {
			slog.Debug("Failed to read pending prompts", "session_id", req.SessionID, "error", err)
			return Outcome{}, false
		}
		if pending.IsEmpty() {
			return Outcome{}, false
		}
		if req.OnBlocked != nil {
			req.OnBlocked(pending)
		}
		return Outcome{Status: StatusBlocked, Pending: &pending, Error: blockedReason(pending)}, true
	}
	busy := func() bool {
		info, err := c.GetAgentSessionInfo(ctx, req.WorkspaceID, req.SessionID)
		return err == nil && info != nil && info.IsBusy
	}

	if req.CheckIdle {
		if out, ok := blocked(); ok {
			return out
		}
		if !busy() {
			return LastOutcome(ctx, c, req.WorkspaceID, req.SessionID)
		}
	}

	var deadline <-chan time.Time
	if req.Timeout > 0 {
		timer := time.NewTimer(req.Timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	followSession := req.Tracker.runID == ""
	sendErr := req.SendErr

	// When following a session, a turn can end while the session still
	// reports busy for a moment (the terminal event is published just
	// before the run is released) or because a queued follow-up starts.
	// No further event is guaranteed in the first case, so the last
	// outcome is held and busy state is polled until it settles.
	var (
		held     *Outcome
		pollTick <-chan time.Time
	)
	poll := time.NewTicker(idlePollInterval)
	defer poll.Stop()

	for {
		select {
		case <-pollTick:
			if !busy() {
				return reconcileDenial(ctx, c, req, *held)
			}

		case ev, ok := <-req.Events:
			if !ok {
				return Outcome{
					Status:   StatusError,
					Error:    "event stream closed before the run finished",
					ExitCode: ExitFailure,
				}
			}
			switch ev.(type) {
			case pubsub.Event[proto.PermissionRequest], pubsub.Event[proto.QuestionRequest]:
				if out, ok := blocked(); ok {
					return out
				}
				continue
			}
			out, err := req.Tracker.Handle(ev)
			if err != nil {
				return Outcome{Status: StatusError, Error: err.Error(), ExitCode: ExitFailure}
			}
			if out == nil {
				continue
			}
			if followSession && busy() {
				held, pollTick = out, poll.C
				continue
			}
			return reconcileDenial(ctx, c, req, *out)

		case <-deadline:
			return Outcome{Status: StatusTimeout, Error: "timed out waiting for the run to finish"}

		case <-ctx.Done():
			return Outcome{Status: StatusCancelled, Error: "interrupted", Interrupted: true}

		case err := <-sendErr:
			if err != nil {
				return Outcome{
					Status:   StatusError,
					Error:    "failed to send message: " + err.Error(),
					ExitCode: ExitFailure,
				}
			}
			sendErr = nil
		}
	}
}

// idlePollInterval is how often [Watch] re-checks a session that ended a
// turn but still reports busy.
const idlePollInterval = 200 * time.Millisecond

// reconcileDenial double-checks a completed outcome against the session
// history. The denial notification that marks a run as denied can be
// missed when the watcher subscribed after it was sent, but the denied
// tool result is always persisted.
func reconcileDenial(ctx context.Context, c WatchClient, req WatchRequest, out Outcome) Outcome {
	if out.Status != StatusCompleted {
		return out
	}
	last := LastOutcome(ctx, c, req.WorkspaceID, req.SessionID)
	if last.Status != StatusPermissionDenied {
		return out
	}
	if out.MessageID != "" && last.MessageID != out.MessageID {
		return out
	}
	out.Status = StatusPermissionDenied
	out.Error = last.Error
	return out
}

// LastOutcome derives the outcome of the session's most recent turn from
// its message history. It is used when a run already ended before the
// caller started watching.
func LastOutcome(ctx context.Context, c WatchClient, workspaceID, sessionID string) Outcome {
	msgs, err := c.ListMessages(ctx, workspaceID, sessionID)
	if err != nil {
		return Outcome{Status: StatusError, Error: "failed to read session history: " + err.Error(), ExitCode: ExitFailure}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != proto.Assistant {
			continue
		}
		out := Outcome{Status: StatusCompleted, MessageID: m.ID, Text: m.Content().String()}
		if f := m.FinishPart(); f != nil {
			switch f.Reason {
			case proto.FinishReasonCanceled:
				out.Status = StatusCancelled
				out.Error = "cancelled"
			case proto.FinishReasonError:
				out.Status = StatusError
				out.Error = f.Message
				if out.Error == "" {
					out.Error = "agent run failed"
				}
			}
		}
		if out.Status == StatusCompleted && endedOnDenial(msgs[i+1:]) {
			out.Status = StatusPermissionDenied
			out.Error = "permission denied"
		}
		return out
	}
	return Outcome{Status: StatusCompleted}
}

// endedOnDenial reports whether the tool results that follow the last
// assistant message include a permission denial, which is how a denied
// tool call ends a turn.
func endedOnDenial(after []proto.Message) bool {
	for _, m := range after {
		if m.Role != proto.Tool {
			continue
		}
		for _, tr := range m.ToolResults() {
			if tr.IsError && strings.Contains(tr.Content, tools.PermissionDeniedMessage) {
				return true
			}
		}
	}
	return false
}

func blockedReason(p proto.PendingPrompts) string {
	switch {
	case len(p.Permissions) > 0:
		return "waiting for permission: " + p.Permissions[0].Description
	case len(p.Questions) > 0:
		return "waiting for an answer: " + p.Questions[0].Prompt
	default:
		return "blocked"
	}
}

// Finish converts an outcome into the final [Result], enriching it with
// usage, files changed, and diagnostics.
func Finish(ctx context.Context, c ReportClient, b Baseline, workspaceID, sessionID, cwd, runID string, out Outcome) Result {
	res := NewResult(out.Status)
	if out.ExitCode != 0 {
		res.ExitCode = out.ExitCode
	}
	res.RunID = runID
	res.MessageID = out.MessageID
	res.Text = out.Text
	res.Error = out.Error
	res.Cwd = cwd
	res.Pending = out.Pending
	res.AttachCommand = AttachCommand(cwd, sessionID)
	b.Enrich(ctx, c, workspaceID, sessionID, &res)
	return res
}

// EventClient is a [WatchClient] that can also subscribe to the
// workspace event stream.
type EventClient interface {
	WatchClient
	SubscribeEvents(ctx context.Context, workspaceID string) (<-chan any, error)
}

// WaitSession waits for whatever is running on sessionID to end or
// block, without having started it. A session that is already idle
// reports its last turn immediately. Interrupting the wait (ctx done)
// never cancels the run itself.
func WaitSession(ctx context.Context, c EventClient, workspaceID, sessionID string, timeout time.Duration, progress Emitter) Outcome {
	events, err := c.SubscribeEvents(ctx, workspaceID)
	if err != nil {
		return Outcome{Status: StatusError, Error: "failed to subscribe to events: " + err.Error(), ExitCode: ExitFailure}
	}
	out := Watch(ctx, c, WatchRequest{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		Tracker:     NewTracker(sessionID, "", progress),
		Events:      events,
		Timeout:     timeout,
		StopOnBlock: true,
		CheckIdle:   true,
		OnBlocked: func(p proto.PendingPrompts) {
			if progress != nil {
				_ = progress.Emit(BlockedEvent{Type: EventBlocked, SessionID: sessionID, Pending: p})
			}
		},
	})
	if out.Interrupted {
		return Outcome{Status: StatusTimeout, Error: "stopped waiting; the run is still going"}
	}
	return out
}
