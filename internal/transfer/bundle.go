// Package transfer moves a session — its full database history and the
// on-disk state of its project — between Crush nodes as a single
// streamed bundle. See docs/cluster.md ("Moving jobs").
//
// A bundle is a gzip-compressed tar stream laid out as:
//
//	manifest.json        always first; lets the receiver refuse early
//	db/rows.jsonl        one typed row per line, parents before children
//	tree/<relpath>       the project's working tree (when shipped)
//
// Paths in the tree are relative to the project root, which keeps the same
// absolute path on every node.
package transfer

import (
	"errors"
	"time"
)

// FormatVersion versions the bundle layout.
const FormatVersion = 1

const (
	manifestName = "manifest.json"
	rowsName     = "db/rows.jsonl"
	treePrefix   = "tree/"
)

// Common errors.
var (
	ErrFormat          = errors.New("unsupported transfer bundle")
	ErrSchemaMismatch  = errors.New("database schema differs between nodes; upgrade both to the same release")
	ErrSessionOwned    = errors.New("session already exists and is active on the destination")
	ErrDestinationBusy = errors.New("destination workspace is open; close it there or choose another conflict policy")
	ErrReuse           = errors.New("destination file could not be reused")
)

// Manifest describes a bundle.
type Manifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Version   string    `json:"version"`

	SourceNodeID   string `json:"source_node_id,omitempty"`
	SourceNodeName string `json:"source_node_name,omitempty"`

	// SessionID is the moved top-level session; SessionIDs also holds
	// its descendants (agent tool sessions, title/summary sessions).
	SessionID  string   `json:"session_id"`
	SessionIDs []string `json:"session_ids"`
	Title      string   `json:"title,omitempty"`

	// Root is the project root on the source node; DataDirRel is the
	// Crush data directory relative to it (usually ".crush").
	Root       string `json:"root"`
	DataDirRel string `json:"data_dir_rel"`

	SchemaVersion int64 `json:"schema_version"`

	// RootMode is the permission bits of the project root directory.
	RootMode uint32 `json:"root_mode,omitempty"`
	// Tree reports whether the working tree is included.
	Tree bool `json:"tree"`
	// Git describes the source repository, when the root is one.
	Git *GitState `json:"git,omitempty"`
	// WasBusy is true when the session had an in-flight turn that was
	// paused for the move; the receiver may resume it.
	WasBusy bool `json:"was_busy,omitempty"`
}

// GitState is a snapshot of a repository's position.
type GitState struct {
	Head   string `json:"head"`
	Branch string `json:"branch,omitempty"`
	Clean  bool   `json:"clean"`
}
