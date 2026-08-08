package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestArchiveSession_AttachedUsesIDPathNoRootQuery verifies an attached
// workspace id is placed in the path and no root query is sent when the
// caller omits root.
func TestArchiveSession_AttachedUsesIDPathNoRootQuery(t *testing.T) {
	t.Parallel()

	var gotPath, gotRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRoot = r.URL.Query().Get("root")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.ArchiveSession(context.Background(), "ws1", "", "sess1"))
	require.Equal(t, "/v1/workspaces/ws1/sessions/sess1/archive", gotPath)
	require.Empty(t, gotRoot)
}

// TestArchiveSession_AttachedWithRootStillUsesIDPath mirrors the real UI
// flow, where an attached target carries BOTH its workspace id and its root
// (the sidebar always sets Root). The real (UUID) id must still be placed
// in the path; the harmless root query rides along and the server ignores
// it once it resolves the id.
func TestArchiveSession_AttachedWithRootStillUsesIDPath(t *testing.T) {
	t.Parallel()

	var gotPath, gotRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRoot = r.URL.Query().Get("root")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.ArchiveSession(context.Background(), "ws1", "/proj/a", "sess1"))
	require.Equal(t, "/v1/workspaces/ws1/sessions/sess1/archive", gotPath)
	require.Equal(t, "/proj/a", gotRoot)
}

// TestArchiveSession_DetachedUsesRootQueryAndPlaceholderID verifies that an
// empty workspace id routes via the detached placeholder path segment and
// the root query parameter.
func TestArchiveSession_DetachedUsesRootQueryAndPlaceholderID(t *testing.T) {
	t.Parallel()

	var gotPath, gotRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRoot = r.URL.Query().Get("root")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.ArchiveSession(context.Background(), "", "/proj/detached", "sess1"))
	require.Equal(t, "/v1/workspaces/-/sessions/sess1/archive", gotPath)
	require.Equal(t, "/proj/detached", gotRoot)
}

// TestMarkSessionSeen_DetachedUsesRootQuery mirrors the archive routing for
// mark-as-read.
func TestMarkSessionSeen_DetachedUsesRootQuery(t *testing.T) {
	t.Parallel()

	var gotPath, gotRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRoot = r.URL.Query().Get("root")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	require.NoError(t, c.MarkSessionSeen(context.Background(), "", "/proj/detached", "sess1"))
	require.Equal(t, "/v1/workspaces/-/sessions/sess1/seen", gotPath)
	require.Equal(t, "/proj/detached", gotRoot)
}
