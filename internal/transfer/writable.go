package transfer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CheckWritable reports whether a project could be received at root:
// root (or its nearest existing ancestor, when root does not exist yet)
// must accept a new directory. It probes by creating and removing a
// temporary directory, so a move is refused before any data is sent
// rather than failing halfway (for example a path under /Volumes that
// does not exist on the destination).
func CheckWritable(root string) error {
	dir := filepath.Clean(root)
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
