package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

// TestPeekCacheInvalidatesOnWALOnlyWrites verifies cached overviews of a
// workspace are refreshed by writes that land only in the WAL (the writer
// keeps its connection open, so nothing is checkpointed into crush.db).
func TestPeekCacheInvalidatesOnWALOnlyWrites(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	var mode string
	require.NoError(t, conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
	require.Equal(t, "wal", mode)
	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "s1", Title: "One"})
	require.NoError(t, err)

	var c peekCache
	titles := func() map[string]string {
		ovs, _, err := c.overviews(ctx, dataDir)
		require.NoError(t, err)
		out := map[string]string{}
		for _, o := range ovs {
			out[o.ID] = o.Title
		}
		return out
	}
	require.Equal(t, map[string]string{"s1": "One"}, titles())

	mainFile := filepath.Join(dataDir, "crush.db")
	before, err := os.Stat(mainFile)
	require.NoError(t, err)

	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "s2", Title: "Two"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1": "One", "s2": "Two"}, titles())

	_, err = conn.ExecContext(ctx, "UPDATE sessions SET title = 'Uno' WHERE id = 's1'")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1": "Uno", "s2": "Two"}, titles())

	_, err = conn.ExecContext(ctx, "DELETE FROM sessions WHERE id = 's2'")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1": "Uno"}, titles())
	after, err := os.Stat(mainFile)
	require.NoError(t, err)
	require.Equal(t, before.Size(), after.Size(), "writes must stay in the WAL")
	require.Equal(t, before.ModTime(), after.ModTime(), "writes must stay in the WAL")

	// Overwriting the WAL from its start after a checkpoint keeps the
	// file size unchanged; the stamp must still move.
	_, err = conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1": "Uno"}, titles())
	_, err = conn.ExecContext(ctx, "UPDATE sessions SET title = 'Eins' WHERE id = 's1'")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"s1": "Eins"}, titles())
}

func TestPeekCacheEvictsLRU(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	loads := map[string]int{}
	c := peekCache{maxSessions: 5}
	c.peek = func(_ context.Context, dataDir string) ([]db.PeekedSession, error) {
		loads[dataDir]++
		return make([]db.PeekedSession, 2), nil
	}
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	for _, d := range dirs[:2] {
		_, err := c.load(ctx, d)
		require.NoError(t, err)
	}
	_, err := c.load(ctx, dirs[0]) // touch: dirs[1] is now oldest
	require.NoError(t, err)
	_, err = c.load(ctx, dirs[2])
	require.NoError(t, err)
	require.Len(t, c.entries, 2)
	require.Equal(t, 4, c.total)
	require.NotContains(t, c.entries, dirs[1])
	_, err = c.load(ctx, dirs[0])
	require.NoError(t, err)
	require.Equal(t, 1, loads[dirs[0]], "recently used entry stays cached")
	_, err = c.load(ctx, dirs[1])
	require.NoError(t, err)
	require.Equal(t, 2, loads[dirs[1]], "evicted entry is reloaded")
}
