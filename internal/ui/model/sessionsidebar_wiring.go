package model

import (
	"context"
	"image"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/util"
)

// leftSidebarWidth is the fixed width of the left session navigator. It
// matches the right info sidebar's width for visual parity.
const leftSidebarWidth = 30

// workspaceOverviewsMsg carries the result of a background overview fetch.
type workspaceOverviewsMsg struct {
	overviews []proto.WorkspaceOverview
}

// loadWorkspaceOverviews fetches the cross-workspace session overviews off
// the Update goroutine.
func (m *UI) loadWorkspaceOverviews() tea.Cmd {
	return func() tea.Msg {
		overviews, err := m.com.Workspace.ListWorkspaceOverviews(context.Background())
		if err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		return workspaceOverviewsMsg{overviews: overviews}
	}
}

// toggleLeftSidebar shows/hides the left session navigator. Showing it also
// focuses it and refreshes its data; hiding it returns focus to the editor.
func (m *UI) toggleLeftSidebar() tea.Cmd {
	if m.leftSidebarVisible {
		m.leftSidebarVisible = false
		if m.focus == uiFocusLeftSidebar {
			m.setFocusAfterSidebarClose()
		}
		m.updateLayoutAndSize()
		return nil
	}
	m.leftSidebarVisible = true
	m.focus = uiFocusLeftSidebar
	if m.session != nil {
		m.leftSidebar.SetActiveSession(m.session.ID)
	}
	m.updateLayoutAndSize()
	return m.loadWorkspaceOverviews()
}

// setFocusAfterSidebarClose restores a sensible focus when the sidebar
// loses focus (editor in chat/landing, else none).
func (m *UI) setFocusAfterSidebarClose() {
	switch m.state {
	case uiChat, uiLanding:
		m.focus = uiFocusEditor
		m.textarea.Focus()
	default:
		m.focus = uiFocusNone
	}
}

// handleLeftSidebarKey processes navigation/selection keys while the left
// sidebar is focused. It returns the command to run and whether the key was
// consumed.
func (m *UI) handleLeftSidebarKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		m.leftSidebar.MoveUp()
		return nil, true
	case "down", "j":
		m.leftSidebar.MoveDown()
		return nil, true
	case "enter", "l":
		return m.activateLeftSidebarSelection(), true
	case "esc", "h":
		m.leftSidebarVisible = false
		m.setFocusAfterSidebarClose()
		m.updateLayoutAndSize()
		return nil, true
	}
	return nil, false
}

// activateLeftSidebarSelection switches to the session under the cursor. If
// it lives in the currently attached workspace it is a plain session load;
// otherwise the client re-targets that workspace first (leaving the old one
// running on the server) and then loads the session.
func (m *UI) activateLeftSidebarSelection() tea.Cmd {
	root, sessionID, ok := m.leftSidebar.Selected()
	if !ok {
		return nil
	}

	// Collapse the sidebar and return focus to the editor after switching.
	m.leftSidebarVisible = false
	m.setFocusAfterSidebarClose()
	m.updateLayoutAndSize()

	if m.leftSidebar.SelectedWorkspaceAttached() {
		return m.loadSession(sessionID)
	}
	return m.switchWorkspaceAndLoad(root, sessionID)
}

// switchWorkspaceAndLoad re-targets the client at the workspace rooted at
// root, then loads the session. The attach happens off the Update goroutine.
func (m *UI) switchWorkspaceAndLoad(root, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SwitchWorkspace(context.Background(), root); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		return workspaceSwitchedMsg{sessionID: sessionID}
	}
}

// workspaceSwitchedMsg is emitted after a successful cross-workspace switch
// so the main loop can load the target session on the Update goroutine.
type workspaceSwitchedMsg struct {
	sessionID string
}

// drawLeftSidebar renders the left session navigator into area.
func (m *UI) drawLeftSidebar(scr uv.Screen, area image.Rectangle) {
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}
	focused := m.focus == uiFocusLeftSidebar
	view := m.leftSidebar.Render(area.Dx(), area.Dy(), focused)
	uv.NewStyledString(view).Draw(scr, area)
}
