package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/fantasy"
)

// Goal turn-budget bounds. defaultGoalMaxTurns is the budget applied when
// the evaluator does not set an explicit one. maxGoalMaxTurns is a hard
// ceiling on any model-chosen budget so a runaway evaluator cannot grant
// itself an unbounded loop. These mirror the spirit of Claude Code's
// agent-evaluator turn cap.
const (
	defaultGoalMaxTurns = 25
	maxGoalMaxTurns     = 100
)

// goalReportToolName is the tool the evaluator calls to report whether the
// goal is met and, optionally, to override the turn budget.
const goalReportToolName = "report_goal_status"

// goalState is the per-session autonomous-goal state behind /goal. It is
// session-scoped and in-memory: a goal does not survive a process restart,
// which matches the reset-on-resume behavior of comparable tools and keeps
// the state model simple. All access is guarded by goalState.mu because
// the evaluator (agent goroutine) and the UI (set/clear/status) can touch
// it concurrently.
type goalState struct {
	mu        sync.Mutex
	condition string
	turns     int  // continuations performed so far
	maxTurns  int  // turn budget; defaultGoalMaxTurns until overridden
	budgetSet bool // whether the evaluator has set an explicit budget
}

// newGoalState creates an active goal for condition with the default budget.
func newGoalState(condition string) *goalState {
	return &goalState{
		condition: condition,
		maxTurns:  defaultGoalMaxTurns,
	}
}

// snapshot returns a consistent copy of the user-visible goal fields.
func (g *goalState) snapshot() (condition string, turns, maxTurns int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.condition, g.turns, g.maxTurns
}

// goalDecision is the outcome of one evaluation of the goal condition.
type goalDecision struct {
	// met is true when the evaluator judged the condition satisfied.
	met bool
	// reason explains the verdict (for logging and the stop notice).
	reason string
	// suggestedBudget, when > 0, is the evaluator's requested turn
	// budget. It is applied once (the first time it is provided) and
	// clamped to [1, maxGoalMaxTurns].
	suggestedBudget int
}

// clampBudget bounds a model-suggested turn budget to a sane range.
func clampBudget(n int) int {
	if n < 1 {
		return 1
	}
	if n > maxGoalMaxTurns {
		return maxGoalMaxTurns
	}
	return n
}

// goalAdvance applies one evaluation outcome to the goal state and reports
// whether the agent should continue working. It enforces the turn budget
// and applies a one-time budget override from the evaluator. It mutates
// the goal state (budget on first override; turns on each continuation)
// and is the pure, side-effect-isolated core that the LLM evaluator and
// tests both drive.
func goalAdvance(g *goalState, d goalDecision) (cont bool, stopReason string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Apply a one-time budget override before checking the cap so the
	// new budget governs this same evaluation.
	if d.suggestedBudget > 0 && !g.budgetSet {
		g.maxTurns = clampBudget(d.suggestedBudget)
		g.budgetSet = true
	}

	if d.met {
		reason := d.reason
		if reason == "" {
			reason = "goal met"
		}
		return false, reason
	}
	if g.turns >= g.maxTurns {
		return false, fmt.Sprintf("goal turn budget exhausted after %d turn(s)", g.maxTurns)
	}
	g.turns++
	return true, ""
}

// goalContinuationPrompt builds the directive injected as the next turn
// when a goal blocks the stop. It restates the condition and any
// evaluator feedback so the model resumes with the goal in focus.
func goalContinuationPrompt(condition, feedback string) string {
	var b strings.Builder
	b.WriteString("[goal] Keep working toward this goal; it is not yet complete.\n")
	fmt.Fprintf(&b, "Goal: %s\n", condition)
	if feedback = strings.TrimSpace(feedback); feedback != "" {
		fmt.Fprintf(&b, "Not done because: %s\n", feedback)
	}
	b.WriteString("Continue from where you left off and do not stop to ask for confirmation.")
	return b.String()
}

// goalTranscriptMaxMessages bounds how many recent messages are handed to
// the evaluator so the prompt stays small and cheap.
const goalTranscriptMaxMessages = 30

// SetGoal activates an autonomous goal for the session. A blank condition
// clears any existing goal. Replacing a goal resets its turn budget and
// counters.
func (a *sessionAgent) SetGoal(sessionID, condition string) {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		a.goals.Del(sessionID)
		return
	}
	a.goals.Set(sessionID, newGoalState(condition))
}

// ClearGoal removes any active goal for the session.
func (a *sessionAgent) ClearGoal(sessionID string) {
	a.goals.Del(sessionID)
}

// GoalStatus reports the active goal for the session, if any.
func (a *sessionAgent) GoalStatus(sessionID string) (condition string, turns, maxTurns int, active bool) {
	g, ok := a.goals.Get(sessionID)
	if !ok {
		return "", 0, 0, false
	}
	condition, turns, maxTurns = g.snapshot()
	return condition, turns, maxTurns, true
}

// AdvanceGoal evaluates the session's active goal against the current
// transcript and reports whether the agent should run another turn. When
// cont is true, prompt is the continuation directive to feed back into the
// agent. With no active goal it returns cont=false. On stop (goal met or
// budget exhausted) the goal is auto-cleared.
func (a *sessionAgent) AdvanceGoal(ctx context.Context, sessionID string) (cont bool, prompt string) {
	g, ok := a.goals.Get(sessionID)
	if !ok {
		return false, ""
	}
	condition, _, _ := g.snapshot()

	transcript := a.buildGoalTranscript(ctx, sessionID)
	decision := a.evaluateGoal(ctx, condition, transcript)
	cont, stopReason := goalAdvance(g, decision)
	if !cont {
		a.goals.Del(sessionID)
		slog.Info("Goal finished", "session_id", sessionID, "reason", stopReason)
		return false, ""
	}
	slog.Info("Goal continuing", "session_id", sessionID, "reason", decision.reason)
	return true, goalContinuationPrompt(condition, decision.reason)
}

// buildGoalTranscript renders the last goalTranscriptMaxMessages messages
// of the session into a compact text transcript for the evaluator. Tool
// calls and results are summarized so the evaluator can see what the agent
// actually did, not just its prose.
func (a *sessionAgent) buildGoalTranscript(ctx context.Context, sessionID string) string {
	msgs, err := a.messages.List(ctx, sessionID)
	if err != nil {
		slog.Error("Failed to load transcript for goal evaluation", "error", err)
		return ""
	}
	if len(msgs) > goalTranscriptMaxMessages {
		msgs = msgs[len(msgs)-goalTranscriptMaxMessages:]
	}

	var b strings.Builder
	for i := range msgs {
		m := &msgs[i]
		switch m.Role {
		case message.User:
			if text := strings.TrimSpace(m.Content().Text); text != "" {
				fmt.Fprintf(&b, "USER: %s\n", text)
			}
		case message.Assistant:
			if text := strings.TrimSpace(m.Content().Text); text != "" {
				fmt.Fprintf(&b, "ASSISTANT: %s\n", text)
			}
			for _, tc := range m.ToolCalls() {
				fmt.Fprintf(&b, "TOOL_CALL: %s %s\n", tc.Name, tc.Input)
			}
		case message.Tool:
			for _, tr := range m.ToolResults() {
				status := "ok"
				if tr.IsError {
					status = "error"
				}
				fmt.Fprintf(&b, "TOOL_RESULT(%s): %s\n", status, truncateForTranscript(tr.Content))
			}
		}
	}
	return b.String()
}

// truncateForTranscript caps a single transcript entry so one large tool
// result cannot dominate the evaluator prompt.
func truncateForTranscript(s string) string {
	const maxLen = 2000
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen] + "…(truncated)"
	}
	return s
}

// goalReportParams is the typed input the evaluator passes to the report
// tool. MaxTurns is optional; 0 means "leave the current budget".
type goalReportParams struct {
	Met      bool   `json:"met" jsonschema:"description=True if the goal condition is fully satisfied based on the transcript."`
	Reason   string `json:"reason" jsonschema:"description=Brief justification for the verdict."`
	MaxTurns int    `json:"max_turns,omitempty" jsonschema:"description=Optional override for how many additional turns the agent may take to reach the goal. Set this only once near the start when you can estimate the effort; omit or 0 to keep the default."`
}

// newGoalReportTool builds the single tool the evaluator uses to report
// its verdict. The returned tool writes the parsed params into *out; the
// caller reads *out after the evaluator agent finishes. captured is set to
// true when the tool fires so the caller can distinguish a real report
// from a misbehaving model that never called it.
func newGoalReportTool(out *goalReportParams, captured *bool) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		goalReportToolName,
		"Report whether the goal condition is met and optionally override the turn budget. Call this exactly once.",
		func(_ context.Context, params goalReportParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			*out = params
			*captured = true
			return fantasy.NewTextResponse("recorded"), nil
		},
	)
}

// goalEvaluatorSystemPrompt instructs the small model to judge the goal.
const goalEvaluatorSystemPrompt = `You are a strict goal evaluator for an autonomous coding agent.
You are given a goal condition and a transcript of the agent's recent work.
Judge ONLY whether the condition is fully satisfied by evidence in the transcript.
Be conservative: if the evidence is missing or ambiguous, the goal is NOT met.
You must call the ` + goalReportToolName + ` tool exactly once with your verdict.
Do not write any other prose.`

// evaluateGoal runs the small-model evaluator for an active goal and
// returns its decision. On any failure to obtain a structured verdict it
// returns met=true (i.e. stop) so an evaluator error can never produce an
// unbounded continuation loop.
func (a *sessionAgent) evaluateGoal(ctx context.Context, condition, transcript string) goalDecision {
	small := a.smallModel.Get()
	if small.Model == nil {
		slog.Warn("Goal evaluator has no small model; stopping goal")
		return goalDecision{met: true, reason: "no evaluator model available"}
	}

	var params goalReportParams
	var captured bool
	tool := newGoalReportTool(&params, &captured)

	evaluator := fantasy.NewAgent(
		small.Model,
		fantasy.WithSystemPrompt(goalEvaluatorSystemPrompt),
		fantasy.WithTools(tool),
		fantasy.WithUserAgent(userAgent),
	)

	prompt := fmt.Sprintf("Goal condition:\n%s\n\nTranscript:\n%s", condition, transcript)
	if _, err := evaluator.Stream(ctx, fantasy.AgentStreamCall{Prompt: prompt}); err != nil {
		slog.Error("Goal evaluation failed; stopping goal", "error", err)
		return goalDecision{met: true, reason: "evaluator error"}
	}
	if !captured {
		slog.Warn("Goal evaluator did not report a verdict; stopping goal")
		return goalDecision{met: true, reason: "evaluator produced no verdict"}
	}
	return goalDecision{
		met:             params.Met,
		reason:          params.Reason,
		suggestedBudget: params.MaxTurns,
	}
}
