package model

import (
	"sort"

	"github.com/taigrr/crush/internal/proto"
)

// nestedRef is one entry of nestSpawned's output: the index into the
// input slice and the nesting depth it should render at.
type nestedRef struct {
	idx   int
	depth int
}

// Session urgency tiers, highest last. They mirror the busy → unread
// priority sortSessions applies, with a pending prompt ranked above busy
// because a session blocked on a permission/question needs the user now.
const (
	tierIdle = iota
	tierUnread
	tierBusy
	tierPending
)

// sessionTier ranks a session by how urgently it needs attention.
func (s *SessionsSidebar) sessionTier(sess proto.SessionOverview) int {
	switch {
	case s.HasPending(sess.ID):
		return tierPending
	case sess.IsBusy:
		return tierBusy
	case sess.Unread:
		return tierUnread
	}
	return tierIdle
}

// groupedRows lays out one workspace's filtered sessions (idxs, in
// sortSessions order) for the grouped view and returns the rows to show.
//
// Spawned workers are nested under their spawner (nestSpawned), then the
// resulting top-level subtrees are ordered by their most urgent member:
// the subtree holding the active session first, then by best tier
// (pending > busy > unread > idle), then by the newest UpdatedAt in the
// subtree. Within a subtree the spawner stays first and children keep
// their sorted order. This keeps a busy worker at the top of its group
// even when its spawner is idle.
//
// The display cap is a soft budget: every pending or busy row, plus the
// spawner rows needed to show it nested, is always kept; the remaining
// slots are filled in layout order. If the must-show rows alone exceed the
// cap they are all shown anyway, so live work never hides behind the
// "… N more" overflow row.
func (s *SessionsSidebar) groupedRows(wi int, idxs []int, limit int) []nestedRef {
	sessions := s.overviews[wi].Sessions
	ids := make([]string, len(idxs))
	spawners := make([]string, len(idxs))
	for i, si := range idxs {
		ids[i], spawners[i] = sessions[si].ID, sessions[si].SpawnedBySessionID
	}
	nested := nestSpawned(ids, spawners)

	type subtree struct {
		refs    []nestedRef
		active  bool
		tier    int
		updated int64
	}
	var trees []subtree
	for _, r := range nested {
		sess := sessions[idxs[r.idx]]
		if r.depth == 0 || len(trees) == 0 {
			trees = append(trees, subtree{})
		}
		t := &trees[len(trees)-1]
		t.refs = append(t.refs, r)
		if s.activeSessionID != "" && sess.ID == s.activeSessionID {
			t.active = true
		}
		t.tier = max(t.tier, s.sessionTier(sess))
		t.updated = max(t.updated, sess.UpdatedAt)
	}
	sort.SliceStable(trees, func(i, j int) bool {
		a, b := trees[i], trees[j]
		if a.active != b.active {
			return a.active
		}
		if a.tier != b.tier {
			return a.tier > b.tier
		}
		return a.updated > b.updated
	})
	ordered := make([]nestedRef, 0, len(nested))
	for _, t := range trees {
		ordered = append(ordered, t.refs...)
	}
	if limit >= len(ordered) {
		return ordered
	}

	// Mark pending/busy rows and their ancestor chain as must-show. The
	// nested order is a depth-first walk, so the ancestors of a row are
	// the most recent rows at each shallower depth.
	must := make([]bool, len(ordered))
	mustCount := 0
	var chain []int
	for k, r := range ordered {
		if r.depth < len(chain) {
			chain = chain[:r.depth]
		}
		chain = append(chain, k)
		if s.sessionTier(sessions[idxs[r.idx]]) < tierBusy {
			continue
		}
		for _, a := range chain {
			if !must[a] {
				must[a] = true
				mustCount++
			}
		}
	}
	slots := max(0, limit-mustCount)
	shown := make([]nestedRef, 0, mustCount+slots)
	for k, r := range ordered {
		switch {
		case must[k]:
			shown = append(shown, r)
		case slots > 0:
			slots--
			shown = append(shown, r)
		}
	}
	return shown
}

// nestSpawned reorders an already-sorted list of sessions so that every
// swarm-spawned session (spawners[i] != "") sits directly under its
// spawner when the spawner is also in the list, at depth+1. Sessions
// whose spawner is absent (different workspace, archived, filtered out,
// or human-opened) keep their position at depth 0. Relative order among
// siblings is preserved, and lineage cycles (which the backend never
// writes, but a corrupt row could) are broken by treating the offending
// session as a root.
//
// ids and spawners are parallel slices; ids[i] is the session and
// spawners[i] is its SpawnedBySessionID (or "").
func nestSpawned(ids, spawners []string) []nestedRef {
	n := len(ids)
	if n == 0 {
		return nil
	}
	pos := make(map[string]int, n)
	for i, id := range ids {
		if id != "" {
			pos[id] = i
		}
	}

	// children[p] lists the input indices whose spawner is ids[p], in
	// input order so sibling order is stable.
	children := make(map[int][]int, n)
	isChild := make([]bool, n)
	for i, sp := range spawners {
		if sp == "" || sp == ids[i] {
			continue
		}
		p, ok := pos[sp]
		if !ok {
			continue
		}
		children[p] = append(children[p], i)
		isChild[i] = true
	}

	out := make([]nestedRef, 0, n)
	visited := make([]bool, n)
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if visited[i] {
			return
		}
		visited[i] = true
		out = append(out, nestedRef{idx: i, depth: depth})
		for _, c := range children[i] {
			walk(c, depth+1)
		}
	}
	for i := range ids {
		if !isChild[i] {
			walk(i, 0)
		}
	}
	// Anything left unvisited is part of a lineage cycle with no root;
	// emit it flat so no session silently disappears from the list.
	for i := range ids {
		if !visited[i] {
			walk(i, 0)
		}
	}
	return out
}
