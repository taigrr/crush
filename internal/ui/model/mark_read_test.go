package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// TestSidebar_MarkReadSelectionSpansWorkspaces verifies the bulk mark-read
// gathering INCLUDES sessions from other (detached) workspaces, each routed
// to its own workspace, and includes the active session (harmless: already
// read).
func TestSidebar_MarkReadSelectionSpansWorkspaces(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetCurrentRoot("/proj/a")
	s.SetActiveSession("a1")

	s.ToggleVisualMode()
	s.MoveDown()
	s.MoveDown() // a1,a2,b1 selected

	targets := s.MarkReadSelection()
	require.Equal(t, []SessionTarget{
		{Root: "/proj/a", ID: "a1"},
		{Root: "/proj/a", ID: "a2"},
		{Root: "/proj/b", ID: "b1"},
	}, targets)
}

// TestSidebar_MarkReadSelectionSkipsUnknown verifies an id not present in
// the overviews is silently skipped (cannot be routed to a workspace).
func TestSidebar_MarkReadSelectionSkipsUnknown(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(sampleOverviews())
	s.SetSelection([]string{"ghost"})

	targets := s.MarkReadSelection()
	require.Empty(t, targets)
}

// TestMarkSessionsReadCmd_CollectsAllFailures verifies the bulk mark-read
// attempts every target (does not abort on the first failure), reports the
// individual failures, and refreshes overviews.
func TestMarkSessionsReadCmd_CollectsAllFailures(t *testing.T) {
	t.Parallel()
	ws := &archiveStubWorkspace{
		markFailIDs: map[string]bool{"b": true},
	}
	s := themes.CharmtonePantera()
	m := &UI{com: &common.Common{Styles: &s, Workspace: ws}}

	msg := m.markSessionsReadCmd([]SessionTarget{{ID: "a"}, {ID: "b"}, {ID: "c"}})().(sessionsMarkedReadMsg)

	require.Equal(t, 2, msg.succeeded)
	require.Equal(t, []string{"b"}, msg.failed)
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
	sty := themes.CharmtonePantera()
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
	sty := themes.CharmtonePantera()
	m := &UI{
		keyMap:      DefaultKeyMap(),
		leftSidebar: s,
		com:         &common.Common{Styles: &sty, Workspace: ws},
	}

	_, consumed := m.handleLeftSidebarKey(tea.KeyPressMsg{Code: 'r'})
	require.True(t, consumed, "r should be consumed by the focused sidebar")
}
