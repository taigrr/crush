package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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
	// sidebarRowSection is a non-selectable status-section header used only
	// in inbox mode (e.g. "Running", "Unread", "Read"). Like workspace
	// headers it takes a line but is not a navigation stop.
	sidebarRowSection
	// sidebarRowSpacer is a blank line inserted between workspace groups to
	// keep them visually separated. It is never selectable.
	sidebarRowSpacer
)

// sidebarRow is one rendered/navigable line. Workspace and section header
// rows are not selectable; session and overflow rows are.
type sidebarRow struct {
	kind sidebarRowKind
	// workspace index into the overviews slice.
	wsIdx int
	// session index within that workspace (only for session rows).
	sessIdx int
	// remaining is the count of hidden sessions (only for overflow rows).
	remaining int
	// label is the header text for section rows (inbox mode only).
	label string
}

// sidebarViewMode selects how the sidebar projects its sessions.
type sidebarViewMode uint8

const (
	// sidebarModeSessions is the default workspace-grouped, tiered view
	// with per-workspace headers.
	sidebarModeSessions sidebarViewMode = iota
	// sidebarModeInbox is a flat, cross-workspace view sectioned by status
	// (Running, Unread, Read) with a per-row workspace tag and no
	// workspace grouping.
	sidebarModeInbox
)

// minSessionsPerWorkspace is the floor on how many sessions each workspace
// shows in the navigator before an overflow row, even when vertical space
// is tight (a workspace with fewer sessions shows only what it has).
const minSessionsPerWorkspace = 5

// maxWorkspaceTagWidth caps the per-row workspace tag (inbox mode) so a long
// workspace basename cannot consume the row and push the title out. The tag
// is truncated with an ellipsis beyond this width.
const maxWorkspaceTagWidth = 14

// summaryMinHeight is the sidebar height at/above which the 3-line
// ready/working/total summary block is shown. Below it the fixed header is
// just the section title + a blank line, so short terminals still show
// session rows. Click hit-testing uses fixedHeaderHeight, which honors this
// same threshold, so the two never disagree.
const summaryMinHeight = 8

// SessionsSidebar is the left panel listing every known workspace and its
// sessions for cross-workspace navigation. It follows the imperative
// component pattern (see internal/ui/AGENTS.md): no Bubble Tea Update; the
// main model drives it via methods and reads Selected on enter.
type SessionsSidebar struct {
	com *common.Common

	// mode selects the row projection: workspace-grouped (default) or the
	// flat, status-sectioned inbox. It is a field on the long-lived sidebar
	// so it PERSISTS across sidebar close/reopen (toggleLeftSidebar never
	// resets it).
	mode sidebarViewMode

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

	// searchMode is true while the "/" text filter is active. In this mode
	// rebuildRows applies searchInput's value as a case-insensitive
	// substring predicate over each session's title and subtext, in BOTH
	// the grouped and inbox projections. A header/section that filters to
	// zero sessions is omitted.
	searchMode bool
	// searchInput mirrors the popup picker's filter textinput (see
	// dialog/sessions.go): it holds the live query the user is typing.
	searchInput textinput.Model

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

	// pendingSessions is the set of session IDs (any workspace) that
	// currently have an unresolved permission or question request cached
	// by this client. The row renderer consults HasPending to draw a
	// red "prompt-pending" indicator so a background session blocked on
	// a prompt is visible in the list before the user switches to it.
	pendingSessions map[string]bool
}

// SetPendingSessions replaces the set of session IDs that have an
// unresolved permission/question request. The main model calls this
// whenever a request is cached or resolved. A nil/empty map clears all
// indicators.
func (s *SessionsSidebar) SetPendingSessions(ids map[string]bool) {
	s.pendingSessions = ids
}

// HasPending reports whether the given session has an unresolved
// permission or question request awaiting a response on this client.
func (s *SessionsSidebar) HasPending(sessionID string) bool {
	return s.pendingSessions[sessionID]
}

// NewSessionsSidebar creates an empty sidebar bound to shared context.
func NewSessionsSidebar(com *common.Common) *SessionsSidebar {
	ti := textinput.New()
	ti.SetVirtualCursor(false)
	// The sidebar renders its own "/" prefix in Render, so clear the
	// textinput's default "> " prompt to avoid a doubled prompt.
	ti.Prompt = ""
	ti.Placeholder = "Filter sessions"
	if com != nil && com.Styles != nil {
		ti.SetStyles(com.Styles.TextInput)
	}
	return &SessionsSidebar{com: com, mode: sidebarModeInbox, selected: map[string]bool{}, searchInput: ti}
}

// EnterSearch activates the "/" text filter: focuses the input, clears any
// in-progress multi-select (a fresh filter is a new context; a stale
// selection must not survive into a filtered view and get bulk-archived),
// and reprojects rows. It composes with the current view mode (grouped or
// inbox).
func (s *SessionsSidebar) EnterSearch() {
	if s.searchMode {
		return
	}
	// Entering search is a fresh action: drop any multi-select/visual sweep.
	s.ClearSelection()
	s.searchMode = true
	s.searchInput.SetValue("")
	s.searchInput.Focus()
	prevID := s.selectedSessionID()
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// ExitSearch leaves the filter mode, clears the query, blurs the input, and
// restores the full (unfiltered) list, keeping the cursor on the same
// session where possible.
func (s *SessionsSidebar) ExitSearch() {
	if !s.searchMode {
		return
	}
	prevID := s.selectedSessionID()
	s.searchMode = false
	s.searchInput.SetValue("")
	s.searchInput.Blur()
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// Searching reports whether the "/" text filter is active.
func (s *SessionsSidebar) Searching() bool { return s.searchMode }

// HandleSearchKey feeds a key to the filter input while search mode is
// active and re-filters. It returns whether the query changed (so the
// caller can reschedule the live preview for the new top result). Callers
// must only invoke it for keys they want to route to the input; navigation
// and action keys are handled before this.
func (s *SessionsSidebar) HandleSearchKey(msg tea.KeyPressMsg) bool {
	before := s.searchInput.Value()
	var cmd tea.Cmd
	s.searchInput, cmd = s.searchInput.Update(msg)
	_ = cmd // the sidebar input needs no async cursor-blink command
	after := s.searchInput.Value()
	if after == before {
		return false
	}
	prevID := s.selectedSessionID()
	s.rebuildRows()
	// After a query change the previously-selected session may be filtered
	// out; restoreCursor falls back to the first match in that case.
	s.restoreCursor(prevID)
	return true
}

// searchQuery returns the current lowercased, trimmed filter query, or "".
func (s *SessionsSidebar) searchQuery() string {
	if !s.searchMode {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s.searchInput.Value()))
}

// sessionMatchesFilter reports whether a session passes the active filter.
// The predicate is a case-insensitive substring over the session's TITLE
// plus its SUBTEXT — the workspace basename (the inbox tag / group header)
// and the swarm animal — so a query can match by project or swarm name too.
// With no active query every session matches.
func (s *SessionsSidebar) sessionMatchesFilter(sess proto.SessionOverview, root string) bool {
	q := s.searchQuery()
	if q == "" {
		return true
	}
	hay := strings.ToLower(sess.Title + " " + filepath.Base(root) + " " + sess.Animal)
	return strings.Contains(hay, q)
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

// ToggleInbox flips between the workspace-grouped view and the flat,
// status-sectioned inbox, rebuilding the row projection and keeping the
// cursor on the same session where possible. The mode persists on the
// long-lived sidebar across close/reopen.
func (s *SessionsSidebar) ToggleInbox() {
	prevID := s.selectedSessionID()
	if s.mode == sidebarModeInbox {
		s.mode = sidebarModeSessions
	} else {
		s.mode = sidebarModeInbox
	}
	// Selection/visual sweep is anchored by session ID and survives the
	// reprojection, so it is preserved across a mode toggle.
	s.rebuildRows()
	s.restoreCursor(prevID)
}

// InboxMode reports whether the sidebar is in the flat inbox view.
func (s *SessionsSidebar) InboxMode() bool { return s.mode == sidebarModeInbox }

// title returns the sidebar's section title for the current mode.
func (s *SessionsSidebar) title() string {
	if s.mode == sidebarModeInbox {
		return "Inbox"
	}
	return "Sessions"
}

// SessionCounts summarizes the visible sessions across ALL workspaces the
// sidebar shows. Definitions:
//   - Working: sessions with an in-flight agent turn (IsBusy) — the SAME
//     signal the busy tier in sortSessions and the row busy dot use.
//   - Ready: sessions waiting for review, i.e. Unread AND NOT busy. This
//     matches exactly the sessions renderSessionRow shows a green dot for
//     (the green OnlineIcon is drawn only for Unread, non-busy rows), so
//     Ready == the number of visible green dots.
//   - Total: all visible (non-archived) sessions, INCLUDING any collapsed
//     under a workspace's "…N more" overflow row.
//
// Ready is NOT Total-minus-Working: read-idle sessions (read, not busy)
// count toward Total but are neither Ready nor Working, so
// Ready+Working <= Total.
//
// Empty workspaces contribute nothing (consistent with hidden headers).
// ws.Sessions is the visible/non-archived set (see the invariant in
// rebuildRows), so Total counts every shown-or-collapsed visible session.
type SessionCounts struct {
	Ready   int
	Working int
	Total   int
}

// SessionCounts computes the live ready/working/total tally. It reads
// straight from the current overviews, so it is always in sync with the
// latest SetOverviews / SetActiveSession refresh. When a "/" filter is
// active the tally is computed over the FILTERED set only, so the summary
// block agrees with the (filtered) rows shown below it.
func (s *SessionsSidebar) SessionCounts() SessionCounts {
	var c SessionCounts
	for _, ws := range s.overviews {
		for _, sess := range ws.Sessions {
			if !s.sessionMatchesFilter(sess, ws.Root) {
				continue
			}
			c.Total++
			switch {
			case s.HasPending(sess.ID):
				// A session blocked on a prompt shows the red dot, not
				// green, so it must not inflate the Ready tally. Count it
				// as Working (it is active and needs attention), keeping
				// Ready == number of visible green dots.
				c.Working++
			case sess.IsBusy:
				c.Working++
			case sessionReady(sess):
				// Ready == waiting-for-review == the green dot in
				// renderSessionRow (Unread and not busy).
				c.Ready++
			}
		}
	}
	return c
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

// rebuildRows reprojects overviews into the flattened navigable row list,
// dispatching on the current view mode, then clamps and snaps the cursor.
func (s *SessionsSidebar) rebuildRows() {
	s.rows = s.rows[:0]
	if s.mode == sidebarModeInbox {
		s.rebuildInboxRows()
	} else {
		s.rebuildGroupedRows()
	}
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	// Never leave the cursor resting on a header (workspace or section) if
	// a selectable row is reachable.
	s.snapCursorToSession(1)
}

// rebuildInboxRows projects a FLAT, cross-workspace list sectioned by
// status. Section order (highest priority first): Blocked (waiting on a
// permission/question prompt), Favorite (pinned by the user), then Running
// (busy), Unread (unread, not busy), and Read (everything else). Each
// session lands in exactly one section by that priority, so a favorited or
// blocked session sticks to the top regardless of its busy/unread state.
// Each section is sorted by UpdatedAt descending. There are no workspace
// headers; the per-row workspace tag (basename of the workspace root)
// supplies project context. Empty sections are omitted — in particular the
// Favorite and Blocked sections do not render unless populated — so
// filtering/hide-empty composes. Session rows still reference their source
// (wsIdx, sessIdx), so sessionIDAt/rowForSessionID/Selected/ClickToActivate
// and cross-workspace activation all work unchanged over the flat layout.
func (s *SessionsSidebar) rebuildInboxRows() {
	type ref struct{ wi, si int }
	var blocked, favorite, running, unread, other []ref
	for wi, ws := range s.overviews {
		for si, sess := range ws.Sessions {
			if !s.sessionMatchesFilter(sess, ws.Root) {
				continue
			}
			switch {
			case s.HasPending(sess.ID):
				blocked = append(blocked, ref{wi, si})
			case sess.Favorite:
				favorite = append(favorite, ref{wi, si})
			case sess.IsBusy:
				running = append(running, ref{wi, si})
			case sess.Unread:
				unread = append(unread, ref{wi, si})
			default:
				other = append(other, ref{wi, si})
			}
		}
	}
	byUpdatedDesc := func(refs []ref) {
		sort.SliceStable(refs, func(i, j int) bool {
			a := s.overviews[refs[i].wi].Sessions[refs[i].si]
			b := s.overviews[refs[j].wi].Sessions[refs[j].si]
			return a.UpdatedAt > b.UpdatedAt
		})
	}
	byUpdatedDesc(blocked)
	byUpdatedDesc(favorite)
	byUpdatedDesc(running)
	byUpdatedDesc(unread)
	byUpdatedDesc(other)

	emit := func(label string, refs []ref) {
		if len(refs) == 0 {
			return
		}
		s.rows = append(s.rows, sidebarRow{kind: sidebarRowSection, label: label})
		for _, r := range refs {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowSession, wsIdx: r.wi, sessIdx: r.si})
		}
	}
	emit("Blocked", blocked)
	emit("Favorite", favorite)
	emit("Running", running)
	emit("Unread", unread)
	emit("Read", other)
}

// filteredIdxs returns the indices (into ws.Sessions, preserving sort order)
// of the sessions in workspace wi that pass the active filter. With no
// active query it returns every index.
func (s *SessionsSidebar) filteredIdxs(wi int) []int {
	ws := s.overviews[wi]
	idxs := make([]int, 0, len(ws.Sessions))
	for si, sess := range ws.Sessions {
		if s.sessionMatchesFilter(sess, ws.Root) {
			idxs = append(idxs, si)
		}
	}
	return idxs
}

// rebuildGroupedRows projects the default workspace-grouped, tiered view
// with per-workspace headers and "…N more" overflow rows. When a filter is
// active, only matching sessions are emitted and a workspace that filters to
// zero matches contributes no header (composes with hide-empty-headers).
func (s *SessionsSidebar) rebuildGroupedRows() {
	caps := s.computeCaps()
	emittedGroups := 0
	for wi := range s.overviews {
		// Skip workspaces with no visible (and, when filtering, no matching)
		// sessions entirely: don't emit a header (or overflow) row for a
		// workspace that contributes zero navigable session rows. An empty
		// header would take space and be non-interactive.
		//
		// INVARIANT: ws.Sessions holds only VISIBLE (non-archived) sessions
		// — the server-side overview (ListWorkspaceOverviews) omits archived
		// ones, and proto.SessionOverview has no Archived field. So an empty
		// filtered set means "no visible/matching sessions".
		idxs := s.filteredIdxs(wi)
		if len(idxs) == 0 {
			continue
		}
		// Blank lines separate visible workspace groups. Counting emitted
		// groups avoids a leading spacer when earlier workspaces are empty or
		// filtered out.
		if emittedGroups > 0 {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowSpacer, wsIdx: wi})
		}
		emittedGroups++
		s.rows = append(s.rows, sidebarRow{kind: sidebarRowWorkspace, wsIdx: wi})
		shown := min(len(idxs), caps[wi])
		for _, si := range idxs[:shown] {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowSession, wsIdx: wi, sessIdx: si})
		}
		if remaining := len(idxs) - shown; remaining > 0 {
			s.rows = append(s.rows, sidebarRow{kind: sidebarRowOverflow, wsIdx: wi, remaining: remaining})
		}
	}
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
	// rebuildRows), so they don't count toward the height budget. Counts
	// use the FILTERED session set so an active "/" search rebalances the
	// budget over just the matches (and a workspace filtered to zero drops
	// out of nonEmpty entirely, matching rebuildGroupedRows).
	counts := make([]int, n)
	total := 0
	nonEmpty := 0
	for i := range s.overviews {
		c := len(s.filteredIdxs(i))
		counts[i] = c
		if c == 0 {
			continue
		}
		nonEmpty++
		total += 1 + c // header + sessions
	}
	if nonEmpty == 0 {
		return caps
	}
	// Account for the blank spacer line between each pair of visible
	// workspace groups.
	spacers := nonEmpty - 1
	total += spacers
	if total <= h {
		copy(caps, counts)
		return caps
	}

	// Even share: each non-empty workspace's block is
	// (h-spacers)/nonEmpty rows. Reserve one line for the header and one for
	// the overflow row, then floor at the minimum.
	perWorkspace := max(0, h-spacers) / nonEmpty
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
// or overflow row), i.e. not a workspace or section header.
func (s *SessionsSidebar) selectableRow(i int) bool {
	if i < 0 || i >= len(s.rows) {
		return false
	}
	k := s.rows[i].kind
	return k == sidebarRowSession || k == sidebarRowOverflow
}

// snapCursorToSession advances the cursor in the given direction (+1/-1)
// off a header row (workspace or section) onto the nearest selectable row,
// if any.
func (s *SessionsSidebar) snapCursorToSession(dir int) {
	if len(s.rows) == 0 {
		s.cursor = 0
		return
	}
	// Bail out when nothing is selectable, otherwise the direction flip
	// below can walk between both ends indefinitely.
	selectable := false
	for i := range s.rows {
		if s.selectableRow(i) {
			selectable = true
			break
		}
	}
	if !selectable {
		return
	}
	for s.cursor >= 0 && s.cursor < len(s.rows) && !s.selectableRow(s.cursor) {
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
	for i := range slices.Backward(s.rows) {
		if s.selectableRow(i) {
			s.cursor = i
			s.ensureVisible()
			s.extendVisual()
			return
		}
	}
}

// isSectionHeader reports whether the row at i is a section boundary: a
// workspace header (grouped mode) or a status-section header (inbox mode).
func (s *SessionsSidebar) isSectionHeader(i int) bool {
	if i < 0 || i >= len(s.rows) {
		return false
	}
	k := s.rows[i].kind
	return k == sidebarRowWorkspace || k == sidebarRowSection
}

// firstSelectableAfter returns the index of the first selectable row after
// header index h, or -1 if none.
func (s *SessionsSidebar) firstSelectableAfter(h int) int {
	for j := h + 1; j < len(s.rows); j++ {
		if s.selectableRow(j) {
			return j
		}
	}
	return -1
}

// MoveNextSection jumps the cursor to the first selectable row of the next
// section (workspace in grouped mode, status section in inbox mode).
func (s *SessionsSidebar) MoveNextSection() {
	if len(s.rows) == 0 {
		return
	}
	for i := s.cursor + 1; i < len(s.rows); i++ {
		if !s.isSectionHeader(i) {
			continue
		}
		if j := s.firstSelectableAfter(i); j >= 0 {
			s.cursor = j
			s.ensureVisible()
			s.extendVisual()
			return
		}
	}
}

// MovePrevSection jumps the cursor to the first selectable row of the current
// section; if already there, it jumps to the first selectable row of the
// previous section.
func (s *SessionsSidebar) MovePrevSection() {
	if len(s.rows) == 0 {
		return
	}
	// Find the header of the current section.
	curHeader := -1
	for i := s.cursor - 1; i >= 0; i-- {
		if s.isSectionHeader(i) {
			curHeader = i
			break
		}
	}
	firstOfCur := -1
	if curHeader >= 0 {
		firstOfCur = s.firstSelectableAfter(curHeader)
	}
	if curHeader >= 0 && s.cursor > firstOfCur {
		s.cursor = firstOfCur
		s.ensureVisible()
		s.extendVisual()
		return
	}
	// Already at the section start (or no current header): go to the
	// previous section.
	start := curHeader
	if start < 0 {
		start = s.cursor
	}
	for i := start - 1; i >= 0; i-- {
		if !s.isSectionHeader(i) {
			continue
		}
		if j := s.firstSelectableAfter(i); j >= 0 {
			s.cursor = j
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

// SessionTarget identifies a selected session together with the workspace
// that owns it, so a bulk action can route each session to its OWN
// workspace (attached or detached) rather than assuming the attached one.
// WorkspaceID is empty for a detached workspace; Root is then used to
// resolve it server-side.
type SessionTarget struct {
	WorkspaceID string
	Root        string
	ID          string
}

// ArchivableSelection returns the selected sessions eligible for bulk
// archive, in deterministic (sorted-by-id) order, together with the number
// of selected sessions skipped because they are the active session (never
// archive the one the user is viewing) or because they are currently busy
// (never archive a session mid-run: archiving prunes snapshot refs, which
// must not happen under a live agent).
//
// Sessions in ANY workspace are archivable — attached or detached — so
// each target carries its own workspace id and root for per-session
// routing. An id not present in the current overviews (e.g. dropped by a
// background refresh) is silently skipped: without a known workspace it
// cannot be routed, and archiving it would be a no-op false success.
func (s *SessionsSidebar) ArchivableSelection() (targets []SessionTarget, skippedActive, skippedBusy int) {
	for id := range s.selected {
		if id == s.activeSessionID {
			skippedActive++
			continue
		}
		t, busy, ok := s.lookupSession(id)
		if !ok {
			continue
		}
		if busy {
			skippedBusy++
			continue
		}
		targets = append(targets, t)
	}
	sortTargets(targets)
	return targets, skippedActive, skippedBusy
}

// MarkReadSelection returns the selected sessions eligible for bulk
// mark-as-read, in deterministic (sorted-by-id) order. Like
// ArchivableSelection it spans all workspaces (attached and detached),
// routing each session by its own workspace id/root; unknown ids are
// silently skipped. Unlike archive the active session is included and busy
// sessions are NOT skipped: marking a session read is non-destructive.
func (s *SessionsSidebar) MarkReadSelection() (targets []SessionTarget) {
	for id := range s.selected {
		t, _, ok := s.lookupSession(id)
		if !ok {
			continue
		}
		targets = append(targets, t)
	}
	sortTargets(targets)
	return targets
}

// lookupSession resolves a selected session id to its owning workspace
// target and live busy state from the current overviews.
func (s *SessionsSidebar) lookupSession(id string) (target SessionTarget, busy, ok bool) {
	for _, ws := range s.overviews {
		for _, sess := range ws.Sessions {
			if sess.ID == id {
				return SessionTarget{WorkspaceID: ws.WorkspaceID, Root: ws.Root, ID: id}, sess.IsBusy, true
			}
		}
	}
	return SessionTarget{}, false, false
}

// sortTargets orders targets by session id for deterministic processing.
func sortTargets(targets []SessionTarget) {
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
}

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

// FavoriteTargetUnderCursor resolves the session under the cursor to its
// owning workspace target and current favorite state, so the toggle can
// route the write to the session's OWN workspace (attached or detached).
// ok is false when the cursor is not on a session row.
func (s *SessionsSidebar) FavoriteTargetUnderCursor() (target SessionTarget, favorite, ok bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return SessionTarget{}, false, false
	}
	r := s.rows[s.cursor]
	if r.kind != sidebarRowSession {
		return SessionTarget{}, false, false
	}
	ws := s.overviews[r.wsIdx]
	sess := ws.Sessions[r.sessIdx]
	return SessionTarget{WorkspaceID: ws.WorkspaceID, Root: ws.Root, ID: sess.ID}, sess.Favorite, true
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
	// styled rows beneath. The 3-line summary block is fixed top matter
	// (like the section title), not a scrollable/selectable row — it does
	// not participate in s.rows, so cursor/row-index math is unaffected.
	//
	// At very small heights, drop the summary block so short terminals
	// still show session rows: below summaryMinHeight the fixed header is
	// just the title + blank (2 lines), matching the pre-summary layout.
	showSummary := height >= summaryMinHeight
	lines := []string{common.Section(t, s.title(), width)}
	headerLines := s.fixedHeaderHeight(height)
	// The "/" filter input renders as a fixed line directly under the
	// title (before the summary), so it never scrolls with the rows and
	// click hit-testing (fixedHeaderHeight, which also adds 1 while
	// searching) stays consistent with the cursor's row math.
	if s.searchMode {
		lines = append(lines, ansi.Truncate("/"+s.searchInput.View(), width, "…"))
	}
	if showSummary {
		lines = append(lines, s.summaryLines(width)...)
	}
	lines = append(lines, "")

	bodyHeight := max(1, height-headerLines)

	// The per-workspace session cap depends on the available body height,
	// so rebuild the row projection whenever it changes (e.g. terminal
	// resize) before rendering.
	if bodyHeight != s.bodyHeight {
		prevID := s.selectedSessionID()
		s.bodyHeight = bodyHeight
		s.rebuildRows()
		s.restoreCursor(prevID)
	}

	rendered := s.renderRows(width, focused)
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

	return strings.Join(lines, "\n")
}

// fixedHeaderHeight returns the number of fixed (non-scrolling, non-row)
// lines rendered above the session list for the given sidebar height: the
// section title + trailing blank (2), plus the 3-line summary block when the
// height is at least summaryMinHeight, plus one line for the "/" filter
// input while search mode is active. Click hit-testing subtracts this from
// the clicked line so screen-Y maps to the same rows the cursor uses.
func (s *SessionsSidebar) fixedHeaderHeight(height int) int {
	h := 2 // section title + trailing blank
	if s.searchMode {
		h++ // the "/" filter input line
	}
	if height >= summaryMinHeight {
		h += 3 // ready/working/total
	}
	return h
}

// ClickToActivate maps a click at localY (0-based, relative to the sidebar's
// own top edge) for a sidebar rendered at the given height to a projected
// row, and — if that row is a session — moves the cursor there and reports
// activatable=true so the caller can run the normal open path. A click on a
// workspace header or the "…N more" overflow row moves the cursor there (so
// the click still "lands") but is not activatable. A click on the fixed top
// matter (title, summary, blank) or below the last row is a no-op
// (activatable=false, moved=false).
//
// Coordinate math: localY covers the whole sidebar column. Subtracting
// fixedHeaderHeight(height) yields the body line (0-based, first visible
// row). Adding s.scroll — the index of the first visible row, updated every
// Render — maps to the absolute row index in s.rows, the SAME projection
// sessionIDAt/rowForSessionID use. So it is correct at both header sizes and
// at any scroll offset.
//
// A plain click is a fresh single action: it clears any in-progress
// multi-select selection and never enters visual mode.
func (s *SessionsSidebar) ClickToActivate(localY, height int) (activatable, moved bool) {
	header := s.fixedHeaderHeight(height)
	if localY < header {
		return false, false // clicked the fixed top matter
	}
	rowIdx := s.scroll + (localY - header)
	if rowIdx < 0 || rowIdx >= len(s.rows) {
		return false, false // clicked empty space past the last row
	}
	// Fresh single action: drop any multi-selection / visual mode.
	s.ClearSelection()
	s.cursor = rowIdx
	s.ensureVisible()
	switch s.rows[rowIdx].kind {
	case sidebarRowSession:
		return true, true
	default:
		// Header or overflow: cursor moved, but nothing to activate here.
		return false, true
	}
}

// summaryLines renders the fixed 3-line count block shown under the section
// title: a green-dot "ready" line, a yellow-dot "working" line, and a plain
// "total" line. The yellow BusyIcon matches the row busy dot exactly, and
// the green OnlineIcon matches the row unread dot: "ready" counts exactly
// the sessions renderSessionRow draws a green dot for (Unread and not busy),
// so the ready count equals the number of visible green dots.
func (s *SessionsSidebar) summaryLines(width int) []string {
	t := s.com.Styles
	c := s.SessionCounts()
	ready := t.Resource.OnlineIcon.String() + " " +
		t.Resource.AdditionalText.Render(fmt.Sprintf("%d ready %s", c.Ready, plural(c.Ready, "session")))
	working := t.Resource.BusyIcon.String() + " " +
		t.Resource.AdditionalText.Render(fmt.Sprintf("%d working %s", c.Working, plural(c.Working, "session")))
	total := "  " + t.Resource.AdditionalText.Render(fmt.Sprintf("%d total %s", c.Total, plural(c.Total, "session")))
	return []string{
		ansi.Truncate(ready, width, "…"),
		ansi.Truncate(working, width, "…"),
		ansi.Truncate(total, width, "…"),
	}
}

// plural returns the word with a trailing "s" unless n == 1.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// renderRows renders each navigable row to a styled line at the given width.
// focused reports whether the sidebar owns keyboard focus. When it does not,
// the cursor row is marked with the selection bar alone rather than a
// full-row highlight, so it does not compete with the editor caret.
func (s *SessionsSidebar) renderRows(width int, focused bool) []string {
	t := s.com.Styles
	out := make([]string, 0, len(s.rows))
	for i, r := range s.rows {
		selected := i == s.cursor
		switch r.kind {
		case sidebarRowSpacer:
			out = append(out, "")
		case sidebarRowWorkspace:
			out = append(out, s.renderWorkspaceRow(t, s.overviews[r.wsIdx], width))
		case sidebarRowSection:
			out = append(out, common.Section(t, r.label, width))
		case sidebarRowSession:
			ws := s.overviews[r.wsIdx]
			sess := ws.Sessions[r.sessIdx]
			// In inbox mode there are no workspace headers, so tag each
			// row with its workspace basename for project context.
			tag := ""
			if s.mode == sidebarModeInbox {
				tag = filepath.Base(ws.Root)
			}
			out = append(out, s.renderSessionRow(t, sess, width, selected, focused, s.selected[sess.ID], tag))
		case sidebarRowOverflow:
			out = append(out, s.renderOverflowRow(t, r.remaining, width, selected, focused))
		}
	}
	return out
}

// renderOverflowRow renders the "…N more" row that opens the workspace's
// full session picker when selected. It aligns under the session titles
// (a 5-cell prefix: bar + space + active + marker + space).
func (s *SessionsSidebar) renderOverflowRow(t *styles.Styles, remaining, width int, selected, focused bool) string {
	bar := " "
	if selected {
		bar = styles.BorderThick
	}
	label := fmt.Sprintf("… %d more", remaining)
	prefix := bar + "    " // bar + 4 spaces = 5-cell prefix
	avail := max(1, width-ansi.StringWidth(prefix))
	label = ansi.Truncate(label, avail, "…")
	if selected && focused {
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

func (s *SessionsSidebar) renderSessionRow(t *styles.Styles, sess proto.SessionOverview, width int, selected, focused, marked bool, tag string) string {
	// Status glyph: pending-prompt dot (red), busy dot (yellow), unread
	// dot (green), or blank. Pending outranks busy because a session
	// blocked on a permission/question prompt is almost always ALSO busy
	// (its agent run is still in-flight while Request blocks); if busy
	// won, the red dot would never show for the case it exists to flag.
	// Pending is the actionable "needs you now" state, so it takes the
	// row. When multi-selected, a check replaces the status dot so the
	// selection is visually distinct from both the cursor bar and the
	// active arrow.
	pending := s.HasPending(sess.ID)
	statusStyle := t.Resource.OfflineIcon
	switch {
	case pending:
		statusStyle = t.Resource.ErrorIcon
	case sess.IsBusy:
		statusStyle = t.Resource.BusyIcon
	case sess.Unread:
		statusStyle = t.Resource.OnlineIcon
	}
	marker := " "
	if pending || sess.IsBusy || sess.Unread {
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

	// Inbox mode appends a dim workspace tag suffix (basename of the
	// workspace root) since there are no workspace headers. Cap the tag
	// itself (a long basename must not eat the row), budget it before the
	// title, and clamp the final composed line to width so it can never
	// overrun the sidebar column and corrupt the layout.
	tagRaw := ""
	if tag != "" {
		tagRaw = " " + ansi.Truncate(tag, maxWorkspaceTagWidth, "…")
	}
	// Favorited sessions carry a trailing star so they read as pinned even
	// in the grouped view (where there is no Favorite section). It is
	// budgeted before the title, like the tag, so it never overruns.
	favRaw := ""
	if sess.Favorite {
		favRaw = " " + styles.FavoriteIcon
	}
	avail := max(1, width-prefixWidth-ansi.StringWidth(tagRaw)-ansi.StringWidth(favRaw))
	title = ansi.Truncate(title, avail, "…")

	if selected && focused {
		// Full-row highlight without extra padding (the dialog selected
		// style adds Padding(0,1) which would overflow the sidebar width).
		line := ansi.Truncate(prefixRaw+title+favRaw+tagRaw, width, "…")
		return t.Dialog.SelectedItem.UnsetPadding().Width(width).Render(line)
	}

	titleStyle := t.Resource.Name
	if marked {
		titleStyle = titleStyle.Bold(true)
	}
	styledPrefix := t.Resource.AdditionalText.Render(bar+" "+active) + marker + squareCell + " "
	styledFav := ""
	if favRaw != "" {
		styledFav = t.Resource.Name.Render(favRaw)
	}
	styledTag := ""
	if tagRaw != "" {
		styledTag = t.Resource.AdditionalText.Render(tagRaw)
	}
	return ansi.Truncate(styledPrefix+titleStyle.Render(title)+styledFav+styledTag, width, "…")
}
