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
