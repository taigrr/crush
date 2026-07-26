package filepathext

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// SmartJoin joins two paths, treating the second path as absolute if it is an
// absolute path. A leading "~" in the second path is expanded to the user's
// home directory.
func SmartJoin(one, two string) string {
	two = ExpandTilde(two)
	if SmartIsAbs(two) {
		return two
	}
	return filepath.Join(one, two)
}

// SmartIsAbs checks if a path is absolute, considering both OS-specific and
// Unix-style paths.
func SmartIsAbs(path string) bool {
	switch runtime.GOOS {
	case "windows":
		return filepath.IsAbs(path) || strings.HasPrefix(filepath.ToSlash(path), "/")
	default:
		return filepath.IsAbs(path)
	}
}

// ExpandTilde expands a leading "~" in path to a home directory. A bare "~" or
// "~/" (and "~\" on Windows) expands to the current user's home directory. The
// form "~username" or "~username/..." expands to that user's home directory.
// If the relevant home directory cannot be resolved, the original path is
// returned unchanged.
func ExpandTilde(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}

	// Find the end of the "~" segment (up to the first separator). Only "/"
	// is a separator on non-Windows; "\" is a legal filename byte there.
	seps := "/"
	if runtime.GOOS == "windows" {
		seps = `/\`
	}
	rest := path[1:]
	sep := strings.IndexAny(rest, seps)
	var name, tail string
	if sep == -1 {
		name = rest
	} else {
		name = rest[:sep]
		tail = rest[sep+1:]
	}

	var home string
	if name == "" {
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			return path
		}
		home = h
	} else {
		u, err := user.Lookup(name)
		if err != nil || u.HomeDir == "" {
			return path
		}
		home = u.HomeDir
	}

	if tail == "" {
		return home
	}
	return filepath.Join(home, tail)
}
