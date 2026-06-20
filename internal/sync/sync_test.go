package sync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func TestFingerprint_Stable(t *testing.T) {
	a, pa := Fingerprint(FingerprintInputs{Remote: "github.com/x/y", RepoRelCrushDir: ".crush"})
	b, pb := Fingerprint(FingerprintInputs{Remote: "github.com/x/y", RepoRelCrushDir: ".crush"})
	require.Equal(t, a, b)
	require.True(t, pa)
	require.True(t, pb)

	c, portable := Fingerprint(FingerprintInputs{AbsPath: "/tmp/foo/.crush"})
	require.NotEmpty(t, c)
	require.False(t, portable)
}

func TestIdentityAndCursors(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	conn, err := db.Connect(ctx, t.TempDir())
	require.NoError(t, err)

	id1, err := LoadOrInitIdentity(ctx, conn, FingerprintInputs{Remote: "github.com/c/c", RepoRelCrushDir: ".crush"})
	require.NoError(t, err)
	require.NotEmpty(t, id1.DBID)
	require.NotEmpty(t, id1.Fingerprint)
	require.True(t, id1.Portable)

	id2, err := LoadOrInitIdentity(ctx, conn, FingerprintInputs{AbsPath: "/elsewhere"})
	require.NoError(t, err)
	require.Equal(t, id1, id2, "identity must be stable across loads")

	c, err := LoadCursors(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, int64(0), c.PushCursor)

	require.NoError(t, SetPushCursor(ctx, conn, 42))
	c, err = LoadCursors(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, int64(42), c.PushCursor)
}

func TestPendingChanges_CollapsesAndDeletes(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	conn, err := db.Connect(ctx, t.TempDir())
	require.NoError(t, err)

	now := time.Now().UnixMilli()
	_, err = conn.ExecContext(ctx,
		`INSERT INTO sessions(id,title,updated_at,created_at) VALUES('s1','t',?,?)`, now, now)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx,
		`UPDATE sessions SET title='t2' WHERE id='s1'`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx,
		`INSERT INTO sessions(id,title,updated_at,created_at) VALUES('s2','x',?,?)`, now, now)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `DELETE FROM sessions WHERE id='s2'`)
	require.NoError(t, err)

	changes, maxSeq, err := PendingChanges(ctx, conn, 0)
	require.NoError(t, err)
	require.Greater(t, maxSeq, int64(0))

	// We expect: one Insert for s1 (collapsed I+U), one Delete for s2.
	bySession := map[string]Change{}
	for _, c := range changes {
		if c.Table == "sessions" {
			bySession[c.PK] = c
		}
	}
	require.Equal(t, OpInsert, bySession["s1"].Op)
	require.Equal(t, "t2", bySession["s1"].Row["title"])
	require.Equal(t, OpDelete, bySession["s2"].Op)
	require.Nil(t, bySession["s2"].Row)

	require.NoError(t, TruncateChangelog(ctx, conn, maxSeq))
	changes2, _, err := PendingChanges(ctx, conn, maxSeq)
	require.NoError(t, err)
	require.Empty(t, changes2)
}
