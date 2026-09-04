package model

import (
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/workspace"
)

// connectionStatusLine renders a single status row reusing the same
// resource-status icon language as the LSP/MCP sections (see
// t.Resource.{Busy,Error}Icon). It returns "" when the connection is
// healthy (ConnectionStateConnected) so it only takes up space in the
// sidebar while there is something worth telling the user about,
// mirroring how the header's LSP error count only appears when > 0.
func connectionStatusLine(t *styles.Styles, connState workspace.ConnectionState, width int) string {
	var icon, title string
	switch connState {
	case workspace.ConnectionStateConnecting:
		icon = t.Resource.BusyIcon.String()
		title = "Connecting to server…"
	case workspace.ConnectionStateReconnecting:
		icon = t.Resource.ErrorIcon.String()
		title = "Connection lost, reconnecting…"
	case workspace.ConnectionStateUpdating:
		icon = t.Resource.BusyIcon.String()
		title = "Server updating, reconnecting…"
	default:
		return ""
	}
	return common.Status(t, common.StatusOpts{
		Icon:  icon,
		Title: title,
	}, width)
}
