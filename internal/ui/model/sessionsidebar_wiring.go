package model

import (
	"context"
	"image"

	"charm.land/bubbles/v2/key"

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
		m.leftSidebar.ClearSelection()
		if m.focus == uiFocusLeftSidebar {
			m.setFocusAfterSidebarClose()
		}
		m.updateLayoutAndSize()
		return nil
	}
	m.leftSidebarVisible = true
	m.focus = uiFocusLeftSidebar
	m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
	if m.session != nil {
		m.leftSidebar.SetActiveSession(m.session.ID)
	}
	m.updateLayoutAndSize()
	return m.loadWorkspaceOverviews()
}

// setFocusAfterSidebarClose restores a sensible focus when the sidebar
// loses focus (editor in chat/landing, else none). It also drops any
// pending multi-selection so a selection never outlives a focus session
// (preventing an accidental bulk archive after close/reopen).
func (m *UI) setFocusAfterSidebarClose() {
	m.leftSidebar.ClearSelection()
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
	// Vim-style navigation mirrors the right info sidebar: j/k move,
	// g/Home jumps to the top, G/End to the bottom.
	switch {
	case key.Matches(msg, m.keyMap.Chat.Up):
		m.leftSidebar.MoveUp()
		return nil, true
	case key.Matches(msg, m.keyMap.Chat.Down):
		m.leftSidebar.MoveDown()
		return nil, true
	case key.Matches(msg, m.keyMap.Chat.Home):
		m.leftSidebar.MoveTop()
		return nil, true
	case key.Matches(msg, m.keyMap.Chat.End):
		m.leftSidebar.MoveBottom()
		return nil, true
	case key.Matches(msg, m.keyMap.SessionSidebar.VisualSelect):
		m.leftSidebar.ToggleVisualMode()
		return nil, true
	case key.Matches(msg, m.keyMap.SessionSidebar.ToggleSelect):
		m.leftSidebar.ToggleSelected()
		return nil, true
	case key.Matches(msg, m.keyMap.SessionSidebar.ArchiveSelect):
		return m.archiveSelectedSessions(), true
	}
	switch msg.String() {
	case "enter", "l":
		return m.activateLeftSidebarSelection(), true
	case "esc", "h":
		// Esc first exits visual selection / clears a pending multi-
		// selection; only when there is nothing selected does it close
		// the sidebar. "h" always closes.
		if msg.String() == "esc" && (m.leftSidebar.VisualMode() || m.leftSidebar.SelectionCount() > 0) {
			m.leftSidebar.ClearSelection()
			return nil, true
		}
		m.leftSidebarVisible = false
		m.setFocusAfterSidebarClose()
		m.updateLayoutAndSize()
		return nil, true
	}
	return nil, false
}

// archiveSelectedSessions archives the selected sessions in the CURRENT
// workspace. Sessions from other workspaces and the active session are
// skipped (the client archive API only reaches the attached workspace).
// The selection is NOT cleared here: it is trimmed to the failures only
// after the archive command reports, so failures stay selected for a retry.
func (m *UI) archiveSelectedSessions() tea.Cmd {
	toArchive, skippedActive, skippedWorkspace := m.leftSidebar.ArchivableSelection()
	if len(toArchive) == 0 {
		if skippedActive+skippedWorkspace > 0 {
			return util.ReportWarn("Selected sessions can't be archived from here; nothing to archive")
		}
		return util.ReportInfo("No sessions selected")
	}
	survivor := m.leftSidebar.SurvivingNeighbor(toArchive)
	return m.archiveSessionsCmd(toArchive, survivor, skippedActive+skippedWorkspace)
}

// sessionsArchivedMsg reports the outcome of a bulk archive: the refreshed
// overviews (nil if the refresh itself failed), which IDs failed (so they
// stay selected), how many succeeded, how many were skipped as
// out-of-workspace, and the neighbor session to focus.
type sessionsArchivedMsg struct {
	overviews  []proto.WorkspaceOverview
	failed     []string
	succeeded  int
	skipped    int
	survivorID string
}

// archiveSessionsCmd archives the given session IDs off the Update
// goroutine. It attempts EVERY id (deterministic order, set by the caller)
// and collects the individual failures rather than aborting on the first,
// so the outcome is stable. It refreshes the overviews afterwards; if that
// refresh fails it still emits sessionsArchivedMsg (with nil overviews) so
// the successfully-archived IDs are dropped from the selection and only the
// failures remain — never leaving now-gone sessions selected.
func (m *UI) archiveSessionsCmd(ids []string, survivorID string, skipped int) tea.Cmd {
	return func() tea.Msg {
		succeeded := 0
		var failed []string
		for _, id := range ids {
			if err := m.com.Workspace.ArchiveSession(context.Background(), id); err != nil {
				failed = append(failed, id)
				continue
			}
			succeeded++
		}
		overviews, err := m.com.Workspace.ListWorkspaceOverviews(context.Background())
		if err != nil {
			// Refresh failed, but archives may have succeeded: still report
			// so the selection is trimmed to the failures (overviews nil).
			return sessionsArchivedMsg{
				failed:     failed,
				succeeded:  succeeded,
				skipped:    skipped,
				survivorID: survivorID,
			}
		}
		return sessionsArchivedMsg{
			overviews:  overviews,
			failed:     failed,
			succeeded:  succeeded,
			skipped:    skipped,
			survivorID: survivorID,
		}
	}
}

// activeSessionArchivedMsg reports the outcome of archiving the current
// (active) session from the main window. nextSessionID is the session to
// switch to afterwards (most-recently-updated remaining in the current
// workspace), or empty when none remain (switch to the empty landing
// state so the user never keeps viewing an archived session).
type activeSessionArchivedMsg struct {
	err           error
	nextSessionID string
}

// archiveCurrentSession archives the active session, then resolves the
// switch-away target off the Update goroutine: the most-recently-updated
// remaining session in the current workspace (ListSessions is ordered by
// updated_at desc), or empty if the workspace has no other sessions.
func (m *UI) archiveCurrentSession() tea.Cmd {
	if m.session == nil {
		return nil
	}
	archivedID := m.session.ID
	return func() tea.Msg {
		if err := m.com.Workspace.ArchiveSession(context.Background(), archivedID); err != nil {
			return activeSessionArchivedMsg{err: err}
		}
		next := ""
		sessions, err := m.com.Workspace.ListSessions(context.Background())
		if err == nil {
			for _, s := range sessions {
				if s.ID != archivedID {
					next = s.ID
					break
				}
			}
		}
		return activeSessionArchivedMsg{nextSessionID: next}
	}
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

// handleLeftSidebarClick handles a mouse click that may land in the left
// session navigator. It returns (cmd, true) when the click was inside the
// sidebar rect and consumed (so the caller stops further click routing), or
// (nil, false) when the click was elsewhere and should fall through to the
// chat/other handlers.
//
// A click on a session row focuses the sidebar, moves the cursor there, and
// activates it via the same path as enter/l (activateLeftSidebarSelection),
// preserving the cross-workspace switch handling. A click on a header or
// overflow row just moves the cursor (and focuses). A click on the fixed top
// matter focuses the sidebar but does nothing else.
func (m *UI) handleLeftSidebarClick(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	if !m.leftSidebarVisible {
		return nil, false
	}
	area := m.layout.leftSidebar
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return nil, false
	}
	if !image.Pt(msg.X, msg.Y).In(area) {
		return nil, false
	}
	// Focus the sidebar on any click within it, mirroring click-to-focus
	// for other panels. Any in-rect click is a fresh single action, so it
	// also clears an in-progress multi-select — including clicks on the
	// fixed top matter / header / overflow (which ClickToActivate returns
	// from early), so a header-area click doesn't leave a stale selection.
	m.focus = uiFocusLeftSidebar
	m.leftSidebar.ClearSelection()
	localY := msg.Y - area.Min.Y
	activatable, _ := m.leftSidebar.ClickToActivate(localY, area.Dy())
	if activatable {
		return m.activateLeftSidebarSelection(), true
	}
	// Header/overflow/fixed-matter click: consumed (cursor may have moved),
	// but nothing to open.
	return nil, true
}
