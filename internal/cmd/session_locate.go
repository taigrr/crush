package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/projects"
	"github.com/taigrr/crush/internal/session"
)

// sessionIDMatches reports whether query identifies the session id: the
// full UUID, a UUID prefix, the full hash, or a hash prefix.
func sessionIDMatches(id, query string) bool {
	if query == "" {
		return false
	}
	if id == query || strings.HasPrefix(id, query) {
		return true
	}
	return strings.HasPrefix(session.HashID(id), query)
}

// locateSessionProject searches every known project's database for the
// session identified by query and returns the project root that holds
// it. It returns "" with no error when the session is not found in any
// known project, so callers can fall back to the current directory.
func locateSessionProject(ctx context.Context, query string) (string, error) {
	projectList, err := projects.List()
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}

	type hit struct{ id, path string }
	var hits []hit
	seenDataDir := make(map[string]bool)
	seenID := make(map[string]bool)
	for _, p := range projectList {
		if p.DataDir == "" || seenDataDir[p.DataDir] {
			continue
		}
		seenDataDir[p.DataDir] = true

		peeked, err := db.PeekSessions(ctx, p.DataDir)
		if err != nil {
			slog.Debug("Skipping project while locating session", "path", p.Path, "error", err)
			continue
		}
		for _, s := range peeked {
			if seenID[s.ID] || !sessionIDMatches(s.ID, query) {
				continue
			}
			if s.ID == query {
				return p.Path, nil
			}
			seenID[s.ID] = true
			hits = append(hits, hit{id: s.ID, path: p.Path})
		}
	}

	switch len(hits) {
	case 0:
		return "", nil
	case 1:
		return hits[0].path, nil
	default:
		return "", fmt.Errorf("session ID %q is ambiguous (%d matches)", query, len(hits))
	}
}
