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
    %s
FROM sessions
WHERE parent_session_id IS NULL
  AND %s
ORDER BY updated_at DESC`,
		sel("working_dir", "''"),
		sel("last_finished_at", "0"),
		sel("last_seen_at", "0"),
		archivedProjection(cols),
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
		); err != nil {
			return nil, err
		}
		p.WorkingDir = workingDir.String
		p.LastFinishedAt = finished.Int64
		p.LastSeenAt = seen.Int64
		p.Archived = archived.Valid && archived.Int64 != 0
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
