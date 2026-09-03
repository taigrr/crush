package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/agent/prompt"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/shell"
)

//go:embed templates/review_tool.md
var reviewToolDescription string

const (
	ReviewToolName = "review"
	// ReviewerCount is the fixed number of adversarial reviewers spawned
	// in parallel per review call. Exported so UI code that reconstructs
	// per-reviewer groups from persisted sessions can bound its scan to
	// the actual fan-out.
	ReviewerCount = 2
	// reviewerCount is the unexported alias used internally.
	reviewerCount = ReviewerCount
	// maxReviewPayloadBytes caps the size of the change fed to reviewers.
	maxReviewPayloadBytes = 400_000
)

// reviewSubToolCallSuffix matches the "-review-N" suffix appended to a
// review tool call's ID to derive a distinct child session per
// reviewer. The UI strips it to map reviewer sub-agent events back to
// the single parent review tool call.
var reviewSubToolCallSuffix = regexp.MustCompile(`-review-\d+$`)

// reviewSubToolCallID derives the child tool-call ID (and thus child
// session ID) for reviewer i (0-indexed) under a review tool call.
func reviewSubToolCallID(parentCallID string, i int) string {
	return ReviewSubToolCallID(parentCallID, i)
}

// ReviewSubToolCallID derives the child tool-call ID (and thus child
// session ID) for reviewer i (0-indexed) under a review tool call. The
// UI uses it to enumerate reviewer sessions when reloading from the DB.
func ReviewSubToolCallID(parentCallID string, i int) string {
	return fmt.Sprintf("%s-review-%d", parentCallID, i+1)
}

// StripReviewSuffix removes the reviewer suffix added by
// [reviewSubToolCallID], returning the parent review tool call's ID. If
// there is no such suffix the input is returned unchanged.
func StripReviewSuffix(toolCallID string) string {
	return reviewSubToolCallSuffix.ReplaceAllString(toolCallID, "")
}

// ReviewerIndexFromToolCallID extracts the 0-based reviewer index from a
// reviewer child tool-call ID (e.g. "<parent>-review-2" -> 1, true).
// Returns ok=false when the ID carries no reviewer suffix.
func ReviewerIndexFromToolCallID(toolCallID string) (int, bool) {
	m := reviewSubToolCallSuffix.FindString(toolCallID)
	if m == "" {
		return 0, false
	}
	// m looks like "-review-N"; parse the trailing number.
	n, err := strconv.Atoi(m[len("-review-"):])
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true
}

type ReviewParams struct {
	// Command is a shell command whose stdout is the change to review
	// (e.g. "git diff $(git merge-base HEAD main)"). The harness runs it
	// and feeds the output to the reviewers, so the diff never has to
	// pass through the model. For un-versioned projects, write the
	// relevant files to a temp file and pass e.g. "cat /tmp/review.txt".
	Command string `json:"command" description:"Shell command whose stdout is the change to review, e.g. 'git diff $(git merge-base HEAD main)'. The harness runs it and passes the output to the reviewers. For projects without git, write the files to a temp file and pass 'cat /tmp/review.txt'. Required."`
	Goal    string `json:"goal,omitempty" description:"The original goal/intent of the change, for the reviewers' context. Optional."`
	Focus   string `json:"focus,omitempty" description:"Specific areas or concerns the reviewers should focus on. Optional."`
	// Model optionally picks the model both reviewers run on: a
	// configured role name, 'provider/model', or a bare id. A reviewer
	// from a different vendor than the writer catches a different class
	// of mistakes. Empty means the workspace's large model.
	Model string `json:"model,omitempty" description:"Optional model for the reviewers: a role name (large, small, orchestrator, or a configured role), 'provider/model', or a bare model id. Defaults to the workspace's large model. Prefer a different vendor than the one that wrote the change."`
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

// reverseDiffFiles splits a unified diff into per-file sections and
// reverses their order, leaving each file's hunks intact. Sections are
// delimited only by a "diff --git " that begins a line, so an added or
// removed line whose content happens to contain "diff --git " (e.g. a
// diff-of-a-diff) does not falsely split a hunk. Any preamble before
// the first file boundary is preserved at the top. If no boundaries are
// found, the diff is returned unchanged.
func reverseDiffFiles(diff string) string {
	const marker = "diff --git "

	// isBoundary reports whether marker begins at byte offset i, i.e. at
	// the very start of the string or immediately after a newline.
	isBoundary := func(i int) bool {
		if !strings.HasPrefix(diff[i:], marker) {
			return false
		}
		return i == 0 || diff[i-1] == '\n'
	}

	// Locate the first file boundary.
	first := -1
	for i := 0; i+len(marker) <= len(diff); i++ {
		if isBoundary(i) {
			first = i
			break
		}
	}
	if first == -1 {
		return diff
	}

	preamble := diff[:first]

	// Collect section start offsets.
	var starts []int
	for i := first; i+len(marker) <= len(diff); i++ {
		if isBoundary(i) {
			starts = append(starts, i)
		}
	}

	sections := make([]string, len(starts))
	for i, start := range starts {
		end := len(diff)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		sections[i] = diff[start:end]
	}

	slices.Reverse(sections)
	return preamble + strings.Join(sections, "")
}

// buildReviewPrompt assembles the per-call prompt handed to reviewer
// number variant (0-indexed), given the resolved change payload. The
// base ordering/framing cycles through reviewVariants; on top of that
// each reviewer is told its position in the panel so that, even when
// the panel is larger than the number of base variants (N reviewers >
// len(reviewVariants)), no two reviewers receive a byte-identical
// prompt — preserving decorrelation as the panel scales.
func buildReviewPrompt(params ReviewParams, change string, variant int) string {
	v := reviewVariants[variant%len(reviewVariants)]

	if v.reverseFiles {
		change = reverseDiffFiles(change)
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
		sb.WriteString(change)
		sb.WriteString("\n</diff>\n")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "You are reviewer %d of %d independent reviewers on this panel. Review on your own; do not assume another reviewer will catch what you skip.\n\n", variant+1, reviewerCount)
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
// parallel. The coder passes a command; the harness runs it and feeds
// its stdout (the diff or code) to the reviewers, so the change never
// passes through the model. Each reviewer runs in its own isolated
// session and receives only the change (and optional goal/focus) — not
// the coder's reasoning. Both reports are returned so the coder, which
// retains the original goal and full context, can apply the fixes.
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
			if strings.TrimSpace(params.Command) == "" {
				return fantasy.NewTextErrorResponse("command is required"), nil
			}

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			// Resolve the optional model before running the command so
			// a bad reference fails fast and cheaply.
			model, err := c.optionalModelRef(params.Model)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			// Run the command in the coder's working directory and use
			// its stdout as the change to review.
			sh := shell.NewShell(&shell.Options{WorkingDir: c.workingDir(ctx)})
			stdout, stderr, runErr := sh.Exec(ctx, params.Command)
			if runErr != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"Command failed: %s\n%s", runErr, strings.TrimSpace(stderr),
				)), nil
			}
			change := strings.TrimSpace(stdout)
			if change == "" {
				return fantasy.NewTextErrorResponse(
					"The command produced no output — nothing to review. Check that the command captures the full change (e.g. wrong diff base, or committing directly on the base branch).",
				), nil
			}
			if len(change) > maxReviewPayloadBytes {
				// Truncate on a UTF-8 rune boundary so we never emit a
				// split multibyte character into the reviewer prompt.
				cut := maxReviewPayloadBytes
				for cut > 0 && !utf8.RuneStart(change[cut]) {
					cut--
				}
				change = change[:cut] + "\n\n[... change truncated; review the largest/most relevant subset in a follow-up call ...]"
			}

			// Run reviewers in parallel. Each reviewer is independent: a
			// failure in one must not cancel the others or discard their
			// completed reports, so we collect per-reviewer outcomes
			// rather than using errgroup's fail-fast cancellation.
			results := make([]string, reviewerCount)
			failures := make([]bool, reviewerCount)
			var wg sync.WaitGroup
			for i := range reviewerCount {
				wg.Go(func() {
					resp, err := c.runSubAgent(ctx, subAgentParams{
						Agent:          agent,
						SessionID:      sessionID,
						AgentMessageID: agentMessageID,
						ToolCallID:     reviewSubToolCallID(call.ID, i),
						Prompt:         buildReviewPrompt(params, change, i),
						SessionTitle:   fmt.Sprintf("Adversarial Review %d", i+1),
						Model:          model,
					})
					switch {
					case err != nil:
						results[i] = fmt.Sprintf("_Reviewer failed: %s_", err)
						failures[i] = true
					case resp.IsError:
						results[i] = fmt.Sprintf("_Reviewer failed: %s_", resp.Content)
						failures[i] = true
					default:
						results[i] = resp.Content
					}
				})
			}
			wg.Wait()

			// Only fail the whole call if every reviewer failed.
			allFailed := true
			for _, f := range failures {
				if !f {
					allFailed = false
					break
				}
			}
			if allFailed {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"All reviewers failed. First error: %s", strings.TrimSpace(results[0]),
				)), nil
			}

			var sb strings.Builder
			for i, r := range results {
				fmt.Fprintf(&sb, "## Reviewer %d\n\n%s\n\n", i+1, strings.TrimSpace(r))
			}
			return fantasy.NewTextResponse(strings.TrimSpace(sb.String())), nil
		},
	), nil
}
