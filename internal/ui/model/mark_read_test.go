package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

// TestSidebar_MarkReadSelectionScopesToCurrentWorkspace verifies the bulk
// mark-read gathering excludes sessions from other workspaces and counts
// them as skipped, while INCLUDING the active session (harmless: already
// read).
func TestSidebar_MarkReadSelectionScopesToCurrentWorkspace(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a") // b1 lives in /proj/b -> skipped
	s.SetActiveSession("a1")

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	ids, skippedWorkspace := s.MarkReadSelection()
	require.Equal(t, []string{"a1", "a2"}, ids) // active a1 included
	require.Equal(t, 1, skippedWorkspace)       // b1 in /proj/b
}

// TestSidebar_MarkReadSelectionFailsClosed verifies the filter fails closed:
// when no current root is set, nothing is markable and everything counts as
// skipped.
func TestSidebar_MarkReadSelectionFailsClosed(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	// No SetCurrentRoot -> currentRoot == "".

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	ids, skippedWorkspace := s.MarkReadSelection()
	require.Empty(t, ids)
	require.Equal(t, 3, skippedWorkspace)
}

// TestMarkSessionsReadCmd_CollectsAllFailures verifies the bulk mark-read
// attempts every id (does not abort on the first failure), reports the
// individual failures, and refreshes overviews.
func TestMarkSessionsReadCmd_CollectsAllFailures(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{
		markFailIDs: map[string]bool{"b": true},
	}
	s := styles.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &s, Workspace: ws}}

	msg := m.markSessionsReadCmd([]string{"a", "b", "c"}, 1)().(sessionsMarkedReadMsg)

	require.Equal(t, 2, msg.succeeded)
	require.Equal(t, []string{"b"}, msg.failed)
	require.Equal(t, 1, msg.skipped)
	require.Equal(t, []string{"a", "c"}, ws.markedRead)
}

// TestMarkSelectedSessionsRead_EmptyReportsInfo verifies the empty-selection
// behavior mirrors archive: an info toast, no RPC calls.
func TestMarkSelectedSessionsRead_EmptyReportsInfo(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")
	ws := &archiveStubWorkspace{}
	sty := styles.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &sty, Workspace: ws}, leftSidebar: s}

	cmd := m.markSelectedSessionsRead()
	require.NotNil(t, cmd)
	require.Empty(t, ws.markedRead, "no RPC should fire for an empty selection")
}

// TestHandleLeftSidebarKey_RMarksRead exercises the REAL key routing path so
// a mis-declared binding is caught, and confirms `r` is consumed by the
// focused sidebar.
func TestHandleLeftSidebarKey_RMarksRead(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")
	ws := &archiveStubWorkspace{}
	sty := styles.CharmtonePantera()
	m := &UI{
		keyMap:      DefaultKeyMap(),
		leftSidebar: s,
		com:         &common.Common{Styles: &sty, Workspace: ws},
	}

	_, consumed := m.handleLeftSidebarKey(tea.KeyPressMsg{Code: 'r'})
	require.True(t, consumed, "r should be consumed by the focused sidebar")
}
