package model

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

// sidebarRowKind distinguishes the two kinds of rows in the flattened,
// navigable session-sidebar list.
type sidebarRowKind uint8

const (
	sidebarRowWorkspace sidebarRowKind = iota
	sidebarRowSession
)

// sidebarRow is one rendered/navigable line. Workspace header rows are not
// selectable targets to switch to; only session rows are.
type sidebarRow struct {
	kind sidebarRowKind
	// workspace index into the overviews slice.
	wsIdx int
	// session index within that workspace (only for session rows).
	sessIdx int
}

// SessionsSidebar is the left panel listing every known workspace and its
// sessions for cross-workspace navigation. It follows the imperative
// component pattern (see internal/ui/AGENTS.md): no Bubble Tea Update; the
// main model drives it via methods and reads Selected on enter.
type SessionsSidebar struct {
	com *common.Common

	overviews []proto.WorkspaceOverview
	// rows is the flattened navigable projection of overviews, rebuilt
	// whenever overviews change.
	rows []sidebarRow
	// cursor indexes into rows. It is kept on a session row where possible.
	cursor int
	// scroll is the index of the first visible row.
	scroll int

	// activeSessionID is the session currently open in the main pane, shown
	// with a marker.
	activeSessionID string
}

// NewSessionsSidebar creates an empty sidebar bound to shared context.
func NewSessionsSidebar(com *common.Common) *SessionsSidebar {
	return &SessionsSidebar{com: com}
}

// AttachedSessions returns the sessions of the currently attached
// workspace (the one hosted by this client), for the landing screen.
func (s *SessionsSidebar) AttachedSessions() []proto.SessionOverview {
	for _, ws := range s.overviews {
		if ws.Attached {
			return ws.Sessions
		}
	}
	return nil
}

// WorkspaceCount returns the number of known workspaces.
func (s *SessionsSidebar) WorkspaceCount() int {
	return len(s.overviews)
}

// SetOverviews replaces the listed data and rebuilds the navigable rows,
// keeping the cursor on the same session when possible.
func (s *SessionsSidebar) SetOverviews(overviews []proto.WorkspaceOverview) {
	prevID := s.selectedSessionID()
	s.overviews = overviews
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// SetActiveSession records which session is open so it can be marked.
func (s *SessionsSidebar) SetActiveSession(id string) {
	s.activeSessionID = id
}

func (s *SessionsSidebar) rebuildRows() {
	s.rows = s.rows[:0]
	for wi, ws := range s.overviews {
		s.rows = append(s.rows, sidebarRow{kind: sidebarRowWorkspace, wsIdx: wi})
		for si := range ws.Sessions {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowSession, wsIdx: wi, sessIdx: si})
		}
	}
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	// Never leave the cursor resting on a workspace header if a session
	// row is reachable.
	s.snapCursorToSession(1)
}

// restoreCursor moves the cursor back onto the session with id if it still
// exists, otherwise leaves it at the first session.
func (s *SessionsSidebar) restoreCursor(id string) {
	if id == "" {
		s.snapCursorToSession(1)
		return
	}
	for i, r := range s.rows {
		if r.kind != sidebarRowSession {
			continue
		}
		if s.overviews[r.wsIdx].Sessions[r.sessIdx].ID == id {
			s.cursor = i
			s.ensureVisible()
			return
		}
	}
	s.snapCursorToSession(1)
}

// snapCursorToSession advances the cursor in the given direction (+1/-1)
// off a workspace-header row onto the nearest session row, if any.
func (s *SessionsSidebar) snapCursorToSession(dir int) {
	if len(s.rows) == 0 {
		s.cursor = 0
		return
	}
	for s.cursor >= 0 && s.cursor < len(s.rows) && s.rows[s.cursor].kind == sidebarRowWorkspace {
		next := s.cursor + dir
		if next < 0 || next >= len(s.rows) {
			// No session in that direction; try the other way once.
			dir = -dir
			next = s.cursor + dir
			if next < 0 || next >= len(s.rows) {
				return
			}
		}
		s.cursor = next
	}
	s.ensureVisible()
}

// MoveUp / MoveDown move the cursor to the previous/next session row,
// skipping workspace headers.
func (s *SessionsSidebar) MoveUp() {
	s.moveBy(-1)
}

func (s *SessionsSidebar) MoveDown() {
	s.moveBy(1)
}

func (s *SessionsSidebar) moveBy(dir int) {
	if len(s.rows) == 0 {
		return
	}
	i := s.cursor
	for {
		i += dir
		if i < 0 || i >= len(s.rows) {
			return // no move past the ends
		}
		if s.rows[i].kind == sidebarRowSession {
			s.cursor = i
			s.ensureVisible()
			return
		}
	}
}

// Selected returns the workspace root and session ID under the cursor, and
// whether the cursor is on a selectable session row.
func (s *SessionsSidebar) Selected() (root, sessionID string, ok bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return "", "", false
	}
	r := s.rows[s.cursor]
	if r.kind != sidebarRowSession {
		return "", "", false
	}
	ws := s.overviews[r.wsIdx]
	return ws.Root, ws.Sessions[r.sessIdx].ID, true
}

// SelectedWorkspaceAttached reports whether the workspace under the cursor
// is the one currently attached (so switching is session-only, no attach).
func (s *SessionsSidebar) SelectedWorkspaceAttached() bool {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return false
	}
	return s.overviews[s.rows[s.cursor].wsIdx].Attached
}

func (s *SessionsSidebar) selectedSessionID() string {
	_, id, ok := s.Selected()
	if !ok {
		return ""
	}
	return id
}

// visibleRows is how many rows fit in the given height.
func (s *SessionsSidebar) ensureVisible() {
	// scroll is corrected against the viewport height at render time; here
	// we just keep it sane relative to the cursor.
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
}

// Render draws the sidebar to a string of the given width/height.
func (s *SessionsSidebar) Render(width, height int, focused bool) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	t := s.com.Styles

	title := t.Sidebar.SessionTitle.Render("Sessions")
	lines := []string{title, ""}

	// Reserve two lines for the title + blank.
	bodyHeight := max(1, height-2)

	rendered := s.renderRows(width)
	// Clamp scroll so the cursor stays visible within bodyHeight.
	if s.cursor >= s.scroll+bodyHeight {
		s.scroll = s.cursor - bodyHeight + 1
	}
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
	end := min(len(rendered), s.scroll+bodyHeight)
	for i := s.scroll; i < end; i++ {
		lines = append(lines, rendered[i])
	}
	if len(rendered) == 0 {
		lines = append(lines, t.Resource.AdditionalText.Render("No sessions yet"))
	}

	_ = focused
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// renderRows renders each navigable row to a styled line at the given width.
func (s *SessionsSidebar) renderRows(width int) []string {
	t := s.com.Styles
	out := make([]string, 0, len(s.rows))
	for i, r := range s.rows {
		selected := i == s.cursor
		switch r.kind {
		case sidebarRowWorkspace:
			out = append(out, s.renderWorkspaceRow(t, s.overviews[r.wsIdx], width))
		case sidebarRowSession:
			ws := s.overviews[r.wsIdx]
			out = append(out, s.renderSessionRow(t, ws.Sessions[r.sessIdx], width, selected))
		}
	}
	return out
}

func (s *SessionsSidebar) renderWorkspaceRow(t *styles.Styles, ws proto.WorkspaceOverview, width int) string {
	name := home.Short(ws.Root)
	label := styles.FolderIcon + " " + name
	if !ws.Attached {
		// Dim unattached workspaces slightly by suffixing nothing special;
		// heading style already differentiates it as a group header.
	}
	return t.Resource.Heading.Render(ansi.Truncate(label, width, "…"))
}

func (s *SessionsSidebar) renderSessionRow(t *styles.Styles, sess proto.SessionOverview, width int, selected bool) string {
	// Status glyph: busy spinner-dot, unread dot, or space.
	var marker string
	switch {
	case sess.IsBusy:
		marker = t.Resource.BusyIcon.String()
	case sess.Unread:
		marker = t.Resource.OnlineIcon.String()
	default:
		marker = " "
	}

	active := " "
	if sess.ID == s.activeSessionID {
		active = styles.ArrowRightIcon
	}

	title := sess.Title
	if title == "" {
		title = "(untitled)"
	}

	prefix := active + marker + " "
	avail := max(1, width-lipgloss.Width(prefix))
	title = ansi.Truncate(title, avail, "…")

	line := prefix + title
	if selected {
		return t.Dialog.SelectedItem.Width(width).Render(ansi.Strip(line))
	}
	return t.Dialog.NormalItem.Render(line)
}
