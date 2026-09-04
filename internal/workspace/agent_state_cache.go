package workspace

import (
	"context"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// agentStateTTL bounds how long a cached agent/session status answer is
// reused before the server is asked again. Events invalidate the cache
// eagerly, so the TTL is only a backstop against a missed event.
const agentStateTTL = time.Second

// agentStateCache memoizes the agent-status RPCs the TUI issues on every
// Update and View: AgentIsReady, AgentIsBusy, AgentIsSessionBusy,
// AgentQueuedPrompts. Without it a streaming turn (tens of message events
// per second, each triggering several of those calls) turns the Bubble Tea
// loop into a queue of blocking HTTP round trips and keypresses stall
// until the stream ends. Entries are dropped on any event that can change
// the answer and on every local mutation (send, cancel, clear queue).
type agentStateCache struct {
	mu       sync.Mutex
	info     *proto.AgentInfo
	infoAt   time.Time
	sessions map[string]sessionState
}

type sessionState struct {
	busy     *bool
	queued   *int
	busyAt   time.Time
	queuedAt time.Time
}

func (c *agentStateCache) invalidate() {
	c.mu.Lock()
	c.info = nil
	c.sessions = nil
	c.mu.Unlock()
}

// agentInfo returns the cached workspace-wide agent info, fetching it when
// stale. A fetch error yields nil and is not cached.
func (w *ClientWorkspace) agentInfo(ctx context.Context) *proto.AgentInfo {
	c := &w.agentState
	c.mu.Lock()
	if c.info != nil && time.Since(c.infoAt) < agentStateTTL {
		info := c.info
		c.mu.Unlock()
		return info
	}
	c.mu.Unlock()
	info, err := w.client.GetAgentInfo(ctx, w.workspaceID())
	if err != nil {
		return nil
	}
	c.mu.Lock()
	c.info, c.infoAt = info, time.Now()
	c.mu.Unlock()
	return info
}

// sessionBusy returns whether sessionID has a run in flight, cached.
func (w *ClientWorkspace) sessionBusy(ctx context.Context, sessionID string) bool {
	c := &w.agentState
	c.mu.Lock()
	if st, ok := c.sessions[sessionID]; ok && st.busy != nil && time.Since(st.busyAt) < agentStateTTL {
		busy := *st.busy
		c.mu.Unlock()
		return busy
	}
	c.mu.Unlock()
	info, err := w.client.GetAgentSessionInfo(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return false
	}
	busy := info.IsBusy
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = map[string]sessionState{}
	}
	st := c.sessions[sessionID]
	st.busy, st.busyAt = &busy, time.Now()
	c.sessions[sessionID] = st
	c.mu.Unlock()
	return busy
}

// sessionQueued returns the number of prompts queued behind sessionID's
// active turn, cached.
func (w *ClientWorkspace) sessionQueued(ctx context.Context, sessionID string) int {
	c := &w.agentState
	c.mu.Lock()
	if st, ok := c.sessions[sessionID]; ok && st.queued != nil && time.Since(st.queuedAt) < agentStateTTL {
		n := *st.queued
		c.mu.Unlock()
		return n
	}
	c.mu.Unlock()
	n, err := w.client.GetAgentSessionQueuedPrompts(ctx, w.workspaceID(), sessionID)
	if err != nil {
		return 0
	}
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = map[string]sessionState{}
	}
	st := c.sessions[sessionID]
	st.queued, st.queuedAt = &n, time.Now()
	c.sessions[sessionID] = st
	c.mu.Unlock()
	return n
}

// invalidatesAgentState reports whether an incoming server event can
// change an agent-status answer: run lifecycle (agent events, run
// completion, attention busy/idle), queue changes (a message created for a
// busy session), and agent readiness (LSP/MCP init events are not
// included; readiness is covered by agent events).
func invalidatesAgentState(ev any) bool {
	switch ev.(type) {
	case pubsub.Event[proto.AgentEvent],
		pubsub.Event[proto.RunComplete],
		pubsub.Event[proto.AttentionEvent],
		pubsub.Event[proto.Message],
		pubsub.Event[proto.Session]:
		return true
	}
	return false
}
