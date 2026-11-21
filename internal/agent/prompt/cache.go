package prompt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/skills"
	"golang.org/x/sync/singleflight"
)

// inputCacheTTL bounds how long cached prompt inputs are reused even when
// their stat fingerprint is unchanged. Git status reflects unstaged work
// tree edits that touch no file under .git, and skills can be edited in
// nested dirs, so the fingerprint alone cannot catch every change.
const inputCacheTTL = 5 * time.Second

// inputCacheMaxEntries bounds per-cache memory for servers that host many
// workspaces (entries are small: a git status string or a skills slice);
// expired entries are pruned first, then the least recently used.
const inputCacheMaxEntries = 1024

type cachedInput[T any] struct {
	key   string
	at    time.Time
	used  uint64
	value T
}

// inputCache memoizes an expensive prompt input per identity (working dir
// or skills path set). Entries are reused while their fingerprint matches
// and they are younger than inputCacheTTL; concurrent misses for the same
// identity share a single computation.
type inputCache[T any] struct {
	mu      sync.Mutex
	entries map[string]cachedInput[T]
	tick    uint64
	group   singleflight.Group
	now     func() time.Time
	// refingerprint re-reads the fingerprint after compute, for inputs
	// that compute itself mutates (git status refreshes .git/index).
	refingerprint bool
}

func newInputCache[T any]() *inputCache[T] {
	return &inputCache[T]{entries: map[string]cachedInput[T]{}, now: time.Now}
}

func (c *inputCache[T]) get(id string, fingerprint func() string, compute func() T) T {
	key := fingerprint()
	c.mu.Lock()
	if e, ok := c.entries[id]; ok && e.key == key && c.now().Sub(e.at) < inputCacheTTL {
		c.tick++
		e.used = c.tick
		c.entries[id] = e
		c.mu.Unlock()
		return e.value
	}
	c.mu.Unlock()

	v, _, _ := c.group.Do(id+"\x00"+key, func() (any, error) {
		start := c.now()
		value := compute()
		// Otherwise keep the pre-compute key so a change racing with
		// compute forces the next call to recompute.
		after := key
		if c.refingerprint {
			after = fingerprint()
		}
		c.mu.Lock()
		c.pruneLocked(id)
		c.tick++
		c.entries[id] = cachedInput[T]{key: after, at: start, used: c.tick, value: value}
		c.mu.Unlock()
		return value, nil
	})
	return v.(T)
}

func (c *inputCache[T]) pruneLocked(id string) {
	if _, ok := c.entries[id]; ok || len(c.entries) < inputCacheMaxEntries {
		return
	}
	now := c.now()
	for k, e := range c.entries {
		if now.Sub(e.at) >= inputCacheTTL {
			delete(c.entries, k)
		}
	}
	if len(c.entries) < inputCacheMaxEntries {
		return
	}
	oldest, oldestUsed := "", uint64(0)
	for k, e := range c.entries {
		if oldest == "" || e.used < oldestUsed {
			oldest, oldestUsed = k, e.used
		}
	}
	delete(c.entries, oldest)
}

func (c *inputCache[T]) reset() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

var (
	gitStatusCache = func() *inputCache[string] {
		c := newInputCache[string]()
		c.refingerprint = true
		return c
	}()
	userSkillCache = newInputCache[[]*skills.Skill]()
)

// statKey appends a cheap fingerprint of path (size + mtime, or "-" when
// missing) to sb.
func statKey(sb *strings.Builder, path string) {
	sb.WriteString(path)
	sb.WriteByte('=')
	if strings.Contains(path, "..") {
		sb.WriteString("!;")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		sb.WriteString("-;")
		return
	}
	sb.WriteString(strconv.FormatInt(info.Size(), 10))
	sb.WriteByte(':')
	sb.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
	sb.WriteByte(';')
}

// resolveGitDirs returns the git dir and common dir for a work tree,
// following a ".git" file (linked worktrees, submodules).
func resolveGitDirs(dir string) (gitDir, commonDir string) {
	gitDir = filepath.Join(dir, ".git")
	if strings.Contains(gitDir, "..") {
		return gitDir, gitDir
	}
	info, err := os.Stat(gitDir)
	if err != nil || info.IsDir() {
		return gitDir, gitDir
	}
	data, err := os.ReadFile(gitDir)
	if err != nil {
		return gitDir, gitDir
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return gitDir, gitDir
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	gitDir = filepath.Clean(target)
	commonDir = gitDir
	if strings.Contains(gitDir, "..") {
		return gitDir, commonDir
	}
	commonFile := filepath.Join(gitDir, "commondir")
	if strings.Contains(commonFile, "..") {
		return gitDir, commonDir
	}
	if data, err := os.ReadFile(commonFile); err == nil {
		common := strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		commonDir = filepath.Clean(common)
	}
	return gitDir, commonDir
}

func gitFingerprint(dir string) string {
	gitDir, commonDir := resolveGitDirs(dir)
	var sb strings.Builder
	statKey(&sb, filepath.Join(gitDir, "HEAD"))
	statKey(&sb, filepath.Join(gitDir, "index"))
	statKey(&sb, filepath.Join(commonDir, "packed-refs"))
	statKey(&sb, filepath.Join(commonDir, "refs", "heads"))
	headFile := filepath.Join(gitDir, "HEAD")
	if strings.Contains(headFile, "..") {
		return sb.String()
	}
	if head, err := os.ReadFile(headFile); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: "); ok && !strings.Contains(ref, "..") {
			statKey(&sb, filepath.Join(commonDir, filepath.FromSlash(ref)))
		}
	}
	return sb.String()
}

// cachedGitStatus returns getGitStatus(dir), shared across concurrent and
// repeated prompt builds while the repository's HEAD, index and refs are
// unchanged and the entry is younger than inputCacheTTL.
func cachedGitStatus(ctx context.Context, dir string) string {
	return gitStatusCache.get(dir, func() string { return gitFingerprint(dir) }, func() string {
		return getGitStatus(context.WithoutCancel(ctx), dir)
	})
}

func skillsFingerprint(paths []string) string {
	var sb strings.Builder
	for _, root := range paths {
		statKey(&sb, root)
		if strings.Contains(root, "..") {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			child := filepath.Join(root, entry.Name())
			statKey(&sb, child)
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				statKey(&sb, filepath.Join(child, skills.SkillFileName))
			}
		}
	}
	return sb.String()
}

// cachedDiscoverSkills returns skills.Discover(paths), reused while the
// skills dirs' entry sets and SKILL.md stats are unchanged and the entry
// is younger than inputCacheTTL. The returned slice is a fresh copy.
func cachedDiscoverSkills(paths []string) []*skills.Skill {
	id := strings.Join(paths, "\x00")
	found := userSkillCache.get(id, func() string { return skillsFingerprint(paths) }, func() []*skills.Skill {
		return skills.Discover(paths)
	})
	return slices.Clone(found)
}
