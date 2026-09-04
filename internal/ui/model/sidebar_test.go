package model

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/workspace"
)

// TestHandleSwarmAddressClick verifies a click on the recorded swarm
// address row inside the right sidebar yields a copy command, and that
// clicks on other rows, outside the sidebar, or when no address is drawn
// fall through unhandled.
func TestHandleSwarmAddressClick(t *testing.T) {
	t.Parallel()

	newUI := func() *UI {
		m := &UI{swarmAddrRow: 7, swarmAddr: "lightpink-bee-3ae0"}
		m.layout.sidebar = image.Rect(80, 0, 120, 40)
		return m
	}

	t.Run("click on address row copies", func(t *testing.T) {
		t.Parallel()
		m := newUI()
		cmd, handled := m.handleSwarmAddressClick(tea.MouseClickMsg{X: 90, Y: 7})
		require.True(t, handled)
		require.NotNil(t, cmd)
	})

	t.Run("click on another sidebar row falls through", func(t *testing.T) {
		t.Parallel()
		m := newUI()
		_, handled := m.handleSwarmAddressClick(tea.MouseClickMsg{X: 90, Y: 8})
		require.False(t, handled)
	})

	t.Run("click on the same row outside the sidebar falls through", func(t *testing.T) {
		t.Parallel()
		m := newUI()
		_, handled := m.handleSwarmAddressClick(tea.MouseClickMsg{X: 10, Y: 7})
		require.False(t, handled)
	})

	t.Run("no address drawn falls through", func(t *testing.T) {
		t.Parallel()
		m := newUI()
		m.swarmAddrRow = -1
		m.swarmAddr = ""
		_, handled := m.handleSwarmAddressClick(tea.MouseClickMsg{X: 90, Y: 7})
		require.False(t, handled)
	})
}

func TestReasoningInfoFor(t *testing.T) {
	t.Parallel()

	t.Run("non-reasoning model has no info", func(t *testing.T) {
		t.Parallel()
		m := &workspace.AgentModel{CatwalkCfg: catwalk.Model{CanReason: false}}
		require.Empty(t, reasoningInfoFor(m))
	})

	t.Run("binary thinking on", func(t *testing.T) {
		t.Parallel()
		m := &workspace.AgentModel{
			CatwalkCfg: catwalk.Model{CanReason: true},
			ModelCfg:   config.SelectedModel{Think: true},
		}
		require.Equal(t, "Thinking On", reasoningInfoFor(m))
	})

	t.Run("binary thinking off", func(t *testing.T) {
		t.Parallel()
		m := &workspace.AgentModel{
			CatwalkCfg: catwalk.Model{CanReason: true},
			ModelCfg:   config.SelectedModel{Think: false},
		}
		require.Equal(t, "Thinking Off", reasoningInfoFor(m))
	})

	t.Run("reasoning effort levels", func(t *testing.T) {
		t.Parallel()
		m := &workspace.AgentModel{
			CatwalkCfg: catwalk.Model{CanReason: true, ReasoningLevels: []string{"low", "high"}},
			ModelCfg:   config.SelectedModel{ReasoningEffort: "high"},
		}
		require.Contains(t, reasoningInfoFor(m), "Reasoning")
	})
}
