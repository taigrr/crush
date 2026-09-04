package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/workspace"
)

// scopedBusyWorkspace models a workspace where a sibling session is
// streaming while the open one is idle.
type scopedBusyWorkspace struct {
	workspace.Workspace
	busySessions map[string]bool
}

func (w *scopedBusyWorkspace) Config() *config.Config { return nil }
func (w *scopedBusyWorkspace) AgentIsReady() bool     { return true }
func (w *scopedBusyWorkspace) AgentIsBusy() bool      { return len(w.busySessions) > 0 }
func (w *scopedBusyWorkspace) AgentIsSessionBusy(id string) bool {
	return w.busySessions[id]
}

// A sibling session's run must not make the open session look busy; the
// workspace-scoped predicate still sees it.
func TestIsAgentBusy_ScopedToOpenSession(t *testing.T) {
	t.Parallel()
	ws := &scopedBusyWorkspace{busySessions: map[string]bool{"worker": true}}
	ui := newSendTestUI(t, ws)

	ui.session = &session.Session{ID: "orchestrator"}
	require.False(t, ui.isAgentBusy(), "idle session is not busy because a sibling is")
	require.True(t, ui.isWorkspaceBusy())

	ui.session = &session.Session{ID: "worker"}
	require.True(t, ui.isAgentBusy())

	ui.session = nil
	require.True(t, ui.isAgentBusy(), "no open session: fall back to workspace scope")
}
