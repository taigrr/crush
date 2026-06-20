package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// PendingChanges returns the rolled-up changeset of all _changelog
// entries with change_seq > sinceSeq. Multiple I/U entries against the
// same (table, pk, pk2) are collapsed to a single change carrying the
// current row values; if the latest op is D the change is a delete.
//
// The returned MaxSeq is the highest change_seq observed and should be
// used as the new push cursor after the server accepts the push.
func PendingChanges(ctx context.Context, db *sql.DB, sinceSeq int64) (changes []Change, maxSeq int64, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT change_seq, op, table_name, pk, COALESCE(pk2,'')
		FROM _changelog
		WHERE change_seq > ?
		ORDER BY change_seq ASC`, sinceSeq)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	type key struct{ table, pk, pk2 string }
	type entry struct {
		op  Op
		seq int64
	}
	latest := map[key]entry{}
	order := []key{} // first-seen order, stable for tests

	for rows.Next() {
		var seq int64
		var op, table, pk, pk2 string
		if err := rows.Scan(&seq, &op, &table, &pk, &pk2); err != nil {
			return nil, 0, err
		}
		if seq > maxSeq {
			maxSeq = seq
		}
		k := key{table, pk, pk2}
		if _, seen := latest[k]; !seen {
			order = append(order, k)
		}
		// A delete is terminal: subsequent rows for the same key
		// (e.g. a re-insert with the same id) overwrite. Inserts
		// and updates collapse to "current state".
		latest[k] = entry{op: Op(op), seq: seq}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	for _, k := range order {
		e := latest[k]
		c := Change{Seq: e.seq, Op: e.op, Table: k.table, PK: k.pk, PK2: k.pk2}
		if e.op != OpDelete {
			row, ok, err := fetchRow(ctx, db, k.table, k.pk, k.pk2)
			if err != nil {
				return nil, 0, err
			}
			if !ok {
				// Row vanished between log and read -> downgrade
				// to delete so the server agrees with reality.
				c.Op = OpDelete
			} else {
				c.Row = row
				// Always send the current state as an insert/upsert;
				// the server applies INSERT OR REPLACE for syncable
				// tables, so we don't need to distinguish I vs U on
				// the wire.
				c.Op = OpInsert
			}
		}
		changes = append(changes, c)
	}
	return changes, maxSeq, nil
}

// TruncateChangelog removes _changelog rows up to and including
// upToSeq. Call after a successful push has advanced the push cursor.
func TruncateChangelog(ctx context.Context, db *sql.DB, upToSeq int64) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM _changelog WHERE change_seq <= ?`, upToSeq)
	return err
}

// pkColumns maps each syncable table to its PK column(s). Defined here
// (rather than discovered via PRAGMA) so the wire schema is explicit
// and unit-testable.
var pkColumns = map[string][]string{
	"sessions":   {"id"},
	"messages":   {"id"},
	"files":      {"id"},
	"read_files": {"path", "session_id"},
	"snapshots":  {"id"},
	"worktrees":  {"id"},
	"milestones": {"id"},
}

func fetchRow(ctx context.Context, db *sql.DB, table, pk, pk2 string) (map[string]any, bool, error) {
	cols, ok := pkColumns[table]
	if !ok {
		return nil, false, fmt.Errorf("sync: unknown syncable table %q", table)
	}
	var (
		where = make([]string, 0, len(cols))
		args  = make([]any, 0, len(cols))
	)
	for i, c := range cols {
		where = append(where, c+" = ?")
		switch i {
		case 0:
			args = append(args, pk)
		case 1:
			args = append(args, pk2)
		}
	}
	q := "SELECT * FROM " + quoteIdent(table) + " WHERE " + strings.Join(where, " AND ") + " LIMIT 1"

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	colNames, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	vals := make([]any, len(colNames))
	ptrs := make([]any, len(colNames))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, false, err
	}
	out := make(map[string]any, len(colNames))
	for i, n := range colNames {
		v := vals[i]
		// []byte -> string for JSON friendliness; SQLite text
		// columns frequently come back as []byte.
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		out[n] = v
	}
	return out, true, nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
