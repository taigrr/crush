package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
)

// TestSubscribeLoopRecreatesMissingWorkspace covers a server restart: the
// cached workspace ID 404s, so the client must open the workspace again
// rather than polling the dead ID forever.
func TestSubscribeLoopRecreatesMissingWorkspace(t *testing.T) {
	t.Parallel()
	var staleHits, creates atomic.Int32
	var created proto.Workspace
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workspaces/ws-old/events":
			staleHits.Add(1)
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces":
			creates.Add(1)
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(proto.Workspace{ID: "ws-new", Path: created.Path})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workspaces/ws-new/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	w := NewClientWorkspace(c, proto.Workspace{ID: "ws-old", Path: "/proj", YOLO: true})
	w.SetReconnectDelayForTest(time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.SubscribeLoopForTest(func(tea.Msg) {})
	}()
	require.Eventually(t, func() bool {
		return w.workspaceID() == "ws-new" && w.ConnectionStateForTest() == ConnectionStateConnected
	}, 5*time.Second, 5*time.Millisecond)
	w.StopSubscribeLoopForTest()
	<-done

	require.Equal(t, int32(1), staleHits.Load())
	require.Equal(t, int32(1), creates.Load())
	require.Equal(t, "/proj", created.Path)
	require.True(t, created.YOLO)
}
