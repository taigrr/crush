package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/session"
)

func TestFormatSessions(t *testing.T) {
	t.Parallel()

	sessions := []session.Session{
		{ID: "aaaa1111-rest", Title: "deploy chat", MessageCount: 4, UpdatedAt: 1000},
		{ID: "bbbb2222-rest", Title: "", MessageCount: 2, UpdatedAt: 900, ArchivedAt: 950},
		{ID: "cccc3333-rest", Title: "worker", MessageCount: 1, UpdatedAt: 800, LastFinishedAt: 800, LastSeenAt: 700},
		{ID: "dddd4444-rest", Title: "busy", MessageCount: 1, UpdatedAt: 700, LastFinishedAt: 700, LastSeenAt: 600},
	}

	busy := func(id string) bool { return id == "dddd4444-rest" }
	out := formatSessions(sessions, "aaaa1111-rest", 0, 4, busy)

	// Current session is marked, the other is not.
	require.Contains(t, out, "* aaaa1111-rest")
	require.Contains(t, out, "  bbbb2222-rest")
	// Full ids, not truncated.
	require.NotContains(t, out, "aaaa1111  ")
	// Untitled fallback and status column.
	require.Contains(t, out, "(untitled)")
	require.Contains(t, out, "aaaa1111-rest  Read ")
	require.Contains(t, out, "bbbb2222-rest  Archived")
	require.Contains(t, out, "cccc3333-rest  Unread ")
	require.Contains(t, out, "dddd4444-rest  Running ")
	// Pagination header.
	require.True(t, strings.HasPrefix(out, "Sessions 1-4 of 4:"))
}

func TestFormatSessionsPagination(t *testing.T) {
	t.Parallel()

	sessions := []session.Session{
		{ID: "cccc3333-rest", Title: "page two", MessageCount: 1, UpdatedAt: 800},
	}
	// One session shown, starting at offset 2, out of 5 total.
	out := formatSessions(sessions, "", 2, 5, nil)
	require.Contains(t, out, "Sessions 3-3 of 5:")
	require.Contains(t, out, "offset=3")
}
