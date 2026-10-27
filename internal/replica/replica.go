// Package replica keeps read-only copies of sessions owned by other
// cluster nodes. Each node serves the rows it owns from its _changelog;
// peers pull them and apply them under strict single-owner rules, so two
// machines never write the same session. See docs/cluster.md
// ("Replication").
package replica

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	crushsync "github.com/taigrr/crush/internal/sync"
	"github.com/taigrr/crush/internal/transfer"
)

// Change is one replicated row mutation.
type Change struct {
	Seq   int64                     `json:"seq"`
	Op    string                    `json:"op"` // "U" upsert, "D" delete
	Table string                    `json:"table"`
	PK    string                    `json:"pk"`
	PK2   string                    `json:"pk2,omitempty"`
	Row   map[string]transfer.Value `json:"row,omitempty"`
}

// Project is a workspace a node can serve changes for.
type Project struct {
	Root       string `json:"root"`
	DataDirRel string `json:"data_dir_rel"`
	MaxSeq     int64  `json:"max_seq"`
	// MaxEmbed is the newest embedding rowid.
	MaxEmbed int64 `json:"max_embed,omitempty"`
}

// Batch is a page of changes for one project.
type Batch struct {
	Root       string   `json:"root"`
	DataDirRel string   `json:"data_dir_rel"`
	MaxSeq     int64    `json:"max_seq"`
	Full       bool     `json:"full,omitempty"`
	Changes    []Change `json:"changes"`
	// Dirs is the project's directory skeleton (relative paths), sent
	// with full snapshots so peers can mirror the tree layout.
	Dirs []string `json:"dirs,omitempty"`
	// EmbedCursor is the newest embedding rowid included. Embeddings are
	// append-only and not in _changelog, so they replicate by rowid.
	EmbedCursor int64 `json:"embed_cursor,omitempty"`
	// More is set when the batch was truncated; pull again.
	More bool `json:"more,omitempty"`
}

// pkColumns maps replicated tables to their primary key columns.
var pkColumns = map[string][]string{
	"sessions":   {"id"},
	"messages":   {"id"},
	"files":      {"id"},
	"read_files": {"path", "session_id"},
	"milestones": {"id"},
	"snapshots":  {"id"},
	"worktrees":  {"id"},
	"embeddings": {"source_type", "source_id", "chunk_idx", "signature"},
}

// applyOrder applies parents before children.
var applyOrder = []string{"sessions", "messages", "files", "read_files", "milestones", "snapshots", "worktrees", "embeddings"}

// MaxSeq returns the newest change sequence in db.
func MaxSeq(ctx context.Context, db *sql.DB) int64 {
	var v sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT MAX(change_seq) FROM _changelog`).Scan(&v)
	return v.Int64
}

func minSeq(ctx context.Context, db *sql.DB) int64 {
	var v sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT MIN(change_seq) FROM _changelog`).Scan(&v)
	return v.Int64
}

func typed(row map[string]any) map[string]transfer.Value {
	out := make(map[string]transfer.Value, len(row))
	for k, v := range row {
		out[k] = transfer.EncodeValue(v)
	}
	return out
}

// ownedIDs returns the IDs of sessions this node owns.
func ownedIDs(ctx context.Context, db *sql.DB, self string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM sessions WHERE (owner_node IS NULL OR owner_node = '' OR owner_node = ?) AND moved_to_node IS NULL`, self)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// stampOwner marks a served session row with its owner.
func stampOwner(row map[string]transfer.Value, self string) {
	if v, ok := row["owner_node"]; !ok || v.K == "n" || v.V == "" {
		row["owner_node"] = transfer.Value{K: "s", V: self}
	}
}

// Serve returns the changes after since for sessions self owns, plus
// embeddings after embedSince. A negative since (never synced), a since
// that predates the retained changelog, or full returns a full snapshot
// of every owned session instead.
func Serve(ctx context.Context, db *sql.DB, self string, since, embedSince int64, full bool) (Batch, error) {
	owned, err := ownedIDs(ctx, db, self)
	if err != nil {
		return Batch{}, err
	}
	b := Batch{MaxSeq: MaxSeq(ctx, db)}
	if lo := minSeq(ctx, db); since >= 0 && lo > since+1 {
		full = true
	}
	if full || since < 0 {
		b.Full = true
		ids := make([]string, 0, len(owned))
		for id := range owned {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) == 0 {
			return b, nil
		}
		err := transfer.ExportRows(ctx, db, ids, func(r transfer.Row) error {
			if r.Table == "sessions" {
				stampOwner(r.Cols, self)
			}
			if r.Table == "embeddings" {
				return nil // Paged separately below.
			}
			b.Changes = append(b.Changes, Change{Op: "U", Table: r.Table, Row: r.Cols})
			return nil
		})
		if err != nil {
			return b, err
		}
		return b, serveEmbeddings(ctx, db, owned, 0, &b)
	}

	changes, maxSeq, err := crushsync.PendingChanges(ctx, db, since)
	if err != nil {
		return Batch{}, err
	}
	if maxSeq > b.MaxSeq {
		b.MaxSeq = maxSeq
	}
	for _, c := range changes {
		if _, ok := pkColumns[c.Table]; !ok {
			continue
		}
		rc := Change{Seq: c.Seq, Table: c.Table, PK: c.PK, PK2: c.PK2}
		if c.Op == crushsync.OpDelete {
			rc.Op = "D"
			b.Changes = append(b.Changes, rc)
			continue
		}
		sid := sessionOf(c.Table, c.Row)
		if !owned[sid] {
			continue
		}
		rc.Op, rc.Row = "U", typed(c.Row)
		if c.Table == "sessions" {
			stampOwner(rc.Row, self)
		}
		b.Changes = append(b.Changes, rc)
	}
	return b, serveEmbeddings(ctx, db, owned, embedSince, &b)
}

func sessionOf(table string, row map[string]any) string {
	key := "session_id"
	if table == "sessions" {
		key = "id"
	}
	s, _ := row[key].(string)
	return s
}

func valueString(v transfer.Value) string {
	s, _ := v.V.(string)
	return s
}

// Stats summarizes an applied batch.
type Stats struct {
	Applied, Skipped, Deleted int
}

// Apply writes batch, received from peer, into db. A row is applied only
// when the session it belongs to is owned by peer; rows for sessions
// this node owns (or that another node owns) are never touched.
func Apply(ctx context.Context, db *sql.DB, peer string, batch Batch) (Stats, error) {
	var st Stats
	if peer == "" {
		return st, errors.New("replica: empty peer")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer func() { _ = tx.Rollback() }()

	owner := func(sessionID string) (string, bool) {
		var o, moved sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT owner_node, moved_to_node FROM sessions WHERE id = ?`, sessionID).Scan(&o, &moved)
		if err != nil {
			return "", false
		}
		if !o.Valid || o.String == "" {
			return "", true // owned by this node
		}
		return o.String, true
	}

	byTable := map[string][]Change{}
	for _, c := range batch.Changes {
		byTable[c.Table] = append(byTable[c.Table], c)
	}
	var sessionRows []Change
	for _, table := range applyOrder {
		for _, c := range byTable[table] {
			switch c.Op {
			case "D":
				ok, err := deleteIfPeerOwned(ctx, tx, c, peer, owner)
				if err != nil {
					return st, err
				}
				if ok {
					st.Deleted++
				} else {
					st.Skipped++
				}
			case "U":
				if table == "sessions" {
					id := valueString(c.Row["id"])
					if valueString(c.Row["owner_node"]) != peer {
						st.Skipped++
						continue
					}
					if cur, exists := owner(id); exists && cur != peer {
						if cur == "" {
							// We own it; never let a peer overwrite it.
							slog.Warn("Replica refused session owned here", "session", id, "peer", peer)
							st.Skipped++
							continue
						}
						// The session moved between two other nodes; the peer
						// serving it as owner is the new owner.
						slog.Info("Replica learned new session owner", "session", id, "from", cur, "to", peer)
					}
					sessionRows = append(sessionRows, c)
				} else {
					sid := valueString(c.Row["session_id"])
					if cur, exists := owner(sid); !exists || cur != peer {
						st.Skipped++
						continue
					}
				}
				if err := upsert(ctx, tx, table, c.Row, table == "sessions"); err != nil {
					return st, fmt.Errorf("apply %s: %w", table, err)
				}
				st.Applied++
			}
		}
	}
	// Message inserts bump trigger-maintained counters; restore the
	// owner's values.
	for _, c := range sessionRows {
		if err := upsert(ctx, tx, "sessions", c.Row, false); err != nil {
			return st, err
		}
	}
	return st, tx.Commit()
}

func deleteIfPeerOwned(ctx context.Context, tx *sql.Tx, c Change, peer string, owner func(string) (string, bool)) (bool, error) {
	var sid string
	switch c.Table {
	case "sessions":
		sid = c.PK
	case "read_files":
		sid = c.PK2
	default:
		cols := pkColumns[c.Table]
		if len(cols) != 1 {
			return false, nil
		}
		q := fmt.Sprintf(`SELECT session_id FROM %s WHERE %s = ?`, quote(c.Table), quote(cols[0]))
		if err := tx.QueryRowContext(ctx, q, c.PK).Scan(&sid); err != nil {
			return false, nil
		}
	}
	if cur, exists := owner(sid); !exists || cur != peer {
		return false, nil
	}
	cols := pkColumns[c.Table]
	args := []any{c.PK}
	where := quote(cols[0]) + " = ?"
	if len(cols) == 2 {
		where += " AND " + quote(cols[1]) + " = ?"
		args = append(args, c.PK2)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, quote(c.Table), where), args...)
	if err == nil && c.Table == "messages" {
		_, err = tx.ExecContext(ctx, `DELETE FROM embeddings WHERE source_type = 'message' AND source_id = ?`, c.PK)
	}
	return err == nil, err
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// upsert inserts row or updates the existing row in place. It never uses
// REPLACE, which would cascade-delete a session's children. With
// deferRefs, session references to rows not yet applied are cleared.
func upsert(ctx context.Context, tx *sql.Tx, table string, row map[string]transfer.Value, deferRefs bool) error {
	pk := pkColumns[table]
	names := make([]string, 0, len(row))
	for c := range row {
		names = append(names, c)
	}
	slices.Sort(names)
	args := make([]any, len(names))
	quoted := make([]string, len(names))
	var sets []string
	for i, c := range names {
		v, err := transfer.DecodeValue(row[c])
		if err != nil {
			return err
		}
		if deferRefs && (c == "worktree_id" || c == "forked_from_snapshot_id") {
			v = nil
		}
		args[i] = v
		quoted[i] = quote(c)
		if !slices.Contains(pk, c) {
			sets = append(sets, quote(c)+" = excluded."+quote(c))
		}
	}
	pkq := make([]string, len(pk))
	for i, c := range pk {
		pkq[i] = quote(c)
	}
	q := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT(%s) DO UPDATE SET %s`,
		quote(table), strings.Join(quoted, ","), strings.TrimSuffix(strings.Repeat("?,", len(names)), ","),
		strings.Join(pkq, ","), strings.Join(sets, ", "))
	if len(sets) == 0 {
		q = fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`,
			quote(table), strings.Join(quoted, ","), strings.TrimSuffix(strings.Repeat("?,", len(names)), ","))
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

// Cursor reads the last applied sequence from peer, or -1 when this
// node has never received a snapshot from it.
func Cursor(ctx context.Context, db *sql.DB, peer string) int64 {
	var v sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT value FROM sync_metadata WHERE key = ?`, "repl:"+peer).Scan(&v); err != nil {
		return -1
	}
	var n int64
	_, _ = fmt.Sscan(v.String, &n)
	return n
}

// SetCursor records the last applied sequence from peer.
func SetCursor(ctx context.Context, db *sql.DB, peer string, seq int64) error {
	_, err := db.ExecContext(ctx, `INSERT INTO sync_metadata(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "repl:"+peer, fmt.Sprint(seq))
	return err
}

// embedPage bounds the embeddings sent per batch (vectors are large).
const embedPage = 2000

// MaxEmbed returns the newest embedding rowid in db.
func MaxEmbed(ctx context.Context, db *sql.DB) int64 {
	var v sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM embeddings`).Scan(&v)
	return v.Int64
}

// serveEmbeddings appends owned embeddings with rowid > since.
func serveEmbeddings(ctx context.Context, db *sql.DB, owned map[string]bool, since int64, b *Batch) error {
	b.EmbedCursor = since
	rows, err := db.QueryContext(ctx, `SELECT rowid, * FROM embeddings WHERE rowid > ? ORDER BY rowid LIMIT ?`, since, embedPage)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	n := 0
	for rows.Next() {
		n++
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := map[string]transfer.Value{}
		for i, c := range cols {
			if i == 0 {
				if id, ok := vals[0].(int64); ok {
					b.EmbedCursor = id
				}
				continue
			}
			row[c] = transfer.EncodeValue(vals[i])
		}
		if sid := valueString(row["session_id"]); owned[sid] {
			b.Changes = append(b.Changes, Change{Op: "U", Table: "embeddings", Row: row})
		}
	}
	b.More = n == embedPage
	return rows.Err()
}

// EmbedCursor reads the last applied embedding rowid from peer.
func EmbedCursor(ctx context.Context, db *sql.DB, peer string) int64 {
	return max(0, Cursor(ctx, db, "embed:"+peer))
}

// SetEmbedCursor records the last applied embedding rowid from peer.
func SetEmbedCursor(ctx context.Context, db *sql.DB, peer string, id int64) error {
	return SetCursor(ctx, db, "embed:"+peer, id)
}
