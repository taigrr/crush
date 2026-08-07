package db

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenReadOnly_MissingDatabaseReturnsNil(t *testing.T) {
	t.Parallel()

	// A directory with no crush.db yields (nil, nil) so the caller can
	// skip an uninitialized workspace without erroring.
	conn, err := OpenReadOnly(t.TempDir())
	require.NoError(t, err)
	require.Nil(t, conn)
}

func TestOpenReadOnly_MissingDirectoryReturnsNil(t *testing.T) {
	t.Parallel()

	conn, err := OpenReadOnly(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	require.Nil(t, conn)
}
