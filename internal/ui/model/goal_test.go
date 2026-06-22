package model

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// goalAction classifies how handleGoal would route an argument string,
// without invoking the workspace. It mirrors the switch in handleGoal so
// the routing contract is unit-tested independently of UI state.
func goalAction(args string) string {
	switch {
	case args == "":
		return "status"
	case slices.Contains(goalClearVerbs, args):
		return "clear"
	default:
		return "set"
	}
}

func TestGoalVerbRouting(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                   "status",
		"clear":              "clear",
		"stop":               "clear",
		"off":                "clear",
		"reset":              "clear",
		"none":               "clear",
		"cancel":             "clear",
		"all tests pass":     "set",
		"clear the test dir": "set", // multi-word starting with "clear" is a condition
	}
	for args, want := range cases {
		require.Equal(t, want, goalAction(args), "args=%q", args)
	}
}

func TestGoalClearVerbsRegistered(t *testing.T) {
	t.Parallel()
	// Guard the alias set against accidental edits.
	for _, v := range []string{"clear", "stop", "off", "reset", "none", "cancel"} {
		require.True(t, slices.Contains(goalClearVerbs, v), "missing clear verb %q", v)
	}
}

func TestGoalCommandRegistered(t *testing.T) {
	t.Parallel()
	c, ok := lookupSlash(builtinSlashCommands, "goal")
	require.True(t, ok)
	require.True(t, c.requiresSession)
	require.NotNil(t, c.run)
}
