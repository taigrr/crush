package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/replica"
)

const (
	subjReplChanged = "crush.repl.changed"

	replSweepInterval  = 30 * time.Second
	replNotifyInterval = 3 * time.Second
	replDebounce       = time.Second
	// replReconcileEvery is how often a replica compares per-session
	// digests with the owner and re-fetches only the sessions that drifted,
	// healing from anything an incremental pull missed (for example an
	// ownership hand-off it could not apply at the time).
	replReconcileEvery = 10 * time.Minute
	// replRepairPage bounds the session IDs per repair request.
	replRepairPage = 50
	// replStartDelay lets workspaces open before the first sweep.
	replStartDelay = 10 * time.Second
	// replMaxBackoff caps the delay between pulls from a failing peer.
	replMaxBackoff = 10 * time.Minute
)

func (s *Server) installReplicationRoutes(mux *routeMux) {
	mux.HandleFunc("GET /v1/replication/projects", s.handleReplicationProjects)
	mux.HandleFunc("GET /v1/replication/changes", s.handleReplicationChanges)
	mux.HandleFunc("GET /v1/replication/digest", s.handleReplicationDigest)
	mux.HandleFunc("GET /v1/replication/repair", s.handleReplicationRepair)
}

// handleReplicationDigest handles "hash owned sessions".
//
//	@Summary		Hash owned sessions
//	@Tags			replication
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/replication/digest [get]
func (s *Server) handleReplicationDigest(w http.ResponseWriter, r *http.Request) {
	digest, err := s.backend.ReplicationDigest(r.Context(), r.URL.Query().Get("root"))
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, digest)
}

// handleReplicationRepair handles "serve drifted sessions".
//
//	@Summary		Serve drifted sessions
//	@Tags			replication
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/replication/repair [get]
func (s *Server) handleReplicationRepair(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ids := q["session"]
	if len(ids) > replRepairPage {
		jsonError(w, http.StatusBadRequest, "too many sessions")
		return
	}
	batch, err := s.backend.ReplicationRepair(r.Context(), q.Get("root"), ids)
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, batch)
}

// handleReplicationProjects handles "list replicated projects".
//
//	@Summary		List replicated projects
//	@Tags			replication
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/replication/projects [get]
func (s *Server) handleReplicationProjects(w http.ResponseWriter, r *http.Request) {
	jsonEncode(w, s.backend.ReplicationProjects(r.Context()))
}

// handleReplicationChanges handles "stream replication changes".
//
//	@Summary		Stream replication changes
//	@Tags			replication
//	@Produce		json
//	@x-crush-scope	"never"
//	@Router			/replication/changes [get]
func (s *Server) handleReplicationChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	embedSince, _ := strconv.ParseInt(q.Get("embed_since"), 10, 64)
	batch, err := s.backend.ReplicationChanges(r.Context(), q.Get("root"), since, embedSince, q.Get("full") != "")
	if err != nil {
		jsonError(w, transferStatus(err), err.Error())
		return
	}
	jsonEncode(w, batch)
}

// replicator pulls peers' owned sessions into local read-only replicas.
// Owners announce changes on the mesh; a periodic sweep catches anything
// missed (or a peer that was offline).
type replicator struct {
	bridge *meshBridge
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	pending   map[string]*time.Timer
	running   map[string]bool
	lastCheck map[string]time.Time
	// unusable holds peer+root pairs whose root cannot be mirrored here;
	// they are skipped for the life of the process.
	unusable map[string]bool
	// backoff delays pulls from peers that keep failing.
	backoff map[string]peerBackoff
}

type peerBackoff struct {
	fails int
	until time.Time
}

// failed records a failed pull from peer and backs off exponentially.
func (r *replicator) failed(peer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.backoff[peer]
	b.fails++
	b.until = time.Now().Add(min(replSweepInterval<<min(b.fails-1, 5), replMaxBackoff))
	r.backoff[peer] = b
}

func (r *replicator) succeeded(peer string) {
	r.mu.Lock()
	delete(r.backoff, peer)
	r.mu.Unlock()
}

func (r *replicator) backingOff(peer string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Now().Before(r.backoff[peer].until)
}

func newReplicator(br *meshBridge) *replicator {
	return &replicator{bridge: br, pending: map[string]*time.Timer{}, running: map[string]bool{}, lastCheck: map[string]time.Time{}, unusable: map[string]bool{}, backoff: map[string]peerBackoff{}}
}

type replChanged struct {
	NodeID string `json:"node_id"`
}

func (r *replicator) start() error {
	self := r.bridge.NodeID()
	if _, err := r.bridge.mesh.Subscribe(subjReplChanged, func(msg *nats.Msg) {
		var ev replChanged
		if json.Unmarshal(msg.Data, &ev) == nil && ev.NodeID != "" && ev.NodeID != self {
			r.schedule(ev.NodeID)
		}
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Go(func() { r.notifyLoop(ctx) })
	r.wg.Go(func() { r.sweepLoop(ctx) })
	return nil
}

func (r *replicator) stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Lock()
	for _, t := range r.pending {
		t.Stop()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// notifyLoop announces local changes so peers pull promptly.
func (r *replicator) notifyLoop(ctx context.Context) {
	var last int64 = -1
	tick := time.NewTicker(replNotifyInterval)
	defer tick.Stop()
	data, _ := json.Marshal(replChanged{NodeID: r.bridge.NodeID()})
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cur := r.bridge.backend.ReplicationMaxSeqs(ctx)
		if cur != last {
			last = cur
			_ = r.bridge.mesh.Conn().Publish(subjReplChanged, data)
		}
	}
}

func (r *replicator) sweepLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(replStartDelay):
	}
	tick := time.NewTicker(replSweepInterval)
	defer tick.Stop()
	for {
		for _, m := range r.bridge.node.Members() {
			r.schedule(m.NodeID)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// schedule debounces a pull from peer.
func (r *replicator) schedule(peer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.pending[peer]; ok {
		t.Reset(replDebounce)
		return
	}
	r.pending[peer] = time.AfterFunc(replDebounce, func() {
		r.mu.Lock()
		delete(r.pending, peer)
		if r.running[peer] {
			r.mu.Unlock()
			r.schedule(peer)
			return
		}
		r.running[peer] = true
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			delete(r.running, peer)
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		r.pull(ctx, peer)
	})
}

// pull fetches and applies every changed project from peer.
func (r *replicator) pull(ctx context.Context, peer string) {
	if r.backingOff(peer) {
		return
	}
	mem, err := r.bridge.node.MemberByID(peer)
	if err != nil {
		return
	}
	var projects []replica.Project
	if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/projects", &projects); err != nil {
		slog.Debug("Replication: peer unreachable", "peer", mem.Name, "error", err)
		r.failed(peer)
		return
	}
	for _, p := range projects {
		key := mem.NodeID + "\x00" + p.Root
		r.mu.Lock()
		skip := r.unusable[key]
		r.mu.Unlock()
		if skip {
			continue
		}
		if err := r.bridge.backend.ReplicaRootUsable(p.Root); err != nil {
			slog.Info("Replication: skipping project that cannot be mirrored here", "peer", mem.Name, "root", p.Root, "error", err)
			r.mu.Lock()
			r.unusable[key] = true
			r.mu.Unlock()
			continue
		}
		ok, transport := r.pullProject(ctx, mem, p)
		if transport {
			// The peer is struggling; leave the rest for a later pass.
			r.failed(peer)
			return
		}
		if ok {
			r.maybeReconcile(ctx, mem, p)
		}
	}
	r.succeeded(peer)
}

// maybeReconcile compares session digests with mem at most every
// replReconcileEvery and re-fetches only the sessions that differ.
func (r *replicator) maybeReconcile(ctx context.Context, mem cluster.Member, p replica.Project) {
	key := mem.NodeID + "\x00" + p.Root
	r.mu.Lock()
	last, seen := r.lastCheck[key]
	if !seen {
		// The first pull after start already brought the replica up to
		// date; check on the next interval.
		r.lastCheck[key] = time.Now()
	}
	due := seen && time.Since(last) > replReconcileEvery
	if due {
		r.lastCheck[key] = time.Now()
	}
	r.mu.Unlock()
	if !due {
		return
	}
	b := r.bridge.backend
	var remote map[string]string
	q := url.Values{"root": {p.Root}}
	if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/digest?"+q.Encode(), &remote); err != nil {
		slog.Debug("Replication: digest failed", "peer", mem.Name, "root", p.Root, "error", err)
		return
	}
	local, err := b.ReplicaDigest(ctx, mem.NodeID, p.Root, p.DataDirRel)
	if err != nil {
		slog.Debug("Replication: local digest failed", "peer", mem.Name, "root", p.Root, "error", err)
		return
	}
	drifted := replica.Drifted(remote, local)
	for len(drifted) > 0 {
		n := min(len(drifted), replRepairPage)
		q := url.Values{"root": {p.Root}, "session": drifted[:n]}
		drifted = drifted[n:]
		var batch replica.Batch
		if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/repair?"+q.Encode(), &batch); err != nil {
			slog.Warn("Replication: repair fetch failed", "peer", mem.Name, "root", p.Root, "error", err)
			return
		}
		batch.Repair = true
		st, err := b.ApplyReplica(ctx, mem.NodeID, batch)
		if err != nil {
			slog.Warn("Replication: repair apply failed", "peer", mem.Name, "root", p.Root, "error", err)
			return
		}
		slog.Debug("Replication repaired drift", "peer", mem.Name, "root", p.Root, "sessions", n, "applied", st.Applied)
	}
}

// pullProject applies one project's pending changes from mem, paging
// until the peer reports nothing more. It reports whether the replica
// ended up current. Only changes are fetched; the owner falls back to a
// full snapshot itself when this node never synced or fell behind its
// retained changelog. transport reports a failure talking to the peer.
func (r *replicator) pullProject(ctx context.Context, mem cluster.Member, p replica.Project) (ok, transport bool) {
	b := r.bridge.backend
	for page := 0; page < 1000; page++ {
		cursor, embed := b.ReplicaCursor(ctx, mem.NodeID, p.Root, p.DataDirRel)
		if cursor >= 0 && p.MaxSeq <= cursor && p.MaxEmbed <= embed {
			return true, false
		}
		q := url.Values{
			"root":        {p.Root},
			"since":       {strconv.FormatInt(cursor, 10)},
			"embed_since": {strconv.FormatInt(embed, 10)},
		}
		var batch replica.Batch
		if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/changes?"+q.Encode(), &batch); err != nil {
			slog.Warn("Replication: fetch failed", "peer", mem.Name, "root", p.Root, "error", err)
			return false, !cluster.IsPeerStatus(err)
		}
		st, err := b.ApplyReplica(ctx, mem.NodeID, batch)
		if err != nil {
			slog.Warn("Replication: apply failed", "peer", mem.Name, "root", p.Root, "error", err)
			return false, false
		}
		if st.Applied+st.Deleted > 0 {
			slog.Debug("Replication applied", "peer", mem.Name, "root", p.Root, "full", batch.Full, "applied", st.Applied, "unchanged", st.Unchanged, "deleted", st.Deleted, "skipped", st.Skipped)
		}
		if !batch.More {
			return true, false
		}
	}
	return true, false
}
