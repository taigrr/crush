// Package startuptrace records startup phase timestamps for benchmarking.
//
// It is disabled unless CRUSH_STARTUP_TRACE names a file. Each phase is
// appended once per process as "<pid> <phase> <unix-nanos>\n"; [Span] and
// [Count] append every occurrence as five-field lines.
package startuptrace

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	path = os.Getenv("CRUSH_STARTUP_TRACE")
	mu   sync.Mutex
	seen = map[string]bool{}
)

// Enabled reports whether startup tracing is on.
func Enabled() bool { return path != "" }

// Mark records the first occurrence of phase. It is a no-op when tracing
// is disabled.
func Mark(phase string) {
	if path == "" {
		return
	}
	now := time.Now().UnixNano()
	mu.Lock()
	defer mu.Unlock()
	if seen[phase] {
		return
	}
	seen[phase] = true
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%d %s %d\n", os.Getpid(), phase, now)
}

// Span starts a timed span and returns the function that ends it. Unlike
// [Mark], every occurrence is recorded, as
// "<pid> span <label> <start-unix-nanos> <duration-nanos>\n", so callers
// can count how often a phase runs per connect. Labels must be static
// phase names: never include paths, IDs, or other user data.
func Span(label string) func() {
	if path == "" {
		return func() {}
	}
	start := time.Now()
	return func() {
		write(fmt.Sprintf("%d span %s %d %d\n", os.Getpid(), sanitize(label), start.UnixNano(), time.Since(start).Nanoseconds()))
	}
}

// Count records a numeric observation (e.g. files visited by a walk) as
// "<pid> count <label> <unix-nanos> <n>\n". Labels follow the [Span] rules.
func Count(label string, n int) {
	if path == "" {
		return
	}
	write(fmt.Sprintf("%d count %s %d %d\n", os.Getpid(), sanitize(label), time.Now().UnixNano(), n))
}

func sanitize(label string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\t' {
			return '_'
		}
		return r
	}, label)
}

func write(line string) {
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
