// Package sync implements the client side of Crush's row-level
// database sync protocol against a Cloudflare Workers + Durable Objects
// backend. See docs/sync-spec.md for the full design.
//
// The package is intentionally split so the CF backend (cf.go) can be
// swapped for an in-memory fake during tests, and so the on-disk
// concerns (identity, changelog, apply) stay independent of the wire
// format.
package sync

import "context"

// Op is a row-level mutation operation.
type Op string

const (
	OpInsert Op = "I"
	OpUpdate Op = "U"
	OpDelete Op = "D"
)

// Change is one row-level mutation in a changeset. For inserts and
// updates, Row holds the current row values keyed by column name. For
// deletes, Row is nil and only Table/PK/PK2 are meaningful.
type Change struct {
	Seq   int64          `json:"seq"`
	Op    Op             `json:"op"`
	Table string         `json:"table"`
	PK    string         `json:"pk"`
	PK2   string         `json:"pk2,omitempty"`
	Row   map[string]any `json:"row,omitempty"`
}

// PushRequest is what the client sends on POST /v1/projects/:fp/push.
type PushRequest struct {
	BaseSeq   int64    `json:"base_seq"`
	ClientSeq int64    `json:"client_seq"`
	Changes   []Change `json:"changes"`
}

// PushResponse is the delta+pull-in-one response.
type PushResponse struct {
	ServerSeq   int64        `json:"server_seq"`
	Applied     int          `json:"applied"`
	Resolutions []Resolution `json:"resolutions,omitempty"`
	Pull        []Change     `json:"pull,omitempty"`
}

// Resolution describes a server-side conflict outcome (e.g. a session
// archived as "diverged").
type Resolution struct {
	Kind   string `json:"kind"`
	Table  string `json:"table"`
	PK     string `json:"pk"`
	NewPK  string `json:"new_pk,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// LookupResponse is returned by /v1/projects/lookup.
type LookupResponse struct {
	Exists    bool   `json:"exists"`
	DBID      string `json:"db_id,omitempty"`
	ServerSeq int64  `json:"server_seq,omitempty"`
}

// ProvisionRequest creates a new project DO.
type ProvisionRequest struct {
	Fingerprint string `json:"fingerprint"`
	DBID        string `json:"db_id"`
	Hint        Hint   `json:"hint,omitempty"`
}

// Hint carries non-authoritative project metadata for the dashboard.
type Hint struct {
	Name   string `json:"name,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// StatusRequest is the bulk status check fired at startup.
type StatusRequest struct {
	Projects []ProjectCursor `json:"projects"`
}

// ProjectCursor is one entry in a status request.
type ProjectCursor struct {
	Fingerprint string `json:"fingerprint"`
	PushCursor  int64  `json:"push_cursor"`
	PullCursor  int64  `json:"pull_cursor"`
}

// StatusResponse tells the client which projects to pull and which the
// server knows about but the client doesn't.
type StatusResponse struct {
	Stale    []ProjectStale    `json:"stale,omitempty"`
	Orphaned []ProjectOrphaned `json:"orphaned,omitempty"`
}

// ProjectStale means the server has changes the client hasn't pulled.
type ProjectStale struct {
	Fingerprint string `json:"fingerprint"`
	ServerSeq   int64  `json:"server_seq"`
}

// ProjectOrphaned means the server has a project the client lost.
type ProjectOrphaned struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name,omitempty"`
}

// Backend is the transport-agnostic surface used by the rest of crush.
// The Cloudflare implementation lives in cf.go; tests use a fake.
type Backend interface {
	Lookup(ctx context.Context, fingerprint string) (*LookupResponse, error)
	Provision(ctx context.Context, req ProvisionRequest) (*LookupResponse, error)
	Push(ctx context.Context, fingerprint string, req PushRequest) (*PushResponse, error)
	Pull(ctx context.Context, fingerprint string, since int64) (*PushResponse, error)
	Bootstrap(ctx context.Context, fingerprint string) (<-chan Change, error)
	Status(ctx context.Context, req StatusRequest) (*StatusResponse, error)
}
