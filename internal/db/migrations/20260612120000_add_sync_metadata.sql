-- +goose Up
-- +goose StatementBegin

-- Per-database sync metadata. Initialized lazily by the sync package on
-- first connect (db_id, project_fingerprint). Keys:
--   db_id                uuidv7, globally unique
--   project_fingerprint  SHA256(remote+":"+repo_relative_.crush_path)
--   change_seq           monotonic Lamport-style local mutation counter
--   push_cursor          highest local change_seq accepted by server
--   pull_cursor          highest server_seq applied locally
--   last_sync_at         epoch seconds of last successful sync
CREATE TABLE IF NOT EXISTS sync_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR IGNORE INTO sync_metadata(key, value) VALUES
    ('change_seq', '0'),
    ('push_cursor', '0'),
    ('pull_cursor', '0'),
    ('last_sync_at', '0');

-- _changelog records every mutation on syncable tables. Drained at
-- push time; rows with change_seq > push_cursor are the pending
-- changeset. We do NOT store a row snapshot here -- the pusher resolves
-- (table, pk, pk2) to current row values, so updates collapse to the
-- latest state and deletes carry just the key.
CREATE TABLE IF NOT EXISTS _changelog (
    change_seq  INTEGER PRIMARY KEY,
    op          TEXT    NOT NULL CHECK (op IN ('I','U','D')),
    table_name  TEXT    NOT NULL,
    pk          TEXT    NOT NULL,
    pk2         TEXT,
    changed_at  INTEGER NOT NULL  -- ms epoch
);

CREATE INDEX IF NOT EXISTS idx_changelog_table_pk
    ON _changelog (table_name, pk, pk2);

-- Helper macro implemented as a per-trigger pair of statements:
--   1) bump sync_metadata.change_seq
--   2) insert a _changelog row using the new value
-- We cannot define SQL functions, so each trigger inlines both steps.

-- +goose StatementEnd

-- sessions
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_sessions_i
AFTER INSERT ON sessions BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','sessions',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_sessions_u
AFTER UPDATE ON sessions BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'U','sessions',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_sessions_d
AFTER DELETE ON sessions BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'D','sessions',OLD.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- messages (append-only in practice, but track U/D defensively)
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_messages_i
AFTER INSERT ON messages BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','messages',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_messages_u
AFTER UPDATE ON messages BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'U','messages',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_messages_d
AFTER DELETE ON messages BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'D','messages',OLD.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- files
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_files_i
AFTER INSERT ON files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','files',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_files_u
AFTER UPDATE ON files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'U','files',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_files_d
AFTER DELETE ON files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'D','files',OLD.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- read_files (composite PK: path, session_id)
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_read_files_i
AFTER INSERT ON read_files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','read_files',NEW.path,NEW.session_id,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_read_files_u
AFTER UPDATE ON read_files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'U','read_files',NEW.path,NEW.session_id,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_read_files_d
AFTER DELETE ON read_files BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'D','read_files',OLD.path,OLD.session_id,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- snapshots (immutable -> only insert)
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_snapshots_i
AFTER INSERT ON snapshots BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','snapshots',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- worktrees
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_worktrees_i
AFTER INSERT ON worktrees BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','worktrees',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_worktrees_u
AFTER UPDATE ON worktrees BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'U','worktrees',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_worktrees_d
AFTER DELETE ON worktrees BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'D','worktrees',OLD.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- milestones (immutable in practice -> only insert)
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS _changelog_milestones_i
AFTER INSERT ON milestones BEGIN
    UPDATE sync_metadata SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='change_seq';
    INSERT INTO _changelog(change_seq, op, table_name, pk, pk2, changed_at)
        VALUES((SELECT CAST(value AS INTEGER) FROM sync_metadata WHERE key='change_seq'),
               'I','milestones',NEW.id,NULL,CAST(strftime('%s','now') AS INTEGER)*1000);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS _changelog_milestones_i;
DROP TRIGGER IF EXISTS _changelog_worktrees_d;
DROP TRIGGER IF EXISTS _changelog_worktrees_u;
DROP TRIGGER IF EXISTS _changelog_worktrees_i;
DROP TRIGGER IF EXISTS _changelog_snapshots_i;
DROP TRIGGER IF EXISTS _changelog_read_files_d;
DROP TRIGGER IF EXISTS _changelog_read_files_u;
DROP TRIGGER IF EXISTS _changelog_read_files_i;
DROP TRIGGER IF EXISTS _changelog_files_d;
DROP TRIGGER IF EXISTS _changelog_files_u;
DROP TRIGGER IF EXISTS _changelog_files_i;
DROP TRIGGER IF EXISTS _changelog_messages_d;
DROP TRIGGER IF EXISTS _changelog_messages_u;
DROP TRIGGER IF EXISTS _changelog_messages_i;
DROP TRIGGER IF EXISTS _changelog_sessions_d;
DROP TRIGGER IF EXISTS _changelog_sessions_u;
DROP TRIGGER IF EXISTS _changelog_sessions_i;
DROP INDEX IF EXISTS idx_changelog_table_pk;
DROP TABLE IF EXISTS _changelog;
DROP TABLE IF EXISTS sync_metadata;
-- +goose StatementEnd
