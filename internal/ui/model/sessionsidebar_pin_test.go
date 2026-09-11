package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// newPinTestUI is a uiChat model with an open, focused navigator holding
// sample sessions, pinned or not.
func newPinTestUI(t *testing.T, pinned bool) *UI {
	t.Helper()
	m := newTestUI()
	m.keyMap = DefaultKeyMap()
	m.leftSidebar = newTestSidebar(t)
	m.leftSidebar.SetOverviews(sampleOverviews())
	m.state = uiChat
	m.leftSidebarVisible = true
	m.leftSidebarPinned = pinned
	m.focus = uiFocusLeftSidebar
	return m
}

// A pinned navigator survives esc/h: focus returns to the editor but the
// panel stays open.
func TestHandleLeftSidebarKey_EscKeepsPinnedOpen(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"esc", "h"} {
		m := newPinTestUI(t, true)

		var msg tea.KeyPressMsg
		if k == "esc" {
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		} else {
			msg = tea.KeyPressMsg{Code: 'h'}
		}
		_, consumed := m.handleLeftSidebarKey(msg)
		require.True(t, consumed, k)
		require.True(t, m.leftSidebarVisible, "%s must not close a pinned sidebar", k)
		require.Equal(t, uiFocusEditor, m.focus, "%s hands focus back to the editor", k)
	}
}

// Unpinned behavior is unchanged: esc closes the navigator.
func TestHandleLeftSidebarKey_EscClosesUnpinned(t *testing.T) {
	t.Parallel()
	m := newPinTestUI(t, false)

	_, consumed := m.handleLeftSidebarKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, consumed)
	require.False(t, m.leftSidebarVisible)
	require.Equal(t, uiFocusEditor, m.focus)
}

// ctrl+s on a pinned, focused navigator releases focus instead of hiding it.
func TestToggleLeftSidebar_PinnedFocusedReleasesFocus(t *testing.T) {
	t.Parallel()
	m := newPinTestUI(t, true)

	_ = m.toggleLeftSidebar()
	require.True(t, m.leftSidebarVisible, "pinned sidebar stays open")
	require.Equal(t, uiFocusEditor, m.focus)
}

// Activation collapses only an unpinned navigator.
func TestCollapseLeftSidebarAfterActivate(t *testing.T) {
	t.Parallel()
	t.Run("pinned stays open", func(t *testing.T) {
		t.Parallel()
		m := newPinTestUI(t, true)
		m.collapseLeftSidebarAfterActivate()
		require.True(t, m.leftSidebarVisible)
		require.Equal(t, uiFocusEditor, m.focus)
	})
	t.Run("unpinned closes", func(t *testing.T) {
		t.Parallel()
		m := newPinTestUI(t, false)
		m.collapseLeftSidebarAfterActivate()
		require.False(t, m.leftSidebarVisible)
		require.Equal(t, uiFocusEditor, m.focus)
	})
}

// The "p" sidebar key and the global alt+s both map to the pin bindings.
func TestPinBindings_MatchRealKeys(t *testing.T) {
	t.Parallel()
	km := DefaultKeyMap()
	require.True(t, key.Matches(tea.KeyPressMsg{Code: 'p'}, km.SessionSidebar.Pin))
	require.True(t, key.Matches(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt}, km.PinSessions))
	require.False(t, key.Matches(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, km.PinSessions), "ctrl+s must stay the plain toggle")
}
