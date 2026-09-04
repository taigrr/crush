-- +goose Up
-- +goose StatementBegin

-- Optional per-session model reference. A swarm session spawned with a
-- `model` argument records the reference it was given (a configured role
-- name such as "scout", "provider/model", or a bare model id); the
-- coordinator resolves it against the workspace config on every turn, so
-- a role that is re-pointed in config takes effect without touching the
-- row and any tuning on the role follows along. NULL means "run the
-- workspace's large model", which is the historical behavior, so existing
-- rows are unaffected.
ALTER TABLE sessions ADD COLUMN model_ref TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the column in place on down.

-- +goose StatementEnd
