package model

import (
	"strings"
	"testing"

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
		require.Equal(t, []string{"other", "orch", "worker", "stray"}, ids)
		require.Equal(t, []int{0, 0, 1, 0}, depths)

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
		require.Equal(t, "other", id)
		s.MoveDown()
		_, id, _ = s.Selected()
		require.Equal(t, "orch", id)
		s.MoveDown()
		_, id, _ = s.Selected()
		require.Equal(t, "worker", id)
	})
}
