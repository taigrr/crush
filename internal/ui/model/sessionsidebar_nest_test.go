package model

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/styles"
)

func TestNestSpawned(t *testing.T) {
	t.Parallel()

	t.Run("no lineage keeps order flat", func(t *testing.T) {
		t.Parallel()
		got := nestSpawned([]string{"a", "b", "c"}, []string{"", "", ""})
		require.Equal(t, []nestedRef{{0, 0}, {1, 0}, {2, 0}}, got)
	})

	t.Run("worker moves under its spawner", func(t *testing.T) {
		t.Parallel()
		// Sorted by recency: worker w1 newest, then unrelated x, then
		// orchestrator o, then worker w2.
		ids := []string{"w1", "x", "o", "w2"}
		spawners := []string{"o", "", "", "o"}
		got := nestSpawned(ids, spawners)
		require.Equal(t, []nestedRef{
			{1, 0},         // x
			{2, 0},         // o
			{0, 1}, {3, 1}, // w1, w2 under o in their original relative order
		}, got)
	})

	t.Run("absent spawner leaves the worker flat", func(t *testing.T) {
		t.Parallel()
		got := nestSpawned([]string{"w", "x"}, []string{"elsewhere", ""})
		require.Equal(t, []nestedRef{{0, 0}, {1, 0}}, got)
	})

	t.Run("grandchildren nest deeper", func(t *testing.T) {
		t.Parallel()
		got := nestSpawned([]string{"o", "w", "ww"}, []string{"", "o", "w"})
		require.Equal(t, []nestedRef{{0, 0}, {1, 1}, {2, 2}}, got)
	})

	t.Run("cycles do not drop sessions", func(t *testing.T) {
		t.Parallel()
		got := nestSpawned([]string{"a", "b", "self"}, []string{"b", "a", "self"})
		require.Len(t, got, 3)
		seen := map[int]bool{}
		for _, r := range got {
			seen[r.idx] = true
		}
		require.Len(t, seen, 3, "every input index must appear exactly once")
	})
}

func sidebarSessionOrder(s *SessionsSidebar) (ids []string, depths []int) {
	for _, r := range s.rows {
		if r.kind != sidebarRowSession {
			continue
		}
		ids = append(ids, s.overviews[r.wsIdx].Sessions[r.sessIdx].ID)
		depths = append(depths, r.depth)
	}
	return ids, depths
}

// In the grouped view a swarm-spawned session renders under its spawner
// with a tree elbow; in the inbox view it does so only when both landed
// in the same status section.
func TestSidebar_NestsSpawnedSessions(t *testing.T) {
	t.Parallel()

	overviews := func() []proto.WorkspaceOverview {
		return []proto.WorkspaceOverview{{
			Root:     "/proj/a",
			Attached: true,
			Sessions: []proto.SessionOverview{
				{ID: "worker", Title: "Worker", UpdatedAt: 30, SpawnedBySessionID: "orch"},
				{ID: "other", Title: "Other", UpdatedAt: 20},
				{ID: "orch", Title: "Orchestrator", UpdatedAt: 10},
				{ID: "stray", Title: "Stray", UpdatedAt: 5, SpawnedBySessionID: "gone"},
			},
		}}
	}

	t.Run("grouped", func(t *testing.T) {
		t.Parallel()
		s := newTestSidebar(t)
		s.SetOverviews(overviews())
		s.Render(40, 40, true)
		ids, depths := sidebarSessionOrder(s)
		// The orchestrator's subtree bubbles up because its worker is the
		// most recently updated session; the spawner stays first within it.
		require.Equal(t, []string{"orch", "worker", "other", "stray"}, ids)
		require.Equal(t, []int{0, 1, 0, 0}, depths)

		out := ansi.Strip(strings.Join(s.renderRows(40, true), "\n"))
		require.Contains(t, out, styles.TreeElbowIcon+" ")
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Worker") {
				require.Contains(t, line, styles.TreeElbowIcon, "worker row must carry the tree elbow")
			}
			if strings.Contains(line, "Orchestrator") || strings.Contains(line, "Stray") {
				require.NotContains(t, line, styles.TreeElbowIcon)
			}
		}
	})

	t.Run("inbox nests only within a section", func(t *testing.T) {
		t.Parallel()
		s := newTestSidebar(t)
		s.mode = sidebarModeInbox
		ov := overviews()
		s.SetOverviews(ov)
		s.Render(40, 40, true)
		ids, depths := sidebarSessionOrder(s)
		require.Equal(t, []string{"other", "orch", "worker", "stray"}, ids)
		require.Equal(t, []int{0, 0, 1, 0}, depths)

		// Worker becomes busy: it moves to Running while the orchestrator
		// stays in Read, so it renders flat there.
		ov = overviews()
		ov[0].Sessions[0].IsBusy = true
		s.SetOverviews(ov)
		ids, depths = sidebarSessionOrder(s)
		require.Equal(t, []string{"worker", "other", "orch", "stray"}, ids)
		require.Equal(t, []int{0, 0, 0, 0}, depths)
	})

	t.Run("navigation and selection follow the nested order", func(t *testing.T) {
		t.Parallel()
		s := newTestSidebar(t)
		s.SetOverviews(overviews())
		_, id, ok := s.Selected()
		require.True(t, ok)
		require.Equal(t, "orch", id)
		s.MoveDown()
		_, id, _ = s.Selected()
		require.Equal(t, "worker", id)
		s.MoveDown()
		_, id, _ = s.Selected()
		require.Equal(t, "other", id)
	})
}

// A busy worker nested under an idle spawner must not sink below idle
// top-level sessions: the whole subtree sorts to the top of its group.
func TestSidebar_BusyNestedWorkerBubblesSubtree(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Attached: true,
		Sessions: []proto.SessionOverview{
			{ID: "unread", Title: "Unread", UpdatedAt: 90, Unread: true},
			{ID: "recent", Title: "Recent", UpdatedAt: 80},
			{ID: "orch", Title: "Orchestrator", UpdatedAt: 10},
			{ID: "worker", Title: "Worker", UpdatedAt: 5, IsBusy: true, SpawnedBySessionID: "orch"},
		},
	}})
	s.Render(40, 40, true)
	ids, depths := sidebarSessionOrder(s)
	require.Equal(t, []string{"orch", "worker", "unread", "recent"}, ids)
	require.Equal(t, []int{0, 1, 0, 0}, depths)

	// A pending prompt outranks busy: once the recent session blocks on a
	// prompt it moves above the busy subtree without waiting for a refresh.
	s.SetPendingSessions(map[string]bool{"recent": true})
	ids, _ = sidebarSessionOrder(s)
	require.Equal(t, []string{"recent", "orch", "worker", "unread"}, ids)

	// The active session's subtree is still pinned first.
	s.SetPendingSessions(nil)
	s.SetActiveSession("unread")
	ids, _ = sidebarSessionOrder(s)
	require.Equal(t, []string{"unread", "orch", "worker", "recent"}, ids)
}

// The per-workspace cap must never push a busy session (or the spawner
// it nests under) into the "… N more" overflow row, even when the busy
// rows alone exceed the cap.
func TestSidebar_BusyRowsNeverOverflow(t *testing.T) {
	t.Parallel()

	build := func(busy int) proto.WorkspaceOverview {
		ws := proto.WorkspaceOverview{Root: "/proj/a", Attached: true}
		for i := range 20 {
			ws.Sessions = append(ws.Sessions, proto.SessionOverview{
				ID:        fmt.Sprintf("idle-%02d", i),
				Title:     "Idle",
				UpdatedAt: int64(1000 - i),
			})
		}
		ws.Sessions = append(ws.Sessions, proto.SessionOverview{ID: "orch", Title: "Orchestrator", UpdatedAt: 1})
		for i := range busy {
			ws.Sessions = append(ws.Sessions, proto.SessionOverview{
				ID:                 fmt.Sprintf("busy-%02d", i),
				Title:              "Busy",
				UpdatedAt:          int64(i),
				IsBusy:             true,
				SpawnedBySessionID: "orch",
			})
		}
		return ws
	}
	shownIDs := func(s *SessionsSidebar) map[string]bool {
		ids, _ := sidebarSessionOrder(s)
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	overflowRemaining := func(s *SessionsSidebar) int {
		for _, r := range s.rows {
			if r.kind == sidebarRowOverflow {
				return r.remaining
			}
		}
		return 0
	}

	t.Run("busy under cap fills remaining slots", func(t *testing.T) {
		t.Parallel()
		s := newTestSidebar(t)
		s.SetOverviews([]proto.WorkspaceOverview{build(2)})
		// Tight body: cap is the floor of minSessionsPerWorkspace (5).
		s.Render(30, 9, true)
		ids, depths := sidebarSessionOrder(s)
		require.Len(t, ids, minSessionsPerWorkspace)
		require.Equal(t, []string{"orch", "busy-01", "busy-00", "idle-00", "idle-01"}, ids)
		require.Equal(t, []int{0, 1, 1, 0, 0}, depths)
		require.Equal(t, 23-minSessionsPerWorkspace, overflowRemaining(s))
		require.Equal(t, SessionCounts{Working: 2, Total: 23}, s.SessionCounts())
	})

	t.Run("busy over cap are all shown", func(t *testing.T) {
		t.Parallel()
		s := newTestSidebar(t)
		s.SetOverviews([]proto.WorkspaceOverview{build(7)})
		s.Render(30, 9, true)
		shown := shownIDs(s)
		require.True(t, shown["orch"], "spawner needed to nest busy workers must be shown")
		for i := range 7 {
			require.True(t, shown[fmt.Sprintf("busy-%02d", i)], "busy worker %d must not be in overflow", i)
		}
		ids, _ := sidebarSessionOrder(s)
		require.Len(t, ids, 8, "soft cap: all must-show rows, no idle filler")
		require.Equal(t, 28-8, overflowRemaining(s))
		require.Equal(t, SessionCounts{Working: 7, Total: 28}, s.SessionCounts())
	})

	t.Run("busy workers under separate idle spawners past the cap", func(t *testing.T) {
		t.Parallel()
		ws := proto.WorkspaceOverview{Root: "/proj/a", Attached: true}
		for i := range 20 {
			ws.Sessions = append(ws.Sessions, proto.SessionOverview{
				ID:        fmt.Sprintf("idle-%02d", i),
				Title:     "Idle",
				UpdatedAt: int64(1000 - i),
			})
		}
		// Both spawners and both workers are the oldest sessions, so by
		// recency alone all four would sit past the cap of 5.
		ws.Sessions = append(ws.Sessions,
			proto.SessionOverview{ID: "orch-a", Title: "Orch A", UpdatedAt: 4},
			proto.SessionOverview{ID: "orch-b", Title: "Orch B", UpdatedAt: 3},
			proto.SessionOverview{ID: "busy-a", Title: "Busy A", UpdatedAt: 2, IsBusy: true, SpawnedBySessionID: "orch-a"},
			proto.SessionOverview{ID: "busy-b", Title: "Busy B", UpdatedAt: 1, IsBusy: true, SpawnedBySessionID: "orch-b"},
		)
		s := newTestSidebar(t)
		s.SetOverviews([]proto.WorkspaceOverview{ws})
		s.Render(30, 9, true)
		ids, depths := sidebarSessionOrder(s)
		require.Equal(t, []string{"orch-a", "busy-a", "orch-b", "busy-b", "idle-00"}, ids)
		require.Equal(t, []int{0, 1, 0, 1, 0}, depths)
		require.Equal(t, 24-5, overflowRemaining(s))
		require.Equal(t, SessionCounts{Working: 2, Total: 24}, s.SessionCounts())

		// The expanded workspace lists everything and keeps the same order
		// at the top; the toggle row becomes "show less".
		s.MoveBottom()
		require.True(t, s.ToggleOverflowUnderCursor())
		ids, _ = sidebarSessionOrder(s)
		require.Len(t, ids, 24)
		require.Equal(t, []string{"orch-a", "busy-a", "orch-b", "busy-b"}, ids[:4])
		require.Zero(t, overflowRemaining(s))
	})
}

// A busy row's title is tinted with the busy color and a pending row's with
// the error color, so a running session stands out beyond its 1-cell dot.
func TestSidebar_BusyTitleTakesStatusColor(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	sty := s.com.Styles
	busyFg := sty.Resource.BusyIcon.GetForeground()
	errFg := sty.Resource.ErrorIcon.GetForeground()
	nameFg := sty.Resource.Name.GetForeground()
	require.NotEqual(t, nameFg, busyFg)

	titleOf := func(fg color.Color) string {
		return lipgloss.NewStyle().Foreground(fg).Render("Title")
	}
	idle := proto.SessionOverview{ID: "idle", Title: "Title"}
	busy := proto.SessionOverview{ID: "busy", Title: "Title", IsBusy: true}

	require.Contains(t, s.renderSessionRow(sty, idle, 40, false, false, false, "", 0), titleOf(nameFg))
	require.Contains(t, s.renderSessionRow(sty, busy, 40, false, false, false, "", 0), titleOf(busyFg))

	s.SetPendingSessions(map[string]bool{"busy": true})
	require.Contains(t, s.renderSessionRow(sty, busy, 40, false, false, false, "", 0), titleOf(errFg))

	// The focused selection keeps the existing selected style.
	sel := s.renderSessionRow(sty, busy, 40, true, true, false, "", 0)
	require.NotContains(t, sel, titleOf(errFg))
	require.Contains(t, ansi.Strip(sel), "Title")
}
