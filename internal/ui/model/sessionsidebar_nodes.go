package model

import (
	"slices"

	"github.com/taigrr/crush/internal/proto"
)

// nodeFilterHome selects sessions on the server this client attached to.
const nodeFilterHome = "\x00home"

// nodeMatches reports whether ws passes the active node filter.
func (s *SessionsSidebar) nodeMatches(ws proto.WorkspaceOverview) bool {
	switch s.nodeFilter {
	case "":
		return true
	case nodeFilterHome:
		return ws.NodeID == ""
	default:
		return ws.NodeID == s.nodeFilter
	}
}

// clusterNodes returns the distinct remote nodes present in the
// overviews, in first-seen order, as (id, name) pairs.
func (s *SessionsSidebar) clusterNodes() [][2]string {
	var out [][2]string
	for _, ws := range s.overviews {
		if ws.NodeID == "" || slices.ContainsFunc(out, func(n [2]string) bool { return n[0] == ws.NodeID }) {
			continue
		}
		out = append(out, [2]string{ws.NodeID, ws.NodeName})
	}
	return out
}

// HasRemoteNodes reports whether any session lives on another node.
func (s *SessionsSidebar) HasRemoteNodes() bool { return len(s.clusterNodes()) > 0 }

// CycleNodeFilter advances the node filter: all nodes, this machine,
// then each remote node, then back to all.
func (s *SessionsSidebar) CycleNodeFilter() {
	opts := []string{"", nodeFilterHome}
	for _, n := range s.clusterNodes() {
		opts = append(opts, n[0])
	}
	i := slices.Index(opts, s.nodeFilter)
	s.nodeFilter = opts[(i+1)%len(opts)]
	prev := s.selectedSessionID()
	s.rebuildRows()
	s.restoreCursor(prev)
}

// nodeFilterLabel names the active node filter for the title ("" when
// showing every node).
func (s *SessionsSidebar) nodeFilterLabel() string {
	switch s.nodeFilter {
	case "":
		return ""
	case nodeFilterHome:
		return "this machine"
	}
	for _, n := range s.clusterNodes() {
		if n[0] == s.nodeFilter {
			return "@" + n[1]
		}
	}
	return "@" + s.nodeFilter
}

// SelectedSessionOverview returns the session row under the cursor with
// its workspace.
func (s *SessionsSidebar) SelectedSessionOverview() (proto.WorkspaceOverview, proto.SessionOverview, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return proto.WorkspaceOverview{}, proto.SessionOverview{}, false
	}
	r := s.rows[s.cursor]
	if r.kind != sidebarRowSession {
		return proto.WorkspaceOverview{}, proto.SessionOverview{}, false
	}
	ws := s.overviews[r.wsIdx]
	return ws, ws.Sessions[r.sessIdx], true
}

// NeighborSessions returns the (root, session ID) of the session rows just
// above and below the cursor, for prefetching.
func (s *SessionsSidebar) NeighborSessions() [][2]string {
	var out [][2]string
	for _, dir := range []int{-1, 1} {
		for i := s.cursor + dir; i >= 0 && i < len(s.rows); i += dir {
			r := s.rows[i]
			if r.kind != sidebarRowSession {
				continue
			}
			ws := s.overviews[r.wsIdx]
			out = append(out, [2]string{ws.Root, ws.Sessions[r.sessIdx].ID})
			break
		}
	}
	return out
}
