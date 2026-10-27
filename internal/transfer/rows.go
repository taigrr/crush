package transfer

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// Value is one typed SQLite value. JSON alone cannot distinguish blobs,
// integers, and reals, so each value carries its kind.
type Value struct {
	K string `json:"k"` // n=null i=int f=float s=text b=blob
	V any    `json:"v,omitempty"`
}

// Row is one table row keyed by column name.
type Row struct {
	Table string           `json:"table"`
	Cols  map[string]Value `json:"cols"`
}

// tableOrder is the insert order: parents before children. Every table
// except sessions is scoped by its session_id column.
var tableOrder = []string{"sessions", "messages", "files", "read_files", "milestones", "snapshots", "worktrees", "embeddings"}

// deferredSessionCols reference rows inserted after sessions; they are
// cleared on insert and restored once everything is in place.
var deferredSessionCols = []string{"worktree_id", "forked_from_snapshot_id"}

// sessionCounterCols are maintained by triggers on message insert, so
// they are restored from the source after import.
var sessionCounterCols = []string{"message_count", "prompt_tokens", "completion_tokens", "cost", "summary_message_id"}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func encodeValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{K: "n"}
	case int64:
		return Value{K: "i", V: x}
	case float64:
		return Value{K: "f", V: x}
	case []byte:
		return Value{K: "b", V: base64.StdEncoding.EncodeToString(x)}
	case string:
		return Value{K: "s", V: x}
	case bool:
		if x {
			return Value{K: "i", V: int64(1)}
		}
		return Value{K: "i", V: int64(0)}
	default:
		return Value{K: "s", V: fmt.Sprint(x)}
	}
}

func (v Value) decode() (any, error) {
	switch v.K {
	case "n":
		return nil, nil
	case "i":
		switch n := v.V.(type) {
		case float64:
			return int64(n), nil
		case json.Number:
			return n.Int64()
		case int64:
			return n, nil
		}
	case "f":
		switch n := v.V.(type) {
		case float64:
			return n, nil
		case json.Number:
			return n.Float64()
		}
	case "s":
		if s, ok := v.V.(string); ok {
			return s, nil
		}
	case "b":
		if s, ok := v.V.(string); ok {
			return base64.StdEncoding.DecodeString(s)
		}
	}
	return nil, fmt.Errorf("invalid %q value %v", v.K, v.V)
}

// SessionTree returns rootID and every descendant session ID.
func SessionTree(ctx context.Context, db *sql.DB, rootID string) ([]string, error) {
	ids := []string{rootID}
	for i := 0; i < len(ids); i++ {
		children, err := childSessions(ctx, db, ids[i])
		if err != nil {
			return nil, err
		}
		for _, id := range children {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func childSessions(ctx context.Context, db *sql.DB, parent string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM sessions WHERE parent_session_id = ?`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func tableExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string,
) bool {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return err == nil && n > 0
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anyArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// ExportRows streams every row belonging to ids, in insert order.
func ExportRows(ctx context.Context, db *sql.DB, ids []string, emit func(Row) error) error {
	for _, table := range tableOrder {
		if !tableExists(ctx, db, table) {
			continue
		}
		key := "session_id"
		if table == "sessions" {
			key = "id"
		}
		order := ""
		switch table {
		case "messages", "snapshots", "milestones":
			order = " ORDER BY created_at"
		}
		q := fmt.Sprintf(`SELECT * FROM %s WHERE %s IN (%s)%s`, quoteIdent(table), key, placeholders(len(ids)), order)
		if err := exportTable(ctx, db, table, q, anyArgs(ids), emit); err != nil {
			return err
		}
	}
	return nil
}

func exportTable(ctx context.Context, db *sql.DB, table, q string, args []any, emit func(Row) error) error {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("export %s: %w", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		r := Row{Table: table, Cols: make(map[string]Value, len(cols))}
		for i, c := range cols {
			r.Cols[c] = encodeValue(vals[i])
		}
		if err := emit(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// ImportRows replaces the sessions in ids with rows, atomically. Any
// existing rows for those sessions are deleted first.
func ImportRows(ctx context.Context, db *sql.DB, ids []string, rows []Row) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Delete children first, then sessions.
	for _, table := range slices.Backward(tableOrder) {
		if !tableExists(ctx, tx, table) {
			continue
		}
		key := "session_id"
		if table == "sessions" {
			key = "id"
		}
		q := fmt.Sprintf(`DELETE FROM %s WHERE %s IN (%s)`, quoteIdent(table), key, placeholders(len(ids)))
		if _, err := tx.ExecContext(ctx, q, anyArgs(ids)...); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	colsByTable := map[string]map[string]bool{}
	dropped := map[string]bool{}
	var sessions []Row
	for _, r := range rows {
		if !slices.Contains(tableOrder, r.Table) {
			return fmt.Errorf("%w: unexpected table %q", ErrFormat, r.Table)
		}
		cols, ok := colsByTable[r.Table]
		if !ok {
			if !tableExists(ctx, tx, r.Table) {
				return fmt.Errorf("%w: destination lacks table %q", ErrSchemaMismatch, r.Table)
			}
			if cols, err = tableColumns(ctx, tx, r.Table); err != nil {
				return err
			}
			colsByTable[r.Table] = cols
		}
		if r.Table == "sessions" {
			sessions = append(sessions, r)
		}
		names := make([]string, 0, len(r.Cols))
		for c := range r.Cols {
			if !cols[c] {
				// Both sides passed the schema version check, so a column only
				// the source has was added outside the migrations (for example
				// by another build that opened its database). Drop it.
				if key := r.Table + "." + c; !dropped[key] {
					dropped[key] = true
					slog.Warn("Dropping column the destination schema does not have", "table", r.Table, "column", c)
				}
				continue
			}
			names = append(names, c)
		}
		slices.Sort(names)
		args := make([]any, len(names))
		for i, c := range names {
			v, err := r.Cols[c].decode()
			if err != nil {
				return fmt.Errorf("%s.%s: %w", r.Table, c, err)
			}
			if r.Table == "sessions" && slices.Contains(deferredSessionCols, c) {
				v = nil
			}
			args[i] = v
		}
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = quoteIdent(n)
		}
		q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, quoteIdent(r.Table), strings.Join(quoted, ","), placeholders(len(names)))
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("insert %s: %w", r.Table, err)
		}
	}

	// Restore deferred references and trigger-maintained counters.
	for _, r := range sessions {
		var sets []string
		var args []any
		for _, c := range append(slices.Clone(deferredSessionCols), sessionCounterCols...) {
			v, ok := r.Cols[c]
			if !ok {
				continue
			}
			dv, err := v.decode()
			if err != nil {
				return err
			}
			sets = append(sets, quoteIdent(c)+" = ?")
			args = append(args, dv)
		}
		if len(sets) == 0 {
			continue
		}
		id, err := r.Cols["id"].decode()
		if err != nil {
			return err
		}
		args = append(args, id)
		q := fmt.Sprintf(`UPDATE sessions SET %s WHERE id = ?`, strings.Join(sets, ", "))
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("restore session %v: %w", id, err)
		}
	}
	return tx.Commit()
}

// SchemaVersion returns the database's latest applied migration.
func SchemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&v)
	return v.Int64, err
}

// EncodeValue wraps a database/sql scanned value with its kind.
func EncodeValue(v any) Value { return encodeValue(v) }

// DecodeValue unwraps v into a database/sql argument.
func DecodeValue(v Value) (any, error) { return v.decode() }
