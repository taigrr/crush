package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OpenReadOnly opens a workspace's database at dataDir READ-ONLY (no
// migrations, no data-dir lock, no writes) for cross-workspace fan-out
// reads. It returns (nil, nil) when the database file does not exist, so
// callers can skip an empty/uninitialized workspace without treating it as
// an error. The caller owns the returned handle and must Close it.
func OpenReadOnly(dataDir string) (*sql.DB, error) {
	dbPath := filepath.Join(dataDir, "crush.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return openReadOnlyDB(dbPath)
}

// ErrDataDirBusy is returned by [OpenWritable] when the workspace database
// is already open in this process's shared connection pool (via [Connect]).
// Opening a second writable handle to a file this process is attaching (or
// mid-closing) is refused, so callers treat it as a per-target failure.
var ErrDataDirBusy = errors.New("data directory already in use")

// OpenWritable opens a workspace's database at dataDir READ-WRITE without
// running migrations, for cross-workspace fan-out writes (archive,
// mark-read) against a detached workspace whose schema already exists. It
// reuses the standard [openDB] path so the same pragmas apply — notably
// WAL journal mode and busy_timeout. It returns (nil, nil) when the
// database file does not exist, so callers can treat an uninitialized
// workspace as "not found". The caller owns the returned handle and must
// Close it. The returned handle is capped at a single connection.
//
// Concurrency model:
//
//   - It holds poolMu ACROSS the pool check and openDB, and refuses with
//     [ErrDataDirBusy] when the same data directory is already present in
//     this process's shared pool (a live [Connect] handle). Because both
//     [Connect] and [Release] hold poolMu across their open/close, the pool
//     CHECK cannot race an attach or teardown in the same process: the open
//     never interleaves with a mid-attach or mid-close pooled handle. In
//     particular it closes the TEARDOWN-ORDERING race where the backend
//     removes a workspace from its in-memory map before its DB handle is
//     shut down (Release holds poolMu across the map delete and db.Close, so
//     a check that runs while a workspace is mid-close reliably observes the
//     entry and backs off). This is instant-of-open exclusion, NOT
//     mutual exclusion over the write's lifetime: because a one-shot handle
//     is not registered in the pool, a [Connect] that starts after this
//     open completes can still open a second live handle concurrently. That
//     is safe for the reason below, not because of poolMu.
//   - It deliberately does NOT take the per-data-directory flock. Doing so
//     would be counterproductive: the flock is acquired non-blocking
//     (LOCK_EX|LOCK_NB), so a brief one-shot write holding it would make a
//     concurrent legitimate attach ([Connect] with locking) FAIL with a
//     misleading "in use by another process" error. Correctness does not
//     require it: two independent handles, each capped at one connection,
//     against a WAL database is SQLite's supported multi-connection mode
//     (unlike the multi-connection-per-pool interleave that caused the
//     historical SQLITE_NOTADB — see [Connect]); busy_timeout serializes
//     writers. This single-conn-WAL property — not poolMu — is what makes
//     both a post-open same-process Connect and the CROSS-PROCESS case (a
//     workspace running under another crush process) corruption-safe. The
//     remaining cross-process concern — archiving a session an agent is
//     running in another process — is bounded by the documented behavior
//     that a detached archive does not prune snapshot refs, so nothing is
//     pruned out from under a live agent; mark-read is non-destructive.
//
// Unlike [Connect] this never runs migrations and never registers in the
// shared connection pool: it is a one-shot handle for a single write, closed
// immediately after. A real read-write attach still goes through [Connect].
func OpenWritable(dataDir string) (*sql.DB, error) {
	dbPath := filepath.Join(dataDir, "crush.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		absPath = dbPath
	}

	// Hold poolMu across the pool check AND the open so this cannot
	// interleave with a concurrent Connect/Release for the same file.
	poolMu.Lock()
	defer poolMu.Unlock()
	if _, pooled := pool[absPath]; pooled {
		return nil, ErrDataDirBusy
	}
	conn, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	// Serialize access through a single connection, matching Connect: SQLite
	// serializes writes at the file level and interleaving pool connections
	// has caused WAL/header desync.
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// PeekedSession is a lightweight, read-only view of a session, sufficient
// for the cross-workspace picker. It deliberately excludes heavy or
// runtime-only fields (message history, live busy state).
type PeekedSession struct {
	ID             string
	Title          string
	WorkingDir     string
	CreatedAt      int64
	UpdatedAt      int64
	LastFinishedAt int64
	LastSeenAt     int64
	Archived       bool
	Color          string
	Animal         string
}

// Unread reports whether the session finished a run more recently than it
// was last opened.
func (p PeekedSession) Unread() bool {
	return p.LastFinishedAt > 0 && p.LastFinishedAt > p.LastSeenAt
}

// PeekSessions opens the workspace database at dataDir READ-ONLY and lists
// its top-level sessions without running migrations, taking the data
// directory lock, or writing anything.
//
// This is the safe path for the cross-workspace picker: it never upgrades
// a handle in place. When a workspace is actually opened, a fresh
// read-write [Connect] is used, which takes the lock and runs migrations.
// The read-only handle here is closed before this function returns, so
// there is no lingering reader to interfere with that later write open.
//
// It is defensive about schema drift: a workspace whose database predates
// the working_dir/last_finished_at/last_seen_at columns is read using
// whatever columns exist, with missing values defaulted to zero. A missing
// database file yields an empty slice and no error.
func PeekSessions(ctx context.Context, dataDir string) ([]PeekedSession, error) {
	dbPath := filepath.Join(dataDir, "crush.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []PeekedSession{}, nil
		}
		return nil, err
	}

	conn, err := openReadOnlyDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	cols, err := sessionColumns(ctx, conn)
	if err != nil {
		return nil, err
	}

	// Build the projection defensively so an older schema (missing the
	// newer columns) still reads. Absent columns are selected as literal
	// 0/'' so scanning is uniform.
	sel := func(name, zero string) string {
		if cols[name] {
			return name
		}
		return zero + " AS " + name
	}

	query := fmt.Sprintf(
		`
SELECT
    id,
    title,
    %s,
    created_at,
    updated_at,
    %s,
    %s,
    %s,
    %s,
    %s
FROM sessions
WHERE parent_session_id IS NULL
  AND %s
ORDER BY updated_at DESC`,
		sel("working_dir", "''"),
		sel("last_finished_at", "0"),
		sel("last_seen_at", "0"),
		archivedProjection(cols),
		sel("color", "''"),
		sel("animal", "''"),
		archivedFilter(cols),
	)

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PeekedSession
	for rows.Next() {
		var (
			p          PeekedSession
			workingDir sql.NullString
			finished   sql.NullInt64
			seen       sql.NullInt64
			archived   sql.NullInt64
			color      sql.NullString
			animal     sql.NullString
		)
		if err := rows.Scan(
			&p.ID,
			&p.Title,
			&workingDir,
			&p.CreatedAt,
			&p.UpdatedAt,
			&finished,
			&seen,
			&archived,
			&color,
			&animal,
		); err != nil {
			return nil, err
		}
		p.WorkingDir = workingDir.String
		p.LastFinishedAt = finished.Int64
		p.LastSeenAt = seen.Int64
		p.Archived = archived.Valid && archived.Int64 != 0
		p.Color = color.String
		p.Animal = animal.String
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// archivedProjection selects the archived flag (0/1) whether or not the
// archived_at column exists.
func archivedProjection(cols map[string]bool) string {
	if cols["archived_at"] {
		return "CASE WHEN archived_at IS NOT NULL THEN 1 ELSE 0 END AS archived"
	}
	return "0 AS archived"
}

// archivedFilter excludes archived sessions when the column exists.
func archivedFilter(cols map[string]bool) string {
	if cols["archived_at"] {
		return "archived_at IS NULL"
	}
	return "1=1"
}

// sessionColumns returns the set of column names present on the sessions
// table, used to build a schema-drift-tolerant projection.
func sessionColumns(ctx context.Context, conn *sql.DB) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info(sessions)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     sql.NullString
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, errors.New("sessions table not found")
	}
	return cols, nil
}
