package cluster

import (
	"cmp"
	"fmt"
	"slices"
)

// DefaultBalanceMargin is how much higher (on the [0,1] BalanceScore
// scale) another node must score before autobalance moves a new job
// there. It keeps jobs from hopping between nodes whose load differs by
// noise.
const DefaultBalanceMargin = 0.15

// BalanceScore estimates how lightly loaded a node is, in [0,1]; higher
// is better. It blends the spare-capacity [NodeStats.FreeScore] used by
// `crush cluster migrate` with the 1-minute load average per core, which
// also reflects work outside Crush (builds, other users). Platforms
// without a load average (Windows) report zero and fall back to
// FreeScore alone.
func (s NodeStats) BalanceScore() float64 {
	free := s.FreeScore()
	if s.CPUCores <= 0 || s.Load1 <= 0 {
		return free
	}
	loadFree := 1 - clamp01(s.Load1/float64(s.CPUCores))
	return 0.5*free + 0.5*loadFree
}

// BalanceDecision is the outcome of [PickBalanceTarget].
type BalanceDecision struct {
	// Move is true when the job should move to Target.
	Move   bool
	Target NodeDashboard
	// Reason explains the decision in a short human-readable phrase.
	Reason string
}

// PickBalanceTarget decides where a new job for the project at root
// should run, given the cluster dashboard. The job stays local unless an
// online peer scores at least margin higher than this node. A project
// with a job already running on some online node goes to that node so a
// repository is never split across machines (as with migrate).
// excludeSession is the new job's own session, ignored when looking for
// the project's other jobs.
func PickBalanceTarget(nodes []NodeDashboard, root, excludeSession string, margin float64) BalanceDecision {
	selfIdx := slices.IndexFunc(nodes, func(n NodeDashboard) bool { return n.Self })
	if selfIdx < 0 {
		return BalanceDecision{Reason: "this node is not in the cluster dashboard"}
	}
	self := nodes[selfIdx]

	if pin, ok := projectHome(nodes, root, excludeSession); ok {
		if pin.Self {
			return BalanceDecision{Target: self, Reason: "the project already has a job running here"}
		}
		return BalanceDecision{Move: true, Target: pin, Reason: fmt.Sprintf("the project already has a job running on %s", pin.Member.Name)}
	}

	var candidates []NodeDashboard
	for _, n := range nodes {
		if n.Self || !n.Online || n.Stats == nil {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return BalanceDecision{Target: self, Reason: "no other node is online"}
	}
	if self.Stats == nil {
		return BalanceDecision{Target: self, Reason: "this node has no load stats yet"}
	}

	best := slices.MinFunc(candidates, compareBalance)
	selfScore, bestScore := self.Stats.BalanceScore(), best.Stats.BalanceScore()
	if bestScore-selfScore < margin {
		return BalanceDecision{Target: self, Reason: fmt.Sprintf("this node is within the margin of the least loaded node (%.2f vs %s %.2f)", selfScore, best.Member.Name, bestScore)}
	}
	return BalanceDecision{Move: true, Target: best, Reason: fmt.Sprintf("%s is less loaded (%.2f vs %.2f here)", best.Member.Name, bestScore, selfScore)}
}

// compareBalance orders nodes best first: higher score, then lower RTT,
// then node ID so ties are deterministic.
func compareBalance(a, b NodeDashboard) int {
	if c := cmp.Compare(b.Stats.BalanceScore(), a.Stats.BalanceScore()); c != 0 {
		return c
	}
	if c := cmp.Compare(rttRank(a), rttRank(b)); c != 0 {
		return c
	}
	return cmp.Compare(a.Member.NodeID, b.Member.NodeID)
}

func rttRank(n NodeDashboard) int64 {
	if n.RTTMillis < 0 {
		return 1 << 62
	}
	return n.RTTMillis
}

// projectHome returns the online node already running a job for root,
// preferring this node.
func projectHome(nodes []NodeDashboard, root, excludeSession string) (NodeDashboard, bool) {
	if root == "" {
		return NodeDashboard{}, false
	}
	var found []NodeDashboard
	for _, n := range nodes {
		if (!n.Online && !n.Self) || n.Stats == nil {
			continue
		}
		if slices.ContainsFunc(n.Stats.Jobs, func(j NodeJob) bool {
			return j.WorkspaceRoot == root && j.SessionID != excludeSession
		}) {
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return NodeDashboard{}, false
	}
	if i := slices.IndexFunc(found, func(n NodeDashboard) bool { return n.Self }); i >= 0 {
		return found[i], true
	}
	return found[0], true
}
