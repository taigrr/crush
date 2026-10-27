package backend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/db"
)

// peekCache memoizes read-only session listings of detached workspaces,
// keyed by the database files' size and modification time. Listing every
// known project used to reopen each database on every sidebar refresh,
// which costs hundreds of milliseconds once a user has many projects.
type peekCache struct {
	mu      sync.Mutex
	entries map[string]peekEntry
}

type peekEntry struct {
	stamp    string
	sessions []db.PeekedSession
}

// dbStamp fingerprints a data directory's database files; any write
// changes the WAL or the main file.
func dbStamp(dataDir string) string {
	var stamp string
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
	stamp := dbStamp(dataDir)
	c.mu.Lock()
	if e, ok := c.entries[dataDir]; ok && e.stamp == stamp {
		c.mu.Unlock()
		return e.sessions, nil
	}
	c.mu.Unlock()
	sessions, err := db.PeekSessions(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]peekEntry{}
	}
	c.entries[dataDir] = peekEntry{stamp: stamp, sessions: sessions}
	c.mu.Unlock()
	return sessions, nil
}
