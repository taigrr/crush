package checkpoint

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/taigrr/crush/internal/startuptrace"
)

// loadIgnoreMatcher returns a function yielding the work tree's gitignore
// matcher (nil when there are no patterns). When a cached entry exists its
// revalidation runs in the background so repo setup does not wait on the
// stat walk; the returned function blocks until it finishes.
func loadIgnoreMatcher(workTree string) func() gitignore.Matcher {
	resolve := func() gitignore.Matcher {
		endIgnore := startuptrace.Span("srv.checkpoint.read_gitignore_patterns")
		patterns, err := readIgnorePatterns(workTree)
		endIgnore()
		startuptrace.Count("srv.checkpoint.gitignore_patterns", len(patterns))
		if err == nil && len(patterns) > 0 {
			return gitignore.NewMatcher(patterns)
		}
		return nil
	}
	if !hasCachedIgnore(workTree) {
		m := resolve()
		return func() gitignore.Matcher { return m }
	}
	done := make(chan struct{})
	var m gitignore.Matcher
	go func() {
		defer close(done)
		m = resolve()
	}()
	return func() gitignore.Matcher {
		<-done
		return m
	}
}

func hasCachedIgnore(workTree string) bool {
	root, err := filepath.Abs(workTree)
	if err != nil || strings.Contains(root, "..") {
		return false
	}
	_, ok := ignoreCache.Load(root)
	return ok
}

// ignoreStat records what a stat of a path looked like during a walk so a
// later reuse can detect changes without re-reading directories.
type ignoreStat struct {
	path    string
	exists  bool
	modTime time.Time
	size    int64
}

// errUnsafePath marks a walk that hit a path the cache will not touch
// directly; callers fall back to the uncached go-git walk.
var errUnsafePath = errors.New("unsafe ignore path")

func statFingerprint(path string) ignoreStat {
	if strings.Contains(path, "..") {
		return ignoreStat{path: path, size: -1}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ignoreStat{path: path}
	}
	return ignoreStat{path: path, exists: true, modTime: fi.ModTime(), size: fi.Size()}
}

func (s ignoreStat) fresh() bool {
	if strings.Contains(s.path, "..") {
		return false
	}
	return statFingerprint(s.path) == s
}

type ignoreEntry struct {
	patterns []gitignore.Pattern
	stats    []ignoreStat
}

func (e *ignoreEntry) fresh() bool {
	for _, s := range e.stats {
		if !s.fresh() {
			return false
		}
	}
	return true
}

// ignoreCacheMaxEntries bounds memory for servers that open many work trees;
// the least recently used entry is evicted when full.
const ignoreCacheMaxEntries = 1024

// ignoreCacheStore maps a cleaned absolute work tree to its *ignoreEntry.
type ignoreCacheStore struct {
	mu      sync.Mutex
	entries map[string]*ignoreEntry
	used    map[string]uint64
	tick    uint64
}

func (s *ignoreCacheStore) Load(root string) (*ignoreEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[root]
	if ok {
		s.tick++
		s.used[root] = s.tick
	}
	return e, ok
}

func (s *ignoreCacheStore) Store(root string, e *ignoreEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]*ignoreEntry{}
		s.used = map[string]uint64{}
	}
	if _, ok := s.entries[root]; !ok && len(s.entries) >= ignoreCacheMaxEntries {
		oldest, oldestUsed := "", uint64(0)
		for k, u := range s.used {
			if oldest == "" || u < oldestUsed {
				oldest, oldestUsed = k, u
			}
		}
		delete(s.entries, oldest)
		delete(s.used, oldest)
	}
	s.tick++
	s.entries[root] = e
	s.used[root] = s.tick
}

func (s *ignoreCacheStore) Delete(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, root)
	delete(s.used, root)
}

func (s *ignoreCacheStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

var ignoreCache ignoreCacheStore

// readIgnorePatterns returns the same patterns as
// gitignore.ReadPatterns(osfs.New(workTree), nil), reusing a process-wide
// cached result when no walked directory or probed ignore file changed.
func readIgnorePatterns(workTree string) ([]gitignore.Pattern, error) {
	root, err := filepath.Abs(workTree)
	if err != nil || strings.Contains(root, "..") {
		return walkIgnorePatterns(workTree)
	}
	if e, ok := ignoreCache.Load(root); ok {
		if e.fresh() {
			return e.patterns, nil
		}
	}
	e := &ignoreEntry{}
	ps, err := e.walk(root, nil)
	if err != nil {
		ignoreCache.Delete(root)
		if errors.Is(err, errUnsafePath) {
			return walkIgnorePatterns(workTree)
		}
		return ps, err
	}
	e.patterns = ps
	ignoreCache.Store(root, e)
	return ps, nil
}

func walkIgnorePatterns(workTree string) ([]gitignore.Pattern, error) {
	return gitignore.ReadPatterns(osfs.New(workTree), nil)
}

func (e *ignoreEntry) readFile(root string, path []string, name string) ([]gitignore.Pattern, error) {
	full := filepath.Join(append(append([]string{root}, path...), name)...)
	if strings.Contains(full, "..") {
		return nil, errUnsafePath
	}
	f, err := os.Open(full)
	if err != nil {
		e.stats = append(e.stats, statFingerprint(full))
		return nil, nil
	}
	defer f.Close()
	s := ignoreStat{path: full}
	if fi, err := f.Stat(); err == nil {
		s = ignoreStat{path: full, exists: true, modTime: fi.ModTime(), size: fi.Size()}
	}
	e.stats = append(e.stats, s)
	var ps []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && len(strings.TrimSpace(line)) > 0 {
			ps = append(ps, gitignore.ParsePattern(line, path))
		}
	}
	return ps, nil
}

// walk mirrors gitignore.ReadPatterns while recording fingerprints.
func (e *ignoreEntry) walk(root string, path []string) ([]gitignore.Pattern, error) {
	ps, err := e.readFile(root, path, filepath.Join(".git", "info", "exclude"))
	if err != nil {
		return ps, err
	}
	more, err := e.readFile(root, path, ".gitignore")
	if err != nil {
		return ps, err
	}
	ps = append(ps, more...)

	dir := filepath.Join(append([]string{root}, path...)...)
	if strings.Contains(dir, "..") {
		return ps, errUnsafePath
	}
	e.stats = append(e.stats, statFingerprint(dir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ps, err
	}
	for _, de := range entries {
		if !de.IsDir() || de.Name() == ".git" {
			continue
		}
		child := append(append([]string(nil), path...), de.Name())
		if gitignore.NewMatcher(ps).Match(child, true) {
			continue
		}
		sub, err := e.walk(root, child)
		if err != nil {
			return ps, err
		}
		ps = append(ps, sub...)
	}
	return ps, nil
}
