-- +goose Up
-- +goose StatementBegin

-- No-op marker. local/daily recorded 20260904000000 as spawned_by, so
-- goose never runs 20260904000000_add_session_model_ref.sql on those
-- DBs. connect.go ensureSessionsColumns adds model_ref if missing.
SELECT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the column in place on down.

-- +goose StatementEnd
