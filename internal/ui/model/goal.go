package model

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/ui/util"
)

// goalClearVerbs are the sub-verbs that clear an active goal, matching the
// alias set used by comparable tools.
var goalClearVerbs = []string{"clear", "stop", "off", "reset", "none", "cancel"}

// handleGoal implements the /goal command. With no args it reports status;
// with a clear verb it clears the goal; otherwise it sets the goal to the
// given condition. The caller (dispatchSlash) guarantees an active session.
func (m *UI) handleGoal(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	switch {
	case args == "":
		return m.goalStatus()
	case slices.Contains(goalClearVerbs, strings.ToLower(args)):
		return m.goalClear()
	default:
		return m.goalSet(args)
	}
}

// goalSet activates an autonomous goal for the active session.
func (m *UI) goalSet(condition string) tea.Cmd {
	sessionID := m.session.ID
	return func() tea.Msg {
		if err := m.com.Workspace.AgentSetGoal(sessionID, condition); err != nil {
			return util.NewErrorMsg(err)
		}
		return util.NewInfoMsg("Goal set; the agent will keep working until it's met. Use /goal clear to stop.")
	}
}

// goalClear removes any active goal for the session.
func (m *UI) goalClear() tea.Cmd {
	sessionID := m.session.ID
	return func() tea.Msg {
		if err := m.com.Workspace.AgentClearGoal(sessionID); err != nil {
			return util.NewErrorMsg(err)
		}
		return util.NewInfoMsg("Goal cleared.")
	}
}

// goalStatus reports the active goal for the session.
func (m *UI) goalStatus() tea.Cmd {
	sessionID := m.session.ID
	return func() tea.Msg {
		status, err := m.com.Workspace.AgentGoalStatus(sessionID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		if !status.Active {
			return util.NewInfoMsg("No active goal. Set one with /goal <condition>.")
		}
		return util.NewInfoMsg(fmt.Sprintf(
			"Goal active (%d/%d turns): %s",
			status.Turns, status.MaxTurns, status.Condition,
		))
	}
}
