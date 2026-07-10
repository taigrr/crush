package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChangelog_RecordsSessionMutations(t *testing.T) {
	t.Cleanup(ResetPool)
	ctx := context.Background()
	conn, err := Connect(ctx, t.TempDir())
	require.NoError(t, err)

	now := time.Now().UnixMilli()
	_, err = conn.ExecContext(ctx,
		`INSERT INTO sessions(id,title,updated_at,created_at) VALUES('s1','t',?,?)`, now, now)
	require.NoError(t, err)

	_, err = conn.ExecContext(ctx,
		`INSERT INTO messages(id,session_id,role,parts,created_at,updated_at) VALUES('m1','s1','user','[]',?,?)`, now, now)
	require.NoError(t, err)

	_, err = conn.ExecContext(ctx, `DELETE FROM sessions WHERE id='s1'`)
	require.NoError(t, err)

	rows, err := conn.QueryContext(ctx,
		`SELECT op, table_name, pk FROM _changelog ORDER BY change_seq`)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var op, table, pk string
		require.NoError(t, rows.Scan(&op, &table, &pk))
		got = append(got, op+":"+table+":"+pk)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, got, "I:sessions:s1")
	require.Contains(t, got, "I:messages:m1")
	require.Contains(t, got, "D:sessions:s1")
	// Cascade delete from messages should also have logged.
	require.Contains(t, got, "D:messages:m1")

	var seq int
	require.NoError(t, conn.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'`).Scan(&seq))
	require.GreaterOrEqual(t, seq, 4)
}
