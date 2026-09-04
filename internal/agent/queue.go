package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/journal"
)

// journalQueue writes the session's current queue through to the
// journal, if one is attached. It reads the live queue under journalMu
// so concurrent mutations can interleave with each other freely: every
// write sees a state at least as new as the mutation that triggered it,
// and the last write always reflects the final state. Callers invoke it
// after every mutation of messageQueue. Failures are logged; the
// in-memory queue remains authoritative.
func (a *sessionAgent) journalQueue(sessionID string) {
	a.journalMu.Lock()
	defer a.journalMu.Unlock()
	if a.queueJournal == nil {
		return
	}
	queued, _ := a.messageQueue.Get(sessionID)
	entries := make([]journal.QueuedPrompt, 0, len(queued))
	for _, call := range queued {
		entries = append(entries, journal.QueuedPrompt{
			SessionID:   call.SessionID,
			RunID:       call.RunID,
			Prompt:      call.Prompt,
			Attachments: call.Attachments,
			SwarmParts:  call.SwarmParts,
		})
	}
	if err := a.queueJournal.SaveQueue(context.Background(), sessionID, entries); err != nil {
		slog.Warn("Failed to journal session queue", "session_id", sessionID, "error", err)
	}
}

// notifyDispatched runs the dispatch hook for calls leaving the queue.
// Callers must not hold the session's dispatch mutex.
func (a *sessionAgent) notifyDispatched(calls ...SessionAgentCall) {
	if a.onQueueDispatch == nil {
		return
	}
	for _, c := range calls {
		a.onQueueDispatch(c)
	}
}

// DetachQueueJournal stops writing queue changes through to the
// journal. See Drainable.DetachJournals.
func (a *sessionAgent) DetachQueueJournal() {
	a.journalMu.Lock()
	defer a.journalMu.Unlock()
	a.queueJournal = nil
}

// PauseQueueDispatch stops finished turns from handing off to queued
// prompts. See Drainable.PauseQueueDispatch.
func (a *sessionAgent) PauseQueueDispatch() {
	a.dispatchPaused.Store(true)
}

// BusySessions lists every session that has an active request or an
// accepted-but-not-yet-active run. Summarize requests are attributed to
// their session. While dispatch is NOT paused, a session with queued
// prompts also counts: a finished turn hands off to its queue through a
// window in which the session is neither active nor accepted (between
// releaseActiveOnce and the BeginAccepted in dispatchNextQueued), and a
// drain waiter observing that instant must not conclude the agent is
// idle. Once paused, queued prompts stay put and are journaled, so they
// no longer hold the drain open.
func (a *sessionAgent) BusySessions() []string {
	seen := make(map[string]struct{})
	for key, cancel := range a.activeRequests.Seq2() {
		if cancel == nil {
			continue
		}
		seen[strings.TrimSuffix(key, "-summarize")] = struct{}{}
	}
	if !a.dispatchPaused.Load() {
		for sessionID, queued := range a.messageQueue.Seq2() {
			if len(queued) > 0 {
				seen[sessionID] = struct{}{}
			}
		}
	}
	if a.acceptedRuns != nil {
		a.acceptedMu.Lock()
		for sessionID, count := range a.acceptedRuns.Seq2() {
			if count > 0 {
				seen[sessionID] = struct{}{}
			}
		}
		a.acceptedMu.Unlock()
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

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
//
// Reaching zero also wakes WaitForIdle waiters: IsBusy counts accepted
// runs, so a run canceled on entry (or failing before it becomes active)
// would otherwise leave a waiter asleep until some unrelated active run
// ended or its context expired.
func (a *sessionAgent) endAccepted(sessionID string) {
	a.acceptedMu.Lock()
	count, ok := a.acceptedRuns.Get(sessionID)
	if !ok || count <= 1 {
		a.acceptedRuns.Del(sessionID)
		a.cancelMark.Del(sessionID)
		a.acceptedMu.Unlock()
		a.signalIdle()
		return
	}
	a.acceptedRuns.Set(sessionID, count-1)
	a.acceptedMu.Unlock()
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
//
// Callers journal the queue (journalQueue) after releasing the session's
// dispatch mutex; the write is a SQLite transaction and must not run
// under a lock that Cancel needs.
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
// Calls covered by a pending cancel are dropped and returned in canceled
// so the caller can publish the terminal cancelled RunComplete for those
// that carry a RunID (a caller waiting on that RunID, e.g. `crush run`,
// would otherwise hang) and release anything they carried. Uncanceled calls without
// a RunID are returned in fold to be folded into the active turn,
// preserving the existing follow-up behavior. Uncanceled calls that carry
// a RunID are left in the queue so each runs as its own turn via the
// recursive run path and publishes its own RunComplete, giving every
// RunID-bearing prompt an explicit lifecycle instead of being silently
// absorbed into another turn. fold is processed by the caller without the
// lock held.
//
// The session's soft interrupt is re-armed under the same lock and the
// fresh channel is returned in softInterrupt for the caller to hand to
// this step's tools. Doing both under one lock acquisition is what makes
// a concurrent Steer safe: it either enqueues before the drain (and is
// folded here, closing only the previous step's channel, which nobody
// observes anymore) or after it (closing the channel returned here, so
// the tools of this step wrap up early and the next drain folds it).
func (a *sessionAgent) drainQueueForStep(sessionID string) (fold, canceled []SessionAgentCall, softInterrupt <-chan struct{}) {
	dispatchLock := a.sessionMu(sessionID)
	dispatchLock.Lock()
	softInterrupt = a.armSoftInterruptLocked(sessionID)
	queuedCalls, _ := a.messageQueue.Get(sessionID)
	var keep []SessionAgentCall
	for _, queued := range queuedCalls {
		if a.canceledBySeq(sessionID, queued.acceptSeq) {
			// Every dropped call is returned, not just RunID-bearing
			// ones: publishCanceledQueueDrops filters for the terminal
			// event itself, and the drop hook must see all of them (a
			// dropped swarm message has no RunID but does carry a reply
			// obligation to release).
			canceled = append(canceled, queued)
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
	// System notices are folded after the user's prompts and are never
	// subject to cancel filtering: nobody asked for them to run, so there
	// is nothing for a cancel to cover. They were never journaled or
	// dispatched from the queue, so only the user prompts are reported.
	asides, _ := a.pendingAsides.Take(sessionID)
	dispatchLock.Unlock()
	if len(keep) != len(queuedCalls) {
		a.journalQueue(sessionID)
	}
	a.notifyDispatched(fold...)
	fold = append(fold, asides...)
	return fold, canceled, softInterrupt
}

// reparkFold puts back fold entries that drainQueueForStep handed out but
// the caller could not persist (a createUserMessage failure aborts the
// step). Prompts go to the front of the message queue, notices back to
// pendingAsides, so nothing the user or a finished job said is lost when
// the turn errors out; the next turn's first drain picks them up again.
func (a *sessionAgent) reparkFold(sessionID string, remaining []SessionAgentCall) {
	if len(remaining) == 0 {
		return
	}
	var prompts, asides []SessionAgentCall
	for _, c := range remaining {
		if c.aside {
			asides = append(asides, c)
		} else {
			prompts = append(prompts, c)
		}
	}
	mu := a.sessionMu(sessionID)
	mu.Lock()
	if len(prompts) > 0 {
		queued, _ := a.messageQueue.Get(sessionID)
		a.messageQueue.Set(sessionID, append(prompts, queued...))
	}
	if len(asides) > 0 {
		existing, _ := a.pendingAsides.Get(sessionID)
		a.pendingAsides.Set(sessionID, append(asides, existing...))
	}
	mu.Unlock()
	if len(prompts) > 0 {
		a.journalQueue(sessionID)
	}
}

// maxPendingAsides bounds how many system notices wait per session. A
// session that never gets another turn would otherwise accumulate every
// finished job forever; beyond the cap the oldest notice is dropped.
const maxPendingAsides = 20

// notifyJobDone is the tools.JobNotifyFunc handed to tools: it parks a
// system notice for sessionID to be folded into the conversation at the
// next step boundary (or the start of the next turn when idle). It takes
// the dispatch lock so it is ordered against drainQueueForStep and a
// notice can never slip between the drain and the step it was meant for
// without being picked up by the following one.
func (a *sessionAgent) notifyJobDone(sessionID, text string) {
	if sessionID == "" || text == "" {
		return
	}
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	existing, _ := a.pendingAsides.Get(sessionID)
	existing = append(existing, SessionAgentCall{SessionID: sessionID, Prompt: text, aside: true})
	if drop := len(existing) - maxPendingAsides; drop > 0 {
		slog.Warn("Dropping oldest pending job notices", "session_id", sessionID, "dropped", drop)
		existing = existing[drop:]
	}
	a.pendingAsides.Set(sessionID, existing)
}

// armSoftInterruptLocked replaces the session's soft-interrupt channel
// with a fresh open one and returns it. Callers must hold the session's
// dispatch mutex. A previous channel that was never closed is simply
// dropped: an interrupt is scoped to the step it was raised in, so a
// later step must not inherit it.
func (a *sessionAgent) armSoftInterruptLocked(sessionID string) <-chan struct{} {
	ch := make(chan struct{})
	a.softInterrupts.Set(sessionID, ch)
	return ch
}

// softInterruptLocked closes the session's current soft-interrupt
// channel, if any, waking every tool selecting on it. Callers must hold
// the session's dispatch mutex. The closed channel is removed so a second
// call within the same step is a no-op rather than a double close; the
// next PrepareStep arms a new one.
func (a *sessionAgent) softInterruptLocked(sessionID string) {
	ch, ok := a.softInterrupts.Take(sessionID)
	if !ok || ch == nil {
		return
	}
	slog.Debug("Soft interrupt raised", "session_id", sessionID)
	close(ch)
}

// SoftInterrupt implements SessionAgent.
func (a *sessionAgent) SoftInterrupt(sessionID string) {
	mu := a.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	a.softInterruptLocked(sessionID)
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
	// Release whatever the dropped calls carried (swarm reply
	// obligations). Skipped only once the journal has been detached:
	// at that point the queue is being handed off to the next server,
	// not discarded, and its obligations travel with it. A drop that
	// happens earlier — even mid-drain, e.g. the user hits Esc — is a
	// real discard and must release them. Callers must NOT hold the
	// session's dispatch mutex: the hook may send a swarm message.
	a.journalMu.Lock()
	handedOff := a.queueJournal == nil && a.dispatchPaused.Load()
	a.journalMu.Unlock()
	if a.onQueueDrop != nil && !handedOff {
		for _, d := range drops {
			a.onQueueDrop(d)
		}
	}
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
	a.journalQueue(sessionID)
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
	if got {
		a.journalQueue(sessionID)
		a.notifyDispatched(head)
	}
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
	if len(queued) > 0 {
		a.journalQueue(sessionID)
	}
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
	// The step that owned the current soft-interrupt channel is over;
	// drop it so a SoftInterrupt on the now-idle session is a no-op and
	// the next turn starts from a clean slate. Done under the dispatch
	// mutex and only while the session is still idle: a new turn that
	// slipped in after the Del above arms its own channel under the same
	// mutex, and deleting that one would silently disarm its steers.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	if _, active := a.activeRequests.Get(sessionID); !active {
		a.softInterrupts.Del(sessionID)
	}
	mu.Unlock()
	a.signalIdle()
}

// signalIdle wakes every WaitForIdle waiter so it re-checks IsBusy. It is
// a broadcast, not a per-session signal: waiters re-evaluate the whole
// agent, so a spurious wakeup is harmless. Callers must publish the state
// change (activeRequests/acceptedRuns) BEFORE calling this; WaitForIdle
// relies on that ordering to avoid a lost wakeup.
func (a *sessionAgent) signalIdle() {
	a.idleMu.Lock()
	close(a.idleCh)
	a.idleCh = make(chan struct{})
	a.idleMu.Unlock()
}

// WaitForIdle blocks until the agent has no active or accepted runs, or
// ctx is done. It is event-driven: each released run closes the current
// idle channel, waking the waiter to re-check. Returns ctx.Err() if the
// context is canceled first.
//
// The idle channel is captured BEFORE the busy check. A release that lands
// between the two closes the captured channel (state is published before
// signalIdle), so the waiter wakes and re-checks; capturing after the
// check could grab the replacement channel and sleep through a release
// that already happened.
func (a *sessionAgent) WaitForIdle(ctx context.Context) error {
	for {
		a.idleMu.Lock()
		ch := a.idleCh
		a.idleMu.Unlock()
		if !a.IsBusy() {
			return nil
		}
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

// deferCall appends call to the session's queue without ever dispatching
// it from this process. It is used while the server drains for an
// update: the entry is journaled by the normal write-through and left
// for the next server to rehydrate. Taking the session's dispatch mutex
// serializes the append against a concurrent hand-off or fold on the
// same session, so the journal snapshot written here can neither be
// overwritten by, nor overwrite, that turn's own snapshot. The
// call's OnComplete is stripped like any queued call. The entry is
// stamped with a fresh accept sequence (reserved and released around the
// append) so a later Cancel of the active turn treats it like a prompt
// queued through the normal path — dropped only if it predates the
// cancel — rather than as an untracked entry every mark covers.
func (a *sessionAgent) deferCall(call SessionAgentCall, front bool) {
	accept := a.BeginAccepted(call.SessionID)
	call.Accepted = accept
	mu := a.sessionMu(call.SessionID)
	mu.Lock()
	if front {
		queued := call
		queued.acceptSeq = accept.seq
		queued.OnComplete = nil
		queued.Accepted = nil
		existing, _ := a.messageQueue.Get(call.SessionID)
		a.messageQueue.Set(call.SessionID, append([]SessionAgentCall{queued}, existing...))
	} else {
		a.enqueueCall(call)
	}
	// A deferred steer still hurries the active turn along (its tools
	// hand their work to the background) even though the message itself
	// waits for its own turn; on an idle session there is nothing to
	// wake and this is a no-op.
	if call.Steer && a.IsSessionBusy(call.SessionID) {
		a.softInterruptLocked(call.SessionID)
	}
	mu.Unlock()
	accept.Close()
	a.journalQueue(call.SessionID)
}
