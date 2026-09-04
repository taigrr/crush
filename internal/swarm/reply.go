package swarm

import "sync"

// ReplyObligation records that a session owes a swarm reply to the
// session that messaged it with require_reply set. The obligated
// session may not end its turn until it has sent a swarm message back
// to SenderSessionID; the coordinator nudges it and, once Nudges
// reaches the cap, replies on its behalf.
type ReplyObligation struct {
	// SenderSessionID is the session waiting for the reply.
	SenderSessionID string
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
}

// ReplyTracker is the in-memory registry of outstanding reply
// obligations, keyed by the obligated (receiving) session id. It is
// shared between the swarm tool (which fulfills obligations when the
// obligated session sends to the waiting sender) and the coordinator
// (which registers them on delivery and enforces them at end of turn).
// Obligations are process-local: they do not survive a restart.
type ReplyTracker struct {
	mu      sync.Mutex
	pending map[string][]ReplyObligation
}

// NewReplyTracker returns an empty tracker.
func NewReplyTracker() *ReplyTracker {
	return &ReplyTracker{pending: make(map[string][]ReplyObligation)}
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

// Nudge advances every obligation sessionID owes by one continuation
// turn. Obligations that still have nudges left are incremented and
// returned in due; those that have already been nudged maxNudges times
// are removed and returned in exhausted so the caller can fall back to
// replying on the agent's behalf.
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
	return due, exhausted
}

// Clear drops every obligation sessionID owes and returns them.
func (t *ReplyTracker) Clear(sessionID string) []ReplyObligation {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	obs := t.pending[sessionID]
	delete(t.pending, sessionID)
	return obs
}
