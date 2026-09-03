package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/workspace"
)

type modelSlashWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *modelSlashWorkspace) Config() *config.Config { return w.cfg }
func (w *modelSlashWorkspace) ConnectionState() workspace.ConnectionState {
	return workspace.ConnectionStateConnected
}

func modelSlashConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("bedrock", config.ProviderConfig{
		ID: "bedrock",
		Models: []catwalk.Model{
			{ID: "us.anthropic.claude-fable-5-1", Name: "Claude Fable 5.1", ReasoningLevels: []string{"low", "high", "xhigh"}, DefaultReasoningEffort: "high"},
			{ID: "us.anthropic.claude-haiku-4-5", Name: "Claude Haiku 4.5"},
		},
	})
	return &config.Config{
		Providers: providers,
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeLarge:        {Provider: "bedrock", Model: "us.anthropic.claude-fable-5-1"},
			config.SelectedModelTypeSmall:        {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"},
			config.SelectedModelTypeOrchestrator: {Provider: "bedrock", Model: "us.anthropic.claude-fable-5-1", ReasoningEffort: "xhigh"},
			"scout":                              {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"},
		},
	}
}

func newModelSlashUI(t *testing.T) *UI {
	t.Helper()
	ui := newSendTestUI(t, &modelSlashWorkspace{cfg: modelSlashConfig()})
	ui.session = &session.Session{ID: "S1"}
	return ui
}

func TestModelArgCompletions(t *testing.T) {
	ui := newModelSlashUI(t)

	t.Run("first argument offers roles then models", func(t *testing.T) {
		got := ui.modelArgCompletions("")
		require.GreaterOrEqual(t, len(got), 6)
		require.Equal(t, "orchestrator", got[0].Text)
		require.True(t, got[0].Continue)
		require.Equal(t, "large", got[1].Text)
		require.Equal(t, "small", got[2].Text)
		require.Equal(t, "scout", got[3].Text, "custom roles follow builtins")
		require.Equal(t, "bedrock/us.anthropic.claude-fable-5-1", got[4].Text, "role holders lead the model list")
		require.False(t, got[4].Continue)
	})

	t.Run("after a role only models are offered", func(t *testing.T) {
		got := ui.modelArgCompletions("orchestrator")
		for _, g := range got {
			require.Contains(t, g.Text, "/")
		}
	})

	t.Run("after a model its effort levels are offered", func(t *testing.T) {
		got := ui.modelArgCompletions("fable")
		require.Len(t, got, 3)
		require.Equal(t, "low", got[0].Text)
		require.Contains(t, got[1].Description, "default")
	})

	t.Run("after a role and model its effort levels are offered", func(t *testing.T) {
		got := ui.modelArgCompletions("orchestrator fable")
		require.Len(t, got, 3)
	})

	t.Run("nothing after an effort", func(t *testing.T) {
		require.Empty(t, ui.modelArgCompletions("fable xhigh"))
	})

	t.Run("nothing for a model without levels", func(t *testing.T) {
		require.Empty(t, ui.modelArgCompletions("haiku"))
	})
}

func TestSlashArgCompletions_OnlyForCommandsThatOfferThem(t *testing.T) {
	ui := newModelSlashUI(t)
	require.NotEmpty(t, ui.slashArgCompletions("/model "))
	require.Empty(t, ui.slashArgCompletions("/review "))
	require.Empty(t, ui.slashArgCompletions("/nope "))
	require.Empty(t, ui.slashArgCompletions("model "))
}

func TestDescribeModels(t *testing.T) {
	ui := newModelSlashUI(t)
	out := ui.describeModels()
	require.Contains(t, out, "this session: bedrock/us.anthropic.claude-fable-5-1 (follows large)")
	require.Contains(t, out, "orchestrator: bedrock/us.anthropic.claude-fable-5-1 (xhigh)")
	require.Contains(t, out, "scout: bedrock/us.anthropic.claude-haiku-4-5")
	ui.session.Model = &config.SelectedModel{Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"}
	require.Contains(t, ui.describeModels(), "this session: bedrock/us.anthropic.claude-haiku-4-5 (pinned)")
}

func TestRoleToken(t *testing.T) {
	ui := newModelSlashUI(t)
	for _, tok := range []string{"large", "Small", "ORCHESTRATOR", "scout"} {
		_, ok := ui.roleToken(tok)
		require.True(t, ok, tok)
	}
	_, ok := ui.roleToken("fable")
	require.False(t, ok)
}

func TestFollowsOrchestrator(t *testing.T) {
	ui := newModelSlashUI(t)
	prev := config.SelectedModel{Provider: "bedrock", Model: "us.anthropic.claude-fable-5-1"}

	ui.session.Model = nil
	require.True(t, ui.followsOrchestrator(config.SelectedModel{}, false), "no stamp, no orchestrator yet")
	require.False(t, ui.followsOrchestrator(prev, true), "no stamp but an orchestrator existed: session is a worker-style default")

	ui.session.Model = &prev
	require.True(t, ui.followsOrchestrator(prev, true), "stamped with the old orchestrator")

	ui.session.Model = &config.SelectedModel{Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"}
	require.False(t, ui.followsOrchestrator(prev, true), "deliberately pinned elsewhere")

	ui.session = nil
	require.False(t, ui.followsOrchestrator(prev, true))
}
