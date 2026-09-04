package db

import (
	"context"
	"database/sql"
	"fmt"
)

// sessionColumnsRequired are columns sqlc queries assume exist. They
// are also added by goose migrations, but local/daily and the port
// reused goose version 20260904000000 for different ALTERs, so a
// migrated database can still be missing one side.
var sessionColumnsRequired = []struct {
	name, decl string
}{
	{"model_ref", "TEXT"},
	{"spawned_by_session_id", "TEXT"},
	{"spawned_by_workspace_id", "TEXT"},
}

// ensureSessionsColumns adds any of sessionColumnsRequired that are
// missing. Safe to call on a fully-migrated database; each ALTER is
// skipped when the column is already present.
func ensureSessionsColumns(ctx context.Context, conn *sql.DB) error {
	for _, col := range sessionColumnsRequired {
		var n int
		err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`,
			col.name,
		).Scan(&n)
		if err != nil {
			return fmt.Errorf("inspect sessions.%s: %w", col.name, err)
		}
		if n > 0 {
			continue
		}
		q := fmt.Sprintf("ALTER TABLE sessions ADD COLUMN %s %s", col.name, col.decl)
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("add sessions.%s: %w", col.name, err)
		}
	}
	return nil
}
