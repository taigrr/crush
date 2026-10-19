package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/proto"
)

var sessionStatusCmd = &cobra.Command{
	Use:   "status <session>",
	Short: "Show whether a session is running, blocked, or idle",
	Long: `Show whether a session is running, blocked on a permission request or
question, or idle, plus its last result. The session can be given as an ID,
a hash prefix from 'crush session list', or a swarm address.`,
	Example: `
crush session status 7f0c3b9e -o json
crush session status coral-otter-3b9e`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := headlessFormatFlag(cmd)
		if err != nil {
			return err
		}
		return reportRunError(os.Stdout, format, withHeadlessSession(cmd, args[0], func(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session) error {
			return sessionStatus(ctx, c, ws, sess, format, os.Stdout)
		}))
	},
}

var sessionWaitCmd = &cobra.Command{
	Use:   "wait <session>",
	Short: "Wait for a session's run to finish or block",
	Long: `Wait until the session's current run finishes, or until it blocks on a
permission request or question, then print the result. A session that is
already idle reports its last turn right away. Interrupting the wait does not
cancel the run.

Usage and files changed are totals for the whole session.`,
	Example: `
crush session wait 7f0c3b9e --timeout 9m -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := headlessFormatFlag(cmd)
		if err != nil {
			return err
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		return reportRunError(os.Stdout, format, withHeadlessSession(cmd, args[0], func(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session) error {
			return sessionWait(ctx, c, ws, sess, timeout, format, os.Stdout)
		}))
	},
}

var sessionAnswerCmd = &cobra.Command{
	Use:   "answer <session>",
	Short: "Answer a session's pending permission request or question",
	Long: `Resolve what a blocked session is waiting on. Use --allow, --allow-session,
or --deny for a permission request, and --answer (repeatable for multiple
choice) or --cancel-question for a question. When more than one prompt is
pending, pick one with --request (IDs are listed by 'crush session status').

With --wait, wait for the run to finish or block again and print its result.`,
	Example: `
crush session answer 7f0c3b9e --allow --wait -o json
crush session answer 7f0c3b9e --answer "Use PostgreSQL"
crush session answer 7f0c3b9e --deny --request 3d1c...`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := headlessFormatFlag(cmd)
		if err != nil {
			return err
		}
		req, err := answerRequestFromFlags(cmd)
		if err != nil {
			return reportRunError(os.Stdout, format, err)
		}
		wait, _ := cmd.Flags().GetBool("wait")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		return reportRunError(os.Stdout, format, withHeadlessSession(cmd, args[0], func(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session) error {
			return sessionAnswer(ctx, c, ws, sess, req, wait, timeout, format, os.Stdout)
		}))
	},
}

var sessionCancelCmd = &cobra.Command{
	Use:   "cancel <session>",
	Short: "Cancel a session's running turn",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := headlessFormatFlag(cmd)
		if err != nil {
			return err
		}
		return reportRunError(os.Stdout, format, withHeadlessSession(cmd, args[0], func(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session) error {
			return sessionCancel(ctx, c, ws, sess, format, os.Stdout)
		}))
	},
}

func init() {
	for _, c := range []*cobra.Command{sessionStatusCmd, sessionWaitCmd, sessionAnswerCmd, sessionCancelCmd} {
		c.Flags().StringP("output-format", "o", string(headless.FormatText), "Output format: text, json, or stream-json")
		sessionCmd.AddCommand(c)
	}
	sessionWaitCmd.Flags().Duration("timeout", 0, "Stop waiting after this long (e.g. 9m); exits with code 124")

	addAnswerFlags(sessionAnswerCmd)
}

func addAnswerFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("allow", false, "Allow the pending permission request once")
	cmd.Flags().Bool("allow-session", false, "Allow the request and similar ones for the rest of the session")
	cmd.Flags().Bool("deny", false, "Deny the pending permission request (ends the agent's turn)")
	cmd.Flags().StringArray("answer", nil, "Answer the pending question (repeat for multiple choice)")
	cmd.Flags().Bool("cancel-question", false, "Dismiss the pending question without answering")
	cmd.Flags().String("request", "", "ID of the permission request or question to resolve")
	cmd.Flags().Bool("wait", false, "After answering, wait for the run to finish or block again")
	cmd.Flags().Duration("timeout", 0, "With --wait, stop waiting after this long")
	cmd.MarkFlagsMutuallyExclusive("allow", "allow-session", "deny", "answer", "cancel-question")
}

func headlessFormatFlag(cmd *cobra.Command) (headless.OutputFormat, error) {
	f, _ := cmd.Flags().GetString("output-format")
	return headless.ParseOutputFormat(f)
}

func answerRequestFromFlags(cmd *cobra.Command) (headless.AnswerRequest, error) {
	var (
		allow, _        = cmd.Flags().GetBool("allow")
		allowSession, _ = cmd.Flags().GetBool("allow-session")
		deny, _         = cmd.Flags().GetBool("deny")
		answers, _      = cmd.Flags().GetStringArray("answer")
		cancelQ, _      = cmd.Flags().GetBool("cancel-question")
		requestID, _    = cmd.Flags().GetString("request")
	)
	req := headless.AnswerRequest{RequestID: requestID, Answers: answers, CancelQuestion: cancelQ}
	switch {
	case allow:
		req.Permission = proto.PermissionAllow
	case allowSession:
		req.Permission = proto.PermissionAllowForSession
	case deny:
		req.Permission = proto.PermissionDeny
	}
	if req.Permission == "" && len(req.Answers) == 0 && !req.CancelQuestion {
		return req, errors.New("specify one of --allow, --allow-session, --deny, --answer, or --cancel-question")
	}
	return req, nil
}

// withHeadlessSession connects to the server for the current directory,
// resolves the session reference, and calls fn.
func withHeadlessSession(cmd *cobra.Command, ref string, fn func(context.Context, *client.Client, *proto.Workspace, *proto.Session) error) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	c, ws, cleanup, err := connectToServer(cmd)
	if err != nil {
		return err
	}
	defer cleanup()

	sess, elsewhere, err := headless.LocateSession(ctx, c, ws.ID, ref)
	if err != nil {
		return err
	}
	if elsewhere == nil {
		return fn(ctx, c, ws, sess)
	}

	oc, ows, ocleanup, err := connectToServerAt(cmd, elsewhere.Path)
	if err != nil {
		return err
	}
	defer ocleanup()
	sess, err = oc.GetSession(ctx, ows.ID, elsewhere.SessionID)
	if err != nil {
		return err
	}
	return fn(ctx, oc, ows, sess)
}

// resolveSessionRef resolves a session from a full ID, a hash prefix as
// shown by `crush session list`, or a swarm address.
func resolveSessionRef(ctx context.Context, c headless.SessionLister, workspaceID, ref string) (*proto.Session, error) {
	return headless.ResolveSession(ctx, c, workspaceID, ref)
}

func sessionStatus(ctx context.Context, c headless.StatusClient, ws *proto.Workspace, sess *proto.Session, format headless.OutputFormat, out io.Writer) error {
	st, err := headless.BuildStatus(ctx, c, ws.ID, workspaceCwd(ws), *sess)
	if err != nil {
		return err
	}
	if format.IsStructured() {
		return headless.NewNDJSONEmitter(out).Emit(st)
	}

	name := st.Address
	if name == "" {
		name = st.SessionID
	}
	_, _ = fmt.Fprintf(out, "%s  %s\n", name, st.State)
	if st.QueuedPrompts > 0 {
		_, _ = fmt.Fprintf(out, "queued prompts: %d\n", st.QueuedPrompts)
	}
	if st.Pending != nil {
		for _, p := range st.Pending.Permissions {
			_, _ = fmt.Fprintf(out, "permission %s: %s %s %s\n", p.ID, p.ToolName, p.Action, p.Description)
		}
		for _, q := range st.Pending.Questions {
			_, _ = fmt.Fprintf(out, "question %s: %s", q.ID, q.Prompt)
			if len(q.Options) > 0 {
				_, _ = fmt.Fprintf(out, " [%s]", strings.Join(q.Options, " | "))
			}
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintf(out, "answer with: crush session answer %s --allow|--deny|--answer ...\n", st.SessionID)
	}
	if st.Last != nil {
		_, _ = fmt.Fprintf(out, "last: %s\n", st.Last.Status)
		if st.Last.Error != "" {
			_, _ = fmt.Fprintf(out, "error: %s\n", st.Last.Error)
		}
		if text := strings.TrimSpace(st.Last.Text); text != "" {
			_, _ = fmt.Fprintf(out, "\n%s\n", text)
		}
	}
	return nil
}

func sessionWait(ctx context.Context, c headless.EventClient, ws *proto.Workspace, sess *proto.Session, timeout time.Duration, format headless.OutputFormat, out io.Writer) error {
	emitter := headless.NewNDJSONEmitter(out)
	var progress headless.Emitter
	if format == headless.FormatStreamJSON {
		progress = emitter
	}
	outcome := headless.WaitSession(ctx, c, ws.ID, sess.ID, timeout, progress)

	enrichCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	res := headless.Finish(enrichCtx, c, headless.Baseline{}, ws.ID, sess.ID, workspaceCwd(ws), "", outcome)
	return writeResult(out, format, emitter, res)
}

// answerWaitClient can both answer prompts and wait for the run.
type answerWaitClient interface {
	headless.EventClient
	headless.AnswerClient
}

func sessionAnswer(
	ctx context.Context,
	c answerWaitClient,
	ws *proto.Workspace,
	sess *proto.Session,
	req headless.AnswerRequest,
	wait bool,
	timeout time.Duration,
	format headless.OutputFormat,
	out io.Writer,
) error {
	res, err := headless.Answer(ctx, c, ws.ID, sess.ID, req)
	if err != nil {
		return err
	}
	if wait {
		return sessionWait(ctx, c, ws, sess, timeout, format, out)
	}
	if format.IsStructured() {
		return headless.NewNDJSONEmitter(out).Emit(res)
	}
	state := "resolved"
	if !res.Resolved {
		state = "already resolved by another client"
	}
	_, _ = fmt.Fprintf(out, "%s %s: %s\n", res.Kind, res.RequestID, state)
	return nil
}

// eventCancel is the type of the object printed by `crush session cancel`.
const eventCancel headless.EventType = "cancel"

type cancelResult struct {
	Type      headless.EventType `json:"type"`
	SessionID string             `json:"session_id"`
	WasBusy   bool               `json:"was_busy"`
}

// cancelClient is the subset of the client used by [sessionCancel].
type cancelClient interface {
	sessionCanceller
	GetAgentSessionInfo(ctx context.Context, workspaceID, sessionID string) (*proto.AgentSession, error)
}

func sessionCancel(ctx context.Context, c cancelClient, ws *proto.Workspace, sess *proto.Session, format headless.OutputFormat, out io.Writer) error {
	busy := false
	if info, err := c.GetAgentSessionInfo(ctx, ws.ID, sess.ID); err == nil && info != nil {
		busy = info.IsBusy
	}
	if err := c.CancelAgentSession(ctx, ws.ID, sess.ID); err != nil {
		return err
	}
	res := cancelResult{Type: eventCancel, SessionID: sess.ID, WasBusy: busy}
	if format.IsStructured() {
		return headless.NewNDJSONEmitter(out).Emit(res)
	}
	if busy {
		_, _ = fmt.Fprintf(out, "cancelled %s\n", sess.ID)
	} else {
		_, _ = fmt.Fprintf(out, "%s was not running\n", sess.ID)
	}
	return nil
}
