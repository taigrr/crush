package dialog

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

func TestRenderClusterDashboard(t *testing.T) {
	t.Parallel()
	sty := themes.CharmtonePantera()
	com := &common.Common{Styles: &sty}
	rows := []cluster.NodeDashboard{
		{Member: cluster.Member{Name: "laptop"}, Self: true, Online: true, Stats: &cluster.NodeStats{CPUPercent: 50, MemPercent: 25, DiskFree: 10 << 30}},
		{
			Member: cluster.Member{Name: "mini1"}, Online: true, RTTMillis: 3, LastSeen: time.Now(),
			Stats: &cluster.NodeStats{Jobs: []cluster.NodeJob{{SessionID: "abc", Title: "Refactor auth", WorkspaceRoot: "/p"}}},
		},
		{Member: cluster.Member{Name: "mini2"}, RTTMillis: -1},
	}
	out := ansi.Strip(strings.Join(RenderClusterDashboard(rows, 100, com), "\n"))
	require.Contains(t, out, "2/3 nodes online · 1 running jobs")
	require.Contains(t, out, "laptop (this machine)")
	require.Contains(t, out, "rtt 3ms")
	require.Contains(t, out, "Refactor auth")
	require.Contains(t, out, "no heartbeat yet")
	require.Contains(t, out, "█████░░░░░  50%")
}
