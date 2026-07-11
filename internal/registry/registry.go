// Package registry maintains a small, global, cross-workspace index of the
// workspace roots Crush has used, so a freshly started instance (or the
// shared server on startup) can enumerate previously used workspaces and
// where their databases live — without needing any of them attached.
//
// This is the ONLY global state Crush persists about workspaces. Sessions
// themselves are NOT tracked here: they are derived by reading each
// workspace's own SQLite database (.crush/crush.db), which is the single
// source of truth for titles, history, working directories, and read/
// unread state.
//
// The store is a flat JSONL file: one JSON record per line, append-only on
// writes, deduplicated by workspace root on read (last record wins).
// Compact rewrites the file to drop superseded entries.
package registry

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/taigrr/crush/internal/config"
)

// Entry records a workspace root Crush has used.
type Entry struct {
	// Root is the resolved project root whose .crush directory holds the
	// workspace's session database.
	Root string `json:"root"`
	// DataDir is the resolved data directory (the .crush directory) for the
	// workspace, so the database can be located without re-deriving it.
	DataDir string `json:"data_dir"`
	// LastUsed is the unix time this workspace was last recorded, used to
	// order the picker most-recent first.
	LastUsed int64 `json:"last_used"`
}

// Store is a concurrency-safe handle to the global workspace registry file.
type Store struct {
	path string
	mu   sync.Mutex
}

// defaultPath returns the location of the global registry file, alongside
// the global data config in the user's data directory.
func defaultPath() string {
	return filepath.Join(filepath.Dir(config.GlobalConfigData()), "workspaces.jsonl")
}

// New returns a Store backed by the default global registry path.
func New() *Store {
	return &Store{path: defaultPath()}
}

// NewWithPath returns a Store backed by an explicit path (for tests).
func NewWithPath(path string) *Store {
	return &Store{path: path}
}

// Add appends an entry for a workspace. Repeated calls for the same root
// are safe: List deduplicates by root, keeping the most recent record. An
// empty Root is ignored.
func (s *Store) Add(e Entry) error {
	if e.Root == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// List returns the deduplicated set of workspace entries, most-recent
// record per root winning, ordered most-recently-used first. A missing
// file yields an empty slice.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Entry, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	defer f.Close()

	byRoot := make(map[string]Entry)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip malformed lines rather than failing the whole read.
			continue
		}
		if e.Root == "" {
			continue
		}
		// Last record wins; keep the max LastUsed so re-adds never move a
		// workspace backwards in time.
		if prev, ok := byRoot[e.Root]; ok && prev.LastUsed > e.LastUsed {
			e.LastUsed = prev.LastUsed
		}
		byRoot[e.Root] = e
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(byRoot))
	for _, e := range byRoot {
		out = append(out, e)
	}
	// Most-recently-used first; stable tiebreak on root for determinism.
	sortByLastUsedDesc(out)
	return out, nil
}

// Compact rewrites the file with only the deduplicated entries, dropping
// superseded records. Safe to call periodically to bound file growth.
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.listLocked()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "workspaces-*.jsonl.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	w := bufio.NewWriter(tmp)
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			tmp.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func sortByLastUsedDesc(entries []Entry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			a, b := entries[j-1], entries[j]
			if a.LastUsed > b.LastUsed || (a.LastUsed == b.LastUsed && a.Root <= b.Root) {
				break
			}
			entries[j-1], entries[j] = b, a
		}
	}
}
