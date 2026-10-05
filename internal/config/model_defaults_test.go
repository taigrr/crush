package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
)

// modelDefaultsStore builds a store backed by a provider whose models carry
// catalog defaults the tests can assert on.
func modelDefaultsStore(t *testing.T) *ConfigStore {
	t.Helper()

	dir := t.TempDir()
	cfg := &Config{}
	cfg.setDefaults(dir, "")
	cfg.Providers.Set("dp-claude", ProviderConfig{
		ID:   "dp-claude",
		Type: "anthropic",
		Models: []catwalk.Model{
			{ID: "fable", Name: "Fable", DefaultMaxTokens: 16000, CanReason: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoningEffort: "high"},
			{ID: "haiku", Name: "Haiku", DefaultMaxTokens: 8000},
			{ID: "odd", Name: "Odd", DefaultMaxTokens: 4000, CanReason: true, ReasoningLevels: []string{"low"}, DefaultReasoningEffort: "ultra"},
		},
	})
	return testStoreWithPath(cfg, dir)
}

func TestUpdatePreferredModel_BackfillsCatalogDefaults(t *testing.T) {
	t.Parallel()
	store := modelDefaultsStore(t)

	sel := SelectedModel{Provider: "dp-claude", Model: "fable"}
	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge, sel))

	got := store.config.Models[SelectedModelTypeLarge]
	require.Equal(t, int64(16000), got.MaxTokens, "max tokens seeded from catalog")
	require.Equal(t, "high", got.ReasoningEffort, "reasoning effort seeded from catalog")

	persisted := readConfigJSON(t, store.globalDataPath)
	models := persisted["models"].(map[string]any)
	large := models[string(SelectedModelTypeLarge)].(map[string]any)
	require.Equal(t, float64(16000), large["max_tokens"])
	require.Equal(t, "high", large["reasoning_effort"])
}

func TestUpdatePreferredModel_ExplicitValuesWin(t *testing.T) {
	t.Parallel()
	store := modelDefaultsStore(t)

	sel := SelectedModel{Provider: "dp-claude", Model: "fable", MaxTokens: 1234, ReasoningEffort: "low"}
	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge, sel))

	got := store.config.Models[SelectedModelTypeLarge]
	require.Equal(t, int64(1234), got.MaxTokens)
	require.Equal(t, "low", got.ReasoningEffort)
}

func TestUpdatePreferredModel_NoEffortForNonReasoningModel(t *testing.T) {
	t.Parallel()
	store := modelDefaultsStore(t)

	sel := SelectedModel{Provider: "dp-claude", Model: "haiku"}
	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeSmall, sel))

	got := store.config.Models[SelectedModelTypeSmall]
	require.Equal(t, int64(8000), got.MaxTokens)
	require.Empty(t, got.ReasoningEffort)
}

func TestUpdatePreferredModel_SkipsEffortNotInReasoningLevels(t *testing.T) {
	t.Parallel()
	store := modelDefaultsStore(t)

	sel := SelectedModel{Provider: "dp-claude", Model: "odd"}
	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge, sel))

	got := store.config.Models[SelectedModelTypeLarge]
	require.Equal(t, int64(4000), got.MaxTokens)
	require.Empty(t, got.ReasoningEffort, "catalog default not advertised in reasoning_levels must not be seeded")
}

func TestUpdatePreferredModel_UnknownModelLeftUntouched(t *testing.T) {
	t.Parallel()
	store := modelDefaultsStore(t)

	sel := SelectedModel{Provider: "dp-claude", Model: "ghost"}
	require.NoError(t, store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge, sel))

	require.Equal(t, sel, store.config.Models[SelectedModelTypeLarge])
}
