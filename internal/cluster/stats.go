package cluster

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// NodeJob is one session with an in-flight agent turn on a node.
type NodeJob struct {
	SessionID     string `json:"session_id"`
	Title         string `json:"title"`
	WorkspaceRoot string `json:"workspace_root"`
	Animal        string `json:"animal,omitempty"`
	Color         string `json:"color,omitempty"`
	Blocked       bool   `json:"blocked,omitempty"`
}

// NodeStats is a point-in-time resource snapshot of a node, broadcast as
// its heartbeat.
type NodeStats struct {
	NodeID    string    `json:"node_id"`
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Platform  string    `json:"platform"`
	Hostname  string    `json:"hostname"`
	SampledAt time.Time `json:"sampled_at"`

	CPUPercent float64 `json:"cpu_percent"`
	CPUCores   int     `json:"cpu_cores"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`

	MemTotal     uint64  `json:"mem_total"`
	MemAvailable uint64  `json:"mem_available"`
	MemPercent   float64 `json:"mem_percent"`

	DiskPath    string  `json:"disk_path"`
	DiskTotal   uint64  `json:"disk_total"`
	DiskFree    uint64  `json:"disk_free"`
	DiskPercent float64 `json:"disk_percent"`

	UptimeSeconds uint64 `json:"uptime_seconds"`

	Workspaces int       `json:"workspaces"`
	Sessions   int       `json:"sessions"`
	Jobs       []NodeJob `json:"jobs,omitempty"`
}

// FreeScore estimates spare capacity in [0,1] from CPU and memory. The
// scheduler places work on the node with the highest score.
func (s NodeStats) FreeScore() float64 {
	cpuFree := 1 - clamp01(s.CPUPercent/100)
	memFree := 1 - clamp01(s.MemPercent/100)
	jobPenalty := 1 / (1 + float64(len(s.Jobs))*0.15)
	return (0.5*cpuFree + 0.5*memFree) * jobPenalty
}

func clamp01(v float64) float64 {
	return max(0, min(1, v))
}

// CollectStats samples host resources. diskPath selects the filesystem
// reported (the user's home by default). Errors from individual probes
// leave their fields zero.
func CollectStats(ctx context.Context, diskPath string) NodeStats {
	s := NodeStats{
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		SampledAt: time.Now().UTC(),
		CPUCores:  runtime.NumCPU(),
	}
	s.Hostname, _ = os.Hostname()
	// A zero interval compares against the previous call, so periodic
	// heartbeats get the utilization since the last beat without
	// blocking.
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		s.CPUPercent = pct[0]
	}
	if avg, err := load.AvgWithContext(ctx); err == nil {
		s.Load1, s.Load5, s.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemTotal, s.MemAvailable, s.MemPercent = vm.Total, vm.Available, vm.UsedPercent
	}
	if diskPath == "" {
		diskPath, _ = os.UserHomeDir()
	}
	if diskPath != "" {
		if du, err := disk.UsageWithContext(ctx, diskPath); err == nil {
			s.DiskPath, s.DiskTotal, s.DiskFree, s.DiskPercent = diskPath, du.Total, du.Free, du.UsedPercent
		}
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		s.UptimeSeconds = up
	}
	return s
}

// NodeDashboard is one row of the cluster dashboard.
type NodeDashboard struct {
	Member   Member    `json:"member"`
	Self     bool      `json:"self"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	// RTTMillis is the last mesh round-trip time; -1 when unknown.
	RTTMillis int64      `json:"rtt_ms"`
	Stats     *NodeStats `json:"stats,omitempty"`
}
