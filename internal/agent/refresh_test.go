package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/fantasy/providers/openaicompat"
	"golang.org/x/sync/errgroup"
)

// newOfflineCoordinator builds a real coordinator against an
// openai-compatible provider that constructs without network I/O.
func newOfflineCoordinator(t *testing.T) *coordinator {
	t.Helper()
	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	const providerID, modelID = "test-openai-compat", "test-model"
	cfg.Config().Providers.Set(providerID, config.ProviderConfig{
		ID: providerID, Name: "Test", Type: openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1", APIKey: "test",
		Models: []catwalk.Model{{ID: modelID, DefaultMaxTokens: 4096}},
	})
	selected := config.SelectedModel{Provider: providerID, Model: modelID}
	cfg.Config().Models[config.SelectedModelTypeLarge] = selected
	cfg.Config().Models[config.SelectedModelTypeSmall] = selected
	cfg.SetupAgents()
	coderCfg := cfg.Config().Agents[config.AgentCoder]
	coderCfg.AllowedTools = nil
	cfg.Config().Agents[config.AgentCoder] = coderCfg

	c, err := NewCoordinator(t.Context(), cfg, env.sessions, env.messages, nil,
		permission.NewPermissionService(env.workingDir, true, nil), question.NewQuestionService(),
		nil, nil, nil, nil, embedding.Build(nil, embedding.Params{Configured: false}),
		nil, nil, nil, nil, nil, env.workingDir)
	require.NoError(t, err)
	return c.(*coordinator)
}

// TestRefresh_ClearsPoisonedReadiness: a readiness build that failed once
// must not fail every future run; Refresh installs a fresh group and the
// same coordinator (not a rebuilt one) becomes runnable again.
func TestRefresh_ClearsPoisonedReadiness(t *testing.T) {
	t.Parallel()
	c := newOfflineCoordinator(t)
	require.NoError(t, c.readiness().Wait(), "initial build must succeed")

	poisoned := &errgroup.Group{}
	poisonErr := errors.New("mcp init exploded")
	poisoned.Go(func() error { return poisonErr })
	c.readyMu.Lock()
	c.readyWg = poisoned
	c.readyMu.Unlock()
	require.ErrorIs(t, c.readiness().Wait(), poisonErr)

	before := c.currentAgent
	deferred, err := c.Refresh(t.Context())
	require.NoError(t, err)
	require.False(t, deferred, "an idle coordinator refreshes immediately")
	require.Same(t, before, c.currentAgent, "Refresh must keep the same agent")
	require.NoError(t, c.readiness().Wait(), "the fresh readiness group must not carry the old error")
}
