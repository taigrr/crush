package swarm

import (
	"log/slog"
	"sync"
)

// ReplyObligation records that a session owes a swarm reply to the
// session that messaged it with require_reply set. The obligated
// session may not end its turn until it has sent a swarm message back
// to SenderSessionID; the coordinator nudges it and, once Nudges
// reaches the cap, replies on its behalf.
type ReplyObligation struct {
	// SenderSessionID is the session waiting for the reply.
	SenderSessionID string
	// SenderWorkspaceID is the workspace the waiting session lives in,
	// when known. Informational; the reply is routed by session id.
	SenderWorkspaceID string
	// SenderAddress is the sender's formatted color-animal-shorthash
	// address, the string the obligated agent should pass to the
	// swarm tool.
	SenderAddress string
	// Body is the un-prefixed message that carried the requirement,
	// restated in nudges so the agent knows what it is replying to.
	Body string
	// Nudges counts how many continuation turns have already been
	// spent reminding the agent to reply.
	Nudges int
	// Undelivered is true while the message that carries the
	// requirement is still queued and the agent has not seen it (a
	// swarm send deferred during a server drain, or a replayed queue
	// tail). The obligation is recorded so it survives a restart, but
	// it is not enforced — no nudges, no failure forwarding — until
	// MarkDelivered flips it when the queued message actually runs.
	Undelivered bool
}

// ReplyJournal persists the obligations a session owes so they survive
// a server restart. Save is called with the full, current set for the
// session after every mutation (an empty set means "delete"); Load
// returns everything on disk keyed by obligated session id.
type ReplyJournal interface {
	SaveReplies(sessionID string, obs []ReplyObligation) error
	LoadReplies() (map[string][]ReplyObligation, error)
}

// ReplyTracker is the in-memory registry of outstanding reply
// obligations, keyed by the obligated (receiving) session id. It is
// shared between the swarm tool (which fulfills obligations when the
// obligated session sends to the waiting sender) and the coordinator
// (which registers them on delivery and enforces them at end of turn).
// The in-memory map is the source of truth at runtime; when a
// [ReplyJournal] is attached every mutation is written through to it
// so the next server can rehydrate via [ReplyTracker.Hydrate].
type ReplyTracker struct {
	mu      sync.Mutex
	pending map[string][]ReplyObligation
	journal ReplyJournal
}

// NewReplyTracker returns an empty tracker.
func NewReplyTracker() *ReplyTracker {
	return &ReplyTracker{pending: make(map[string][]ReplyObligation)}
}

// NewPersistentReplyTracker returns a tracker that writes through to
// journal and starts out holding whatever journal has on disk.
func NewPersistentReplyTracker(journal ReplyJournal) *ReplyTracker {
	t := NewReplyTracker()
	t.journal = journal
	t.Hydrate()
	return t
}

// Hydrate merges the journal's obligations into the tracker: every
// session present in the journal has its pending list replaced by the
// persisted one; sessions the journal does not mention are untouched. A
// nil journal or a load failure leaves the tracker as it was.
func (t *ReplyTracker) Hydrate() {
	if t == nil || t.journal == nil {
		return
	}
	loaded, err := t.journal.LoadReplies()
	if err != nil {
		slog.Warn("Failed to load persisted swarm reply obligations", "error", err)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for sessionID, obs := range loaded {
		if len(obs) > 0 {
			t.pending[sessionID] = obs
		}
	}
}

// DetachJournal stops writing through to the journal. Used right before
// a drained server tears its workspaces down so the teardown-time Clear
// does not erase obligations the next server should pick up.
func (t *ReplyTracker) DetachJournal() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.journal = nil
}

// persist writes the session's current obligations through to the
// journal. Callers must hold t.mu.
func (t *ReplyTracker) persist(sessionID string) {
	if t.journal == nil {
		return
	}
	if err := t.journal.SaveReplies(sessionID, t.pending[sessionID]); err != nil {
		slog.Warn("Failed to persist swarm reply obligations", "session_id", sessionID, "error", err)
	}
}

// Require records that sessionID owes a reply to ob.SenderSessionID.
// A second requirement from the same sender replaces the first (and
// resets its nudge count) rather than stacking, so a parent that
// messages twice is owed one reply, not two. Self-addressed or
// sender-less obligations are ignored.
func (t *ReplyTracker) Require(sessionID string, ob ReplyObligation) {
	if t == nil || sessionID == "" || ob.SenderSessionID == "" || ob.SenderSessionID == sessionID {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.persist(sessionID)
	obs := t.pending[sessionID]
	for i := range obs {
		if obs[i].SenderSessionID == ob.SenderSessionID {
			obs[i] = ob
			t.pending[sessionID] = obs
			return
		}
	}
	t.pending[sessionID] = append(obs, ob)
}

// MarkDelivered flips the obligation sessionID owes to senderSessionID
// to delivered, making it enforceable. No-op if none exists.
func (t *ReplyTracker) MarkDelivered(sessionID, senderSessionID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	for i := range obs {
		if obs[i].SenderSessionID == senderSessionID && obs[i].Undelivered {
			obs[i].Undelivered = false
			t.pending[sessionID] = obs
			t.persist(sessionID)
			return
		}
	}
}

// Due returns a copy of the delivered (enforceable) obligations
// sessionID owes.
func (t *ReplyTracker) Due(sessionID string) []ReplyObligation {
	var out []ReplyObligation
	for _, ob := range t.Pending(sessionID) {
		if !ob.Undelivered {
			out = append(out, ob)
		}
	}
	return out
}

// Fulfill clears any obligation sessionID owes to targetSessionID and
// reports whether one existed. Called when sessionID sends a swarm
// message to targetSessionID.
func (t *ReplyTracker) Fulfill(sessionID, targetSessionID string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	kept := obs[:0]
	found := false
	for _, ob := range obs {
		if ob.SenderSessionID == targetSessionID {
			found = true
			continue
		}
		kept = append(kept, ob)
	}
	if len(kept) == 0 {
		delete(t.pending, sessionID)
	} else {
		t.pending[sessionID] = kept
	}
	if found {
		t.persist(sessionID)
	}
	return found
}

// Pending returns a copy of the obligations sessionID currently owes.
func (t *ReplyTracker) Pending(sessionID string) []ReplyObligation {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	if len(obs) == 0 {
		return nil
	}
	out := make([]ReplyObligation, len(obs))
	copy(out, obs)
	return out
}

// Nudge advances every delivered obligation sessionID owes by one
// continuation turn. Obligations that still have nudges left are
// incremented and returned in due; those that have already been nudged
// maxNudges times are removed and returned in exhausted so the caller
// can fall back to replying on the agent's behalf. Undelivered
// obligations are kept untouched and not returned.
func (t *ReplyTracker) Nudge(sessionID string, maxNudges int) (due, exhausted []ReplyObligation) {
	if t == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	if len(obs) == 0 {
		return nil, nil
	}
	kept := obs[:0]
	for _, ob := range obs {
		if ob.Undelivered {
			kept = append(kept, ob)
			continue
		}
		if ob.Nudges >= maxNudges {
			exhausted = append(exhausted, ob)
			continue
		}
		ob.Nudges++
		kept = append(kept, ob)
		due = append(due, ob)
	}
	if len(kept) == 0 {
		delete(t.pending, sessionID)
	} else {
		t.pending[sessionID] = kept
	}
	t.persist(sessionID)
	return due, exhausted
}

// Clear drops every delivered obligation sessionID owes and returns
// them. Undelivered ones belong to a message that has not run yet and
// are kept: the turn that just ended never saw it, so its outcome says
// nothing about it.
func (t *ReplyTracker) Clear(sessionID string) []ReplyObligation {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	var cleared, kept []ReplyObligation
	for _, ob := range obs {
		if ob.Undelivered {
			kept = append(kept, ob)
		} else {
			cleared = append(cleared, ob)
		}
	}
	if len(kept) == 0 {
		delete(t.pending, sessionID)
	} else {
		t.pending[sessionID] = kept
	}
	if len(cleared) > 0 {
		t.persist(sessionID)
	}
	return cleared
}
