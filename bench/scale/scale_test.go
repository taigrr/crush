package scale

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/session"
)

var sessionCounts = []int{200, 2000, 5000}

// fixture returns a migrated data dir holding n sessions (4 messages each,
// 3 long sessions of 300 messages).
func fixture(b *testing.B, n int) string {
	b.Helper()
	dir := filepath.Join(b.TempDir(), ".crush")
	conn, err := db.Connect(context.Background(), dir)
	require.NoError(b, err)
	_, err = conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(b, err)
	require.NoError(b, db.Release(dir))
	require.NoError(b, Fill(filepath.Join(dir, "crush.db"), 0, n, 4, 3, 300, 1767225600))
	return dir
}

func BenchmarkPeekSessions(b *testing.B) {
	for _, n := range sessionCounts {
		b.Run(fmt.Sprintf("sessions=%d", n), func(b *testing.B) {
			dir := fixture(b, n)
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.PeekSessions(context.Background(), dir)
				if err != nil || len(got) != n {
					b.Fatal(err, len(got))
				}
			}
		})
	}
}

func BenchmarkSessionServiceList(b *testing.B) {
	for _, n := range sessionCounts {
		b.Run(fmt.Sprintf("sessions=%d", n), func(b *testing.B) {
			dir := fixture(b, n)
			conn, err := db.Connect(context.Background(), dir)
			require.NoError(b, err)
			b.Cleanup(func() { _ = db.Release(dir) })
			svc := session.NewService(db.New(conn), conn)
			b.ReportAllocs()
			for b.Loop() {
				got, err := svc.List(context.Background())
				if err != nil || len(got) != n {
					b.Fatal(err, len(got))
				}
			}
		})
	}
}

func BenchmarkConnectRelease(b *testing.B) {
	for _, n := range sessionCounts {
		b.Run(fmt.Sprintf("sessions=%d", n), func(b *testing.B) {
			dir := fixture(b, n)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := db.Connect(context.Background(), dir); err != nil {
					b.Fatal(err)
				}
				if err := db.Release(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRegistryList(b *testing.B) {
	for _, n := range []int{10, 100, 300, 1000} {
		b.Run(fmt.Sprintf("workspaces=%d", n), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "workspaces.jsonl")
			reg := registry.NewWithPath(path)
			// Each workspace recorded several times, as repeated opens do.
			for r := range 5 {
				for i := range n {
					root := fmt.Sprintf("/w/ws%04d", i)
					require.NoError(b, reg.Add(registry.Entry{Root: root, DataDir: root + "/.crush", LastUsed: int64(r*n + i)}))
				}
			}
			fi, err := os.Stat(path)
			require.NoError(b, err)
			b.ReportMetric(float64(fi.Size()), "file_bytes")
			b.ReportAllocs()
			for b.Loop() {
				got, err := reg.List()
				if err != nil || len(got) != n {
					b.Fatal(err, len(got))
				}
			}
		})
	}
}

// BenchmarkDecodeOverviews decodes a saved /v1/workspace-overviews payload
// (SCALE_OVERVIEW_JSON) the way the TUI client does.
func BenchmarkDecodeOverviews(b *testing.B) {
	path := os.Getenv("SCALE_OVERVIEW_JSON")
	if path == "" {
		b.Skip("SCALE_OVERVIEW_JSON not set")
	}
	data, err := os.ReadFile(path)
	require.NoError(b, err)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var ovs []proto.WorkspaceOverview
		if err := json.Unmarshal(data, &ovs); err != nil {
			b.Fatal(err)
		}
	}
}
