package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// headlessClient is the subset of the server client used by
// [runHeadless] and the `crush session` control commands. It exists so
// the run loop can be tested against a scripted fake instead of a live
// server.
type headlessClient interface {
	headless.WatchClient
	SubscribeEvents(ctx context.Context, workspaceID string) (<-chan any, error)
	SendAgentMessage(ctx context.Context, workspaceID string, msg proto.AgentMessage) error
	runInterrupter
}

// cancelTimeout bounds best-effort server calls made after the caller's
// context is already done (cancelling the run, enriching the result).
const cancelTimeout = 5 * time.Second

// runHeadless sends the prompt to sess and reports the run in the
// requested output format: a single [headless.Result] for json, an init
// event, progress events, and a final result for stream-json, or just
// the final text for text. It returns an error carrying the result's
// exit code when the run did not complete.
func runHeadless(
	ctx context.Context,
	c headlessClient,
	ws *proto.Workspace,
	sess *proto.Session,
	opts runOptions,
	out io.Writer,
) error {
	emitter := headless.NewNDJSONEmitter(out)
	var progress headless.Emitter
	if opts.format == headless.FormatStreamJSON {
		progress = emitter
	}

	runID := uuid.New().String()
	cwd := workspaceCwd(ws)
	baseline := headless.TakeBaseline(ctx, c, ws.ID, sess.ID)

	// Our prompt may be queued behind another turn (a TUI user's, or one
	// that starts between any busy check and our send), so output and
	// prompts are only attributed to us once our own user message shows
	// up. Completion is still matched by RunID.
	tracker := headless.NewTracker(sess.ID, runID, progress)
	tracker.AwaitUserPrompt(opts.prompt)

	events, err := c.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		return reportRunError(out, opts.format, err)
	}

	if progress != nil {
		if err := progress.Emit(initEvent(ws, sess, runID, cwd)); err != nil {
			return err
		}
	}

	msg := proto.AgentMessage{
		SessionID:        sess.ID,
		RunID:            runID,
		Prompt:           opts.prompt,
		PermissionPolicy: opts.mode.Policy(),
	}

	if opts.detach {
		if err := c.SendAgentMessage(ctx, ws.ID, msg); err != nil {
			return reportRunError(out, opts.format, fmt.Errorf("failed to send message: %w", err))
		}
		res := headless.NewResult(headless.StatusRunning)
		res.SessionID = sess.ID
		res.ShortID = session.ShortID(sess.ID)
		res.Address = headless.SessionAddress(*sess)
		res.WorkspaceID = ws.ID
		res.RunID = runID
		res.Cwd = cwd
		res.AttachCommand = headless.AttachCommand(cwd, sess.ID)
		return writeResult(out, opts.format, emitter, res)
	}

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- c.SendAgentMessage(ctx, ws.ID, msg)
	}()

	outcome := headless.Watch(ctx, c, headless.WatchRequest{
		WorkspaceID: ws.ID,
		SessionID:   sess.ID,
		Tracker:     tracker,
		Events:      events,
		SendErr:     sendErr,
		Timeout:     opts.timeout,
		StopOnBlock: opts.mode.Delegates(),
		OnBlocked: func(p proto.PendingPrompts) {
			if progress != nil {
				_ = progress.Emit(headless.BlockedEvent{Type: headless.EventBlocked, SessionID: sess.ID, Pending: p})
			}
		},
	})

	switch {
	case outcome.Interrupted:
		// Ctrl+C on a foreground run means "stop", not "stop watching".
		interruptRun(c, ws.ID, sess.ID, runID, tracker.Started())
	case opts.isolated && outcome.Status == headless.StatusTimeout:
		// Nothing can reach a private workspace after we exit, so
		// leaving the run going would only burn tokens.
		interruptRun(c, ws.ID, sess.ID, runID, tracker.Started())
		outcome.Error += "; the run was cancelled because it was isolated"
	}

	enrichCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	res := headless.Finish(enrichCtx, c, baseline, ws.ID, sess.ID, cwd, runID, outcome)
	return writeResult(out, opts.format, emitter, res)
}

func initEvent(ws *proto.Workspace, sess *proto.Session, runID, cwd string) headless.InitEvent {
	init := headless.InitEvent{
		Type:          headless.EventInit,
		SessionID:     sess.ID,
		ShortID:       session.ShortID(sess.ID),
		Address:       headless.SessionAddress(*sess),
		WorkspaceID:   ws.ID,
		RunID:         runID,
		Cwd:           cwd,
		AttachCommand: headless.AttachCommand(cwd, sess.ID),
	}
	if ws.Config != nil {
		if m, ok := ws.Config.Models[config.SelectedModelTypeLarge]; ok {
			init.Provider = m.Provider
			init.Model = m.Model
		}
	}
	if sess.ModelRef != "" {
		init.Provider, init.Model = "", sess.ModelRef
		if provider, model, ok := strings.Cut(sess.ModelRef, "/"); ok {
			init.Provider, init.Model = provider, model
		}
	}
	return init
}

// writeResult reports res in the requested format and returns the
// matching exit error. In text mode the final assistant text goes to
// stdout and anything other than completion is reported as an error so
// the CLI prints it to stderr.
func writeResult(out io.Writer, format headless.OutputFormat, emitter headless.Emitter, res headless.Result) error {
	if format.IsStructured() {
		if err := emitter.Emit(res); err != nil {
			return err
		}
		return res.Err()
	}

	if text := strings.TrimSpace(res.Text); text != "" {
		_, _ = fmt.Fprintln(out, text)
	}
	switch res.Status {
	case headless.StatusCompleted:
		return nil
	case headless.StatusRunning:
		_, _ = fmt.Fprintln(out, res.SessionID)
		return nil
	}
	msg := res.Error
	if msg == "" {
		msg = string(res.Status)
	}
	if res.Status == headless.StatusBlocked || res.Status == headless.StatusTimeout {
		msg += fmt.Sprintf(" (see 'crush session status %s')", res.SessionID)
	}
	return headless.NewExitError(res.ExitCode, errors.New(msg))
}

// sessionCanceller cancels an in-flight run on the server.
type sessionCanceller interface {
	CancelAgentSession(ctx context.Context, workspaceID, sessionID string) error
}

// runInterrupter stops one run: drops it if still queued, else cancels
// the session's active turn.
type runInterrupter interface {
	sessionCanceller
	DropQueuedRun(ctx context.Context, workspaceID, sessionID, runID string) (bool, error)
}

// interruptRun stops the caller's own run after it gave up (Ctrl+C,
// SIGTERM, or an isolated timeout). A prompt still queued behind another
// turn is dropped from the queue; the session's active turn is cancelled
// only when it is ours (started), so giving up never cancels a turn
// someone else is running on a shared session.
func interruptRun(c runInterrupter, workspaceID, sessionID, runID string, started bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	dropped, err := c.DropQueuedRun(ctx, workspaceID, sessionID, runID)
	if err != nil {
		slog.Warn("Failed to drop queued run", "session_id", sessionID, "error", err)
	}
	if dropped || !started {
		return
	}
	cancelServerRun(c, workspaceID, sessionID)
}

// cancelServerRun asks the server to cancel the session's active turn.
func cancelServerRun(c sessionCanceller, workspaceID, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	if err := c.CancelAgentSession(ctx, workspaceID, sessionID); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("Failed to cancel interrupted run", "session_id", sessionID, "error", err)
	}
}

// workspaceCwd returns the directory the workspace's tools run in.
func workspaceCwd(ws *proto.Workspace) string {
	if ws.WorkingDir != "" {
		return ws.WorkingDir
	}
	return ws.Path
}
