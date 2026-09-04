package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/agent/notify"
)

func (r *AcceptedRun) Close() {
	if r == nil {
		return
	}
	if !r.done.CompareAndSwap(false, true) {
		return
	}
	r.agent.endAccepted(r.sessionID)
}

// SessionID exposes the session this reservation is for so the run path
// can use it without an extra parameter.
func (r *AcceptedRun) SessionID() string {
	if r == nil {
		return ""
	}
	return r.sessionID
}

// BeginAccepted increments the accept counter for sessionID and returns
// a handle whose Close is the only way to decrement it. It is the only
// entry point that mutates acceptedRuns.
func (a *sessionAgent) BeginAccepted(sessionID string) *AcceptedRun {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, _ := a.acceptedRuns.Get(sessionID)
	a.acceptedRuns.Set(sessionID, count+1)
	a.acceptSeqGen++
	return &AcceptedRun{agent: a, sessionID: sessionID, seq: a.acceptSeqGen}
}

// endAccepted decrements the accept counter for sessionID. It is only
// called via AcceptedRun.Close. It uses a dedicated lock (not the
// per-session dispatch mutex) so it can run while Run holds dispatchMu
// for the same session without deadlocking.
//
// When the count reaches zero the session's cancel mark is dropped: no
// accepted handle remains for it to cover, and any handle accepted later
// gets a strictly higher sequence that the mark would not match anyway.
// Handles canceled on entry never reach RunComplete, so this is the only
// place that clears the mark for an all-canceled batch. Sibling handles
// covered by the same mark are serialized on the per-session dispatch
// mutex and read the mark before they Close, so this never clears it out
// from under a covered handle still waiting to enter Run.
func (a *sessionAgent) endAccepted(sessionID string) {
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, ok := a.acceptedRuns.Get(sessionID)
	if !ok || count <= 1 {
		a.acceptedRuns.Del(sessionID)
		a.cancelMark.Del(sessionID)
		return
	}
	a.acceptedRuns.Set(sessionID, count-1)
}

// sessionMu returns the per-session dispatch mutex, creating it on first
// use. Creation is guarded so concurrent callers always observe the same
// mutex instance for a given session.
func (a *sessionAgent) sessionMu(sessionID string) *sync.Mutex {
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	a.dispatchMuCreate.Lock()
	defer a.dispatchMuCreate.Unlock()
	if mu, ok := a.dispatchMu.Get(sessionID); ok {
		return mu
	}
	mu := &sync.Mutex{}
	a.dispatchMu.Set(sessionID, mu)
	return mu
}

// enqueueCall appends call to the session's message queue. The
// OnComplete hook is stripped: the caller that supplied it (typically
// coordinator.Run) has its own retry/coalesce scope that ends when it
// returns, so by the time the queue drains nobody is left to consume the
// buffered terminal event. The recursive Run falls back to the default
// broker publish, which is what existing subscribers expect for queued
// turns.
func (a *sessionAgent) enqueueCall(call SessionAgentCall) {
	existing, ok := a.messageQueue.Get(call.SessionID)
	if !ok {
		existing = []SessionAgentCall{}
	}
	queued := call
	if call.Accepted != nil {
		// Preserve the accept sequence after the handle is stripped so
		// the queue-drain paths can tell a follow-up queued before a
		// cancel (covered by the mark) from one queued after it.
		queued.acceptSeq = call.Accepted.seq
	}
	queued.OnComplete = nil
	queued.Accepted = nil
	existing = append(existing, queued)
	a.messageQueue.Set(call.SessionID, existing)
}

// drainQueueForStep partitions the session's queued calls for the current
// streaming step under the per-session dispatch mutex so the filtering is
// atomic against a concurrent Cancel: canceledBySeq requires the caller to
// hold that mutex, and evaluating it here (rather than after unlocking)
// prevents a cancel recorded between the drain and the check from being
// observed inconsistently.
//
// Calls covered by a pending cancel are dropped; the dropped ones that
// carry a RunID are returned in canceledWithRunID so the caller can
// publish their terminal cancelled RunComplete (a caller waiting on that
// RunID, e.g. `crush run`, would otherwise hang). Uncanceled calls without
// a RunID are returned in fold to be folded into the active turn,
// preserving the existing follow-up behavior. Uncanceled calls that carry
// a RunID are left in the queue so each runs as its own turn via the
// recursive run path and publishes its own RunComplete, giving every
// RunID-bearing prompt an explicit lifecycle instead of being silently
// absorbed into another turn. fold is processed by the caller without the
// lock held.
func (a *sessionAgent) drainQueueForStep(sessionID string) (fold, canceledWithRunID []SessionAgentCall) {
	dispatchLock := a.sessionMu(sessionID)
	dispatchLock.Lock()
	defer dispatchLock.Unlock()
	queuedCalls, _ := a.messageQueue.Get(sessionID)
	var keep []SessionAgentCall
	for _, queued := range queuedCalls {
		if a.canceledBySeq(sessionID, queued.acceptSeq) {
			if queued.RunID != "" {
				canceledWithRunID = append(canceledWithRunID, queued)
			}
			continue
		}
		if queued.RunID != "" {
			keep = append(keep, queued)
			continue
		}
		fold = append(fold, queued)
	}
	if len(keep) == 0 {
		a.messageQueue.Del(sessionID)
	} else {
		a.messageQueue.Set(sessionID, keep)
	}
	return fold, canceledWithRunID
}

// publishCanceledQueueDrops emits a terminal cancelled RunComplete for
// every dropped queued call that carries a RunID. A queued prompt removed
// from the queue without ever running — covered by a pending cancel, or
// cleared by Cancel/ClearQueue — would otherwise leave a caller blocked on
// that RunID: `crush run` ignores live message events and exits only on a
// RunComplete whose RunID matches. Calls without a RunID had no such waiter
// and are dropped silently as before. A detached, bounded context keeps the
// must-deliver publish alive even when the run context that triggered the
// drop is already canceled.
func (a *sessionAgent) publishCanceledQueueDrops(drops []SessionAgentCall) {
	var hasRunID bool
	for _, d := range drops {
		if d.RunID != "" {
			hasRunID = true
			break
		}
	}
	if !hasRunID {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, d := range drops {
		if d.RunID == "" {
			continue
		}
		a.publishRunComplete(ctx, d, notify.RunComplete{
			SessionID: d.SessionID,
			RunID:     d.RunID,
			Cancelled: true,
		})
	}
}

// clearQueueAndNotify removes all queued prompts for the session and
// publishes a terminal cancelled RunComplete for any that carried a RunID,
// so callers waiting on those RunIDs (e.g. `crush run`) are not left
// hanging when their queued prompt is discarded without running.
func (a *sessionAgent) clearQueueAndNotify(sessionID string) {
	queued, ok := a.messageQueue.Get(sessionID)
	a.messageQueue.Del(sessionID)
	if !ok {
		return
	}
	a.publishCanceledQueueDrops(queued)
}

// clearPendingCancel removes any pending-cancel mark for sessionID. It
// takes the per-session dispatch lock so it is ordered against Cancel
// and the dispatch handoff.
func (a *sessionAgent) clearPendingCancel(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	a.cancelMark.Del(sessionID)
}

// canceledBySeq reports whether an accepted handle or queued call with
// the given accept sequence is covered by a pending cancel for the
// session. Callers must hold the session's dispatch mutex. A tracked
// sequence (seq > 0) is covered only when it is at or below the cancel
// high-water mark, so a prompt accepted after the cancel (higher seq) is
// never poisoned. An untracked sequence (seq == 0, an in-process enqueue
// with no accept reservation) is covered whenever any mark is present,
// preserving the pre-sequence behavior. The mark is not consumed: it
// stays so every sibling handle it covers observes the same cancel, and
// a later handle (higher seq) ignores it regardless.
func (a *sessionAgent) canceledBySeq(sessionID string, seq uint64) bool {
	mark, ok := a.cancelMark.Get(sessionID)
	if !ok || mark == 0 {
		return false
	}
	return seq == 0 || seq <= mark
}

// persistCanceledTurn writes the user/assistant records for a turn that
// was canceled before (or just as) streaming would have produced them.
// It creates the user message only when it was not already created by an
// earlier createUserMessage call (userMsgCreated), then writes an
// assistant message with FinishReasonCanceled. Both writes use
// context.WithoutCancel(ctx) so workspace shutdown (which cancels the run
// context) can't drop them.
func (a *sessionAgent) popQueuedCall(sessionID string) (SessionAgentCall, bool) {
	var head SessionAgentCall
	var got bool
	a.messageQueue.Update(sessionID, func(q []SessionAgentCall, ok bool) ([]SessionAgentCall, bool) {
		if !ok || len(q) == 0 {
			return nil, false
		}
		head = q[0]
		got = true
		rest := q[1:]
		if len(rest) == 0 {
			return nil, false
		}
		return rest, true
	})
	return head, got
}

func (a *sessionAgent) Cancel(sessionID string) {
	// Serialize against the dispatch handoff in Run so the accepted ->
	// (cancel-on-entry | queued | active) transition is atomic against
	// this cancel. Every cancel observes at least one of: an active
	// request or an accepted run (recorded as a pending cancel). If
	// neither holds, an idle Escape is a true no-op and must not poison
	// the next prompt.
	//
	// Cancel intentionally leaves any already-queued follow-up prompts
	// in place: it only stops the turn that is currently streaming.
	// Once the active run unwinds (see the cancel handling in run.go),
	// it hands off to the next queued call exactly like a normal
	// completion would, so queued messages still run instead of being
	// silently dropped. A prompt that was accepted but not yet
	// dispatched is still covered by the cancel mark below and is
	// dropped, since it belongs to the run being canceled rather than
	// to a follow-up queued after it. Callers that want to discard the
	// queue outright should call ClearQueue explicitly.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	a.cancelLocked(sessionID)
}

// cancelLocked is the body of Cancel; callers must hold
// a.sessionMu(sessionID) for the duration of the call. Split out so
// cancelAndClearQueue can perform the cancel and the queue drop as one
// atomic operation under a single lock acquisition — CancelAll needs
// that atomicity (see cancelAndClearQueue) even though a plain Cancel
// must not touch the queue at all.
func (a *sessionAgent) cancelLocked(sessionID string) {
	// Cancel regular requests. Don't use Take() here - we need the entry to
	// remain in activeRequests so IsBusy() returns true until the goroutine
	// fully completes (including error handling that may access the DB).
	// The defer in processRequest will clean up the entry.
	if cancel, ok := a.activeRequests.Get(sessionID); ok && cancel != nil {
		slog.Debug("Request cancellation initiated", "session_id", sessionID)
		cancel()
	}

	// Also check for summarize requests.
	if cancel, ok := a.activeRequests.Get(sessionID + "-summarize"); ok && cancel != nil {
		slog.Debug("Summarize cancellation initiated", "session_id", sessionID)
		cancel()
	}

	// Record a pending cancel only when a dispatched-but-not-yet-active
	// run exists. This catches runs still in the goroutine scheduler or
	// about to enter Run's busy-queue branch, while leaving an idle
	// session untouched. Active and accepted are not mutually exclusive:
	// when a run is active and a follow-up has been accepted, both the
	// cancel above and this pending record fire.
	//
	// Raise the session's cancel mark to the latest accept sequence
	// assigned so far. Every prompt currently accepted-but-not-yet-
	// active has a sequence at or below that value, so one cancel covers
	// all of them; a prompt accepted after this cancel gets a strictly
	// higher sequence and is never poisoned. Using max keeps repeated
	// cancels idempotent while the same prompts are in flight and lets a
	// later cancel extend coverage to prompts accepted since.
	a.acceptedMu.Lock()
	count, ok := a.acceptedRuns.Get(sessionID)
	mark := a.acceptSeqGen
	a.acceptedMu.Unlock()
	if ok && count > 0 {
		slog.Debug("Recording cancel mark for accepted runs", "session_id", sessionID, "count", count, "mark", mark)
		existing, _ := a.cancelMark.Get(sessionID)
		a.cancelMark.Set(sessionID, max(existing, mark))
	}
}

func (a *sessionAgent) ClearQueue(sessionID string) {
	if a.QueuedPrompts(sessionID) > 0 {
		slog.Debug("Clearing queued prompts", "session_id", sessionID)
		a.clearQueueAndNotify(sessionID)
	}
}

// cancelAndClearQueue cancels sessionID's active/accepted run and drops
// its queue as one atomic operation under the session's dispatch mutex,
// so it can't interleave with dispatchNextQueued's own lock-protected
// dequeue-and-handoff. CancelAll uses this rather than calling Cancel and
// ClearQueue back to back (which would race: dispatchNextQueued could
// dequeue and commit to running a follow-up in the gap between the two
// separately-locked calls). A plain Cancel must not use this — it
// deliberately preserves the queue so a canceled turn can still hand off
// to it; only "stop everything" callers want the queue gone too.
func (a *sessionAgent) cancelAndClearQueue(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	a.cancelLocked(sessionID)
	queued, _ := a.messageQueue.Get(sessionID)
	a.messageQueue.Del(sessionID)
	mu.Unlock()
	a.publishCanceledQueueDrops(queued)
}

func (a *sessionAgent) CancelAll() {
	if !a.IsBusy() {
		return
	}
	// Collect every session that is busy via an active request OR an
	// accepted-but-not-yet-active run (the dispatch window). IsBusy
	// observes both, so CancelAll must too: a session busy only via an
	// accepted run has no activeRequests entry, and iterating that map
	// alone would silently skip it, leaving the run to complete
	// uncancelled. Cancel handles both cases per session (it cancels an
	// active request and records a pending cancel mark when an accepted
	// run exists).
	//
	// Unlike a single Cancel (which deliberately preserves a session's
	// queued follow-ups so they still run once the canceled turn hands
	// off), CancelAll means "stop everything now": it also drops each
	// session's queue so a canceled turn does not immediately hand off
	// to the next queued prompt and keep the session busy. Because the
	// hand-off runs concurrently with this loop, dropping the queue
	// once is not enough — a follow-up can be dequeued into a fresh
	// active turn between our snapshot and its Cancel — so this is
	// repeated on every poll until the workspace is actually idle or
	// the timeout gives up.
	cancelBusySessions := func() {
		sessions := make(map[string]struct{})
		for key := range a.activeRequests.Seq2() {
			sessions[key] = struct{}{} // key is sessionID (or sessionID-summarize)
		}
		if a.acceptedRuns != nil {
			a.acceptedMu.Lock()
			for sessionID, count := range a.acceptedRuns.Seq2() {
				if count > 0 {
					sessions[sessionID] = struct{}{}
				}
			}
			a.acceptedMu.Unlock()
		}
		for sessionID := range sessions {
			a.cancelAndClearQueue(sessionID)
		}
	}
	cancelBusySessions()

	timeout := time.After(5 * time.Second)
	for a.IsBusy() {
		select {
		case <-timeout:
			return
		default:
			time.Sleep(200 * time.Millisecond)
			cancelBusySessions()
		}
	}
}

func (a *sessionAgent) IsBusy() bool {
	for cancelFunc := range a.activeRequests.Seq() {
		if cancelFunc != nil {
			return true
		}
	}
	// A run is also busy in the dispatch window between BeginAccepted and
	// the goroutine registering its cancel in activeRequests. Without
	// this, Esc-cancel races where the user hits Escape before streaming
	// starts read IsBusy()==false and silently drop the keypress.
	if a.acceptedRuns == nil {
		return false
	}
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	for _, count := range a.acceptedRuns.Seq2() {
		if count > 0 {
			return true
		}
	}
	return false
}

func (a *sessionAgent) IsSessionBusy(sessionID string) bool {
	_, busy := a.activeRequests.Get(sessionID)
	return busy
}

// IsSessionBusyOrAccepted is the observer-facing busy predicate: true when
// sessionID has an active run OR an accepted-but-not-yet-active one. The
// dispatch window between BeginAccepted and activeRequests.Set spans
// readyWg, model resolution, and DB writes, and the AttentionBusy event
// that makes clients refresh their session overviews fires at the start
// of it — so a listing that consulted IsSessionBusy alone would read
// false and show a live turn as idle until the next unrelated refresh.
//
// Run itself must keep using the strict IsSessionBusy so a prompt is
// never queued behind its own accept reservation.
func (a *sessionAgent) IsSessionBusyOrAccepted(sessionID string) bool {
	if a.IsSessionBusy(sessionID) {
		return true
	}
	if a.acceptedRuns == nil {
		return false
	}
	a.acceptedMu.Lock()
	defer a.acceptedMu.Unlock()
	count, _ := a.acceptedRuns.Get(sessionID)
	return count > 0
}

// clearActiveRequest removes the session's active request and signals any
// WaitForIdle waiters. Every place that releases an active request must go
// through here so the idle wakeup is never missed.
func (a *sessionAgent) clearActiveRequest(sessionID string) {
	a.activeRequests.Del(sessionID)
	a.idleMu.Lock()
	close(a.idleCh)
	a.idleCh = make(chan struct{})
	a.idleMu.Unlock()
}

// WaitForIdle blocks until the agent has no active requests or ctx is done.
// It is event-driven: each cleared active request closes the current idle
// channel, waking the waiter to re-check. Returns ctx.Err() if the context
// is canceled first.
func (a *sessionAgent) WaitForIdle(ctx context.Context) error {
	for {
		if !a.IsBusy() {
			return nil
		}
		a.idleMu.Lock()
		ch := a.idleCh
		a.idleMu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *sessionAgent) QueuedPrompts(sessionID string) int {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return 0
	}
	return len(l)
}

func (a *sessionAgent) QueuedPromptsList(sessionID string) []string {
	l, ok := a.messageQueue.Get(sessionID)
	if !ok {
		return nil
	}
	prompts := make([]string, len(l))
	for i, call := range l {
		prompts[i] = call.Prompt
	}
	return prompts
}
