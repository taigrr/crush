package agent

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

//go:embed templates/milestone.md
var milestonePrompt []byte

// milestoneInterval is the number of turns (total messages regardless
// of role) between milestone generation.
const milestoneInterval = 10

// generateMilestone runs the small model to produce a milestone summary
// for the session. It is designed to run as a background goroutine,
// similar to generateTitle.
func (a *sessionAgent) generateMilestone(ctx context.Context, sessionID string, userMsgCount int, msgs []message.Message, latestPrompt string) {
	if a.milestones == nil {
		return
	}

	smallModel := a.smallModel.Get()
	systemPromptPrefix := a.systemPromptPrefix.Get()

	// Gather the prior milestone for context.
	var priorSummary string
	latest, err := a.milestones.Latest(ctx, sessionID)
	if err == nil {
		priorSummary = latest.FullSummary
	}

	// Build the prompt with messages since last milestone.
	prompt := buildMilestonePrompt(msgs, latestPrompt, priorSummary, latest.TurnNumber)

	agent := fantasy.NewAgent(
		smallModel.Model,
		fantasy.WithSystemPrompt(string(milestonePrompt)+"\n /no_think"),
		fantasy.WithMaxOutputTokens(300),
		fantasy.WithUserAgent(userAgent),
	)

	resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
		Prompt: prompt,
		PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = opts.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{
					fantasy.NewSystemMessage(systemPromptPrefix),
				}, prepared.Messages...)
			}
			return callCtx, prepared, nil
		},
	})
	if err != nil {
		slog.Error("Failed to generate milestone", "error", err, "session_id", sessionID)
		return
	}

	if resp == nil || resp.Response.Content.Text() == "" {
		slog.Debug("Empty milestone response", "session_id", sessionID)
		return
	}

	short, full := parseMilestoneResponse(resp.Response.Content.Text())
	if short == "" || full == "" {
		slog.Debug("Could not parse milestone response", "session_id", sessionID, "text", resp.Response.Content.Text())
		return
	}

	_, err = a.milestones.Create(ctx, sessionID, int64(userMsgCount), short, full)
	if err != nil {
		slog.Error("Failed to save milestone", "error", err, "session_id", sessionID)
	}
}

// backfillMilestones generates milestones for every milestoneInterval
// chunk from the beginning of the conversation. Each milestone is
// generated sequentially so that later ones can reference the prior
// summary for continuity.
func (a *sessionAgent) backfillMilestones(ctx context.Context, sessionID string, msgs []message.Message, latestPrompt string) {
	if a.milestones == nil {
		return
	}

	totalTurns := len(msgs) + 1
	smallModel := a.smallModel.Get()
	systemPromptPrefix := a.systemPromptPrefix.Get()

	var priorSummary string
	for turn := milestoneInterval; turn <= totalTurns; turn += milestoneInterval {
		// Build prompt from messages in this chunk.
		chunkStart := turn - milestoneInterval
		chunkEnd := turn
		if chunkEnd > len(msgs) {
			chunkEnd = len(msgs)
		}
		chunk := msgs[chunkStart:chunkEnd]

		prompt := buildBackfillPrompt(chunk, priorSummary, turn)

		agent := fantasy.NewAgent(
			smallModel.Model,
			fantasy.WithSystemPrompt(string(milestonePrompt)+"\n /no_think"),
			fantasy.WithMaxOutputTokens(300),
			fantasy.WithUserAgent(userAgent),
		)

		resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
			Prompt: prompt,
			PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
				prepared.Messages = opts.Messages
				if systemPromptPrefix != "" {
					prepared.Messages = append([]fantasy.Message{
						fantasy.NewSystemMessage(systemPromptPrefix),
					}, prepared.Messages...)
				}
				return callCtx, prepared, nil
			},
		})
		if err != nil {
			slog.Error("Failed to generate backfill milestone", "error", err, "session_id", sessionID, "turn", turn)
			return
		}

		if resp == nil || resp.Response.Content.Text() == "" {
			continue
		}

		short, full := parseMilestoneResponse(resp.Response.Content.Text())
		if short == "" || full == "" {
			continue
		}

		_, err = a.milestones.Create(ctx, sessionID, int64(turn), short, full)
		if err != nil {
			slog.Error("Failed to save backfill milestone", "error", err, "session_id", sessionID, "turn", turn)
			return
		}
		priorSummary = full
	}

	// Also generate one for the current turn if it's not on the interval.
	if totalTurns%milestoneInterval != 0 {
		chunkStart := (totalTurns / milestoneInterval) * milestoneInterval
		chunk := msgs[chunkStart:]

		prompt := buildBackfillPrompt(chunk, priorSummary, totalTurns)

		agent := fantasy.NewAgent(
			smallModel.Model,
			fantasy.WithSystemPrompt(string(milestonePrompt)+"\n /no_think"),
			fantasy.WithMaxOutputTokens(300),
			fantasy.WithUserAgent(userAgent),
		)

		resp, err := agent.Stream(ctx, fantasy.AgentStreamCall{
			Prompt: prompt,
			PrepareStep: func(callCtx context.Context, opts fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
				prepared.Messages = opts.Messages
				if systemPromptPrefix != "" {
					prepared.Messages = append([]fantasy.Message{
						fantasy.NewSystemMessage(systemPromptPrefix),
					}, prepared.Messages...)
				}
				return callCtx, prepared, nil
			},
		})
		if err != nil {
			slog.Error("Failed to generate backfill milestone (trailing)", "error", err, "session_id", sessionID)
			return
		}

		if resp != nil && resp.Response.Content.Text() != "" {
			short, full := parseMilestoneResponse(resp.Response.Content.Text())
			if short != "" && full != "" {
				if _, err := a.milestones.Create(ctx, sessionID, int64(totalTurns), short, full); err != nil {
					slog.Error("Failed to save trailing backfill milestone", "error", err, "session_id", sessionID)
				}
			}
		}
	}
}

// buildBackfillPrompt constructs the prompt for a single backfill chunk.
func buildBackfillPrompt(chunk []message.Message, priorSummary string, turn int) string {
	var b strings.Builder

	if priorSummary != "" {
		b.WriteString("## Previous Milestone Summary\n\n")
		b.WriteString(priorSummary)
		b.WriteString("\n\n")
	}

	b.WriteString("## Messages in This Segment\n\n")
	for _, msg := range chunk {
		content := msg.Content().String()
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		switch msg.Role {
		case message.User:
			fmt.Fprintf(&b, "**User**: %s\n\n", content)
		case message.Assistant:
			fmt.Fprintf(&b, "**Assistant**: %s\n\n", content)
		default:
			fmt.Fprintf(&b, "**%s**: %s\n\n", msg.Role, content)
		}
	}

	fmt.Fprintf(&b, "This covers turns up to turn %d. Now generate the milestone summary.", turn)
	return b.String()
}

// buildMilestonePrompt constructs the user prompt for milestone
// generation including recent messages and prior context.
func buildMilestonePrompt(msgs []message.Message, latestPrompt, priorSummary string, lastMilestoneTurn int64) string {
	var b strings.Builder

	if priorSummary != "" {
		b.WriteString("## Previous Milestone Summary\n\n")
		b.WriteString(priorSummary)
		b.WriteString("\n\n")
	}

	b.WriteString("## Messages Since Last Milestone\n\n")

	// Include the last milestoneInterval messages (any role).
	var userCount int
	startIdx := 0
	if lastMilestoneTurn > 0 && len(msgs) > milestoneInterval {
		startIdx = len(msgs) - milestoneInterval
	}

	for i := startIdx; i < len(msgs); i++ {
		msg := msgs[i]
		switch msg.Role {
		case message.User:
			userCount++
			content := msg.Content().String()
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			fmt.Fprintf(&b, "**User [%d]**: %s\n\n", userCount, content)
		case message.Assistant:
			content := msg.Content().String()
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			b.WriteString("**Assistant**: ")
			b.WriteString(content)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Most Recent User Message\n\n")
	b.WriteString(latestPrompt)
	b.WriteString("\n\n")
	b.WriteString("Now generate the milestone summary.")

	return b.String()
}

// parseMilestoneResponse extracts the SHORT and FULL sections from the
// model's response.
func parseMilestoneResponse(text string) (short, full string) {
	text = strings.TrimSpace(text)

	// Remove any think tags.
	text = thinkTagRegex.ReplaceAllString(text, "")
	text = orphanThinkTagRegex.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "SHORT:"); ok {
			short = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "FULL:"); ok {
			full = strings.TrimSpace(after)
		}
	}

	// If FULL spans multiple lines (after the FULL: prefix), collect them.
	if full == "" {
		inFull := false
		var fullLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if after, ok := strings.CutPrefix(trimmed, "FULL:"); ok {
				inFull = true
				rest := strings.TrimSpace(after)
				if rest != "" {
					fullLines = append(fullLines, rest)
				}
				continue
			}
			if inFull && trimmed != "" {
				fullLines = append(fullLines, trimmed)
			}
		}
		if len(fullLines) > 0 {
			full = strings.Join(fullLines, " ")
		}
	}

	return short, full
}
