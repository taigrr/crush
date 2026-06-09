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
// of role — user, assistant, tool calls all count) between milestones.
const milestoneInterval = 10

// milestoneBoundaries returns the milestone turn numbers that should be
// generated for the half-open range (afterTurn, totalTurns]. Boundaries
// are exact multiples of milestoneInterval, so milestones land
// consistently at turns 10, 20, 30, … regardless of how many messages a
// single run emits. Returning every crossed boundary (rather than just
// the latest) is what prevents large batches from being skipped when one
// run jumps past several boundaries at once.
func milestoneBoundaries(afterTurn int64, totalTurns int) []int64 {
	var boundaries []int64
	first := ((afterTurn / milestoneInterval) + 1) * milestoneInterval
	for turn := first; turn <= int64(totalTurns); turn += milestoneInterval {
		boundaries = append(boundaries, turn)
	}
	return boundaries
}

// generateMilestones produces a milestone summary for every
// milestoneInterval boundary in (afterTurn, totalTurns]. It is the single
// entry point for both the initial backfill (afterTurn == 0) and
// incremental generation. Because a single Run can emit many messages and
// cross several boundaries at once, this loops over every crossed boundary
// instead of generating a single milestone, so batches are never skipped.
// Milestones are generated sequentially so each can reference the prior
// summary for continuity. Designed to run as a background goroutine.
func (a *sessionAgent) generateMilestones(ctx context.Context, sessionID string, afterTurn int64, totalTurns int, msgs []message.Message, priorSummary string) {
	if a.milestones == nil {
		return
	}

	smallModel := a.smallModel.Get()
	systemPromptPrefix := a.systemPromptPrefix.Get()

	for _, turn := range milestoneBoundaries(afterTurn, totalTurns) {
		// Messages covered by this boundary: the milestoneInterval-sized
		// window ending at this turn, clamped to the available messages.
		chunkStart := int(turn) - milestoneInterval
		if chunkStart < 0 {
			chunkStart = 0
		}
		chunkEnd := min(int(turn), len(msgs))
		if chunkStart > chunkEnd {
			chunkStart = chunkEnd
		}
		chunk := msgs[chunkStart:chunkEnd]

		prompt := buildBackfillPrompt(chunk, priorSummary, int(turn))

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
			slog.Error("Failed to generate milestone", "error", err, "session_id", sessionID, "turn", turn)
			return
		}

		if resp == nil || resp.Response.Content.Text() == "" {
			slog.Debug("Empty milestone response", "session_id", sessionID, "turn", turn)
			continue
		}

		short, full := parseMilestoneResponse(resp.Response.Content.Text())
		if short == "" || full == "" {
			slog.Debug("Could not parse milestone response", "session_id", sessionID, "turn", turn, "text", resp.Response.Content.Text())
			continue
		}

		if _, err := a.milestones.Create(ctx, sessionID, turn, short, full); err != nil {
			slog.Error("Failed to save milestone", "error", err, "session_id", sessionID, "turn", turn)
			return
		}
		priorSummary = full
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
