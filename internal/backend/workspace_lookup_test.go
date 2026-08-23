package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/home"
)

// TestResolveWorkspaceKey_ExpandsTilde verifies that a leading `~` in
// the swarm path is expanded to the home directory rather than being
// treated as a literal directory name under the current working
// directory (which previously produced a stray `.../~/...` tree).
func TestResolveWorkspaceKey_ExpandsTilde(t *testing.T) {
	t.Parallel()
	if home.Dir() == "" {
		t.Skip("home directory unavailable")
	}

	got, err := resolveWorkspaceKey(filepath.FromSlash("~/code/foss/vswarm"))
	require.NoError(t, err)

	want, err := resolveWorkspaceKey(filepath.Join(home.Dir(), "code", "foss", "vswarm"))
	require.NoError(t, err)

	require.Equal(t, want, got)
	require.NotContains(t, got, string(filepath.Separator)+"~"+string(filepath.Separator))
}

// TestResolveWorkspaceByPath_Found verifies that a path indexed under
// its canonical project root resolves to the registered workspace id.
func TestResolveWorkspaceByPath_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key, err := resolveWorkspaceKey(dir)
	require.NoError(t, err)

	b := &Backend{pathIndex: map[string]string{key: "ws-1"}}

	id, found, err := b.ResolveWorkspaceByPath(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ws-1", id)

	// The canonical root itself resolves identically.
	require.Equal(t, key, config.ProjectRoot(key))
}

// TestResolveWorkspaceByPath_NotFound verifies that an unindexed path
// reports found=false without an error.
func TestResolveWorkspaceByPath_NotFound(t *testing.T) {
	t.Parallel()
	b := &Backend{pathIndex: map[string]string{}}

	id, found, err := b.ResolveWorkspaceByPath(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, id)
}

// TestResolveWorkspaceByPath_EmptyPath verifies an empty path is
// rejected with ErrPathRequired.
func TestResolveWorkspaceByPath_EmptyPath(t *testing.T) {
	t.Parallel()
	b := &Backend{pathIndex: map[string]string{}}

	_, found, err := b.ResolveWorkspaceByPath(context.Background(), "   ")
	require.ErrorIs(t, err, ErrPathRequired)
	require.False(t, found)
}
