package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestQueueControlKeyBindings(t *testing.T) {
	t.Parallel()
	km := DefaultKeyMap()

	press := func(code rune, mod tea.KeyMod) tea.KeyPressMsg {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}

	require.True(t, key.Matches(press('p', tea.ModCtrl|tea.ModShift), km.PromoteQueued))
	require.True(t, key.Matches(press('p', tea.ModAlt), km.PromoteQueued))
	require.False(t, key.Matches(press('p', tea.ModCtrl), km.PromoteQueued), "plain ctrl+p stays the command palette")

	require.True(t, key.Matches(press('c', tea.ModCtrl|tea.ModShift), km.CancelTool))
	require.True(t, key.Matches(press('c', tea.ModAlt), km.CancelTool))
	require.False(t, key.Matches(press('c', tea.ModCtrl), km.CancelTool), "plain ctrl+c stays quit")
}
