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
// ConnectionStateReconnecting and keep retrying rather than going
// silent, then recover to Connected. Recovery works because the
// backend holds a client's entry for a reconnect-grace window after
// its last SSE stream drops (see [backend.Backend] detachStream), so
// the workspace is still there when the client retries.
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

	// The reconnect-grace window keeps the workspace alive across the
	// drop, so the loop's retries must recover the connection.
	require.Eventually(t, func() bool {
		return ws.ConnectionState() == workspace.ConnectionStateConnected
	}, 15*time.Second, 10*time.Millisecond, "client never recovered to Connected after the drop")

	got := snapshot()
	require.Contains(t, got, workspace.ConnectionStateReconnecting)
	reconnectedAfterDrop := false
	sawReconnecting := false
	for _, state := range got {
		if state == workspace.ConnectionStateReconnecting {
			sawReconnecting = true
		}
		if sawReconnecting && state == workspace.ConnectionStateConnected {
			reconnectedAfterDrop = true
		}
	}
	require.True(t, reconnectedAfterDrop, "expected a Connected event after Reconnecting, got %v", got)
}
