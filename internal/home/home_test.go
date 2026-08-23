package home

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDir(t *testing.T) {
	require.NotEmpty(t, Dir())
}

func TestShort(t *testing.T) {
	d := filepath.Join(Dir(), "documents", "file.txt")
	require.Equal(t, filepath.FromSlash("~/documents/file.txt"), Short(d))
	ad := filepath.FromSlash("/absolute/path/file.txt")
	require.Equal(t, ad, Short(ad))
}

func TestLong(t *testing.T) {
	d := filepath.FromSlash("~/documents/file.txt")
	require.Equal(t, filepath.Join(Dir(), "documents", "file.txt"), Long(d))
	ad := filepath.FromSlash("/absolute/path/file.txt")
	require.Equal(t, ad, Long(ad))
}

func TestExpand(t *testing.T) {
	require.Equal(t, Dir(), Expand("~"))
	require.Equal(t,
		filepath.Join(Dir(), "code", "foss", "vswarm"),
		Expand(filepath.FromSlash("~/code/foss/vswarm")))

	// Absolute and relative paths are returned unchanged.
	ad := filepath.FromSlash("/absolute/path")
	require.Equal(t, ad, Expand(ad))
	rel := filepath.FromSlash("code/foss/vswarm")
	require.Equal(t, rel, Expand(rel))

	// Embedded tildes and ~user forms are left untouched.
	require.Equal(t, "~alice/x", Expand("~alice/x"))
	require.Equal(t, filepath.FromSlash("/a/~/b"), Expand(filepath.FromSlash("/a/~/b")))
}
