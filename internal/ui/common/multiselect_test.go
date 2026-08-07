package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMultiSelect_ToggleDiscrete(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.Toggle("a")
	m.Toggle("c")
	require.True(t, m.Selected("a"))
	require.True(t, m.Selected("c"))
	require.False(t, m.Selected("b"))
	require.Equal(t, 2, m.Count())
	require.Equal(t, []string{"a", "c"}, m.IDs()) // sorted

	m.Toggle("a") // toggle off
	require.False(t, m.Selected("a"))
	require.Equal(t, []string{"c"}, m.IDs())
}

func TestMultiSelect_VisualSweepAdditive(t *testing.T) {
	t.Parallel()
	order := []string{"a", "b", "c", "d"}
	var m MultiSelect
	m.ToggleVisual("a") // anchor a, selects a
	require.True(t, m.Visual())
	require.Equal(t, []string{"a"}, m.IDs())

	m.ExtendVisual(order, "c") // sweep a..c
	require.Equal(t, []string{"a", "b", "c"}, m.IDs())

	m.ExtendVisual(order, "b") // move back: additive, stays
	require.Equal(t, []string{"a", "b", "c"}, m.IDs())
}

func TestMultiSelect_ToggleVisualExits(t *testing.T) {
	t.Parallel()
	order := []string{"a", "b", "c"}
	var m MultiSelect
	m.ToggleVisual("a")
	m.ExtendVisual(order, "c")
	require.NotEmpty(t, m.IDs())

	m.ToggleVisual("") // second v exits + clears
	require.False(t, m.Visual())
	require.Empty(t, m.IDs())
}

func TestMultiSelect_AnchorSurvivesReorder(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.ToggleVisual("a") // anchor a
	// Order reflows so a is now last; sweeping to b must still span a..b by
	// their positions in the NEW order (anchor resolved by ID, not index).
	newOrder := []string{"c", "b", "a"}
	m.ExtendVisual(newOrder, "b") // b at idx1, a at idx2 -> {b,a}
	require.Equal(t, []string{"a", "b"}, m.IDs())
}

func TestMultiSelect_AnchorVanishedReanchors(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.ToggleVisual("gone") // anchor an id not in the order
	order := []string{"a", "b", "c"}
	m.ExtendVisual(order, "b") // anchor missing -> re-anchor at cursor b
	require.Equal(t, []string{"b", "gone"}, m.IDs())
}

func TestMultiSelect_SetSelectionAndClear(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.ToggleVisual("a")
	m.SetSelection([]string{"x", "y"})
	require.False(t, m.Visual())
	require.Equal(t, []string{"x", "y"}, m.IDs())

	m.Clear()
	require.Empty(t, m.IDs())
	require.False(t, m.Visual())
}

func TestMultiSelect_ExtendVisualNoopWhenNotVisual(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.Toggle("a")
	m.ExtendVisual([]string{"a", "b", "c"}, "c") // not in visual mode
	require.Equal(t, []string{"a"}, m.IDs())
}

func TestMultiSelect_RetainDropsUnknown(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.Toggle("a")
	m.Toggle("b")
	m.Toggle("gone")
	pruned := m.Retain(map[string]bool{"a": true, "b": true})
	require.Equal(t, 1, pruned)
	require.Equal(t, []string{"a", "b"}, m.IDs())
}

func TestMultiSelect_RetainClearsPrunedAnchor(t *testing.T) {
	t.Parallel()
	var m MultiSelect
	m.ToggleVisual("a") // anchor a
	// Re-anchor away leaves "gone" behavior aside; simulate the anchor
	// becoming unknown and assert Retain clears it so a later ExtendVisual
	// re-anchors at the cursor instead of spanning from a phantom.
	m.ExtendVisual([]string{"c", "a"}, "c") // still visual, anchor a present
	m.Retain(map[string]bool{"c": true})    // a pruned (anchor gone)
	require.Equal(t, []string{"c"}, m.IDs())
	// Anchor was cleared: extending now to a new order re-anchors at cursor.
	m.ExtendVisual([]string{"c", "d", "e"}, "e")
	require.Equal(t, []string{"c", "e"}, m.IDs()) // re-anchored at e -> {e}, plus retained c
}
