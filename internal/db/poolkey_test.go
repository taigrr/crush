package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectSharesConnectionAcrossSymlinkAliases(t *testing.T) {
	t.Cleanup(ResetPool)
	real := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(real, 0o755))
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(real, alias))

	a, err := Connect(t.Context(), filepath.Join(real, ".crush"), WithDataDirLock(true))
	require.NoError(t, err)
	b, err := Connect(t.Context(), filepath.Join(alias, ".crush"), WithDataDirLock(true))
	require.NoError(t, err, "a symlinked alias must reuse the pooled connection, not fight its own lock")
	require.Same(t, a, b)
	require.NoError(t, Release(filepath.Join(alias, ".crush")))
	require.NoError(t, Release(filepath.Join(real, ".crush")))
}

func TestOpenReadOnlyMigratesAnOlderDatabase(t *testing.T) {
	t.Cleanup(ResetPool)
	dir := filepath.Join(t.TempDir(), ".crush")
	conn, err := Connect(t.Context(), dir)
	require.NoError(t, err)
	// Roll the newest migration back by hand, as a database last opened
	// by an older build would look.
	_, err = conn.Exec(`ALTER TABLE sessions DROP COLUMN owner_node`)
	require.NoError(t, err)
	_, err = conn.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, LatestSchemaVersion())
	require.NoError(t, err)
	require.NoError(t, Release(dir))

	ro, err := OpenReadOnly(dir)
	require.NoError(t, err)
	defer ro.Close()
	_, err = ro.Exec(`SELECT owner_node FROM sessions`)
	require.NoError(t, err, "the read-only handle sees the migrated schema")
}
