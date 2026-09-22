package cmd

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/version"
)

// TestWriteStderrBanner verifies the banner written to the detached
// server's stderr.log carries enough build identity (version, commit,
// build id, pid) that a runtime panic trace appended after it can be
// attributed to a specific crush build.
func TestWriteStderrBanner(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeStderrBanner(&buf)
	out := buf.String()

	require.True(t, len(out) > 0)
	require.Equal(t, byte('\n'), out[len(out)-1], "banner must be newline-terminated so a following panic starts on its own line")
	require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("\n")), "banner must be a single line")

	require.Contains(t, out, "=== crush server start:")
	require.Contains(t, out, "version="+version.Version)
	require.Contains(t, out, "commit="+version.Commit)
	require.Contains(t, out, "build_id="+version.BuildID)
	require.Contains(t, out, "pid="+strconv.Itoa(os.Getpid()))
	require.Contains(t, out, "go=go")
	require.Contains(t, out, "time=")
}
