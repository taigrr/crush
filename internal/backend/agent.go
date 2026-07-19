package backend

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/shell"
)

// SendMessage validates and accepts a prompt for the workspace's agent,
// then dispatches the run on a goroutine bound to the workspace context
// and returns immediately. It does not wait for the LLM turn to
// complete: the run's lifetime is owned by the workspace, not by the
// caller. Errors from the dispatched run reach observers through the
// agent event channels (a notify.TypeAgentError notification), not
// through this return value.
//
// SendMessage returns synchronously when the request cannot be accepted:
// ErrWorkspaceNotFound if the workspace is missing, ErrAgentNotInitialized
// if its coordinator is nil, the structural validation errors from
// agent.ValidateCall (ErrEmptyPrompt, ErrSessionMissing) when the prompt
// or session is missing, and ErrWorkspaceClosing if the workspace is
// being torn down.
func (b *Backend) SendMessage(workspaceID string, msg proto.AgentMessage) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator == nil {
		return ErrAgentNotInitialized
	}

	if err := agent.ValidateCall(agent.SessionAgentCall{
		SessionID:   msg.SessionID,
		Prompt:      msg.Prompt,
		Attachments: proto.AttachmentsToMessage(msg.Attachments),
	}); err != nil {
		return err
	}

	accept := ws.AgentCoordinator.BeginAccepted(msg.SessionID)

	ws.runMu.Lock()
	if ws.closing {
		ws.runMu.Unlock()
		accept.Close()
		return ErrWorkspaceClosing
	}
	ws.runWG.Add(1)
	ws.runMu.Unlock()

	go b.runAgent(ws, msg, accept)
	return nil
}

// runAgent executes an accepted agent run for the workspace. It owns the
// accept reservation (releasing it on return) and the runWG ticket added
// by SendMessage. The run is bound to the workspace context so its
// lifetime is independent of any client's HTTP request.
//
// On a non-cancel error it surfaces the failure to observers via a
// notify.TypeAgentError notification (lossy, best-effort). That alone is
// not a reliable terminal signal: the agent-event fan-in uses lossy
// subscribers, so a `crush run` caller blocking on its RunID could hang
// if the event is dropped. To guarantee termination, when msg.RunID is
// non-empty and the coordinator did not already publish the run's
// authoritative terminal RunComplete (e.g. the error was returned before
// sessionAgent.Run executed, such as a readyWg or UpdateModels failure),
// runAgent emits an errored RunComplete on the must-deliver
// runCompletions broker so the waiter observes a deterministic terminal
// event. context.Canceled is expected (sessionAgent.Run already
// publishes the cancelled terminal marker) and produces no error
// terminal event.
//
// When msg.RunID is non-empty it is attached to the context via
// agent.WithRunID so the coordinator can stamp the terminal
// notify.RunComplete event with that correlator. A run-complete marker
// is also attached so the coordinator can report whether it published
// the terminal event, letting runAgent avoid a duplicate fallback.
func (b *Backend) runAgent(ws *Workspace, msg proto.AgentMessage, accept *agent.AcceptedRun) {
	// Registered first so it runs last — after runWG.Done below — so that
	// if this was the final in-flight run and all clients have already
	// detached, the workspace (and, if it was the last one, the server)
	// is torn down now. While the run was live, teardownIfIdle on the
	// detach paths deliberately kept the workspace alive. teardownIfIdle
	// is a cheap no-op when clients remain or another run is still busy.
	defer b.teardownIfIdle(ws)
	defer ws.runWG.Done()
	defer accept.Close()

	ctx := ws.ctx
	if msg.RunID != "" {
		ctx = agent.WithRunID(ctx, msg.RunID)
	}
	ctx = agent.WithRunCompleteMarker(ctx)

	// Route the originating client's editor bridge to the tools for this
	// turn. The coordinator is shared across all clients on the
	// workspace, so per-client editor targeting must flow through the
	// request context rather than coordinator state.
	ctx = tools.WithEditorBridge(ctx, b.clientBridge(ws, msg.ClientID))

	// Route the originating client's launch directory to the tools for
	// this turn. Sibling git worktrees collapse to the same project root
	// and therefore share one workspace (and one coordinator); without
	// per-turn cwd the coordinator would resolve every client to the
	// directory whichever client created the workspace first launched
	// from. See coordinator.workingDir.
	ctx = tools.WithWorkingDir(ctx, b.clientCwd(ws, msg.ClientID))

	_, err := ws.AgentCoordinator.RunAccepted(ctx, accept, msg.SessionID, msg.Prompt, proto.AttachmentsToMessage(msg.Attachments)...)
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}

	ws.AgentNotifications().Publish(pubsub.CreatedEvent, notify.Notification{
		SessionID: msg.SessionID,
		RunID:     msg.RunID,
		Type:      notify.TypeAgentError,
		Message:   err.Error(),
	})

	// Reliable terminal fallback. Only needed when a RunID waiter
	// exists and the coordinator has not already emitted the run's
	// terminal RunComplete; otherwise this would be a duplicate.
	if msg.RunID == "" || agent.RunCompletePublished(ctx) {
		return
	}
	if rc := ws.RunCompletions(); rc != nil {
		rc.PublishMustDeliver(ctx, pubsub.UpdatedEvent, notify.RunComplete{
			SessionID: msg.SessionID,
			RunID:     msg.RunID,
			Error:     err.Error(),
		})
	}
}

// GetAgentInfo returns the agent's model and busy status.
func (b *Backend) GetAgentInfo(workspaceID string) (proto.AgentInfo, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.AgentInfo{}, err
	}

	var agentInfo proto.AgentInfo
	if ws.AgentCoordinator != nil {
		m := ws.AgentCoordinator.Model()
		agentInfo = proto.AgentInfo{
			Model:    m.CatwalkCfg,
			ModelCfg: m.ModelCfg,
			IsBusy:   ws.AgentCoordinator.IsBusy(),
			IsReady:  true,
		}
	}
	return agentInfo, nil
}

// InitAgent initializes the coder agent for the workspace.
func (b *Backend) InitAgent(ctx context.Context, workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.InitCoderAgent(ctx)
}

// UpdateAgent reloads the agent model configuration.
func (b *Backend) UpdateAgent(ctx context.Context, workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	return ws.UpdateAgentModel(ctx)
}

// CancelSession cancels an ongoing agent operation for the given
// session.
func (b *Backend) CancelSession(workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.Cancel(sessionID)
	}
	return nil
}

// CancelAllSessions cancels every in-flight agent run in the workspace,
// regardless of which session it belongs to. It is the workspace-wide
// counterpart to CancelSession, used when a client is not focused on the
// busy session (e.g. after detaching from and reattaching to a workspace
// whose run is still going).
func (b *Backend) CancelAllSessions(workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.CancelAll()
	}
	return nil
}

// SummarizeSession triggers a session summarization.
func (b *Backend) SummarizeSession(ctx context.Context, workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator == nil {
		return ErrAgentNotInitialized
	}

	return ws.AgentCoordinator.Summarize(ctx, sessionID)
}

// GenerateTitleForSession regenerates a session's title on demand.
func (b *Backend) GenerateTitleForSession(ctx context.Context, workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator == nil {
		return ErrAgentNotInitialized
	}

	return ws.AgentCoordinator.RegenerateTitle(ctx, sessionID)
}

// QueuedPrompts returns the number of queued prompts for the session.
func (b *Backend) QueuedPrompts(workspaceID, sessionID string) (int, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return 0, err
	}

	if ws.AgentCoordinator == nil {
		return 0, nil
	}

	return ws.AgentCoordinator.QueuedPrompts(sessionID), nil
}

// ClearQueue clears the prompt queue for the session.
func (b *Backend) ClearQueue(workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.ClearQueue(sessionID)
	}
	return nil
}

// SetGoal sets (or, when condition is blank, clears) the autonomous goal
// for a session.
func (b *Backend) SetGoal(workspaceID, sessionID, condition string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.SetGoal(sessionID, condition)
	}
	return nil
}

// ClearGoal clears any active autonomous goal for a session.
func (b *Backend) ClearGoal(workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.ClearGoal(sessionID)
	}
	return nil
}

// SetSessionWorkingDir records the working directory tools run in for a
// session. Persisted via the session store so it survives reconnects and
// applies across clients.
func (b *Backend) SetSessionWorkingDir(ctx context.Context, workspaceID, sessionID, dir string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if ws.App == nil || ws.Sessions == nil {
		return ErrAgentNotInitialized
	}
	return ws.Sessions.SetWorkingDir(ctx, sessionID, dir)
}

// GoalStatus reports the active autonomous goal for a session.
func (b *Backend) GoalStatus(workspaceID, sessionID string) (proto.GoalStatus, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.GoalStatus{}, err
	}
	if ws.AgentCoordinator == nil {
		return proto.GoalStatus{}, nil
	}
	condition, turns, maxTurns, active := ws.AgentCoordinator.GoalStatus(sessionID)
	return proto.GoalStatus{
		Active:    active,
		Condition: condition,
		Turns:     turns,
		MaxTurns:  maxTurns,
	}, nil
}

// QueuedPromptsList returns the list of queued prompt strings for a
// session.
func (b *Backend) QueuedPromptsList(workspaceID, sessionID string) ([]string, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}

	if ws.AgentCoordinator == nil {
		return nil, nil
	}

	return ws.AgentCoordinator.QueuedPromptsList(sessionID), nil
}

// GetDefaultSmallModel returns the default small model for a provider.
func (b *Backend) GetDefaultSmallModel(workspaceID, providerID string) (config.SelectedModel, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return config.SelectedModel{}, err
	}

	return ws.GetDefaultSmallModel(providerID), nil
}

// RunShellCommand runs a shell command in the workspace directory and
// persists the command + output as a user message in the session.
func (b *Backend) RunShellCommand(ctx context.Context, workspaceID string, req proto.ShellCommandRequest) (proto.ShellCommandResponse, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return proto.ShellCommandResponse{}, err
	}

	var stdout, stderr bytes.Buffer
	// Run in the launch directory of the client that initiated the command
	// (matching the agent's bash tool), falling back to the workspace's
	// effective working dir. ws.App.WorkingDir() is the first client's dir,
	// not necessarily this client's — they differ for subdirectories and
	// sibling git worktrees that share one workspace.
	cwd := b.clientCwd(ws, req.ClientID)
	if cwd == "" {
		cwd = ws.WorkingDir()
	}
	runErr := shell.Run(ctx, shell.RunOptions{
		Command: req.Command,
		Cwd:     cwd,
		Env:     append(os.Environ(), ws.Env...),
		Stdout:  &stdout,
		Stderr:  &stderr,
	})

	exitCode := 0
	if runErr != nil {
		exitCode = shell.ExitCode(runErr)
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	// Persist as a shell message. First part is the command, second is
	// the output. This lets the UI show only the command in history
	// while ToAIMessage joins them for the LLM.
	if req.SessionID != "" {
		parts := []message.ContentPart{
			message.TextContent{Text: req.Command},
			message.TextContent{Text: output},
		}
		_, _ = ws.Messages.Create(ctx, req.SessionID, message.CreateMessageParams{
			Role:  message.Shell,
			Parts: parts,
		})
	}

	return proto.ShellCommandResponse{
		Output:   output,
		ExitCode: exitCode,
	}, nil
}
