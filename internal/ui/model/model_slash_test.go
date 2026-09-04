package model

import (
	"strings"
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
			config.SelectedModelTypeLarge:  {Provider: "bedrock", Model: "us.anthropic.claude-fable-5-1"},
			config.SelectedModelTypeSmall:  {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"},
			config.SelectedModelTypeWorker: {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5", ReasoningEffort: ""},
			"scout":                        {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5"},
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
		require.Equal(t, "large", got[0].Text)
		require.True(t, got[0].Continue)
		require.Equal(t, "small", got[1].Text)
		require.Equal(t, "worker", got[2].Text)
		require.Equal(t, "scout", got[3].Text, "custom roles follow builtins")
		require.Equal(t, "bedrock/us.anthropic.claude-fable-5-1", got[4].Text, "role holders lead the model list")
		require.False(t, got[4].Continue)
	})

	t.Run("after a role only models are offered", func(t *testing.T) {
		got := ui.modelArgCompletions("worker")
		require.NotEmpty(t, got)
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
		got := ui.modelArgCompletions("worker fable")
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
	require.Contains(t, out, "this session: bedrock/us.anthropic.claude-fable-5-1 (large)")
	require.Contains(t, out, "worker: bedrock/us.anthropic.claude-haiku-4-5")
	require.Contains(t, out, "scout: bedrock/us.anthropic.claude-haiku-4-5")
	require.Less(t, strings.Index(out, "large:"), strings.Index(out, "worker:"), "builtins first")
}

func TestRoleToken(t *testing.T) {
	ui := newModelSlashUI(t)
	for _, tok := range []string{"large", "Small", "WORKER", "scout", "Scout"} {
		role, ok := ui.roleToken(tok)
		require.True(t, ok, tok)
		if strings.EqualFold(tok, "scout") {
			require.Equal(t, config.SelectedModelType("scout"), role, "custom roles keep their configured spelling")
		}
	}
	_, ok := ui.roleToken("fable")
	require.False(t, ok)
}

func TestValidRoleName(t *testing.T) {
	for _, ok := range []string{"large", "scout", "fast-cheap", "tier_2", "A1"} {
		require.True(t, validRoleName(ok), ok)
	}
	for _, bad := range []string{"", "a.b", "a b", "a*", "a:b", "a/b", "a|b"} {
		require.False(t, validRoleName(bad), bad)
	}
}

func TestParseModelSlash(t *testing.T) {
	ui := newModelSlashUI(t)
	cfg := ui.com.Config()

	t.Run("bare model sets large", func(t *testing.T) {
		a, err := ui.parseModelSlash(cfg, "haiku")
		require.NoError(t, err)
		require.Equal(t, config.SelectedModelTypeLarge, a.role)
		require.Equal(t, "us.anthropic.claude-haiku-4-5", a.sel.Model)
	})

	t.Run("model with effort", func(t *testing.T) {
		a, err := ui.parseModelSlash(cfg, "fable xhigh")
		require.NoError(t, err)
		require.Equal(t, "xhigh", a.sel.ReasoningEffort)
	})

	t.Run("existing role alone shows it", func(t *testing.T) {
		a, err := ui.parseModelSlash(cfg, "worker")
		require.NoError(t, err)
		require.True(t, a.show)
		require.Equal(t, config.SelectedModelTypeWorker, a.role)
	})

	t.Run("existing role with model sets it", func(t *testing.T) {
		a, err := ui.parseModelSlash(cfg, "Scout fable high")
		require.NoError(t, err)
		require.False(t, a.show)
		require.Equal(t, config.SelectedModelType("scout"), a.role, "configured spelling is kept")
		require.Equal(t, "us.anthropic.claude-fable-5-1", a.sel.Model)
		require.Equal(t, "high", a.sel.ReasoningEffort)
	})

	t.Run("new custom role is created", func(t *testing.T) {
		a, err := ui.parseModelSlash(cfg, "reviewer fable")
		require.NoError(t, err)
		require.Equal(t, config.SelectedModelType("reviewer"), a.role)
		require.Equal(t, "us.anthropic.claude-fable-5-1", a.sel.Model)
	})

	t.Run("new role with an invalid name is rejected", func(t *testing.T) {
		_, err := ui.parseModelSlash(cfg, "re.viewer fable")
		require.Error(t, err)
	})

	t.Run("unknown model errors rather than creating a role", func(t *testing.T) {
		_, err := ui.parseModelSlash(cfg, "reviewer nope")
		require.Error(t, err)
	})
}
