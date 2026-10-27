package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// JoinStatus is the outcome of a join request.
type JoinStatus string

const (
	JoinPending  JoinStatus = "pending"
	JoinApproved JoinStatus = "approved"
	JoinDenied   JoinStatus = "denied"
)

// NodeInfo describes a node over the wire.
type NodeInfo struct {
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	ClusterID string `json:"cluster_id"`
	Version   string `json:"version"`
	Protocol  int    `json:"protocol"`
}

type joinRequestBody struct {
	Name      string     `json:"name"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
}

type joinResponse struct {
	Status     JoinStatus `json:"status"`
	RequestID  string     `json:"request_id,omitempty"`
	Members    []Member   `json:"members,omitempty"`
	Key        []byte     `json:"key,omitempty"`
	KeyVersion int        `json:"key_version,omitempty"`
}

type membersBody struct {
	Members []Member `json:"members"`
}

type rotateBody struct {
	Key        []byte `json:"key"`
	KeyVersion int    `json:"key_version"`
}

type errorBody struct {
	Error string `json:"error,omitempty"`
	// Message is the field used by the local Crush API (proto.Error).
	Message string `json:"message,omitempty"`
}

// Handler returns the HTTP handler for cluster RPCs.
func (n *Node) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cluster/v1/info", n.handleInfo)
	mux.HandleFunc("POST /cluster/v1/join", n.versioned(n.handleJoin))
	mux.HandleFunc("POST /cluster/v1/members", n.versioned(n.memberOnly(n.handleMembers)))
	mux.HandleFunc("POST /cluster/v1/rotate", n.versioned(n.memberOnly(n.handleRotate)))
	mux.HandleFunc("POST /cluster/v1/leave", n.versioned(n.memberOnly(n.handleLeave)))
	mux.HandleFunc("POST /cluster/v1/upgrade", n.memberOnly(n.handleUpgrade))
	mux.HandleFunc(apiPrefix+"/", n.versioned(n.memberOnly(n.handleAPI)))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorBody{Error: err.Error()})
}

func (n *Node) info() NodeInfo {
	st, _ := n.store.Snapshot()
	return NodeInfo{
		NodeID:    n.id.ID,
		Name:      n.id.Name,
		ClusterID: st.ClusterID,
		Version:   n.version,
		Protocol:  ProtocolVersion,
	}
}

func (n *Node) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, n.info())
}

// versioned rejects requests from peers running an incompatible build.
func (n *Node) versioned(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		proto, _ := strconv.Atoi(r.Header.Get(headerProtocol))
		if err := CheckCompatible(n.version, r.Header.Get(headerVersion), proto); err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		next(w, r)
	}
}

// memberOnly requires the caller's certificate to belong to a pinned
// member, and records that member as seen.
func (n *Node) memberOnly(next func(http.ResponseWriter, *http.Request, Member)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		peer, err := peerFromState(r.TLS, n.trustedKeys())
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err)
			return
		}
		st, err := n.store.Snapshot()
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		m, ok := st.Member(peer.NodeID)
		if !ok || !samePublicKey(m.PublicKey, peer.PublicKey) {
			writeErr(w, http.StatusForbidden, errors.New("not a cluster member"))
			return
		}
		n.touch(peer.NodeID, r.Header.Get(headerVersion))
		next(w, r, m)
	}
}

// touch updates a member's last-seen time and version.
func (n *Node) touch(nodeID, version string) {
	n.MarkSeen(nodeID, time.Now().UTC())
	if version == "" {
		return
	}
	st, err := n.store.Snapshot()
	if err != nil {
		return
	}
	if m, ok := st.Member(nodeID); !ok || m.Version == version {
		return
	}
	_ = n.store.Update(func(st *State) error {
		for i := range st.Members {
			if st.Members[i].NodeID == nodeID {
				st.Members[i].Version = version
			}
		}
		return nil
	})
}

// MarkSeen records that nodeID was heard from at t. Last-seen times are
// kept in memory so frequent heartbeats never rewrite cluster.json.
func (n *Node) MarkSeen(nodeID string, t time.Time) {
	n.seenMu.Lock()
	defer n.seenMu.Unlock()
	if t.After(n.seen[nodeID]) {
		n.seen[nodeID] = t
	}
}

// LastSeen returns when nodeID was last heard from.
func (n *Node) LastSeen(nodeID string) time.Time {
	n.seenMu.Lock()
	defer n.seenMu.Unlock()
	return n.seen[nodeID]
}

func (n *Node) handleJoin(w http.ResponseWriter, r *http.Request) {
	// Joins must prove the current key: a token leaked before a rotation
	// must not even be able to queue a request.
	cur, err := n.store.Snapshot()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	peer, err := peerFromState(r.TLS, [][]byte{cur.Key})
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	if peer.NodeID == n.id.ID {
		writeErr(w, http.StatusBadRequest, errors.New("a node cannot join itself"))
		return
	}
	var body joinRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	n.deniedMu.Lock()
	denied := n.denied[peer.NodeID]
	n.deniedMu.Unlock()
	if denied {
		writeJSON(w, http.StatusForbidden, joinResponse{Status: JoinDenied})
		return
	}

	st, err := n.store.Snapshot()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	if m, ok := st.Member(peer.NodeID); ok {
		if !samePublicKey(m.PublicKey, peer.PublicKey) {
			writeErr(w, http.StatusForbidden, errors.New("node ID is pinned to a different key"))
			return
		}
		n.touch(peer.NodeID, r.Header.Get(headerVersion))
		st, _ = n.store.Snapshot()
		writeJSON(w, http.StatusOK, joinResponse{
			Status:     JoinApproved,
			Members:    st.Members,
			Key:        st.Key,
			KeyVersion: st.KeyVersion,
		})
		return
	}

	name := body.Name
	if name == "" {
		name = peer.Name
	}
	var (
		req   JoinRequest
		isNew bool
	)
	err = n.store.Update(func(st *State) error {
		if i := slices.IndexFunc(st.Pending, func(p JoinRequest) bool { return p.NodeID == peer.NodeID }); i >= 0 {
			if !samePublicKey(st.Pending[i].PublicKey, peer.PublicKey) {
				return errors.New("a different key already requested to join as this node")
			}
			st.Pending[i].Endpoints = body.Endpoints
			st.Pending[i].Name = name
			req = st.Pending[i]
			return nil
		}
		req = JoinRequest{
			ID:          uuid.NewString()[:8],
			NodeID:      peer.NodeID,
			Name:        name,
			PublicKey:   peer.PublicKey,
			Endpoints:   body.Endpoints,
			Version:     r.Header.Get(headerVersion),
			RemoteAddr:  r.RemoteAddr,
			RequestedAt: time.Now().UTC(),
		}
		st.Pending = append(st.Pending, req)
		isNew = true
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	if isNew {
		slog.Info("Cluster join requested", "node", req.Name, "node_id", req.NodeID, "request", req.ID)
		if n.OnJoinRequest != nil {
			n.OnJoinRequest(req)
		}
	}
	writeJSON(w, http.StatusAccepted, joinResponse{Status: JoinPending, RequestID: req.ID})
}

func (n *Node) handleMembers(w http.ResponseWriter, r *http.Request, _ Member) {
	var body membersBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := n.store.Update(func(st *State) error {
		for _, m := range body.Members {
			if m.NodeID == "" || len(m.PublicKey) == 0 {
				continue
			}
			if existing, ok := st.Member(m.NodeID); ok && !samePublicKey(existing.PublicKey, m.PublicKey) {
				return fmt.Errorf("member %s is pinned to a different key", m.NodeID)
			}
			if m.NodeID == n.id.ID {
				continue
			}
			m.LastSeen = time.Time{}
			st.upsertMember(m)
			st.Pending = slices.DeleteFunc(st.Pending, func(p JoinRequest) bool { return p.NodeID == m.NodeID })
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (n *Node) handleRotate(w http.ResponseWriter, r *http.Request, from Member) {
	var body rotateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Key) != KeySize {
		writeErr(w, http.StatusBadRequest, errors.New("invalid key"))
		return
	}
	err := n.store.Update(func(st *State) error {
		if body.KeyVersion <= st.KeyVersion {
			return nil
		}
		st.PrevKey = st.Key
		st.Key = body.Key
		st.KeyVersion = body.KeyVersion
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("Cluster key rotated by peer", "from", from.Name, "key_version", body.KeyVersion)
	writeJSON(w, http.StatusOK, struct{}{})
}

func (n *Node) handleLeave(w http.ResponseWriter, _ *http.Request, from Member) {
	err := n.store.Update(func(st *State) error {
		st.Members = slices.DeleteFunc(st.Members, func(m Member) bool { return m.NodeID == from.NodeID })
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	slog.Info("Cluster member left", "node", from.Name)
	writeJSON(w, http.StatusOK, struct{}{})
}

type upgradeBody struct {
	Version string `json:"version,omitempty"`
}

func (n *Node) handleUpgrade(w http.ResponseWriter, r *http.Request, from Member) {
	var body upgradeBody
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	if n.OnUpgrade == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("upgrade not supported on this node"))
		return
	}
	slog.Info("Cluster upgrade requested", "from", from.Name, "version", body.Version)
	writeJSON(w, http.StatusAccepted, struct{}{})
	go func() {
		if err := n.OnUpgrade(body.Version); err != nil {
			slog.Error("Cluster upgrade failed", "error", err)
		}
	}()
}
