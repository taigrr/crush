// Package home provides utilities for dealing with the user's home directory.
package home

import (
	"cmp"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var homedir, homedirErr = os.UserHomeDir()

func init() {
	if homedirErr != nil {
		slog.Error("Failed to get user home directory", "error", homedirErr)
	}
}

// Dir returns the user home directory.
func Dir() string {
	return homedir
}

// Config returns the user config directory.
func Config() string {
	return cmp.Or(
		os.Getenv("XDG_CONFIG_HOME"),
		filepath.Join(Dir(), ".config"),
	)
}

// Short replaces the actual home path from [Dir] with `~`.
func Short(p string) string {
	if homedir == "" || !strings.HasPrefix(p, homedir) {
		return p
	}
	return filepath.Join("~", strings.TrimPrefix(p, homedir))
}

// Long replaces the `~` with actual home path from [Dir].
func Long(p string) string {
	if homedir == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	return strings.Replace(p, "~", homedir, 1)
}

// Expand expands a leading `~` or `~/` in p to the user's home
// directory. Unlike [Long], it only rewrites a leading tilde segment
// (`~` alone or `~/...`), leaving embedded tildes and `~user` forms
// untouched, and returns p unchanged when the home directory is
// unknown. Use this to normalize user-supplied paths before resolving
// or creating filesystem locations.
func Expand(p string) string {
	if homedir == "" {
		return p
	}
	if p == "~" {
		return homedir
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		return filepath.Join(homedir, p[2:])
	}
	return p
}
