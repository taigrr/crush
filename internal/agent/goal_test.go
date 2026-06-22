package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClampBudget(t *testing.T) {
	t.Parallel()
	require.Equal(t, 1, clampBudget(0))
	require.Equal(t, 1, clampBudget(-5))
	require.Equal(t, 1, clampBudget(1))
	require.Equal(t, 25, clampBudget(25))
	require.Equal(t, maxGoalMaxTurns, clampBudget(maxGoalMaxTurns))
	require.Equal(t, maxGoalMaxTurns, clampBudget(maxGoalMaxTurns+50))
}

func TestGoalAdvanceMetStops(t *testing.T) {
	t.Parallel()
	g := newGoalState("ship it")
	cont, reason := goalAdvance(g, goalDecision{met: true, reason: "all tests pass"})
	require.False(t, cont)
	require.Equal(t, "all tests pass", reason)
	require.Equal(t, 0, g.turns, "a met goal must not consume a turn")
}

func TestGoalAdvanceMetWithoutReasonHasDefault(t *testing.T) {
	t.Parallel()
	g := newGoalState("ship it")
	_, reason := goalAdvance(g, goalDecision{met: true})
	require.Equal(t, "goal met", reason)
}

func TestGoalAdvanceContinuesUntilBudget(t *testing.T) {
	t.Parallel()
	g := newGoalState("ship it")
	g.maxTurns = 3

	for i := 1; i <= 3; i++ {
		cont, _ := goalAdvance(g, goalDecision{met: false})
		require.True(t, cont, "turn %d should continue", i)
		require.Equal(t, i, g.turns)
	}

	// Budget now exhausted: turns(3) >= maxTurns(3).
	cont, reason := goalAdvance(g, goalDecision{met: false})
	require.False(t, cont)
	require.Contains(t, reason, "budget exhausted")
	require.Equal(t, 3, g.turns, "exhausted budget must not increment turns")
}

func TestGoalAdvanceBudgetOverrideOnce(t *testing.T) {
	t.Parallel()
	g := newGoalState("ship it") // default budget 25
	require.Equal(t, defaultGoalMaxTurns, g.maxTurns)

	// First evaluation overrides the budget to 2.
	cont, _ := goalAdvance(g, goalDecision{met: false, suggestedBudget: 2})
	require.True(t, cont)
	require.Equal(t, 2, g.maxTurns)
	require.True(t, g.budgetSet)
	require.Equal(t, 1, g.turns)

	// A later suggestion must be ignored (override is one-time).
	cont, _ = goalAdvance(g, goalDecision{met: false, suggestedBudget: 99})
	require.True(t, cont)
	require.Equal(t, 2, g.maxTurns, "budget override must apply only once")
	require.Equal(t, 2, g.turns)

	// Now exhausted at the overridden budget.
	cont, reason := goalAdvance(g, goalDecision{met: false})
	require.False(t, cont)
	require.Contains(t, reason, "budget exhausted")
}

func TestGoalAdvanceBudgetOverrideClamped(t *testing.T) {
	t.Parallel()
	g := newGoalState("ship it")
	goalAdvance(g, goalDecision{met: false, suggestedBudget: 10_000})
	require.Equal(t, maxGoalMaxTurns, g.maxTurns)

	// A non-positive suggestion is treated as "no override" and leaves
	// the default budget untouched, rather than being clamped to 1.
	g2 := newGoalState("ship it")
	goalAdvance(g2, goalDecision{met: false, suggestedBudget: -3})
	require.Equal(t, defaultGoalMaxTurns, g2.maxTurns)
	require.False(t, g2.budgetSet)
}

func TestGoalAdvanceOverrideGovernsSameEvaluation(t *testing.T) {
	t.Parallel()
	// If the model sets a budget of 1 and the goal isn't met, this same
	// evaluation consumes the only turn; the next one must stop.
	g := newGoalState("ship it")
	cont, _ := goalAdvance(g, goalDecision{met: false, suggestedBudget: 1})
	require.True(t, cont)
	require.Equal(t, 1, g.turns)

	cont, reason := goalAdvance(g, goalDecision{met: false})
	require.False(t, cont)
	require.Contains(t, reason, "budget exhausted")
}

func TestGoalContinuationPrompt(t *testing.T) {
	t.Parallel()

	t.Run("with feedback", func(t *testing.T) {
		t.Parallel()
		p := goalContinuationPrompt("tests pass", "3 tests still failing")
		require.Contains(t, p, "[goal]")
		require.Contains(t, p, "Goal: tests pass")
		require.Contains(t, p, "Not done because: 3 tests still failing")
	})

	t.Run("without feedback omits reason line", func(t *testing.T) {
		t.Parallel()
		p := goalContinuationPrompt("tests pass", "   ")
		require.Contains(t, p, "Goal: tests pass")
		require.NotContains(t, p, "Not done because:")
	})
}

func TestGoalStateSnapshot(t *testing.T) {
	t.Parallel()
	g := newGoalState("do the thing")
	goalAdvance(g, goalDecision{met: false, suggestedBudget: 7})
	cond, turns, maxTurns := g.snapshot()
	require.Equal(t, "do the thing", cond)
	require.Equal(t, 1, turns)
	require.Equal(t, 7, maxTurns)
}

func TestNewGoalReportToolCaptures(t *testing.T) {
	t.Parallel()
	var out goalReportParams
	var captured bool
	tool := newGoalReportTool(&out, &captured)
	require.Equal(t, goalReportToolName, tool.Info().Name)
	require.False(t, captured)
	// The tool's schema/description should mention the budget override.
	require.Contains(t, strings.ToLower(tool.Info().Description), "turn budget")
}
