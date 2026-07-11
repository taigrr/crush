package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/taigrr/crush/internal/agent/hyper"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/stringext"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
	"github.com/taigrr/fantasy/providers/google"
	"github.com/taigrr/fantasy/providers/openai"
)

func (a *sessionAgent) persistCanceledTurn(ctx context.Context, call SessionAgentCall, userMsgCreated bool) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if !userMsgCreated {
		if _, err := a.createUserMessage(writeCtx, call); err != nil {
			return err
		}
	}
	largeModel := a.largeModel.Get()
	assistant, err := a.messages.Create(writeCtx, call.SessionID, message.CreateMessageParams{
		Role:     message.Assistant,
		Parts:    []message.ContentPart{},
		Model:    largeModel.ModelCfg.Model,
		Provider: largeModel.ModelCfg.Provider,
	})
	if err != nil {
		return err
	}
	assistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
	return a.messages.Update(writeCtx, assistant)
}

// publishRunComplete emits the authoritative terminal event for a turn.
// It honors the per-call OnComplete hook when set (so the coordinator can
// coalesce retries) and otherwise falls back to the RunComplete broker.
// ctx is used only for the bounded-blocking must-deliver publish; the
// terminal payload is supplied by the caller. This is the single emit path
// shared by the streaming defer and the cancel-on-entry early return so a
// caller waiting on RunComplete (e.g. `crush run` with a RunID) always
// observes exactly one terminal event regardless of which Run branch ends
// the turn.
func (a *sessionAgent) publishRunComplete(ctx context.Context, call SessionAgentCall, complete notify.RunComplete) {
	if call.OnComplete != nil {
		call.OnComplete(complete)
		return
	}
	if a.runComplete == nil {
		return
	}
	a.runComplete.PublishMustDeliver(ctx, pubsub.UpdatedEvent, complete)
}

// ValidateCall performs the cheap structural validation that
// sessionAgent.Run requires before a call can be dispatched: a call must
// carry either a non-empty prompt or a text attachment, and it must name a
// session. It is exported so callers that accept a run before dispatching it
// (e.g. backend.SendMessage) can apply the same checks and keep the error
// contract consistent.
func ValidateCall(call SessionAgentCall) error {
	if call.Prompt == "" && !message.ContainsTextAttachment(call.Attachments) {
		return ErrEmptyPrompt
	}
	if call.SessionID == "" {
		return ErrSessionMissing
	}
	return nil
}

func (a *sessionAgent) Run(ctx context.Context, call SessionAgentCall) (result *fantasy.AgentResult, retErr error) {
	if err := ValidateCall(call); err != nil {
		return nil, err
	}

	// genCtx/cancel are the run context and its cancel func. For the
	// accepted (fire-and-forget) dispatch path they are created under
	// dispatchMu below so a concurrent Cancel can observe the
	// activeRequests entry before the assistant message exists. For
	// the in-process path they stay nil here and are created later,
	// preserving the original ordering.
	var (
		genCtx           context.Context
		cancel           context.CancelFunc
		activeRegistered bool
		userMsgCreated   bool
	)

	if call.Accepted != nil {
		// Serialize the accepted -> (cancel-on-entry | queued |
		// active) transition against a concurrent Cancel. Cancel takes
		// the same per-session lock, so every cancel observes at least
		// one of: a cancel mark, an activeRequests entry, or a
		// messageQueue entry it then clears.
		mu := a.sessionMu(call.SessionID)
		mu.Lock()

		if a.canceledBySeq(call.SessionID, call.Accepted.seq) {
			// Cancel-on-entry: a cancel arrived while this run was
			// dispatched but not yet active, and this handle's accept
			// sequence is at or below the session's cancel mark. The
			// mark is left in place so sibling handles it also covers
			// observe the same cancel; release the accept reservation,
			// drop the lock, and persist a canceled turn without
			// entering Stream.
			//
			// This path returns before the streaming defer that
			// publishes RunComplete is installed, so emit the terminal
			// event explicitly. Without it, a caller waiting on
			// RunComplete for this RunID (e.g. `crush run`, which
			// ignores message events and blocks on RunComplete) would
			// hang on an immediately-canceled accepted run.
			call.Accepted.Close()
			mu.Unlock()
			complete := notify.RunComplete{
				SessionID: call.SessionID,
				RunID:     call.RunID,
				Cancelled: true,
			}
			if err := a.persistCanceledTurn(ctx, call, false); err != nil {
				complete.Error = err.Error()
				a.publishRunComplete(ctx, call, complete)
				return nil, err
			}
			a.publishRunComplete(ctx, call, complete)
			return nil, nil
		}

		if a.IsSessionBusy(call.SessionID) {
			// Busy: an earlier prompt is active. Queue this call and
			// release the accept reservation. A Cancel arriving after
			// this point sees the active entry and clears the queue.
			a.enqueueCall(call)
			call.Accepted.Close()
			mu.Unlock()
			return nil, nil
		}

		// Idle: become the active run. Register the cancel func before
		// dropping the lock so a Cancel that arrives between here and
		// assistant creation is not lost.
		runCtx := context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)
		genCtx, cancel = context.WithCancel(runCtx)
		a.activeRequests.Set(call.SessionID, cancel)
		activeRegistered = true
		call.Accepted.Close()
		mu.Unlock()

		defer cancel()
		defer a.clearActiveRequest(call.SessionID)
	} else if a.IsSessionBusy(call.SessionID) {
		// Queue the message if busy. Strip OnComplete: the caller that
		// supplied the hook (typically coordinator.Run) has its own
		// retry/coalesce scope that ends when it returns, so by the time
		// the queue drains nobody is left to consume the buffered
		// terminal event. The recursive Run will fall back to the
		// default broker publish, which is what existing subscribers
		// expect for queued turns.
		a.enqueueCall(call)
		return nil, nil
	}

	// Copy mutable fields under lock to avoid races with
	// SetTools/SetModels, then drop tools that opt out of this turn's
	// context (e.g. editor tools when the initiating client has no
	// attached editor).
	agentTools := tools.FilterAvailableTools(ctx, a.tools.Copy())
	largeModel := a.largeModel.Get()
	systemPrompt := a.systemPrompt.Get()
	promptPrefix := a.systemPromptPrefix.Get()
	var instructions strings.Builder

	for _, server := range mcp.GetStates() {
		if server.State != mcp.StateConnected {
			continue
		}
		if s := server.Client.InitializeResult().Instructions; s != "" {
			instructions.WriteString(s)
			instructions.WriteString("\n\n")
		}
	}

	if s := instructions.String(); s != "" {
		systemPrompt += "\n\n<mcp-instructions>\n" + s + "\n</mcp-instructions>"
	}

	// Tell the model, every turn, which directory its tools run in. This
	// keeps it accurate when a session is resumed from a client with a
	// different launch cwd, or after an explicit /cwd change: the model
	// should treat paths as relative to this directory and must not try to
	// `cd` into it. The recorded session working dir is authoritative (see
	// coordinator.workingDir); when unset the model falls back to its
	// system-prompt default.
	if a.sessions != nil {
		if sess, err := a.sessions.Get(ctx, call.SessionID); err == nil && sess.WorkingDir != "" {
			systemPrompt += "\n\n<environment>\nCurrent working directory: " + sess.WorkingDir +
				"\nAll tool calls run in this directory. Treat relative paths as relative to it; do not cd into it.\n</environment>"
		}
	}

	if len(agentTools) > 0 {
		// Add Anthropic caching to the last tool.
		agentTools[len(agentTools)-1].SetProviderOptions(a.getCacheControlOptions())
	}

	agent := fantasy.NewAgent(
		largeModel.Model,
		fantasy.WithSystemPrompt(systemPrompt),
		fantasy.WithTools(agentTools...),
		fantasy.WithUserAgent(userAgent),
	)

	sessionLock := sync.Mutex{}
	currentSession, err := a.sessions.Get(ctx, call.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	// Enforce the model's per-image and aggregate image limits before
	// the user message is persisted or sent. Oversized current-message
	// images are downscaled (proportionally, so multiple images stay
	// readable) to fit the budget left by images already in the thread;
	// if the turn cannot be made to fit, refuse it with a user-facing
	// error rather than letting it become an unrecoverable provider API
	// failure.
	fitted, err := fitImageAttachments(largeModel, msgs, call.Attachments)
	if err != nil {
		return nil, err
	}
	call.Attachments = fitted

	var wg sync.WaitGroup
	// Generate a title on the first message, then refresh it once the
	// conversation has more context. By the retitleAfterUserMessages-th user
	// message the conversation usually has enough substance for a sharper
	// title than the one derived from the opening prompt alone.
	userMsgCount := 0
	for _, msg := range msgs {
		if msg.Role == message.User {
			userMsgCount++
		}
	}
	// msgs excludes the prompt we are about to add, so the incoming message is
	// user message number userMsgCount+1.
	switch userMsgCount + 1 {
	case 1:
		titleCtx := ctx // Copy to avoid race with ctx reassignment below.
		wg.Go(func() {
			a.generateTitle(titleCtx, call.SessionID, call.Prompt)
		})
	case retitleAfterUserMessages:
		titleCtx := ctx // Copy to avoid race with ctx reassignment below.
		prompt := titlePromptFromMessages(msgs, call.Prompt)
		wg.Go(func() {
			a.generateTitle(titleCtx, call.SessionID, prompt)
		})
	}

	// Generate a milestone every milestoneInterval messages (total messages
	// regardless of role — user, assistant, tool calls all count). Since a
	// single Run() can produce many messages before the next Run(), we
	// generate one milestone for every boundary crossed since the last
	// generated milestone rather than a single one at the current turn —
	// otherwise a run that jumps past several boundaries at once would skip
	// the intermediate milestones.
	turnCount := len(msgs) + 1 // +1 for the prompt we're about to add.
	if !a.isSubAgent && a.milestones != nil {
		var lastTurn int64
		var priorSummary string
		if latest, err := a.milestones.Latest(ctx, call.SessionID); err == nil {
			lastTurn = latest.TurnNumber
			priorSummary = latest.FullSummary
		}
		// If at least one boundary lies in (lastTurn, turnCount], generate
		// every missing milestone in that range. This unifies the initial
		// backfill (lastTurn == 0) and incremental generation.
		if len(milestoneBoundaries(lastTurn, turnCount)) > 0 {
			milestoneCtx := ctx
			milestoneMsgs := msgs
			afterTurn := lastTurn
			prior := priorSummary
			wg.Go(func() {
				a.generateMilestones(milestoneCtx, call.SessionID, afterTurn, turnCount, milestoneMsgs, prior)
			})
		}
	}
	defer wg.Wait()

	// Add the user message to the session.
	userMsg, err := a.createUserMessage(ctx, call)
	if err != nil {
		return nil, err
	}
	userMsgCreated = true

	// Create snapshot of filesystem state (best-effort, don't block on failure).
	if a.checkpoints != nil && a.checkpoints.IsEnabled() && !a.isSubAgent {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic in snapshot goroutine", "recover", r, "session_id", call.SessionID)
				}
			}()
			// Use background context since this shouldn't be cancelled with the request.
			snapshotCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := a.checkpoints.CreateSnapshot(snapshotCtx, call.SessionID, userMsg.ID, call.Prompt); err != nil {
				slog.Debug("Failed to create snapshot", "error", err, "session_id", call.SessionID)
			}
		}()
	}

	// Add the session to the context.
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, call.SessionID)

	// For the accepted dispatch path the run context and cancel func
	// were already created and registered under dispatchMu above; reuse
	// them. For the in-process path create them here, preserving the
	// original ordering.
	if !activeRegistered {
		genCtx, cancel = context.WithCancel(ctx)
		a.activeRequests.Set(call.SessionID, cancel)

		defer cancel()
		defer a.clearActiveRequest(call.SessionID)
	}
	// skipRunComplete is set just before the queued-recursion path so
	// the outer Run doesn't publish a RunComplete that would race
	// with — and be superseded by — the recursive call's own
	// RunComplete (each queued user prompt is its own turn and
	// publishes exactly one terminal event).
	var skipRunComplete bool
	// currentAssistant is declared here so the deferred RunComplete
	// publish below can capture the pointer that PrepareStep will
	// later (re)assign for each streaming step. The final assistant
	// message of the turn is the value reachable through this
	// pointer when the defer runs.
	var currentAssistant *message.Message
	// Drain any debounced message updates before returning. message.Service
	// already flushes synchronously on terminal updates, but a defer here
	// guarantees the contract at every Run exit (success, error, panic
	// recovery upstream) without callers needing to know.
	//
	// After the flush completes — meaning all per-message
	// Publish(UpdatedEvent) calls have fired and been buffered into
	// every subscriber's channel — publish the authoritative
	// RunComplete event for this turn. The flush-then-publish order
	// gives well-behaved clients the best chance of seeing the final
	// message event before RunComplete; the embedded Text field
	// reconciles for clients that observe the events out of order
	// (the pubsub broker fan-in does not serialize publishes from
	// different upstream brokers).
	defer func() {
		// Use a context detached from the run context: workspace
		// shutdown cancels ctx before this goroutine returns, but the
		// buffered streaming deltas must still land before the DB is
		// closed. A short timeout bounds the flush.
		flushCtx, flushCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer flushCancel()
		if flushErr := a.messages.FlushAll(flushCtx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after run", "error", flushErr)
		}
		if skipRunComplete {
			return
		}
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if retErr != nil {
			complete.Error = retErr.Error()
			complete.Cancelled = errors.Is(retErr, context.Canceled)
		} else if ctx.Err() != nil {
			complete.Cancelled = true
		}
		// Prefer the per-call hook when supplied so the coordinator
		// can coalesce retries (e.g. unauthorized → re-auth → retry)
		// into a single user-visible terminal event. The fallback
		// must-deliver publish applies bounded-blocking semantics to
		// the authoritative terminal event so a momentarily-full
		// subscriber channel can't silently drop it and hang
		// non-interactive clients waiting on RunComplete.
		a.publishRunComplete(ctx, call, complete)
	}()

	history, files := a.preparePrompt(msgs, largeModel.CatwalkCfg.SupportsImages, call.Attachments...)

	var stepMessages []fantasy.Message
	var shouldSummarize bool
	// Don't send MaxOutputTokens if 0 — some providers (e.g. LM Studio) reject it
	var maxOutputTokens *int64
	if call.MaxOutputTokens > 0 {
		maxOutputTokens = &call.MaxOutputTokens
	}

	result, err = agent.Stream(genCtx, fantasy.AgentStreamCall{
		Prompt:           message.PromptWithTextAttachments(call.Prompt, call.Attachments),
		Files:            files,
		Messages:         history,
		ProviderOptions:  call.ProviderOptions,
		MaxOutputTokens:  maxOutputTokens,
		TopP:             call.TopP,
		Temperature:      call.Temperature,
		PresencePenalty:  call.PresencePenalty,
		TopK:             call.TopK,
		FrequencyPenalty: call.FrequencyPenalty,
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = options.Messages
			for i := range prepared.Messages {
				prepared.Messages[i].ProviderOptions = nil
			}

			// Use latest tools (updated by SetTools when MCP tools
			// change), filtered to those available for this turn's
			// context.
			prepared.Tools = tools.FilterAvailableTools(ctx, a.tools.Copy())

			// Drain queued follow-up prompts for this step. Calls covered
			// by a cancel recorded while they sat in the queue are dropped:
			// a cancel that arrived after a prompt was queued must not let
			// it run as part of this step. Coverage is per-call by accept
			// sequence so a follow-up queued after the cancel (higher seq)
			// is not dropped. A dropped prompt carrying a RunID still gets
			// its terminal cancelled RunComplete so a caller waiting on it
			// does not hang. Uncanceled prompts without a RunID are folded
			// into this turn; uncanceled prompts with a RunID are left
			// queued so each runs as its own turn (with its own
			// RunComplete) via the recursive run path below.
			fold, canceledRunIDs := a.drainQueueForStep(call.SessionID)
			a.publishCanceledQueueDrops(canceledRunIDs)
			for _, queued := range fold {
				userMessage, createErr := a.createUserMessage(callContext, queued)
				if createErr != nil {
					return callContext, prepared, createErr
				}
				prepared.Messages = append(prepared.Messages, userMessage.ToAIMessage()...)
			}

			prepared.Messages = a.workaroundProviderMediaLimitations(prepared.Messages, largeModel)

			lastSystemRoleInx := 0
			systemMessageUpdated := false
			for i, msg := range prepared.Messages {
				// Only add cache control to the last message.
				if msg.Role == fantasy.MessageRoleSystem {
					lastSystemRoleInx = i
				} else if !systemMessageUpdated {
					prepared.Messages[lastSystemRoleInx].ProviderOptions = a.getCacheControlOptions()
					systemMessageUpdated = true
				}
				// Than add cache control to the last 2 messages.
				if i > len(prepared.Messages)-3 {
					prepared.Messages[i].ProviderOptions = a.getCacheControlOptions()
				}
			}

			if promptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(promptPrefix)}, prepared.Messages...)
			}

			sessionLock.Lock()
			stepMessages = cloneFantasyMessages(prepared.Messages)
			sessionLock.Unlock()

			var assistantMsg message.Message
			assistantMsg, err = a.messages.Create(callContext, call.SessionID, message.CreateMessageParams{
				Role:     message.Assistant,
				Parts:    []message.ContentPart{},
				Model:    largeModel.ModelCfg.Model,
				Provider: largeModel.ModelCfg.Provider,
			})
			if err != nil {
				return callContext, prepared, err
			}
			callContext = context.WithValue(callContext, tools.MessageIDContextKey, assistantMsg.ID)
			callContext = context.WithValue(callContext, tools.SupportsImagesContextKey, largeModel.CatwalkCfg.SupportsImages)
			callContext = context.WithValue(callContext, tools.ModelNameContextKey, largeModel.CatwalkCfg.Name)
			currentAssistant = &assistantMsg
			return callContext, prepared, err
		},
		OnReasoningStart: func(id string, reasoning fantasy.ReasoningContent) error {
			currentAssistant.AppendReasoningContent(reasoning.Text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningDelta: func(id string, text string) error {
			currentAssistant.AppendReasoningContent(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// handle anthropic signature
			if anthropicData, ok := reasoning.ProviderMetadata[anthropic.Name]; ok {
				if reasoning, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok {
					currentAssistant.AppendReasoningSignature(reasoning.Signature)
				}
			}
			if googleData, ok := reasoning.ProviderMetadata[google.Name]; ok {
				if reasoning, ok := googleData.(*google.ReasoningMetadata); ok {
					currentAssistant.AppendThoughtSignature(reasoning.Signature, reasoning.ToolID)
				}
			}
			if openaiData, ok := reasoning.ProviderMetadata[openai.Name]; ok {
				if reasoning, ok := openaiData.(*openai.ResponsesReasoningMetadata); ok {
					currentAssistant.SetReasoningResponsesData(reasoning)
				}
			}
			currentAssistant.FinishThinking()
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnTextDelta: func(id string, text string) error {
			// Strip leading newline from initial text content. This is is
			// particularly important in non-interactive mode where leading
			// newlines are very visible.
			if len(currentAssistant.Parts) == 0 {
				text = strings.TrimPrefix(text, "\n")
			}

			currentAssistant.AppendContent(text)
			return a.messages.Update(genCtx, *currentAssistant)
		},
		OnToolInputStart: func(id string, toolName string) error {
			toolCall := message.ToolCall{
				ID:               id,
				Name:             toolName,
				ProviderExecuted: false,
				Finished:         false,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnRetry: func(err *fantasy.ProviderError, delay time.Duration) {
			slog.Warn("Provider request failed, retrying", providerRetryLogFields(err, delay)...)
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			toolCall := message.ToolCall{
				ID:               tc.ToolCallID,
				Name:             tc.ToolName,
				Input:            tc.Input,
				ProviderExecuted: false,
				Finished:         true,
			}
			currentAssistant.AddToolCall(toolCall)
			// Use parent ctx instead of genCtx to ensure the update succeeds
			// even if the request is canceled mid-stream
			return a.messages.Update(ctx, *currentAssistant)
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			toolResult := a.convertToToolResult(result)
			// Use parent ctx instead of genCtx to ensure the message is created
			// even if the request is canceled mid-stream
			_, createMsgErr := a.messages.Create(ctx, currentAssistant.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					toolResult,
				},
			})
			return createMsgErr
		},
		OnStepFinish: func(stepResult fantasy.StepResult) error {
			finishReason := message.FinishReasonUnknown
			switch stepResult.FinishReason {
			case fantasy.FinishReasonLength:
				finishReason = message.FinishReasonMaxTokens
			case fantasy.FinishReasonStop:
				finishReason = message.FinishReasonEndTurn
			case fantasy.FinishReasonToolCalls:
				finishReason = message.FinishReasonToolUse
			}
			// If a tool result halted the turn (e.g. a hook halt or a
			// permission denial), the step ends on FinishReasonToolCalls but
			// the model will not be called again. Treat it as the end of the
			// turn so the UI can render the assistant footer.
			if finishReason == message.FinishReasonToolUse {
				for _, tr := range stepResult.Content.ToolResults() {
					if tr.StopTurn {
						finishReason = message.FinishReasonEndTurn
						break
					}
				}
			}
			currentAssistant.AddFinish(finishReason, "", "")
			sessionLock.Lock()
			defer sessionLock.Unlock()

			updatedSession, getSessionErr := a.sessions.Get(ctx, call.SessionID)
			if getSessionErr != nil {
				return getSessionErr
			}
			usage, estimated := fallbackStepUsage(stepMessages, stepResult)
			a.updateSessionUsage(largeModel, &updatedSession, usage, a.openrouterCost(stepResult.ProviderMetadata), estimated)
			_, sessionErr := a.sessions.Save(ctx, updatedSession)
			if sessionErr != nil {
				return sessionErr
			}
			currentSession = updatedSession
			return a.messages.Update(genCtx, *currentAssistant)
		},
		StopWhen: []fantasy.StopCondition{
			func(steps []fantasy.StepResult) bool {
				cw := int64(largeModel.CatwalkCfg.ContextWindow)
				// If context window is unknown (0), skip auto-summarize
				// to avoid immediately truncating custom/local models.
				if cw == 0 {
					return false
				}
				// Use the most recent step's input usage as a proxy for the
				// current context size. Cumulative session tokens grow
				// monotonically and would falsely trigger summarization even
				// when the active context is small.
				var tokens int64
				if len(steps) > 0 {
					last := steps[len(steps)-1]
					tokens = last.Usage.InputTokens + last.Usage.CacheReadTokens + last.Usage.CacheCreationTokens + last.Usage.OutputTokens
				}
				// Fall back to cumulative session totals only when no step
				// usage is available (e.g., first call in some providers).
				if tokens == 0 {
					tokens = currentSession.CompletionTokens + currentSession.PromptTokens
				}

				remaining := cw - tokens
				var threshold int64
				if cw > largeContextWindowThreshold {
					threshold = largeContextWindowBuffer
				} else {
					threshold = int64(float64(cw) * smallContextWindowRatio)
				}
				if (remaining <= threshold) && !a.disableAutoSummarize {
					shouldSummarize = true
					return true
				}
				return false
			},
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
		},
	})
	if err != nil {
		isHyper := largeModel.ModelCfg.Provider == hyper.Name
		isCancelErr := errors.Is(err, context.Canceled)
		if currentAssistant == nil {
			// Cancel-before-assistant-creation window: the run was
			// canceled after activeRequests.Set but before PrepareStep
			// created the assistant message. Without this, the turn
			// would return with no FinishReasonCanceled marker and no
			// user-visible record. The user message was already created
			// above, so persistCanceledTurn only writes the assistant
			// record.
			if isCancelErr {
				if persistErr := a.persistCanceledTurn(ctx, call, userMsgCreated); persistErr != nil {
					return nil, persistErr
				}
			}
			return result, err
		}
		// Persist final state with a context detached from the run
		// context. The run context (ctx) is derived from the
		// workspace context, which workspace shutdown cancels before
		// agent goroutines finish; using ctx here would drop the
		// final assistant state. WithoutCancel keeps the values
		// (e.g. session ID) while ignoring cancellation, and a short
		// timeout bounds the cleanup writes.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		// Ensure we finish thinking on error to close the reasoning state.
		currentAssistant.FinishThinking()
		toolCalls := currentAssistant.ToolCalls()
		// INFO: we use the cleanup context here because the genCtx has been cancelled.
		msgs, createErr := a.messages.List(cleanupCtx, currentAssistant.SessionID)
		if createErr != nil {
			return nil, createErr
		}
		for _, tc := range toolCalls {
			if !tc.Finished {
				tc.Finished = true
				tc.Input = "{}"
				currentAssistant.AddToolCall(tc)
				updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
				if updateErr != nil {
					return nil, updateErr
				}
			}

			found := false
			for _, msg := range msgs {
				if msg.Role == message.Tool {
					for _, tr := range msg.ToolResults() {
						if tr.ToolCallID == tc.ID {
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
			if found {
				continue
			}
			content := "There was an error while executing the tool"
			if isCancelErr {
				content = "Error: user cancelled assistant tool calling"
			}
			toolResult := message.ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    content,
				IsError:    true,
			}
			_, createErr = a.messages.Create(cleanupCtx, currentAssistant.SessionID, message.CreateMessageParams{
				Role: message.Tool,
				Parts: []message.ContentPart{
					toolResult,
				},
			})
			if createErr != nil {
				return nil, createErr
			}
		}
		var providerErr *fantasy.ProviderError
		const defaultTitle = "Provider Error"
		linkStyle := lipgloss.NewStyle().Foreground(charmtone.Guac).Underline(true)
		if isCancelErr {
			currentAssistant.AddFinish(message.FinishReasonCanceled, "User canceled request", "")
		} else if isHyper && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusUnauthorized {
			currentAssistant.AddFinish(message.FinishReasonError, "Unauthorized", `Please re-authenticate with Hyper. You can also run "crush auth" to re-authenticate.`)
		} else if isHyper && errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusPaymentRequired {
			url := hyper.BaseURL()
			link := linkStyle.Hyperlink(url, "id=hyper").Render(url)
			currentAssistant.AddFinish(message.FinishReasonError, "No credits", "You're out of credits. Add more at "+link)
		} else if errors.As(err, &providerErr) {
			if providerErr.Message == "The requested model is not supported." {
				url := "https://github.com/settings/copilot/features"
				link := linkStyle.Hyperlink(url, "id=copilot").Render(url)
				currentAssistant.AddFinish(
					message.FinishReasonError,
					"Copilot model not enabled",
					fmt.Sprintf("%q is not enabled in Copilot. Go to the following page to enable it. Then, wait 5 minutes before trying again. %s", largeModel.CatwalkCfg.Name, link),
				)
			} else {
				currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(providerErr.Title), defaultTitle), providerErr.Message)
			}
		} else if fantasyErr, ok := errors.AsType[*fantasy.Error](err); ok {
			currentAssistant.AddFinish(message.FinishReasonError, cmp.Or(stringext.Capitalize(fantasyErr.Title), defaultTitle), fantasyErr.Message)
		} else {
			currentAssistant.AddFinish(message.FinishReasonError, defaultTitle, err.Error())
		}
		// Note: we use the cleanup context here because the genCtx has been
		// cancelled.
		updateErr := a.messages.Update(cleanupCtx, *currentAssistant)
		if updateErr != nil {
			return nil, updateErr
		}
		return nil, err
	}

	if shouldSummarize {
		a.clearActiveRequest(call.SessionID)
		if summarizeErr := a.Summarize(genCtx, call.SessionID, call.ProviderOptions); summarizeErr != nil {
			return nil, summarizeErr
		}
		// If the agent wasn't done...
		if len(currentAssistant.ToolCalls()) > 0 {
			call.Prompt = fmt.Sprintf("The previous session was interrupted because it got too long, the initial user request was: `%s`", call.Prompt)
			a.messageQueue.Update(call.SessionID, func(existing []SessionAgentCall, _ bool) ([]SessionAgentCall, bool) {
				return append(existing, call), true
			})
		}
	}

	// Release active request before publishing the notification.
	// TUI handlers poll IsSessionBusy() and only re-evaluate when a
	// tea.Msg arrives, so the cleanup must precede the notify or
	// subscribers see stale busy state at the moment of receipt.
	a.clearActiveRequest(call.SessionID)
	cancel()

	// Send notification that agent has finished its turn (skip for
	// nested/non-interactive sessions).
	if !call.NonInteractive && a.notify != nil {
		// Stamp the session's most recent run completion so read/unread
		// state can be computed: a session is unread when it finished a
		// run more recently than the viewing client last opened it. Only
		// interactive turns count (nested/title/task sessions do not
		// surface in the picker). Best-effort; a failure here must not
		// disrupt the turn's terminal handling.
		if a.sessions != nil {
			if err := a.sessions.MarkFinished(ctx, call.SessionID); err != nil {
				slog.Debug("Failed to mark session finished", "session_id", call.SessionID, "error", err)
			}
		}
		a.notify.Publish(pubsub.CreatedEvent, notify.Notification{
			SessionID:    call.SessionID,
			SessionTitle: currentSession.Title,
			Type:         notify.TypeAgentFinished,
		})
	}

	// Hand off to the next queued prompt (if any) under dispatchMu so
	// the transition from this finished run to the queued run is atomic
	// against a concurrent Cancel. activeRequests for this session was
	// just deleted above, so without the lock there is a window in
	// which the session looks idle and a cancel becomes a no-op that
	// fails to stop the queued prompt. Holding the lock lets us observe
	// a pending cancel recorded against the session and drop the queue
	// instead of running it, and (for the recursion) hand a fresh
	// accept reservation to the dequeued call so acceptedRuns stays > 0
	// across the recursive Run's own dispatch handoff — keeping the
	// session observable to Cancel for the entire transition and
	// closing the dequeue -> re-register window.
	mu := a.sessionMu(call.SessionID)
	mu.Lock()
	queuedMessages, _ := a.messageQueue.Get(call.SessionID)
	if mark, ok := a.cancelMark.Get(call.SessionID); ok && mark > 0 && len(queuedMessages) > 0 {
		// A cancel was recorded for this session (e.g. it arrived while
		// this run was active and follow-ups had been queued). Drop the
		// queued prompts it covers (accept sequence at or below the
		// mark, or untracked); keep any queued after the cancel (higher
		// sequence) so they still run.
		var kept []SessionAgentCall
		var canceledRunIDDrops []SessionAgentCall
		for _, q := range queuedMessages {
			if q.acceptSeq == 0 || q.acceptSeq <= mark {
				if q.RunID != "" {
					canceledRunIDDrops = append(canceledRunIDDrops, q)
				}
				continue
			}
			kept = append(kept, q)
		}
		queuedMessages = kept
		a.messageQueue.Set(call.SessionID, kept)
		// A dropped prompt carrying a RunID must still publish its
		// terminal cancelled RunComplete so a caller waiting on that
		// RunID does not hang.
		a.publishCanceledQueueDrops(canceledRunIDDrops)
	}
	if len(queuedMessages) == 0 {
		// No queued work. Clear the cancel mark only when no accepted
		// run remains in flight that it might still cover; otherwise a
		// sibling prompt (sequence at or below the mark) waiting to
		// enter Run would lose its cancellation. When accepted runs are
		// gone, this also clears a stale mark so it can't catch a
		// future run.
		a.messageQueue.Del(call.SessionID)
		a.acceptedMu.Lock()
		inFlight, _ := a.acceptedRuns.Get(call.SessionID)
		a.acceptedMu.Unlock()
		if inFlight == 0 {
			a.cancelMark.Del(call.SessionID)
		}
		mu.Unlock()
		return result, err
	}
	// There are queued messages, restart the loop. Suppress the outer
	// defer's emit: it would otherwise observe the recursive Run's retErr
	// (named-return clobbering through the return below) against this
	// turn's MessageID/Text and publish a mixed, racing event.
	skipRunComplete = true
	// Decide whether this turn still owes its own terminal RunComplete.
	// Each submitted prompt with a RunID has its own lifecycle, so a turn
	// that is finished and handing off to a *different* queued prompt must
	// publish its own RunComplete here — leaving it to the recursive turn
	// (which carries a different RunID) would hang a caller waiting on
	// this turn's RunID. The exception is the summarize-continuation path,
	// which re-queues this same call (same RunID) to resume after a
	// summary; in that case the eventual terminal turn for this RunID
	// publishes, so publishing now would double-emit.
	outerOwesRunComplete := call.RunID != ""
	if outerOwesRunComplete {
		for _, q := range queuedMessages {
			if q.RunID == call.RunID {
				outerOwesRunComplete = false
				break
			}
		}
	}
	firstQueuedMessage := queuedMessages[0]
	a.messageQueue.Set(call.SessionID, queuedMessages[1:])
	// Reserve a fresh accept for the dequeued prompt before dropping the
	// lock so acceptedRuns > 0 across the handoff into the recursive
	// Run. This closes the window between this dequeue and the recursive
	// Run registering its activeRequests entry: a cancel arriving in
	// that window now records a pending cancel (acceptedRuns > 0) that
	// the recursive Run's accepted path observes as cancel-on-entry.
	firstQueuedMessage.Accepted = a.BeginAccepted(call.SessionID)
	mu.Unlock()
	if outerOwesRunComplete {
		complete := notify.RunComplete{SessionID: call.SessionID, RunID: call.RunID}
		if currentAssistant != nil {
			complete.MessageID = currentAssistant.ID
			complete.Text = currentAssistant.Content().String()
		}
		if ctx.Err() != nil {
			complete.Cancelled = true
		}
		a.publishRunComplete(ctx, call, complete)
	}
	return a.Run(ctx, firstQueuedMessage)
}

// popQueuedCall atomically removes and returns the head of the queued
// prompts for the session. Returns false if the queue is empty. Atomicity
// matters: a Get followed by a Set would race with a concurrent enqueue and
// either drop the new entry or the popped tail.
func providerRetryLogFields(err *fantasy.ProviderError, delay time.Duration) []any {
	fields := []any{
		"retry_delay", delay.String(),
	}
	if err == nil {
		return fields
	}
	fields = append(fields, "status_code", err.StatusCode)
	if err.Title != "" {
		fields = append(fields, "title", err.Title)
	}
	if err.Message != "" {
		fields = append(fields, "message", err.Message)
	}
	return fields
}
