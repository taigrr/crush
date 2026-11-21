package backend

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
)

// peekCache memoizes read-only session listings of detached workspaces,
// keyed by the database files' size and modification time. Listing every
// known project used to reopen each database on every sidebar refresh,
// which costs hundreds of milliseconds once a user has many projects.
//
// Every read runs under a [stallGuard], so a database whose open never
// returns marks that one workspace unavailable instead of wedging the
// whole overview.
type peekCache struct {
	mu      sync.Mutex
	entries map[string]peekEntry
	// total is the number of sessions held across entries; tick orders
	// entries by last use for eviction once total exceeds the cap.
	total int
	tick  uint64
	// maxSessions caps total; zero means [defaultPeekCacheSessions].
	maxSessions int
	guard       stallGuard
	// peek lists a data directory's sessions; nil means [db.PeekSessions].
	peek func(ctx context.Context, dataDir string) ([]db.PeekedSession, error)
}

type peekEntry struct {
	stamp    string
	sessions []db.PeekedSession
	// overviews is sessions converted and sorted for the overview, built
	// once per stamp. Shared by every caller: read-only.
	overviews []proto.SessionOverview
	at        time.Time
	used      uint64
}

// defaultPeekCacheSessions bounds how many sessions the peek cache holds
// across all workspaces (roughly 0.5 KiB each), evicting least recently
// used workspaces beyond it.
const defaultPeekCacheSessions = 2_000_000

// dbStamp fingerprints a data directory's database files; any write
// changes the WAL or the main file. The shared-memory WAL index header
// (change counter, last frame, salts) is included too, so a WAL rewritten
// from its start at the same size is caught even where mtimes are coarse.
func dbStamp(dataDir string) string {
	stamp := shmHeader(dataDir)
	for _, name := range []string{"crush.db", "crush.db-wal"} {
		fi, err := os.Stat(filepath.Join(dataDir, name))
		if err != nil {
			stamp += name + ":-;"
			continue
		}
		stamp += name + ":" + fi.ModTime().UTC().Format(time.RFC3339Nano) + ":" + strconv.FormatInt(fi.Size(), 10) + ";"
	}
	return stamp
}

func (c *peekCache) get(ctx context.Context, dataDir string) ([]db.PeekedSession, error) {
	return guarded(ctx, &c.guard, filepath.Clean(dataDir), func(ctx context.Context) ([]db.PeekedSession, error) {
		return c.load(ctx, dataDir)
	})
}

func (c *peekCache) load(ctx context.Context, dataDir string) ([]db.PeekedSession, error) {
	e, err := c.loadEntry(ctx, dataDir)
	return e.sessions, err
}

// overviews returns dataDir's sessions in overview display order. When
// the read fails but an earlier listing is cached, that listing is
// returned with stale set (and the error), so a slow or contended read
// degrades to old data instead of an empty workspace.
func (c *peekCache) overviews(ctx context.Context, dataDir string) (ovs []proto.SessionOverview, staleSince time.Time, err error) {
	e, err := guarded(ctx, &c.guard, filepath.Clean(dataDir), func(ctx context.Context) (peekEntry, error) {
		return c.loadEntry(ctx, dataDir)
	})
	if err == nil {
		return e.overviews, time.Time{}, nil
	}
	c.mu.Lock()
	prev, ok := c.entries[dataDir]
	c.mu.Unlock()
	if !ok {
		return nil, time.Time{}, err
	}
	return prev.overviews, prev.at, err
}

func (c *peekCache) loadEntry(ctx context.Context, dataDir string) (peekEntry, error) {
	stamp := dbStamp(dataDir)
	c.mu.Lock()
	if e, ok := c.entries[dataDir]; ok && e.stamp == stamp {
		c.tick++
		e.used = c.tick
		c.entries[dataDir] = e
		c.mu.Unlock()
		return e, nil
	}
	c.mu.Unlock()
	peek := c.peek
	if peek == nil {
		peek = db.PeekSessions
	}
	sessions, err := peek(ctx, dataDir)
	if err != nil {
		return peekEntry{}, err
	}
	e := peekEntry{stamp: stamp, sessions: sessions, overviews: peekedOverviews(sessions), at: time.Now()}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]peekEntry{}
	}
	c.put(dataDir, e)
	c.mu.Unlock()
	return e, nil
}

// peekedOverviews converts a detached workspace's sessions to sorted
// overview entries.
func peekedOverviews(peeked []db.PeekedSession) []proto.SessionOverview {
	if len(peeked) == 0 {
		return nil
	}
	out := make([]proto.SessionOverview, 0, len(peeked))
	for _, p := range peeked {
		out = append(out, proto.SessionOverview{
			ID:         p.ID,
			Title:      p.Title,
			WorkingDir: p.WorkingDir,
			UpdatedAt:  p.UpdatedAt,
			IsBusy:     false, // not attached: no live state
			Unread:     p.Unread(),
			Color:      p.Color,
			Animal:     p.Animal,
			Favorite:   p.Favorite,
			OwnerNode:  p.OwnerNode,
		})
	}
	sortSessionOverviews(out)
	return out
}

// reset drops every memoized listing.
func (c *peekCache) reset() {
	c.mu.Lock()
	c.entries = nil
	c.total = 0
	c.mu.Unlock()
}

// shmHeader returns the first WAL index header of crush.db-shm, or "" when
// there is none. A torn read only costs a spurious reload.
func shmHeader(dataDir string) string {
	f, err := os.Open(filepath.Join(dataDir, "crush.db-shm"))
	if err != nil {
		return ""
	}
	defer f.Close()
	var hdr [48]byte
	n, _ := io.ReadFull(f, hdr[:])
	return "shm:" + string(hdr[:n]) + ";"
}

// put stores e under dataDir and evicts least recently used entries while
// the cache holds more sessions than its cap. The newest entry is always
// kept. Callers hold c.mu.
func (c *peekCache) put(dataDir string, e peekEntry) {
	if prev, ok := c.entries[dataDir]; ok {
		c.total -= len(prev.sessions)
	}
	c.tick++
	e.used = c.tick
	c.entries[dataDir] = e
	c.total += len(e.sessions)
	limit := c.maxSessions
	if limit <= 0 {
		limit = defaultPeekCacheSessions
	}
	for c.total > limit && len(c.entries) > 1 {
		victim, oldest := "", uint64(0)
		for k, v := range c.entries {
			if k != dataDir && (victim == "" || v.used < oldest) {
				victim, oldest = k, v.used
			}
		}
		c.total -= len(c.entries[victim].sessions)
		delete(c.entries, victim)
	}
}
