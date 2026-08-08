package model

import (
	"image"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

func newTestSidebar(t *testing.T) *SessionsSidebar {
	t.Helper()
	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty}
	return NewSessionsSidebar(com)
}

func sampleOverviews() []proto.WorkspaceOverview {
	return []proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a1", Title: "First"},
				{ID: "a2", Title: "Second"},
			},
		},
		{
			Root:     "/proj/b",
			Attached: false,
			Sessions: []proto.SessionOverview{
				{ID: "b1", Title: "Other"},
			},
		},
	}
}

// TestSidebar_CursorStartsOnFirstSession verifies the cursor lands on a
// session row, not a workspace header.
func TestSidebar_CursorStartsOnFirstSession(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	root, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "/proj/a", root)
	require.Equal(t, "a1", id)
	require.True(t, s.SelectedWorkspaceAttached())
}

// TestSidebar_NavigationSkipsHeaders verifies down/up move between session
// rows across workspace groups, never resting on a header.
func TestSidebar_NavigationSkipsHeaders(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	s.MoveDown() // a1 -> a2
	_, id, _ := s.Selected()
	require.Equal(t, "a2", id)

	s.MoveDown() // a2 -> b1 (skips /proj/b header)
	root, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "/proj/b", root)
	require.Equal(t, "b1", id)
	require.False(t, s.SelectedWorkspaceAttached())

	s.MoveDown() // at end, stays on b1
	_, id, _ = s.Selected()
	require.Equal(t, "b1", id)

	s.MoveUp() // b1 -> a2
	_, id, _ = s.Selected()
	require.Equal(t, "a2", id)
}

// TestSidebar_SetOverviewsKeepsCursorOnSession verifies a data refresh keeps
// the cursor on the same session when it still exists.
func TestSidebar_SetOverviewsKeepsCursorOnSession(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.MoveDown()
	s.MoveDown() // on b1

	// Refresh with the same data; cursor should stay on b1.
	s.SetOverviews(sampleOverviews())
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "b1", id)
}

// TestSidebar_RenderShowsTitlesAndDoesNotPanic exercises rendering at a
// small size.
func TestSidebar_RenderShowsTitlesAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	out := s.Render(30, 10, true)
	require.Contains(t, out, "First")
	require.Contains(t, out, "Other")
}

// TestSidebar_EmptyIsSafe verifies rendering and selection with no data.
func TestSidebar_EmptyIsSafe(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	_, _, ok := s.Selected()
	require.False(t, ok)
	out := s.Render(30, 10, false)
	require.Contains(t, out, "No sessions yet")
}

// manySessions builds a workspace with n sessions titled s1..sn.
func manySessions(root string, n int) proto.WorkspaceOverview {
	ws := proto.WorkspaceOverview{Root: root, Attached: true}
	for i := 1; i <= n; i++ {
		ws.Sessions = append(ws.Sessions, proto.SessionOverview{
			ID:    root + "-" + string(rune('0'+i)),
			Title: "Session",
		})
	}
	return ws
}

// TestSidebar_OverflowRowCapsSessions verifies a workspace with more
// sessions than the per-workspace cap shows an overflow row, and that the
// overflow row reports the workspace root for the picker.
func TestSidebar_OverflowRowCapsSessions(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{manySessions("/proj/a", 20)})
	// Render with a tight body so the cap (min 5) applies. At height 9 the
	// 5-line fixed header (title + 3 summary + blank) leaves body = 4.
	s.Render(30, 9, true)

	// With one workspace and body height 4: header(1)+20 sessions = 21 > 4,
	// so cap = max(5, 4-2)=5 sessions + overflow row.
	// Navigate to the last selectable row: it must be the overflow row.
	for range 10 {
		s.MoveDown()
	}
	root, ok := s.SelectedOverflowWorkspace()
	require.True(t, ok, "cursor should reach the overflow row")
	require.Equal(t, "/proj/a", root)
}

// TestSidebar_NoOverflowWhenEverythingFits verifies that when all sessions
// fit in the body there is no overflow row.
func TestSidebar_NoOverflowWhenEverythingFits(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{manySessions("/proj/a", 3)})
	s.Render(30, 40, true) // plenty of room

	// Move to the end; never land on an overflow row.
	for range 10 {
		s.MoveDown()
	}
	_, ok := s.SelectedOverflowWorkspace()
	require.False(t, ok)
	out := s.Render(30, 40, true)
	require.NotContains(t, out, "more")
}

// TestSidebar_EvenShareAcrossWorkspaces verifies each workspace is capped so
// one cannot push the others off screen: with two big workspaces both show
// an overflow row.
func TestSidebar_EvenShareAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		manySessions("/proj/a", 20),
		manySessions("/proj/b", 20),
	})
	s.Render(30, 22, true) // body = 20, split across 2 workspaces

	overflowRoots := map[string]bool{}
	// Walk every selectable row and collect overflow workspaces.
	s.cursor = 0
	s.snapCursorToSession(1)
	seen := -1
	for s.cursor != seen {
		seen = s.cursor
		if root, ok := s.SelectedOverflowWorkspace(); ok {
			overflowRoots[root] = true
		}
		s.MoveDown()
	}
	require.True(t, overflowRoots["/proj/a"], "workspace a should overflow")
	require.True(t, overflowRoots["/proj/b"], "workspace b should overflow")
}

func sortedIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// TestSidebar_VisualModeSweepsContiguousRange verifies entering visual mode
// anchors at the cursor and moving sweeps a contiguous range of sessions
// into the selection, spanning across workspace headers.
func TestSidebar_VisualModeSweepsContiguousRange(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	s.ToggleVisualMode() // anchor on a1, selects a1
	require.True(t, s.VisualMode())
	require.Equal(t, []string{"a1"}, s.SelectedSessionIDs())

	s.MoveDown() // a1 -> a2, sweep adds a2
	require.Equal(t, []string{"a1", "a2"}, sortedIDs(s.SelectedSessionIDs()))

	s.MoveDown() // a2 -> b1 (skips header), sweep adds b1
	require.Equal(t, []string{"a1", "a2", "b1"}, sortedIDs(s.SelectedSessionIDs()))
}

// TestSidebar_VisualSweepIsAdditive verifies moving back over a swept range
// does not deselect (additive model; space toggles discrete members).
func TestSidebar_VisualSweepIsAdditive(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected
	s.MoveUp()   // back to a2; selection stays
	require.Equal(t, []string{"a1", "a2", "b1"}, sortedIDs(s.SelectedSessionIDs()))
}

// TestSidebar_SpaceTogglesDiscreteMembers verifies space builds a
// non-contiguous selection and toggles individual members off again.
func TestSidebar_SpaceTogglesDiscreteMembers(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	s.ToggleSelected() // a1
	s.MoveDown()
	s.MoveDown() // b1 (skip a2)
	s.ToggleSelected()
	require.Equal(t, []string{"a1", "b1"}, sortedIDs(s.SelectedSessionIDs()))
	require.False(t, s.VisualMode(), "space alone does not enter visual mode")

	s.ToggleSelected() // toggle b1 back off
	require.Equal(t, []string{"a1"}, s.SelectedSessionIDs())
}

// TestSidebar_ClearOnVisualToggleAndEsc verifies toggling visual mode off
// (and ClearSelection) exits and drops the whole selection.
func TestSidebar_ClearOnVisualToggleAndEsc(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())

	s.ToggleVisualMode()
	s.MoveDown()
	require.NotEmpty(t, s.SelectedSessionIDs())

	s.ToggleVisualMode() // v again exits + clears
	require.False(t, s.VisualMode())
	require.Empty(t, s.SelectedSessionIDs())
	require.Equal(t, 0, s.SelectionCount())

	// Rebuild a selection and clear it directly (esc path).
	s.ToggleSelected()
	require.NotEmpty(t, s.SelectedSessionIDs())
	s.ClearSelection()
	require.Empty(t, s.SelectedSessionIDs())
}

// TestSidebar_ArchivableSelectionSkipsActive verifies the bulk-archive
// gathering excludes the currently active session but INCLUDES sessions
// from other (detached) workspaces, each routed to its own workspace.
func TestSidebar_ArchivableSelectionSkipsActive(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")
	s.SetActiveSession("a1")

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	require.Equal(t, []string{"a1", "a2", "b1"}, sortedIDs(s.SelectedSessionIDs()))
	targets, skippedActive, _ := s.ArchivableSelection()
	// a1 is active (skipped); a2 (/proj/a) and b1 (/proj/b) are both
	// archivable, each carrying its own workspace root.
	require.Equal(t, []SessionTarget{
		{Root: "/proj/a", ID: "a2"},
		{Root: "/proj/b", ID: "b1"},
	}, targets)
	require.Equal(t, 1, skippedActive)
}

// TestSidebar_ArchivableSelectionSpansWorkspaces verifies that sessions
// from every workspace (attached and detached) are archivable and routed
// to their own workspace root, in deterministic order.
func TestSidebar_ArchivableSelectionSpansWorkspaces(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	targets, skippedActive, _ := s.ArchivableSelection()
	require.Equal(t, []SessionTarget{
		{Root: "/proj/a", ID: "a1"},
		{Root: "/proj/a", ID: "a2"},
		{Root: "/proj/b", ID: "b1"},
	}, targets)
	require.Equal(t, 0, skippedActive)
}

// TestSidebar_ArchivableSelectionSkipsUnknown verifies an id not present in
// the current overviews (e.g. dropped by a background refresh) is silently
// skipped: without a known workspace it cannot be routed.
func TestSidebar_ArchivableSelectionSkipsUnknown(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")
	s.SetSelection([]string{"ghost"}) // not present in any overview
	targets, skippedActive, _ := s.ArchivableSelection()
	require.Empty(t, targets, "unknown id must not be archivable")
	require.Equal(t, 0, skippedActive)
}

// TestSidebar_ArchivableSelectionSkipsBusy verifies a busy session is
// excluded from the archivable set and counted, so a mid-run session is
// never archived (which would prune its snapshot refs).
func TestSidebar_ArchivableSelectionSkipsBusy(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "idle"},
				{ID: "busy", IsBusy: true},
			},
		},
	})
	s.SetSelection([]string{"idle", "busy"})
	targets, skippedActive, skippedBusy := s.ArchivableSelection()
	require.Equal(t, []SessionTarget{{Root: "/proj/a", ID: "idle"}}, targets)
	require.Equal(t, 0, skippedActive)
	require.Equal(t, 1, skippedBusy)
}

// TestSidebar_MarkReadSelectionIncludesBusy verifies mark-read does NOT
// skip busy sessions (marking read is non-destructive).
func TestSidebar_MarkReadSelectionIncludesBusy(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{{ID: "busy", IsBusy: true}},
		},
	})
	s.SetSelection([]string{"busy"})
	require.Equal(t, []SessionTarget{{Root: "/proj/a", ID: "busy"}}, s.MarkReadSelection())
}

// TestSidebar_SelectionSurvivesRefresh verifies a data refresh keeps the
// selection set intact (keyed by session ID).
func TestSidebar_SelectionSurvivesRefresh(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.ToggleSelected() // a1

	s.SetOverviews(sampleOverviews())
	require.Equal(t, []string{"a1"}, s.SelectedSessionIDs())
}

// TestSidebar_VisualAnchorSurvivesReorder verifies that when a background
// refresh reorders rows mid-sweep, the anchor (stored by session ID) still
// resolves so the swept range matches the visually-contiguous sessions,
// not stale row indices.
func TestSidebar_VisualAnchorSurvivesReorder(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a1", Title: "One", UpdatedAt: 100},
				{ID: "a2", Title: "Two", UpdatedAt: 90},
				{ID: "a3", Title: "Three", UpdatedAt: 80},
			},
		},
	})

	s.ToggleVisualMode() // anchor on a1 (top)
	require.Equal(t, []string{"a1"}, s.SelectedSessionIDs())

	// A refresh flips the order so a1 is now at the bottom.
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a3", Title: "Three", UpdatedAt: 300},
				{ID: "a2", Title: "Two", UpdatedAt: 200},
				{ID: "a1", Title: "One", UpdatedAt: 100},
			},
		},
	})
	// Cursor was restored to a1 (now the last row). Move up once: the sweep
	// should span a1..a2 (anchor a1 resolved to its new row), not stale
	// indices.
	s.MoveUp()
	require.Equal(t, []string{"a1", "a2"}, sortedIDs(s.SelectedSessionIDs()))
}

// TestSidebar_SortPreservesBusyUnreadTiers verifies the sidebar sort keeps
// busy → unread priority above plain UpdatedAt ordering (so the overflow
// cap never hides live/unseen work), with the active session pinned first.
func TestSidebar_SortPreservesBusyUnreadTiers(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetActiveSession("act")
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "old", Title: "Old", UpdatedAt: 500},
				{ID: "busy", Title: "Busy", UpdatedAt: 10, IsBusy: true},
				{ID: "unread", Title: "Unread", UpdatedAt: 20, Unread: true},
				{ID: "act", Title: "Active", UpdatedAt: 1},
			},
		},
	})
	// Expected order: active (pinned), busy, unread, then plain by UpdatedAt.
	got := []string{}
	for _, sess := range s.overviews[0].Sessions {
		got = append(got, sess.ID)
	}
	require.Equal(t, []string{"act", "busy", "unread", "old"}, got)
}

// TestSidebar_SurvivingNeighbor verifies the neighbor picked after a bulk
// archive is the first session below the archived block, else above.
func TestSidebar_SurvivingNeighbor(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a1", Title: "1", UpdatedAt: 40},
				{ID: "a2", Title: "2", UpdatedAt: 30},
				{ID: "a3", Title: "3", UpdatedAt: 20},
				{ID: "a4", Title: "4", UpdatedAt: 10},
			},
		},
	})
	// Archiving a2,a3 -> neighbor below is a4.
	require.Equal(t, "a4", s.SurvivingNeighbor([]string{"a2", "a3"}))
	// Archiving the last two -> neighbor above is a2.
	require.Equal(t, "a2", s.SurvivingNeighbor([]string{"a3", "a4"}))
	// Archiving everything -> no survivor.
	require.Equal(t, "", s.SurvivingNeighbor([]string{"a1", "a2", "a3", "a4"}))
}

// TestSidebar_SetSelectionTrimsToFailures verifies SetSelection replaces the
// selection with only the given IDs and exits visual mode.
func TestSidebar_SetSelectionTrimsToFailures(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected, visual on

	s.SetSelection([]string{"b1"})
	require.Equal(t, []string{"b1"}, s.SelectedSessionIDs())
	require.False(t, s.VisualMode())
}

// TestSidebar_HidesEmptyWorkspaceHeader verifies a workspace with zero
// visible sessions emits neither a header nor any row, while a non-empty
// workspace still shows its header + sessions.
func TestSidebar_HidesEmptyWorkspaceHeader(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{Root: "/proj/ghostws", Attached: false}, // no sessions
		{
			Root:     "/proj/full",
			Attached: true,
			Sessions: []proto.SessionOverview{{ID: "f1", Title: "One"}},
		},
	})
	s.Render(30, 40, true)

	// No row should reference the empty workspace.
	for _, r := range s.rows {
		require.NotEqual(t, "/proj/ghostws", s.overviews[r.wsIdx].Root,
			"empty workspace must contribute no rows (incl. header)")
	}
	// The non-empty workspace's header + session are present; the empty
	// workspace's unique basename must not appear anywhere in the output.
	out := s.Render(30, 40, true)
	require.Contains(t, out, "full")
	require.Contains(t, out, "One")
	require.NotContains(t, out, "ghostws", "empty workspace header must not render")
}

// TestSidebar_HeaderShownWhenNonEmpty is the positive control: a workspace
// with >=1 session shows its header.
func TestSidebar_HeaderShownWhenNonEmpty(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{{ID: "a1", Title: "One"}},
	}})
	s.Render(30, 40, true)
	hasHeader := false
	for _, r := range s.rows {
		if r.kind == sidebarRowWorkspace {
			hasHeader = true
		}
	}
	require.True(t, hasHeader, "non-empty workspace should have a header row")
}

// TestSidebar_AllEmptyRendersPlaceholder verifies that when every workspace
// is empty the body renders the empty placeholder, no stray header, no crash.
func TestSidebar_AllEmptyRendersPlaceholder(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{Root: "/proj/a"},
		{Root: "/proj/b"},
	})
	out := s.Render(30, 20, true)
	require.Empty(t, s.rows, "no rows when all workspaces are empty")
	require.Contains(t, out, "No sessions yet")
	_, _, ok := s.Selected()
	require.False(t, ok)
}

// TestSidebar_RowIndexIntegrityWithHiddenHeader verifies cursor/row-index
// mapping (sessionIDAt/rowForSessionID/Selected) stays correct when an empty
// workspace's header is suppressed between two non-empty workspaces.
func TestSidebar_RowIndexIntegrityWithHiddenHeader(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{{ID: "a1", Title: "A1"}},
		},
		{Root: "/proj/empty"}, // suppressed
		{
			Root:     "/proj/b",
			Attached: false,
			Sessions: []proto.SessionOverview{{ID: "b1", Title: "B1"}},
		},
	})
	s.Render(30, 40, true)

	// Cursor starts on a1.
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "a1", id)

	// Down moves to b1, skipping the (present) /proj/b header and never
	// landing in the suppressed empty workspace.
	s.MoveDown()
	root, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "/proj/b", root)
	require.Equal(t, "b1", id)

	// Every session row maps back to its own id.
	for i, r := range s.rows {
		if r.kind != sidebarRowSession {
			continue
		}
		gotID, ok := s.sessionIDAt(i)
		require.True(t, ok)
		require.Equal(t, s.overviews[r.wsIdx].Sessions[r.sessIdx].ID, gotID)
	}
}

// TestSidebar_HeaderDropsAfterWorkspaceEmptied simulates a bulk archive
// emptying a workspace: after a refresh with that workspace now empty, its
// header is gone and the cursor lands on a valid remaining session.
func TestSidebar_HeaderDropsAfterWorkspaceEmptied(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{{ID: "a1", Title: "A1"}},
		},
		{
			Root:     "/proj/b",
			Attached: false,
			Sessions: []proto.SessionOverview{{ID: "b1", Title: "B1"}},
		},
	})
	s.Render(30, 40, true)
	s.MoveDown() // cursor on b1
	_, id, _ := s.Selected()
	require.Equal(t, "b1", id)

	// Refresh with /proj/b now empty (its only session archived).
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{{ID: "a1", Title: "A1"}},
		},
		{Root: "/proj/b"}, // emptied
	})
	s.Render(30, 40, true)
	for _, r := range s.rows {
		require.NotEqual(t, "/proj/b", s.overviews[r.wsIdx].Root)
	}
	// Cursor snapped to a valid remaining session.
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "a1", id)
}

// TestSidebar_SessionCounts verifies ready/working/total across a mix of
// busy/unread/read-idle sessions in multiple workspaces. Ready keys off the
// Unread field (and not busy) — exactly the green-dot condition in
// renderSessionRow. Archived sessions are already excluded from overviews,
// so they simply don't appear. Read-idle sessions (read, not busy) count
// toward Total but neither Ready nor Working, so Ready+Working < Total.
func TestSidebar_SessionCounts(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a1", Title: "read-idle"},            // total only
				{ID: "a2", Title: "busy", IsBusy: true},   // working
				{ID: "a3", Title: "unread", Unread: true}, // ready
			},
		},
		{Root: "/proj/empty"}, // contributes nothing
		{
			Root:     "/proj/b",
			Attached: false,
			Sessions: []proto.SessionOverview{
				{ID: "b1", Title: "busy2", IsBusy: true},                     // working
				{ID: "b2", Title: "busy+unread", IsBusy: true, Unread: true}, // busy wins
				{ID: "b3", Title: "unread2", Unread: true},                   // ready
				{ID: "b4", Title: "read-idle2"},                              // total only
			},
		},
	})

	c := s.SessionCounts()
	require.Equal(t, 7, c.Total, "all visible sessions across workspaces")
	require.Equal(t, 3, c.Working, "busy sessions (a2, b1, b2)")
	require.Equal(t, 2, c.Ready, "unread, non-busy sessions (a3, b3)")
	require.Less(t, c.Ready+c.Working, c.Total, "read-idle sessions are neither ready nor working")
}

// TestSidebar_SessionCountsLiveOnRefresh verifies counts update when a
// session's state changes across a SetOverviews refresh. Ready tracks the
// Unread field (and not busy), and read-idle sessions are excluded.
func TestSidebar_SessionCountsLiveOnRefresh(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "a1", Title: "x", IsBusy: true},
			{ID: "a2", Title: "read-idle"},
		},
	}})
	require.Equal(t, SessionCounts{Ready: 0, Working: 1, Total: 2}, s.SessionCounts())

	// a1 finished its turn and now waits for review (unread); a2 still read.
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "a1", Title: "x", IsBusy: false, Unread: true},
			{ID: "a2", Title: "read-idle"},
		},
	}})
	require.Equal(t, SessionCounts{Ready: 1, Working: 0, Total: 2}, s.SessionCounts())
}

// TestSidebar_SummaryRendersInTopMatter verifies the 3 summary lines render
// in the fixed header (above the list) and do not become selectable rows.
func TestSidebar_SummaryRendersInTopMatter(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "a1", Title: "One"},
			{ID: "a2", Title: "Two", IsBusy: true},
		},
	}})
	out := s.Render(40, 20, true)
	require.Contains(t, out, "ready")
	require.Contains(t, out, "working")
	require.Contains(t, out, "total")

	// Summary lines are not rows: only the 2 sessions (+ header) exist as
	// rows, none of them a summary line.
	sessionRows := 0
	for _, r := range s.rows {
		if r.kind == sidebarRowSession {
			sessionRows++
		}
	}
	require.Equal(t, 2, sessionRows)
	// Cursor still maps to a real session, unaffected by the fixed matter.
	// (a2 is busy so it sorts to the top tier.)
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "a2", id)
}

// TestSidebar_SummaryDroppedAtSmallHeight verifies the 3-line summary block
// is omitted at very small heights so short terminals still show session
// rows (fixed header falls back to title + blank).
func TestSidebar_SummaryDroppedAtSmallHeight(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{{ID: "a1", Title: "OnlyOne"}},
	}})

	// Tall enough: summary present.
	tall := s.Render(30, 20, true)
	require.Contains(t, tall, "ready")
	require.Contains(t, tall, "total")

	// Very short: summary dropped, but the session row still renders.
	short := s.Render(30, 5, true)
	require.NotContains(t, short, "ready")
	require.NotContains(t, short, "working")
	require.Contains(t, short, "OnlyOne", "session row must still show at small height")
}

// TestSidebar_ClickToActivateMapsRow verifies click-Y → row mapping with the
// full 5-line header (summary shown) and no scroll: body line 0 is the first
// row (a workspace header), line 1 the first session, etc.
func TestSidebar_ClickToActivateMapsRow(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews()) // /proj/a: a1,a2 ; /proj/b: b1
	height := 40
	s.Render(30, height, true) // header = 5 (summary shown)

	// rows: [wsA, a1, a2, wsB, b1]. Header is 5 lines; row i is at localY 5+i.
	// Click a1 (row index 1) -> activatable, cursor on a1.
	act, moved := s.ClickToActivate(5+1, height)
	require.True(t, act)
	require.True(t, moved)
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "a1", id)

	// Click b1 (row index 4).
	act, _ = s.ClickToActivate(5+4, height)
	require.True(t, act)
	_, id, _ = s.Selected()
	require.Equal(t, "b1", id)

	// Click a workspace header row (index 0) -> not activatable but moved.
	act, moved = s.ClickToActivate(5+0, height)
	require.False(t, act)
	require.True(t, moved)
}

// TestSidebar_ClickIgnoresFixedMatter verifies clicks on the title/summary
// lines (localY < header) are no-ops.
func TestSidebar_ClickIgnoresFixedMatter(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.Render(30, 40, true)
	for y := 0; y < 5; y++ { // title + 3 summary + blank
		act, moved := s.ClickToActivate(y, 40)
		require.False(t, act, "fixed matter click must not activate (y=%d)", y)
		require.False(t, moved, "fixed matter click must not move cursor (y=%d)", y)
	}
}

// TestSidebar_ClickBelowLastRowIgnored verifies a click past the last row is
// a no-op.
func TestSidebar_ClickBelowLastRowIgnored(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews()) // 5 rows
	s.Render(30, 40, true)
	act, moved := s.ClickToActivate(5+100, 40)
	require.False(t, act)
	require.False(t, moved)
}

// TestSidebar_ClickWithScrollOffset verifies the mapping accounts for the
// scroll offset: after scrolling, body line 0 maps to s.scroll.
func TestSidebar_ClickWithScrollOffset(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{
		manySessions("/proj/a", 20),
		manySessions("/proj/b", 20),
		manySessions("/proj/c", 20),
	})
	// Small body so the list scrolls: height 12 -> header 5, body 7. Several
	// workspaces make the projected rows exceed the body.
	s.Render(30, 12, true)
	// Move cursor to the bottom so scroll advances.
	for range 60 {
		s.MoveDown()
	}
	s.Render(30, 12, true)
	require.Greater(t, s.scroll, 0, "list should have scrolled")

	// Body line 0 now corresponds to row index s.scroll. Clicking it must
	// select that exact row's session (row kind session in this single-ws
	// projection where row 0 is the header, so pick a body line that lands
	// on a session row).
	header := s.fixedHeaderHeight(12)
	// Find a visible session row and its body line.
	wantIdx := -1
	for i := s.scroll; i < len(s.rows); i++ {
		if s.rows[i].kind == sidebarRowSession {
			wantIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, wantIdx, 0)
	bodyLine := wantIdx - s.scroll
	act, _ := s.ClickToActivate(header+bodyLine, 12)
	require.True(t, act)
	_, gotID, ok := s.Selected()
	require.True(t, ok)
	wantID := s.overviews[s.rows[wantIdx].wsIdx].Sessions[s.rows[wantIdx].sessIdx].ID
	require.Equal(t, wantID, gotID)
}

// TestSidebar_ClickSmallHeaderNoSummary verifies mapping at small height
// where the summary block is dropped (header = 2).
func TestSidebar_ClickSmallHeaderNoSummary(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	height := 6 // < summaryMinHeight(8): header = 2
	s.Render(30, height, true)
	require.Equal(t, 2, s.fixedHeaderHeight(height))
	// Row 1 (a1) is at localY 2+1.
	act, _ := s.ClickToActivate(2+1, height)
	require.True(t, act)
	_, id, _ := s.Selected()
	require.Equal(t, "a1", id)
}

// TestSidebar_ClickClearsSelection verifies a click clears an in-progress
// multi-select and does not enter visual mode.
func TestSidebar_ClickClearsSelection(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.Render(30, 40, true)
	s.ToggleVisualMode() // visual + a1 selected
	s.MoveDown()
	require.NotEmpty(t, s.SelectedSessionIDs())

	s.ClickToActivate(5+1, 40) // click a1
	require.Empty(t, s.SelectedSessionIDs(), "click clears multi-selection")
	require.False(t, s.VisualMode(), "click does not enter visual mode")
}

// TestHandleLeftSidebarClick_RectGating verifies handleLeftSidebarClick only
// consumes clicks inside the sidebar rect, and translates screen-Y through
// the rect origin. A header click inside the rect is consumed (handled) but
// returns no command; a click outside the rect is not handled (falls
// through to chat/other routers).
func TestHandleLeftSidebarClick_RectGating(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	rect := image.Rect(0, 3, 30, 43) // origin at y=3, height 40
	s.Render(rect.Dx(), rect.Dy(), true)

	m := &UI{
		keyMap:             DefaultKeyMap(),
		leftSidebar:        s,
		leftSidebarVisible: true,
	}
	m.layout.leftSidebar = rect

	// Click outside the rect (to the right) -> not handled.
	_, handled := m.handleLeftSidebarClick(tea.MouseClickMsg{X: 100, Y: 10})
	require.False(t, handled, "click outside sidebar rect must not be handled")

	// Click on the first workspace header row: rect origin y=3 + header(5) +
	// row 0 => screen Y = 3+5+0 = 8. Consumed, no command, focus set.
	cmd, handled := m.handleLeftSidebarClick(tea.MouseClickMsg{X: 2, Y: 3 + 5 + 0})
	require.True(t, handled, "header click inside rect is consumed")
	require.Nil(t, cmd, "header click has no activate command")
	require.Equal(t, uiFocusLeftSidebar, m.focus, "click focuses the sidebar")
}

// TestHandleLeftSidebarClick_HiddenIgnored verifies clicks are ignored when
// the sidebar is not visible.
func TestHandleLeftSidebarClick_HiddenIgnored(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s, leftSidebarVisible: false}
	m.layout.leftSidebar = image.Rect(0, 0, 30, 40)
	_, handled := m.handleLeftSidebarClick(tea.MouseClickMsg{X: 2, Y: 6})
	require.False(t, handled)
}

// inboxOverviews returns a two-workspace fixture with a mix of statuses for
// exercising the flat inbox projection.
func inboxOverviews() []proto.WorkspaceOverview {
	return []proto.WorkspaceOverview{
		{
			Root:     "/home/user/alpha",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "a1", Title: "a-read-old", UpdatedAt: 10},
				{ID: "a2", Title: "a-busy", IsBusy: true, UpdatedAt: 50},
				{ID: "a3", Title: "a-unread", Unread: true, UpdatedAt: 20},
			},
		},
		{
			Root:     "/home/user/beta",
			Attached: false,
			Sessions: []proto.SessionOverview{
				{ID: "b1", Title: "b-unread-new", Unread: true, UpdatedAt: 90},
				{ID: "b2", Title: "b-busy-new", IsBusy: true, UpdatedAt: 99},
				{ID: "b3", Title: "b-read-new", UpdatedAt: 80},
			},
		},
	}
}

// sessionOrder returns the session IDs of the session rows in row order,
// ignoring header/section rows.
func sessionOrder(s *SessionsSidebar) []string {
	var ids []string
	for _, r := range s.rows {
		if r.kind == sidebarRowSession {
			ids = append(ids, s.overviews[r.wsIdx].Sessions[r.sessIdx].ID)
		}
	}
	return ids
}

// TestSidebar_InboxProjection verifies the inbox mode builds a flat,
// status-sectioned list across workspaces: Running, then Unread, then Read,
// each ordered by UpdatedAt desc, with no workspace header rows.
func TestSidebar_InboxProjection(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(inboxOverviews())
	s.ToggleInbox()
	require.True(t, s.InboxMode())

	// No workspace headers in inbox.
	for _, r := range s.rows {
		require.NotEqual(t, sidebarRowWorkspace, r.kind, "inbox must not emit workspace headers")
		require.NotEqual(t, sidebarRowOverflow, r.kind, "inbox must not emit overflow rows")
	}

	// Section headers in order.
	var sections []string
	for _, r := range s.rows {
		if r.kind == sidebarRowSection {
			sections = append(sections, r.label)
		}
	}
	require.Equal(t, []string{"Running", "Unread", "Read"}, sections)

	// Flat session order: Running (busy) by UpdatedAt desc (b2=99, a2=50),
	// then Unread (b1=90, a3=20), then Read (b3=80, a1=10).
	require.Equal(t, []string{"b2", "a2", "b1", "a3", "b3", "a1"}, sessionOrder(s))
}

// TestSidebar_InboxWorkspaceTag verifies inbox rows render the workspace
// basename tag and grouped mode does not.
func TestSidebar_InboxWorkspaceTag(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(inboxOverviews())

	grouped := s.Render(30, 40, true)
	require.Contains(t, grouped, "Sessions", "grouped mode title")

	s.ToggleInbox()
	out := s.Render(30, 40, true)
	require.Contains(t, out, "alpha", "inbox rows carry a workspace tag")
	require.Contains(t, out, "beta")
	require.Contains(t, out, "Inbox", "title reflects inbox mode")
	require.NotContains(t, out, "Sessions", "inbox title is Inbox, not Sessions")
}

// TestSidebar_InboxToggleAndPersistence verifies toggling flips modes, keeps
// the cursor on the same session, and that the mode persists (it is a field
// on the long-lived sidebar; SetOverviews does not reset it).
func TestSidebar_InboxToggleAndPersistence(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(inboxOverviews())
	require.False(t, s.InboxMode())

	// Put the cursor on a specific session, then toggle to inbox.
	s.FocusSessionID("a3")
	_, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "a3", id)

	s.ToggleInbox()
	require.True(t, s.InboxMode())
	_, id, ok = s.Selected()
	require.True(t, ok)
	require.Equal(t, "a3", id, "cursor stays on the same session across toggle")

	// A refresh keeps inbox mode (persistence across data changes / reopen).
	s.SetOverviews(inboxOverviews())
	require.True(t, s.InboxMode(), "inbox mode persists across SetOverviews")

	// Toggle back.
	s.ToggleInbox()
	require.False(t, s.InboxMode())
}

// TestSidebar_InboxNavAndRowIndexIntegrity verifies j/k navigation skips
// section headers and that sessionIDAt/rowForSessionID/Selected agree with
// the flat projection at every session row.
func TestSidebar_InboxNavAndRowIndexIntegrity(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(inboxOverviews())
	s.ToggleInbox()

	// Cursor must start on a session row, never a section header.
	require.True(t, s.selectableRow(s.cursor))

	// Walk every row with MoveDown; the cursor must only ever land on
	// selectable rows, and index math must be consistent.
	s.MoveTop()
	visited := map[string]bool{}
	for {
		require.True(t, s.selectableRow(s.cursor), "cursor never rests on a section header")
		id, ok := s.sessionIDAt(s.cursor)
		require.True(t, ok)
		require.Equal(t, s.cursor, s.rowForSessionID(id), "rowForSessionID round-trips")
		_, selID, selOK := s.Selected()
		require.True(t, selOK)
		require.Equal(t, id, selID)
		visited[id] = true
		before := s.cursor
		s.MoveDown()
		if s.cursor == before {
			break
		}
	}
	require.Len(t, visited, 6, "all six sessions reachable by navigation")
}

// TestSidebar_InboxCrossWorkspaceActivation verifies a foreign-workspace row
// in inbox reports its own root via Selected (so activation routes through
// switchWorkspaceAndLoad), while an attached-workspace row reports attached.
func TestSidebar_InboxCrossWorkspaceActivation(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(inboxOverviews())
	s.ToggleInbox()

	// b1 lives in the non-attached /home/user/beta workspace.
	s.FocusSessionID("b1")
	root, id, ok := s.Selected()
	require.True(t, ok)
	require.Equal(t, "b1", id)
	require.Equal(t, "/home/user/beta", root, "foreign row carries its own root")
	require.False(t, s.SelectedWorkspaceAttached(), "beta is not attached")

	// a2 lives in the attached /home/user/alpha workspace.
	s.FocusSessionID("a2")
	root, id, ok = s.Selected()
	require.True(t, ok)
	require.Equal(t, "a2", id)
	require.Equal(t, "/home/user/alpha", root)
	require.True(t, s.SelectedWorkspaceAttached(), "alpha is attached")
}

// TestSidebar_InboxEmptySectionsOmitted verifies sections with no members
// are not emitted (so filtering/hide-empty composes).
func TestSidebar_InboxEmptySectionsOmitted(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	// Only unread sessions: Running and Read sections must be absent.
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "a1", Title: "one", Unread: true, UpdatedAt: 2},
			{ID: "a2", Title: "two", Unread: true, UpdatedAt: 1},
		},
	}})
	s.ToggleInbox()
	var sections []string
	for _, r := range s.rows {
		if r.kind == sidebarRowSection {
			sections = append(sections, r.label)
		}
	}
	require.Equal(t, []string{"Unread"}, sections)
	require.Equal(t, []string{"a1", "a2"}, sessionOrder(s))
}

// TestSidebar_InboxLongWorkspaceTagNoOverflow verifies a long workspace
// basename is truncated so no inbox row exceeds the sidebar width.
func TestSidebar_InboxLongWorkspaceTagNoOverflow(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/home/user/an-extremely-long-workspace-directory-name-that-overflows",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "s1", Title: "A session with a fairly long title too", Unread: true, UpdatedAt: 2},
			{ID: "s2", Title: "second", IsBusy: true, UpdatedAt: 1},
		},
	}})
	s.ToggleInbox()

	const width = 30
	out := s.Render(width, 40, true)
	for _, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), width,
			"inbox row must not exceed sidebar width: %q", line)
	}
}

// searchOverviews returns a fixture spanning two workspaces with varied
// titles/animals for exercising the "/" filter.
func searchOverviews() []proto.WorkspaceOverview {
	return []proto.WorkspaceOverview{
		{
			Root:     "/home/user/backend",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "b1", Title: "Fix login bug", Animal: "otter", UpdatedAt: 30},
				{ID: "b2", Title: "Refactor cache", Animal: "tiger", UpdatedAt: 20},
			},
		},
		{
			Root:     "/home/user/frontend",
			Attached: false,
			Sessions: []proto.SessionOverview{
				{ID: "f1", Title: "Login form styling", Animal: "eel", UpdatedAt: 25},
				{ID: "f2", Title: "Dark mode", Animal: "whale", UpdatedAt: 15},
			},
		},
	}
}

// typeQuery drives the sidebar's filter input as if the user typed each rune.
func typeQuery(s *SessionsSidebar, q string) {
	for _, r := range q {
		s.HandleSearchKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// TestSidebar_SearchTitleAndSubtextMatch verifies case-insensitive substring
// matching over title AND subtext (workspace basename + animal).
func TestSidebar_SearchTitleAndSubtextMatch(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())

	// Title match, case-insensitive: "login" hits b1 ("Fix login bug") and
	// f1 ("Login form styling").
	s.EnterSearch()
	typeQuery(s, "LOGIN")
	require.ElementsMatch(t, []string{"b1", "f1"}, sessionOrder(s))
	s.ExitSearch()

	// Subtext match by workspace basename: "frontend" hits both f-sessions.
	s.EnterSearch()
	typeQuery(s, "frontend")
	require.ElementsMatch(t, []string{"f1", "f2"}, sessionOrder(s))
	s.ExitSearch()

	// Subtext match by animal: "tiger" hits only b2.
	s.EnterSearch()
	typeQuery(s, "tiger")
	require.Equal(t, []string{"b2"}, sessionOrder(s))
}

// TestSidebar_SearchEscRestores verifies esc exits search and restores the
// full unfiltered list.
func TestSidebar_SearchEscRestores(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())
	full := sessionOrder(s)

	s.EnterSearch()
	typeQuery(s, "dark")
	require.Equal(t, []string{"f2"}, sessionOrder(s))
	require.True(t, s.Searching())

	s.ExitSearch()
	require.False(t, s.Searching())
	require.ElementsMatch(t, full, sessionOrder(s), "full list restored")
}

// TestSidebar_SearchNavAndRowIndexOnFilteredSet verifies navigation stays on
// selectable rows and row-index round-trips on the filtered projection.
func TestSidebar_SearchNavAndRowIndexOnFilteredSet(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())

	s.EnterSearch()
	typeQuery(s, "login") // b1 + f1 across two workspaces
	require.True(t, s.selectableRow(s.cursor))

	s.MoveTop()
	seen := map[string]bool{}
	for {
		require.True(t, s.selectableRow(s.cursor))
		id, ok := s.sessionIDAt(s.cursor)
		require.True(t, ok)
		require.Equal(t, s.cursor, s.rowForSessionID(id))
		seen[id] = true
		before := s.cursor
		s.MoveDown()
		if s.cursor == before {
			break
		}
	}
	require.Equal(t, map[string]bool{"b1": true, "f1": true}, seen)
}

// TestSidebar_SearchAllFilteredOutHidesHeaders verifies that, in BOTH
// grouped and inbox modes, a query matching nothing yields no rows at all
// (headers/sections are omitted when their sessions filter empty).
func TestSidebar_SearchAllFilteredOutHidesHeaders(t *testing.T) {
	t.Parallel()

	// Grouped mode.
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())
	s.EnterSearch()
	typeQuery(s, "zzzznomatch")
	require.Empty(t, s.rows, "grouped: no headers when everything filters out")
	_, _, ok := s.Selected()
	require.False(t, ok)

	// Inbox mode.
	s2 := newTestSidebar(t)
	s2.SetOverviews(searchOverviews())
	s2.ToggleInbox()
	s2.EnterSearch()
	typeQuery(s2, "zzzznomatch")
	require.Empty(t, s2.rows, "inbox: no sections when everything filters out")
}

// TestSidebar_SearchComposesWithInboxSections verifies filtering inside inbox
// mode drops sections that filter empty and keeps matching ones.
func TestSidebar_SearchComposesWithInboxSections(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/x",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "run", Title: "alpha running", IsBusy: true, UpdatedAt: 3},
			{ID: "unr", Title: "beta unread", Unread: true, UpdatedAt: 2},
			{ID: "red", Title: "alpha read", UpdatedAt: 1},
		},
	}})
	s.ToggleInbox()
	s.EnterSearch()
	typeQuery(s, "alpha") // matches the running + read rows, not the unread one

	var sections []string
	for _, r := range s.rows {
		if r.kind == sidebarRowSection {
			sections = append(sections, r.label)
		}
	}
	require.Equal(t, []string{"Running", "Read"}, sections, "Unread section drops out (no match)")
	require.Equal(t, []string{"run", "red"}, sessionOrder(s))
}

// TestSidebar_SearchOnlyWhenFocused verifies "/" enters search only through
// the focused-sidebar key handler and not when the sidebar is unfocused
// (the editor owns "/" in that case). The model gates handleLeftSidebarKey
// behind (leftSidebarVisible && focus==uiFocusLeftSidebar). Uses an empty
// sidebar so the preview path (which needs a workspace on com) is a no-op.
func TestSidebar_SearchOnlyWhenFocused(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t) // no overviews: Selected() is false, preview no-ops
	slash := tea.KeyPressMsg{Code: '/', Text: "/"}

	// Unfocused: the routing gate is false, so the handler is never
	// invoked and search cannot start.
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s, leftSidebarVisible: true, focus: uiFocusEditor}
	if m.leftSidebarVisible && m.focus == uiFocusLeftSidebar {
		m.handleLeftSidebarKey(slash)
	}
	require.False(t, s.Searching(), "unfocused sidebar must not enter search")

	// Focused: "/" routes through the handler into search.
	m.focus = uiFocusLeftSidebar
	_, consumed := m.handleLeftSidebarKey(slash)
	require.True(t, consumed)
	require.True(t, s.Searching(), "focused sidebar enters search on /")
}

// TestSidebar_SearchSecondSlashIsLiteral verifies a second "/" while already
// searching is routed to the filter input as literal text, not a re-trigger.
func TestSidebar_SearchSecondSlashIsLiteral(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t) // empty: preview path is a no-op
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s, leftSidebarVisible: true, focus: uiFocusLeftSidebar}
	slash := tea.KeyPressMsg{Code: '/', Text: "/"}

	m.handleLeftSidebarKey(slash) // enter search
	require.True(t, s.Searching())
	m.handleLeftSidebarKey(slash) // second "/" -> literal query char
	require.True(t, s.Searching(), "still searching")
	require.Equal(t, "/", s.searchInput.Value(), "second / typed into the query")
}

// TestSidebar_SearchLetsGlobalKeysFallThrough verifies that a non-editing
// key (e.g. ctrl+s) pressed while searching is reported UNCONSUMED so it
// falls through to the global handlers, instead of being swallowed by the
// filter input.
func TestSidebar_SearchLetsGlobalKeysFallThrough(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	m := &UI{keyMap: DefaultKeyMap(), leftSidebar: s, leftSidebarVisible: true, focus: uiFocusLeftSidebar}
	m.handleLeftSidebarKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	require.True(t, s.Searching())

	// ctrl+s does not edit the query, so it must not be consumed.
	_, consumed := m.handleLeftSidebarKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.False(t, consumed, "non-editing key falls through while searching")
	require.True(t, s.Searching(), "and search stays active")

	// A printable rune DOES edit the query and is consumed.
	_, consumed = m.handleLeftSidebarKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, consumed, "typed char is consumed by the filter")
	require.Equal(t, "x", s.searchInput.Value())

	// Cursor-motion keys (left/right) edit the input WITHOUT changing the
	// value; they must still be consumed so they don't fall through and
	// double-fire a global binding (e.g. Chat.PillLeft/Right).
	_, consumed = m.handleLeftSidebarKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.True(t, consumed, "left arrow consumed (input cursor motion)")
	_, consumed = m.handleLeftSidebarKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, consumed, "right arrow consumed (input cursor motion)")
	require.Equal(t, "x", s.searchInput.Value(), "cursor motion leaves query intact")
}

// TestSidebar_SearchClearsSelection verifies entering search drops an
// in-progress multi-selection.
func TestSidebar_SearchClearsSelection(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())
	s.ToggleSelected()
	require.Positive(t, s.SelectionCount())
	s.EnterSearch()
	require.Zero(t, s.SelectionCount(), "entering search clears multi-select")
}

// TestSidebar_SearchNoDoubledPrompt verifies the rendered filter line shows a
// single "/" prompt (the textinput's default "> " prompt is cleared).
func TestSidebar_SearchNoDoubledPrompt(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())
	s.EnterSearch()
	typeQuery(s, "log")
	out := ansi.Strip(s.Render(30, 20, true))
	require.Contains(t, out, "/log")
	require.NotContains(t, out, "/> ", "no doubled prompt from the textinput default")
}

// TestSidebar_SearchFiltersSummaryCounts verifies the summary ready/working/
// total block reflects the FILTERED set while a search is active, so the
// summary agrees with the visible rows.
func TestSidebar_SearchFiltersSummaryCounts(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/x",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "a", Title: "alpha busy", IsBusy: true, UpdatedAt: 4},
			{ID: "b", Title: "alpha unread", Unread: true, UpdatedAt: 3},
			{ID: "c", Title: "beta unread", Unread: true, UpdatedAt: 2},
			{ID: "d", Title: "beta read", UpdatedAt: 1},
		},
	}})
	// Unfiltered: 4 total, 1 working, 2 ready.
	require.Equal(t, SessionCounts{Ready: 2, Working: 1, Total: 4}, s.SessionCounts())

	// Filter to "alpha": a (busy) + b (unread) only.
	s.EnterSearch()
	typeQuery(s, "alpha")
	require.Equal(t, SessionCounts{Ready: 1, Working: 1, Total: 2}, s.SessionCounts())

	// Exiting restores global totals.
	s.ExitSearch()
	require.Equal(t, SessionCounts{Ready: 2, Working: 1, Total: 4}, s.SessionCounts())
}

// TestSidebar_SearchExitsOnFocusAwayClick verifies a click that moves focus
// off the (still-visible) sidebar exits search and restores the full list,
// via the exitLeftSidebarSearchOnBlur guard used by handleClickFocus.
func TestSidebar_SearchExitsOnFocusAwayClick(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(searchOverviews())
	s.EnterSearch()
	typeQuery(s, "dark")
	require.True(t, s.Searching())

	m := &UI{leftSidebar: s}
	// Simulate a click that moved focus to the editor: wasFocused=true,
	// focus now off the sidebar -> the guard exits search.
	m.focus = uiFocusEditor
	m.exitLeftSidebarSearchOnBlur(true)
	require.False(t, s.Searching(), "search exited when focus left the sidebar")

	// Guard is a no-op when focus stays on the sidebar.
	s.EnterSearch()
	m.focus = uiFocusLeftSidebar
	m.exitLeftSidebarSearchOnBlur(true)
	require.True(t, s.Searching(), "search preserved when focus stays on sidebar")
}
