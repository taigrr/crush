package backend

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/transfer"
)

// DashboardBridge exposes live node health to the scheduler.
type DashboardBridge interface {
	Dashboard(ctx context.Context) []cluster.NodeDashboard
}

// Placement strategies for [Backend.PlanMigration].
const (
	StrategyCapacity = "capacity" // most spare CPU/memory first
	StrategyEven     = "even"     // round-robin
)

type migrationJob struct {
	wsID string // empty for a workspace that is not open
	sess proto.SessionOverview
}

// PlanMigration decides where each local job would go, without moving
// anything. Sessions of one project always land on the same node so a
// repository is never split across machines.
func (b *Backend) PlanMigration(ctx context.Context, req proto.MigrateRequest) (proto.MigrationPlan, error) {
	db, ok := b.clusterBridge().(DashboardBridge)
	if !ok {
		return proto.MigrationPlan{}, ErrNoCluster
	}
	self := b.selfNodeID()
	var candidates []cluster.NodeDashboard
	for _, n := range db.Dashboard(ctx) {
		if n.Self || !n.Online {
			continue
		}
		if len(req.Targets) > 0 && !slices.Contains(req.Targets, n.Member.NodeID) {
			continue
		}
		candidates = append(candidates, n)
	}
	if len(candidates) == 0 {
		return proto.MigrationPlan{}, errors.New("no online cluster nodes to migrate to")
	}

	// Collect jobs grouped by project. Running jobs only exist in open
	// workspaces; with AllSessions every owned session counts, including
	// those of workspaces that are not open right now.
	groups := map[string][]migrationJob{}
	var roots []string
	all, err := b.listAllOverviews(ctx)
	if err != nil {
		return proto.MigrationPlan{}, err
	}
	owned, _ := b.splitByOwner(all)
	for _, ov := range owned {
		for _, s := range ov.Sessions {
			if !req.AllSessions && !s.IsBusy {
				continue
			}
			if _, ok := groups[ov.Root]; !ok {
				roots = append(roots, ov.Root)
			}
			groups[ov.Root] = append(groups[ov.Root], migrationJob{wsID: ov.WorkspaceID, sess: s})
		}
	}
	slices.Sort(roots)
	// Place the biggest projects first so they get the roomiest nodes.
	slices.SortStableFunc(roots, func(a, c string) int { return cmp.Compare(len(groups[c]), len(groups[a])) })

	plan := proto.MigrationPlan{Strategy: cmp.Or(req.Strategy, StrategyCapacity), Source: self}
	sizes := map[string]int{}
	for r, g := range groups {
		sizes[r] = len(g)
	}
	placed, err := placeRoots(roots, sizes, candidates, plan.Strategy)
	if err != nil {
		return proto.MigrationPlan{}, err
	}
	for _, root := range roots {
		pick := placed[root]
		for j, job := range groups[root] {
			conflict := req.OnConflict
			if j > 0 {
				// The first move of the project carries its files; the
				// rest only need their conversations.
				conflict = transfer.ConflictKeep
			}
			plan.Moves = append(plan.Moves, proto.PlannedMove{
				WorkspaceID: job.wsID,
				Root:        root,
				SessionID:   job.sess.ID,
				Title:       job.sess.Title,
				Busy:        job.sess.IsBusy,
				TargetNode:  pick.Member.NodeID,
				TargetName:  pick.Member.Name,
				OnConflict:  conflict,
			})
		}
	}
	return plan, nil
}

// ExecuteMigration performs a plan, continuing past individual failures
// and reporting each outcome.
func (b *Backend) ExecuteMigration(ctx context.Context, plan proto.MigrationPlan, resume bool) proto.MigrationResult {
	var res proto.MigrationResult
	failedRoots := map[string]bool{}
	for _, mv := range plan.Moves {
		out := proto.MigrationOutcome{PlannedMove: mv}
		if failedRoots[mv.Root+"\x00"+mv.TargetNode] && mv.OnConflict == transfer.ConflictKeep {
			out.Error = "skipped: the project's files did not reach the destination"
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		wsID, release, err := b.openForMove(mv)
		if err != nil {
			out.Error = err.Error()
			failedRoots[mv.Root+"\x00"+mv.TargetNode] = true
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		r, err := b.MoveSession(ctx, proto.MoveSessionRequest{
			WorkspaceID: wsID,
			SessionID:   mv.SessionID,
			TargetNode:  mv.TargetNode,
			OnConflict:  mv.OnConflict,
			Resume:      resume,
		})
		release()
		if err != nil {
			out.Error = err.Error()
			failedRoots[mv.Root+"\x00"+mv.TargetNode] = true
		} else {
			out.Plan = r.Plan
			out.Resumed = r.Resumed
			res.Moved++
		}
		res.Outcomes = append(res.Outcomes, out)
	}
	return res
}

// placeRoots assigns each project root (with sizes[root] jobs, roots in
// placement order) to a candidate node.
func placeRoots(roots []string, sizes map[string]int, candidates []cluster.NodeDashboard, strategy string) (map[string]cluster.NodeDashboard, error) {
	out := map[string]cluster.NodeDashboard{}
	load := map[string]int{}
	score := func(n cluster.NodeDashboard) float64 {
		s := 0.5
		if n.Stats != nil {
			s = n.Stats.FreeScore()
		}
		return s / (1 + 0.3*float64(load[n.Member.NodeID]))
	}
	for i, root := range roots {
		var pick cluster.NodeDashboard
		switch strategy {
		case StrategyEven:
			pick = candidates[i%len(candidates)]
		case StrategyCapacity:
			pick = slices.MaxFunc(candidates, func(a, c cluster.NodeDashboard) int { return cmp.Compare(score(a), score(c)) })
		default:
			return nil, fmt.Errorf("unknown strategy %q", strategy)
		}
		load[pick.Member.NodeID] += sizes[root]
		out[root] = pick
	}
	return out, nil
}

// openForMove returns an attached workspace ID for mv, opening the
// workspace (and returning a release func) when it is not already open.
func (b *Backend) openForMove(mv proto.PlannedMove) (string, func(), error) {
	if _, err := b.GetWorkspace(mv.WorkspaceID); mv.WorkspaceID != "" && err == nil {
		return mv.WorkspaceID, func() {}, nil
	}
	clientID := uuid.NewString()
	ws, _, err := b.CreateWorkspace(proto.Workspace{Path: mv.Root, ClientID: clientID})
	if err != nil {
		return "", nil, fmt.Errorf("open %s: %w", mv.Root, err)
	}
	return ws.ID, func() { _ = b.DeleteWorkspace(ws.ID, clientID) }, nil
}
