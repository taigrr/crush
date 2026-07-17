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
	// Overflow row ("…N more"): open the full session picker for that
	// workspace instead of switching to a specific session.
	if root, ok := m.leftSidebar.SelectedOverflowWorkspace(); ok {
		m.leftSidebarVisible = false
		m.setFocusAfterSidebarClose()
		m.updateLayoutAndSize()
		if m.isCurrentWorkspace(root) {
			return m.openSessionsDialog()
		}
		// Attach/switch this client to the workspace first, then open its
		// picker.
		return m.switchWorkspaceThenPickSession(root)
	}

	root, sessionID, ok := m.leftSidebar.Selected()
	if !ok {
		return nil
	}

	// Collapse the sidebar and return focus to the editor after switching.
	m.leftSidebarVisible = false
	m.setFocusAfterSidebarClose()
	m.updateLayoutAndSize()

	// Compare against THIS client's current workspace, not the server-side
	// "attached" flag: the server can host several workspaces (background
	// runs, other clients), so a workspace this client is not viewing can
	// still report Attached=true. Loading a session directly then would
	// query the wrong (current) workspace DB and silently fail. Only take
	// the fast path when the session lives in the workspace this client is
	// actually pointed at.
	if m.isCurrentWorkspace(root) {
		return m.loadSession(sessionID)
	}
	return m.switchWorkspaceAndLoad(root, sessionID)
}

// isCurrentWorkspace reports whether root is the workspace this client is
// currently viewing (its resolved project root).
func (m *UI) isCurrentWorkspace(root string) bool {
	return root != "" && root == m.com.Workspace.BaseDir()
}

// switchWorkspaceThenPickSession re-targets the client at root and then
// opens the session picker for the now-attached workspace.
func (m *UI) switchWorkspaceThenPickSession(root string) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SwitchWorkspace(context.Background(), root); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		return workspaceSwitchedMsg{openPicker: true}
	}
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
// so the main loop can act on the Update goroutine: load a specific
// session, or open the session picker for the newly attached workspace.
type workspaceSwitchedMsg struct {
	sessionID  string
	openPicker bool
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
