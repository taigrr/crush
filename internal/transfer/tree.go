package transfer

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultExcludes are directory names never shipped: they are
// regenerable and often huge.
var DefaultExcludes = []string{
	"node_modules", ".venv", "venv", "__pycache__", ".pytest_cache",
	".mypy_cache", ".ruff_cache", ".next", ".turbo", ".gradle", ".parcel-cache",
}

// dataDirSkips are entries inside the Crush data directory that must
// never be copied: the live database (it is rebuilt from rows), its
// lock, and logs.
var dataDirSkips = []string{"crush.db", "crush.db-wal", "crush.db-shm", "crush.db-journal", "logs", "crush.lock", ".lock"}

// TreeOptions controls which parts of a project tree are shipped.
type TreeOptions struct {
	// DataDirRel is the Crush data dir relative to the root.
	DataDirRel string
	// Excludes are directory base names to skip anywhere in the tree.
	Excludes []string
	// Overlay ships only working files: no .git and no Crush internal
	// git/worktree state, for applying onto an existing checkout.
	Overlay bool
	// OnCopy, when set, is called as file contents are written with the
	// file's relative path and the number of bytes just copied, so callers
	// can report progress inside large files.
	OnCopy func(rel string, n int64)
	// Ignored holds git-ignored paths (see [GitIgnored]) to leave out.
	// The Crush data dir is always shipped regardless.
	Ignored map[string]bool
	// Have lists files the destination already holds; matching files are
	// sent as reuse markers instead of their contents.
	Have map[string]FileSig
}

func joinRel(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// keepsDataDir reports whether rel is the data dir, inside it, or one of
// its parents, none of which may be dropped as git-ignored.
func (o TreeOptions) keepsDataDir(rel string) bool {
	if o.DataDirRel == "" {
		return false
	}
	return rel == o.DataDirRel || strings.HasPrefix(rel, o.DataDirRel+"/") || strings.HasPrefix(o.DataDirRel, rel+"/")
}

func (o TreeOptions) skip(rel string, d fs.DirEntry) bool {
	base := d.Name()
	if d.IsDir() && slices.Contains(o.Excludes, base) {
		return true
	}
	if o.DataDirRel != "" {
		if inner, ok := strings.CutPrefix(rel, o.DataDirRel+"/"); ok {
			first, _, _ := strings.Cut(inner, "/")
			if slices.Contains(dataDirSkips, first) || strings.HasSuffix(first, ".lock") {
				return true
			}
			if o.Overlay && (first == "git" || first == "worktrees") {
				return true
			}
		}
	}
	if o.Overlay && (rel == ".git" || strings.HasPrefix(rel, ".git/")) {
		return true
	}
	return o.Ignored[rel] && !o.keepsDataDir(rel)
}

// TreeSize counts the files and bytes [WriteTree] would ship.
func TreeSize(ctx context.Context, root string, opts TreeOptions) (files int, size int64) {
	_ = walkTree(ctx, root, opts, func(_ string, info fs.FileInfo) error {
		if info.Mode().IsRegular() {
			files++
			size += info.Size()
		}
		return nil
	})
	return files, size
}

// walkTree visits every entry WriteTree ships (relative path, info).
func walkTree(ctx context.Context, root string, opts TreeOptions, fn func(rel string, info fs.FileInfo) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if opts.skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(rel, info)
	})
}

// WriteTree adds root's files to tw under treePrefix. onFile, when set, is
// called after each regular file with its size.
func WriteTree(ctx context.Context, tw *tar.Writer, root string, opts TreeOptions, onFile ...func(size int64)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if opts.skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		if !info.Mode().IsRegular() && !info.IsDir() && link == "" {
			return nil // sockets, devices, fifos
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = treePrefix + rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if !info.Mode().IsRegular() {
			return tw.WriteHeader(hdr)
		}
		if sig, ok := opts.Have[rel]; ok && sig.Size == info.Size() {
			if sum, err := hashFile(p); err == nil && sum == sig.SHA256 {
				reuse := *hdr
				reuse.Size = 0
				reuse.Format = tar.FormatPAX
				reuse.PAXRecords = map[string]string{paxReuse: sum}
				if err := tw.WriteHeader(&reuse); err != nil {
					return err
				}
				if opts.OnCopy != nil {
					opts.OnCopy(rel, info.Size())
				}
				for _, cb := range onFile {
					cb(info.Size())
				}
				return nil
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		var dst io.Writer = tw
		if opts.OnCopy != nil {
			dst = copyReporter{w: tw, rel: rel, fn: opts.OnCopy}
		}
		if _, err := io.CopyN(dst, f, hdr.Size); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		for _, cb := range onFile {
			cb(hdr.Size)
		}
		return nil
	})
}

type copyReporter struct {
	w   io.Writer
	rel string
	fn  func(rel string, n int64)
}

func (c copyReporter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.fn(c.rel, int64(n))
	}
	return n, err
}

// extractor writes tree entries under dest, deferring symlinks until all
// regular files exist so a malicious link can never redirect a later
// write outside dest.
type extractor struct {
	dest  string
	links []*tar.Header
	// reuseFrom is the existing destination tree that reuse markers
	// refer to. With reuseInPlace the file is left where it is (the
	// staged tree is overlaid onto reuseFrom) instead of being copied.
	reuseFrom    string
	reuseInPlace bool
}

func (x *extractor) target(name string) (string, error) {
	rel, ok := strings.CutPrefix(name, treePrefix)
	if !ok {
		return "", fmt.Errorf("%w: entry %q outside tree", ErrFormat, name)
	}
	clean := path.Clean(strings.TrimSuffix(rel, "/"))
	if clean == "." {
		return x.dest, nil
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: unsafe path %q", ErrFormat, name)
	}
	return filepath.Join(x.dest, filepath.FromSlash(clean)), nil
}

func (x *extractor) entry(hdr *tar.Header, r io.Reader) error {
	dst, err := x.target(hdr.Name)
	if err != nil {
		return err
	}
	mode := fs.FileMode(hdr.Mode).Perm()
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(dst, mode|0o700)
	case tar.TypeReg:
		if sum, ok := hdr.PAXRecords[paxReuse]; ok {
			return x.reuse(dst, sum, hdr)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := x.noSymlinkParents(dst); err != nil {
			return err
		}
		if fi, err := os.Lstat(dst); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			if err := os.Remove(dst); err != nil {
				return err
			}
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Chtimes(dst, hdr.ModTime, hdr.ModTime)
	case tar.TypeSymlink:
		x.links = append(x.links, hdr)
		return nil
	default:
		return nil
	}
}

// reuse fills dst from the destination's existing copy of the same
// file, verifying it still matches what the sender has.
func (x *extractor) reuse(dst, sum string, hdr *tar.Header) error {
	if x.reuseFrom == "" {
		return fmt.Errorf("%w: unexpected reuse entry", ErrFormat)
	}
	rel, err := filepath.Rel(x.dest, dst)
	if err != nil {
		return err
	}
	src := filepath.Join(x.reuseFrom, rel)
	if err := (&extractor{dest: x.reuseFrom}).noSymlinkParents(src); err != nil {
		return err
	}
	fi, err := os.Lstat(src)
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s changed during the transfer; retry the move", ErrReuse, rel)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	h := sha256.New()
	if x.reuseInPlace {
		if _, err := io.Copy(h, in); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != sum {
			return fmt.Errorf("%w: %s changed during the transfer; retry the move", ErrReuse, rel)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := x.noSymlinkParents(dst); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode).Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		_ = os.Remove(dst)
		return fmt.Errorf("%w: %s changed during the transfer; retry the move", ErrReuse, rel)
	}
	return os.Chtimes(dst, hdr.ModTime, hdr.ModTime)
}

// finish creates the deferred symlinks.
func (x *extractor) finish() error {
	for _, hdr := range x.links {
		dst, err := x.target(hdr.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.Remove(dst)
		if err := os.Symlink(hdr.Linkname, dst); err != nil {
			return err
		}
	}
	return nil
}

// ReadGitState inspects the repository at root, or returns nil when root
// is not a git work tree.
func ReadGitState(ctx context.Context, root string) *GitState {
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if top, err := run("rev-parse", "--show-toplevel"); err != nil || top == "" {
		return nil
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return nil
	}
	branch, _ := run("rev-parse", "--abbrev-ref", "HEAD")
	status, err := run("status", "--porcelain", "--untracked-files=normal")
	return &GitState{Head: head, Branch: branch, Clean: err == nil && status == ""}
}

// DestState classifies what already exists at a destination path.
type DestState struct {
	Exists bool
	// Empty is true when the path holds nothing but (possibly nested)
	// empty directories — e.g. the skeleton mirrored by bootstrap.
	Empty bool
	Git   *GitState
}

// InspectDest examines path. Entries under any of ignore (relative
// paths, e.g. the Crush data dir holding replica databases) do not count
// against emptiness.
func InspectDest(ctx context.Context, p string, ignore ...string) (DestState, error) {
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return DestState{Empty: true}, nil
	}
	if err != nil {
		return DestState{}, err
	}
	if !fi.IsDir() {
		return DestState{}, fmt.Errorf("destination %s exists and is not a directory", p)
	}
	empty := true
	errFound := errors.New("found")
	walkErr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel, rerr := filepath.Rel(p, path); rerr == nil && slices.Contains(ignore, filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			empty = false
			return errFound
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errFound) {
		return DestState{}, walkErr
	}
	st := DestState{Exists: true, Empty: empty}
	if !empty {
		st.Git = ReadGitState(ctx, p)
	}
	return st, nil
}

// noSymlinkParents refuses writes whose parent chain (below dest)
// passes through a symlink, which could point outside dest when
// overlaying onto an existing checkout.
func (x *extractor) noSymlinkParents(dst string) error {
	for dir := filepath.Dir(dst); dir != x.dest && strings.HasPrefix(dir, x.dest); dir = filepath.Dir(dir) {
		fi, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to write through symlink %s", ErrFormat, dir)
		}
	}
	return nil
}
