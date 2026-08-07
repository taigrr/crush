package model

import (
	"fmt"
	"sort"
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

	// currentRoot is the resolved root of the workspace this client is
	// pointed at. Bulk archive is scoped to it because the client archive
	// API can only reach the currently-attached workspace; selected
	// sessions from other workspaces are skipped (and reported).
	currentRoot string

	// selected is the set of session IDs currently multi-selected for a
	// bulk action (keyed by session ID so it survives re-sorts and
	// row reprojection).
	selected map[string]bool
	// visualMode is true while vim-style visual selection is active: cursor
	// movement sweeps a contiguous range into selected.
	visualMode bool
	// anchorID is the session ID where visual mode began. It is stored by
	// ID (not row index) so an in-progress sweep survives row reprojection
	// from a background refresh, resize, or re-sort. The swept range runs
	// between the anchor's current row and the cursor.
	anchorID string
}

// NewSessionsSidebar creates an empty sidebar bound to shared context.
func NewSessionsSidebar(com *common.Common) *SessionsSidebar {
	return &SessionsSidebar{com: com, selected: map[string]bool{}}
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
	s.sortSessions()
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// SetActiveSession records which session is open so it can be marked.
func (s *SessionsSidebar) SetActiveSession(id string) {
	if s.activeSessionID == id {
		return
	}
	s.activeSessionID = id
	// Re-sort so the newly active session pins to the top of its
	// workspace, then reproject rows keeping the cursor where possible.
	prevID := s.selectedSessionID()
	s.sortSessions()
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// sortSessions orders each workspace's sessions. The primary key is the
// same one the popup session picker uses — UpdatedAt (last-message time,
// most recent first) — but with two deliberate additions the picker does
// not have: the active session is pinned to the very top of its workspace,
// and the server's busy → unread priority tiers are preserved. Keeping
// those tiers matters because computeCaps relies on the pre-sort to keep
// the most relevant sessions above the per-workspace overflow cap, so a
// busy/unread session must not sink below an old-but-recently-updated one.
//
// Sort key: active first, then busy, then unread, then UpdatedAt desc.
func (s *SessionsSidebar) sortSessions() {
	for wi := range s.overviews {
		sess := s.overviews[wi].Sessions
		sort.SliceStable(sess, func(i, j int) bool {
			a, b := sess[i], sess[j]
			if s.activeSessionID != "" {
				if a.ID == s.activeSessionID {
					return true
				}
				if b.ID == s.activeSessionID {
					return false
				}
			}
			if a.IsBusy != b.IsBusy {
				return a.IsBusy
			}
			if a.Unread != b.Unread {
				return a.Unread
			}
			return a.UpdatedAt > b.UpdatedAt
		})
	}
}

func (s *SessionsSidebar) rebuildRows() {
	s.rows = s.rows[:0]
	caps := s.computeCaps()
	for wi, ws := range s.overviews {
		// Skip workspaces with no visible sessions entirely: don't emit a
		// header (or overflow) row for a workspace that contributes zero
		// navigable session rows (e.g. all its sessions are archived). An
		// empty header would take space and be non-interactive.
		//
		// INVARIANT: ws.Sessions holds only VISIBLE (non-archived) sessions
		// — the server-side overview (ListWorkspaceOverviews) omits archived
		// ones, and proto.SessionOverview has no Archived field. So
		// len(ws.Sessions)==0 means "no visible sessions". If a future change
		// ever includes archived sessions here, a fully-archived workspace
		// would have len>0 and its header would silently reappear; keep this
		// check keyed to the visible set.
		if len(ws.Sessions) == 0 {
			continue
		}
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

	// If everything fits, show all sessions with no overflow rows. Empty
	// workspaces contribute nothing (their header is suppressed in
	// rebuildRows), so they don't count toward the height budget. This
	// relies on the same invariant as rebuildRows: ws.Sessions is the
	// visible (non-archived) set, so len==0 means "no visible sessions".
	total := 0
	nonEmpty := 0
	for _, ws := range s.overviews {
		if len(ws.Sessions) == 0 {
			continue
		}
		nonEmpty++
		total += 1 + len(ws.Sessions) // header + sessions
	}
	if nonEmpty == 0 {
		return caps
	}
	if total <= h {
		for i, ws := range s.overviews {
			caps[i] = len(ws.Sessions)
		}
		return caps
	}

	// Even share: each non-empty workspace's block is h/nonEmpty rows.
	// Reserve one line for the header and one for the overflow row, then
	// floor at the minimum.
	perWorkspace := h / nonEmpty
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

// MoveTop / MoveBottom jump the cursor to the first/last selectable row,
// skipping workspace headers.
func (s *SessionsSidebar) MoveTop() {
	if len(s.rows) == 0 {
		return
	}
	for i := range s.rows {
		if s.selectableRow(i) {
			s.cursor = i
			s.ensureVisible()
			s.extendVisual()
			return
		}
	}
}

func (s *SessionsSidebar) MoveBottom() {
	if len(s.rows) == 0 {
		return
	}
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.selectableRow(i) {
			s.cursor = i
			s.ensureVisible()
			s.extendVisual()
			return
		}
	}
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
			s.extendVisual()
			return
		}
	}
}

// enterVisualMode toggles vim-style visual selection. Entering anchors the
// sweep at the current cursor and selects the session under it; toggling it
// off (or ClearSelection) exits and clears the whole selection.
func (s *SessionsSidebar) ToggleVisualMode() {
	if s.visualMode {
		s.ClearSelection()
		return
	}
	s.visualMode = true
	s.anchorID = ""
	if s.selected == nil {
		s.selected = map[string]bool{}
	}
	if id, ok := s.sessionIDAt(s.cursor); ok {
		s.anchorID = id
		s.selected[id] = true
	}
}

// ClearSelection exits visual mode and drops all selected sessions.
func (s *SessionsSidebar) ClearSelection() {
	s.visualMode = false
	s.anchorID = ""
	s.selected = map[string]bool{}
}

// ToggleSelected toggles the session under the cursor in/out of the
// selection set. It works independently of visual mode, allowing a
// non-contiguous selection.
func (s *SessionsSidebar) ToggleSelected() {
	id, ok := s.sessionIDAt(s.cursor)
	if !ok {
		return
	}
	if s.selected == nil {
		s.selected = map[string]bool{}
	}
	if s.selected[id] {
		delete(s.selected, id)
	} else {
		s.selected[id] = true
	}
}

// extendVisual, while in visual mode, adds every session row between the
// anchor and the cursor (inclusive) to the selection set. The anchor is
// resolved from its session ID on demand so a row reprojection (background
// refresh, resize, re-sort) between movements never corrupts the swept
// range. Visual sweeping is additive: moving back over a range does not
// deselect, matching the "space toggles discrete members" model.
func (s *SessionsSidebar) extendVisual() {
	if !s.visualMode {
		return
	}
	if s.selected == nil {
		s.selected = map[string]bool{}
	}
	anchorRow := s.rowForSessionID(s.anchorID)
	if anchorRow < 0 {
		// Anchor session vanished (e.g. archived elsewhere); re-anchor at
		// the cursor so the sweep stays coherent.
		if id, ok := s.sessionIDAt(s.cursor); ok {
			s.anchorID = id
		}
		anchorRow = s.cursor
	}
	lo, hi := anchorRow, s.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	for i := lo; i <= hi; i++ {
		if id, ok := s.sessionIDAt(i); ok {
			s.selected[id] = true
		}
	}
}

// rowForSessionID returns the row index whose session has id, or -1.
func (s *SessionsSidebar) rowForSessionID(id string) int {
	if id == "" {
		return -1
	}
	for i, r := range s.rows {
		if r.kind != sidebarRowSession {
			continue
		}
		if s.overviews[r.wsIdx].Sessions[r.sessIdx].ID == id {
			return i
		}
	}
	return -1
}

// sessionIDAt returns the session ID for the row at i, if it is a session
// row.
func (s *SessionsSidebar) sessionIDAt(i int) (string, bool) {
	if i < 0 || i >= len(s.rows) {
		return "", false
	}
	r := s.rows[i]
	if r.kind != sidebarRowSession {
		return "", false
	}
	return s.overviews[r.wsIdx].Sessions[r.sessIdx].ID, true
}

// VisualMode reports whether visual selection mode is active.
func (s *SessionsSidebar) VisualMode() bool { return s.visualMode }

// SelectionCount returns how many sessions are currently selected.
func (s *SessionsSidebar) SelectionCount() int { return len(s.selected) }

// SelectedSessionIDs returns the selected session IDs (unordered).
func (s *SessionsSidebar) SelectedSessionIDs() []string {
	ids := make([]string, 0, len(s.selected))
	for id := range s.selected {
		ids = append(ids, id)
	}
	return ids
}

// ArchivableSelection returns the selected session IDs eligible for bulk
// archive, in deterministic (sorted) order, together with the number of
// selected sessions that were skipped. A session is skipped when it is the
// active session (never archive the one the user is viewing) or when it does
// not provably belong to the current workspace (the client archive API only
// reaches the attached workspace; cross-workspace archive would require
// switching workspaces, which the bulk path deliberately avoids).
//
// The workspace filter fails CLOSED: a session is archivable only when its
// workspace root is KNOWN (present in the current overviews) AND equal to
// currentRoot. Unknown ids (e.g. dropped by a background refresh) and every
// id when currentRoot is unset are skipped, so a stale/foreign id is never
// passed to ArchiveSession (which would otherwise report a false success —
// db.ArchiveSession is an unconditional UPDATE that returns nil for a
// nonexistent id).
//
// INVARIANT: currentRoot (set from workspace.BaseDir) and the overview Root
// values must use identical path normalization; otherwise the current
// workspace's own sessions would fail the root == currentRoot check and be
// silently skipped. This is the same equality assumption isCurrentWorkspace
// relies on.
func (s *SessionsSidebar) ArchivableSelection() (ids []string, skippedActive, skippedWorkspace int) {
	roots := s.sessionRoots()
	for id := range s.selected {
		if id == s.activeSessionID {
			skippedActive++
			continue
		}
		// Fail closed: require a known root equal to the current workspace.
		root, ok := roots[id]
		if s.currentRoot == "" || !ok || root != s.currentRoot {
			skippedWorkspace++
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, skippedActive, skippedWorkspace
}

// sessionRoots maps each known session ID to its workspace root, for
// workspace-scoping the bulk archive selection.
func (s *SessionsSidebar) sessionRoots() map[string]string {
	roots := make(map[string]string)
	for _, ws := range s.overviews {
		for _, sess := range ws.Sessions {
			roots[sess.ID] = ws.Root
		}
	}
	return roots
}

// SetCurrentRoot records the resolved root of the workspace this client is
// pointed at, used to scope bulk archive to it.
func (s *SessionsSidebar) SetCurrentRoot(root string) {
	s.currentRoot = root
}

// SetSelection replaces the selection set with the given IDs (used to keep
// only the sessions that failed to archive selected after a partial bulk
// archive). It does not enter visual mode.
func (s *SessionsSidebar) SetSelection(ids []string) {
	s.selected = make(map[string]bool, len(ids))
	for _, id := range ids {
		s.selected[id] = true
	}
	s.visualMode = false
	s.anchorID = ""
}

// SurvivingNeighbor returns the session ID nearest to the archived block
// that will still exist after the given IDs are archived: the first session
// below the block, else the first above it. Empty if none survive. Used to
// keep the cursor near where it was instead of snapping to the top.
func (s *SessionsSidebar) SurvivingNeighbor(archived []string) string {
	if len(archived) == 0 {
		return ""
	}
	gone := make(map[string]bool, len(archived))
	for _, id := range archived {
		gone[id] = true
	}
	lo, hi := -1, -1
	for i, r := range s.rows {
		if r.kind != sidebarRowSession {
			continue
		}
		if gone[s.overviews[r.wsIdx].Sessions[r.sessIdx].ID] {
			if lo < 0 {
				lo = i
			}
			hi = i
		}
	}
	if hi < 0 {
		return ""
	}
	for i := hi + 1; i < len(s.rows); i++ {
		if id, ok := s.sessionIDAt(i); ok && !gone[id] {
			return id
		}
	}
	for i := lo - 1; i >= 0; i-- {
		if id, ok := s.sessionIDAt(i); ok && !gone[id] {
			return id
		}
	}
	return ""
}

// FocusSessionID moves the cursor onto the session with id if present.
func (s *SessionsSidebar) FocusSessionID(id string) {
	if row := s.rowForSessionID(id); row >= 0 {
		s.cursor = row
		s.ensureVisible()
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

	// Sidebar-local hint while a multi-selection is active.
	if n := len(s.selected); n > 0 {
		mode := ""
		if s.visualMode {
			mode = "visual · "
		}
		hint := fmt.Sprintf("%s%d selected · a archive · esc clear", mode, n)
		lines = append(lines, t.Resource.AdditionalText.Render(ansi.Truncate(hint, width, "…")))
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
			sess := ws.Sessions[r.sessIdx]
			out = append(out, s.renderSessionRow(t, sess, width, selected, s.selected[sess.ID]))
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

func (s *SessionsSidebar) renderSessionRow(t *styles.Styles, sess proto.SessionOverview, width int, selected, marked bool) string {
	// Status glyph: busy dot, unread dot, or blank. When the row is
	// multi-selected, a check replaces the status dot so the selection is
	// visually distinct from both the cursor bar and the active arrow.
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
	if marked {
		marker = t.Resource.OnlineIcon.SetString(styles.CheckIcon).String()
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

	titleStyle := t.Resource.Name
	if marked {
		titleStyle = titleStyle.Bold(true)
	}
	styledPrefix := t.Resource.AdditionalText.Render(bar+" "+active) + marker + squareCell + " "
	return styledPrefix + titleStyle.Render(title)
}
