package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/app"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/session"
)

// newDetachedWorkspaceDB builds a real workspace database at a fresh data
// directory with the given sessions and returns its data dir. The database
// is fully closed (released) before returning, so it is DETACHED: known
// only by its files, not open anywhere.
func newDetachedWorkspaceDB(t *testing.T, ids ...string) string {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	for _, id := range ids {
		_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: id, Title: id})
		require.NoError(t, err)
		// Stamp a run completion so the session is "unread" until seen.
		require.NoError(t, q.MarkSessionFinished(ctx, id))
	}
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()
	return dataDir
}

// reopenSession reads a single session back from a detached workspace
// database (a fresh read-only open) so a write can be asserted to have
// landed durably.
func reopenSession(t *testing.T, dataDir, id string) session.Session {
	t.Helper()
	conn, err := db.OpenReadOnly(dataDir)
	require.NoError(t, err)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close() })
	svc := session.NewService(db.New(conn), conn)
	s, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	return s
}

func newTestBackendWithRegistry(t *testing.T) *Backend {
	t.Helper()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	return b
}

// TestArchiveSession_DetachedWorkspace verifies archiving a session in a
// workspace that is registry-known but not attached writes archived_at,
// visible on reopen.
func TestArchiveSession_DetachedWorkspace(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	dataDir := newDetachedWorkspaceDB(t, "s1")
	b := newTestBackendWithRegistry(t)
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/detached", DataDir: dataDir}))

	// Empty workspaceID (no attached id) routed by root.
	require.NoError(t, b.ArchiveSession(ctx, "", "/proj/detached", "s1"))

	s := reopenSession(t, dataDir, "s1")
	require.NotZero(t, s.ArchivedAt, "archive must land in the detached workspace DB")
}

// TestMarkSessionSeen_DetachedWorkspace verifies marking a session read in a
// detached workspace bumps last_seen_at so its unread state clears.
func TestMarkSessionSeen_DetachedWorkspace(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	dataDir := newDetachedWorkspaceDB(t, "s1")
	b := newTestBackendWithRegistry(t)
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/detached", DataDir: dataDir}))

	require.True(t, reopenSession(t, dataDir, "s1").Unread(), "precondition: unread")

	require.NoError(t, b.MarkSessionSeen(ctx, "", "/proj/detached", "s1"))

	require.False(t, reopenSession(t, dataDir, "s1").Unread(), "mark-seen must clear unread in the detached DB")
}

// TestWithWorkspaceSession_DetachedBranch verifies the helper opens a
// detached workspace by root and passes a nil *Workspace plus a working
// session service.
func TestWithWorkspaceSession_DetachedBranch(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	b := newTestBackendWithRegistry(t)
	dataDir := newDetachedWorkspaceDB(t, "s1")
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/detached", DataDir: dataDir}))

	gotNil := false
	require.NoError(t, b.withWorkspaceSession(ctx, "no-such-id", "/proj/detached", func(w *Workspace, s session.Service) error {
		gotNil = w == nil
		_, err := s.Get(ctx, "s1")
		return err
	}))
	require.True(t, gotNil, "detached branch passes a nil *Workspace")
}

// TestMarkSessionSeen_AttachedByRootUsesLiveService verifies that when a
// request routes by root with an empty id but the workspace is actually
// ATTACHED in this process, the helper uses the live session service (not a
// second writable DB open), so the in-memory service observes the write.
// mark-seen is used (not archive) because the attached archive path also
// prunes snapshot refs via a checkpoint service not wired in this stub.
func TestMarkSessionSeen_AttachedByRootUsesLiveService(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir); db.ResetPool() })
	sessions := session.NewService(db.New(conn), conn)
	created, err := sessions.Create(ctx, "live")
	require.NoError(t, err)
	id := created.ID
	require.NoError(t, sessions.MarkFinished(ctx, id))
	require.True(t, mustGet(t, sessions, id).Unread(), "precondition: unread")

	b := newTestBackendWithRegistry(t)
	const root = "/proj/attached"
	ws := &Workspace{
		ID:           "ws-live",
		resolvedPath: root,
		clients:      map[string]*clientState{},
		App:          &app.App{Sessions: sessions},
	}
	b.workspaces.Set(ws.ID, ws)
	b.mu.Lock()
	b.pathIndex[root] = ws.ID
	b.mu.Unlock()

	// Empty id + attached root -> live service path.
	require.NoError(t, b.MarkSessionSeen(ctx, "", root, id))

	// The live service (same conn) must observe the write immediately.
	require.False(t, mustGet(t, sessions, id).Unread(), "mark-seen must land via the live attached service")
}

func mustGet(t *testing.T, svc session.Service, id string) session.Session {
	t.Helper()
	s, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	return s
}

// TestArchiveSession_UnknownWorkspace verifies a per-session error (not a
// panic) when neither an attached id nor a matching registry root resolves.
func TestArchiveSession_UnknownWorkspace(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	b := newTestBackendWithRegistry(t)

	err := b.ArchiveSession(ctx, "no-such-id", "/proj/missing", "s1")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

// TestArchiveSession_DetachedMissingDBFailsGracefully verifies that a
// registered workspace whose database file is absent yields a per-session
// error rather than panicking, so a batch can continue.
func TestArchiveSession_DetachedMissingDBFailsGracefully(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	b := newTestBackendWithRegistry(t)
	// Register a data dir with no crush.db in it.
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/empty", DataDir: t.TempDir()}))

	err := b.ArchiveSession(ctx, "", "/proj/empty", "s1")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

// TestArchiveSession_DetachedOneFailureDoesNotBlockOthers simulates the
// caller loop: one session routed to a missing workspace and one in a good
// detached workspace; the good one still lands even though the other fails.
func TestArchiveSession_DetachedOneFailureDoesNotBlockOthers(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	b := newTestBackendWithRegistry(t)

	dataDir := newDetachedWorkspaceDB(t, "ok")
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/ok", DataDir: dataDir}))

	// First target: a missing workspace -> per-session error.
	require.Error(t, b.ArchiveSession(ctx, "", "/proj/missing", "x"))
	// Second target: the good one still archives.
	require.NoError(t, b.ArchiveSession(ctx, "", "/proj/ok", "ok"))
	require.NotZero(t, reopenSession(t, dataDir, "ok").ArchivedAt)
}
