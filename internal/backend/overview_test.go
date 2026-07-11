package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
)

// TestListWorkspaceOverviews_PeeksUnattachedRegistryWorkspace verifies a
// workspace known only from the registry (not attached) is listed by
// reading its database read-only, without attaching it.
func TestListWorkspaceOverviews_PeeksUnattachedRegistryWorkspace(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	// Build a real workspace database with one session.
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "s1", Title: "Peeked"})
	require.NoError(t, err)
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()

	// Register it and list.
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	require.NoError(t, b.registry.Add(registry.Entry{
		Root:    "/proj/peeked",
		DataDir: dataDir,
	}))

	overviews, err := b.ListWorkspaceOverviews(ctx)
	require.NoError(t, err)
	require.Len(t, overviews, 1)
	require.False(t, overviews[0].Attached)
	require.Equal(t, "/proj/peeked", overviews[0].Root)
	require.Len(t, overviews[0].Sessions, 1)
	require.Equal(t, "Peeked", overviews[0].Sessions[0].Title)
	require.False(t, overviews[0].Sessions[0].IsBusy, "unattached workspace has no live busy state")
}

// TestSortSessionOverviews verifies busy-first, then unread, then recency
// ordering.
func TestSortSessionOverviews(t *testing.T) {
	t.Parallel()
	s := []proto.SessionOverview{
		{ID: "old", UpdatedAt: 10},
		{ID: "busy", IsBusy: true, UpdatedAt: 1},
		{ID: "unread", Unread: true, UpdatedAt: 5},
		{ID: "recent", UpdatedAt: 20},
	}
	sortSessionOverviews(s)
	got := make([]string, len(s))
	for i, o := range s {
		got[i] = o.ID
	}
	require.Equal(t, []string{"busy", "unread", "recent", "old"}, got)
}
