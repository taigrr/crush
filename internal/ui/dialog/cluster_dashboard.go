package dialog

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/ui/common"
)

// ClusterDashboardID is the identifier for the cluster dashboard.
const ClusterDashboardID = "cluster_dashboard"

// ClusterDashboard is the cluster "eye in the sky": one card per node
// with liveness, round-trip time, CPU, memory, disk, and running jobs.
// The UI refreshes it periodically via SetRows while it is open.
type ClusterDashboard struct {
	com     *common.Common
	rows    []cluster.NodeDashboard
	err     string
	updated time.Time
	scroll  int
	keyMap  struct {
		Up, Down, Close key.Binding
	}
}

var _ Dialog = (*ClusterDashboard)(nil)

// NewClusterDashboard creates an empty dashboard; rows arrive via SetRows.
func NewClusterDashboard(com *common.Common) *ClusterDashboard {
	d := &ClusterDashboard{com: com}
	d.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "scroll"))
	d.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "scroll"))
	d.keyMap.Close = CloseKey
	return d
}

// ID implements [Dialog].
func (*ClusterDashboard) ID() string { return ClusterDashboardID }

// SetRows replaces the displayed nodes; err, when non-empty, is shown
// instead of stale data.
func (d *ClusterDashboard) SetRows(rows []cluster.NodeDashboard, err string) {
	d.rows, d.err, d.updated = rows, err, time.Now()
}

// HandleMsg implements [Dialog].
func (d *ClusterDashboard) HandleMsg(msg tea.Msg) Action {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch {
	case key.Matches(kp, d.keyMap.Close):
		return ActionClose{}
	case key.Matches(kp, d.keyMap.Up):
		d.scroll = max(0, d.scroll-1)
	case key.Matches(kp, d.keyMap.Down):
		d.scroll++
	}
	return nil
}

// Draw implements [Dialog].
func (d *ClusterDashboard) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	width := min(max(60, area.Dx()-8), 110)
	inner := width - 4
	lines := []string{d.com.Styles.Dialog.Title.Render("Cluster"), ""}
	switch {
	case d.err != "":
		lines = append(lines, d.com.Styles.Resource.ErrorIcon.Render(d.err))
	case d.updated.IsZero():
		lines = append(lines, "Loading…")
	default:
		lines = append(lines, RenderClusterDashboard(d.rows, inner, d.com)...)
	}
	lines = append(lines, "", d.com.Styles.Resource.AdditionalText.Render("↑/↓ scroll · esc close · refreshes every 2s"))

	maxBody := max(5, area.Dy()-6)
	if len(lines) > maxBody {
		d.scroll = min(d.scroll, len(lines)-maxBody)
		lines = lines[d.scroll : d.scroll+maxBody]
	} else {
		d.scroll = 0
	}
	view := d.com.Styles.Dialog.View.Width(width).Render(strings.Join(lines, "\n"))
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements [help.KeyMap].
func (d *ClusterDashboard) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.Up, d.keyMap.Down, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *ClusterDashboard) FullHelp() [][]key.Binding {
	return [][]key.Binding{d.ShortHelp()}
}

// RenderClusterDashboard renders node cards to width.
func RenderClusterDashboard(rows []cluster.NodeDashboard, width int, com *common.Common) []string {
	t := com.Styles
	if len(rows) == 0 {
		return []string{"No machines reported. Is the Crush server running?"}
	}
	var out []string
	var online, jobs int
	for _, r := range rows {
		if r.Online {
			online++
		}
		if r.Stats != nil {
			jobs += len(r.Stats.Jobs)
		}
	}
	out = append(out, t.Resource.AdditionalText.Render(fmt.Sprintf("%d/%d nodes online · %d running jobs", online, len(rows), jobs)), "")
	if len(rows) == 1 && rows[0].Self {
		out = append(out,
			"No other machines yet. To add one, run here:",
			"  crush cluster init",
			"then on each other machine:",
			"  crush cluster join <this-machine-ip> <token>",
			"",
		)
	}
	for i, r := range rows {
		if i > 0 {
			out = append(out, "")
		}
		dot := t.Resource.OnlineIcon.String()
		if !r.Online {
			dot = t.Resource.ErrorIcon.String()
		}
		name := lipgloss.NewStyle().Bold(true).Render(r.Member.Name)
		if r.Self {
			name += t.Resource.AdditionalText.Render(" (this machine)")
		}
		meta := []string{r.Member.Version}
		if r.Stats != nil && r.Stats.Version != "" {
			meta[0] = r.Stats.Version
		}
		switch {
		case r.Self:
		case r.RTTMillis >= 0:
			meta = append(meta, fmt.Sprintf("rtt %dms", r.RTTMillis))
		default:
			meta = append(meta, "rtt ?")
		}
		if !r.Self {
			meta = append(meta, "seen "+humanAgo(r.LastSeen))
		}
		out = append(out, ansi.Truncate(dot+" "+name+"  "+t.Resource.AdditionalText.Render(strings.Join(meta, " · ")), width, "…"))
		s := r.Stats
		if s == nil {
			out = append(out, t.Resource.AdditionalText.Render("  no heartbeat yet"))
			continue
		}
		bar := func(label string, pct float64, extra string) string {
			return fmt.Sprintf("%s %s %3.0f%%%s", label, meter(pct, 10), pct, extra)
		}
		out = append(out, ansi.Truncate("  "+strings.Join([]string{
			bar("CPU ", s.CPUPercent, fmt.Sprintf(" (%d cores, load %.1f)", s.CPUCores, s.Load1)),
			bar("MEM ", s.MemPercent, fmt.Sprintf(" (%s free)", bytesHuman(s.MemAvailable))),
		}, "   "), width, "…"))
		out = append(out, ansi.Truncate("  "+bar("DISK", s.DiskPercent, fmt.Sprintf(" (%s free)", bytesHuman(s.DiskFree)))+
			t.Resource.AdditionalText.Render(fmt.Sprintf("   up %s · %d workspaces · %d sessions", durHuman(time.Duration(s.UptimeSeconds)*time.Second), s.Workspaces, s.Sessions)), width, "…"))
		if len(s.Jobs) == 0 {
			out = append(out, t.Resource.AdditionalText.Render("  idle"))
			continue
		}
		for _, j := range s.Jobs {
			title := j.Title
			if title == "" {
				title = j.SessionID[:min(8, len(j.SessionID))]
			}
			line := fmt.Sprintf("  %s %s  %s", t.Resource.BusyIcon.String(), title, t.Resource.AdditionalText.Render(home.Short(j.WorkspaceRoot)))
			if j.Animal != "" {
				line += t.Resource.AdditionalText.Render(" · " + j.Color + "-" + j.Animal)
			}
			out = append(out, ansi.Truncate(line, width, "…"))
		}
	}
	return out
}

func meter(pct float64, cells int) string {
	filled := int(pct/100*float64(cells) + 0.5)
	filled = max(0, min(cells, filled))
	return strings.Repeat("█", filled) + strings.Repeat("░", cells-filled)
}

func bytesHuman(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

func durHuman(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

func humanAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	return durHuman(d) + " ago"
}
