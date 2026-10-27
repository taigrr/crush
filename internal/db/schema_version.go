package db

import (
	"io/fs"
	"strconv"
	"strings"
)

// LatestSchemaVersion returns the newest embedded migration version, i.e.
// the schema every database opened by this build is migrated to.
func LatestSchemaVersion() int64 {
	entries, err := fs.ReadDir(FS, "migrations")
	if err != nil {
		return 0
	}
	var latest int64
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(prefix, 10, 64); err == nil && v > latest {
			latest = v
		}
	}
	return latest
}
