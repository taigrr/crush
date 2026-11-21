package startuptrace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpanAndCountRecordEveryOccurrence(t *testing.T) {
	old := path
	path = filepath.Join(t.TempDir(), "trace.txt")
	t.Cleanup(func() { path = old })

	Span("srv.a b")()
	Span("srv.a b")()
	Count("cli.n", 7)
	Mark("m")
	Mark("m")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 4)
	for _, l := range lines[:2] {
		f := strings.Fields(l)
		require.Len(t, f, 5)
		require.Equal(t, "span", f[1])
		require.Equal(t, "srv.a_b", f[2])
	}
	f := strings.Fields(lines[2])
	require.Equal(t, []string{"count", "cli.n"}, f[1:3])
	require.Equal(t, "7", f[4])
	require.Len(t, strings.Fields(lines[3]), 3)
}

func TestDisabledIsNoop(t *testing.T) {
	old := path
	path = ""
	t.Cleanup(func() { path = old })
	Span("x")()
	Count("x", 1)
	require.False(t, Enabled())
}
