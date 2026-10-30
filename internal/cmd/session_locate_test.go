package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/projects"
	"github.com/taigrr/crush/internal/session"
)

func TestSessionIDMatches(t *testing.T) {
	t.Parallel()
	id := "7f0c3b9e-1111-2222-3333-444455556666"
	require.True(t, sessionIDMatches(id, id))
	require.True(t, sessionIDMatches(id, "7f0c"))
	require.True(t, sessionIDMatches(id, session.HashID(id)[:6]))
	require.False(t, sessionIDMatches(id, ""))
	require.False(t, sessionIDMatches(id, "zzzz"))
}

func TestLocateSessionProject(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Cleanup(db.ResetPool)
	ctx := t.Context()

	makeProject := func(ids ...string) string {
		root := t.TempDir()
		dataDir := filepath.Join(root, ".crush")
		conn, err := db.Connect(ctx, dataDir)
		require.NoError(t, err)
		q := db.New(conn)
		for _, id := range ids {
			_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: id, Title: id})
			require.NoError(t, err)
		}
		require.NoError(t, db.Release(dataDir))
		require.NoError(t, projects.Register(root, dataDir))
		return root
	}

	first := makeProject("aaaa1111-0000-0000-0000-000000000000")
	second := makeProject("bbbb2222-0000-0000-0000-000000000000", "bbbb3333-0000-0000-0000-000000000000")
	db.ResetPool()

	dir, err := locateSessionProject(ctx, "aaaa1111-0000-0000-0000-000000000000")
	require.NoError(t, err)
	require.Equal(t, first, dir)

	dir, err = locateSessionProject(ctx, "bbbb2")
	require.NoError(t, err)
	require.Equal(t, second, dir)

	_, err = locateSessionProject(ctx, "bbbb")
	require.ErrorContains(t, err, "ambiguous")

	dir, err = locateSessionProject(ctx, "cccc")
	require.NoError(t, err)
	require.Empty(t, dir)
}
