package model

import (
	"context"
	"fmt"
	"image"

	"charm.land/bubbles/v2/key"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/util"
)

const sessionsSidebarWidthKey = "options.tui.sessions_sidebar_width"

// sessionsSidebarPinnedKey is the config path the pin toggle persists to.
const sessionsSidebarPinnedKey = "options.tui.sessions_sidebar_pinned"

// Left session navigator width bounds. The default matches the right info
// sidebar for visual parity.
const (
	defaultLeftSidebarWidth = 30
	minLeftSidebarWidth     = 20
	maxLeftSidebarWidth     = 80
	leftSidebarResizeStep   = 2
)

// clampLeftSidebarWidth brings w into the supported range, mapping zero (unset
// in config) onto the default.
func clampLeftSidebarWidth(w int) int {
	if w == 0 {
		return defaultLeftSidebarWidth
	}
	return min(maxLeftSidebarWidth, max(minLeftSidebarWidth, w))
}

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
// When the navigator is pinned and already open, ctrl+s moves focus between
// the navigator and the editor instead of collapsing it.
func (m *UI) toggleLeftSidebar() tea.Cmd {
	if m.leftSidebarVisible && m.leftSidebarPinned {
		if m.focus == uiFocusLeftSidebar {
			m.setFocusAfterSidebarClose()
			return m.cancelPreview()
		}
		m.focus = uiFocusLeftSidebar
		m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
		if m.session != nil {
			m.leftSidebar.SetActiveSession(m.session.ID)
		}
		return m.loadWorkspaceOverviews()
	}
	if m.leftSidebarVisible {
		m.leftSidebarVisible = false
		m.leftSidebar.ExitSearch()
		m.leftSidebar.ClearSelection()
		if m.focus == uiFocusLeftSidebar {
			m.setFocusAfterSidebarClose()
		}
		m.updateLayoutAndSize()
		// Discard any live preview and restore the committed session.
		return m.cancelPreview()
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

// toggleLeftSidebarPin flips the navigator's pinned state and persists it
// to the global config. Pinning a hidden navigator opens it without
// stealing focus from the editor; unpinning leaves it open until the next
// session switch or ctrl+s collapses it.
func (m *UI) toggleLeftSidebarPin() tea.Cmd {
	m.leftSidebarPinned = !m.leftSidebarPinned
	var cmds []tea.Cmd
	if m.leftSidebarPinned && !m.leftSidebarVisible {
		m.leftSidebarVisible = true
		m.leftSidebar.SetCurrentRoot(m.com.Workspace.BaseDir())
		if m.session != nil {
			m.leftSidebar.SetActiveSession(m.session.ID)
		}
		m.updateLayoutAndSize()
		cmds = append(cmds, m.loadWorkspaceOverviews())
	}
	status := "unpinned"
	if m.leftSidebarPinned {
		status = "pinned"
	}
	cmds = append(cmds, util.ReportInfo("Sessions sidebar "+status))
	pinned := m.leftSidebarPinned
	cmds = append(cmds, func() tea.Msg {
		if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, sessionsSidebarPinnedKey, pinned); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		return nil
	})
	return tea.Batch(cmds...)
}

// collapseLeftSidebarAfterActivate is what a session activation does to
// the navigator: hide it, unless it is pinned, in which case it stays open
// and only focus returns to the editor.
func (m *UI) collapseLeftSidebarAfterActivate() {
	if !m.leftSidebarPinned {
		m.leftSidebarVisible = false
	}
	m.setFocusAfterSidebarClose()
	m.updateLayoutAndSize()
}

// setFocusAfterSidebarClose restores a sensible focus when the sidebar
// loses focus (editor in chat/landing, else none). It also drops any
// pending multi-selection so a selection never outlives a focus session
// (preventing an accidental bulk archive after close/reopen).
func (m *UI) setFocusAfterSidebarClose() {
	m.leftSidebar.ExitSearch()
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
	// While the "/" text filter is active, keys are routed differently:
	// printable characters type into the filter (so j/k/g/etc. are literal
	// query text), navigation over the filtered set uses arrow keys, and
	// esc exits search.
	if m.leftSidebar.Searching() {
		return m.handleLeftSidebarSearchKey(msg)
	}
	// Vim-style navigation mirrors the right info sidebar: j/k move,
	// g/Home jumps to the top, G/End to the bottom.
	switch {
	case key.Matches(msg, m.keyMap.Chat.Up):
		m.leftSidebar.MoveUp()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.Chat.Down):
		m.leftSidebar.MoveDown()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.Chat.Home):
		m.leftSidebar.MoveTop()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.Chat.End):
		m.leftSidebar.MoveBottom()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.PrevSection):
		m.leftSidebar.MovePrevSection()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.NextSection):
		m.leftSidebar.MoveNextSection()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.VisualSelect):
		m.leftSidebar.ToggleVisualMode()
		return nil, true
	case key.Matches(msg, m.keyMap.SessionSidebar.ToggleSelect):
		m.leftSidebar.ToggleSelected()
		return nil, true
	case key.Matches(msg, m.keyMap.SessionSidebar.ArchiveSelect):
		return m.archiveSelectedSessions(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.MarkRead):
		return m.markSelectedSessionsRead(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.Favorite):
		return m.toggleFavoriteUnderCursor(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.Inbox):
		// Toggle inbox/sessions view. The cursor stays on the same session
		// where possible, so re-run the preview scheduler for the (possibly
		// unchanged) selection after reprojection.
		m.leftSidebar.ToggleInbox()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.Search):
		// Enter the text filter. Only reachable while the sidebar is
		// focused (this handler runs solely in that case), so it never
		// collides with the editor's "/" add-file binding.
		m.leftSidebar.EnterSearch()
		return m.scheduleSidebarPreview(), true
	case key.Matches(msg, m.keyMap.SessionSidebar.Pin):
		return m.toggleLeftSidebarPin(), true
	}
	switch msg.String() {
	case "enter", "l":
		return m.activateLeftSidebarSelection(), true
	case "[", "-", "shift+left":
		return m.resizeLeftSidebar(-leftSidebarResizeStep), true
	case "]", "+", "=", "shift+right":
		return m.resizeLeftSidebar(leftSidebarResizeStep), true
	case "esc", "h":
		// Esc first exits visual selection / clears a pending multi-
		// selection; only when there is nothing selected does it close
		// the sidebar. "h" always closes. A pinned sidebar is never
		// closed here: both keys just hand focus back to the editor.
		if msg.String() == "esc" && (m.leftSidebar.VisualMode() || m.leftSidebar.SelectionCount() > 0) {
			m.leftSidebar.ClearSelection()
			return nil, true
		}
		if !m.leftSidebarPinned {
			m.leftSidebarVisible = false
		}
		m.setFocusAfterSidebarClose()
		m.updateLayoutAndSize()
		// Closing without committing discards any live preview and
		// restores the session the user actually had open.
		cancel := m.cancelPreview()
		return cancel, true
	}
	return nil, false
}

// handleLeftSidebarSearchKey handles keys while the "/" filter is active.
// Navigation over the filtered set uses arrow keys (and their ctrl aliases)
// so that letter keys remain available as filter text; esc exits and
// restores the full list (cancelling any preview); enter activates the
// selected filtered session; every other key is fed to the filter input,
// which re-filters live and reschedules the preview for the new top match.
// A second "/" is a literal query character (handled by the default case),
// not a re-trigger.
func (m *UI) handleLeftSidebarSearchKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.leftSidebar.ExitSearch()
		// Restoring the full list discards the filtered preview target and
		// returns to the committed session.
		return m.cancelPreview(), true
	case "enter":
		return m.activateLeftSidebarSelection(), true
	case "up", "ctrl+k", "ctrl+p":
		m.leftSidebar.MoveUp()
		return m.scheduleSidebarPreview(), true
	case "down", "ctrl+j", "ctrl+n":
		m.leftSidebar.MoveDown()
		return m.scheduleSidebarPreview(), true
	case "home":
		m.leftSidebar.MoveTop()
		return m.scheduleSidebarPreview(), true
	case "end":
		m.leftSidebar.MoveBottom()
		return m.scheduleSidebarPreview(), true
	}
	// Any other key is offered to the filter input. Three outcomes:
	//   - it changed the query -> re-filter, preview, consume;
	//   - it edited the input without changing the value (cursor motion
	//     like left/right, backspace on an empty query) -> consume so it
	//     does NOT fall through and double-fire a global binding
	//     (e.g. Chat.PillLeft/Right on the arrow keys);
	//   - the input ignores it (global shortcuts like ctrl+s, Chat.Cancel)
	//     -> report UNCONSUMED so it falls through to the global handlers.
	if m.leftSidebar.HandleSearchKey(msg) {
		return m.scheduleSidebarPreview(), true
	}
	if isTextEditingKey(msg) {
		return nil, true
	}
	return nil, false
}

// isTextEditingKey reports whether a key is one the filter textinput edits
// (a printable character or a cursor/erase motion), as opposed to a global
// shortcut the input ignores. Such keys are consumed while searching even
// when they don't change the query value, so they never fall through to the
// global handlers and cause a side effect (e.g. the left/right arrows also
// switching the expanded pill section).
func isTextEditingKey(msg tea.KeyPressMsg) bool {
	// A printable character (with no control modifier) is query text.
	if msg.Text != "" {
		return true
	}
	switch msg.String() {
	case "backspace", "delete", "left", "right",
		"ctrl+a", "ctrl+e", "ctrl+b", "ctrl+f", "ctrl+h",
		"ctrl+u", "ctrl+w", "ctrl+d", "ctrl+v",
		"alt+backspace", "alt+delete", "alt+left", "alt+right",
		"alt+b", "alt+f", "alt+d",
		"ctrl+left", "ctrl+right":
		return true
	}
	return false
}

// scheduleSidebarPreview debounces a live preview of the session now under
// the sidebar cursor, including foreign-workspace sessions (see
// schedulePreview); a cursor on a header/overflow row (no session) cancels
// any active preview back to the committed view. Preview is suppressed while
// a multi-select/visual selection is in progress so the chat view doesn't
// flicker while the user is building a selection.
func (m *UI) scheduleSidebarPreview() tea.Cmd {
	if m.leftSidebar.VisualMode() || m.leftSidebar.SelectionCount() > 0 {
		return nil
	}
	root, id, ok := m.leftSidebar.Selected()
	if !ok {
		return m.cancelPreview()
	}
	return m.schedulePreview(id, root)
}

// archiveSelectedSessions archives the selected sessions, each routed to
// ITS OWN workspace (attached or detached). The active session (never
// archive the one being viewed) and busy sessions (never archive mid-run)
// are skipped. The selection is NOT cleared here: it is trimmed to the
// failures only after the archive command reports, so failures stay
// selected for a retry.
func (m *UI) archiveSelectedSessions() tea.Cmd {
	targets, skippedActive, skippedBusy := m.leftSidebar.ArchivableSelection()
	if len(targets) == 0 {
		switch {
		case skippedActive > 0 && skippedBusy > 0:
			return util.ReportWarn("Selected session(s) are active or busy; nothing to archive")
		case skippedBusy > 0:
			return util.ReportWarn("Selected session(s) are busy; nothing to archive")
		case skippedActive > 0:
			return util.ReportWarn("Only the active session was selected; nothing to archive")
		case m.leftSidebar.SelectionCount() > 0:
			return util.ReportWarn("Selected session(s) are no longer available; nothing to archive")
		default:
			return util.ReportInfo("No sessions selected")
		}
	}
	ids := targetIDs(targets)
	survivor := m.leftSidebar.SurvivingNeighbor(ids)
	return m.archiveSessionsCmd(targets, survivor, skippedActive+skippedBusy)
}

// targetIDs projects the session ids out of a target slice, preserving
// order.
func targetIDs(targets []SessionTarget) []string {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	return ids
}

// sessionsArchivedMsg reports the outcome of a bulk archive: the refreshed
// overviews (nil if the refresh itself failed), which IDs failed (so they
// stay selected), how many succeeded, how many selected sessions were
// skipped (active or busy, never archived), and the neighbor to focus.
type sessionsArchivedMsg struct {
	overviews  []proto.WorkspaceOverview
	failed     []string
	succeeded  int
	skipped    int
	survivorID string
}

// archiveSessionsCmd archives the given targets off the Update goroutine,
// routing each session to its own workspace id/root. It attempts EVERY
// target (deterministic order, set by the caller) and collects individual
// failures rather than aborting on the first — a locked or unreadable
// detached workspace database fails only that session — so the outcome is
// stable. It refreshes the overviews afterwards; if that refresh fails it
// still emits sessionsArchivedMsg (with nil overviews) so successfully
// archived IDs are dropped from the selection and only failures remain.
func (m *UI) archiveSessionsCmd(targets []SessionTarget, survivorID string, skipped int) tea.Cmd {
	return func() tea.Msg {
		succeeded := 0
		var failed []string
		for _, t := range targets {
			if err := m.com.Workspace.ArchiveSessionInWorkspace(context.Background(), t.WorkspaceID, t.Root, t.ID); err != nil {
				failed = append(failed, t.ID)
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

// markSelectedSessionsRead marks the selected sessions as read, each routed
// to ITS OWN workspace (attached or detached). There is no destructive
// concern: the selection is cleared and visual mode exited unconditionally
// after the command reports, and the cursor stays put.
func (m *UI) markSelectedSessionsRead() tea.Cmd {
	targets := m.leftSidebar.MarkReadSelection()
	if len(targets) == 0 {
		return util.ReportInfo("No sessions selected")
	}
	return m.markSessionsReadCmd(targets)
}

// sessionsMarkedReadMsg reports the outcome of a bulk mark-as-read: the
// refreshed overviews (nil if the refresh itself failed) so the derived
// unread/green-dot state updates, which IDs failed, and how many succeeded.
type sessionsMarkedReadMsg struct {
	overviews []proto.WorkspaceOverview
	failed    []string
	succeeded int
}

// markSessionsReadCmd marks the given targets read off the Update
// goroutine, routing each session to its own workspace id/root. It attempts
// EVERY target (deterministic order) and collects individual failures
// rather than aborting on the first, then refreshes the overviews so the
// unread state updates. It always emits sessionsMarkedReadMsg (with nil
// overviews if the refresh failed).
func (m *UI) markSessionsReadCmd(targets []SessionTarget) tea.Cmd {
	return func() tea.Msg {
		succeeded := 0
		var failed []string
		for _, t := range targets {
			if err := m.com.Workspace.MarkSessionSeenInWorkspace(context.Background(), t.WorkspaceID, t.Root, t.ID); err != nil {
				failed = append(failed, t.ID)
				continue
			}
			succeeded++
		}
		overviews, err := m.com.Workspace.ListWorkspaceOverviews(context.Background())
		if err != nil {
			return sessionsMarkedReadMsg{
				failed:    failed,
				succeeded: succeeded,
			}
		}
		return sessionsMarkedReadMsg{
			overviews: overviews,
			failed:    failed,
			succeeded: succeeded,
		}
	}
}

// favoriteToggledMsg reports the outcome of toggling a session's favorite
// flag: the refreshed overviews (nil if the refresh failed) so the sidebar
// reprojects (moving the session in/out of the Favorite section), the now
// favorite state, and any error.
type favoriteToggledMsg struct {
	overviews []proto.WorkspaceOverview
	favorite  bool
	err       error
}

// toggleFavoriteUnderCursor flips the favorite flag of the session under
// the sidebar cursor, routing the write to the session's OWN workspace
// (attached or detached), then refreshes the overviews so the inbox
// reprojects. It is non-destructive and works in both grouped and inbox
// views.
func (m *UI) toggleFavoriteUnderCursor() tea.Cmd {
	target, favorite, ok := m.leftSidebar.FavoriteTargetUnderCursor()
	if !ok {
		return nil
	}
	next := !favorite
	return func() tea.Msg {
		if err := m.com.Workspace.SetSessionFavoriteInWorkspace(context.Background(), target.WorkspaceID, target.Root, target.ID, next); err != nil {
			return favoriteToggledMsg{favorite: next, err: err}
		}
		overviews, err := m.com.Workspace.ListWorkspaceOverviews(context.Background())
		if err != nil {
			return favoriteToggledMsg{favorite: next}
		}
		return favoriteToggledMsg{overviews: overviews, favorite: next}
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
	// Overflow row ("…N more" / "show less"): expand or collapse that
	// workspace in place. The sidebar stays open and focused so the user
	// can keep navigating the now-visible sessions.
	if m.leftSidebar.ToggleOverflowUnderCursor() {
		return nil
	}

	root, sessionID, ok := m.leftSidebar.Selected()
	if !ok {
		return nil
	}

	// Collapse the sidebar (unless pinned) and return focus to the editor
	// after switching.
	m.collapseLeftSidebarAfterActivate()

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

// switchWorkspaceAndLoad re-targets the client at the workspace rooted at
// root, then loads the session. The attach happens off the Update goroutine.
func (m *UI) switchWorkspaceAndLoad(root, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SwitchWorkspace(context.Background(), root); err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		return workspaceSwitchedMsg{sessionID: sessionID, yolo: m.com.Workspace.PermissionSkipRequests()}
	}
}

// workspaceSwitchedMsg is emitted after a successful cross-workspace switch
// so the main loop can load the session on the Update goroutine. yolo is the newly-attached workspace's own permission skip-requests
// flag, fetched here (off the Update goroutine, alongside the switch
// itself) so the Update handler can refresh the cached indicator without
// an extra blocking round trip of its own.
type workspaceSwitchedMsg struct {
	sessionID string
	yolo      bool
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

// handleLeftSidebarWheel scrolls the session navigator when a mouse-wheel
// event lands inside its rect and reports whether it consumed the event.
// It does not require the sidebar to be focused.
func (m *UI) handleLeftSidebarWheel(msg tea.MouseWheelMsg) bool {
	if !m.leftSidebarVisible {
		return false
	}
	area := m.layout.leftSidebar
	if area.Dx() <= 0 || area.Dy() <= 0 || !image.Pt(msg.X, msg.Y).In(area) {
		return false
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.leftSidebar.ScrollBy(-MouseScrollThreshold)
	case tea.MouseWheelDown:
		m.leftSidebar.ScrollBy(MouseScrollThreshold)
	default:
		return false
	}
	return true
}

// resizeLeftSidebar widens (delta > 0) or narrows (delta < 0) the session
// navigator and persists the new width.
func (m *UI) resizeLeftSidebar(delta int) tea.Cmd {
	// Never let the navigator squeeze the main pane out of existence. Clamp the
	// current width to the available room *before* applying the delta so a
	// widen keystroke can never shrink the sidebar (and persist that shrink)
	// just because the terminal is currently narrower than the stored width.
	cur := m.leftSidebarWidth
	room := m.width - 12
	if room >= minLeftSidebarWidth {
		cur = min(cur, room)
	}
	want := clampLeftSidebarWidth(cur + delta)
	if delta > 0 && room >= minLeftSidebarWidth {
		want = min(want, room)
	}
	if want == m.leftSidebarWidth {
		return nil
	}
	m.leftSidebarWidth = want
	m.updateLayoutAndSize()

	return func() tea.Msg {
		err := m.com.Workspace.SetConfigField(
			config.ScopeGlobal, sessionsSidebarWidthKey, want,
		)
		if err != nil {
			return util.InfoMsg{Type: util.InfoTypeError, Msg: err.Error()}
		}
		// Every project and workspace config outranks the global data config
		// we just wrote, so a higher-precedence file would silently revert
		// this resize on restart.
		cfg := m.com.Config()
		if cfg == nil || cfg.Options == nil || cfg.Options.TUI == nil {
			return nil
		}
		if got := cfg.Options.TUI.SessionsSidebarWidth; got != want && got != 0 {
			return util.InfoMsg{
				Type: util.InfoTypeWarn,
				Msg: fmt.Sprintf(
					"Sidebar width not saved: a project config pins it to %d. Edit %s there.",
					got, sessionsSidebarWidthKey,
				),
			}
		}
		return nil
	}
}
