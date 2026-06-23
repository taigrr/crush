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
	}

	out := formatSessions(sessions, "aaaa1111-rest", 0, 2)

	// Current session is marked, the other is not.
	require.Contains(t, out, "* aaaa1111-rest")
	require.Contains(t, out, "  bbbb2222-rest")
	// Full ids, not truncated.
	require.NotContains(t, out, "aaaa1111  ")
	// Untitled fallback and archived marker.
	require.Contains(t, out, "(untitled)")
	require.Contains(t, out, "[archived]")
	// Pagination header.
	require.True(t, strings.HasPrefix(out, "Sessions 1-2 of 2:"))
}

func TestFormatSessionsPagination(t *testing.T) {
	t.Parallel()

	sessions := []session.Session{
		{ID: "cccc3333-rest", Title: "page two", MessageCount: 1, UpdatedAt: 800},
	}
	// One session shown, starting at offset 2, out of 5 total.
	out := formatSessions(sessions, "", 2, 5)
	require.Contains(t, out, "Sessions 3-3 of 5:")
	require.Contains(t, out, "offset=3")
}
