package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/version"
)

// autobalanceEnv turns autobalance on or off for `crush run` when the
// --balance flag is not given. It overrides cluster.autobalance.
const autobalanceEnv = "CRUSH_CLUSTER_AUTOBALANCE"

// resolveAutobalance decides whether a run balances: an explicit
// --balance flag wins, then CRUSH_CLUSTER_AUTOBALANCE, then the
// cluster.autobalance config option.
func resolveAutobalance(flagSet, flagValue bool, env string, cfg *config.ClusterConfig) bool {
	if flagSet {
		return flagValue
	}
	if env != "" {
		if v, err := strconv.ParseBool(env); err == nil {
			return v
		}
	}
	return cfg != nil && cfg.Autobalance
}

func autobalanceMargin(cfg *config.ClusterConfig) float64 {
	if cfg == nil || cfg.AutobalanceMargin == nil {
		return cluster.DefaultBalanceMargin
	}
	return max(0, min(1, *cfg.AutobalanceMargin))
}

// balanceAPI is the part of the client autobalance needs; tests fake it.
type balanceAPI interface {
	ClusterDashboard(ctx context.Context) ([]cluster.NodeDashboard, error)
	MoveSession(ctx context.Context, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error)
	// NodeConfigured reports whether nodeID has an AI provider set up,
	// so a job is not moved somewhere it cannot run.
	NodeConfigured(ctx context.Context, nodeID string) error
}

type clientBalanceAPI struct{ c *client.Client }

func (a clientBalanceAPI) ClusterDashboard(ctx context.Context) ([]cluster.NodeDashboard, error) {
	return a.c.ClusterDashboard(ctx)
}

func (a clientBalanceAPI) MoveSession(ctx context.Context, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error) {
	return a.c.MoveSession(ctx, req)
}

func (a clientBalanceAPI) NodeConfigured(ctx context.Context, nodeID string) error {
	cfg, err := a.c.ForNode(nodeID).GetGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("could not read its config: %w", err)
	}
	if !cfg.Configured {
		return errors.New("it has no AI provider configured")
	}
	return nil
}

// balanceSession moves a fresh session to the least loaded node when
// that is worthwhile. It returns the node the session now lives on, or
// ok=false when the job should keep running locally. Every outcome is
// reported on w; failures never leave the session half-moved because
// ownership only changes once the destination has accepted it.
func balanceSession(ctx context.Context, api balanceAPI, wsID, root, sessionID string, margin float64, w io.Writer) (target cluster.NodeDashboard, ok bool) {
	nodes, err := api.ClusterDashboard(ctx)
	if err != nil {
		fmt.Fprintf(w, "autobalance: cluster unavailable, running locally: %v\n", err)
		return cluster.NodeDashboard{}, false
	}
	d := cluster.PickBalanceTarget(nodes, root, sessionID, margin)
	if !d.Move {
		fmt.Fprintf(w, "autobalance: running locally (%s)\n", d.Reason)
		return cluster.NodeDashboard{}, false
	}
	name := d.Target.Member.Name
	if err := api.NodeConfigured(ctx, d.Target.Member.NodeID); err != nil {
		fmt.Fprintf(w, "autobalance: not moving to %s, running locally: %v\n", name, err)
		return cluster.NodeDashboard{}, false
	}
	fmt.Fprintf(w, "autobalance: moving to %s (%s)\n", name, d.Reason)
	if _, err := api.MoveSession(ctx, proto.MoveSessionRequest{
		WorkspaceID: wsID,
		SessionID:   sessionID,
		TargetNode:  d.Target.Member.NodeID,
	}); err != nil {
		fmt.Fprintf(w, "autobalance: move to %s failed, running locally: %v\n", name, err)
		return cluster.NodeDashboard{}, false
	}
	return d.Target, true
}

// autobalanceRun runs balanceSession for a new `crush run` session and,
// after a move, returns a client and workspace on the destination node
// with its agent ready. The returned cleanup closes that workspace.
func autobalanceRun(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session, opts runOptions) (*client.Client, *proto.Workspace, func(), error) {
	noop := func() {}
	target, moved := balanceSession(ctx, clientBalanceAPI{c}, ws.ID, ws.Path, sess.ID, autobalanceMargin(ws.Config.Cluster), os.Stderr)
	if !moved {
		return c, ws, noop, nil
	}
	rc := c.ForNode(target.Member.NodeID)
	rws, err := rc.CreateWorkspace(ctx, proto.Workspace{
		Path:    ws.Path,
		DataDir: ws.DataDir,
		Debug:   ws.Debug,
		YOLO:    ws.YOLO,
		Version: version.Version,
	})
	if err != nil {
		return nil, nil, noop, fmt.Errorf("session %s moved to %s but its workspace could not be opened there: %w", sess.ID, target.Member.Name, err)
	}
	cleanup := func() { _ = rc.DeleteWorkspace(context.Background(), rws.ID) }
	if opts.largeModel != "" || opts.smallModel != "" {
		if err := overrideModels(ctx, rc, rws, opts.largeModel, opts.smallModel); err != nil {
			cleanup()
			return nil, nil, noop, fmt.Errorf("failed to override models on %s: %w", target.Member.Name, err)
		}
	}
	if err := rc.InitiateAgentProcessing(ctx, rws.ID); err != nil {
		fmt.Fprintf(os.Stderr, "autobalance: initiating the agent on %s: %v\n", target.Member.Name, err)
	}
	if err := waitForAgent(ctx, rc, rws.ID); err != nil {
		cleanup()
		return nil, nil, noop, fmt.Errorf("session %s moved to %s but the agent there is not ready: %w", sess.ID, target.Member.Name, err)
	}
	_ = rc.UpdateAgent(ctx, rws.ID)
	return rc, rws, cleanup, nil
}
