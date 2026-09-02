package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/openaicompat"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
)

// sessionModelCoordinator builds a coordinator whose config has two
// offline openai-compatible providers, a large selection on the first,
// and a mock current agent reporting that large model. buildModel
// constructs providers without network I/O, so per-session and per-call
// resolution can be exercised end to end against the real code path.
func sessionModelCoordinator(t *testing.T, env fakeEnv) *coordinator {
	t.Helper()
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
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
	cfg.Config().Models[config.SelectedModelTypeOrchestrator] = config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
	cfg.Config().Models["scout"] = config.SelectedModel{Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"}

	c := &coordinator{
		cfg:        cfg,
		sessions:   env.sessions,
		messages:   env.messages,
		modelCache: csync.NewMap[string, Model](),
	}
	c.currentAgent = &mockSessionAgent{model: Model{
		CatwalkCfg: catwalk.Model{ID: large.Model, Name: "Luna", DefaultMaxTokens: 4096},
		ModelCfg:   large,
	}}
	return c
}

func TestModelForSession(t *testing.T) {
	t.Run("unstamped session resolves to large with no override", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		sess, err := env.sessions.Create(t.Context(), "worker")
		require.NoError(t, err)

		m, override, err := c.modelForSession(t.Context(), sess.ID, false)
		require.NoError(t, err)
		assert.Nil(t, override)
		assert.Equal(t, "gpt-5.6-luna(low)", m.ModelCfg.Model)
	})

	t.Run("stamped session resolves to its own model", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		stamp := &config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}
		sess, err := env.sessions.CreateWithModel(t.Context(), "orchestrator", stamp)
		require.NoError(t, err)

		m, override, err := c.modelForSession(t.Context(), sess.ID, false)
		require.NoError(t, err)
		require.NotNil(t, override)
		assert.Equal(t, "claude-fable-5-1", m.ModelCfg.Model)
		assert.Equal(t, "dp-claude", m.ModelCfg.Provider)
		assert.Equal(t, "Fable", m.CatwalkCfg.Name)
		assert.NotNil(t, m.Model, "a runnable language model must be built")
		// Catalog defaults are backfilled onto a bare stamp.
		assert.Equal(t, int64(16000), m.ModelCfg.MaxTokens)
		assert.Equal(t, "low", m.ModelCfg.ReasoningEffort)

		// Second resolution reuses the built provider/model: the cache
		// holds exactly one entry for this selection and hands back the
		// same language model value.
		key := modelCacheKey(*stamp, false)
		cached, ok := c.modelCache.Get(key)
		require.True(t, ok)
		m2, _, err := c.modelForSession(t.Context(), sess.ID, false)
		require.NoError(t, err)
		assert.Equal(t, cached.CatwalkCfg, m2.CatwalkCfg)
		assert.Equal(t, 1, c.modelCache.Len(), "second resolution must not add a cache entry")

		// UpdateModels-style reset forces a rebuild.
		c.modelCache.Reset(map[string]Model{})
		_, ok = c.modelCache.Get(key)
		assert.False(t, ok)
		_, _, err = c.modelForSession(t.Context(), sess.ID, false)
		require.NoError(t, err)
		_, ok = c.modelCache.Get(key)
		assert.True(t, ok, "rebuilt and re-cached after reset")
	})

	t.Run("stamp pointing at an unknown model is an error, not a silent fallback", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		sess, err := env.sessions.CreateWithModel(t.Context(), "broken", &config.SelectedModel{Provider: "dp-claude", Model: "ghost"})
		require.NoError(t, err)
		_, _, err = c.modelForSession(t.Context(), sess.ID, false)
		require.ErrorContains(t, err, "ghost")
	})

	t.Run("missing session falls back to large", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		m, override, err := c.modelForSession(t.Context(), "does-not-exist", false)
		require.NoError(t, err)
		assert.Nil(t, override)
		assert.Equal(t, "gpt-5.6-luna(low)", m.ModelCfg.Model)
	})
}

func TestRunSubAgent_ModelOverride(t *testing.T) {
	t.Run("per-call model stamps the child and rides the call", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		parent, err := env.sessions.CreateWithModel(t.Context(), "Parent", &config.SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"})
		require.NoError(t, err)

		var got SessionAgentCall
		agent := &mockSessionAgent{
			model: c.currentAgent.Model(),
			runFunc: func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
				got = call
				return agentResultWithText("done"), nil
			},
		}
		sel, err := c.cfg.Config().ResolveModelRef("scout")
		require.NoError(t, err)

		resp, err := c.runSubAgent(t.Context(), subAgentParams{
			Agent: agent, SessionID: parent.ID, AgentMessageID: "msg-1", ToolCallID: "call-1",
			Prompt: "find it", SessionTitle: "Scout", Model: &sel,
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError, resp.Content)

		require.NotNil(t, got.Model, "override must be passed on the call")
		assert.Equal(t, "claude-haiku-4-5-20251001", got.Model.ModelCfg.Model)
		assert.Equal(t, int64(8000), got.MaxOutputTokens, "max tokens come from the override's catalog entry")

		child, err := env.sessions.Get(t.Context(), c.sessions.CreateAgentToolSessionID("msg-1", "call-1"))
		require.NoError(t, err)
		assert.Equal(t, parent.ID, child.ParentSessionID)
		require.NotNil(t, child.Model)
		assert.Equal(t, "claude-haiku-4-5-20251001", child.Model.Model)

		// The parent keeps its own stamp.
		parent, err = env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.Equal(t, "claude-fable-5-1", parent.Model.Model)
	})

	t.Run("no model runs large and leaves the child unstamped", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		var got SessionAgentCall
		agent := &mockSessionAgent{model: c.currentAgent.Model(), runFunc: func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			got = call
			return agentResultWithText("done"), nil
		}}
		_, err = c.runSubAgent(t.Context(), subAgentParams{Agent: agent, SessionID: parent.ID, AgentMessageID: "m", ToolCallID: "c", Prompt: "x", SessionTitle: "t"})
		require.NoError(t, err)
		assert.Nil(t, got.Model)
		child, err := env.sessions.Get(t.Context(), c.sessions.CreateAgentToolSessionID("m", "c"))
		require.NoError(t, err)
		assert.Nil(t, child.Model)
	})

	t.Run("unbuildable model is a tool error and creates no child session", func(t *testing.T) {
		env := testEnv(t)
		c := sessionModelCoordinator(t, env)
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
}

// The tools' `model` grammar resolves against live config through the
// coordinator, and empty means no override.
func TestOptionalModelRef(t *testing.T) {
	env := testEnv(t)
	c := sessionModelCoordinator(t, env)

	sel, err := c.optionalModelRef("")
	require.NoError(t, err)
	assert.Nil(t, sel)

	sel, err = c.optionalModelRef("orchestrator")
	require.NoError(t, err)
	require.NotNil(t, sel)
	assert.Equal(t, "claude-fable-5-1", sel.Model)

	sel, err = c.optionalModelRef("dp-claude/claude-haiku-4-5-20251001")
	require.NoError(t, err)
	assert.Equal(t, "dp-claude", sel.Provider)

	_, err = c.optionalModelRef("nope")
	require.Error(t, err)
}
