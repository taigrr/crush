package sessionimport

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Source identifies the harness a session was exported from.
type Source string

const (
	SourceAuto   Source = "auto"
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
	SourceGrok   Source = "grok"
	SourcePi     Source = "pi"
)

// harness adapts one external coding-agent harness (Claude Code, Codex,
// Grok Build, Pi, ...) to Crush's import pipeline. Each supported
// harness implements this interface once; the registry wires
// detection, discovery, and parsing to a shared [Session]/[Candidate]
// output so the DB writer and UI stay harness-agnostic.
type harness interface {
	// id is the stable [Source] enum value for this harness.
	id() Source
	// name is the human-readable harness name.
	name() string
	// root is the base directory under home where the harness stores
	// its sessions.
	root(home string) string
	// matchWalk reports whether a walked entry is an importable
	// session path for this harness, and whether descent into it
	// should stop.
	matchWalk(path string, entry fs.DirEntry) (matched, skipDir bool)
	// detect reports whether path was produced by this harness. isDir
	// marks directory input; records holds the parsed JSONL records for
	// files (non-empty) and is nil for directories.
	detect(path string, isDir bool, records []rawRecord) bool
	// parse reads a complete transcript from path.
	parse(path string) (Session, error)
	// discover reads cheap metadata from path into a [Candidate].
	discover(path string) (Candidate, error)
}

// registry lists every supported harness. Order matters for
// auto-detection: the first harness whose detect matches wins, so more
// specific formats must precede more permissive ones.
var registry = []harness{
	piHarness{},
	codexHarness{},
	claudeHarness{},
	grokHarness{},
}

func harnessByID(id Source) harness {
	for _, h := range registry {
		if h.id() == id {
			return h
		}
	}
	return nil
}

// Parse reads a session transcript at path. When source is
// [SourceAuto] the harness is detected from the file contents.
func Parse(path string, source Source) (Session, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return Session{}, err
	}
	if source == SourceAuto {
		source, err = detectSource(resolved)
		if err != nil {
			return Session{}, err
		}
	}
	h := harnessByID(source)
	if h == nil {
		return Session{}, fmt.Errorf("unsupported session source %q", source)
	}
	return h.parse(resolved)
}

func detectSource(path string) (Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		for _, h := range registry {
			if h.detect(path, true, nil) {
				return h.id(), nil
			}
		}
		return "", fmt.Errorf("cannot detect session format from directory %s", path)
	}
	records, _, err := readJSONL(path)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", fmt.Errorf("session file %s is empty", path)
	}
	for _, h := range registry {
		if h.detect(path, false, records) {
			return h.id(), nil
		}
	}
	return "", fmt.Errorf("cannot detect session format from %s", path)
}

// matchJSONLFile reports whether entry is a plain (optionally
// zstd-compressed) JSONL session file. Shared by all file-based
// harnesses.
func matchJSONLFile(entry fs.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	name := entry.Name()
	return strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst")
}
