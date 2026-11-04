package lsp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), mode))
}

func TestCommandIndexUnix(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "gopls"), 0o755)
	writeFile(t, filepath.Join(first, "notes"), 0o644)
	require.NoError(t, os.Mkdir(filepath.Join(first, "clangd"), 0o755))
	writeFile(t, filepath.Join(second, "clangd"), 0o755)
	path := strings.Join([]string{"", first, filepath.Join(first, "missing"), second, first}, string(os.PathListSeparator))
	idx := buildCommandIndex(path, "", "linux", time.Now())
	require.Len(t, idx.dirs, 2, "missing and repeated directories are skipped")

	tests := []struct {
		command string
		want    bool
	}{
		{"gopls", true},
		{"clangd", true}, // a directory of that name is skipped for the file later in PATH
		{"notes", false}, // not executable
		{"GOPLS", false}, // case-sensitive
		{"rust-analyzer", false},
	}
	if runtime.GOOS == goosWindows {
		t.Skip("execute bits are not meaningful on Windows")
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, idx.has(tt.command, "linux"), tt.command)
	}
}

func TestCommandIndexWindows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Gopls.EXE"), 0o644)
	writeFile(t, filepath.Join(dir, "pwsh.cmd"), 0o644)
	writeFile(t, filepath.Join(dir, "tool.txt"), 0o644)
	idx := buildCommandIndex(dir, ".COM;.EXE;.BAT;.CMD", goosWindows, time.Now())
	tests := []struct {
		command string
		want    bool
	}{
		{"gopls", true},
		{"GOPLS.exe", true},
		{"pwsh", true},
		{"tool", false},
		{"tool.txt", false},
		{"clangd", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, idx.has(tt.command, goosWindows), tt.command)
	}
}

func TestCurrentCommandIndexRebuilds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	now := time.Unix(1000, 0)
	commandIndexMu.Lock()
	commandIndexNow = func() time.Time { return now }
	cachedIndex = nil
	commandIndexMu.Unlock()
	t.Cleanup(func() {
		commandIndexMu.Lock()
		commandIndexNow, cachedIndex = time.Now, nil
		commandIndexMu.Unlock()
	})

	name := "fake-lsp"
	if runtime.GOOS == goosWindows {
		name += ".exe"
	}
	require.False(t, commandInstalled("fake-lsp"))
	writeFile(t, filepath.Join(dir, name), 0o755)
	require.False(t, commandInstalled("fake-lsp"), "the listing is reused within the TTL")
	now = now.Add(commandIndexTTL + time.Second)
	require.True(t, commandInstalled("fake-lsp"), "an expired listing is rebuilt")

	other := t.TempDir()
	t.Setenv("PATH", other)
	require.False(t, commandInstalled("fake-lsp"), "a PATH change rebuilds the listing")
}
