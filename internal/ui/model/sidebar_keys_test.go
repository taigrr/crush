package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// spaceKeyMsg constructs a space KeyPressMsg the way Bubble Tea v2 delivers
// it. In v2 a space press reports String()=="space" (NOT " "), so a binding
// declared with key.WithKeys(" ") never matches — this guards that
// SessionSidebar.ToggleSelect is bound with the "space" key name.
func spaceKeyMsg() tea.KeyPressMsg { return tea.KeyPressMsg{Code: ' '} }

func TestSessionSidebarBindings_MatchRealKeys(t *testing.T) {
	t.Parallel()
	km := DefaultKeyMap()

	require.Equal(t, "space", spaceKeyMsg().String(),
		"sanity: bubbletea reports space as \"space\"")

	require.True(t, key.Matches(spaceKeyMsg(), km.SessionSidebar.ToggleSelect),
		"space must match ToggleSelect (regression: was bound to \" \")")
	require.True(t, key.Matches(tea.KeyPressMsg{Code: 'v'}, km.SessionSidebar.VisualSelect),
		"v must match VisualSelect")
	require.True(t, key.Matches(tea.KeyPressMsg{Code: 'a'}, km.SessionSidebar.ArchiveSelect),
		"a must match ArchiveSelect")
}

// TestHandleLeftSidebarKey_SpaceTogglesSelection exercises the REAL key
// routing path (handleLeftSidebarKey -> key.Matches -> ToggleSelected), not a
// direct handler call, so a mis-declared space binding is caught.
func TestHandleLeftSidebarKey_SpaceTogglesSelection(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews()) // cursor on a1
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s}

	_, consumed := m.handleLeftSidebarKey(spaceKeyMsg())
	require.True(t, consumed, "space should be consumed by the sidebar")
	require.True(t, s.selected["a1"], "space should toggle the cursor session into the selection")

	_, consumed = m.handleLeftSidebarKey(spaceKeyMsg())
	require.True(t, consumed)
	require.False(t, s.selected["a1"], "space again should toggle it back off")
}

// TestHandleLeftSidebarKey_VEntersVisual exercises the v routing path.
func TestHandleLeftSidebarKey_VEntersVisual(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s}

	_, consumed := m.handleLeftSidebarKey(tea.KeyPressMsg{Code: 'v'})
	require.True(t, consumed)
	require.True(t, s.VisualMode(), "v should enter visual mode")
}
