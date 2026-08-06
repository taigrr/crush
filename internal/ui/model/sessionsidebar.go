package model

import (
	"fmt"
	"strings"

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
	// sidebarRowOverflow is the "…N more" row shown when a workspace has
	// more sessions than its per-workspace display cap. Selecting it opens
	// the full session picker for that workspace.
	sidebarRowOverflow
)

// sidebarRow is one rendered/navigable line. Workspace header rows are not
// selectable; session and overflow rows are.
type sidebarRow struct {
	kind sidebarRowKind
	// workspace index into the overviews slice.
	wsIdx int
	// session index within that workspace (only for session rows).
	sessIdx int
	// remaining is the count of hidden sessions (only for overflow rows).
	remaining int
}

// minSessionsPerWorkspace is the floor on how many sessions each workspace
// shows in the navigator before an overflow row, even when vertical space
// is tight (a workspace with fewer sessions shows only what it has).
const minSessionsPerWorkspace = 5

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
	// bodyHeight is the last rendered body height (rows available for the
	// workspace/session list, excluding the title). It drives the
	// per-workspace session cap so one workspace cannot push others off
	// the screen.
	bodyHeight int

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
	caps := s.computeCaps()
	for wi, ws := range s.overviews {
		s.rows = append(s.rows, sidebarRow{kind: sidebarRowWorkspace, wsIdx: wi})
		shown := min(len(ws.Sessions), caps[wi])
		for si := range shown {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowSession, wsIdx: wi, sessIdx: si})
		}
		if remaining := len(ws.Sessions) - shown; remaining > 0 {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowOverflow, wsIdx: wi, remaining: remaining})
		}
	}
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	// Never leave the cursor resting on a workspace header if a selectable
	// row is reachable.
	s.snapCursorToSession(1)
}

// computeCaps returns the per-workspace session display cap. Sessions are
// pre-sorted (busy, unread, recent) so a cap keeps the most relevant ones.
//
// Rules:
//   - If every workspace's header + all its sessions fit in the body, show
//     everything (no cap, no overflow rows).
//   - Otherwise each workspace gets an even vertical share of the body, and
//     shows up to that many sessions with a floor of minSessionsPerWorkspace
//     so no single workspace crowds the others out. A workspace with fewer
//     sessions than its cap shows only what it has.
func (s *SessionsSidebar) computeCaps() []int {
	n := len(s.overviews)
	caps := make([]int, n)
	if n == 0 {
		return caps
	}

	h := s.bodyHeight
	if h <= 0 {
		// No layout yet: fall back to the floor so navigation is sane.
		for i := range caps {
			caps[i] = minSessionsPerWorkspace
		}
		return caps
	}

	// If everything fits, show all sessions with no overflow rows.
	total := 0
	for _, ws := range s.overviews {
		total += 1 + len(ws.Sessions) // header + sessions
	}
	if total <= h {
		for i, ws := range s.overviews {
			caps[i] = len(ws.Sessions)
		}
		return caps
	}

	// Even share: each workspace's block is h/n rows. Reserve one line for
	// the header and one for the overflow row, then floor at the minimum.
	perWorkspace := h / n
	cap := max(minSessionsPerWorkspace, perWorkspace-2)
	for i := range caps {
		caps[i] = cap
	}
	return caps
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

// selectableRow reports whether the row at i is a navigation stop (session
// or overflow row), i.e. not a workspace header.
func (s *SessionsSidebar) selectableRow(i int) bool {
	if i < 0 || i >= len(s.rows) {
		return false
	}
	return s.rows[i].kind != sidebarRowWorkspace
}

// snapCursorToSession advances the cursor in the given direction (+1/-1)
// off a workspace-header row onto the nearest selectable row, if any.
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

// MoveUp / MoveDown move the cursor to the previous/next selectable row,
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
		if s.selectableRow(i) {
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

// SelectedOverflowWorkspace reports whether the cursor is on an overflow
// ("…N more") row and, if so, returns that workspace's root. Selecting it
// should open the full session picker for that workspace rather than
// switching to a specific session.
func (s *SessionsSidebar) SelectedOverflowWorkspace() (root string, ok bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return "", false
	}
	r := s.rows[s.cursor]
	if r.kind != sidebarRowOverflow {
		return "", false
	}
	return s.overviews[r.wsIdx].Root, true
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

	// Match the right sidebar's aesthetic: a titled section header, then
	// workspace group headers rendered as section lines, with resource-
	// styled rows beneath.
	lines := []string{common.Section(t, "Sessions", width), ""}

	// Reserve the two header lines above.
	bodyHeight := max(1, height-2)

	// The per-workspace session cap depends on the available body height,
	// so rebuild the row projection whenever it changes (e.g. terminal
	// resize) before rendering.
	if bodyHeight != s.bodyHeight {
		prevID := s.selectedSessionID()
		s.bodyHeight = bodyHeight
		s.rebuildRows()
		s.restoreCursor(prevID)
	}

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
	return strings.Join(lines, "\n")
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
		case sidebarRowOverflow:
			out = append(out, s.renderOverflowRow(t, r.remaining, width, selected))
		}
	}
	return out
}

// renderOverflowRow renders the "…N more" row that opens the workspace's
// full session picker when selected. It aligns under the session titles
// (a 5-cell prefix: bar + space + active + marker + space).
func (s *SessionsSidebar) renderOverflowRow(t *styles.Styles, remaining, width int, selected bool) string {
	bar := " "
	if selected {
		bar = styles.BorderThick
	}
	label := fmt.Sprintf("… %d more", remaining)
	prefix := bar + "    " // bar + 4 spaces = 5-cell prefix
	avail := max(1, width-ansi.StringWidth(prefix))
	label = ansi.Truncate(label, avail, "…")
	if selected {
		return t.Dialog.SelectedItem.UnsetPadding().Width(width).Render(prefix + label)
	}
	return t.Resource.AdditionalText.Render(prefix + label)
}

func (s *SessionsSidebar) renderWorkspaceRow(t *styles.Styles, ws proto.WorkspaceOverview, width int) string {
	name := home.Short(ws.Root)
	// Leave room for the section's trailing " ──" so the header never
	// overflows the sidebar width and wraps.
	name = ansi.Truncate(name, max(1, width-4), "…")
	return common.Section(t, name, width)
}

func (s *SessionsSidebar) renderSessionRow(t *styles.Styles, sess proto.SessionOverview, width int, selected bool) string {
	// Status glyph: busy dot, unread dot, or blank.
	statusStyle := t.Resource.OfflineIcon
	switch {
	case sess.IsBusy:
		statusStyle = t.Resource.BusyIcon
	case sess.Unread:
		statusStyle = t.Resource.OnlineIcon
	}
	marker := " "
	if sess.IsBusy || sess.Unread {
		marker = statusStyle.String()
	}

	// Active-session arrow, selection bar. The bar (▌) marks the cursor
	// row like a focused list item without the padded dialog background.
	bar := " "
	if selected {
		bar = styles.BorderThick
	}
	active := " "
	if sess.ID == s.activeSessionID {
		active = styles.ArrowRightIcon
	}

	title := sess.Title
	if title == "" {
		title = "(untitled)"
	}

	// Swarm color square (one cell) rendered before the title. Empty
	// when the session has no assigned color (freshly created before
	// backfill runs, or the color name is not in the current
	// palette).
	square := common.SwarmSquare(sess.Color)
	squareCell := " "
	if square != "" {
		squareCell = square
	}

	// Build the fixed-width prefix: bar + space + active + marker + square + space.
	// Each glyph is a single cell.
	prefixRaw := bar + " " + active + marker + squareCell + " "
	prefixWidth := ansi.StringWidth(prefixRaw)
	avail := max(1, width-prefixWidth)
	title = ansi.Truncate(title, avail, "…")

	if selected {
		// Full-row highlight without extra padding (the dialog selected
		// style adds Padding(0,1) which would overflow the sidebar width).
		line := prefixRaw + title
		return t.Dialog.SelectedItem.UnsetPadding().Width(width).Render(line)
	}

	styledPrefix := t.Resource.AdditionalText.Render(bar+" "+active) + marker + squareCell + " "
	return styledPrefix + t.Resource.Name.Render(title)
}
