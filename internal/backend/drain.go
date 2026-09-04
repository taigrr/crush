package backend

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/journal"
	"github.com/taigrr/crush/internal/proto"
)

// ErrDraining is returned from the run/send paths while the server is
// draining for an update: in-flight turns finish, but no new turn is
// accepted. Clients should hold the prompt and retry against the
// replacement server rather than surface this as a failure.
var ErrDraining = errors.New("server is draining for an update; no new runs accepted")

// drainPromptLogInterval is how often a draining server logs the
// sessions whose blocked permission/question prompts are holding the
// drain open, so a user wondering why an update is stuck can see it.
var drainPromptLogInterval = 30 * time.Second

// drainPollInterval bounds how long the drain waiter sleeps between
// re-checks when no run-completion wakeup arrives.
var drainPollInterval = 500 * time.Millisecond

// drainState tracks a drain in progress. It is created once by Drain
// and never reset: a draining server only ever exits.
type drainState struct {
	startedAt time.Time
	// wake is pulsed by runAgent whenever a run completes so the
	// waiter re-checks without waiting for the next poll tick.
	wake chan struct{}
	// done is set (under Backend.mu) once the drain has finished and
	// the journals have been handed off, right before the final
	// Shutdown. ShutdownWorkspaces uses it to tell a completed drain
	// (leave the journal alone) from a forced stop that interrupted
	// one (discard it, like any forced stop).
	done bool
}

// Drain puts the server in drain mode: no new agent runs are accepted
// (SendMessage returns ErrDraining), every workspace's queue dispatch is
// paused so already-queued follow-ups stay journaled for the next
// server, reads and event streams keep working, and once no run is
// active the server tears its workspaces down and invokes the shutdown
// callback, which stops HTTP and removes the socket.
//
// Drain is idempotent: repeated calls (several clients of a newer build
// connecting during the same update) return the current health snapshot
// without restarting the wait. A run blocked on a permission or question
// prompt counts as active and is never auto-denied; the waiter logs at
// Info every drainPromptLogInterval which sessions it is waiting on.
func (b *Backend) Drain() proto.Health {
	b.mu.Lock()
	if b.drain != nil {
		b.mu.Unlock()
		return b.Health()
	}
	b.drain = &drainState{startedAt: time.Now(), wake: make(chan struct{}, 1)}
	b.mu.Unlock()

	slog.Info("Server draining for update; finishing in-flight runs", "active_runs", b.ActiveRuns())
	for ws := range b.workspaces.Seq() {
		ws.pauseQueueDispatch()
	}
	go b.waitForDrain()
	return b.Health()
}

// Draining reports whether Drain has been called.
func (b *Backend) Draining() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.drain != nil
}

// Health returns the server's liveness snapshot.
func (b *Backend) Health() proto.Health {
	return proto.Health{
		Status:     "ok",
		Draining:   b.Draining(),
		ActiveRuns: b.ActiveRuns(),
	}
}

// ActiveRuns counts, across every workspace, the runs still in flight:
// the larger of the coordinator's busy-session count and the number of
// live runAgent goroutines. The goroutine count closes the gaps in the
// coordinator's view (e.g. a goal continuation between turns); the
// session count covers turns the coordinator started on its own.
func (b *Backend) ActiveRuns() int {
	n := 0
	for ws := range b.workspaces.Seq() {
		n += ws.activeRuns()
	}
	return n
}

// activeRuns is the per-workspace half of Backend.ActiveRuns.
func (w *Workspace) activeRuns() int {
	w.runMu.Lock()
	live := w.liveRuns
	w.runMu.Unlock()
	return max(live, len(w.busySessions()))
}

// signalDrain wakes the drain waiter after a run completes. No-op when
// not draining.
func (b *Backend) signalDrain() {
	b.mu.Lock()
	d := b.drain
	b.mu.Unlock()
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// waitForDrain blocks until no run is active, then shuts the server
// down with journals detached so the persisted queue and reply
// obligations survive the teardown for the next server to rehydrate.
func (b *Backend) waitForDrain() {
	b.mu.Lock()
	d := b.drain
	b.mu.Unlock()
	lastPromptLog := time.Time{}
	for {
		if b.ActiveRuns() == 0 {
			break
		}
		if time.Since(lastPromptLog) >= drainPromptLogInterval {
			b.logDrainWaiting(d)
			lastPromptLog = time.Now()
		}
		select {
		case <-d.wake:
		case <-time.After(drainPollInterval):
		case <-b.ctx.Done():
			return
		}
	}
	slog.Info("Drain complete; shutting down for update", "waited", time.Since(d.startedAt).Round(time.Millisecond))
	for ws := range b.workspaces.Seq() {
		ws.handOffJournal()
	}
	b.mu.Lock()
	d.done = true
	b.mu.Unlock()
	b.Shutdown()
}

// handOffJournal leaves the workspace's journaled queue and reply
// obligations intact for the next server: it stops write-through (so
// the teardown-time queue clear does not erase them) and writes the
// hand-off marker that tells the next server the rows were left
// deliberately and should all be replayed.
func (w *Workspace) handOffJournal() {
	w.detachJournals()
	if w.App != nil && w.Journal != nil {
		if err := w.Journal.MarkHandoff(); err != nil {
			slog.Warn("Failed to write queue hand-off marker; next server will replay only fresh rows", "workspace", w.ID, "error", err)
		}
	}
}

// logDrainWaiting reports what the drain is waiting on, calling out
// sessions blocked on a permission or question prompt separately since
// those need a human, not time.
func (b *Backend) logDrainWaiting(d *drainState) {
	var busy, prompting []string
	for ws := range b.workspaces.Seq() {
		busy = append(busy, ws.busySessions()...)
		prompting = append(prompting, ws.promptingSessions()...)
	}
	sort.Strings(busy)
	sort.Strings(prompting)
	if len(prompting) > 0 {
		slog.Info("Drain waiting on unanswered permission/question prompts; answer them to let the update proceed",
			"sessions", prompting, "waited", time.Since(d.startedAt).Round(time.Second))
	}
	slog.Info("Drain waiting on active runs", "active_runs", len(busy), "sessions", busy,
		"waited", time.Since(d.startedAt).Round(time.Second))
}

// busySessions lists the workspace's sessions with an active or accepted
// run. Coordinators that do not implement agent.Drainable fall back to
// the boolean busy predicate.
func (w *Workspace) busySessions() []string {
	if w.busySessionsFn != nil {
		return w.busySessionsFn()
	}
	if w.App == nil || w.AgentCoordinator == nil {
		return nil
	}
	if d, ok := w.AgentCoordinator.(agent.Drainable); ok {
		return d.BusySessions()
	}
	if w.AgentCoordinator.IsBusy() {
		return []string{"(unknown)"}
	}
	return nil
}

// promptingSessions lists the workspace's sessions with a permission or
// question prompt blocked waiting for a client.
func (w *Workspace) promptingSessions() []string {
	if w.App == nil {
		return nil
	}
	seen := make(map[string]struct{})
	if w.Permissions != nil {
		for _, id := range w.Permissions.PendingSessions() {
			seen[id] = struct{}{}
		}
	}
	if w.Questions != nil {
		for _, id := range w.Questions.PendingSessions() {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// pauseQueueDispatch stops the workspace's coordinator from handing
// finished turns off to queued prompts. See agent.Drainable.
func (w *Workspace) pauseQueueDispatch() {
	if w.App == nil || w.AgentCoordinator == nil {
		return
	}
	if d, ok := w.AgentCoordinator.(agent.Drainable); ok {
		d.PauseQueueDispatch()
	}
}

// detachJournals stops the workspace's coordinator from writing queue
// and reply-obligation changes to the database. See agent.Drainable.
func (w *Workspace) detachJournals() {
	if w.App == nil || w.AgentCoordinator == nil {
		return
	}
	if d, ok := w.AgentCoordinator.(agent.Drainable); ok {
		d.DetachJournals()
	}
}

// rehydrateQueue replays the prompts a previous server left journaled
// for this workspace's sessions. Per session, the tail of the queue is
// appended straight onto the coordinator's in-memory queue first (each
// entry carrying a fresh run id so it runs as its own turn, in order)
// and then only the head is dispatched: the head becomes the session's
// turn and, when it ends — however it ends, including failing at once —
// hands off to the tail through the normal queue path. Dispatching the
// head first would leave a tail appended after a fast-failing head
// orphaned on an idle session. The tail re-journals itself through the
// normal write-through; the head is no longer "queued" once accepted. If
// the head cannot be dispatched (ErrDraining from a second update, or
// the workspace closing) it is put back at the front of the live queue
// so the journal, written from the live calls, again reads head, tail.
//
// The one ordering caveat: a prompt from elsewhere that lands between
// the tail append and the head's accept runs first (the tail queues
// behind it, the head behind the tail). Replayed prompts still run in
// their journaled order relative to each other.
//
// A rehydrated prompt runs with a fresh run id — the waiter that
// supplied the original id belonged to the old process. Nothing is
// replayed while draining; the rows stay for the next server.
func (b *Backend) rehydrateQueue(ws *Workspace) {
	if ws.App == nil || ws.Journal == nil || ws.AgentCoordinator == nil || b.Draining() {
		return
	}
	if !b.mayRehydrate(ws) {
		return
	}
	ctx, cancel := context.WithTimeout(ws.ctx, 10*time.Second)
	defer cancel()
	queues, err := ws.Journal.LoadQueue(ctx)
	if err != nil {
		slog.Warn("Failed to load persisted session queue", "workspace", ws.ID, "error", err)
		return
	}
	handedOff := ws.Journal.ConsumeHandoff()
	if len(queues) == 0 {
		return
	}
	if err := ws.Journal.ClearQueues(ctx); err != nil {
		slog.Warn("Failed to clear persisted session queue before replay", "workspace", ws.ID, "error", err)
		return
	}
	// Without a hand-off marker the rows were left by a crash or a
	// forced kill, possibly long ago; replaying a days-old prompt into
	// a session the user has since moved on from would be a surprise.
	// Only rows younger than journal.ReplayTTL are replayed then.
	if !handedOff {
		now := time.Now()
		for sessionID, entries := range queues {
			kept := entries[:0]
			for _, e := range entries {
				if e.Fresh(now) {
					kept = append(kept, e)
				}
			}
			if dropped := len(entries) - len(kept); dropped > 0 {
				slog.Info("Discarding stale journaled prompts left by an unclean shutdown",
					"workspace", ws.ID, "session_id", sessionID, "dropped", dropped, "ttl", journal.ReplayTTL)
			}
			if len(kept) == 0 {
				delete(queues, sessionID)
			} else {
				queues[sessionID] = kept
			}
		}
	}
	drainable, _ := ws.AgentCoordinator.(agent.Drainable)
	sessionIDs := make([]string, 0, len(queues))
	for id := range queues {
		sessionIDs = append(sessionIDs, id)
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		entries := queues[sessionID]
		if len(entries) == 0 {
			continue
		}
		slog.Info("Replaying persisted session queue", "workspace", ws.ID, "session_id", sessionID, "prompts", len(entries))
		head, tail := entries[0], entries[1:]
		if drainable != nil {
			for _, e := range tail {
				drainable.DeferPrompt(sessionID, newRunID(), e.Prompt, e.Attachments, e.SwarmParts)
			}
		}
		if err := b.SendMessage(ws.ID, replayMessage(sessionID, head)); err != nil {
			slog.Warn("Failed to replay persisted prompt; keeping it queued", "workspace", ws.ID, "session_id", sessionID, "error", err)
			if drainable != nil {
				drainable.RequeueFront(sessionID, newRunID(), head.Prompt, head.Attachments, head.SwarmParts)
			} else if serr := ws.Journal.SaveQueue(ctx, sessionID, entries); serr != nil {
				slog.Error("Failed to re-journal undelivered prompts", "workspace", ws.ID, "session_id", sessionID, "error", serr)
			}
			continue
		}
		if drainable != nil {
			continue
		}
		// Coordinators without Drainable (test doubles): dispatch the
		// tail through SendMessage too; order is then best-effort.
		for _, e := range tail {
			if err := b.SendMessage(ws.ID, replayMessage(sessionID, e)); err != nil {
				slog.Warn("Failed to replay persisted prompt", "workspace", ws.ID, "session_id", sessionID, "error", err)
			}
		}
	}
}

// mayRehydrate reports whether ws is the right workspace to replay the
// journal in its data directory. Isolated workspaces (`crush run`) never
// are: they share the database with the project's shared workspace, so
// replaying there would run the TUI's prompts on a private coordinator
// (often with permissions skipped) and then tear them down. Likewise,
// when another live workspace in this process already uses the same
// data directory, it owns the journal.
func (b *Backend) mayRehydrate(ws *Workspace) bool {
	if ws.isolated {
		return false
	}
	dataDir := ws.Cfg.Config().Options.DataDirectory
	for other := range b.workspaces.Seq() {
		// An isolated workspace never owns the journal (see above), and
		// one without a coordinator cannot run anything; neither may
		// block the shared workspace from replaying.
		if other.ID == ws.ID || other.App == nil || other.Cfg == nil || other.isolated || other.AgentCoordinator == nil {
			continue
		}
		if other.Cfg.Config().Options.DataDirectory == dataDir {
			slog.Debug("Skipping journal replay; another live workspace owns this data directory",
				"workspace", ws.ID, "owner", other.ID, "data_dir", dataDir)
			return false
		}
	}
	return true
}

// replayMessage converts a journaled prompt into a dispatchable message
// with a fresh run id.
func replayMessage(sessionID string, e journal.QueuedPrompt) proto.AgentMessage {
	msg := proto.AgentMessage{
		SessionID:   sessionID,
		RunID:       newRunID(),
		Prompt:      e.Prompt,
		Attachments: proto.AttachmentsFromMessage(e.Attachments),
	}
	for _, p := range e.SwarmParts {
		msg.SwarmParts = append(msg.SwarmParts, proto.SwarmMessage{
			Text:              p.Text,
			Body:              p.Body,
			SenderSessionID:   p.SenderSessionID,
			SenderColor:       p.SenderColor,
			SenderAnimal:      p.SenderAnimal,
			SenderWorkspaceID: p.SenderWorkspaceID,
			BTW:               p.BTW,
			RequireReply:      p.RequireReply,
		})
	}
	return msg
}

// newRunID mints a correlator for a replayed prompt.
func newRunID() string { return uuid.New().String() }
