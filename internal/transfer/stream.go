package transfer

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Plan is how a destination will receive the tree.
type Plan string

const (
	// PlanFresh: nothing usable at the destination; ship everything,
	// including .git and Crush's snapshot repo and managed worktrees.
	PlanFresh Plan = "fresh"
	// PlanOverlay: a clean checkout at the same commit exists; ship only
	// working files on top of it.
	PlanOverlay Plan = "overlay"
	// PlanBackup: move the existing destination aside, then ship fresh.
	PlanBackup Plan = "backup"
	// PlanDBOnly: ship no files (the destination already has them).
	PlanDBOnly Plan = "db-only"
)

// ConflictPolicy decides what happens when the destination diverges.
type ConflictPolicy string

const (
	// ConflictRefuse (default) aborts without touching anything.
	ConflictRefuse ConflictPolicy = ""
	// ConflictBackup renames the existing destination aside.
	ConflictBackup ConflictPolicy = "backup"
	// ConflictKeep ships history only and leaves destination files as-is.
	ConflictKeep ConflictPolicy = "keep"
)

// ConflictError explains why a destination cannot take the tree.
type ConflictError struct {
	Path   string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("destination %s already has different state: %s (choose --on-conflict=backup to move it aside, or --on-conflict=keep to move only the conversation)", e.Path, e.Reason)
}

// Decide picks a plan for a destination given the source git state.
// It never chooses a plan that could lose destination data.
func Decide(dest DestState, src *GitState, root string, policy ConflictPolicy) (Plan, error) {
	if !dest.Exists || dest.Empty {
		return PlanFresh, nil
	}
	if src != nil && dest.Git != nil && dest.Git.Clean && dest.Git.Head == src.Head {
		return PlanOverlay, nil
	}
	reason := "it is not a git checkout"
	switch {
	case dest.Git == nil:
	case !dest.Git.Clean:
		reason = "it has uncommitted or untracked changes"
	case src == nil:
		reason = "the source is not a git checkout"
	default:
		reason = fmt.Sprintf("it is at %s but the source is at %s", short(dest.Git.Head), short(src.Head))
	}
	switch policy {
	case ConflictBackup:
		return PlanBackup, nil
	case ConflictKeep:
		return PlanDBOnly, nil
	}
	return "", &ConflictError{Path: root, Reason: reason}
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// ExportParams describes a bundle to write.
type ExportParams struct {
	DB       *sql.DB
	Manifest Manifest
	Plan     Plan
	Excludes []string
}

// Export writes a bundle for p.Manifest.SessionID to w.
func Export(ctx context.Context, w io.Writer, p ExportParams) error {
	m := p.Manifest
	ids, err := SessionTree(ctx, p.DB, m.SessionID)
	if err != nil {
		return fmt.Errorf("collect sessions: %w", err)
	}
	m.Format = FormatVersion
	m.SessionIDs = ids
	m.CreatedAt = time.Now().UTC()
	m.Tree = p.Plan == PlanFresh || p.Plan == PlanOverlay || p.Plan == PlanBackup
	if m.SchemaVersion, err = SchemaVersion(ctx, p.DB); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if fi, err := os.Stat(m.Root); err == nil {
		m.RootMode = uint32(fi.Mode().Perm())
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := writeJSONEntry(tw, manifestName, m); err != nil {
		return err
	}

	var rows bytes.Buffer
	enc := json.NewEncoder(&rows)
	if err := ExportRows(ctx, p.DB, ids, func(r Row) error { return enc.Encode(r) }); err != nil {
		return err
	}
	if err := writeBytesEntry(tw, rowsName, rows.Bytes()); err != nil {
		return err
	}

	if m.Tree {
		excludes := p.Excludes
		if excludes == nil {
			excludes = DefaultExcludes
		}
		opts := TreeOptions{DataDirRel: m.DataDirRel, Excludes: excludes, Overlay: p.Plan == PlanOverlay}
		if err := WriteTree(ctx, tw, m.Root, opts); err != nil {
			return fmt.Errorf("pack project tree: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeJSONEntry(tw *tar.Writer, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeBytesEntry(tw, name, data)
}

func writeBytesEntry(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// ImportParams configures [Import].
type ImportParams struct {
	// Plan must match the plan the sender was told to use.
	Plan Plan
	// Check validates the manifest before anything is written.
	Check func(Manifest) error
	// OpenDB opens the destination workspace database at dataDir
	// (creating and migrating it if needed).
	OpenDB func(ctx context.Context, dataDir string) (*sql.DB, func(), error)
	// Root overrides the manifest root. Nodes keep identical paths, so
	// this is only for tests and explicit relocation.
	Root string
}

// ImportResult describes an applied bundle.
type ImportResult struct {
	Manifest Manifest
	Plan     Plan
	// BackupPath is where a pre-existing destination was moved, if any.
	BackupPath string
}

// Import applies a bundle read from r. Files are staged next to the
// destination and only moved into place after the whole stream has
// arrived, so an interrupted transfer never leaves a half-written tree.
func Import(ctx context.Context, r io.Reader, p ImportParams) (ImportResult, error) {
	gz, err := gzip.NewReader(bufio.NewReader(r))
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	hdr, err := tr.Next()
	if err != nil || hdr.Name != manifestName {
		return ImportResult{}, fmt.Errorf("%w: missing manifest", ErrFormat)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return ImportResult{}, fmt.Errorf("%w: manifest: %v", ErrFormat, err)
	}
	if m.Format != FormatVersion {
		return ImportResult{}, fmt.Errorf("%w: format %d", ErrFormat, m.Format)
	}
	if !filepath.IsAbs(m.Root) || m.SessionID == "" || len(m.SessionIDs) == 0 {
		return ImportResult{}, fmt.Errorf("%w: incomplete manifest", ErrFormat)
	}
	if p.Check != nil {
		if err := p.Check(m); err != nil {
			return ImportResult{}, err
		}
	}

	hdr, err = tr.Next()
	if err != nil || hdr.Name != rowsName {
		return ImportResult{}, fmt.Errorf("%w: missing rows", ErrFormat)
	}
	var rows []Row
	dec := json.NewDecoder(tr)
	dec.UseNumber()
	for {
		var row Row
		if err := dec.Decode(&row); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return ImportResult{}, fmt.Errorf("%w: rows: %v", ErrFormat, err)
		}
		rows = append(rows, row)
	}

	res := ImportResult{Manifest: m, Plan: p.Plan}
	root := filepath.Clean(m.Root)
	if p.Root != "" {
		root = filepath.Clean(p.Root)
	}
	var staging string
	if m.Tree {
		if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
			return res, err
		}
		staging = filepath.Join(filepath.Dir(root), ".crush-incoming-"+uuid.NewString()[:8])
		if err := os.Mkdir(staging, 0o700); err != nil {
			return res, err
		}
		defer os.RemoveAll(staging)
		x := &extractor{dest: staging}
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return res, fmt.Errorf("read tree: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if err := x.entry(hdr, tr); err != nil {
				return res, fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
		}
		if err := x.finish(); err != nil {
			return res, err
		}
		if err := place(ctx, staging, root, filepath.FromSlash(m.DataDirRel), p.Plan, &res); err != nil {
			return res, err
		}
		if m.RootMode != 0 {
			// The staging dir is created private; give the project root the
			// source's permissions.
			_ = os.Chmod(root, fs.FileMode(m.RootMode).Perm())
		}
	}

	dataDir := filepath.Join(root, filepath.FromSlash(m.DataDirRel))
	db, release, err := p.OpenDB(ctx, dataDir)
	if err != nil {
		return res, fmt.Errorf("open destination database: %w", err)
	}
	defer release()
	if v, err := SchemaVersion(ctx, db); err == nil && v != m.SchemaVersion {
		return res, fmt.Errorf("%w (source %d, destination %d)", ErrSchemaMismatch, m.SchemaVersion, v)
	}
	if err := ImportRows(ctx, db, m.SessionIDs, rows); err != nil {
		return res, fmt.Errorf("import history: %w", err)
	}
	return res, nil
}

// place moves the staged tree to root according to plan.
func place(ctx context.Context, staging, root, dataRel string, plan Plan, res *ImportResult) error {
	switch plan {
	case PlanFresh, PlanBackup:
		dest, err := InspectDest(ctx, root, dataRel)
		if err != nil {
			return err
		}
		switch {
		case !dest.Exists:
		case dest.Empty:
			// Only the mirrored directory skeleton (and possibly replica
			// databases in the data dir): merge into it, keeping those.
			return overlay(staging, root, false)
		case plan == PlanBackup:
			res.BackupPath = fmt.Sprintf("%s.crush-backup-%s", root, time.Now().Format("20060102-150405"))
			if err := os.Rename(root, res.BackupPath); err != nil {
				return fmt.Errorf("back up %s: %w", root, err)
			}
		default:
			return &ConflictError{Path: root, Reason: "files appeared during the transfer"}
		}
		return os.Rename(staging, root)
	case PlanOverlay:
		return overlay(staging, root, true)
	}
	return fmt.Errorf("unknown plan %q", plan)
}

// overlay moves every staged file onto root, replacing files of the same
// name and leaving everything else in root untouched.
func overlay(staging, root string, skipGit bool) error {
	var links []string
	err := filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(staging, p)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			if fi, err := os.Lstat(dst); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%w: refusing to write through symlink %s", ErrFormat, dst)
			}
			return os.MkdirAll(dst, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			links = append(links, rel)
			return nil
		}
		if skipGit && strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return nil
		}
		if _, err := os.Stat(dst); err == nil && !skipGit {
			// Fresh merge into a skeleton: never replace an existing file
			// (such as a replica database).
			return nil
		}
		return os.Rename(p, dst)
	})
	if err != nil {
		return err
	}
	for _, rel := range links {
		target, err := os.Readlink(filepath.Join(staging, rel))
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		_ = os.Remove(dst)
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
	}
	return nil
}
