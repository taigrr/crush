package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/fantasy/providers/anthropic"
)

func TestOpus55CatalogProviderOptions(t *testing.T) {
	providers, err := config.Providers(nil)
	require.NoError(t, err)
	for _, tc := range []struct{ provider, id string }{
		{"anthropic", "claude-opus-5-5"},
		{"bedrock", "us.anthropic.claude-opus-5-5"},
		{"bedrock", "global.anthropic.claude-opus-5-5"},
	} {
		id := tc.id
		t.Run(id, func(t *testing.T) {
			found := false
			for _, provider := range providers {
				if string(provider.ID) != tc.provider {
					continue
				}
				for _, catalogModel := range provider.Models {
					if catalogModel.ID != id {
						continue
					}
					found = true
					opts := getProviderOptions(Model{
						CatwalkCfg: catalogModel,
						ModelCfg:   config.SelectedModel{Model: id, ReasoningEffort: catalogModel.DefaultReasoningEffort},
					}, config.ProviderConfig{ID: string(provider.ID), Type: provider.Type})
					parsed, ok := opts[anthropic.Name].(*anthropic.ProviderOptions)
					require.True(t, ok)
					require.Equal(t, new(anthropic.EffortMedium), parsed.Effort)
					require.Equal(t, new("summarized"), parsed.ThinkingDisplay)
					require.Nil(t, parsed.Thinking, "Opus 5.5 rejects manual thinking budgets")
				}
			}
			require.True(t, found, "model must be selectable from the embedded catalog")
		})
	}
}
