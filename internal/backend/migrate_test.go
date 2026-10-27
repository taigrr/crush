package backend

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
)

func node(id string, cpu, mem float64) cluster.NodeDashboard {
	return cluster.NodeDashboard{Member: cluster.Member{NodeID: id, Name: id}, Online: true, Stats: &cluster.NodeStats{CPUPercent: cpu, MemPercent: mem}}
}

func TestPlaceRootsCapacityPrefersFreeNodesAndSpreads(t *testing.T) {
	t.Parallel()
	idle, busy := node("idle", 5, 20), node("busy", 90, 85)
	roots := []string{"/big", "/a", "/b"}
	sizes := map[string]int{"/big": 3, "/a": 1, "/b": 1}
	got, err := placeRoots(roots, sizes, []cluster.NodeDashboard{busy, idle}, StrategyCapacity)
	require.NoError(t, err)
	require.Equal(t, "idle", got["/big"].Member.NodeID, "the biggest project goes to the freest node")

	// With two equally free nodes, load spreads projects across both.
	m1, m2 := node("m1", 10, 30), node("m2", 10, 30)
	got, err = placeRoots(roots, sizes, []cluster.NodeDashboard{m1, m2}, StrategyCapacity)
	require.NoError(t, err)
	require.NotEqual(t, got["/big"].Member.NodeID, got["/a"].Member.NodeID)

	got, err = placeRoots(roots, sizes, []cluster.NodeDashboard{m1, m2}, StrategyEven)
	require.NoError(t, err)
	require.Equal(t, "m1", got["/big"].Member.NodeID)
	require.Equal(t, "m2", got["/a"].Member.NodeID)
	require.Equal(t, "m1", got["/b"].Member.NodeID)

	_, err = placeRoots(roots, sizes, []cluster.NodeDashboard{m1}, "bogus")
	require.Error(t, err)
}
