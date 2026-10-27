package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

// EnableCluster attaches the cluster runtime to the server. listenAddr is
// where the mTLS cluster listener binds (e.g. ":7731") and meshAddr where
// NATS routes bind (e.g. ":7732"). When the node
// already belongs to a cluster the listener starts immediately;
// otherwise it starts after a successful init or join.
func (s *Server) EnableCluster(n *cluster.Node, listenAddr, meshAddr string) error {
	s.cluster = n
	s.clusterAddr = listenAddr
	s.meshAddr = meshAddr
	n.SetAPIHandler(s.h.Handler)
	n.OnJoinRequest = func(r cluster.JoinRequest) {
		s.backend.PublishClusterJoin(r.ID, r.Name)
	}
	if n.Store().InCluster() {
		// Cluster nodes must stay reachable even with no local clients.
		s.backend.SetPersistent(true)
	}
	if err := n.Start(listenAddr); err != nil {
		return err
	}
	return s.startMesh()
}

// Cluster returns the attached cluster runtime, if any.
func (s *Server) Cluster() *cluster.Node { return s.cluster }

func (s *Server) clusterPort() int {
	return portOf(s.clusterAddr, cluster.DefaultPort)
}

func (s *Server) installClusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/cluster", s.handleClusterStatus)
	mux.HandleFunc("POST /v1/cluster/init", s.handleClusterInit)
	mux.HandleFunc("POST /v1/cluster/join", s.handleClusterJoin)
	mux.HandleFunc("GET /v1/cluster/token", s.handleClusterToken)
	mux.HandleFunc("POST /v1/cluster/approve", s.handleClusterApprove)
	mux.HandleFunc("POST /v1/cluster/deny", s.handleClusterDeny)
	mux.HandleFunc("POST /v1/cluster/rotate", s.handleClusterRotate)
	mux.HandleFunc("POST /v1/cluster/leave", s.handleClusterLeave)
	mux.HandleFunc("POST /v1/cluster/upgrade", s.handleClusterUpgrade)
	mux.HandleFunc("POST /v1/cluster/name", s.handleClusterName)
	mux.HandleFunc("GET /v1/cluster/ping", s.handleClusterPing)
	mux.HandleFunc("GET /v1/cluster/dashboard", s.handleClusterDashboard)
	mux.HandleFunc("/v1/nodes/{node}/{rest...}", s.handleNodeProxy)
}

func (s *Server) requireCluster(w http.ResponseWriter) (*cluster.Node, bool) {
	if s.cluster == nil {
		jsonError(w, http.StatusNotImplemented, "clustering is not enabled on this server")
		return nil, false
	}
	return s.cluster, true
}

func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if r.ContentLength == 0 {
		return v, true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return v, false
	}
	return v, true
}

func errStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	jsonEncode(w, n.Status())
}

func (s *Server) advertised(eps []cluster.Endpoint) []cluster.Endpoint {
	if len(eps) == 0 {
		eps = cluster.LocalEndpoints(s.clusterPort())
	}
	return cluster.WithMeshEndpoints(eps, portOf(s.meshAddr, cluster.DefaultRoutePort))
}

func portOf(addr string, def int) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return def
	}
	port, err := strconv.Atoi(p)
	if err != nil || port == 0 {
		return def
	}
	return port
}

func (s *Server) handleClusterInit(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterInitRequest](w, r)
	if !ok {
		return
	}
	token, err := n.Init(s.advertised(req.Endpoints))
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	s.backend.SetPersistent(true)
	if err := n.Start(s.clusterAddr); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("cluster created but listener failed: %v", err))
		return
	}
	if err := s.startMesh(); err != nil {
		slog.Error("Cluster mesh failed to start", "error", err)
	}
	jsonEncode(w, proto.ClusterTokenResponse{Token: token})
}

func (s *Server) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterJoinRequest](w, r)
	if !ok {
		return
	}
	if req.Addr == "" || req.Token == "" {
		jsonError(w, http.StatusBadRequest, "addr and token are required")
		return
	}
	addr := req.Addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(cluster.DefaultPort))
	}
	// The listener must be bindable before we join, so peers can reach
	// us as soon as we are admitted.
	if ln, err := (&net.ListenConfig{}).Listen(r.Context(), "tcp", s.clusterAddr); err == nil {
		_ = ln.Close()
	} else if n.Listening() == "" {
		jsonError(w, http.StatusConflict, fmt.Sprintf("cluster listener address %s unavailable: %v", s.clusterAddr, err))
		return
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	err := n.Join(ctx, addr, req.Token, cluster.JoinOptions{Endpoints: s.advertised(req.Endpoints)})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			err = errors.New("timed out waiting for approval")
		}
		jsonError(w, status, err.Error())
		return
	}
	s.backend.SetPersistent(true)
	if err := n.Start(s.clusterAddr); err != nil {
		slog.Error("Cluster listener failed after join", "error", err)
	}
	if err := s.startMesh(); err != nil {
		slog.Error("Cluster mesh failed to start", "error", err)
	}
	jsonEncode(w, n.Status())
}

func (s *Server) handleClusterToken(w http.ResponseWriter, _ *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	tok, err := n.Token()
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonEncode(w, proto.ClusterTokenResponse{Token: tok})
}

func (s *Server) handleClusterApprove(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterRefRequest](w, r)
	if !ok {
		return
	}
	m, err := n.Approve(r.Context(), req.Ref)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	s.backend.PublishClusterJoin("", "")
	jsonEncode(w, m)
}

func (s *Server) handleClusterDeny(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterRefRequest](w, r)
	if !ok {
		return
	}
	jr, err := n.Deny(req.Ref)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	s.backend.PublishClusterJoin("", "")
	jsonEncode(w, jr)
}

func (s *Server) handleClusterRotate(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	tok, errs, err := n.RotateKey(r.Context())
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonEncode(w, proto.ClusterTokenResponse{Token: tok, Errors: errStrings(errs)})
}

func (s *Server) handleClusterLeave(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	if !n.Store().InCluster() {
		jsonError(w, http.StatusConflict, cluster.ErrNotInCluster.Error())
		return
	}
	s.stopMesh()
	jsonEncode(w, proto.ClusterTokenResponse{Errors: errStrings(n.Leave(r.Context()))})
}

func (s *Server) handleClusterUpgrade(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterUpgradeRequest](w, r)
	if !ok {
		return
	}
	jsonEncode(w, proto.ClusterTokenResponse{Errors: errStrings(n.UpgradeAll(r.Context(), req.Version))})
}

func (s *Server) handleClusterName(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	req, ok := decodeBody[proto.ClusterRefRequest](w, r)
	if !ok {
		return
	}
	if req.Ref == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := n.Identity().SetName(n.Store().Dir(), req.Ref); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonEncode(w, n.Status())
}

func (s *Server) handleClusterPing(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	st, err := n.Store().Snapshot()
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	var out []proto.ClusterPingResult
	for _, m := range st.Members {
		if m.NodeID == n.Identity().ID {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		info, rtt, err := n.Ping(ctx, m)
		cancel()
		res := proto.ClusterPingResult{NodeID: m.NodeID, Name: m.Name, RTTMillis: rtt.Milliseconds(), Version: info.Version}
		if err != nil {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	jsonEncode(w, out)
}

// handleNodeProxy forwards /v1/nodes/{node}/<rest> to the same API path
// on another cluster node, so a client attached to this server can
// drive sessions that live on any machine.
func (s *Server) handleNodeProxy(w http.ResponseWriter, r *http.Request) {
	n, ok := s.requireCluster(w)
	if !ok {
		return
	}
	nodeID := r.PathValue("node")
	if nodeID == n.Identity().ID {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + r.PathValue("rest")
		r2.URL.RawPath = ""
		s.h.Handler.ServeHTTP(w, r2)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + r.PathValue("rest")
	r2.URL.RawPath = ""
	n.ProxyAPI(w, r2, nodeID)
}

// handleClusterDashboard returns live per-node health and resources.
func (s *Server) handleClusterDashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCluster(w); !ok {
		return
	}
	s.meshMu.Lock()
	br := s.bridge
	s.meshMu.Unlock()
	if br == nil || br.tel == nil {
		// Not paired yet: still show this machine so the dashboard is
		// useful (and explains how to add others).
		jsonEncode(w, []cluster.NodeDashboard{s.standaloneRow(r.Context())})
		return
	}
	jsonEncode(w, br.tel.dashboard(r.Context()))
}

// standaloneRow describes this node when it has no cluster mesh.
func (s *Server) standaloneRow(ctx context.Context) cluster.NodeDashboard {
	id := s.cluster.Identity()
	st := cluster.CollectStats(ctx, "")
	st.NodeID, st.Name, st.Version = id.ID, id.Name, s.cluster.Version()
	st.Jobs, st.Workspaces, st.Sessions = s.backend.ActiveJobs(ctx)
	return cluster.NodeDashboard{
		Member:    cluster.Member{NodeID: id.ID, Name: id.Name, Version: s.cluster.Version()},
		Self:      true,
		Online:    true,
		LastSeen:  time.Now().UTC(),
		Stats:     &st,
		RTTMillis: 0,
	}
}
