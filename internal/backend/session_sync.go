package backend

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/proto"
)

// Session overview delta sync (GET /v1/session-overviews?since=).
//
// Every node keeps a revision counter over its own sessions' overview
// rows. Rather than hooking every place a session can change (title,
// updated_at, busy, unread, favorite, attach/detach, delete, archive,
// move), each listing is diffed against the previous one: whatever the
// list shows is what is tracked, so no change path can be missed. A
// change bumps the revision and lands in a bounded log; a delta is the
// rows whose last change is newer than the cursor.

const (
	// sessionChangeLogMax bounds the retained changes per node.
	sessionChangeLogMax = 10_000
	// sessionChangeLogMaxAge bounds how long a change is retained.
	sessionChangeLogMaxAge = 24 * time.Hour
	// syncCursorVersion is the version of the encoded sync cursor.
	syncCursorVersion = 1
	// epochBytes is the random size of a change log epoch.
	epochBytes = 8
)

// errInvalidSyncCursor marks a since cursor that cannot be used at all.
var errInvalidSyncCursor = errors.New("invalid sync cursor")

// NodeSyncPos is a position in one node's session change log.
type NodeSyncPos struct {
	// Epoch is random per process start: a restarted node's revisions
	// are not comparable with the old ones.
	Epoch string `json:"e"`
	Rev   uint64 `json:"r"`
}

// syncCursor is the decoded sync cursor: a position per node.
type syncCursor struct {
	V     int                    `json:"v"`
	Nodes map[string]NodeSyncPos `json:"n"`
}

// EncodeSyncCursor encodes per-node positions as an opaque cursor.
func EncodeSyncCursor(nodes map[string]NodeSyncPos) string {
	raw, err := json.Marshal(syncCursor{V: syncCursorVersion, Nodes: nodes})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeSyncCursor decodes a cursor made by [EncodeSyncCursor].
func DecodeSyncCursor(s string) (map[string]NodeSyncPos, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errInvalidSyncCursor
	}
	var cur syncCursor
	if err := json.Unmarshal(raw, &cur); err != nil || cur.V != syncCursorVersion || cur.Nodes == nil {
		return nil, errInvalidSyncCursor
	}
	return cur.Nodes, nil
}

type sessionChange struct {
	rev uint64
	id  string
	at  time.Time
}

// sessionChangeLog tracks one node's own session overview rows.
type sessionChangeLog struct {
	mu    sync.Mutex
	epoch string
	rev   uint64
	// floor is the newest evicted revision: cursors older than it cannot
	// be served.
	floor  uint64
	primed bool
	// current is the last observed row per session.
	current map[string]proto.DeviceSessionOverview
	// changed is the revision of each session's last retained change.
	changed    map[string]uint64
	tombstones map[string]proto.SessionTombstone
	log        []sessionChange

	maxChanges int
	maxAge     time.Duration
	now        func() time.Time
}

func newSessionChangeLog() *sessionChangeLog {
	buf := make([]byte, epochBytes)
	_, _ = rand.Read(buf)
	return &sessionChangeLog{
		epoch:      hex.EncodeToString(buf),
		current:    map[string]proto.DeviceSessionOverview{},
		changed:    map[string]uint64{},
		tombstones: map[string]proto.SessionTombstone{},
		maxChanges: sessionChangeLogMax,
		maxAge:     sessionChangeLogMaxAge,
		now:        time.Now,
	}
}

// sessionDelta is one node's changes since a position.
type sessionDelta struct {
	sessions []proto.DeviceSessionOverview
	deleted  []proto.SessionTombstone
}

// observeSince records rows as the node's current list and, when since
// is set, returns the changes after it. Rows of workspaces in keepRoots
// (unreadable right now) keep their last state rather than being
// tombstoned. ok is false when since cannot be served.
func (l *sessionChangeLog) observeSince(rows []proto.DeviceSessionOverview, keepRoots map[string]bool, since *NodeSyncPos) (pos NodeSyncPos, delta sessionDelta, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observeLocked(rows, keepRoots)
	pos = NodeSyncPos{Epoch: l.epoch, Rev: l.rev}
	if since == nil {
		return pos, delta, true
	}
	if since.Epoch != l.epoch || since.Rev < l.floor || since.Rev > l.rev {
		return pos, delta, false
	}
	for id, rev := range l.changed {
		if rev <= since.Rev {
			continue
		}
		if row, live := l.current[id]; live {
			delta.sessions = append(delta.sessions, row)
		} else if tomb, gone := l.tombstones[id]; gone {
			delta.deleted = append(delta.deleted, tomb)
		}
	}
	return pos, delta, true
}

func (l *sessionChangeLog) observeLocked(rows []proto.DeviceSessionOverview, keepRoots map[string]bool) {
	next := make(map[string]proto.DeviceSessionOverview, len(rows))
	for _, row := range rows {
		next[row.ID] = row
	}
	for id, row := range l.current {
		if _, ok := next[id]; !ok && keepRoots[row.Root] {
			next[id] = row
		}
	}
	if !l.primed {
		l.current, l.primed = next, true
		return
	}
	now := l.now()
	for id, row := range next {
		if prev, ok := l.current[id]; !ok || prev != row {
			delete(l.tombstones, id)
			l.bumpLocked(id, now)
		}
	}
	for id, row := range l.current {
		if _, ok := next[id]; ok {
			continue
		}
		l.tombstones[id] = proto.SessionTombstone{ID: id, NodeID: row.NodeID, Root: row.Root}
		l.bumpLocked(id, now)
	}
	l.current = next
	l.evictLocked(now)
}

func (l *sessionChangeLog) bumpLocked(id string, now time.Time) {
	l.rev++
	l.changed[id] = l.rev
	l.log = append(l.log, sessionChange{rev: l.rev, id: id, at: now})
}

func (l *sessionChangeLog) evictLocked(now time.Time) {
	drop := 0
	for drop < len(l.log) && (len(l.log)-drop > l.maxChanges || now.Sub(l.log[drop].at) > l.maxAge) {
		old := l.log[drop]
		l.floor = old.rev
		if l.changed[old.id] == old.rev {
			delete(l.changed, old.id)
			delete(l.tombstones, old.id)
		}
		drop++
	}
	if drop > 0 {
		l.log = append([]sessionChange(nil), l.log[drop:]...)
	}
}

// sessionChanges returns the backend's change log, created on first use.
func (b *Backend) sessionChanges() *sessionChangeLog {
	b.sessionSyncOnce.Do(func() { b.sessionSync = newSessionChangeLog() })
	return b.sessionSync
}

// unavailableRoots is the set of workspace roots that could not be read.
func unavailableRoots(unavailable []proto.NodeUnavailable) map[string]bool {
	out := map[string]bool{}
	for _, u := range unavailable {
		if u.Root != "" {
			out[u.Root] = true
		}
	}
	return out
}

// slimRows drops the fields slim rows (fields=row) leave out.
func slimRows(rows []proto.DeviceSessionOverview) {
	for i := range rows {
		rows[i].DataDir = ""
	}
}

const (
	// sessionPassTTL is how long a pass snapshot serves later pages.
	sessionPassTTL = 2 * time.Minute
	// sessionPassMax bounds the retained pass snapshots.
	sessionPassMax = 32
)

// sessionPass is one computed list (full or delta) that later pages are
// served from.
type sessionPass struct {
	id   string
	rows []proto.DeviceSessionOverview
	// meta is every page field but the rows and paging.
	meta proto.DeviceSessionOverviewPage
	key  string
	at   time.Time
}

// sessionPassCache keeps recent passes by ID.
type sessionPassCache struct {
	mu     sync.Mutex
	passes map[string]*sessionPass
	now    func() time.Time
}

func (c *sessionPassCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// put stores pass for queries with key and returns its ID.
func (c *sessionPassCache) put(pass *sessionPass, key string) string {
	buf := make([]byte, epochBytes)
	_, _ = rand.Read(buf)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.passes == nil {
		c.passes = map[string]*sessionPass{}
	}
	now := c.clock()
	for id, old := range c.passes {
		if now.Sub(old.at) > sessionPassTTL {
			delete(c.passes, id)
		}
	}
	for len(c.passes) >= sessionPassMax {
		var oldest string
		for id, old := range c.passes {
			if oldest == "" || old.at.Before(c.passes[oldest].at) {
				oldest = id
			}
		}
		delete(c.passes, oldest)
	}
	pass.id, pass.key, pass.at = hex.EncodeToString(buf), key, now
	c.passes[pass.id] = pass
	return pass.id
}

// get returns the live pass id made for key, or nil.
func (c *sessionPassCache) get(id, key string) *sessionPass {
	c.mu.Lock()
	defer c.mu.Unlock()
	pass := c.passes[id]
	if pass == nil || pass.key != key || c.clock().Sub(pass.at) > sessionPassTTL {
		return nil
	}
	return pass
}
