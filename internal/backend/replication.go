package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/replica"
	"github.com/taigrr/crush/internal/transfer"
)

// maxSkeletonDirs caps the directory skeleton sent with full snapshots.
const maxSkeletonDirs = 20000

// replicaSource is a workspace database this node can read.
type replicaSource struct {
	root, dataDir string
	ws            *Workspace // nil when detached
}

func (b *Backend) replicaSources() []replicaSource {
	var out []replicaSource
	seen := map[string]bool{}
	for _, ws := range b.workspaces.Seq2() {
		if ws.Cfg == nil || ws.App == nil {
			continue
		}
		out = append(out, replicaSource{root: ws.resolvedPath, dataDir: ws.Cfg.Config().Options.DataDirectory, ws: ws})
		seen[ws.resolvedPath] = true
	}
	if b.registry != nil {
		entries, _ := b.registry.List()
		for _, e := range entries {
			if !seen[e.Root] {
				out = append(out, replicaSource{root: e.Root, dataDir: e.DataDir})
				seen[e.Root] = true
			}
		}
	}
	return out
}

func dataDirRel(root, dataDir string) (string, bool) {
	rel, err := filepath.Rel(root, dataDir)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// withSourceDB runs fn against src's database: the live connection when
// attached, otherwise a read-only handle. ok is false when there is no
// database yet.
func withSourceDB(src replicaSource, fn func(*sql.DB) error) (bool, error) {
	if src.ws != nil {
		return true, fn(src.ws.DB())
	}
	conn, err := db.OpenReadOnly(src.dataDir)
	if err != nil || conn == nil {
		return false, err
	}
	defer conn.Close()
	return true, fn(conn)
}

// ReplicationProjects lists the workspaces this node can serve, with
// their newest change sequence so peers can skip unchanged ones.
func (b *Backend) ReplicationProjects(ctx context.Context) []replica.Project {
	var out []replica.Project
	for _, src := range b.replicaSources() {
		rel, ok := dataDirRel(src.root, src.dataDir)
		if !ok {
			continue
		}
		var seq, emb int64
		found, err := withSourceDB(src, func(conn *sql.DB) error {
			seq, emb = replica.MaxSeq(ctx, conn), replica.MaxEmbed(ctx, conn)
			return nil
		})
		if err != nil || !found {
			continue
		}
		out = append(out, replica.Project{Root: src.root, DataDirRel: rel, MaxSeq: seq, MaxEmbed: emb})
	}
	return out
}

// ReplicationChanges serves owned changes after since for root.
func (b *Backend) ReplicationChanges(ctx context.Context, root string, since, embedSince int64, full bool) (replica.Batch, error) {
	self := b.selfNodeID()
	if self == "" {
		return replica.Batch{}, ErrNoCluster
	}
	root = filepath.Clean(root)
	i := slices.IndexFunc(b.replicaSources(), func(s replicaSource) bool { return s.root == root })
	if i < 0 {
		return replica.Batch{}, ErrWorkspaceNotFound
	}
	src := b.replicaSources()[i]
	rel, ok := dataDirRel(src.root, src.dataDir)
	if !ok {
		return replica.Batch{}, errors.New("data directory outside project root")
	}
	var batch replica.Batch
	found, err := withSourceDB(src, func(conn *sql.DB) error {
		var err error
		batch, err = replica.Serve(ctx, conn, self, since, embedSince, full)
		return err
	})
	if err != nil {
		return replica.Batch{}, err
	}
	if !found {
		return replica.Batch{}, ErrWorkspaceNotFound
	}
	batch.Root, batch.DataDirRel = root, rel
	if batch.Full {
		batch.Dirs = skeleton(root, rel)
	}
	return batch, nil
}

// skeleton lists root's directories (relative), skipping VCS internals,
// Crush's data dir, and regenerable trees.
func skeleton(root, dataRel string) []string {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == ".git" || rel == dataRel || slices.Contains(transfer.DefaultExcludes, d.Name()) {
			return filepath.SkipDir
		}
		if len(dirs) >= maxSkeletonDirs {
			return filepath.SkipAll
		}
		dirs = append(dirs, rel)
		return nil
	})
	return dirs
}

// ReplicaCursor returns how far this node has applied peer's changes and
// embeddings for root (-1 when never).
func (b *Backend) ReplicaCursor(ctx context.Context, peer, root, dataRel string) (seq, embed int64) {
	dataDir := filepath.Join(root, filepath.FromSlash(dataRel))
	conn, err := db.OpenReadOnly(dataDir)
	if err != nil || conn == nil {
		return -1, 0
	}
	defer conn.Close()
	return replica.Cursor(ctx, conn, peer), replica.EmbedCursor(ctx, conn, peer)
}

// ApplyReplica applies a batch received from peer, creating the project
// skeleton and database on first contact.
func (b *Backend) ApplyReplica(ctx context.Context, peer string, batch replica.Batch) (replica.Stats, error) {
	if !filepath.IsAbs(batch.Root) {
		return replica.Stats{}, errors.New("replica root must be absolute")
	}
	root := filepath.Clean(batch.Root)
	rel := filepath.FromSlash(batch.DataDirRel)
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return replica.Stats{}, errors.New("invalid replica data dir")
	}
	if batch.Full {
		for _, d := range append([]string{"."}, batch.Dirs...) {
			clean := filepath.Clean(filepath.FromSlash(d))
			if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
				continue
			}
			if err := os.MkdirAll(filepath.Join(root, clean), 0o755); err != nil {
				return replica.Stats{}, fmt.Errorf("create skeleton: %w", err)
			}
		}
	}
	dataDir := filepath.Join(root, rel)
	if _, err := os.Stat(root); err != nil {
		// Incremental batch for a project we never bootstrapped.
		return replica.Stats{}, fmt.Errorf("replica root %s missing: %w", root, err)
	}
	conn, err := db.Connect(ctx, dataDir, db.WithDataDirLock(true))
	if err != nil {
		return replica.Stats{}, err
	}
	defer func() { _ = db.Release(dataDir) }()
	st, err := replica.Apply(ctx, conn, peer, batch)
	if err != nil {
		return st, err
	}
	if err := replica.SetCursor(ctx, conn, peer, batch.MaxSeq); err != nil {
		return st, err
	}
	if err := replica.SetEmbedCursor(ctx, conn, peer, batch.EmbedCursor); err != nil {
		return st, err
	}
	if b.registry != nil {
		if err := b.registry.Add(registry.Entry{Root: root, DataDir: dataDir}); err != nil {
			slog.Debug("Failed to register replica workspace", "root", root, "error", err)
		}
	}
	return st, nil
}

// ReplicationMaxSeqs returns the newest change sequence of every attached
// workspace; a change in the sum means local sessions changed.
func (b *Backend) ReplicationMaxSeqs(ctx context.Context) int64 {
	var total int64
	for _, ws := range b.workspaces.Seq2() {
		if ws.App != nil {
			total += replica.MaxSeq(ctx, ws.DB()) + replica.MaxEmbed(ctx, ws.DB())
		}
	}
	return total
}
