package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server/devicescope"
)

// newSessionRoutesServer serves /v1 over a backend whose registry (in a
// temp dir) knows one detached workspace with session s1 and three
// messages.
func newSessionRoutesServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	srv, root := newSessionRoutesBackend(t)
	return srv.Handler(), root
}

func newSessionRoutesBackend(t *testing.T) (*Server, string) {
	t.Helper()
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "s1", Title: "Detached"})
	require.NoError(t, err)
	for i := range 3 {
		id := fmt.Sprintf("m%d", i)
		_, err = q.CreateMessage(ctx, db.CreateMessageParams{ID: id, SessionID: "s1", Role: "user", Parts: "[]"})
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, "UPDATE messages SET created_at = ? WHERE id = ?", 100+i, id)
		require.NoError(t, err)
	}
	require.NoError(t, db.Release(dataDir))
	db.ResetPool()

	srv := &Server{}
	srv.backend = backend.New(ctx, nil, func() {})
	srv.backend.SetRegistryPathForTest(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	root := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, srv.backend.AddRegistryEntryForTest(root, dataDir))
	srv.credsPath = filepath.Join(t.TempDir(), "crush.json")
	require.NoError(t, os.MkdirAll(root, 0o755))
	srv.installHandler()
	t.Cleanup(func() {
		srv.backend.ShutdownWorkspaces()
		db.ResetPool()
	})
	return srv, root
}

func doJSON(t *testing.T, h http.Handler, method, target string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequestWithContext(t.Context(), method, target, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), out), rec.Body.String())
	}
	return rec.Code
}

func TestSessionRoutesDetachedRead(t *testing.T) {
	h, root := newSessionRoutesServer(t)

	var sess proto.DeviceSession
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1", nil, &sess))
	require.Equal(t, "Detached", sess.Title)
	require.Equal(t, root, sess.Root)
	require.False(t, sess.Attached)
	require.Equal(t, 3, sess.MessageCount)

	var page proto.DeviceSessionMessages
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1/messages?limit=2", nil, &page))
	require.Len(t, page.Messages, 2)
	require.Equal(t, "m1", page.Messages[0].ID)
	require.Equal(t, "m2", page.Messages[1].ID)
	require.NotNil(t, page.NextBefore)
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1/messages?limit=2&before="+*page.NextBefore+"&root="+root, nil, &page))
	require.Len(t, page.Messages, 1)
	require.Equal(t, "m0", page.Messages[0].ID)
	require.Nil(t, page.NextBefore)

	var overviews proto.DeviceSessionOverviewPage
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/session-overviews?local=1", nil, &overviews))
	require.Equal(t, 1, overviews.Total)
	require.Equal(t, root, overviews.Sessions[0].Root)
	require.Nil(t, overviews.NextCursor)

	var roots proto.WorkspaceRoots
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/workspace-roots", nil, &roots))
	require.Len(t, roots.Roots, 1)
	require.Equal(t, 1, roots.Roots[0].SessionCount)

	var raw map[string]any
	require.Equal(t, http.StatusOK, doJSON(t, h, "POST", "/v1/history/search", proto.NodeSearchHistoryParams{Query: "zzz-none"}, &raw))
	require.Equal(t, []any{}, raw["hits"], "empty hits are [] not null")
}

func TestSessionRoutesStaleWorkspaceID(t *testing.T) {
	h, _ := newSessionRoutesServer(t)
	var perr proto.Error
	require.Equal(t, http.StatusNotFound, doJSON(t, h, "GET", "/v1/workspaces/stale-id/sessions/s1/messages", nil, &perr))
	var page proto.DeviceSessionMessages
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1/messages", nil, &page), "the session route does not need a workspace id")
	require.Len(t, page.Messages, 3)

	require.Equal(t, http.StatusNotFound, doJSON(t, h, "GET", "/v1/sessions/other/messages", nil, &perr))
	require.Equal(t, proto.ErrCodeSessionNotOnNode, perr.Code)
}

func TestSessionRoutesUnknownRootRejected(t *testing.T) {
	h, _ := newSessionRoutesServer(t)
	var perr proto.Error
	unknown := t.TempDir()
	require.Equal(t, http.StatusNotFound, doJSON(t, h, "GET", "/v1/sessions/s1?root="+unknown, nil, &perr))
	require.Equal(t, proto.ErrCodeUnknownRoot, perr.Code)
	require.Equal(t, http.StatusNotFound, doJSON(t, h, "POST", "/v1/sessions/s1/open", proto.DeviceSessionOpenParams{Root: unknown}, &perr))
	require.Equal(t, proto.ErrCodeUnknownRoot, perr.Code)
	require.Equal(t, http.StatusNotFound, doJSON(t, h, "POST", "/v1/sessions", proto.DeviceCreateSessionParams{Root: unknown, Prompt: "hi"}, &perr))
	require.Equal(t, proto.ErrCodeUnknownRoot, perr.Code)
	require.Equal(t, http.StatusBadRequest, doJSON(t, h, "POST", "/v1/sessions/s1/open", proto.DeviceSessionOpenParams{}, &perr))
	require.Equal(t, http.StatusBadRequest, doJSON(t, h, "GET", "/v1/session-overviews?cursor=%21%21", nil, &perr))
	require.Equal(t, proto.ErrCodeInvalidCursor, perr.Code)
	require.Equal(t, http.StatusBadRequest, doJSON(t, h, "GET", "/v1/sessions/s1/messages?limit=0", nil, &perr))
}

func TestSessionRoutesDeviceScopes(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"GET /v1/session-overviews":       devicescope.SessionsRead,
		"GET /v1/sessions/{sid}":          devicescope.SessionsRead,
		"GET /v1/sessions/{sid}/messages": devicescope.SessionsRead,
		"POST /v1/sessions/{sid}/open":    devicescope.SessionsPrompt,
		"POST /v1/history/search":         devicescope.SessionsRead,
		"GET /v1/workspace-roots":         devicescope.SessionsRead,
		"POST /v1/sessions":               devicescope.SessionsPrompt,
	}
	r := newDeviceRouter()
	viewer := pairing.RoleViewer.Scopes()
	for route, scope := range want {
		require.Equal(t, scope, deviceRoutes[route], route)
		m, p, _ := cutPattern(route)
		pattern, berr := r.authorize(m, concrete(p), []string{scope})
		require.Nil(t, berr, route)
		require.Equal(t, route, pattern)
		if scope == devicescope.SessionsPrompt {
			_, berr = r.authorize(m, concrete(p), viewer)
			require.NotNil(t, berr, "viewers cannot %s", route)
		}
	}
}

func TestApplyBodyRulesCreateSession(t *testing.T) {
	t.Parallel()
	d := pairing.Device{DeviceID: "dev1"}
	out, berr := applyBodyRules(routeCreateSession, d, []byte(`{"root":"/r","prompt":"hi","CLIENT_ID":"victim"}`))
	require.Nil(t, berr)
	var params proto.DeviceCreateSessionParams
	require.NoError(t, json.Unmarshal(out, &params))
	require.Equal(t, "device:dev1", params.ClientID)
	require.Equal(t, "hi", params.Prompt)
	_, berr = applyBodyRules(routeCreateSession, d, []byte(`[]`))
	require.Equal(t, codeBadRequest, berr.Code)
}

func TestBridgePassesSessionNotOnNodeCode(t *testing.T) {
	t.Parallel()
	b := &deviceBridge{}
	rec := &cappedRecorder{header: http.Header{"Content-Type": {"application/json"}}, limit: 1 << 20, status: http.StatusNotFound}
	rec.body.WriteString(`{"message":"session is not on this node","code":"session_not_on_node"}`)
	resp := b.build(bridgeRequest{}, rec)
	require.Equal(t, codeSessionNotOnNode, resp.Error.Code)
	rec = &cappedRecorder{header: http.Header{}, limit: 1 << 20, status: http.StatusNotFound}
	rec.body.WriteString(`{"message":"workspace not found"}`)
	require.Equal(t, codeNotFound, b.build(bridgeRequest{}, rec).Error.Code)
}

func isolateCrushDirs(t *testing.T) {
	t.Helper()
	isolateHome(t)
	t.Setenv("CRUSH_GLOBAL_CONFIG", t.TempDir())
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())
	t.Setenv("CRUSH_CACHE_DIR", t.TempDir())
}

func TestSessionRoutesOpenIsIdempotent(t *testing.T) {
	isolateCrushDirs(t)
	srv, root := newSessionRoutesBackend(t)
	h := srv.Handler()
	var first, second proto.DeviceSessionOpen
	require.Equal(t, http.StatusOK, doJSON(t, h, "POST", "/v1/sessions/s1/open", proto.DeviceSessionOpenParams{Root: root}, &first))
	require.NotEmpty(t, first.WorkspaceID)
	require.Equal(t, "s1", first.SessionID)
	require.Equal(t, http.StatusOK, doJSON(t, h, "POST", "/v1/sessions/s1/open", proto.DeviceSessionOpenParams{Root: root}, &second))
	require.Equal(t, first.WorkspaceID, second.WorkspaceID)

	var sess proto.DeviceSession
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1", nil, &sess))
	require.True(t, sess.Attached)
	require.Equal(t, first.WorkspaceID, sess.WorkspaceID)
	var page proto.DeviceSessionMessages
	require.Equal(t, http.StatusOK, doJSON(t, h, "GET", "/v1/sessions/s1/messages", nil, &page))
	require.Len(t, page.Messages, 3)
	require.Equal(t, first.WorkspaceID, page.WorkspaceID)
}
