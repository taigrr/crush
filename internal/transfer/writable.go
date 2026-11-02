package transfer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CheckWritable reports whether a project could be received at root:
// root (or its nearest existing ancestor, when root does not exist yet)
// must accept a new directory. It probes by creating and removing a
// temporary directory, so a move is refused before any data is sent
// rather than failing halfway (for example a path under /Volumes that
// does not exist on the destination).
//
// root comes from a remote peer, so it must be an absolute path free of
// ".." elements and NUL bytes; anything else is refused rather than
// normalized into a different location.
func CheckWritable(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrNotWritable, root)
	}
	if strings.ContainsRune(root, 0) || strings.Contains(root, "..") {
		return fmt.Errorf("%w: %q contains a disallowed path element", ErrNotWritable, root)
	}
	dir := filepath.Clean(root)
	if strings.Contains(dir, "..") {
		return fmt.Errorf("%w: %q contains a disallowed path element", ErrNotWritable, root)
	}
	for {
		fi, err := os.Stat(dir)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%w: %s is not a directory", ErrNotWritable, dir)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %w", ErrNotWritable, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("%w: no existing parent of %s", ErrNotWritable, root)
		}
		dir = parent
	}
	probe, err := os.MkdirTemp(dir, ".crush-transfer-probe-*")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotWritable, err)
	}
	return os.Remove(probe)
}
