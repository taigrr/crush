package backend

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
)

// newDeviceTestDB builds a detached workspace database with sessions
// (updated_at = their index) and n messages in the first session.
func newDeviceTestDB(t *testing.T, sessions []string, messages int) string {
	t.Helper()
	ctx := t.Context()
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	// Pin updated_at: the trigger would stamp the wall clock.
	_, err = conn.ExecContext(ctx, "DROP TRIGGER IF EXISTS update_sessions_updated_at")
	require.NoError(t, err)
	for i, id := range sessions {
		_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: id, Title: "title " + id})
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE id = ?", 1000+i, id)
		require.NoError(t, err)
	}
	for i := range messages {
		id := fmt.Sprintf("m%03d", i)
		_, err = q.CreateMessage(ctx, db.CreateMessageParams{ID: id, SessionID: sessions[0], Role: "user", Parts: "[]"})
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ? WHERE id = ?", 100+i, id)
		require.NoError(t, err)
	}
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()
	return dataDir
}

func newDeviceTestBackend(t *testing.T, roots map[string]string) *Backend {
	t.Helper()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	for root, dataDir := range roots {
		require.NoError(t, b.registry.Add(registry.Entry{Root: root, DataDir: dataDir}))
	}
	return b
}

func TestDeviceSessionDetachedRead(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	dataDir := newDeviceTestDB(t, []string{"s1"}, 5)
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": dataDir})

	for _, root := range []string{"", "/proj/a"} {
		sess, err := b.DeviceSession(ctx, "s1", root)
		require.NoError(t, err)
		require.Equal(t, "title s1", sess.Title)
		require.Equal(t, "/proj/a", sess.Root)
		require.False(t, sess.Attached)
		require.Empty(t, sess.WorkspaceID)
		require.Equal(t, 5, sess.MessageCount)
	}

	msgs, next, wsID, err := b.DeviceSessionMessages(ctx, "s1", "", 2, "")
	require.NoError(t, err)
	require.Empty(t, wsID)
	require.Equal(t, []string{"m003", "m004"}, messageIDs(msgs))
	require.Equal(t, "m003", next)
	msgs, next, _, err = b.DeviceSessionMessages(ctx, "s1", "/proj/a", 2, next)
	require.NoError(t, err)
	require.Equal(t, []string{"m001", "m002"}, messageIDs(msgs))
	msgs, next, _, err = b.DeviceSessionMessages(ctx, "s1", "", 2, next)
	require.NoError(t, err)
	require.Equal(t, []string{"m000"}, messageIDs(msgs))
	require.Empty(t, next)

	_, _, _, err = b.DeviceSessionMessages(ctx, "s1", "", 2, "nope")
	require.ErrorIs(t, err, ErrInvalidCursor)
	_, err = b.DeviceSession(ctx, "missing", "")
	require.ErrorIs(t, err, ErrSessionNotOnNode)
}

func TestDeviceSessionUnknownRootRejected(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	dataDir := newDeviceTestDB(t, []string{"s1"}, 0)
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": dataDir})

	_, err := b.DeviceSession(ctx, "s1", "/proj/other")
	require.ErrorIs(t, err, ErrUnknownRoot)
	_, err = b.OpenDeviceSession(ctx, "s1", t.TempDir())
	require.ErrorIs(t, err, ErrUnknownRoot, "an unregistered root is never attached")
	_, err = b.OpenDeviceSession(ctx, "s1", "")
	require.ErrorIs(t, err, ErrUnknownRoot)
	require.Zero(t, b.workspaces.Len())
}

func TestDeviceSessionReplicaIsNotOnNode(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	dataDir := newDeviceTestDB(t, []string{"s1"}, 0)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "UPDATE sessions SET owner_node = 'elsewhere' WHERE id = 's1'")
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": dataDir})
	_, err = b.DeviceSession(ctx, "s1", "")
	require.ErrorIs(t, err, ErrSessionNotOnNode)
}

func TestSessionOverviewPaging(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	a := newDeviceTestDB(t, []string{"a1", "a2", "a3"}, 0)
	c := newDeviceTestDB(t, []string{"c1", "c2"}, 0)
	b := newDeviceTestBackend(t, map[string]string{"/proj/a": a, "/proj/c": c})

	var got []string
	cursor := ""
	for range 10 {
		page, err := b.ListSessionOverviewPage(ctx, true, 2, cursor)
		require.NoError(t, err)
		require.Equal(t, 5, page.Total)
		require.NotNil(t, page.Unavailable)
		for _, s := range page.Sessions {
			got = append(got, s.ID)
			require.False(t, s.Attached)
			require.Empty(t, s.WorkspaceID)
			require.NotEmpty(t, s.Root)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	// updated_at desc (index order per database), ties broken by id.
	require.Equal(t, []string{"a3", "a2", "c2", "a1", "c1"}, got)

	// The cursor is a keyset position: a session updated after the first
	// page was read moves to the top without shifting later pages.
	first, err := b.ListSessionOverviewPage(ctx, true, 2, "")
	require.NoError(t, err)
	require.Equal(t, []string{"a3", "a2"}, overviewIDs(first.Sessions))
	conn, err := db.Connect(ctx, c)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "UPDATE sessions SET updated_at = 5000 WHERE id = 'c1'")
	require.NoError(t, err)
	require.NoError(t, db.Release(c))
	db.ResetPool()
	b.peekCache.reset()
	second, err := b.ListSessionOverviewPage(ctx, true, 2, *first.NextCursor)
	require.NoError(t, err)
	require.Equal(t, []string{"c2", "a1"}, overviewIDs(second.Sessions))

	_, err = b.ListSessionOverviewPage(ctx, true, 2, "!!")
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestWorkspaceRoots(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	a := newDeviceTestDB(t, []string{"a1", "a2"}, 0)
	c := newDeviceTestDB(t, []string{"c1"}, 0)
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/a", DataDir: a, LastUsed: 10}))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/c", DataDir: c, LastUsed: 20}))

	roots, err := b.WorkspaceRoots(ctx)
	require.NoError(t, err)
	require.Len(t, roots.Roots, 2)
	require.Equal(t, "/proj/c", roots.Roots[0].Root)
	require.Equal(t, 1, roots.Roots[0].SessionCount)
	require.Equal(t, 2, roots.Roots[1].SessionCount)
	require.False(t, roots.Roots[0].Attached)
}

func TestSearchNodeHistoryEmptyHits(t *testing.T) {
	t.Cleanup(db.ResetPool)
	b := newDeviceTestBackend(t, nil)
	res, err := b.SearchNodeHistory(t.Context(), proto0())
	require.NoError(t, err)
	require.NotNil(t, res.Hits)
	require.Empty(t, res.Hits)
}
