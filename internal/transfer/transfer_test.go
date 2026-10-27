package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func openDB(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	return conn
}

func seed(t *testing.T, conn *sql.DB) {
	t.Helper()
	now := time.Now().UnixMilli()
	exec := func(q string, args ...any) {
		_, err := conn.Exec(q, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('s1','Root',0,10,20,1.5,?,?)`, now, now)
	exec(`INSERT INTO sessions (id, parent_session_id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('s1-child','s1','Child',0,0,0,0,?,?)`, now, now)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m1','s1','user','[{"type":"text","data":{"text":"hi"}}]',?,?)`, now, now)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m2','s1','assistant','[]',?,?)`, now+1, now+1)
	exec(`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('m3','s1-child','user','[]',?,?)`, now, now)
	exec(`INSERT INTO files (id, session_id, path, content, version, created_at, updated_at) VALUES ('f1','s1','/x/a.go','package a',0,?,?)`, now, now)
	exec(`INSERT INTO embeddings (source_type, source_id, chunk_idx, signature, dim, vec, session_id, created_at) VALUES ('message','m1',0,'sig',2,?, 's1', ?)`, []byte{1, 2, 3, 4, 5, 6, 7, 8}, now)
	// An unrelated session must not travel.
	exec(`INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES ('other','Other',0,0,0,0,?,?)`, now, now)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func TestExportImportFreshRoundTrip(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	src := t.TempDir()
	require.NoError(t, os.Chmod(src, 0o755))
	git(t, src, "init", "-q")
	writeFile(t, filepath.Join(src, "main.go"), "package main\n")
	git(t, src, "add", ".")
	git(t, src, "commit", "-qm", "init")
	writeFile(t, filepath.Join(src, "wip.go"), "package main // uncommitted\n")
	writeFile(t, filepath.Join(src, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(src, "node_modules", "big", "index.js"), "x")
	require.NoError(t, os.Symlink("main.go", filepath.Join(src, "link.go")))

	srcData := filepath.Join(src, ".crush")
	conn := openDB(t, srcData)
	seed(t, conn)
	writeFile(t, filepath.Join(srcData, "git", "HEAD"), "ref: refs/heads/main\n")

	var buf bytes.Buffer
	gs := ReadGitState(ctx, src)
	require.NotNil(t, gs)
	require.False(t, gs.Clean)
	require.NoError(t, Export(ctx, &buf, ExportParams{
		DB:       conn,
		Plan:     PlanFresh,
		Manifest: Manifest{SessionID: "s1", Root: src, DataDirRel: ".crush", Git: gs},
	}))

	dst := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(dst, "skeleton", "dir"), 0o755))
	var dstConn *sql.DB
	res, err := Import(ctx, &buf, ImportParams{
		Plan: PlanFresh,
		Root: dst,
		Check: func(m Manifest) error {
			require.ElementsMatch(t, []string{"s1", "s1-child"}, m.SessionIDs)
			return nil
		},
		OpenDB: func(ctx context.Context, dataDir string) (*sql.DB, func(), error) {
			c, err := db.Connect(ctx, dataDir)
			dstConn = c
			return c, func() {}, err
		},
	})
	require.NoError(t, err)
	require.Equal(t, PlanFresh, res.Plan)
	t.Cleanup(func() { _ = db.Release(filepath.Join(dst, ".crush")) })

	for _, f := range []string{"main.go", "wip.go", ".env", ".git/HEAD", ".crush/git/HEAD"} {
		require.FileExists(t, filepath.Join(dst, f))
	}
	require.NoDirExists(t, filepath.Join(dst, "node_modules"))
	require.DirExists(t, filepath.Join(dst, "skeleton", "dir"), "the mirrored skeleton is kept")
	target, err := os.Readlink(filepath.Join(dst, "link.go"))
	require.NoError(t, err)
	require.Equal(t, "main.go", target)
	require.Equal(t, gs.Head, ReadGitState(ctx, dst).Head)
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	require.Equal(t, srcInfo.Mode().Perm(), dstInfo.Mode().Perm(), "root keeps its permissions")

	var count, msgCount int
	var cost float64
	require.NoError(t, dstConn.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count))
	require.Equal(t, 3, count)
	require.NoError(t, dstConn.QueryRow(`SELECT message_count, cost FROM sessions WHERE id='s1'`).Scan(&msgCount, &cost))
	require.Equal(t, 1.5, cost)
	var vec []byte
	require.NoError(t, dstConn.QueryRow(`SELECT vec FROM embeddings WHERE source_id='m1'`).Scan(&vec))
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, vec)
	require.NoError(t, dstConn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id='other'`).Scan(&count))
	require.Zero(t, count)
}

func TestDecideNeverClobbers(t *testing.T) {
	t.Parallel()
	clean := &GitState{Head: "aaa", Clean: true}
	dirty := &GitState{Head: "aaa", Clean: false}
	plan, err := Decide(DestState{Empty: true}, clean, "/p", ConflictRefuse)
	require.NoError(t, err)
	require.Equal(t, PlanFresh, plan)

	plan, err = Decide(DestState{Exists: true, Git: clean}, &GitState{Head: "aaa"}, "/p", ConflictRefuse)
	require.NoError(t, err)
	require.Equal(t, PlanOverlay, plan)

	_, err = Decide(DestState{Exists: true, Git: dirty}, clean, "/p", ConflictRefuse)
	var ce *ConflictError
	require.ErrorAs(t, err, &ce)
	require.Contains(t, ce.Reason, "uncommitted")

	_, err = Decide(DestState{Exists: true, Git: &GitState{Head: "bbb", Clean: true}}, clean, "/p", ConflictRefuse)
	require.ErrorAs(t, err, &ce)

	plan, err = Decide(DestState{Exists: true}, clean, "/p", ConflictKeep)
	require.NoError(t, err)
	require.Equal(t, PlanDBOnly, plan)
}

func TestExtractorRejectsTraversal(t *testing.T) {
	t.Parallel()
	x := &extractor{dest: t.TempDir()}
	_, err := x.target("tree/../../etc/passwd")
	require.ErrorIs(t, err, ErrFormat)
	_, err = x.target("evil")
	require.ErrorIs(t, err, ErrFormat)
	p, err := x.target("tree/a/b")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(x.dest, "a", "b"), p)
}

func TestFreshImportKeepsReplicaDatabase(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello")
	srcConn := openDB(t, filepath.Join(src, ".crush"))
	seed(t, srcConn)
	var buf bytes.Buffer
	require.NoError(t, Export(ctx, &buf, ExportParams{DB: srcConn, Plan: PlanFresh, Manifest: Manifest{SessionID: "s1", Root: src, DataDirRel: ".crush"}}))

	// The destination already mirrors the project: a skeleton plus a
	// replica database holding another node's session.
	dst := filepath.Join(t.TempDir(), "proj")
	dstData := filepath.Join(dst, ".crush")
	replica := openDB(t, dstData)
	now := time.Now().UnixMilli()
	_, err := replica.Exec(`INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at, owner_node) VALUES ('rep','Replica',0,0,0,0,?,?,'mini2')`, now, now)
	require.NoError(t, err)

	dest, err := InspectDest(ctx, dst, ".crush")
	require.NoError(t, err)
	require.True(t, dest.Empty, "a replica database alone does not make the destination occupied")

	_, err = Import(ctx, &buf, ImportParams{
		Plan: PlanFresh,
		Root: dst,
		OpenDB: func(ctx context.Context, dataDir string) (*sql.DB, func(), error) {
			c, err := db.Connect(ctx, dataDir)
			return c, func() { _ = db.Release(dataDir) }, err
		},
	})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dst, "a.txt"))
	var n int
	require.NoError(t, replica.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id IN ('rep','s1')`).Scan(&n))
	require.Equal(t, 2, n)
}
