package dialog

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/styles"
)

func TestAppendModelItems(t *testing.T) {
	t.Parallel()

	st := styles.TokyoNight()
	provider := catwalk.Provider{ID: catwalk.InferenceProvider("openai")}
	models := []catwalk.Model{
		{ID: "gpt-4", Name: "GPT-4"},
		{ID: "gpt-3.5", Name: "GPT-3.5"},
	}

	t.Run("registers all items and finds selected", func(t *testing.T) {
		t.Parallel()
		group := NewModelGroup(&st, "OpenAI", true)
		itemsMap := map[string]*ModelItem{}
		current := config.SelectedModel{Provider: "openai", Model: "gpt-3.5"}

		selected := appendModelItems(&st, &group, provider, models, ModelTypeLarge, current, itemsMap)

		require.Len(t, group.Items, 2)
		require.Len(t, itemsMap, 2)
		require.NotEmpty(t, selected)
		require.Contains(t, itemsMap, selected)
		require.Equal(t, "gpt-3.5", itemsMap[selected].model.ID)
	})

	t.Run("returns empty when nothing matches", func(t *testing.T) {
		t.Parallel()
		group := NewModelGroup(&st, "OpenAI", true)
		itemsMap := map[string]*ModelItem{}
		current := config.SelectedModel{Provider: "anthropic", Model: "claude"}

		selected := appendModelItems(&st, &group, provider, models, ModelTypeLarge, current, itemsMap)
		require.Empty(t, selected)
		require.Len(t, group.Items, 2)
	})

	t.Run("provider must match, not just model id", func(t *testing.T) {
		t.Parallel()
		group := NewModelGroup(&st, "OpenAI", true)
		itemsMap := map[string]*ModelItem{}
		// Same model id but a different provider must not be selected.
		current := config.SelectedModel{Provider: "azure", Model: "gpt-4"}

		selected := appendModelItems(&st, &group, provider, models, ModelTypeLarge, current, itemsMap)
		require.Empty(t, selected)
	})
}
