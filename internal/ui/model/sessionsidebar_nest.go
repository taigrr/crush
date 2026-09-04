package model

// nestedRef is one entry of nestSpawned's output: the index into the
// input slice and the nesting depth it should render at.
type nestedRef struct {
	idx   int
	depth int
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
