package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func importTo(t *testing.T, buf *bytes.Buffer, plan Plan, dst string) (ImportResult, error) {
	t.Helper()
	return Import(context.Background(), buf, ImportParams{
		Plan: plan,
		Root: dst,
		OpenDB: func(ctx context.Context, dataDir string) (*sql.DB, func(), error) {
			c, err := db.Connect(ctx, dataDir)
			return c, func() { _ = db.Release(dataDir) }, err
		},
	})
}

func TestExportSkipsGitIgnoredButKeepsDataDir(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	src := t.TempDir()
	git(t, src, "init", "-q")
	writeFile(t, filepath.Join(src, ".gitignore"), "/bin/\n*.log\n")
	writeFile(t, filepath.Join(src, "main.go"), "package main\n")
	writeFile(t, filepath.Join(src, "bin", "app"), "binary")
	writeFile(t, filepath.Join(src, "debug.log"), "noise")
	conn := openDB(t, filepath.Join(src, ".crush"))
	seed(t, conn)
	// The data dir ignores itself, as Crush sets it up.
	writeFile(t, filepath.Join(src, ".crush", ".gitignore"), "*\n")
	writeFile(t, filepath.Join(src, ".crush", "git", "HEAD"), "ref: refs/heads/main\n")

	var buf bytes.Buffer
	require.NoError(t, Export(ctx, &buf, ExportParams{DB: conn, Plan: PlanFresh, Manifest: Manifest{SessionID: "s1", Root: src, DataDirRel: ".crush"}}))
	dst := filepath.Join(t.TempDir(), "proj")
	_, err := importTo(t, &buf, PlanFresh, dst)
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(dst, "main.go"))
	require.FileExists(t, filepath.Join(dst, ".gitignore"))
	require.FileExists(t, filepath.Join(dst, ".crush", "git", "HEAD"), "Crush state travels even though git ignores it")
	require.NoDirExists(t, filepath.Join(dst, "bin"))
	require.NoFileExists(t, filepath.Join(dst, "debug.log"))
}

func TestBackupReusesDestinationFiles(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	big := strings.Repeat("x", 64<<10)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "same.bin"), big)
	writeFile(t, filepath.Join(src, "changed.bin"), big+"new")
	conn := openDB(t, filepath.Join(src, ".crush"))
	seed(t, conn)

	dst := filepath.Join(t.TempDir(), "proj")
	writeFile(t, filepath.Join(dst, "same.bin"), big)
	writeFile(t, filepath.Join(dst, "changed.bin"), big+"old")
	writeFile(t, filepath.Join(dst, "theirs.txt"), "keep in backup")

	have := IndexTree(ctx, dst, TreeOptions{DataDirRel: ".crush"})
	require.Len(t, have, 2)

	var buf bytes.Buffer
	var sent int64
	require.NoError(t, Export(ctx, &buf, ExportParams{
		DB: conn, Plan: PlanBackup, Have: have,
		Manifest:   Manifest{SessionID: "s1", Root: src, DataDirRel: ".crush"},
		OnProgress: func(p Progress) { sent = p.Bytes },
	}))
	require.Less(t, buf.Len(), 8<<10, "the unchanged file is not streamed")
	require.EqualValues(t, 2*len(big)+3, sent, "reused bytes still count toward progress")

	res, err := importTo(t, &buf, PlanBackup, dst)
	require.NoError(t, err)
	require.NotEmpty(t, res.BackupPath)
	got, err := os.ReadFile(filepath.Join(dst, "same.bin"))
	require.NoError(t, err)
	require.Equal(t, big, string(got))
	got, err = os.ReadFile(filepath.Join(dst, "changed.bin"))
	require.NoError(t, err)
	require.Equal(t, big+"new", string(got))
	require.NoFileExists(t, filepath.Join(dst, "theirs.txt"))
	require.FileExists(t, filepath.Join(res.BackupPath, "same.bin"), "the backup keeps its own copy")
}

func TestReuseFailsWhenDestinationChanged(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	big := strings.Repeat("y", 64<<10)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.bin"), big)
	conn := openDB(t, filepath.Join(src, ".crush"))
	seed(t, conn)
	dst := filepath.Join(t.TempDir(), "proj")
	writeFile(t, filepath.Join(dst, "f.bin"), big)
	have := IndexTree(ctx, dst, TreeOptions{})

	var buf bytes.Buffer
	require.NoError(t, Export(ctx, &buf, ExportParams{DB: conn, Plan: PlanBackup, Have: have, Manifest: Manifest{SessionID: "s1", Root: src, DataDirRel: ".crush"}}))
	writeFile(t, filepath.Join(dst, "f.bin"), strings.Repeat("z", 64<<10))

	_, err := importTo(t, &buf, PlanBackup, dst)
	require.ErrorIs(t, err, ErrReuse)
	got, err := os.ReadFile(filepath.Join(dst, "f.bin"))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("z", 64<<10), string(got), "a failed import leaves the destination alone")
}
