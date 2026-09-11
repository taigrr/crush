package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"

	"github.com/taigrr/crush/internal/csync"
)

// modelDefaultsStore builds a store whose catalog carries a reasoning
// ladder, so selections can be backfilled from it.
func modelDefaultsStore(t *testing.T) (*ConfigStore, *Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{}
	cfg.setDefaults(dir, "")
	cfg.Providers = csync.NewMap[string, ProviderConfig]()
	cfg.Providers.Set("dp-gpt", ProviderConfig{
		ID: "dp-gpt",
		Models: []catwalk.Model{{
			ID:                     "gpt-5.6-sol(xhigh)",
			Name:                   "Sol",
			DefaultMaxTokens:       128000,
			CanReason:              true,
			ReasoningLevels:        []string{"low", "medium", "high", "xhigh", "max"},
			DefaultReasoningEffort: "xhigh",
		}, {
			// No reasoning ladder: must not gain an effort.
			ID:               "plain-model",
			Name:             "Plain",
			DefaultMaxTokens: 4096,
		}},
	})
	return testStoreWithPath(cfg, dir), cfg
}

// Regression: the model dialog and `crush run -m` build a selection from
// just provider+model. Without backfill the selection carries no
// reasoning effort, shouldSetEffort never fires, and the provider is
// never asked to reason — thinking traces silently disappear after a
// model switch.
func TestUpdatePreferredModel_BackfillsReasoningEffort(t *testing.T) {
	t.Parallel()
	store, cfg := modelDefaultsStore(t)

	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge,
		SelectedModel{Provider: "dp-gpt", Model: "gpt-5.6-sol(xhigh)"}))

	got := cfg.Models[SelectedModelTypeLarge]
	require.Equal(t, "xhigh", got.ReasoningEffort, "effort must be seeded from the catalog")
	require.Equal(t, int64(128000), got.MaxTokens, "max tokens must be seeded from the catalog")
}

// An explicit effort is the user's choice and must survive.
func TestUpdatePreferredModel_ExplicitEffortWins(t *testing.T) {
	t.Parallel()
	store, cfg := modelDefaultsStore(t)

	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge,
		SelectedModel{Provider: "dp-gpt", Model: "gpt-5.6-sol(xhigh)", ReasoningEffort: "low"}))

	require.Equal(t, "low", cfg.Models[SelectedModelTypeLarge].ReasoningEffort)
}

// A model with no reasoning ladder must not be given an effort: an
// unrecognized value is a hard request error on some providers.
func TestUpdatePreferredModel_NoLadderNoEffort(t *testing.T) {
	t.Parallel()
	store, cfg := modelDefaultsStore(t)

	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge,
		SelectedModel{Provider: "dp-gpt", Model: "plain-model"}))

	got := cfg.Models[SelectedModelTypeLarge]
	require.Empty(t, got.ReasoningEffort, "a model without reasoning_levels must not gain an effort")
	require.Equal(t, int64(4096), got.MaxTokens)
}

// An unknown model is left untouched rather than panicking; validation
// lives elsewhere.
func TestUpdatePreferredModel_UnknownModelUntouched(t *testing.T) {
	t.Parallel()
	store, cfg := modelDefaultsStore(t)

	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge,
		SelectedModel{Provider: "dp-gpt", Model: "does-not-exist"}))

	got := cfg.Models[SelectedModelTypeLarge]
	require.Empty(t, got.ReasoningEffort)
	require.Zero(t, got.MaxTokens)
}
