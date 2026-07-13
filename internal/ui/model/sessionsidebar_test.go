package model

import (
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
