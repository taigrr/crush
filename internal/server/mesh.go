package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/transfer"
)

// Mesh subjects. Every node subscribes to all of them; per-node subjects
// end in the node ID.
const (
	subjAttention   = "crush.attention"
	subjSwarmLookup = "crush.swarm.lookup"
	subjSwarmSend   = "crush.swarm.send." // + node ID
)

const (
	swarmLookupWindow = 1500 * time.Millisecond
	// overviewTimeout bounds a background overview refresh. Requests
	// never wait on it (except the first load, see firstLoadWait), so it
	// can be generous enough for a large list over a slow link.
	overviewTimeout = 15 * time.Second
)

// meshBridge implements [backend.ClusterBridge] over the NATS mesh (for
// events and swarm) and the member API proxy (for overviews).
type meshBridge struct {
	mesh    *cluster.Mesh
	node    *cluster.Node
	backend *backend.Backend
	subs    []*nats.Subscription
	tel     *telemetry
	view    remoteView
	repl    *replicator
	creds   *credsSync
}

var _ backend.ClusterBridge = (*meshBridge)(nil)

func (m *meshBridge) NodeID() string   { return m.node.Identity().ID }
func (m *meshBridge) NodeName() string { return m.node.Identity().Name }

func (m *meshBridge) PublishAttention(ev proto.AttentionEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if err := m.mesh.Conn().Publish(subjAttention, data); err != nil {
		slog.Debug("Failed to publish attention to mesh", "error", err)
	}
}

type swarmLookupRequest struct {
	From    string `json:"from"`
	Address string `json:"address"`
}

type swarmLookupReply struct {
	NodeID string                     `json:"node_id"`
	Match  *backend.SwarmLookupResult `json:"match,omitempty"`
	Error  string                     `json:"error,omitempty"`
}

func (m *meshBridge) SwarmLookup(ctx context.Context, address string) ([]backend.SwarmLookupResult, error) {
	nc := m.mesh.Conn()
	inbox := nc.NewRespInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	data, _ := json.Marshal(swarmLookupRequest{From: m.NodeID(), Address: address})
	if err := nc.PublishRequest(subjSwarmLookup, inbox, data); err != nil {
		return nil, err
	}
	expected := len(m.node.Members())
	deadline := time.Now().Add(swarmLookupWindow)
	var matches []backend.SwarmLookupResult
	for replies := 0; replies < expected; {
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		msg, err := sub.NextMsgWithContext(withTimeout(ctx, wait))
		if err != nil {
			break
		}
		replies++
		var rep swarmLookupReply
		if json.Unmarshal(msg.Data, &rep) != nil || rep.Match == nil {
			continue
		}
		if rep.Error == backend.ErrSwarmAddressAmbiguous.Error() {
			return nil, backend.ErrSwarmAddressAmbiguous
		}
		rep.Match.NodeID = rep.NodeID
		matches = append(matches, *rep.Match)
	}
	return matches, nil
}

// withTimeout returns a context bounded by d. The cancel func is
// intentionally dropped: the context expires on its own within d.
func withTimeout(ctx context.Context, d time.Duration) context.Context {
	c, cancel := context.WithTimeout(ctx, d)
	time.AfterFunc(d, cancel)
	return c
}

type swarmSendRequest struct {
	Target backend.SwarmLookupResult `json:"target"`
	Part   proto.SwarmMessage        `json:"part"`
	Policy proto.PermissionPolicy    `json:"policy,omitempty"`
}

type swarmSendReply struct {
	Result backend.SwarmSendResult `json:"result"`
	Error  string                  `json:"error,omitempty"`
}

func (m *meshBridge) SwarmSend(ctx context.Context, target backend.SwarmLookupResult, part proto.SwarmMessage, policy proto.PermissionPolicy) (backend.SwarmSendResult, error) {
	data, err := json.Marshal(swarmSendRequest{Target: target, Part: part, Policy: policy})
	if err != nil {
		return backend.SwarmSendResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := m.mesh.Request(ctx, subjSwarmSend+target.NodeID, data)
	if err != nil {
		return backend.SwarmSendResult{}, err
	}
	var rep swarmSendReply
	if err := json.Unmarshal(msg.Data, &rep); err != nil {
		return backend.SwarmSendResult{}, err
	}
	if rep.Error != "" {
		return backend.SwarmSendResult{}, errors.New(rep.Error)
	}
	return rep.Result, nil
}

func (m *meshBridge) RemoteOverviews(ctx context.Context) backend.RemoteOverviews {
	return m.view.get(ctx, m)
}

// remoteViewTTL is how long a node's overviews are served before a
// background refresh; attention events from a node refresh it at once.
const remoteViewTTL = 5 * time.Second

// staleGrace is how long a node's last good overviews keep being served
// after refreshes start failing. Slow links (large lists over a WAN)
// time out now and then; flipping the whole node to read-only replicas
// on every miss is worse than briefly showing slightly old data.
const staleGrace = 2 * time.Minute

// firstLoadWait bounds how long the very first overview request waits for
// nodes this server has never heard from. After that, requests never wait
// on the network.
const firstLoadWait = time.Second

type nodeView struct {
	// ovs is the last successful fetch, taken at at.
	ovs []proto.WorkspaceOverview
	at  time.Time
	ok  bool
	// failedSince is the first failure after the last success.
	failedSince time.Time
	// failedAt is the latest failure.
	failedAt time.Time
	tried    bool
	stale    bool
	loading  bool
	// err is the last fetch failure, cleared on success.
	err string
}

// remoteView caches every other node's workspace overviews so listing
// sessions answers from memory. Stale entries refresh in the background
// and a change pings attached TUIs to re-read.
type remoteView struct {
	mu    sync.Mutex
	nodes map[string]*nodeView
	// now is overridable in tests.
	now func() time.Time
}

func (v *remoteView) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

func (v *remoteView) get(ctx context.Context, m *meshBridge) backend.RemoteOverviews {
	members := m.node.Members()
	var firstLoad []chan struct{}
	v.mu.Lock()
	if v.nodes == nil {
		v.nodes = map[string]*nodeView{}
	}
	now := v.clock()
	for _, mem := range members {
		nv := v.nodes[mem.NodeID]
		if nv == nil {
			nv = &nodeView{}
			v.nodes[mem.NodeID] = nv
		}
		// One refresh per node at a time; a failed one is retried after
		// the TTL like a successful one.
		if nv.loading || (nv.tried && !nv.stale && now.Sub(nv.lastTry()) < remoteViewTTL) {
			continue
		}
		nv.loading = true
		done := make(chan struct{})
		if !nv.tried {
			firstLoad = append(firstLoad, done)
		}
		go v.refresh(m, mem, done)
	}
	v.mu.Unlock()

	if len(firstLoad) > 0 {
		timer := time.NewTimer(firstLoadWait)
		for _, done := range firstLoad {
			select {
			case <-done:
			case <-timer.C:
			case <-ctx.Done():
			}
		}
		timer.Stop()
	}

	return v.collect(members)
}

// lastTry is when the node was last fetched, successfully or not.
func (nv *nodeView) lastTry() time.Time {
	if nv.err != "" && nv.failedAt.After(nv.at) {
		return nv.failedAt
	}
	return nv.at
}

// collect assembles members' cached overviews. A node whose refresh
// failed keeps its last good overviews, marked stale, for staleGrace;
// after that (or if it never answered) it gets an unavailable marker.
func (v *remoteView) collect(members []cluster.Member) backend.RemoteOverviews {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	out := backend.RemoteOverviews{Reachable: map[string]bool{}, Down: map[string]backend.NodeDown{}}
	for _, mem := range members {
		nv := v.nodes[mem.NodeID]
		switch {
		case nv == nil:
		case nv.ok && (nv.err == "" || now.Sub(nv.at) < staleGrace):
			out.Reachable[mem.NodeID] = true
			for _, ov := range nv.ovs {
				if nv.err != "" {
					ov.Stale, ov.StaleSince, ov.LastError = true, nv.at.UnixMilli(), nv.err
				}
				out.Overviews = append(out.Overviews, ov)
			}
		case nv.tried || nv.loading:
			// A node that failed (or has not answered yet) is reported
			// rather than silently dropped, so clients can show it.
			reason := nv.err
			if reason == "" {
				reason = "node did not answer in time"
			}
			marker := proto.WorkspaceOverview{
				NodeID:      mem.NodeID,
				NodeName:    mem.Name,
				Unavailable: true,
				Error:       reason,
			}
			if !nv.at.IsZero() {
				marker.StaleSince = nv.at.UnixMilli()
			}
			out.Overviews = append(out.Overviews, marker)
			out.Down[mem.NodeID] = backend.NodeDown{Since: nv.failedSince, Error: reason}
		}
	}
	return out
}

func (v *remoteView) refresh(m *meshBridge, mem cluster.Member, done chan struct{}) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), overviewTimeout)
	defer cancel()
	var ovs []proto.WorkspaceOverview
	started := time.Now()
	err := m.node.CallAPI(ctx, mem, http.MethodGet, "/v1/workspace-overviews?local=1", &ovs)
	for j := range ovs {
		ovs[j].NodeID, ovs[j].NodeName = mem.NodeID, mem.Name
	}
	if v.apply(mem.NodeID, ovs, err) {
		m.backend.PublishOverviewsChanged()
	}
	if err != nil {
		slog.Debug("Failed to fetch remote overviews", "node", mem.Name, "elapsed", time.Since(started), "error", err)
	}
}

// apply records a fetch result and reports whether what collect serves
// changed.
func (v *remoteView) apply(nodeID string, ovs []proto.WorkspaceOverview, err error) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	nv := v.nodes[nodeID]
	if nv == nil {
		nv = &nodeView{}
		v.nodes[nodeID] = nv
	}
	wasFailing := nv.err != ""
	nv.loading, nv.tried, nv.stale = false, true, false
	if err != nil {
		if !wasFailing {
			nv.failedSince = now
		}
		nv.err, nv.failedAt = err.Error(), now
		if nv.ok && now.Sub(nv.at) >= staleGrace {
			// Past the grace: drop the data so replicas take over.
			nv.ok, nv.ovs = false, nil
			return true
		}
		// Otherwise the data is now flagged stale.
		return !wasFailing
	}
	changed := !nv.ok || wasFailing || !reflect.DeepEqual(nv.ovs, ovs)
	nv.ovs, nv.ok, nv.at = ovs, true, now
	nv.err, nv.failedSince = "", time.Time{}
	return changed
}

// markStale makes the next read refresh nodeID's overviews.
func (v *remoteView) markStale(nodeID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if nv := v.nodes[nodeID]; nv != nil {
		nv.stale = true
	}
}

// subscribe installs the handlers that answer other nodes.
func (m *meshBridge) subscribe() error {
	self := m.NodeID()
	handlers := map[string]func(*nats.Msg){
		subjAttention: func(msg *nats.Msg) {
			var ev proto.AttentionEvent
			if json.Unmarshal(msg.Data, &ev) == nil && ev.NodeID != self {
				// Busy/idle and prompts change that node's session list.
				m.view.markStale(ev.NodeID)
				m.backend.IngestRemoteAttention(ev)
			}
		},
		subjSwarmLookup: func(msg *nats.Msg) {
			var req swarmLookupRequest
			if json.Unmarshal(msg.Data, &req) != nil || req.From == self {
				return
			}
			rep := swarmLookupReply{NodeID: self}
			res, err := m.backend.LookupLocalSwarmAddress(context.Background(), req.Address)
			switch {
			case err == nil:
				rep.Match = &res
			case errors.Is(err, backend.ErrSwarmAddressAmbiguous):
				rep.Error = err.Error()
				rep.Match = &backend.SwarmLookupResult{}
			}
			data, _ := json.Marshal(rep)
			_ = msg.Respond(data)
		},
		subjSwarmSend + self: func(msg *nats.Msg) {
			var req swarmSendRequest
			rep := swarmSendReply{}
			if err := json.Unmarshal(msg.Data, &req); err != nil {
				rep.Error = err.Error()
			} else {
				res, err := m.backend.DeliverRemoteSwarm(context.Background(), req.Target, req.Part, req.Policy)
				rep.Result = res
				if err != nil {
					rep.Error = err.Error()
				}
			}
			data, _ := json.Marshal(rep)
			_ = msg.Respond(data)
		},
	}
	for subj, fn := range handlers {
		sub, err := m.mesh.Subscribe(subj, fn)
		if err != nil {
			return err
		}
		m.subs = append(m.subs, sub)
	}
	return m.mesh.Conn().Flush()
}

func (m *meshBridge) close() {
	if m.creds != nil {
		m.creds.stop()
	}
	if m.repl != nil {
		m.repl.stop()
	}
	if m.tel != nil {
		m.tel.stop()
	}
	for _, s := range m.subs {
		_ = s.Unsubscribe()
	}
}

// startMesh brings the NATS mesh up and attaches the cluster bridge to
// the backend. It is a no-op when already running or not in a cluster.
func (s *Server) startMesh() error {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if s.mesh != nil || s.cluster == nil || !s.cluster.Store().InCluster() {
		return nil
	}
	mesh, err := s.cluster.StartMesh(s.meshAddr)
	if err != nil {
		return err
	}
	br := &meshBridge{mesh: mesh, node: s.cluster, backend: s.backend}
	if err := br.subscribe(); err != nil {
		mesh.Close()
		return err
	}
	br.tel = newTelemetry(br)
	if err := br.tel.start(); err != nil {
		br.close()
		mesh.Close()
		return err
	}
	br.creds = newCredsSync(br, cmp.Or(s.credsPath, config.GlobalConfigData()))
	if err := br.creds.start(); err != nil {
		br.close()
		mesh.Close()
		return err
	}
	br.repl = newReplicator(br)
	if err := br.repl.start(); err != nil {
		br.close()
		mesh.Close()
		return err
	}
	s.mesh, s.bridge = mesh, br
	s.backend.SetClusterBridge(br)
	if s.pairing != nil {
		if err := s.pairing.startMesh(mesh); err != nil {
			slog.Error("Mobile pairing failed to start", "error", err)
		}
	}
	return nil
}

// stopMesh detaches the bridge and shuts the mesh down.
func (s *Server) stopMesh() {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	// Pairing never takes meshMu, so it can stop under it; doing so keeps
	// a concurrent startMesh from attaching it to a mesh being torn down.
	if s.pairing != nil {
		s.pairing.stopMesh()
	}
	if s.mesh == nil {
		return
	}
	s.backend.SetClusterBridge(nil)
	s.bridge.close()
	s.mesh.Close()
	s.mesh, s.bridge = nil, nil
}

var _ backend.TransferBridge = (*meshBridge)(nil)

func (m *meshBridge) TransferPreflight(ctx context.Context, nodeID string, req proto.TransferPreflightRequest) (proto.TransferPreflightResponse, error) {
	mem, err := m.node.MemberByID(nodeID)
	if err != nil {
		return proto.TransferPreflightResponse{}, err
	}
	var out proto.TransferPreflightResponse
	err = m.node.CallAPIJSON(ctx, mem, http.MethodPost, "/v1/transfer/preflight", req, &out)
	if out.NodeName == "" {
		out.NodeName = mem.Name
	}
	return out, err
}

func (m *meshBridge) TransferSend(ctx context.Context, nodeID string, plan transfer.Plan, resume bool, body io.Reader) (proto.TransferImportResponse, error) {
	mem, err := m.node.MemberByID(nodeID)
	if err != nil {
		return proto.TransferImportResponse{}, err
	}
	q := url.Values{"plan": {string(plan)}}
	if resume {
		q.Set("resume", "1")
	}
	var out proto.TransferImportResponse
	err = m.node.StreamAPI(ctx, mem, "/v1/transfer/import?"+q.Encode(), "application/x-crush-bundle", body, &out)
	return out, err
}

// MemberName returns a member's display name.
func (m *meshBridge) MemberName(id string) string {
	if mem, err := m.node.MemberByID(id); err == nil {
		return mem.Name
	}
	return ""
}

var _ backend.DashboardBridge = (*meshBridge)(nil)

// Dashboard returns live node health for the scheduler.
func (m *meshBridge) Dashboard(ctx context.Context) []cluster.NodeDashboard {
	if m.tel == nil {
		return nil
	}
	return m.tel.dashboard(ctx)
}
