-- +goose Up
-- +goose StatementBegin

-- Swarm lineage. When a session is created by another session via the
-- swarm tool (address='new'), these record who spawned it and which
-- workspace the spawner lived in. This is deliberately separate from
-- parent_session_id: that column marks hidden sub-agent children (title,
-- summary, task tool) which are filtered out of session lists and are
-- not swarm-addressable. Spawned sessions are first-class peers that
-- stay visible and addressable; the lineage is informational so UIs can
-- nest or link them under their spawner. NULL means "opened by a human
-- or by a client, not by another session".
-- No-op. local/daily already added these columns as version
-- 20260904000000; the port reused that id for model_ref, so a real
-- ALTER here fails with "duplicate column name" on existing DBs.
-- connect.go ensureSessionsColumns adds the columns on databases
-- that still lack them (fresh installs).
SELECT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the columns in place on down.

-- +goose StatementEnd
