package db

import (
	"testing"
)

// TempDataDir returns a fresh temporary data directory whose pooled
// database connection is closed when the test ends, before the directory
// itself is removed. Windows cannot delete a database file that is still
// open, so tests that connect to (or start a workspace on) a temp data
// directory should use this instead of tb.TempDir.
func TempDataDir(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	tb.Cleanup(func() { closeDataDir(dir) })
	return dir
}

// closeDataDir closes the pooled connection for dataDir regardless of
// its reference count.
func closeDataDir(dataDir string) {
	// Same key as Connect: on Windows the temp dir's short (8.3) name and
	// its long name differ, so a hand-built key misses the entry and the
	// database stays open, failing TempDir cleanup.
	absPath := poolKey(dataDir)
	poolMu.Lock()
	entry, ok := pool[absPath]
	if ok {
		delete(pool, absPath)
	}
	poolMu.Unlock()
	if !ok {
		return
	}
	_ = entry.db.Close()
	if entry.lock != nil {
		entry.lock.release()
	}
}
