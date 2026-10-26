package workspace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
)

// TestGetActiveWorktreeCachesPerSession guards the render path: the header
// and sidebar ask for a session's worktree on every frame, including for
// sessions other than the active one, so "no worktree" answers must be
// cached per session instead of hitting the server each time.
func TestGetActiveWorktreeCachesPerSession(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	ws := NewClientWorkspace(c, proto.Workspace{ID: "ws-1"})
	ws.SetActiveSessionID("active")

	for range 50 {
		wt, err := ws.GetActiveWorktree(t.Context(), "previewed")
		require.NoError(t, err)
		require.Nil(t, wt, "no worktree is a nil result, not an error")
		_, err = ws.GetActiveWorktree(t.Context(), "active")
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), hits.Load(), "one server call per session")

	ws.invalidateWorktreeCache()
	_, err = ws.GetActiveWorktree(t.Context(), "active")
	require.NoError(t, err)
	require.Equal(t, int32(3), hits.Load())
}
