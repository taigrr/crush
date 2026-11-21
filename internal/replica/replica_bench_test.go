package replica

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

const benchMessages = 10

// benchSessions defaults to 200; CRUSH_BENCH_REPLICA_SESSIONS overrides it
// for the scale harness (bench/scale.sh).
var benchSessions = func() int {
	if n, err := strconv.Atoi(os.Getenv("CRUSH_BENCH_REPLICA_SESSIONS")); err == nil && n > 0 {
		return n
	}
	return 200
}()

func seedBench(b *testing.B) *sql.DB {
	conn := open(b)
	for i := range benchSessions {
		sid := fmt.Sprintf("s-%04d", i)
		addSession(b, conn, sid, "Benchmark session "+sid)
		for j := range benchMessages {
			addMessage(b, conn, fmt.Sprintf("%s-m%02d", sid, j), sid)
		}
	}
	return conn
}

// BenchmarkReplicaFullSync measures a full replication pass of 200
// sessions x 10 messages: Serve on the owner, JSON over the wire, Apply on
// a fresh replica.
func BenchmarkReplicaFullSync(b *testing.B) {
	b.Cleanup(db.ResetPool)
	ctx := context.Background()
	src := seedBench(b)
	var wire int64
	for b.Loop() {
		b.StopTimer()
		dst := open(b)
		b.StartTimer()
		batch, err := Serve(ctx, src, "nodeA", -1, 0, false)
		require.NoError(b, err)
		data, err := json.Marshal(batch)
		require.NoError(b, err)
		wire = int64(len(data))
		var in Batch
		require.NoError(b, json.Unmarshal(data, &in))
		_, err = Apply(ctx, dst, "nodeA", in)
		require.NoError(b, err)
	}
	b.ReportMetric(float64(wire), "wire-B")
}

// BenchmarkReplicaServeDelta measures an incremental Serve when nothing
// changed (the steady-state sync poll).
func BenchmarkReplicaServeDelta(b *testing.B) {
	b.Cleanup(db.ResetPool)
	ctx := context.Background()
	src := seedBench(b)
	since := MaxSeq(ctx, src)
	for b.Loop() {
		_, err := Serve(ctx, src, "nodeA", since, 0, false)
		require.NoError(b, err)
	}
}
