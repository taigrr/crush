package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
)

// structuredRunClient is the subset of the server client used by
// [runStructured]. It exists so the structured run loop can be tested
// against a scripted fake instead of a live server.
type structuredRunClient interface {
	headless.ReportClient
	SubscribeEvents(ctx context.Context, workspaceID string) (<-chan any, error)
	SendMessage(ctx context.Context, workspaceID, sessionID, runID, prompt string, attachments ...message.Attachment) error
	GetAgentSessionInfo(ctx context.Context, workspaceID, sessionID string) (*proto.AgentSession, error)
	CancelAgentSession(ctx context.Context, workspaceID, sessionID string) error
}

// cancelTimeout bounds best-effort server calls made after the caller's
// context is already done (cancelling the run, enriching the result).
const cancelTimeout = 5 * time.Second

// runStructured sends prompt to sess and reports the run in a JSON
// output format: a single [headless.Result] for json, or an init event,
// progress events, and a final result for stream-json. It returns an
// error carrying the result's exit code when the run did not complete.
func runStructured(
	ctx context.Context,
	c structuredRunClient,
	ws *proto.Workspace,
	sess *proto.Session,
	prompt string,
	format headless.OutputFormat,
	out io.Writer,
) error {
	emitter := headless.NewNDJSONEmitter(out)
	var progress headless.Emitter
	if format == headless.FormatStreamJSON {
		progress = emitter
	}

	runID := uuid.New().String()
	cwd := workspaceCwd(ws)
	baseline := headless.TakeBaseline(ctx, c, ws.ID, sess.ID)

	tracker := headless.NewTracker(sess.ID, runID, progress)
	if info, err := c.GetAgentSessionInfo(ctx, ws.ID, sess.ID); err == nil && info != nil && info.IsBusy {
		tracker.AwaitUserPrompt(prompt)
	}

	events, err := c.SubscribeEvents(ctx, ws.ID)
	if err != nil {
		return reportRunError(out, format, err)
	}

	if progress != nil {
		init := headless.InitEvent{
			Type:          headless.EventInit,
			SessionID:     sess.ID,
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
		if err := progress.Emit(init); err != nil {
			return err
		}
	}

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- c.SendMessage(ctx, ws.ID, sess.ID, runID, prompt)
	}()

	outcome, exitCode := waitForOutcome(ctx, c, ws.ID, sess.ID, tracker, events, sendErr)

	res := headless.NewResult(outcome.Status)
	if exitCode != 0 {
		res.ExitCode = exitCode
	}
	res.RunID = runID
	res.MessageID = outcome.MessageID
	res.Text = outcome.Text
	res.Error = outcome.Error
	res.Cwd = cwd
	res.AttachCommand = headless.AttachCommand(cwd, sess.ID)

	enrichCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	baseline.Enrich(enrichCtx, c, ws.ID, sess.ID, &res)

	if err := emitter.Emit(res); err != nil {
		return err
	}
	return res.Err()
}

// waitForOutcome drives the tracker until the run ends, the caller's
// context is cancelled, or the request fails. The returned exit code
// overrides the status-derived one when non-zero.
func waitForOutcome(
	ctx context.Context,
	c structuredRunClient,
	workspaceID, sessionID string,
	tracker *headless.Tracker,
	events <-chan any,
	sendErr <-chan error,
) (headless.Outcome, int) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return headless.Outcome{
					Status: headless.StatusError,
					Error:  "event stream closed before the run finished",
				}, headless.ExitFailure
			}
			outcome, err := tracker.Handle(ev)
			if err != nil {
				return headless.Outcome{Status: headless.StatusError, Error: err.Error()}, headless.ExitFailure
			}
			if outcome != nil {
				return *outcome, 0
			}

		case <-ctx.Done():
			cancelServerRun(c, workspaceID, sessionID)
			return headless.Outcome{Status: headless.StatusCancelled, Error: "interrupted"}, 0

		case err := <-sendErr:
			if err != nil {
				return headless.Outcome{
					Status: headless.StatusError,
					Error:  "failed to send message: " + err.Error(),
				}, headless.ExitFailure
			}
			sendErr = nil
		}
	}
}

// sessionCanceller cancels an in-flight run on the server.
type sessionCanceller interface {
	CancelAgentSession(ctx context.Context, workspaceID, sessionID string) error
}

// cancelServerRun asks the server to stop the session's run after the
// local caller gave up (Ctrl+C, SIGTERM). The run lives in a workspace
// no other client can reach, so leaving it running would only burn
// tokens in the background.
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
