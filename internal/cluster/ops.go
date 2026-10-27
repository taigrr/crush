package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Status summarizes this node's view of the cluster.
type Status struct {
	Self       NodeInfo      `json:"self"`
	InCluster  bool          `json:"in_cluster"`
	Listening  string        `json:"listening,omitempty"`
	KeyVersion int           `json:"key_version,omitempty"`
	Members    []Member      `json:"members,omitempty"`
	Pending    []JoinRequest `json:"pending,omitempty"`
}

// Status returns this node's cluster status.
func (n *Node) Status() Status {
	s := Status{Self: n.info(), Listening: n.Listening()}
	st, err := n.store.Snapshot()
	if err != nil {
		return s
	}
	s.InCluster = true
	s.KeyVersion = st.KeyVersion
	s.Members = st.Members
	for i := range s.Members {
		if t := n.LastSeen(s.Members[i].NodeID); t.After(s.Members[i].LastSeen) {
			s.Members[i].LastSeen = t
		}
	}
	s.Pending = st.Pending
	return s
}

// Init creates a new cluster containing only this node and returns its
// join token.
func (n *Node) Init(endpoints []Endpoint) (string, error) {
	st, err := n.store.Init(n.id, endpoints, n.version)
	if err != nil {
		return "", err
	}
	return EncodeToken(st.ClusterID, st.Key), nil
}

// Token returns the current join token.
func (n *Node) Token() (string, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return "", err
	}
	return EncodeToken(st.ClusterID, st.Key), nil
}

// JoinOptions configures [Node.Join].
type JoinOptions struct {
	// Endpoints this node advertises to the cluster.
	Endpoints []Endpoint
	// PollInterval between approval checks. Defaults to 2s.
	PollInterval time.Duration
	// OnPending is called once when the request is waiting for approval.
	OnPending func(requestID string)
}

// Join asks the node at addr to admit this node using token, waiting
// until the request is approved, denied, or ctx ends.
func (n *Node) Join(ctx context.Context, addr, token string, opts JoinOptions) error {
	if n.store.InCluster() {
		st, _ := n.store.Snapshot()
		return fmt.Errorf("this node already belongs to cluster %s (run `crush cluster leave` first)", st.ClusterID)
	}
	clusterID, key, err := DecodeToken(token)
	if err != nil {
		return err
	}
	cert, err := IssueNodeCert(clusterID, key, n.id)
	if err != nil {
		return err
	}
	_, clientTLS := tlsConfigs(
		func() (*tls.Certificate, error) { return &cert, nil },
		func() [][]byte { return [][]byte{key} },
	)
	hc := newPeerHTTPClient(clientTLS)

	var info NodeInfo
	if _, err := n.call(ctx, hc, addr, http.MethodGet, "/cluster/v1/info", nil, &info); err != nil {
		return fmt.Errorf("contact %s: %w", addr, err)
	}
	if info.ClusterID != clusterID {
		return fmt.Errorf("%s belongs to a different cluster", addr)
	}
	if err := CheckCompatible(n.version, info.Version, info.Protocol); err != nil {
		return err
	}

	interval := cmpDuration(opts.PollInterval, 2*time.Second)
	body := joinRequestBody{Name: n.id.Name, Endpoints: opts.Endpoints}
	notified := false
	for {
		var rsp joinResponse
		_, err := n.call(ctx, hc, addr, http.MethodPost, "/cluster/v1/join", body, &rsp)
		switch {
		case rsp.Status == JoinDenied:
			return errors.New("join request was denied")
		case err != nil:
			return fmt.Errorf("join %s: %w", addr, err)
		case rsp.Status == JoinApproved:
			return n.completeJoin(clusterID, rsp, addr, info.NodeID, opts.Endpoints)
		case rsp.Status == JoinPending && !notified:
			notified = true
			if opts.OnPending != nil {
				opts.OnPending(rsp.RequestID)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (n *Node) completeJoin(clusterID string, rsp joinResponse, addr, inviterID string, endpoints []Endpoint) error {
	if len(rsp.Key) != KeySize {
		return errors.New("approved join response is missing the cluster key")
	}
	if _, err := n.store.create(clusterID, rsp.Key, n.id, endpoints, n.version); err != nil {
		return err
	}
	return n.store.Update(func(st *State) error {
		st.KeyVersion = max(rsp.KeyVersion, 1)
		for _, m := range rsp.Members {
			if m.NodeID == n.id.ID {
				continue
			}
			if m.NodeID == inviterID && !slices.ContainsFunc(m.Endpoints, func(e Endpoint) bool { return e.Addr == addr }) {
				m.Endpoints = append([]Endpoint{{Kind: EndpointDirect, Addr: addr}}, m.Endpoints...)
			}
			st.upsertMember(m)
		}
		return nil
	})
}

// findPending resolves a pending request by request ID, node ID, node
// ID prefix, or exact name.
func findPending(st *State, ref string) (JoinRequest, error) {
	var matches []JoinRequest
	for _, p := range st.Pending {
		if p.ID == ref || p.NodeID == ref || p.Name == ref || (len(ref) >= 4 && strings.HasPrefix(p.NodeID, ref)) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return JoinRequest{}, fmt.Errorf("no pending join request matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		return JoinRequest{}, fmt.Errorf("%q matches %d pending requests; use the request ID", ref, len(matches))
	}
}

// Approve admits a pending node and announces it to every member.
func (n *Node) Approve(ctx context.Context, ref string) (Member, error) {
	var m Member
	err := n.store.Update(func(st *State) error {
		req, err := findPending(st, ref)
		if err != nil {
			return err
		}
		m = Member{
			NodeID:    req.NodeID,
			Name:      req.Name,
			PublicKey: req.PublicKey,
			Endpoints: req.Endpoints,
			Version:   req.Version,
			JoinedAt:  time.Now().UTC(),
		}
		st.upsertMember(m)
		st.Pending = slices.DeleteFunc(st.Pending, func(p JoinRequest) bool { return p.NodeID == req.NodeID })
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	slog.Info("Cluster join approved", "node", m.Name, "node_id", m.NodeID)
	n.broadcast(ctx, "/cluster/v1/members", membersBody{Members: []Member{m}}, m.NodeID)
	return m, nil
}

// Deny rejects a pending join request. The node is refused until this
// server restarts.
func (n *Node) Deny(ref string) (JoinRequest, error) {
	var req JoinRequest
	err := n.store.Update(func(st *State) error {
		r, err := findPending(st, ref)
		if err != nil {
			return err
		}
		req = r
		st.Pending = slices.DeleteFunc(st.Pending, func(p JoinRequest) bool { return p.NodeID == r.NodeID })
		return nil
	})
	if err != nil {
		return JoinRequest{}, err
	}
	n.deniedMu.Lock()
	n.denied[req.NodeID] = true
	n.deniedMu.Unlock()
	return req, nil
}

// RotateKey replaces the cluster key and distributes it to members.
// It returns the new join token and any members that could not be
// reached (they must rejoin or receive the key on a later rotation).
func (n *Node) RotateKey(ctx context.Context) (string, []error, error) {
	key, err := NewKey()
	if err != nil {
		return "", nil, err
	}
	var body rotateBody
	err = n.store.Update(func(st *State) error {
		body = rotateBody{Key: key, KeyVersion: st.KeyVersion + 1}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	// Distribute before switching locally so members still trust our
	// current certificate while receiving the new key.
	errs := n.broadcast(ctx, "/cluster/v1/rotate", body, "")
	err = n.store.Update(func(st *State) error {
		st.PrevKey = st.Key
		st.Key = body.Key
		st.KeyVersion = body.KeyVersion
		return nil
	})
	if err != nil {
		return "", errs, err
	}
	tok, err := n.Token()
	return tok, errs, err
}

// Leave tells members this node is leaving and deletes local state.
func (n *Node) Leave(ctx context.Context) []error {
	errs := n.broadcast(ctx, "/cluster/v1/leave", struct{}{}, "")
	_ = n.Stop(ctx)
	if err := n.store.Leave(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// UpgradeAll asks every other member to install version (empty means
// latest) and restart.
func (n *Node) UpgradeAll(ctx context.Context, version string) []error {
	return n.broadcast(ctx, "/cluster/v1/upgrade", upgradeBody{Version: version}, "")
}

// Ping fetches a member's info.
func (n *Node) Ping(ctx context.Context, m Member) (NodeInfo, time.Duration, error) {
	start := time.Now()
	var info NodeInfo
	err := n.callMember(ctx, m, http.MethodGet, "/cluster/v1/info", nil, &info)
	return info, time.Since(start), err
}

// broadcast POSTs body to every other member except skipID, returning
// per-member errors.
func (n *Node) broadcast(ctx context.Context, path string, body any, skipID string) []error {
	var errs []error
	for _, m := range n.otherMembers() {
		if m.NodeID == skipID {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := n.callMember(cctx, m, http.MethodPost, path, body, nil)
		cancel()
		if err != nil {
			slog.Warn("Cluster broadcast failed", "path", path, "member", m.Name, "error", err)
			errs = append(errs, err)
		}
	}
	return errs
}

func cmpDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
