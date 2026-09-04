package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/fantasy"
)

// drainCoordinator is a scriptedCoordinator that also behaves like the
// real one under drain: it tracks busy sessions, queues a prompt sent to
// a busy session (journaling it through the workspace store, as the
// real sessionAgent does), and implements agent.Drainable so the
// backend can pause its dispatch and read its busy set.
type drainCoordinator struct {
	*scriptedCoordinator
	store *journal.Store

	mu     sync.Mutex
	busy   map[string]bool
	queued map[string][]string
	paused bool
	// dispatched records every prompt Run was entered with, so the
	// replay on the new server can be asserted.
	dispatched []string
}

func newDrainCoordinator(sc *scriptedCoordinator, store *journal.Store) *drainCoordinator {
	return &drainCoordinator{
		scriptedCoordinator: sc,
		store:               store,
		busy:                make(map[string]bool),
		queued:              make(map[string][]string),
	}
}

func (c *drainCoordinator) RunAccepted(ctx context.Context, accept *agent.AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	c.mu.Lock()
	c.dispatched = append(c.dispatched, prompt)
	if c.busy[sessionID] {
		c.queued[sessionID] = append(c.queued[sessionID], prompt)
		entries := make([]journal.QueuedPrompt, 0, len(c.queued[sessionID]))
		for _, p := range c.queued[sessionID] {
			entries = append(entries, journal.QueuedPrompt{SessionID: sessionID, RunID: uuid.New().String(), Prompt: p})
		}
		// Written under the lock so two concurrent enqueues cannot
		// land their snapshots out of order (the real agent serializes
		// snapshot+write the same way via journalMu).
		_ = c.store.SaveQueue(ctx, sessionID, entries)
		c.mu.Unlock()
		return nil, nil
	}
	c.busy[sessionID] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.busy, sessionID)
		c.mu.Unlock()
	}()
	return c.scriptedCoordinator.Run(ctx, sessionID, prompt, attachments...)
}

func (c *drainCoordinator) IsBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.busy) > 0
}

func (c *drainCoordinator) IsSessionBusyOrAccepted(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy[id]
}

func (c *drainCoordinator) PauseQueueDispatch() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

func (c *drainCoordinator) DetachJournals() {}

func (c *drainCoordinator) BusySessions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.busy))
	for id := range c.busy {
		out = append(out, id)
	}
	return out
}

func (c *drainCoordinator) DeferPrompt(string, string, string, []message.Attachment, []message.SwarmMessage) {
}

func (c *drainCoordinator) RequeueFront(string, string, string, []message.Attachment, []message.SwarmMessage) {
}

func (c *drainCoordinator) dispatchedPrompts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dispatched...)
}

// realWorkspaceHarness drives the real CreateWorkspace path (so the
// workspace has a real database and journal) and swaps a
// drainCoordinator into it.
type realWorkspaceHarness struct {
	*e2eHarness
	ws    *backend.Workspace
	coord *drainCoordinator
}

// newRealWorkspaceHarness attaches a real workspace. With scripted=true
// its coordinator is replaced by a drainCoordinator the test controls;
// with false the real coordinator built by CreateWorkspace is kept (so
// a replayed queue runs through production code).
func newRealWorkspaceHarness(t *testing.T, wsPath, dataDir string, scripted bool) *realWorkspaceHarness {
	t.Helper()
	h := newRealCreateHarness(t)
	h.backend.SetCreateGrace(200 * time.Millisecond)
	h.backend.SetReconnectGrace(0)
	created := h.postWorkspace(t, proto.Workspace{Path: wsPath, DataDir: dataDir, ClientID: uuid.New().String()})
	ws, err := h.backend.GetWorkspace(created.ID)
	require.NoError(t, err)
	require.NotNil(t, ws.Journal, "a real workspace must carry a journal store")

	var coord *drainCoordinator
	if scripted {
		coord = newDrainCoordinator(newScriptedCoordinator(ws.App), ws.Journal)
		ws.AgentCoordinator = coord
	}

	wsDataDir := ws.Cfg.Config().Options.DataDirectory
	backend.SetWorkspaceShutdownFnForTest(ws, func() { _ = db.Release(wsDataDir) })
	return &realWorkspaceHarness{e2eHarness: h, ws: ws, coord: coord}
}

func (h *realWorkspaceHarness) postAgent(t *testing.T, ctx context.Context, sessionID, prompt string) (int, proto.Error) {
	t.Helper()
	body, err := json.Marshal(proto.AgentMessage{SessionID: sessionID, Prompt: prompt, RunID: uuid.New().String()})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.httpSrv.URL+"/v1/workspaces/"+h.ws.ID+"/agent", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var e proto.Error
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return resp.StatusCode, e
}

func (h *e2eHarness) getHealth(t *testing.T, ctx context.Context) proto.Health {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.httpSrv.URL+"/v1/health", nil)
	require.NoError(t, err)
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out proto.Health
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func (h *e2eHarness) postDrain(t *testing.T, ctx context.Context) proto.Health {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.httpSrv.URL+"/v1/drain", nil)
	require.NoError(t, err)
	resp, err := h.httpSrv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out proto.Health
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// writeOfflineProviderConfig gives the project a single openai-compatible
// provider pointing at an unroutable local port and disables every
// default (environment-derived) provider, so a real coordinator built for
// the workspace fails its model call instantly and offline instead of
// reaching whatever credentials the developer's shell happens to carry.
func writeOfflineProviderConfig(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("CRUSH_DISABLE_DEFAULT_PROVIDERS", "true")
	const cfg = `{
  "options": {"disable_default_providers": true},
  "providers": {
    "offline": {
      "type": "openai-compat",
      "base_url": "http://127.0.0.1:1/v1",
      "api_key": "test",
      "models": [{"id": "test-model", "default_max_tokens": 4096}]
    }
  },
  "models": {
    "large": {"provider": "offline", "model": "test-model"},
    "small": {"provider": "offline", "model": "test-model"}
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(cfg), 0o644))
}

// TestE2E_DrainHandsOffQueueToNextServer is the whole graceful-update
// story on one data directory: server A has a slow run and a queued
// follow-up; it is drained; the follow-up stays journaled while the
// slow run finishes; a new prompt is refused with 503/draining; A shuts
// itself down once the run completes; server B attaches the same
// workspace, rehydrates the journaled queue and reply obligation, and
// dispatches the follow-up.
func TestE2E_DrainHandsOffQueueToNextServer(t *testing.T) {
	wsPath := t.TempDir()
	dataDir := t.TempDir()
	writeOfflineProviderConfig(t, wsPath)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	a := newRealWorkspaceHarness(t, wsPath, dataDir, true)
	cid := uuid.New().String()
	evc, killSSE := a.subscribeSSE(t, ctx, a.ws.ID, cid)
	t.Cleanup(killSSE)
	a.waitForAttachedOn(t, a.ws, 1)

	// A real session row, so the replay on server B can run it as a
	// genuine turn.
	sess, err := a.ws.Sessions.Create(ctx, "drain test")
	require.NoError(t, err)
	sid := sess.ID
	code, _ := a.postAgent(t, ctx, sid, "slow")
	require.Equal(t, http.StatusAccepted, code)
	select {
	case <-a.coord.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("slow run never entered")
	}

	// Follow-ups land behind the busy session and are journaled in
	// order.
	code, _ = a.postAgent(t, ctx, sid, "follow-up")
	require.Equal(t, http.StatusAccepted, code)
	code, _ = a.postAgent(t, ctx, sid, "follow-up-2")
	require.Equal(t, http.StatusAccepted, code)
	require.Eventually(t, func() bool {
		q, err := a.ws.Journal.LoadQueue(ctx)
		return err == nil && len(q[sid]) == 2
	}, 2*time.Second, 10*time.Millisecond, "queued follow-ups must be journaled")

	// An outstanding reply obligation, as the coordinator would record
	// for a require_reply swarm message. It is placed on an idle session
	// so the replayed turn on server B (which has no reachable model and
	// fails, clearing that session's obligations) leaves it alone.
	const owing = "s-owing"
	replies := swarm.NewPersistentReplyTracker(a.ws.Journal)
	replies.Require(owing, swarm.ReplyObligation{SenderSessionID: "parent", SenderAddress: "red-fox-1234", Body: "report back"})

	require.False(t, a.getHealth(t, ctx).Draining)
	h := a.postDrain(t, ctx)
	require.True(t, h.Draining)
	require.Equal(t, 1, h.ActiveRuns, "the slow run counts; the queued follow-up does not")
	require.True(t, a.postDrain(t, ctx).Draining, "drain is idempotent")

	code, e := a.postAgent(t, ctx, sid, "too late")
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, proto.ErrorCodeDraining, e.Code)

	time.Sleep(100 * time.Millisecond)
	require.False(t, a.shutdownHit.Load(), "server must not exit while the run is active")

	// Let the slow run finish; it must complete normally (not be
	// cancelled) and be observable on the still-open SSE stream.
	close(a.coord.release)
	pickCtx, pickCancel := context.WithTimeout(ctx, 3*time.Second)
	defer pickCancel()
	got, ok := drainUntil(pickCtx, evc, func(ev pubsub.Event[proto.Message]) bool {
		_, has := finishReason(ev.Payload)
		return ev.Payload.Role == proto.Assistant && has
	})
	require.True(t, ok, "the in-flight run must finish during drain")
	reason, _ := finishReason(got.Payload)
	require.Equal(t, proto.FinishReasonEndTurn, reason, "drain must let the run finish, not cancel it")

	require.Eventually(t, a.shutdownHit.Load, 3*time.Second, 10*time.Millisecond,
		"server must shut itself down once the last run completes")
	_, statErr := os.Stat(filepath.Join(a.ws.Cfg.Config().Options.DataDirectory, "queue-handoff"))
	require.NoError(t, statErr, "a drained server must leave the hand-off marker for its successor")
	require.Equal(t, []string{"slow", "follow-up", "follow-up-2"}, a.coord.dispatchedPrompts())
	killSSE()
	a.httpSrv.Close()

	// Server B on the same data directory. CreateWorkspace builds a real
	// coordinator and replays the journaled queue through it before the
	// harness gets a chance to swap in a scripted one, so assert on the
	// real outcome: the rows are consumed and the follow-up became a
	// user turn in the session's transcript.
	b := newRealWorkspaceHarness(t, wsPath, dataDir, false)
	_, killSSEB := b.subscribeSSE(t, ctx, b.ws.ID, uuid.New().String())
	t.Cleanup(killSSEB)
	b.waitForAttachedOn(t, b.ws, 1)
	// Replay preserves order: the head becomes the session's turn and
	// the tail is queued behind it, so both run as user turns in
	// journaled order. The offline provider fails every turn instantly;
	// an errored turn still hands off to its queue, so the tail runs
	// too and the journal ends up empty.
	userTurns := func() []string {
		msgs, err := b.ws.Messages.List(ctx, sid)
		if err != nil {
			return nil
		}
		var out []string
		for _, m := range msgs {
			if m.Role == message.User {
				out = append(out, m.Content().Text)
			}
		}
		return out
	}
	require.Eventually(t, func() bool {
		return len(userTurns()) >= 2
	}, 10*time.Second, 10*time.Millisecond, "both journaled prompts must be dispatched as turns on the new server")
	require.Equal(t, []string{"follow-up", "follow-up-2"}, userTurns(), "replay must preserve queue order")
	require.Eventually(t, func() bool {
		q, err := b.ws.Journal.LoadQueue(ctx)
		return err == nil && len(q) == 0
	}, 5*time.Second, 10*time.Millisecond, "the journal is empty once every replayed prompt has run")

	hydrated := swarm.NewPersistentReplyTracker(b.ws.Journal)
	pending := hydrated.Pending(owing)
	require.Len(t, pending, 1, "reply obligation survives the swap")
	require.Equal(t, "parent", pending[0].SenderSessionID)
	require.Equal(t, "red-fox-1234", pending[0].SenderAddress)
}
