package model

import (
	"sort"
	"testing"

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
	// Render with a tight body so the cap (min 5) applies: height-2 body.
	s.Render(30, 9, true) // body = 7 -> single workspace, still capped by fit

	// With one workspace and body height 7: header(1)+20 sessions = 21 > 7,
	// so cap = max(5, 7-2)=5 sessions + overflow row.
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
// gathering excludes the currently active session.
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
	ids, skippedActive, skippedWorkspace := s.ArchivableSelection()
	require.Equal(t, []string{"a2"}, ids) // a1 active, b1 other workspace
	require.Equal(t, 1, skippedActive)    // active a1
	require.Equal(t, 1, skippedWorkspace) // b1 in /proj/b
}

// TestSidebar_ArchivableSelectionScopesToCurrentWorkspace verifies that when
// a current workspace root is set, sessions from other workspaces are
// excluded from the archivable set and counted as skipped, and the result
// is deterministically sorted.
func TestSidebar_ArchivableSelectionScopesToCurrentWorkspace(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a") // b1 lives in /proj/b -> skipped

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	ids, skippedActive, skippedWorkspace := s.ArchivableSelection()
	require.Equal(t, []string{"a1", "a2"}, ids) // only current-workspace
	require.Equal(t, 0, skippedActive)
	require.Equal(t, 1, skippedWorkspace) // b1 in /proj/b
}

// TestSidebar_ArchivableSelectionFailsClosed verifies the filter fails
// CLOSED: unknown ids (not in overviews) and every id when currentRoot is
// unset are skipped, never passed through as archivable. This prevents a
// stale/foreign id from reaching ArchiveSession (which would falsely report
// success for a nonexistent row).
func TestSidebar_ArchivableSelectionFailsClosed(t *testing.T) {
	t.Parallel()

	// currentRoot unset -> archive nothing even though sessions exist.
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.ToggleSelected() // a1
	ids, _, skippedWorkspace := s.ArchivableSelection()
	require.Empty(t, ids, "no currentRoot -> nothing archivable")
	require.Equal(t, 1, skippedWorkspace)

	// currentRoot set but the selected id is unknown (dropped by a refresh):
	// it must be skipped, not archived.
	s2 := newTestSidebar(t)
	s2.SetOverviews(sampleOverviews())
	s2.SetCurrentRoot("/proj/a")
	s2.SetSelection([]string{"ghost"}) // not present in any overview
	ids2, _, skippedWorkspace2 := s2.ArchivableSelection()
	require.Empty(t, ids2, "unknown id must not be archivable")
	require.Equal(t, 1, skippedWorkspace2)
}

// TestSidebar_ArchivableSelectionRootMatchIsExact documents/guards the
// invariant that SetCurrentRoot (from workspace.BaseDir) and the overview
// Root must use identical path normalization: a trailing-slash mismatch
// makes the current workspace's own sessions fail the root==currentRoot
// check and be silently skipped. If this test ever fails, the two sources
// have diverged and bulk archive would no-op while reporting "skipped".
func TestSidebar_ArchivableSelectionRootMatchIsExact(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a/") // trailing slash != "/proj/a"
	s.ToggleSelected()           // a1, whose Root is "/proj/a"
	ids, _, skippedWorkspace := s.ArchivableSelection()
	require.Empty(t, ids, "non-normalized root mismatch skips own sessions")
	require.Equal(t, 1, skippedWorkspace)
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
