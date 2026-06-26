package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/workspace"
)

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
