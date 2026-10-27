package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// ClusterBridge connects the backend to the other nodes of a cluster.
// The backend stays transport-agnostic; the server wires an
// implementation backed by the NATS mesh.
type ClusterBridge interface {
	// NodeID and NodeName identify this node.
	NodeID() string
	NodeName() string
	// PublishAttention broadcasts a locally originated attention event
	// to every other node.
	PublishAttention(ev proto.AttentionEvent)
	// SwarmLookup asks every other node to resolve address.
	SwarmLookup(ctx context.Context, address string) ([]SwarmLookupResult, error)
	// SwarmSend delivers part to target on target.NodeID. part already
	// carries the trusted sender identity stamped by this node.
	SwarmSend(ctx context.Context, target SwarmLookupResult, part proto.SwarmMessage, policy proto.PermissionPolicy) (SwarmSendResult, error)
	// RemoteOverviews returns every other node's workspace overviews,
	// tagged with the owning node.
	RemoteOverviews(ctx context.Context) []proto.WorkspaceOverview
}

// SetClusterBridge attaches (or, with nil, detaches) the cluster bridge.
func (b *Backend) SetClusterBridge(cb ClusterBridge) {
	b.clusterMu.Lock()
	defer b.clusterMu.Unlock()
	b.cluster = cb
}

func (b *Backend) clusterBridge() ClusterBridge {
	b.clusterMu.RLock()
	defer b.clusterMu.RUnlock()
	return b.cluster
}

// forwardAttention mirrors a locally originated attention event to the
// rest of the cluster so every node's TUIs see alerts, sounds, and
// permission gates from every machine.
func (b *Backend) forwardAttention(ev proto.AttentionEvent) {
	if ev.NodeID != "" {
		return
	}
	cb := b.clusterBridge()
	if cb == nil {
		return
	}
	ev.NodeID, ev.NodeName = cb.NodeID(), cb.NodeName()
	cb.PublishAttention(ev)
}

// IngestRemoteAttention republishes an attention event received from
// another node on the local attention channel. Remote events always
// carry their NodeID so they are never forwarded again.
func (b *Backend) IngestRemoteAttention(ev proto.AttentionEvent) {
	if ev.NodeID == "" || b.attention == nil {
		return
	}
	if cb := b.clusterBridge(); cb != nil && ev.NodeID == cb.NodeID() {
		return
	}
	b.attention.Publish(pubsub.CreatedEvent, ev)
}

// ListClusterOverviews returns this node's overviews followed by every
// reachable remote node's, each remote overview tagged with its node.
func (b *Backend) ListClusterOverviews(ctx context.Context) ([]proto.WorkspaceOverview, error) {
	all, err := b.listAllOverviews(ctx)
	if err != nil {
		return nil, err
	}
	local, replicas := b.splitByOwner(all)
	cb := b.clusterBridge()
	if cb == nil {
		return local, nil
	}
	remote := cb.RemoteOverviews(ctx)
	reachable := map[string]bool{}
	for _, ov := range remote {
		reachable[ov.NodeID] = true
	}
	// Sessions whose owner did not answer still show, from the local
	// replica, read-only.
	for owner, ovs := range replicas {
		if reachable[owner] {
			continue
		}
		name := b.nodeName(owner)
		for _, ov := range ovs {
			for i := range ov.Sessions {
				ov.Sessions[i].OwnerName = name
				ov.Sessions[i].ReadOnly = true
			}
			if i := slices.IndexFunc(local, func(l proto.WorkspaceOverview) bool { return l.Root == ov.Root }); i >= 0 {
				local[i].Sessions = append(local[i].Sessions, ov.Sessions...)
				continue
			}
			local = append(local, ov)
		}
	}
	return append(local, remote...), nil
}

// remoteSwarmLookup consults other nodes when no local session matches.
func (b *Backend) remoteSwarmLookup(ctx context.Context, address string) (SwarmLookupResult, error) {
	cb := b.clusterBridge()
	if cb == nil {
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	}
	matches, err := cb.SwarmLookup(ctx, address)
	if err != nil {
		slog.Warn("Cluster swarm lookup failed", "address", address, "error", err)
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	}
	switch len(matches) {
	case 0:
		return SwarmLookupResult{}, ErrSwarmAddressNotFound
	case 1:
		return matches[0], nil
	default:
		return SwarmLookupResult{}, ErrSwarmAddressAmbiguous
	}
}

// LookupLocalSwarmAddress resolves address against this node only. It
// answers cluster lookups from other nodes.
func (b *Backend) LookupLocalSwarmAddress(ctx context.Context, address string) (SwarmLookupResult, error) {
	return b.lookupSwarmAddress(ctx, address, false)
}

// DeliverRemoteSwarm delivers a swarm message that another node already
// resolved and stamped with a trusted sender identity.
func (b *Backend) DeliverRemoteSwarm(ctx context.Context, target SwarmLookupResult, part proto.SwarmMessage, policy proto.PermissionPolicy) (SwarmSendResult, error) {
	if target.NodeID != "" {
		if cb := b.clusterBridge(); cb == nil || target.NodeID != cb.NodeID() {
			return SwarmSendResult{}, errors.New("swarm target belongs to another node")
		}
	}
	target.NodeID = ""
	if policy == proto.PermissionPolicyDeny {
		ctx = permission.WithPolicy(ctx, permission.SessionPolicyDeny)
	}
	return b.swarmSendLocal(ctx, "", target, part)
}

// ActiveJobs reports every session with an in-flight agent turn across
// attached workspaces, plus workspace and session totals, for the
// cluster dashboard.
func (b *Backend) ActiveJobs(ctx context.Context) (jobs []cluster.NodeJob, workspaces, sessions int) {
	for _, ws := range b.workspaces.Seq2() {
		workspaces++
		for _, s := range b.attachedSessionOverviews(ctx, ws) {
			sessions++
			if !s.IsBusy {
				continue
			}
			jobs = append(jobs, cluster.NodeJob{
				SessionID:     s.ID,
				Title:         s.Title,
				WorkspaceRoot: ws.resolvedPath,
				Animal:        s.Animal,
				Color:         s.Color,
			})
		}
	}
	return jobs, workspaces, sessions
}

// ErrSessionNotOwned is returned when writing to a session another
// cluster node owns.
var ErrSessionNotOwned = errors.New("session is owned by another cluster node")

// selfNodeID returns this node's cluster ID ("" when not clustered).
func (b *Backend) selfNodeID() string {
	if cb := b.clusterBridge(); cb != nil {
		return cb.NodeID()
	}
	return ""
}

// ownedHere reports whether ownerNode (a session's owner_node) designates
// this node.
func (b *Backend) ownedHere(ownerNode string) bool {
	return ownerNode == "" || ownerNode == b.selfNodeID()
}

// checkOwned refuses writes to sessions owned elsewhere.
func (b *Backend) checkOwned(ctx context.Context, ws *Workspace, sessionID string) error {
	if ws.Sessions == nil {
		return nil
	}
	s, err := ws.Sessions.Get(ctx, sessionID)
	if err != nil || b.ownedHere(s.OwnerNode) {
		return nil
	}
	return fmt.Errorf("%w (%s); move it here to continue it on this machine", ErrSessionNotOwned, b.nodeName(s.OwnerNode))
}

// nodeName resolves a node ID to its display name via the bridge.
func (b *Backend) nodeName(id string) string {
	if nb, ok := b.clusterBridge().(interface{ MemberName(string) string }); ok {
		if n := nb.MemberName(id); n != "" {
			return n
		}
	}
	return id
}

// splitByOwner separates sessions owned here from replicas, keeping each
// workspace's grouping. Replicas are keyed by owning node.
func (b *Backend) splitByOwner(all []proto.WorkspaceOverview) (owned []proto.WorkspaceOverview, replicas map[string][]proto.WorkspaceOverview) {
	replicas = map[string][]proto.WorkspaceOverview{}
	for _, ov := range all {
		mine := ov
		mine.Sessions = nil
		byOwner := map[string][]proto.SessionOverview{}
		for _, s := range ov.Sessions {
			if b.ownedHere(s.OwnerNode) {
				s.OwnerNode = ""
				mine.Sessions = append(mine.Sessions, s)
				continue
			}
			byOwner[s.OwnerNode] = append(byOwner[s.OwnerNode], s)
		}
		owned = append(owned, mine)
		for owner, ss := range byOwner {
			rep := ov
			rep.Sessions = ss
			replicas[owner] = append(replicas[owner], rep)
		}
	}
	return owned, replicas
}

// ReloadAllConfigs re-reads config from disk for every attached
// workspace, e.g. after credentials synced from another node.
func (b *Backend) ReloadAllConfigs() {
	for _, ws := range b.workspaces.Seq2() {
		if err := b.ReloadConfig(ws.ID); err != nil {
			slog.Warn("Failed to reload workspace config", "workspace", ws.ID, "error", err)
		}
	}
}

// PublishOverviewsChanged tells local clients the cluster session list
// changed. It is local-only: every node maintains its own view.
func (b *Backend) PublishOverviewsChanged() {
	if b == nil || b.attention == nil {
		return
	}
	b.attention.Publish(pubsub.CreatedEvent, proto.AttentionEvent{Kind: proto.AttentionOverviews, NodeID: b.selfNodeID()})
}
