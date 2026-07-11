package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPeekSessions_ReadsWithoutBlockingWriteOpen is the core safety test
// for the cross-workspace picker: peeking a workspace read-only must not
// prevent a subsequent full read-write Connect (which takes the data-dir
// lock and runs migrations). We never upgrade a peek handle in place; we
// open read-only, close it, then open read-write fresh.
func TestPeekSessions_ReadsWithoutBlockingWriteOpen(t *testing.T) {
	t.Cleanup(ResetPool)
	ctx := context.Background()
	dataDir := t.TempDir()

	// Create a workspace DB with one session via the normal RW path.
	conn, err := Connect(ctx, dataDir)
	require.NoError(t, err)
	q := New(conn)
	_, err = q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "First"})
	require.NoError(t, err)
	require.NoError(t, Release(dataDir))
	ResetPool()

	// Peek read-only.
	peeked, err := PeekSessions(ctx, dataDir)
	require.NoError(t, err)
	require.Len(t, peeked, 1)
	require.Equal(t, "s1", peeked[0].ID)
	require.Equal(t, "First", peeked[0].Title)
	require.False(t, peeked[0].Unread(), "a session that never finished is not unread")

	// The peek handle is already closed; a fresh RW open (with the
	// data-dir lock) must succeed and see the same data.
	conn2, err := Connect(ctx, dataDir, WithDataDirLock(true))
	require.NoError(t, err)
	defer func() { _ = Release(dataDir) }()
	q2 := New(conn2)
	got, err := q2.GetSessionByID(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "First", got.Title)
}

func TestPeekSessions_MissingDBIsEmpty(t *testing.T) {
	t.Parallel()
	peeked, err := PeekSessions(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.Empty(t, peeked)
}

func TestPeekSessions_ReportsUnreadAndWorkingDir(t *testing.T) {
	t.Cleanup(ResetPool)
	ctx := context.Background()
	dataDir := t.TempDir()

	conn, err := Connect(ctx, dataDir)
	require.NoError(t, err)
	q := New(conn)
	_, err = q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "Busy work"})
	require.NoError(t, err)
	require.NoError(t, q.SetSessionWorkingDir(ctx, SetSessionWorkingDirParams{
		WorkingDir: sql.NullString{String: "/proj/sub", Valid: true},
		ID:         "s1",
	}))
	// Finish more recently than seen -> unread.
	require.NoError(t, q.MarkSessionSeen(ctx, "s1"))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, q.MarkSessionFinished(ctx, "s1"))
	require.NoError(t, Release(dataDir))
	ResetPool()

	peeked, err := PeekSessions(ctx, dataDir)
	require.NoError(t, err)
	require.Len(t, peeked, 1)
	require.Equal(t, "/proj/sub", peeked[0].WorkingDir)
	require.True(t, peeked[0].Unread(), "finished after seen must be unread")
}
