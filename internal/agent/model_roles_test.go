package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/openaicompat"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
)

// roleCoordinator builds a coordinator whose config has two offline
// openai-compatible providers, large/small on the first, and a custom
// "scout" role on the second. buildModel constructs providers without
// network I/O, so per-call override resolution runs against the real path.
// worker is left unset unless a test adds it.
func roleCoordinator(t *testing.T, env fakeEnv) *coordinator {
	t.Helper()
	// Keep the host's real config (and any worker role it defines) out of
	// the test.
	t.Setenv("CRUSH_GLOBAL_CONFIG", filepath.Join(t.TempDir(), "crush.json"))
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	delete(cfg.Config().Models, config.SelectedModelTypeWorker)
	cfg.Config().Providers.Set("dp-gpt", config.ProviderConfig{
		ID: "dp-gpt", Name: "GPT", Type: openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1", APIKey: "x",
		Models: []catwalk.Model{{ID: "gpt-5.6-luna(low)", Name: "Luna", DefaultMaxTokens: 4096}},
	})
	cfg.Config().Providers.Set("dp-claude", config.ProviderConfig{
		ID: "dp-claude", Name: "Claude", Type: openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1", APIKey: "x",
		Models: []catwalk.Model{
			{ID: "claude-fable-5-1", Name: "Fable", DefaultMaxTokens: 16000, CanReason: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoningEffort: "low"},
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku", DefaultMaxTokens: 8000},
		},
	})
	large := config.SelectedModel{Provider: "dp-gpt", Model: "gpt-5.6-luna(low)"}
	cfg.Config().Models[config.SelectedModelTypeLarge] = large
	cfg.Config().Models[config.SelectedModelTypeSmall] = large
	cfg.Config().Models["scout"] = config.SelectedModel{Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"}

	c := &coordinator{
		cfg:           cfg,
		sessions:      env.sessions,
		messages:      env.messages,
		overrideCache: csync.NewMap[string, Model](),
	}
	c.currentAgent = &mockSessionAgent{model: Model{
		CatwalkCfg: catwalk.Model{ID: large.Model, Name: "Luna", DefaultMaxTokens: 4096},
		ModelCfg:   large,
	}}
	return c
}

func newCapturingAgent(c *coordinator, got *SessionAgentCall) *mockSessionAgent {
	return &mockSessionAgent{
		model: c.currentAgent.Model(),
		runFunc: func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			*got = call
			return agentResultWithText("done"), nil
		},
	}
}

func TestRunSubAgent_ModelOverride(t *testing.T) {
	t.Run("per-call model rides the call and sizes the turn", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		var got SessionAgentCall
		sel, err := c.cfg.Config().ResolveModelRef("scout")
		require.NoError(t, err)
		resp, err := c.runSubAgent(t.Context(), subAgentParams{
			Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "msg-1", ToolCallID: "call-1",
			Prompt: "find it", SessionTitle: "Scout", Model: &sel,
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)

		require.NotNil(t, got.ResolveModel, "override resolver must be passed on the call")
		resolved, rerr := got.ResolveModel(t.Context())
		require.NoError(t, rerr)
		require.NotNil(t, resolved)
		assert.Equal(t, "claude-haiku-4-5-20251001", resolved.ModelCfg.Model)
		assert.Equal(t, int64(8000), got.MaxOutputTokens, "max tokens come from the override's catalog entry")

		child, err := env.sessions.Get(t.Context(), c.sessions.CreateAgentToolSessionID("msg-1", "call-1"))
		require.NoError(t, err)
		assert.Equal(t, parent.ID, child.ParentSessionID)
	})

	t.Run("no model and no worker runs large", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t"})
		require.NoError(t, err)
		assert.Nil(t, got.ResolveModel)
		assert.Equal(t, int64(4096), got.MaxOutputTokens)
	})

	t.Run("no model falls back to the configured worker role", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Models[config.SelectedModelTypeWorker] = config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t", UseWorkerDefault: true})
		require.NoError(t, err)
		require.NotNil(t, got.ResolveModel)
		resolved, rerr := got.ResolveModel(t.Context())
		require.NoError(t, rerr)
		require.NotNil(t, resolved)
		assert.Equal(t, "claude-fable-5-1", resolved.ModelCfg.Model)
		assert.Equal(t, int64(16000), got.MaxOutputTokens, "catalog default max tokens are backfilled")
	})

	t.Run("explicit model wins over the worker role", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Models[config.SelectedModelTypeWorker] = config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		sel, err := c.cfg.Config().ResolveModelRef("large")
		require.NoError(t, err)
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t", Model: &sel})
		require.NoError(t, err)
		require.NotNil(t, got.ResolveModel)
		resolved, rerr := got.ResolveModel(t.Context())
		require.NoError(t, rerr)
		assert.Equal(t, "gpt-5.6-luna(low)", resolved.ModelCfg.Model)
	})

	t.Run("worker is opt-in: callers that do not ask keep their own model", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Models[config.SelectedModelTypeWorker] = config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t"})
		require.NoError(t, err)
		assert.Nil(t, got.ResolveModel, "agentic_fetch-style callers must not be moved onto worker")
	})

	t.Run("worker on a disabled provider is ignored", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		pc, _ := c.cfg.Config().Providers.Get("dp-claude")
		pc.Disable = true
		c.cfg.Config().Providers.Set("dp-claude", pc)
		c.cfg.Config().Models[config.SelectedModelTypeWorker] = config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: newCapturingAgent(c, &got), SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t", UseWorkerDefault: true})
		require.NoError(t, err)
		assert.Nil(t, got.ResolveModel, "disabled worker must fall back to large")
	})

	t.Run("unbuildable model is a tool error and creates no child session", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		agent := &mockSessionAgent{model: c.currentAgent.Model(), runFunc: func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
			t.Fatal("agent must not run")
			return nil, nil
		}}
		resp, err := c.runSubAgent(t.Context(), subAgentParams{
			Agent: agent, SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t",
			Model: &config.SelectedModel{Provider: "dp-claude", Model: "ghost"},
		})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "ghost")
		_, err = env.sessions.Get(t.Context(), c.sessions.CreateAgentToolSessionID("m", "c"))
		require.Error(t, err, "no orphan child session")
	})

	t.Run("override cache is reused and reset by UpdateModels-style reset", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		sel, _ := c.cfg.Config().ResolveModelRef("scout")
		_, err := c.buildModel(t.Context(), sel, true)
		require.NoError(t, err)
		assert.Equal(t, 1, c.overrideCache.Len())
		_, err = c.buildModel(t.Context(), sel, true)
		require.NoError(t, err)
		assert.Equal(t, 1, c.overrideCache.Len(), "second build must hit the cache")
		c.overrideCache.Reset(map[string]Model{})
		_, err = c.buildModel(t.Context(), sel, true)
		require.NoError(t, err)
		assert.Equal(t, 1, c.overrideCache.Len(), "rebuilt and re-cached after reset")
	})
}

// The tools' `model` grammar resolves against live config through the
// coordinator, and empty means no override.
func TestOptionalModelRef(t *testing.T) {
	env := testEnv(t)
	c := roleCoordinator(t, env)

	sel, err := c.optionalModelRef("")
	require.NoError(t, err)
	assert.Nil(t, sel)

	sel, err = c.optionalModelRef("scout")
	require.NoError(t, err)
	require.NotNil(t, sel)
	assert.Equal(t, "claude-haiku-4-5-20251001", sel.Model)

	sel, err = c.optionalModelRef("dp-claude/claude-fable-5-1")
	require.NoError(t, err)
	assert.Equal(t, "dp-claude", sel.Provider)

	_, err = c.optionalModelRef("nope")
	require.Error(t, err)
}
