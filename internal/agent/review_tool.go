package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/taigrr/fantasy"
	"golang.org/x/sync/errgroup"

	"github.com/taigrr/crush/internal/agent/prompt"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
)

//go:embed templates/review_tool.md
var reviewToolDescription string

const (
	ReviewToolName = "review"
	// reviewerCount is the fixed number of adversarial reviewers spawned
	// in parallel per review call.
	reviewerCount = 2
)

type ReviewParams struct {
	Diff  string `json:"diff" description:"The full diff (or code) to review. Required."`
	Goal  string `json:"goal" description:"The original goal/intent of the change, for the reviewers' context. Optional."`
	Focus string `json:"focus" description:"Specific areas or concerns the reviewers should focus on. Optional."`
}

// reviewVariant describes a per-reviewer framing. Varying the input
// ordering and emphasis between the two reviewers decorrelates their
// attention: identical prompts to a near-deterministic model tend to
// surface the same findings, so we deliberately send each reviewer down
// a different path — different scan order, different framing, and (for
// the second) the diff's file sections reversed.
type reviewVariant struct {
	intro        string
	diffLast     bool // place the diff after goal/focus (natural) vs. first.
	reverseFiles bool // reverse the order of file sections in the diff.
}

var reviewVariants = [reviewerCount]reviewVariant{
	{
		intro:        "Review the following code change adversarially. Assume it is wrong and find the bugs. Read the change top to bottom, in the order presented.",
		diffLast:     true,
		reverseFiles: false,
	},
	{
		intro:        "You are reviewing a code change that is presented with its files in reverse order. Assume every line is a latent bug until you prove otherwise. Start from the last change and work backward; do not trust the author's intent.",
		diffLast:     false,
		reverseFiles: true,
	},
}

// reverseDiffFiles splits a unified diff at "diff --git" boundaries and
// reverses the order of the file sections, leaving each file's hunks
// intact. Any preamble before the first file boundary is preserved at
// the top. If no boundaries are found, the diff is returned unchanged.
func reverseDiffFiles(diff string) string {
	const sep = "diff --git "
	idx := strings.Index(diff, sep)
	if idx == -1 {
		return diff
	}
	preamble := diff[:idx]
	rest := diff[idx:]

	var sections []string
	for len(rest) > 0 {
		next := strings.Index(rest[len(sep):], sep)
		if next == -1 {
			sections = append(sections, rest)
			break
		}
		cut := len(sep) + next
		sections = append(sections, rest[:cut])
		rest = rest[cut:]
	}

	slices.Reverse(sections)
	return preamble + strings.Join(sections, "")
}

// buildReviewPrompt assembles the per-call prompt handed to reviewer
// number variant (0-indexed).
func buildReviewPrompt(params ReviewParams, variant int) string {
	v := reviewVariants[variant%len(reviewVariants)]

	diff := params.Diff
	if v.reverseFiles {
		diff = reverseDiffFiles(diff)
	}

	writeContext := func(sb *strings.Builder) {
		if strings.TrimSpace(params.Goal) != "" {
			sb.WriteString("<goal>\n")
			sb.WriteString(params.Goal)
			sb.WriteString("\n</goal>\n\n")
		}
		if strings.TrimSpace(params.Focus) != "" {
			sb.WriteString("<focus>\n")
			sb.WriteString(params.Focus)
			sb.WriteString("\n</focus>\n\n")
		}
	}
	writeDiff := func(sb *strings.Builder) {
		sb.WriteString("<diff>\n")
		sb.WriteString(diff)
		sb.WriteString("\n</diff>\n")
	}

	var sb strings.Builder
	sb.WriteString(v.intro)
	sb.WriteString("\n\n")
	if v.diffLast {
		writeContext(&sb)
		writeDiff(&sb)
	} else {
		writeDiff(&sb)
		sb.WriteString("\n")
		writeContext(&sb)
	}
	return sb.String()
}

// reviewTool spawns reviewerCount adversarial reviewer sub-agents in
// parallel. Each reviewer runs in its own isolated session and receives
// only the diff (and optional goal/focus) — not the coder's reasoning.
// Both reports are returned so the coder, which retains the original
// goal and full context, can apply the fixes.
func (c *coordinator) reviewTool(ctx context.Context) (fantasy.AgentTool, error) {
	agentCfg, ok := c.cfg.Config().Agents[config.AgentReviewer]
	if !ok {
		return nil, errors.New("reviewer agent not configured")
	}
	p, err := reviewerPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	// Build the reviewer agent once. Like the task agent, the underlying
	// SessionAgent is safe to run concurrently across distinct sessions,
	// which is what lets the two reviewers run in parallel.
	agent, err := c.buildAgent(ctx, p, agentCfg, true)
	if err != nil {
		return nil, err
	}

	return fantasy.NewParallelAgentTool(
		ReviewToolName,
		reviewToolDescription,
		func(ctx context.Context, params ReviewParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.Diff) == "" {
				return fantasy.NewTextErrorResponse("diff is required"), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			results := make([]string, reviewerCount)
			g, gctx := errgroup.WithContext(ctx)
			for i := range reviewerCount {
				g.Go(func() error {
					resp, err := c.runSubAgent(gctx, subAgentParams{
						Agent:          agent,
						SessionID:      sessionID,
						AgentMessageID: agentMessageID,
						ToolCallID:     fmt.Sprintf("%s-review-%d", call.ID, i+1),
						Prompt:         buildReviewPrompt(params, i),
						SessionTitle:   fmt.Sprintf("Adversarial Review %d", i+1),
					})
					if err != nil {
						return err
					}
					if resp.IsError {
						return fmt.Errorf("reviewer %d failed: %s", i+1, resp.Content)
					}
					results[i] = resp.Content
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Review failed: %s", err)), nil
			}

			var sb strings.Builder
			for i, r := range results {
				fmt.Fprintf(&sb, "## Reviewer %d\n\n%s\n\n", i+1, strings.TrimSpace(r))
			}
			return fantasy.NewTextResponse(strings.TrimSpace(sb.String())), nil
		},
	), nil
}
