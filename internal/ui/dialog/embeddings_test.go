package dialog

import (
	"testing"

	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestEmbeddingItem_MutatorsBumpVersion covers the version-bump contract
// for the embedding picker items (mirrors the other dialog item tests).
func TestEmbeddingItem_MutatorsBumpVersion(t *testing.T) {
	t.Parallel()

	sty := themes.CharmtonePantera()
	item := &EmbeddingItem{
		Versioned: list.NewVersioned(),
		choice:    EmbeddingChoice{Provider: "bedrock", Model: "amazon.titan-embed-text-v2:0", Name: "Titan v2", Dimensions: 1024, Configured: true},
		t:         &sty,
	}

	requireBump(t, "SetFocused[true]", item, func() {
		item.SetFocused(true)
	})
	requireNoBump(t, "SetFocused[true again]", item, func() {
		item.SetFocused(true)
	})
	requireBump(t, "SetFocused[false]", item, func() {
		item.SetFocused(false)
	})

	match := fuzzy.Match{Str: "bedrock", Index: 0, Score: 5, MatchedIndexes: []int{0, 1, 2}}
	requireBump(t, "SetMatch[new]", item, func() {
		item.SetMatch(match)
	})
	requireNoBump(t, "SetMatch[same]", item, func() {
		item.SetMatch(equivMatch(match))
	})
}

// TestEmbeddingItem_Render covers the disabled and configured/unconfigured
// rendering branches.
func TestEmbeddingItem_Render(t *testing.T) {
	t.Parallel()
	sty := themes.CharmtonePantera()

	disabled := &EmbeddingItem{Versioned: list.NewVersioned(), choice: EmbeddingChoice{}, isCurrent: true, t: &sty}
	require.Contains(t, disabled.Render(80), "Disabled")
	require.Contains(t, disabled.Render(80), "current")

	unconfigured := &EmbeddingItem{
		Versioned: list.NewVersioned(),
		choice:    EmbeddingChoice{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 1536, Configured: false},
		t:         &sty,
	}
	out := unconfigured.Render(80)
	require.Contains(t, out, "openai")
	require.Contains(t, out, "1536 dims")
	require.Contains(t, out, "not configured")
}

func TestEmbeddingChoiceDisabled(t *testing.T) {
	t.Parallel()
	require.True(t, EmbeddingChoice{}.disabled())
	require.False(t, EmbeddingChoice{Provider: "openai", Model: "m"}.disabled())
}
