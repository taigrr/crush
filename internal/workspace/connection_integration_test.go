package workspace_test

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/workspace"
)

// TestClientWorkspace_ConnectionDropIsReportedAndRetried exercises the
// full stack (real HTTP/SSE server + real *client.Client, not a
// scripted fn) end to end: a live connection is forcibly severed at
// the TCP level, and the ClientWorkspace reconnect loop must report
// ConnectionStateReconnecting, then recover to Connected.
//
// Recovery works even though the backend may have torn the workspace
// down (or, after a server swap, be a different process that never
// heard of the old workspace id): on a failed subscribe the loop
// re-attaches by path via CreateWorkspace, which is first-wins by
// resolved path, and retries against whatever id the server hands back.
func TestClientWorkspace_ConnectionDropIsReportedAndRetried(t *testing.T) {
	xdgIsolate(t)
	rt := newRuntimeServer(t)

	cwd := t.TempDir()
	dataDir := t.TempDir()

	c := rt.newClient(t, cwd)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wsProto, err := c.CreateWorkspace(ctx, proto.Workspace{Path: cwd, DataDir: dataDir})
	require.NoError(t, err)

	ws := workspace.NewClientWorkspace(c, *wsProto)
	ws.SetReconnectDelayForTest(20 * time.Millisecond)

	var mu sync.Mutex
	var states []workspace.ConnectionState
	record := func(msg tea.Msg) {
		ev, ok := msg.(pubsub.Event[workspace.ConnectionEvent])
		if !ok {
			return
		}
		mu.Lock()
		states = append(states, ev.Payload.State)
		mu.Unlock()
	}
	snapshot := func() []workspace.ConnectionState {
		mu.Lock()
		defer mu.Unlock()
		return append([]workspace.ConnectionState(nil), states...)
	}

	done := make(chan struct{})
	go func() {
		ws.SubscribeLoopForTest(record)
		close(done)
	}()
	t.Cleanup(func() {
		ws.StopSubscribeLoopForTest()
		<-done
	})

	require.Eventually(t, func() bool {
		return ws.ConnectionState() == workspace.ConnectionStateConnected
	}, 5*time.Second, 10*time.Millisecond, "client never reported Connected")

	// Sever the live SSE connection at the transport level to simulate
	// a dropped connection (as opposed to a deliberate shutdown or
	// workspace switch). Detecting the drop takes a few seconds: a
	// severed TCP connection surfaces as repeated non-EOF read errors
	// in client.SubscribeEvents, which tolerates a couple of retries
	// (with a 2s sleep each) before giving up and closing the stream,
	// so give this generous headroom to avoid flaking under load.
	rt.httpSrv.CloseClientConnections()

	require.Eventually(t, func() bool {
		return ws.ConnectionState() == workspace.ConnectionStateReconnecting
	}, 15*time.Second, 10*time.Millisecond, "client never reported Reconnecting after the drop")

	// The loop must recover: the workspace is re-attached by path and
	// the stream comes back up against the (possibly new) workspace id.
	require.Eventually(t, func() bool {
		return ws.ConnectionState() == workspace.ConnectionStateConnected
	}, 10*time.Second, 10*time.Millisecond, "client never recovered to Connected after the drop")

	got := snapshot()
	require.Contains(t, got, workspace.ConnectionStateConnected)
	require.Contains(t, got, workspace.ConnectionStateReconnecting)
	reconnectingCount := 0
	for _, s := range got {
		if s == workspace.ConnectionStateReconnecting {
			reconnectingCount++
		}
	}
	require.GreaterOrEqual(t, reconnectingCount, 1, "expected at least one Reconnecting transition")
}
