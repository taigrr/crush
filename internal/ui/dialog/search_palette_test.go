package dialog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSearchQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in         string
		wantQuery  string
		wantGlobal bool
	}{
		{"foo", "foo", false},
		{"", "", false},
		{"foo bar", "foo bar", false},
		{"foo/g", "foo", true},  // trailing flag
		{"/foo/g", "foo", true}, // regex-style wrap
		{"foo /g", "foo", true}, // space before flag
		{"multi word/g", "multi word", true},
		{"/g", "/g", false},                 // too short to be a flag; literal
		{"//g", "//g", false},               // strips to empty → literal, not global
		{"foo/global", "foo/global", false}, // not the flag
		{"a/g", "a", true},
		{"/etc/g", "etc", true}, // regex-wrap strips the leading slash
	}
	for _, c := range cases {
		q, g := parseSearchQuery(c.in)
		require.Equalf(t, c.wantQuery, q, "query for %q", c.in)
		require.Equalf(t, c.wantGlobal, g, "global for %q", c.in)
	}
}
