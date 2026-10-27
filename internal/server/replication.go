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
	// replFullEvery forces a full snapshot now and then so a replica
	// heals from anything an incremental pull missed (for example an
	// ownership hand-off it could not apply at the time).
	replFullEvery = 10 * time.Minute
)

func (s *Server) installReplicationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/replication/projects", s.handleReplicationProjects)
	mux.HandleFunc("GET /v1/replication/changes", s.handleReplicationChanges)
}

func (s *Server) handleReplicationProjects(w http.ResponseWriter, r *http.Request) {
	jsonEncode(w, s.backend.ReplicationProjects(r.Context()))
}

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

	mu       sync.Mutex
	pending  map[string]*time.Timer
	running  map[string]bool
	lastFull map[string]time.Time
}

func newReplicator(br *meshBridge) *replicator {
	return &replicator{bridge: br, pending: map[string]*time.Timer{}, running: map[string]bool{}, lastFull: map[string]time.Time{}}
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
	mem, err := r.bridge.node.MemberByID(peer)
	if err != nil {
		return
	}
	var projects []replica.Project
	if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/projects", &projects); err != nil {
		slog.Debug("Replication: peer unreachable", "peer", mem.Name, "error", err)
		return
	}
	for _, p := range projects {
		r.pullProject(ctx, mem, p)
	}
}

// pullProject applies one project's pending changes from mem, paging
// until the peer reports nothing more.
func (r *replicator) pullProject(ctx context.Context, mem cluster.Member, p replica.Project) {
	b := r.bridge.backend
	key := mem.NodeID + "\x00" + p.Root
	r.mu.Lock()
	full := time.Since(r.lastFull[key]) > replFullEvery
	if full {
		r.lastFull[key] = time.Now()
	}
	r.mu.Unlock()
	for page := 0; page < 1000; page++ {
		cursor, embed := b.ReplicaCursor(ctx, mem.NodeID, p.Root, p.DataDirRel)
		if !full && cursor >= 0 && p.MaxSeq <= cursor && p.MaxEmbed <= embed {
			return
		}
		q := url.Values{
			"root":        {p.Root},
			"since":       {strconv.FormatInt(cursor, 10)},
			"embed_since": {strconv.FormatInt(embed, 10)},
		}
		if full {
			q.Set("full", "1")
			q.Set("embed_since", "0")
			full = false
		}
		var batch replica.Batch
		if err := r.bridge.node.CallAPI(ctx, mem, http.MethodGet, "/v1/replication/changes?"+q.Encode(), &batch); err != nil {
			slog.Warn("Replication: fetch failed", "peer", mem.Name, "root", p.Root, "error", err)
			return
		}
		st, err := b.ApplyReplica(ctx, mem.NodeID, batch)
		if err != nil {
			slog.Warn("Replication: apply failed", "peer", mem.Name, "root", p.Root, "error", err)
			return
		}
		if st.Applied+st.Deleted > 0 {
			slog.Debug("Replication applied", "peer", mem.Name, "root", p.Root, "applied", st.Applied, "deleted", st.Deleted, "skipped", st.Skipped)
		}
		if !batch.More {
			return
		}
	}
}
