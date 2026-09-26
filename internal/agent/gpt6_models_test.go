package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/fantasy/providers/openai"
)

func TestGPT6CatalogProviderOptions(t *testing.T) {
	providers, err := config.Providers(nil)
	require.NoError(t, err)
	for _, providerID := range []string{"openai"} {
		for _, suffix := range []string{"astra", "sol", "luna"} {
			id := "gpt-6-" + suffix
			t.Run(providerID+"/"+id, func(t *testing.T) {
				found := false
				for _, provider := range providers {
					if string(provider.ID) != providerID {
						continue
					}
					for _, catalogModel := range provider.Models {
						if catalogModel.ID != id {
							continue
						}
						found = true
						opts := getProviderOptions(Model{
							CatwalkCfg: catalogModel,
							ModelCfg:   config.SelectedModel{Model: id, ReasoningEffort: "max"},
						}, config.ProviderConfig{ID: providerID, Type: provider.Type})
						parsed, ok := opts[openai.Name].(*openai.ResponsesProviderOptions)
						require.True(t, ok, "reasoning tool calls must use Responses options")
						require.Equal(t, new(openai.ReasoningEffortMax), parsed.ReasoningEffort)
						require.Equal(t, new("auto"), parsed.ReasoningSummary)
						require.Equal(t, []openai.IncludeType{openai.IncludeReasoningEncryptedContent}, parsed.Include)
					}
				}
				require.True(t, found, "model must be selectable from the embedded catalog")
			})
		}
	}
}
