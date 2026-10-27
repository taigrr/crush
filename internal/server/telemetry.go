package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/cluster"
)

const (
	subjHeartbeat = "crush.heartbeat"
	subjPing      = "crush.ping." // + node ID

	heartbeatInterval = 5 * time.Second
	pingInterval      = 10 * time.Second
	// offlineAfter marks a node offline after this many missed beats.
	offlineAfter = 3 * heartbeatInterval
)

// telemetry gathers heartbeats and round-trip times from every node.
type telemetry struct {
	bridge *meshBridge
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	stats map[string]cluster.NodeStats
	rtt   map[string]time.Duration
}

func newTelemetry(br *meshBridge) *telemetry {
	return &telemetry{
		bridge: br,
		stats:  map[string]cluster.NodeStats{},
		rtt:    map[string]time.Duration{},
	}
}

// localStats samples this node, including its active jobs.
func (t *telemetry) localStats(ctx context.Context) cluster.NodeStats {
	node := t.bridge.node
	s := cluster.CollectStats(ctx, "")
	s.NodeID, s.Name, s.Version = node.Identity().ID, node.Identity().Name, node.Version()
	s.Jobs, s.Workspaces, s.Sessions = t.bridge.backend.ActiveJobs(ctx)
	return s
}

func (t *telemetry) start() error {
	nc := t.bridge.mesh.Conn()
	self := t.bridge.NodeID()
	if _, err := t.bridge.mesh.Subscribe(subjHeartbeat, func(msg *nats.Msg) {
		var s cluster.NodeStats
		if json.Unmarshal(msg.Data, &s) != nil || s.NodeID == "" || s.NodeID == self {
			return
		}
		t.mu.Lock()
		t.stats[s.NodeID] = s
		t.mu.Unlock()
		t.bridge.node.MarkSeen(s.NodeID, time.Now().UTC())
	}); err != nil {
		return err
	}
	if _, err := t.bridge.mesh.Subscribe(subjPing+self, func(msg *nats.Msg) {
		_ = msg.Respond([]byte("pong"))
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.wg.Go(func() { t.loop(ctx, heartbeatInterval, t.beat) })
	t.wg.Go(func() { t.loop(ctx, pingInterval, t.pingAll) })
	return nc.Flush()
}

func (t *telemetry) loop(ctx context.Context, every time.Duration, fn func(context.Context)) {
	fn(ctx)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			fn(ctx)
		}
	}
}

func (t *telemetry) beat(ctx context.Context) {
	data, err := json.Marshal(t.localStats(ctx))
	if err != nil {
		return
	}
	if err := t.bridge.mesh.Conn().Publish(subjHeartbeat, data); err != nil {
		slog.Debug("Failed to publish heartbeat", "error", err)
	}
}

func (t *telemetry) pingAll(ctx context.Context) {
	for _, m := range t.bridge.node.Members() {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		start := time.Now()
		_, err := t.bridge.mesh.Request(cctx, subjPing+m.NodeID, nil)
		cancel()
		t.mu.Lock()
		if err == nil {
			t.rtt[m.NodeID] = time.Since(start)
		} else {
			delete(t.rtt, m.NodeID)
		}
		t.mu.Unlock()
	}
}

func (t *telemetry) stop() {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
}

// dashboard assembles one row per member, this node first.
func (t *telemetry) dashboard(ctx context.Context) []cluster.NodeDashboard {
	node := t.bridge.node
	st, err := node.Store().Snapshot()
	if err != nil {
		return nil
	}
	self := node.Identity().ID
	now := time.Now()
	var out []cluster.NodeDashboard
	for _, m := range st.Members {
		row := cluster.NodeDashboard{Member: m, RTTMillis: -1}
		if m.NodeID == self {
			s := t.localStats(ctx)
			row.Self, row.Online, row.LastSeen, row.Stats, row.RTTMillis = true, true, now.UTC(), &s, 0
			out = append([]cluster.NodeDashboard{row}, out...)
			continue
		}
		t.mu.Lock()
		if s, ok := t.stats[m.NodeID]; ok {
			s := s
			row.Stats = &s
		}
		if rtt, ok := t.rtt[m.NodeID]; ok {
			row.RTTMillis = rtt.Milliseconds()
		}
		t.mu.Unlock()
		row.LastSeen = node.LastSeen(m.NodeID)
		row.Online = !row.LastSeen.IsZero() && now.Sub(row.LastSeen) < offlineAfter
		out = append(out, row)
	}
	return out
}
