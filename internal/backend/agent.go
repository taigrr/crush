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
	"github.com/taigrr/crush/internal/sound"
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
	if b.Draining() {
		return ErrDraining
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
	// Re-check under runMu so a Drain that landed between the check
	// above and here cannot let a run slip in after the drain waiter
	// observed zero active runs.
	if b.Draining() {
		ws.runMu.Unlock()
		accept.Close()
		return ErrDraining
	}
	ws.runWG.Add(1)
	ws.liveRuns++
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
	defer b.signalDrain()
	defer ws.runWG.Done()
	defer func() {
		ws.runMu.Lock()
		ws.liveRuns--
		ws.runMu.Unlock()
	}()
	defer accept.Close()

	// Publish cross-workspace busy/idle transitions on the global
	// attention channel so background sessions show a live busy dot
	// (and clear it when the run ends) without the client attaching to
	// this workspace.
	b.publishAttention(ws, msg.SessionID, "", proto.AttentionBusy)
	defer b.publishAttention(ws, msg.SessionID, "", proto.AttentionIdle)

	ctx := ws.ctx
	if msg.RunID != "" {
		ctx = agent.WithRunID(ctx, msg.RunID)
	}
	if msg.Steer {
		ctx = agent.WithSteer(ctx)
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

	// Convert proto SwarmParts (if any) into their message twins so
	// the coordinator threads them onto the created user message via
	// SessionAgentCall.SwarmParts. This is how a cross-session swarm
	// send arrives at its target with structured sender metadata
	// instead of a plain text prefix.
	if len(msg.SwarmParts) > 0 {
		parts := make([]message.SwarmMessage, len(msg.SwarmParts))
		for i, p := range msg.SwarmParts {
			parts[i] = message.SwarmMessage{
				Text:              p.Text,
				Body:              p.Body,
				SenderSessionID:   p.SenderSessionID,
				SenderColor:       p.SenderColor,
				SenderAnimal:      p.SenderAnimal,
				SenderWorkspaceID: p.SenderWorkspaceID,
				BTW:               p.BTW,
				RequireReply:      p.RequireReply,
			}
		}
		ctx = agent.WithSwarmParts(ctx, parts)
	}

	result, err := ws.AgentCoordinator.RunAccepted(ctx, accept, msg.SessionID, msg.Prompt, proto.AttachmentsToMessage(msg.Attachments)...)
	if err == nil {
		switch {
		case result != nil:
			// Genuine end of turn: a turn actually ran to completion.
			// result is non-nil only on the completed path; the
			// enqueue/fold paths return (nil, nil). This is why a
			// folded /btw aside (empty RunID, no result) is silent and
			// does not double-chime with the turn it folds into.
			b.playSound(ws, sound.EndOfTurn)
		case msg.RunID != "":
			// The message was queued behind an active turn (busy
			// session, non-empty RunID). It will run as its own turn
			// later, drained by the active run; play a queued bump now.
			b.playSound(ws, sound.Queued)
		}
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}

	// Best-effort error toast. Note: when this call's own turn hands
	// off to a queued follow-up (e.g. it was canceled and a prompt
	// queued behind it survives), err reflects the tail of that
	// recursive hand-off rather than this request's own outcome, so
	// the error surfaced here can belong to a different (later) turn
	// than msg.RunID. This request's own authoritative RunComplete is
	// still published correctly by the coordinator regardless.
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

// InitAgent initializes the coder agent for the workspace. Every client
// calls this on connect, and the onboarding flow calls it after the
// first model is chosen. When the workspace already has a coordinator
// this must NOT rebuild it: the existing one may be mid-turn (or have
// just replayed a journaled queue), and swapping the pointer would leave
// those runs on an orphan the busy/drain/teardown paths can no longer
// see. Instead the live coordinator is refreshed in place — models,
// system prompt (context files), tools, and a fresh readiness group —
// deferred until idle if it is busy. Calls are serialized per workspace
// so concurrent inits cannot build two coordinators.
func (b *Backend) InitAgent(ctx context.Context, workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	ws.initMu.Lock()
	defer ws.initMu.Unlock()
	if ws.AgentCoordinator != nil {
		return ws.RefreshAgent(ctx)
	}

	if err := ws.InitCoderAgent(ctx); err != nil {
		return err
	}

	// A fresh coordinator needs the swarm shim wired in, and any
	// journaled queue replayed onto it (CreateWorkspace skipped that
	// when the workspace came up unconfigured).
	if err := b.wireSwarmBackend(ctx, ws); err != nil {
		return err
	}
	b.rehydrateQueue(ws)
	return nil
}

// wireSwarmBackend injects the cross-workspace swarm dispatcher into
// ws's coordinator and refreshes the coder agent's tool set so the
// swarm/workspace_lookup tools take effect. It is the single point of
// backend-level swarm wiring: swarm is inherently cross-workspace (the
// shim wraps [Backend] and its all-workspaces map), so it cannot be
// wired inside app.New like every other single-workspace concern — it
// has to be injected from this layer, after the coordinator is built.
//
// Called by [Backend.CreateWorkspace]'s fresh-creation path (the
// coordinator is guaranteed idle — nothing has run on it yet) and by
// [Backend.InitAgent] after a coordinator rebuild. Prefer
// wireSwarmBackendIfMissing for any call site where the workspace may
// already be attached and busy (see its docs). Test mocks that don't
// implement SwarmConfigurable are silently skipped.
func (b *Backend) wireSwarmBackend(ctx context.Context, ws *Workspace) error {
	setter, ok := ws.AgentCoordinator.(agent.SwarmConfigurable)
	if !ok {
		return nil
	}
	return setter.SetSwarmBackend(ctx, &swarmShim{b: b}, ws.ID, ws.SwarmConfig)
}

// wireSwarmBackendIfMissing self-heals a workspace whose swarm
// backend was never wired (or was lost), without disturbing an
// already-wired, possibly busy, long-lived coordinator. It delegates
// the busy/idle handling to the coordinator itself (see
// [agent.SwarmConfigurable.WireSwarmBackendIfMissing]) rather than
// unconditionally forcing a synchronous tool-set rebuild, which would
// race a live run's readyWg.Wait() against the rebuild's readyWg.Go.
//
// Called from [Backend.CreateWorkspace]'s dedup/reuse branches, which
// run on every client reconnect and every TUI workspace switch — the
// common case must stay a cheap no-op.
func (b *Backend) wireSwarmBackendIfMissing(ctx context.Context, ws *Workspace) error {
	setter, ok := ws.AgentCoordinator.(agent.SwarmConfigurable)
	if !ok {
		return nil
	}
	return setter.WireSwarmBackendIfMissing(ctx, &swarmShim{b: b}, ws.ID, ws.SwarmConfig)
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

// SoftInterruptSession asks the tools running in the session's current
// step to wrap up early without cancelling anything: a long-running bash
// command is handed back to the model as a background job it can poll
// with job_output, and the turn continues. This is the "background the
// running command" affordance; a steer (proto.AgentMessage.Steer) does
// the same and additionally queues a message. It is a no-op when the
// session is idle or nothing in the step listens for the interrupt.
func (b *Backend) SoftInterruptSession(workspaceID, sessionID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if ws.AgentCoordinator != nil {
		ws.AgentCoordinator.SoftInterrupt(sessionID)
	}
	return nil
}

// BackgroundToolCall asks one specific in-flight tool call to move its
// work to the background: the tool returns a normal result naming the
// background job and the turn continues. Unlike SoftInterruptSession it
// targets a single call rather than every opted-in tool in the step. It
// returns ErrToolCallNotBackgroundable when the call is unknown, already
// finished, does not support backgrounding, or belongs to another
// session.
func (b *Backend) BackgroundToolCall(workspaceID, sessionID, toolCallID string) error {
	if _, err := b.GetWorkspace(workspaceID); err != nil {
		return err
	}
	if !tools.RequestBackground(sessionID, toolCallID) {
		return ErrToolCallNotBackgroundable
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
