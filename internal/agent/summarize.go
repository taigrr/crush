package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/milestone"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/anthropic"
)

//go:embed templates/summary.md
var summaryPrompt []byte

func (a *sessionAgent) Summarize(ctx context.Context, sessionID string, model *Model, opts fantasy.ProviderOptions) error {
	if a.IsSessionBusy(sessionID) {
		return ErrSessionBusy
	}

	// Summarize on the model the turn ran on when the caller passes one
	// (a per-call override), so the context window that triggered the
	// summary and the model that writes it agree.
	largeModel := a.largeModel.Get()
	if model != nil && model.Model != nil {
		largeModel = *model
	}
	systemPromptPrefix := a.systemPromptPrefix.Get()

	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		// Nothing to summarize.
		return nil
	}

	aiMsgs, _ := a.preparePrompt(msgs, largeModel.CatwalkCfg.SupportsImages)

	genCtx, cancel := context.WithCancel(ctx)
	a.activeRequests.Set(sessionID, cancel)
	defer a.clearActiveRequest(sessionID)
	defer cancel()
	defer func() {
		if flushErr := a.messages.FlushAll(ctx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after summarize", "error", flushErr)
		}
	}()

	agent := fantasy.NewAgent(
		largeModel.Model,
		fantasy.WithSystemPrompt(string(summaryPrompt)),
		fantasy.WithUserAgent(userAgent),
	)
	summaryMessage, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:             message.Assistant,
		Model:            largeModel.ModelCfg.Model,
		Provider:         largeModel.ModelCfg.Provider,
		IsSummaryMessage: true,
	})
	if err != nil {
		return err
	}

	// Fetch milestones to include in summarization context.
	var sessionMilestones []milestone.Milestone
	if a.milestones != nil {
		if ms, err := a.milestones.List(ctx, sessionID); err == nil {
			sessionMilestones = ms
		}
	}

	summaryPromptText := buildSummaryPrompt(currentSession.Todos, sessionMilestones)

	resp, err := agent.Stream(genCtx, fantasy.AgentStreamCall{
		Prompt:          summaryPromptText,
		Messages:        aiMsgs,
		ProviderOptions: opts,
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = options.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(systemPromptPrefix)}, prepared.Messages...)
			}
			return callContext, prepared, nil
		},
		OnReasoningDelta: func(id string, text string) error {
			summaryMessage.AppendReasoningContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// Handle anthropic signature.
			if anthropicData, ok := reasoning.ProviderMetadata["anthropic"]; ok {
				if signature, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok && signature.Signature != "" {
					summaryMessage.AppendReasoningSignature(signature.Signature)
				}
			}
			summaryMessage.FinishThinking()
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnTextDelta: func(id, text string) error {
			summaryMessage.AppendContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
	})
	if err != nil {
		isCancelErr := errors.Is(err, context.Canceled)
		if isCancelErr {
			// User cancelled summarize we need to remove the summary message.
			deleteErr := a.messages.Delete(ctx, summaryMessage.ID)
			return deleteErr
		}
		// Mark the summary message as finished with an error so the UI
		// stops spinning.
		summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
		if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
			return updateErr
		}
		return err
	}

	summaryMessage.AddFinish(message.FinishReasonEndTurn, "", "")
	err = a.messages.Update(genCtx, summaryMessage)
	if err != nil {
		return err
	}

	var openrouterCost *float64
	for _, step := range resp.Steps {
		stepCost := a.openrouterCost(step.ProviderMetadata)
		if stepCost != nil {
			newCost := *stepCost
			if openrouterCost != nil {
				newCost += *openrouterCost
			}
			openrouterCost = &newCost
		}
	}

	a.updateSessionUsage(largeModel, &currentSession, resp.TotalUsage, openrouterCost, false)

	// Just in case, get just the last usage info.
	usage := resp.Response.Usage
	currentSession.SummaryMessageID = summaryMessage.ID
	currentSession.CompletionTokens = summaryCompletionTokens(usage, summaryMessage)
	currentSession.PromptTokens = 0
	currentSession.EstimatedUsage = usageIsZero(usage)
	_, err = a.sessions.Save(genCtx, currentSession)
	if err != nil {
		return err
	}

	// Release the active request before processing queued messages so that
	// Run() does not see the session as busy.
	a.clearActiveRequest(sessionID)
	cancel()

	// Process any messages that were queued while summarizing. A
	// draining server leaves them for its successor.
	if a.dispatchPaused.Load() {
		return nil
	}
	next, ok := a.popQueuedCall(sessionID)
	if !ok {
		return nil
	}
	slog.Debug("Restarting agent loop with queued prompt after summarize", "session_id", sessionID)
	_, qErr := a.Run(ctx, next)
	return qErr
}

func summaryCompletionTokens(usage fantasy.Usage, summaryMessage message.Message) int64 {
	if usage.OutputTokens != 0 {
		return usage.OutputTokens
	}
	return approxTokenCount(summaryMessage.Content().Text) + approxTokenCount(summaryMessage.ReasoningContent().String())
}

func buildSummaryPrompt(todos []session.Todo, milestones []milestone.Milestone) string {
	var sb strings.Builder
	sb.WriteString("Provide a detailed summary of our conversation above.")

	if len(milestones) > 0 {
		sb.WriteString("\n\n## Session Milestones (key events so far)\n\n")
		for _, m := range milestones {
			fmt.Fprintf(&sb, "- **%s** (turn %d): %s\n", m.ShortSummary, m.TurnNumber, m.FullSummary)
		}
		sb.WriteString("\nUse these milestones to identify the most important work. Prioritize summarizing these outcomes.")
	}

	if len(todos) > 0 {
		sb.WriteString("\n\n## Current Todo List\n\n")
		for _, t := range todos {
			fmt.Fprintf(&sb, "- [%s] %s\n", t.Status, t.Content)
		}
		sb.WriteString("\nInclude these tasks and their statuses in your summary. ")
		sb.WriteString("Instruct the resuming assistant to use the `todos` tool to continue tracking progress on these tasks.")
	}
	return sb.String()
}
