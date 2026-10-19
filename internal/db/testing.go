package db

import (
	"path/filepath"
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
	absPath, err := filepath.Abs(filepath.Join(dataDir, "crush.db"))
	if err != nil {
		return
	}
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
