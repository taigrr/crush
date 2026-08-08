package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenWritable_MissingDatabaseReturnsNil(t *testing.T) {
	t.Parallel()

	// A directory with no crush.db yields a nil conn so the caller can
	// treat an uninitialized workspace as "not found" rather than erroring.
	conn, err := OpenWritable(t.TempDir())
	require.NoError(t, err)
	require.Nil(t, conn)
}

func TestOpenWritable_MissingDirectoryReturnsNil(t *testing.T) {
	t.Parallel()

	conn, err := OpenWritable(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	require.Nil(t, conn)
}

// TestOpenWritable_WritesLandWithoutMigrations verifies OpenWritable opens
// an existing database read-write (no migrations run, no pool entry) and
// that an UPDATE persists, visible on a fresh read-only reopen.
func TestOpenWritable_WritesLandWithoutMigrations(t *testing.T) {
	t.Cleanup(ResetPool)
	ctx := context.Background()

	// Create a real (migrated) database, then fully release it so it is
	// closed on disk — the detached scenario.
	dataDir := t.TempDir()
	conn, err := Connect(ctx, dataDir)
	require.NoError(t, err)
	q := New(conn)
	_, err = q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)
	require.NoError(t, Release(dataDir))
	ResetPool()

	// Reopen writable and archive the session.
	wconn, err := OpenWritable(dataDir)
	require.NoError(t, err)
	require.NotNil(t, wconn)
	require.NoError(t, New(wconn).ArchiveSession(ctx, "s1"))
	require.NoError(t, wconn.Close())

	// Confirm the write is durable via a fresh read-only open.
	rconn, err := OpenReadOnly(dataDir)
	require.NoError(t, err)
	require.NotNil(t, rconn)
	defer rconn.Close()
	s, err := New(rconn).GetSessionByID(ctx, "s1")
	require.NoError(t, err)
	require.True(t, s.ArchivedAt.Valid && s.ArchivedAt.Int64 != 0)
}

// TestOpenWritable_RefusesWhenPooled verifies OpenWritable refuses with
// ErrDataDirBusy while a live Connect handle for the same data dir is still
// pooled, so it never opens a second writable handle to a database this
// process is attaching or mid-closing (the teardown race). Because poolMu
// is held across both Release's delete+Close and OpenWritable's check+open,
// a one-shot write cannot interleave with an attach/teardown in-process.
func TestOpenWritable_RefusesWhenPooled(t *testing.T) {
	t.Cleanup(ResetPool)
	ctx := context.Background()

	dataDir := t.TempDir()
	conn, err := Connect(ctx, dataDir)
	require.NoError(t, err)
	_, err = New(conn).CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)

	// While the Connect handle is still pooled, OpenWritable must refuse.
	wconn, err := OpenWritable(dataDir)
	require.Nil(t, wconn)
	require.ErrorIs(t, err, ErrDataDirBusy)

	// After releasing (pool entry dropped, DB closed) it may open again.
	require.NoError(t, Release(dataDir))
	ResetPool()
	wconn2, err := OpenWritable(dataDir)
	require.NoError(t, err)
	require.NotNil(t, wconn2)
	require.NoError(t, wconn2.Close())
}
