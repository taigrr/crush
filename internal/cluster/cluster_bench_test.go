package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

// Cluster networking benchmarks. Every node lives in a b.TempDir() and
// listens on 127.0.0.1 random ports; nothing touches the user's cluster.

// benchOverview mirrors the JSON shape of a workspace overview with
// sessions (proto imports cluster, so the real type cannot be used here).
type benchOverview struct {
	ID       string         `json:"id"`
	Path     string         `json:"path"`
	NodeID   string         `json:"node_id"`
	Sessions []benchSession `json:"sessions"`
}

type benchSession struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	MessageCount     int64   `json:"message_count"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
	UpdatedAt        int64   `json:"updated_at"`
	CreatedAt        int64   `json:"created_at"`
	Busy             bool    `json:"busy"`
	Model            string  `json:"model"`
}

func benchOverviews(nodeID string, n int) []benchOverview {
	ov := benchOverview{ID: "ws-" + nodeID[:8], Path: "/home/bench/project", NodeID: nodeID}
	now := time.Now().UnixMilli()
	for i := range n {
		ov.Sessions = append(ov.Sessions, benchSession{
			ID:           fmt.Sprintf("%s-session-%04d", nodeID[:8], i),
			Title:        fmt.Sprintf("Benchmark session %d with a realistic title", i),
			MessageCount: int64(i % 97), PromptTokens: 12345, CompletionTokens: 6789,
			Cost: 0.42, UpdatedAt: now, CreatedAt: now, Model: "claude-sonnet-4",
		})
	}
	return []benchOverview{ov}
}

// startTrioMesh pairs three nodes, starts their meshes and waits for a
// full route mesh.
func startTrioMesh(b testing.TB) ([]*Node, []string, []*Mesh) {
	quietLogs(b)
	nodes, addrs := pairedTrio(b)
	meshes := make([]*Mesh, len(nodes))
	for i, n := range nodes {
		m, err := n.StartMesh(addrs[i])
		require.NoError(b, err)
		meshes[i] = m
	}
	b.Cleanup(func() {
		for _, m := range meshes {
			m.Close()
		}
	})
	waitRoutes(b, meshes, 2)
	return nodes, addrs, meshes
}

func waitRoutes(b testing.TB, meshes []*Mesh, want int) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, m := range meshes {
			if peerCount(m) < want {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	b.Fatalf("routes did not reach %d", want)
}

func reportLatency(b *testing.B, samples []time.Duration) {
	if len(samples) == 0 {
		return
	}
	slices.Sort(samples)
	p := func(q float64) float64 {
		return float64(samples[int(q*float64(len(samples)-1))].Microseconds())
	}
	b.ReportMetric(p(0.50), "p50-us")
	b.ReportMetric(p(0.99), "p99-us")
}

func memberByID(b testing.TB, n *Node, id string) Member {
	for _, m := range n.Members() {
		if m.NodeID == id {
			return m
		}
	}
	b.Fatalf("member %s not found", id)
	return Member{}
}

// BenchmarkClusterColdJoin measures pairing three fresh nodes plus mesh
// start until every node has routes to both others.
func BenchmarkClusterColdJoin(b *testing.B) {
	quietLogs(b)
	for b.Loop() {
		nodes, addrs := pairedTrio(b)
		meshes := make([]*Mesh, len(nodes))
		meshStart := time.Now()
		for i, n := range nodes {
			m, err := n.StartMesh(addrs[i])
			require.NoError(b, err)
			meshes[i] = m
		}
		waitRoutes(b, meshes, 2)
		b.ReportMetric(float64(time.Since(meshStart).Milliseconds()), "mesh-ms")
		b.StopTimer()
		for _, m := range meshes {
			m.Close()
		}
		for _, n := range nodes {
			_ = n.Stop(context.Background())
		}
		b.StartTimer()
	}
}

// BenchmarkClusterMeshRequest measures a NATS request/reply round trip
// from node 0 to a responder on node 1.
func BenchmarkClusterMeshRequest(b *testing.B) {
	_, _, meshes := startTrioMesh(b)
	_, err := meshes[1].Subscribe("crush.bench.echo", func(msg *nats.Msg) { _ = msg.Respond(msg.Data) })
	require.NoError(b, err)
	require.NoError(b, meshes[1].Conn().Flush())
	ctx := context.Background()
	require.Eventually(b, func() bool {
		_, err := meshes[0].Request(ctx, "crush.bench.echo", []byte("x"))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	payload := []byte("ping")
	var samples []time.Duration
	for b.Loop() {
		start := time.Now()
		_, err := meshes[0].Request(ctx, "crush.bench.echo", payload)
		samples = append(samples, time.Since(start))
		if err != nil {
			b.Fatal(err)
		}
	}
	reportLatency(b, samples)
}

// BenchmarkClusterCallAPI measures an mTLS HTTP API call node 0 -> node 1
// (the path session overview fetches take).
func BenchmarkClusterCallAPI(b *testing.B) {
	quietLogs(b)
	nodes, _ := pairedTrio(b)
	nodes[1].SetAPIHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v1.0.0"}`))
	}))
	mem := memberByID(b, nodes[0], nodes[1].Identity().ID)
	ctx := context.Background()
	var out map[string]string
	require.NoError(b, nodes[0].CallAPI(ctx, mem, http.MethodGet, "/v1/version", &out))
	var samples []time.Duration
	for b.Loop() {
		start := time.Now()
		err := nodes[0].CallAPI(ctx, mem, http.MethodGet, "/v1/version", &out)
		samples = append(samples, time.Since(start))
		if err != nil {
			b.Fatal(err)
		}
	}
	reportLatency(b, samples)
}

// BenchmarkClusterOverviewFetch measures node 0 fetching workspace
// overviews (200 sessions each) from both other members in parallel, the
// way the server's remote view refreshes.
func BenchmarkClusterOverviewFetch(b *testing.B) {
	quietLogs(b)
	nodes, _ := pairedTrio(b)
	for _, n := range nodes[1:] {
		data, err := json.Marshal(benchOverviews(n.Identity().ID, 200))
		require.NoError(b, err)
		n.SetAPIHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		}))
	}
	members := nodes[0].Members()
	require.Len(b, members, 2)
	ctx := context.Background()
	fetch := func() error {
		var wg sync.WaitGroup
		errs := make([]error, len(members))
		for i, mem := range members {
			wg.Go(func() {
				var ovs []benchOverview
				errs[i] = nodes[0].CallAPI(ctx, mem, http.MethodGet, "/v1/workspace-overviews?local=1", &ovs)
				if errs[i] == nil && (len(ovs) != 1 || len(ovs[0].Sessions) != 200) {
					errs[i] = fmt.Errorf("unexpected overviews from %s", mem.Name)
				}
			})
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}
	require.NoError(b, fetch())
	var samples []time.Duration
	for b.Loop() {
		start := time.Now()
		err := fetch()
		samples = append(samples, time.Since(start))
		if err != nil {
			b.Fatal(err)
		}
	}
	reportLatency(b, samples)
}

// BenchmarkClusterMeshReconnect measures how long the mesh takes to be
// fully routed again after node 2's mesh stops and restarts on the same
// address.
func BenchmarkClusterMeshReconnect(b *testing.B) {
	nodes, addrs, meshes := startTrioMesh(b)
	var samples []time.Duration
	for b.Loop() {
		b.StopTimer()
		meshes[2].Close()
		deadline := time.Now().Add(30 * time.Second)
		for _, m := range meshes[:2] {
			for peerCount(m) > 1 {
				if time.Now().After(deadline) {
					b.Fatal("route to stopped peer never dropped")
				}
				time.Sleep(time.Millisecond)
			}
		}
		b.StartTimer()
		start := time.Now()
		m, err := nodes[2].StartMesh(addrs[2])
		require.NoError(b, err)
		meshes[2] = m
		waitRoutes(b, meshes, 2)
		samples = append(samples, time.Since(start))
	}
	reportLatency(b, samples)
}

// BenchmarkClusterMeshReconnectOneSided is BenchmarkClusterMeshReconnect
// when the restarted peer cannot dial out: its own route URLs point at a
// dead port, so only the survivors' redials can restore the mesh.
func BenchmarkClusterMeshReconnectOneSided(b *testing.B) {
	nodes, addrs, meshes := startTrioMesh(b)
	self := nodes[2].id.ID
	require.NoError(b, nodes[2].store.Update(func(st *State) error {
		for i := range st.Members {
			if st.Members[i].NodeID != self {
				st.Members[i].Endpoints = []Endpoint{{Kind: EndpointNATS, Addr: "127.0.0.1:1"}}
			}
		}
		return nil
	}))
	var samples []time.Duration
	for b.Loop() {
		b.StopTimer()
		meshes[2].Close()
		deadline := time.Now().Add(30 * time.Second)
		for _, m := range meshes[:2] {
			for peerCount(m) > 1 {
				if time.Now().After(deadline) {
					b.Fatal("route to stopped peer never dropped")
				}
				time.Sleep(time.Millisecond)
			}
		}
		b.StartTimer()
		start := time.Now()
		m, err := nodes[2].StartMesh(addrs[2])
		require.NoError(b, err)
		meshes[2] = m
		waitRoutes(b, meshes, 2)
		samples = append(samples, time.Since(start))
	}
	reportLatency(b, samples)
}

// BenchmarkClusterMeshThroughput measures 1 KiB publishes from node 0 to
// a subscriber on node 2, flushed end to end per batch.
func BenchmarkClusterMeshThroughput(b *testing.B) {
	_, _, meshes := startTrioMesh(b)
	const batch = 256
	got := make(chan struct{}, batch)
	_, err := meshes[2].Subscribe("crush.bench.tp", func(*nats.Msg) { got <- struct{}{} })
	require.NoError(b, err)
	require.NoError(b, meshes[2].Conn().Flush())
	payload := make([]byte, 1024)
	require.Eventually(b, func() bool {
		_ = meshes[0].Conn().Publish("crush.bench.tp", payload)
		select {
		case <-got:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 10*time.Second, 10*time.Millisecond)
	for len(got) > 0 {
		<-got
	}
	b.SetBytes(int64(len(payload)) * batch)
	for b.Loop() {
		for range batch {
			if err := meshes[0].Conn().Publish("crush.bench.tp", payload); err != nil {
				b.Fatal(err)
			}
		}
		for range batch {
			select {
			case <-got:
			case <-time.After(10 * time.Second):
				b.Fatal("timed out waiting for messages")
			}
		}
	}
}

func quietLogs(b testing.TB) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(prev) })
}

// peerCount reports distinct peers with a route (NATS pools several route
// connections per peer, so NumRoutes overcounts).
func peerCount(m *Mesh) int {
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()
	if srv == nil {
		return 0
	}
	rz, err := srv.Routez(nil)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	for _, r := range rz.Routes {
		seen[r.RemoteID] = true
	}
	return len(seen)
}
