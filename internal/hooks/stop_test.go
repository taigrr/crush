package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
)

func TestInterpretStop(t *testing.T) {
	t.Parallel()

	t.Run("deny means continue", func(t *testing.T) {
		t.Parallel()
		res := InterpretStop(AggregateResult{Decision: DecisionDeny, Reason: "keep going", HookCount: 1})
		require.True(t, res.Continue)
		require.Equal(t, "keep going", res.Reason)
		require.Equal(t, 1, res.HookCount)
	})

	t.Run("halt means continue", func(t *testing.T) {
		t.Parallel()
		res := InterpretStop(AggregateResult{Halt: true, Reason: "not done"})
		require.True(t, res.Continue)
		require.Equal(t, "not done", res.Reason)
	})

	t.Run("allow means stop", func(t *testing.T) {
		t.Parallel()
		res := InterpretStop(AggregateResult{Decision: DecisionAllow})
		require.False(t, res.Continue)
	})

	t.Run("none means stop", func(t *testing.T) {
		t.Parallel()
		res := InterpretStop(AggregateResult{Decision: DecisionNone})
		require.False(t, res.Continue)
	})

	t.Run("context propagates", func(t *testing.T) {
		t.Parallel()
		res := InterpretStop(AggregateResult{Decision: DecisionDeny, Context: "extra"})
		require.True(t, res.Continue)
		require.Equal(t, "extra", res.Context)
	})
}

func TestBuildStopPayload(t *testing.T) {
	t.Parallel()
	payload := BuildStopPayload("sess-1", "/work", StopInput{
		LastMessage:    "almost there",
		ContinueCount:  2,
		StopHookActive: true,
	})
	s := string(payload)
	require.Contains(t, s, `"event":"`+EventStop+`"`)
	require.Contains(t, s, `"session_id":"sess-1"`)
	require.Contains(t, s, `"last_message":"almost there"`)
	require.Contains(t, s, `"continue_count":2`)
	require.Contains(t, s, `"stop_hook_active":true`)
}

func TestBuildStopEnv(t *testing.T) {
	t.Parallel()
	env := BuildStopEnv("sess-1", "/work", "/project", StopInput{ContinueCount: 3, StopHookActive: true})
	envMap := make(map[string]string)
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}
	require.Equal(t, EventStop, envMap["CRUSH_EVENT"])
	require.Equal(t, "sess-1", envMap["CRUSH_SESSION_ID"])
	require.Equal(t, "/work", envMap["CRUSH_CWD"])
	require.Equal(t, "/project", envMap["CRUSH_PROJECT_DIR"])
	require.Equal(t, "3", envMap["CRUSH_CONTINUE_COUNT"])
	require.Equal(t, "true", envMap["CRUSH_STOP_HOOK_ACTIVE"])
}

func TestRunStop(t *testing.T) {
	t.Parallel()

	t.Run("no hooks means stop", func(t *testing.T) {
		t.Parallel()
		r := NewRunner(nil, t.TempDir(), t.TempDir())
		res, err := r.RunStop(context.Background(), "sess", StopInput{})
		require.NoError(t, err)
		require.False(t, res.Continue)
	})

	t.Run("hook denies via exit 2 to continue", func(t *testing.T) {
		t.Parallel()
		hookCfg := config.HookConfig{Command: `echo "not finished" >&2; exit 2`}
		r := NewRunner([]config.HookConfig{hookCfg}, t.TempDir(), t.TempDir())
		res, err := r.RunStop(context.Background(), "sess", StopInput{})
		require.NoError(t, err)
		require.True(t, res.Continue)
		require.Equal(t, "not finished", res.Reason)
	})

	t.Run("hook continues via JSON decision", func(t *testing.T) {
		t.Parallel()
		hookCfg := config.HookConfig{Command: `echo '{"decision":"deny","reason":"keep working"}'`}
		r := NewRunner([]config.HookConfig{hookCfg}, t.TempDir(), t.TempDir())
		res, err := r.RunStop(context.Background(), "sess", StopInput{})
		require.NoError(t, err)
		require.True(t, res.Continue)
		require.Equal(t, "keep working", res.Reason)
	})

	t.Run("hook allows stop", func(t *testing.T) {
		t.Parallel()
		hookCfg := config.HookConfig{Command: `echo '{"decision":"allow"}'`}
		r := NewRunner([]config.HookConfig{hookCfg}, t.TempDir(), t.TempDir())
		res, err := r.RunStop(context.Background(), "sess", StopInput{})
		require.NoError(t, err)
		require.False(t, res.Continue)
	})

	t.Run("silent hook allows stop", func(t *testing.T) {
		t.Parallel()
		hookCfg := config.HookConfig{Command: `exit 0`}
		r := NewRunner([]config.HookConfig{hookCfg}, t.TempDir(), t.TempDir())
		res, err := r.RunStop(context.Background(), "sess", StopInput{})
		require.NoError(t, err)
		require.False(t, res.Continue)
	})
}

func TestNormalizeStopEventViaConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			"stop": {{Command: `echo '{"decision":"allow"}'`}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	_, ok := cfg.Hooks[EventStop]
	require.True(t, ok, "snake/lower 'stop' should normalize to %q", EventStop)
}
