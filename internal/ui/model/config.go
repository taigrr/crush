package model

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/ui/util"
)

// reloadConfig reloads the configuration from disk and reports the result.
func (m *UI) reloadConfig() tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.ReloadConfig(context.Background()); err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  "Failed to reload config: " + err.Error(),
			}
		}
		return util.InfoMsg{
			Type: util.InfoTypeInfo,
			Msg:  "Config reloaded from disk.",
		}
	}
}
