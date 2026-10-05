package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func writeGlobFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "sub/c.go"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("package x"), 0o644))
	}
	return dir
}

func TestGlobTool_Basic(t *testing.T) {
	dir := writeGlobFixture(t)
	tool := NewGlobTool(func(context.Context) string { return dir })

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"pattern":"**/*.go"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "a.go")
	require.Contains(t, resp.Content, "sub/c.go")
	require.NotContains(t, resp.Content, "timed out")
}

func TestGlobTool_TimeoutReturnsPartialWithNote(t *testing.T) {
	dir := writeGlobFixture(t)

	old := globTimeout
	globTimeout = time.Nanosecond
	t.Cleanup(func() { globTimeout = old })

	tool := NewGlobTool(func(context.Context) string { return dir })
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"pattern":"**/*.go"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, "timeout must not be an error response")
	require.Contains(t, resp.Content, "timed out")
	require.Contains(t, resp.Content, "Narrow the path")
	require.NotContains(t, resp.Content, "Results are truncated")
}

func TestGlobTool_ParentCancellationIsAnError(t *testing.T) {
	dir := writeGlobFixture(t)
	tool := NewGlobTool(func(context.Context) string { return dir })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := tool.Run(ctx, fantasy.ToolCall{Input: `{"pattern":"**/*.go"}`})
	require.ErrorIs(t, err, context.Canceled)
}

func TestGlobFilesWithTimeout_DistinguishesCauses(t *testing.T) {
	dir := writeGlobFixture(t)

	t.Run("own deadline", func(t *testing.T) {
		files, _, timedOut, err := globFilesWithTimeout(t.Context(), "**/*.go", dir, 100, time.Nanosecond)
		require.NoError(t, err)
		require.True(t, timedOut)
		require.NotNil(t, files)
	})

	t.Run("parent cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, timedOut, err := globFilesWithTimeout(ctx, "**/*.go", dir, 100, time.Hour)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, timedOut)
	})

	t.Run("completes", func(t *testing.T) {
		files, _, timedOut, err := globFilesWithTimeout(t.Context(), "**/*.go", dir, 100, time.Hour)
		require.NoError(t, err)
		require.False(t, timedOut)
		require.Len(t, files, 3)
	})
}
