package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// newCountingWorkspace serves the agent-status endpoints and counts hits
// so the cache's RPC behavior can be asserted.
func newCountingWorkspace(t *testing.T, busy *atomic.Bool) (*ClientWorkspace, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.URL.Path == "/v1/workspaces/ws-1/agent":
			_ = json.NewEncoder(w).Encode(proto.AgentInfo{IsReady: true, IsBusy: busy.Load()})
		case strings.HasPrefix(r.URL.Path, "/v1/workspaces/ws-1/agent/sessions/"):
			_ = json.NewEncoder(w).Encode(proto.AgentSession{IsBusy: busy.Load()})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return NewClientWorkspace(c, proto.Workspace{ID: "ws-1"}), &hits
}

// Repeated status reads within the TTL hit the server once per distinct
// question, not once per call.
func TestAgentStateCache_CoalescesReads(t *testing.T) {
	t.Parallel()
	var busy atomic.Bool
	busy.Store(true)
	ws, hits := newCountingWorkspace(t, &busy)

	for range 20 {
		require.True(t, ws.AgentIsReady())
		require.True(t, ws.AgentIsBusy())
		require.True(t, ws.AgentIsSessionBusy("s1"))
	}
	require.EqualValues(t, 2, hits.Load(), "agent info once, session info once")
}

// A server event that can change the answer drops the cache so the next
// read observes the new state; unrelated events do not.
func TestAgentStateCache_InvalidatedByEvents(t *testing.T) {
	t.Parallel()
	var busy atomic.Bool
	busy.Store(true)
	ws, hits := newCountingWorkspace(t, &busy)

	require.True(t, ws.AgentIsSessionBusy("s1"))
	busy.Store(false)
	require.True(t, ws.AgentIsSessionBusy("s1"), "still served from cache")

	evc := make(chan any, 2)
	evc <- pubsub.Event[proto.LSPEvent]{}
	evc <- pubsub.Event[proto.AgentEvent]{Payload: proto.AgentEvent{SessionID: "s1"}}
	close(evc)
	ws.consumeEvents(evc, func(tea.Msg) {})

	before := hits.Load()
	require.False(t, ws.AgentIsSessionBusy("s1"), "agent event invalidated the cache")
	require.EqualValues(t, before+1, hits.Load())
}

// Local mutations (send/cancel/clear) invalidate too, since the server
// state changes before any event could arrive.
func TestAgentStateCache_InvalidatedByLocalMutation(t *testing.T) {
	t.Parallel()
	var busy atomic.Bool
	ws, hits := newCountingWorkspace(t, &busy)

	require.False(t, ws.AgentIsSessionBusy("s1"))
	busy.Store(true)
	ws.AgentCancel("s1")
	before := hits.Load()
	require.True(t, ws.AgentIsSessionBusy("s1"))
	require.EqualValues(t, before+1, hits.Load())
}

func TestInvalidatesAgentState(t *testing.T) {
	t.Parallel()
	require.True(t, invalidatesAgentState(pubsub.Event[proto.AgentEvent]{}))
	require.True(t, invalidatesAgentState(pubsub.Event[proto.RunComplete]{}))
	require.True(t, invalidatesAgentState(pubsub.Event[proto.AttentionEvent]{}))
	require.True(t, invalidatesAgentState(pubsub.Event[proto.Message]{}))
	require.False(t, invalidatesAgentState(pubsub.Event[proto.LSPEvent]{}))
	require.False(t, invalidatesAgentState(pubsub.Event[proto.MCPEvent]{}))
}
