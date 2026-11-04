package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Auto-start checks whether each of the ~130 known language servers is
// installed. One exec.LookPath per server stats every PATH directory
// times every PATHEXT extension: on Windows that is over 100,000 file
// system calls per check, seconds when idle and tens of seconds on a
// loaded machine. commandIndex lists each PATH directory once instead and
// answers every server from that listing.

// commandIndexTTL is how long a PATH listing is reused; it matches how
// long a missing server is remembered (unavailableRetryDelay), so a newly
// installed server is picked up as soon as it was before.
const commandIndexTTL = unavailableRetryDelay

const (
	goosWindows = "windows"
	// pathExtSeparator separates PATHEXT entries (Windows only).
	pathExtSeparator = ";"
)

// commandIndex is the names of the files in the PATH directories.
type commandIndex struct {
	path    string
	pathExt string
	at      time.Time
	// dirs are the PATH directories in order, each with its file names
	// (lower-cased on Windows, where names are case-insensitive).
	dirs []indexedDir
}

type indexedDir struct {
	dir string
	// names maps a folded file name to the name on disk.
	names map[string]string
}

var (
	commandIndexMu  sync.Mutex
	commandIndexNow = time.Now
	cachedIndex     *commandIndex
)

// currentCommandIndex returns the PATH listing, rebuilt when PATH or
// PATHEXT changed or the listing is older than commandIndexTTL.
func currentCommandIndex() *commandIndex {
	path, pathExt := os.Getenv("PATH"), os.Getenv("PATHEXT")
	commandIndexMu.Lock()
	defer commandIndexMu.Unlock()
	now := commandIndexNow()
	if idx := cachedIndex; idx != nil && idx.path == path && idx.pathExt == pathExt && now.Sub(idx.at) < commandIndexTTL {
		return idx
	}
	cachedIndex = buildCommandIndex(path, pathExt, runtime.GOOS, now)
	return cachedIndex
}

func buildCommandIndex(path, pathExt, goos string, now time.Time) *commandIndex {
	idx := &commandIndex{path: path, pathExt: pathExt, at: now}
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// An empty entry means the current directory, which
			// exec.LookPath refuses to use; skip it the same way.
			continue
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := make(map[string]string, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			names[foldName(entry.Name(), goos)] = entry.Name()
		}
		idx.dirs = append(idx.dirs, indexedDir{dir: dir, names: names})
	}
	return idx
}

func foldName(name, goos string) string {
	if goos == goosWindows {
		return strings.ToLower(name)
	}
	return name
}

// candidates are the file names command may have on disk.
func (idx *commandIndex) candidates(command, goos string) []string {
	if goos != goosWindows {
		return []string{command}
	}
	exts := strings.Split(strings.ToLower(idx.pathExt), pathExtSeparator)
	if idx.pathExt == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	lower := strings.ToLower(command)
	out := []string{}
	if ext := filepath.Ext(lower); ext != "" {
		for _, known := range exts {
			if ext == known {
				out = append(out, lower)
				break
			}
		}
	}
	for _, ext := range exts {
		if ext != "" {
			out = append(out, lower+ext)
		}
	}
	return out
}

// has reports whether command is an executable in a PATH directory. A
// listed name is confirmed with one stat, so only real matches cost a
// system call.
func (idx *commandIndex) has(command, goos string) bool {
	names := idx.candidates(command, goos)
	for _, dir := range idx.dirs {
		for _, name := range names {
			onDisk, ok := dir.names[name]
			if !ok {
				continue
			}
			info, err := os.Stat(filepath.Join(dir.dir, onDisk))
			if err != nil || info.IsDir() {
				continue
			}
			if goos == goosWindows || info.Mode()&0o111 != 0 {
				return true
			}
		}
	}
	return false
}

// commandInstalled reports whether a server command can be run. Commands
// with a directory part are checked directly, like exec.LookPath does.
func commandInstalled(command string) bool {
	if strings.ContainsAny(command, `/\`) {
		_, err := exec.LookPath(command)
		return err == nil
	}
	return currentCommandIndex().has(command, runtime.GOOS)
}
