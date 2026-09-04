package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"

	"github.com/taigrr/crush/internal/csync"
)

func modelRefConfig() *Config {
	providers := csync.NewMap[string, ProviderConfig]()
	providers.Set("dp-gpt", ProviderConfig{
		ID:   "dp-gpt",
		Type: "openai-compat",
		Models: []catwalk.Model{
			{ID: "gpt-5.6-sol(xhigh)", Name: "Sol"},
			{ID: "gpt-5.6-luna(low)", Name: "Luna"},
			{ID: "shared-model", Name: "Shared A"},
		},
	})
	providers.Set("dp-claude", ProviderConfig{
		ID:   "dp-claude",
		Type: "anthropic",
		Models: []catwalk.Model{
			{ID: "claude-fable-5-1", Name: "Fable"},
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku"},
			{ID: "shared-model", Name: "Shared B"},
		},
	})
	providers.Set("dp-off", ProviderConfig{
		ID:      "dp-off",
		Type:    "anthropic",
		Disable: true,
		Models:  []catwalk.Model{{ID: "disabled-only"}},
	})
	return &Config{
		Providers: providers,
		Models: map[SelectedModelType]SelectedModel{
			SelectedModelTypeLarge:  {Provider: "dp-gpt", Model: "gpt-5.6-luna(low)", ReasoningEffort: "low"},
			SelectedModelTypeSmall:  {Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"},
			SelectedModelTypeWorker: {Provider: "dp-claude", Model: "claude-fable-5-1", ReasoningEffort: "low"},
			"scout":                 {Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"},
			"broken":                {Provider: "dp-claude", Model: "does-not-exist"},
		},
	}
}

func TestResolveModelRef_Roles(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()

	got, err := c.ResolveModelRef("worker")
	require.NoError(t, err)
	require.Equal(t, "claude-fable-5-1", got.Model)
	require.Equal(t, "low", got.ReasoningEffort, "role selections keep their tuning")

	got, err = c.ResolveModelRef("scout")
	require.NoError(t, err)
	require.Equal(t, "dp-claude", got.Provider)
	require.Equal(t, "claude-haiku-4-5-20251001", got.Model)

	_, err = c.ResolveModelRef("broken")
	require.ErrorContains(t, err, `role "broken"`)
}

func TestResolveModelRef_Qualified(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()
	got, err := c.ResolveModelRef("dp-claude/claude-fable-5-1")
	require.NoError(t, err)
	require.Equal(t, SelectedModel{Provider: "dp-claude", Model: "claude-fable-5-1"}, got)

	_, err = c.ResolveModelRef("dp-claude/nope")
	require.ErrorContains(t, err, `has no model "nope"`)

	_, err = c.ResolveModelRef("dp-off/disabled-only")
	require.ErrorContains(t, err, "disabled")
}

func TestResolveModelRef_Bare(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()
	got, err := c.ResolveModelRef("claude-fable-5-1")
	require.NoError(t, err)
	require.Equal(t, "dp-claude", got.Provider)

	_, err = c.ResolveModelRef("disabled-only")
	require.ErrorContains(t, err, "unknown model")

	_, err = c.ResolveModelRef("  ")
	require.Error(t, err)

	// A typed id that differs only by case still resolves when unique.
	got, err = c.ResolveModelRef("CLAUDE-FABLE-5-1")
	require.NoError(t, err)
	require.Equal(t, "claude-fable-5-1", got.Model)
}

// A bare id present on two providers must error rather than silently
// picking one; the message must tell the caller how to qualify it.
func TestResolveModelRef_Ambiguous(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()
	_, err := c.ResolveModelRef("shared-model")
	require.ErrorContains(t, err, "ambiguous")
	require.ErrorContains(t, err, "dp-claude, dp-gpt")
	require.ErrorContains(t, err, "<provider>/shared-model")

	got, err := c.ResolveModelRef("dp-gpt/shared-model")
	require.NoError(t, err)
	require.Equal(t, "dp-gpt", got.Provider)
}

func TestWorkerModelAndRoles(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()
	sel, ok := c.WorkerModel()
	require.True(t, ok)
	require.Equal(t, "claude-fable-5-1", sel.Model)
	require.Equal(t, []SelectedModelType{"broken", "scout"}, c.ModelRoles())

	delete(c.Models, SelectedModelTypeWorker)
	_, ok = c.WorkerModel()
	require.False(t, ok)

	c.Models[SelectedModelTypeWorker] = SelectedModel{Provider: "dp-claude", Model: "ghost"}
	_, ok = c.WorkerModel()
	require.False(t, ok, "unresolvable worker must read as unconfigured")
}

// Config loading validates the worker slot and user roles: a
// resolvable one keeps its tuning and gains catalog defaults; an
// unresolvable one is dropped rather than replaced with a fallback.
func TestConfigureSelectedModels_RolesValidatedAndBackfilled(t *testing.T) {
	t.Parallel()
	c := modelRefConfig()
	c.Providers.Set("dp-claude", ProviderConfig{
		ID:   "dp-claude",
		Type: "anthropic",
		Models: []catwalk.Model{
			{ID: "claude-fable-5-1", Name: "Fable", DefaultMaxTokens: 16000, CanReason: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoningEffort: "high"},
			{ID: "claude-haiku-4-5-20251001", Name: "Haiku", DefaultMaxTokens: 8000},
		},
	})
	c.Providers.Set("dp-gpt", ProviderConfig{
		ID:   "dp-gpt",
		Type: "openai-compat",
		Models: []catwalk.Model{
			{ID: "gpt-5.6-luna(low)", Name: "Luna", DefaultMaxTokens: 4096},
		},
	})
	store := &ConfigStore{config: c}
	require.NoError(t, configureSelectedModels(store, nil, false))

	worker, ok := c.Models[SelectedModelTypeWorker]
	require.True(t, ok, "resolvable worker must survive")
	require.Equal(t, "low", worker.ReasoningEffort, "explicit effort wins over the catalog default")
	require.Equal(t, int64(16000), worker.MaxTokens, "max tokens backfilled from the catalog")

	scout, ok := c.Models["scout"]
	require.True(t, ok)
	require.Equal(t, int64(8000), scout.MaxTokens)

	_, ok = c.Models["broken"]
	require.False(t, ok, "unresolvable role must be dropped, not defaulted")
	require.Equal(t, []SelectedModelType{"scout"}, c.ModelRoles())
}

func looseRefConfig() *Config {
	providers := csync.NewMap[string, ProviderConfig]()
	providers.Set("bedrock", ProviderConfig{
		ID:   "bedrock",
		Type: "bedrock",
		Models: []catwalk.Model{
			{ID: "us.anthropic.claude-fable-5", Name: "Claude Fable 5", ReasoningLevels: []string{"low", "medium", "high"}},
			{ID: "us.anthropic.claude-fable-5-1", Name: "Claude Fable 5.1", ReasoningLevels: []string{"low", "medium", "high", "xhigh"}},
			{ID: "global.anthropic.claude-fable-5-1", Name: "Claude Fable 5.1 (global)", ReasoningLevels: []string{"low", "medium", "high", "xhigh"}},
			{ID: "us.anthropic.claude-haiku-4-5-20251001-v1:0", Name: "Claude Haiku 4.5"},
		},
	})
	providers.Set("off", ProviderConfig{ID: "off", Type: "anthropic", Disable: true, Models: []catwalk.Model{{ID: "fable-off"}}})
	return &Config{
		Providers: providers,
		Models: map[SelectedModelType]SelectedModel{
			SelectedModelTypeLarge: {Provider: "bedrock", Model: "us.anthropic.claude-fable-5-1"},
			SelectedModelTypeSmall: {Provider: "bedrock", Model: "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
		},
	}
}

func TestResolveModelRefLoose(t *testing.T) {
	c := looseRefConfig()

	t.Run("exact grammar still wins", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("large")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5-1", sel.Model)
		sel, err = c.ResolveModelRefLoose("bedrock/us.anthropic.claude-fable-5")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5", sel.Model)
	})

	t.Run("substring prefers the model holding a role", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("fable")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5-1", sel.Model)
	})

	t.Run("substring falls back to shortest id when nothing holds a role", func(t *testing.T) {
		c2 := looseRefConfig()
		c2.Models = nil
		sel, err := c2.ResolveModelRefLoose("fable-5-1")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5-1", sel.Model, "us. is shorter than global.")
	})

	t.Run("a single weak subsequence hit is not acted on", func(t *testing.T) {
		_, err := c.ResolveModelRefLoose("hku")
		require.Error(t, err)
		require.Contains(t, err.Error(), "did you mean")
	})

	t.Run("display name and case-insensitive", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("HAIKU")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-haiku-4-5-20251001-v1:0", sel.Model)
	})

	t.Run("multi-word name match", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("fable 5.1")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5-1", sel.Model)
	})

	t.Run("trailing effort applies", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("fable xhigh")
		require.NoError(t, err)
		require.Equal(t, "us.anthropic.claude-fable-5-1", sel.Model)
		require.Equal(t, "xhigh", sel.ReasoningEffort)
	})

	t.Run("effort on a role", func(t *testing.T) {
		sel, err := c.ResolveModelRefLoose("large high")
		require.NoError(t, err)
		require.Equal(t, "high", sel.ReasoningEffort)
	})

	t.Run("unsupported effort errors", func(t *testing.T) {
		_, err := c.ResolveModelRefLoose("haiku high")
		require.ErrorContains(t, err, "does not support reasoning effort")
	})

	t.Run("disabled providers are invisible", func(t *testing.T) {
		_, err := c.ResolveModelRefLoose("fable-off")
		require.Error(t, err)
	})

	t.Run("no match", func(t *testing.T) {
		_, err := c.ResolveModelRefLoose("zzzz-nope")
		require.ErrorContains(t, err, "unknown model")
	})

	t.Run("true tie is ambiguous", func(t *testing.T) {
		c2 := looseRefConfig()
		c2.Models = nil
		p, _ := c2.Providers.Get("bedrock")
		p.Models = append(p.Models, catwalk.Model{ID: "eu.anthropic.claude-fable-5-1", Name: "Fable EU"})
		c2.Providers.Set("bedrock", p)
		_, err := c2.ResolveModelRefLoose("fable-5-1")
		var amb *ErrAmbiguousModelRef
		require.ErrorAs(t, err, &amb)
		require.Len(t, amb.Matches, 3)
	})
}
