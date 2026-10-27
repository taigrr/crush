package cluster

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

const stateFile = "cluster.json"

// ErrNotInCluster is returned when an operation needs cluster membership
// but this node has not run init or join.
var ErrNotInCluster = errors.New("this node is not part of a cluster (run `crush cluster init` or `crush cluster join`)")

// EndpointKind names how a peer is reached. Only direct connections
// exist today; the field keeps room for relays later.
type EndpointKind string

// EndpointDirect is a plain host:port reachable over IP.
const EndpointDirect EndpointKind = "direct"

// Endpoint is one way of reaching a node.
type Endpoint struct {
	Kind EndpointKind `json:"kind"`
	Addr string       `json:"addr"`
}

// Member is an approved cluster node, pinned by ID and public key.
type Member struct {
	NodeID    string     `json:"node_id"`
	Name      string     `json:"name"`
	PublicKey []byte     `json:"public_key"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
	Version   string     `json:"version,omitempty"`
	JoinedAt  time.Time  `json:"joined_at"`
	LastSeen  time.Time  `json:"last_seen,omitzero"`
}

// JoinRequest is a node asking to join that has not been approved yet.
type JoinRequest struct {
	ID          string     `json:"id"`
	NodeID      string     `json:"node_id"`
	Name        string     `json:"name"`
	PublicKey   []byte     `json:"public_key"`
	Endpoints   []Endpoint `json:"endpoints,omitempty"`
	Version     string     `json:"version,omitempty"`
	RemoteAddr  string     `json:"remote_addr,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
}

// State is the persisted cluster membership of this node.
type State struct {
	ClusterID  string `json:"cluster_id"`
	Key        []byte `json:"key"`
	KeyVersion int    `json:"key_version"`
	// PrevKey is still trusted for verification after a rotation so
	// members that have not yet received the new key keep working.
	PrevKey []byte        `json:"prev_key,omitempty"`
	Members []Member      `json:"members"`
	Pending []JoinRequest `json:"pending,omitempty"`
}

// Member returns the member with nodeID.
func (s State) Member(nodeID string) (Member, bool) {
	i := slices.IndexFunc(s.Members, func(m Member) bool { return m.NodeID == nodeID })
	if i < 0 {
		return Member{}, false
	}
	return s.Members[i], true
}

// upsertMember adds m or replaces the member with the same node ID.
func (s *State) upsertMember(m Member) {
	if i := slices.IndexFunc(s.Members, func(x Member) bool { return x.NodeID == m.NodeID }); i >= 0 {
		if m.JoinedAt.IsZero() {
			m.JoinedAt = s.Members[i].JoinedAt
		}
		if m.LastSeen.IsZero() {
			m.LastSeen = s.Members[i].LastSeen
		}
		s.Members[i] = m
		return
	}
	s.Members = append(s.Members, m)
}

// trustedKeys returns the keys accepted for certificate verification.
func (s *State) trustedKeys() [][]byte {
	keys := [][]byte{s.Key}
	if len(s.PrevKey) > 0 {
		keys = append(keys, s.PrevKey)
	}
	return keys
}

// Store guards the on-disk cluster state.
type Store struct {
	dir string
	mu  sync.Mutex
	st  *State

	watchMu  sync.Mutex
	watchers []func(State)
}

// Watch registers fn to run (in its own goroutine) after every change to
// the persisted state, including joins and leaves.
func (s *Store) Watch(fn func(State)) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watchers = append(s.watchers, fn)
}

func (s *Store) notify(st *State) {
	s.watchMu.Lock()
	ws := slices.Clone(s.watchers)
	s.watchMu.Unlock()
	var snap State
	if st != nil {
		snap = cloneState(*st)
	}
	for _, fn := range ws {
		go fn(snap)
	}
}

// OpenStore loads the cluster state in dir. A missing file yields a
// store with no state (not in a cluster).
func OpenStore(dir string) (*Store, error) {
	s := &Store{dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", stateFile, err)
	}
	s.st = &st
	return s, nil
}

// Dir returns the directory the store lives in.
func (s *Store) Dir() string { return s.dir }

// Snapshot returns a deep copy of the state, or ErrNotInCluster.
func (s *Store) Snapshot() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return State{}, ErrNotInCluster
	}
	return cloneState(*s.st), nil
}

// InCluster reports whether this node belongs to a cluster.
func (s *Store) InCluster() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st != nil
}

// Update applies fn to the state and persists it. fn must not retain st.
func (s *Store) Update(fn func(st *State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return ErrNotInCluster
	}
	next := cloneState(*s.st)
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.saveLocked(&next); err != nil {
		return err
	}
	s.st = &next
	s.notify(s.st)
	return nil
}

// Init creates a new cluster with self as its only member.
func (s *Store) Init(self *Identity, endpoints []Endpoint, version string) (State, error) {
	key, err := NewKey()
	if err != nil {
		return State{}, err
	}
	return s.create(uuid.NewString(), key, self, endpoints, version)
}

// create installs a fresh state for clusterID/key containing only self.
// It refuses to overwrite an existing membership.
func (s *Store) create(clusterID string, key []byte, self *Identity, endpoints []Endpoint, version string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		return State{}, fmt.Errorf("this node already belongs to cluster %s (run `crush cluster leave` first)", s.st.ClusterID)
	}
	st := &State{
		ClusterID:  clusterID,
		Key:        key,
		KeyVersion: 1,
		Members: []Member{{
			NodeID:    self.ID,
			Name:      self.Name,
			PublicKey: self.PublicKey(),
			Endpoints: endpoints,
			Version:   version,
			JoinedAt:  time.Now().UTC(),
		}},
	}
	if err := s.saveLocked(st); err != nil {
		return State{}, err
	}
	s.st = st
	s.notify(st)
	return cloneState(*st), nil
}

// Leave removes all cluster state from this node.
func (s *Store) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, stateFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.st = nil
	s.notify(nil)
	return nil
}

func (s *Store) saveLocked(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.dir, stateFile), data)
}

func cloneState(st State) State {
	out := st
	out.Key = slices.Clone(st.Key)
	out.PrevKey = slices.Clone(st.PrevKey)
	out.Members = make([]Member, len(st.Members))
	for i, m := range st.Members {
		m.PublicKey = slices.Clone(m.PublicKey)
		m.Endpoints = slices.Clone(m.Endpoints)
		out.Members[i] = m
	}
	out.Pending = make([]JoinRequest, len(st.Pending))
	for i, p := range st.Pending {
		p.PublicKey = slices.Clone(p.PublicKey)
		p.Endpoints = slices.Clone(p.Endpoints)
		out.Pending[i] = p
	}
	return out
}

// samePublicKey compares two ed25519 public keys.
func samePublicKey(a, b []byte) bool {
	return len(a) == ed25519.PublicKeySize && ed25519.PublicKey(a).Equal(ed25519.PublicKey(b))
}
