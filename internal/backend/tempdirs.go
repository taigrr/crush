package backend

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// tempRoots lists the system temporary directories, both as configured
// and symlink-resolved (/tmp is /private/tmp on macOS).
var tempRoots = sync.OnceValue(func() []string {
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		roots = append(roots, p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
			roots = append(roots, resolved)
		}
	}
	add("/tmp")
	add("/private/tmp")
	add(os.TempDir())
	return roots
})

// isTempPath reports whether p lies under a system temporary directory.
func isTempPath(p string) bool {
	if p == "" {
		return false
	}
	p = filepath.Clean(p)
	for _, root := range tempRoots() {
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// hideTempWorkspaces reports whether workspaces under a temporary
// directory should stay out of the registry and overview list. Tests
// create projects under /tmp that would otherwise pollute a real user's
// picker; a server whose own registry lives in a temp dir is itself a
// test and keeps them.
func (b *Backend) hideTempWorkspaces() bool {
	return b.registry != nil && !isTempPath(b.registry.Path())
}

// isTempWorkspace reports whether a workspace's root or data dir is
// under a temporary directory.
func isTempWorkspace(root, dataDir string) bool {
	return isTempPath(root) || isTempPath(dataDir)
}
