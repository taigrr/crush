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
	overviewTimeout   = 3 * time.Second
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

func (m *meshBridge) RemoteOverviews(ctx context.Context) []proto.WorkspaceOverview {
	return m.view.get(ctx, m)
}

// remoteViewTTL is how long a node's overviews are served before a
// background refresh; attention events from a node refresh it at once.
const remoteViewTTL = 5 * time.Second

// firstLoadWait bounds how long the very first overview request waits for
// nodes this server has never heard from. After that, requests never wait
// on the network.
const firstLoadWait = time.Second

type nodeView struct {
	ovs     []proto.WorkspaceOverview
	at      time.Time
	ok      bool
	tried   bool
	stale   bool
	loading bool
}

// remoteView caches every other node's workspace overviews so listing
// sessions answers from memory. Stale entries refresh in the background
// and a change pings attached TUIs to re-read.
type remoteView struct {
	mu    sync.Mutex
	nodes map[string]*nodeView
}

func (v *remoteView) get(ctx context.Context, m *meshBridge) []proto.WorkspaceOverview {
	members := m.node.Members()
	var firstLoad []chan struct{}
	v.mu.Lock()
	if v.nodes == nil {
		v.nodes = map[string]*nodeView{}
	}
	for _, mem := range members {
		nv := v.nodes[mem.NodeID]
		if nv == nil {
			nv = &nodeView{}
			v.nodes[mem.NodeID] = nv
		}
		if nv.loading || (nv.tried && !nv.stale && time.Since(nv.at) < remoteViewTTL) {
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

	v.mu.Lock()
	defer v.mu.Unlock()
	var out []proto.WorkspaceOverview
	for _, mem := range members {
		if nv := v.nodes[mem.NodeID]; nv != nil && nv.ok {
			out = append(out, nv.ovs...)
		}
	}
	return out
}

func (v *remoteView) refresh(m *meshBridge, mem cluster.Member, done chan struct{}) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), overviewTimeout)
	defer cancel()
	var ovs []proto.WorkspaceOverview
	err := m.node.CallAPI(ctx, mem, http.MethodGet, "/v1/workspace-overviews?local=1", &ovs)
	for j := range ovs {
		ovs[j].NodeID, ovs[j].NodeName = mem.NodeID, mem.Name
	}
	v.mu.Lock()
	nv := v.nodes[mem.NodeID]
	nv.loading, nv.tried, nv.stale = false, true, false
	changed := false
	if err != nil {
		slog.Debug("Failed to fetch remote overviews", "node", mem.Name, "error", err)
		changed = nv.ok
		nv.ok = false
	} else {
		changed = !nv.ok || !reflect.DeepEqual(nv.ovs, ovs)
		nv.ovs, nv.ok, nv.at = ovs, true, time.Now()
	}
	v.mu.Unlock()
	if changed {
		m.backend.PublishOverviewsChanged()
	}
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
	return nil
}

// stopMesh detaches the bridge and shuts the mesh down.
func (s *Server) stopMesh() {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
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
