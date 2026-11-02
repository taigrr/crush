package cluster

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func balanceNode(id string, self, online bool, cpu, mem, load1 float64, jobs ...NodeJob) NodeDashboard {
	return NodeDashboard{
		Member:    Member{NodeID: id, Name: id},
		Self:      self,
		Online:    online,
		RTTMillis: 5,
		Stats:     &NodeStats{CPUPercent: cpu, MemPercent: mem, CPUCores: 8, Load1: load1, Jobs: jobs},
	}
}

func TestPickBalanceTarget(t *testing.T) {
	t.Parallel()

	t.Run("no peers", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{balanceNode("a", true, true, 90, 90, 8)}, "/p", "s", DefaultBalanceMargin)
		require.False(t, d.Move)
		require.Contains(t, d.Reason, "no other node")
	})

	t.Run("offline peers ignored", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 90, 90, 8),
			balanceNode("b", false, false, 0, 0, 0),
		}, "/p", "s", DefaultBalanceMargin)
		require.False(t, d.Move)
	})

	t.Run("peer without stats ignored", func(t *testing.T) {
		t.Parallel()
		b := balanceNode("b", false, true, 0, 0, 0)
		b.Stats = nil
		d := PickBalanceTarget([]NodeDashboard{balanceNode("a", true, true, 90, 90, 8), b}, "/p", "s", DefaultBalanceMargin)
		require.False(t, d.Move)
	})

	t.Run("moves to least loaded", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 90, 80, 7),
			balanceNode("b", false, true, 50, 50, 4),
			balanceNode("c", false, true, 5, 20, 0.5),
		}, "/p", "s", DefaultBalanceMargin)
		require.True(t, d.Move)
		require.Equal(t, "c", d.Target.Member.NodeID)
	})

	t.Run("within margin stays", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 30, 40, 2),
			balanceNode("b", false, true, 25, 35, 1.5),
		}, "/p", "s", DefaultBalanceMargin)
		require.False(t, d.Move)
		require.Contains(t, d.Reason, "margin")
	})

	t.Run("local best stays", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 5, 10, 0.1),
			balanceNode("b", false, true, 80, 80, 6),
		}, "/p", "s", 0)
		require.False(t, d.Move)
	})

	t.Run("ties break by rtt then id", func(t *testing.T) {
		t.Parallel()
		b := balanceNode("b", false, true, 0, 0, 0)
		c := balanceNode("c", false, true, 0, 0, 0)
		d0 := balanceNode("d", false, true, 0, 0, 0)
		d0.RTTMillis = 1
		self := balanceNode("a", true, true, 100, 100, 8)
		require.Equal(t, "d", PickBalanceTarget([]NodeDashboard{self, c, b, d0}, "/p", "s", DefaultBalanceMargin).Target.Member.NodeID)
		require.Equal(t, "b", PickBalanceTarget([]NodeDashboard{self, c, b}, "/p", "s", DefaultBalanceMargin).Target.Member.NodeID)
	})

	t.Run("project stays with its running job", func(t *testing.T) {
		t.Parallel()
		job := NodeJob{SessionID: "other", WorkspaceRoot: "/p"}
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 90, 90, 8),
			balanceNode("b", false, true, 50, 50, 4, job),
			balanceNode("c", false, true, 0, 0, 0),
		}, "/p", "s", DefaultBalanceMargin)
		require.True(t, d.Move)
		require.Equal(t, "b", d.Target.Member.NodeID)

		d = PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 90, 90, 8, job),
			balanceNode("c", false, true, 0, 0, 0),
		}, "/p", "s", DefaultBalanceMargin)
		require.False(t, d.Move)
	})

	t.Run("own session not counted as project job", func(t *testing.T) {
		t.Parallel()
		own := NodeJob{SessionID: "s", WorkspaceRoot: "/p"}
		d := PickBalanceTarget([]NodeDashboard{
			balanceNode("a", true, true, 90, 90, 8, own),
			balanceNode("c", false, true, 0, 0, 0),
		}, "/p", "s", DefaultBalanceMargin)
		require.True(t, d.Move)
		require.Equal(t, "c", d.Target.Member.NodeID)
	})

	t.Run("self missing", func(t *testing.T) {
		t.Parallel()
		d := PickBalanceTarget([]NodeDashboard{balanceNode("c", false, true, 0, 0, 0)}, "/p", "s", 0)
		require.False(t, d.Move)
	})
}

func TestBalanceScoreUsesLoadAverage(t *testing.T) {
	t.Parallel()
	idle := NodeStats{CPUCores: 4, Load1: 0.1}
	busy := NodeStats{CPUCores: 4, Load1: 6}
	require.Greater(t, idle.BalanceScore(), busy.BalanceScore())
	noLoad := NodeStats{CPUPercent: 50, MemPercent: 50}
	require.InDelta(t, noLoad.FreeScore(), noLoad.BalanceScore(), 1e-9)
}
