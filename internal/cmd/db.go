package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
)

var (
	dbMergeTarget string
	dbMergeDryRun bool
	dbMergeNoBack bool
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Low-level Crush database maintenance",
}

var dbMergeCmd = &cobra.Command{
	Use:   "merge <source>...",
	Short: "Merge conversation history from other Crush databases into this one",
	Long: `Merge the contents of one or more source Crush databases into a target
database (default: the current project's .crush/crush.db).

This is designed for consolidating histories that were forked from a common
ancestor — e.g. per-worktree .crush databases created before workspace-aware
storage. Rows are deduplicated by primary key, so the same row appearing in
multiple databases is merged once, and sessions that diverged (same id, new
messages in each copy) have all their messages unioned back together.

Each <source> may be a .crush directory or a crush.db file. The target is
backed up first (unless --no-backup), and sources are never modified.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runDBMerge,
}

func init() {
	dbMergeCmd.Flags().StringVar(&dbMergeTarget, "target", "", "Target .crush dir or crush.db file (default: current project)")
	dbMergeCmd.Flags().BoolVar(&dbMergeDryRun, "dry-run", false, "Report what would be merged without writing")
	dbMergeCmd.Flags().BoolVar(&dbMergeNoBack, "no-backup", false, "Skip backing up the target database before merging")
	dbCmd.AddCommand(dbMergeCmd)
	rootCmd.AddCommand(dbCmd)
}

// mergeTables lists the tables merged, in FK-dependency order. Foreign
// keys are disabled during the copy (several tables have circular
// references — sessions<->worktrees, sessions<->snapshots), then
// re-validated with foreign_key_check afterward.
var mergeTables = []string{
	"sessions",
	"messages",
	"files",
	"snapshots",
	"worktrees",
	"milestones",
	"embeddings",
	// read_files is handled separately (composite PK, keep latest read_at).
}

func runDBMerge(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	targetDir, err := resolveTargetDataDir(cmd)
	if err != nil {
		return err
	}
	targetDB := filepath.Join(targetDir, "crush.db")

	// Resolve and validate sources up front.
	sources := make([]string, 0, len(args))
	for _, a := range args {
		p, err := resolveDBFile(a)
		if err != nil {
			return err
		}
		sources = append(sources, p)
	}

	// Open (and migrate) the target via the normal path.
	conn, err := db.Connect(ctx, targetDir, db.WithDataDirLock(true))
	if err != nil {
		return fmt.Errorf("failed to open target database: %w", err)
	}
	defer db.Release(targetDir)

	// Checkpoint and back up the target before mutating it.
	if !dbMergeDryRun && !dbMergeNoBack {
		if _, err := conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return fmt.Errorf("failed to checkpoint target: %w", err)
		}
		backup := fmt.Sprintf("%s.bak-%s", targetDB, time.Now().Format("20060102-150405"))
		if err := copyFile(targetDB, backup); err != nil {
			return fmt.Errorf("failed to back up target: %w", err)
		}
		cmd.Printf("Backed up target to %s\n", backup)
	}

	total := mergeTotals{}
	for _, src := range sources {
		cmd.Printf("Merging %s …\n", src)
		got, err := mergeOneSource(ctx, conn, src)
		if err != nil {
			return fmt.Errorf("merge of %s failed: %w", src, err)
		}
		total.add(got)
	}

	if dbMergeDryRun {
		cmd.Println("\nDry run — no changes written. Totals that WOULD be merged:")
	} else {
		cmd.Println("\nMerge complete. Rows added:")
	}
	total.print(cmd)
	return nil
}

type mergeTotals struct {
	perTable map[string]int64
}

func (m *mergeTotals) add(o map[string]int64) {
	if m.perTable == nil {
		m.perTable = map[string]int64{}
	}
	for k, v := range o {
		m.perTable[k] += v
	}
}

func (m *mergeTotals) print(cmd *cobra.Command) {
	tables := append(append([]string{}, mergeTables...), "read_files")
	for _, t := range tables {
		cmd.Printf("  %-12s %d\n", t, m.perTable[t])
	}
}

// mergeOneSource copies a migrated, read-only copy of src into the target
// connection and returns the per-table added-row counts.
func mergeOneSource(ctx context.Context, conn *sql.DB, srcDBPath string) (map[string]int64, error) {
	// Copy the source to a temp dir and migrate that copy to the current
	// schema, so the original is never modified and old worktree DBs
	// (potentially several migrations behind) line up column-for-column.
	tmpDir, err := os.MkdirTemp("", "crush-merge-src-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	tmpDB := filepath.Join(tmpDir, "crush.db")
	if err := copyFile(srcDBPath, tmpDB); err != nil {
		return nil, fmt.Errorf("failed to copy source: %w", err)
	}
	// Migrate the temp copy to the current schema, then release it so it
	// can be attached read-only below.
	if _, err := db.Connect(ctx, tmpDir); err != nil {
		return nil, fmt.Errorf("failed to migrate source copy: %w", err)
	}
	if err := db.Release(tmpDir); err != nil {
		return nil, fmt.Errorf("failed to release source copy: %w", err)
	}

	counts, err := attachAndMerge(ctx, conn, tmpDB)
	if err != nil {
		return nil, err
	}
	return counts, nil
}

func attachAndMerge(ctx context.Context, conn *sql.DB, srcDB string) (map[string]int64, error) {
	// Attach read-only so the source copy can never be mutated.
	attachURI := fmt.Sprintf("file:%s?mode=ro", srcDB)
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS src", attachURI); err != nil {
		return nil, fmt.Errorf("failed to attach source: %w", err)
	}
	defer conn.ExecContext(ctx, "DETACH DATABASE src")

	// Foreign keys must be toggled outside a transaction in SQLite.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Suppress schema triggers during the merge. Several triggers
	// (update_sessions_updated_at, update_session_message_count_on_insert)
	// fire on the inserts/updates below and would clobber the original
	// created_at/updated_at timestamps and double-count messages. We save
	// their DDL, drop them, do the copy with explicit columns (preserving
	// timestamps), recompute message_count ourselves, then recreate them.
	// On rollback (dry-run) the DROPs revert automatically.
	triggerSQL, err := saveTriggers(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to read triggers: %w", err)
	}
	if err := dropTriggers(ctx, tx, triggerSQL); err != nil {
		return nil, fmt.Errorf("failed to drop triggers: %w", err)
	}

	counts := map[string]int64{}
	for _, table := range mergeTables {
		n, err := mergeTable(ctx, tx, table)
		if err != nil {
			return nil, fmt.Errorf("table %s: %w", table, err)
		}
		counts[table] = n
	}

	// read_files: composite PK (path, session_id); keep the latest read.
	n, err := mergeReadFiles(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("table read_files: %w", err)
	}
	counts["read_files"] = n

	// Recompute denormalized session message counts after unioning
	// messages from a diverged copy. With triggers dropped this does not
	// touch updated_at.
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET message_count = (
			SELECT COUNT(*) FROM messages WHERE messages.session_id = sessions.id
		)`); err != nil {
		return nil, fmt.Errorf("failed to recompute message counts: %w", err)
	}

	// Restore the triggers we dropped.
	if err := recreateTriggers(ctx, tx, triggerSQL); err != nil {
		return nil, fmt.Errorf("failed to recreate triggers: %w", err)
	}

	if dbMergeDryRun {
		// Roll back so nothing is persisted, but we still report counts.
		return counts, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	// Validate referential integrity now that FKs are back on.
	if err := checkForeignKeys(ctx, conn); err != nil {
		return nil, err
	}
	return counts, nil
}

// trigger holds a saved trigger's name and creating SQL.
type trigger struct {
	name string
	sql  string
}

// saveTriggers returns all triggers in the main schema so they can be
// dropped during the merge and faithfully recreated afterward.
func saveTriggers(ctx context.Context, tx *sql.Tx) ([]trigger, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT name, sql FROM main.sqlite_master WHERE type='trigger' AND sql IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var trigs []trigger
	for rows.Next() {
		var t trigger
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, err
		}
		trigs = append(trigs, t)
	}
	return trigs, rows.Err()
}

func dropTriggers(ctx context.Context, tx *sql.Tx, trigs []trigger) error {
	for _, t := range trigs {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS main.%q", t.name)); err != nil {
			return err
		}
	}
	return nil
}

func recreateTriggers(ctx context.Context, tx *sql.Tx, trigs []trigger) error {
	for _, t := range trigs {
		if _, err := tx.ExecContext(ctx, t.sql); err != nil {
			return fmt.Errorf("recreate trigger %q: %w", t.name, err)
		}
	}
	return nil
}

// mergeTable copies rows from src.<table> into main.<table> using the
// columns common to both schemas, deduplicating by primary key.
func mergeTable(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	cols, err := commonColumns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	if len(cols) == 0 {
		return 0, nil
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = `"` + c + `"`
	}
	colList := strings.Join(quoted, ", ")
	q := fmt.Sprintf(
		"INSERT OR IGNORE INTO main.%q (%s) SELECT %s FROM src.%q",
		table, colList, colList, table,
	)
	res, err := tx.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func mergeReadFiles(ctx context.Context, tx *sql.Tx) (int64, error) {
	cols, err := commonColumns(ctx, tx, "read_files")
	if err != nil || len(cols) == 0 {
		return 0, err
	}
	// Keep the most recent read_at on conflict.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO main.read_files (session_id, path, read_at)
		SELECT session_id, path, read_at FROM src.read_files WHERE true
		ON CONFLICT(path, session_id) DO UPDATE SET
			read_at = MAX(read_files.read_at, excluded.read_at)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// commonColumns returns the column names present in both main.<table>
// and src.<table>, ordered by the main schema. This keeps the copy
// robust if the two databases ended up at slightly different schemas.
func commonColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	mainCols, err := tableColumns(ctx, tx, "main", table)
	if err != nil {
		return nil, err
	}
	srcCols, err := tableColumns(ctx, tx, "src", table)
	if err != nil {
		return nil, err
	}
	srcSet := make(map[string]struct{}, len(srcCols))
	for _, c := range srcCols {
		srcSet[c] = struct{}{}
	}
	var common []string
	for _, c := range mainCols {
		if _, ok := srcSet[c]; ok {
			common = append(common, c)
		}
	}
	return common, nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, schema, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA %s.table_info(%q)", schema, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid        int
			name, ctyp string
			notnull    int
			dflt       sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &ctyp, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func checkForeignKeys(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	var violations int
	var firstTable string
	for rows.Next() {
		var (
			tbl    string
			rowid  sql.NullInt64
			parent string
			fkid   int
		)
		if err := rows.Scan(&tbl, &rowid, &parent, &fkid); err != nil {
			return err
		}
		if violations == 0 {
			firstTable = tbl
		}
		violations++
	}
	if violations > 0 {
		return fmt.Errorf("%d foreign-key violation(s) after merge (e.g. table %q); target left unchanged is recommended — restore from the backup", violations, firstTable)
	}
	return rows.Err()
}

// resolveTargetDataDir returns the .crush data directory of the merge
// target: --target if given, otherwise the current project.
func resolveTargetDataDir(cmd *cobra.Command) (string, error) {
	if dbMergeTarget != "" {
		p, err := resolveDBFile(dbMergeTarget)
		if err != nil {
			return "", err
		}
		return filepath.Dir(p), nil
	}
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return "", err
	}
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init(cwd, dataDir, false)
	if err != nil {
		return "", fmt.Errorf("failed to initialize config: %w", err)
	}
	return cfg.Config().Options.DataDirectory, nil
}

// resolveDBFile accepts a .crush directory or a crush.db file and returns
// the path to the crush.db file, verifying it exists.
func resolveDBFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot access %q: %w", path, err)
	}
	dbPath := path
	if info.IsDir() {
		dbPath = filepath.Join(path, "crush.db")
		if _, err := os.Stat(dbPath); err != nil {
			return "", fmt.Errorf("no crush.db in %q", path)
		}
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return dbPath, nil
	}
	return abs, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
