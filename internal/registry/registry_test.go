package registry

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_AddAndList(t *testing.T) {
	t.Parallel()
	s := NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))

	require.NoError(t, s.Add(Entry{Root: "/proj1", DataDir: "/proj1/.crush", LastUsed: 100}))
	require.NoError(t, s.Add(Entry{Root: "/proj2", DataDir: "/proj2/.crush", LastUsed: 200}))

	entries, err := s.List()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	// Most-recently-used first.
	require.Equal(t, "/proj2", entries[0].Root)
	require.Equal(t, "/proj1", entries[1].Root)
}

func TestStore_DedupLastWinsKeepsMaxLastUsed(t *testing.T) {
	t.Parallel()
	s := NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))

	require.NoError(t, s.Add(Entry{Root: "/proj", DataDir: "/old/.crush", LastUsed: 300}))
	require.NoError(t, s.Add(Entry{Root: "/proj", DataDir: "/new/.crush", LastUsed: 100}))

	entries, err := s.List()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "/new/.crush", entries[0].DataDir, "last record wins for data dir")
	require.Equal(t, int64(300), entries[0].LastUsed, "last-used never moves backwards")
}

func TestStore_ListMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	s := NewWithPath(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))

	entries, err := s.List()
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestStore_IgnoresEmptyRoot(t *testing.T) {
	t.Parallel()
	s := NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))

	require.NoError(t, s.Add(Entry{Root: "", DataDir: "/x"}))

	entries, err := s.List()
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestStore_CompactDropsSuperseded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workspaces.jsonl")
	s := NewWithPath(path)

	require.NoError(t, s.Add(Entry{Root: "/a", DataDir: "/a1", LastUsed: 1}))
	require.NoError(t, s.Add(Entry{Root: "/a", DataDir: "/a2", LastUsed: 2}))
	require.NoError(t, s.Add(Entry{Root: "/b", DataDir: "/b1", LastUsed: 3}))
	require.NoError(t, s.Compact())

	entries, err := s.List()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "/b", entries[0].Root)
	require.Equal(t, "/a2", entries[1].DataDir)
}
