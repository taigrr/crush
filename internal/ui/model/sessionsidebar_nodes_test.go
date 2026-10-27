package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func clusterOverviews() []proto.WorkspaceOverview {
	return []proto.WorkspaceOverview{
		{Root: "/proj/a", Attached: true, Sessions: []proto.SessionOverview{{ID: "a1", Title: "Local work"}}},
		{
			Root:     "crush+node://n1/proj/m",
			NodeID:   "n1",
			NodeName: "mini1",
			Sessions: []proto.SessionOverview{{ID: "m1", Title: "Offloaded build"}},
		},
	}
}

func TestSidebar_RemoteSessionsAreTaggedAndFilterable(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews(clusterOverviews())
	require.True(t, s.HasRemoteNodes())

	out := ansi.Strip(s.Render(60, 30, true))
	require.Contains(t, out, "@mini1 /proj/m")
	require.NotContains(t, out, "crush+node://")

	ids := func() []string {
		var got []string
		for i := range s.rows {
			if id, ok := s.sessionIDAt(i); ok {
				got = append(got, id)
			}
		}
		return got
	}
	require.ElementsMatch(t, []string{"a1", "m1"}, ids())

	s.CycleNodeFilter() // this machine
	require.Equal(t, []string{"a1"}, ids())
	require.Contains(t, s.title(), "this machine")

	s.CycleNodeFilter() // mini1
	require.Equal(t, []string{"m1"}, ids())
	require.Contains(t, s.title(), "@mini1")

	s.CycleNodeFilter() // back to all
	require.Len(t, ids(), 2)

	// Text search matches the node name too.
	s.EnterSearch()
	s.searchInput.SetValue("mini1")
	s.rebuildRows()
	require.Equal(t, []string{"m1"}, ids())
}

func TestSidebar_OfflineReplicaIsLabelled(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	s.SetOverviews([]proto.WorkspaceOverview{{
		Root:     "/proj/a",
		Sessions: []proto.SessionOverview{{ID: "r1", Title: "Long build", OwnerName: "mini1", ReadOnly: true}},
	}})
	out := ansi.Strip(s.Render(70, 20, true))
	require.Contains(t, out, "@mini1 offline")
}
