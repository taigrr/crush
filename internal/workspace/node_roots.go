package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// nodeRootPrefix marks a workspace root that lives on a cluster node
// other than the one this client currently targets:
// "crush+node://<node-id>/abs/path". The home server is spelled
// [HomeNodeRef] so it can be addressed while targeting a remote node.
const (
	nodeRootPrefix = "crush+node://"
	HomeNodeRef    = "home"
)

// QualifyNodeRoot returns root qualified with nodeRef.
func QualifyNodeRoot(nodeRef, root string) string {
	return nodeRootPrefix + nodeRef + root
}

// ParseNodeRoot splits a qualified root into its node reference and
// plain root. ok is false for plain roots.
func ParseNodeRoot(s string) (nodeRef, root string, ok bool) {
	rest, ok := strings.CutPrefix(s, nodeRootPrefix)
	if !ok {
		return "", s, false
	}
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, "", true
	}
	return rest[:idx], rest[idx:], true
}

// DisplayRoot strips any node qualification from root.
func DisplayRoot(s string) string {
	_, root, _ := ParseNodeRoot(s)
	return root
}

// NodeID returns the cluster node this workspace currently targets; ""
// means the home server the client attached to.
func (w *ClientWorkspace) NodeID() string {
	if c := w.c(); c != nil {
		return c.NodeID()
	}
	return ""
}

// clientForNode returns a client for nodeID ("" = home).
func (w *ClientWorkspace) clientForNode(nodeID string) *client.Client {
	if nodeID == "" {
		return w.home
	}
	if cur := w.c(); cur != nil && cur.NodeID() == nodeID {
		return cur
	}
	return w.home.ForNode(nodeID)
}

// resolveRoot maps a (possibly qualified) root to the node it lives on,
// the client that reaches it, and the plain root.
func (w *ClientWorkspace) resolveRoot(root string) (nodeID string, c *client.Client, plain string) {
	ref, plain, ok := ParseNodeRoot(root)
	if !ok {
		return w.NodeID(), w.c(), root
	}
	if ref == HomeNodeRef {
		ref = ""
	}
	return ref, w.clientForNode(ref), plain
}

// qualifyOverviews rewrites overview roots so every root not on the
// currently targeted node carries its node. Overviews come from the
// home server: NodeID "" there means the home node itself.
func (w *ClientWorkspace) qualifyOverviews(ovs []proto.WorkspaceOverview) []proto.WorkspaceOverview {
	cur := w.NodeID()
	for i := range ovs {
		if ovs[i].NodeID != "" && ovs[i].NodeName != "" {
			w.nodeNames.Store(ovs[i].NodeID, ovs[i].NodeName)
		}
		if ovs[i].NodeID == cur {
			continue
		}
		ref := ovs[i].NodeID
		if ref == "" {
			ref = HomeNodeRef
		}
		ovs[i].Root = QualifyNodeRoot(ref, ovs[i].Root)
	}
	return ovs
}

// switchNode retargets the workspace at nodeID and attaches path there.
func (w *ClientWorkspace) switchNode(ctx context.Context, nodeID, path string) error {
	target := w.clientForNode(nodeID)
	created, err := target.CreateWorkspace(ctx, proto.Workspace{Path: path})
	if err != nil {
		return fmt.Errorf("failed to attach workspace %q on node %s: %w", path, nodeLabel(nodeID), err)
	}
	if created.Config != nil {
		created.Config.SetupAgents()
	}
	w.mu.Lock()
	w.ws = *created
	w.activeSessionID = ""
	w.worktrees = nil
	w.mu.Unlock()
	w.target.Store(target)
	w.requestSwitch()
	return nil
}

func nodeLabel(id string) string {
	if id == "" {
		return HomeNodeRef
	}
	return id
}

// NodeName returns the display name of the targeted cluster node, or ""
// when targeting the home server.
func (w *ClientWorkspace) NodeName() string {
	id := w.NodeID()
	if id == "" {
		return ""
	}
	if v, ok := w.nodeNames.Load(id); ok {
		return v.(string)
	}
	return id
}

// ClusterDashboard returns the home server's view of every node.
func (w *ClientWorkspace) ClusterDashboard(ctx context.Context) ([]cluster.NodeDashboard, error) {
	return w.home.ClusterDashboard(ctx)
}

// MoveSession moves req.SessionID, living in the workspace at root (possibly
// on another node; "" = the current workspace), to req.TargetNode.
func (w *ClientWorkspace) MoveSession(ctx context.Context, root string, req proto.MoveSessionRequest) (proto.MoveSessionResponse, error) {
	c, wsID := w.c(), w.workspaceID()
	if root != "" {
		nodeID, nc, plain := w.resolveRoot(root)
		if nodeID != w.NodeID() || plain != w.BaseDir() {
			ws, err := nc.CreateWorkspace(ctx, proto.Workspace{Path: plain})
			if err != nil {
				return proto.MoveSessionResponse{}, err
			}
			defer func() { _ = nc.DeleteWorkspace(context.Background(), ws.ID) }()
			c, wsID = nc, ws.ID
		}
	}
	req.WorkspaceID = wsID
	return c.MoveSession(ctx, req)
}

// PlanMigration asks the home server where its jobs would move.
func (w *ClientWorkspace) PlanMigration(ctx context.Context, req proto.MigrateRequest) (proto.MigrationPlan, error) {
	return w.home.PlanMigration(ctx, req)
}

// ExecuteMigration runs a plan on the home server.
func (w *ClientWorkspace) ExecuteMigration(ctx context.Context, plan proto.MigrationPlan, resume bool) (proto.MigrationResult, error) {
	return w.home.ExecuteMigration(ctx, plan, resume)
}

// Transfers lists moves running from the node that hosts root ("" = the
// current target) and, when different, from the home server (which runs
// migrate-all).
func (w *ClientWorkspace) Transfers(ctx context.Context, root string) ([]proto.TransferStatus, error) {
	c := w.c()
	if root != "" {
		_, c, _ = w.resolveRoot(root)
	}
	out, err := c.Transfers(ctx)
	if err != nil {
		return nil, err
	}
	if c.NodeID() != "" {
		if home, err := w.home.Transfers(ctx); err == nil {
			out = append(out, home...)
		}
	}
	return out, nil
}

// SessionBundle is a session plus everything needed to render it.
type SessionBundle struct {
	Session   session.Session
	Files     []history.File
	ReadFiles []string
	Messages  []message.Message
	// Children holds nested agent-tool sessions' messages by session ID.
	Children map[string][]message.Message
}

// SessionBundle loads sessionID in one round trip. root "" means the
// current workspace; otherwise any known workspace (possibly on another
// node).
func (w *ClientWorkspace) SessionBundle(ctx context.Context, root, sessionID string) (SessionBundle, error) {
	c := w.c()
	params := proto.SessionBundleParams{SessionID: sessionID, WorkspaceID: w.workspaceID()}
	if root != "" {
		nodeID, nc, plain := w.resolveRoot(root)
		if nodeID != w.NodeID() || plain != w.BaseDir() {
			c, params = nc, proto.SessionBundleParams{SessionID: sessionID, Root: plain}
		}
	}
	b, err := c.SessionBundle(ctx, params)
	if err != nil {
		return SessionBundle{}, err
	}
	children := make(map[string][]message.Message, len(b.Children))
	for id, msgs := range b.Children {
		children[id] = protoToMessages(msgs)
	}
	return SessionBundle{
		Session:   protoToSession(b.Session),
		Files:     protoToFiles(b.Files),
		ReadFiles: b.ReadFiles,
		Messages:  protoToMessages(b.Messages),
		Children:  children,
	}, nil
}
