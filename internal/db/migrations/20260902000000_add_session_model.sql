-- +goose Up
-- +goose StatementBegin

-- Per-session model selection. A session created by a human is stamped
-- with the configured orchestrator model (when one is set); sessions
-- spawned by tools (swarm workers, task/review children) are stamped
-- only when the caller passed an explicit model. NULL means "resolve
-- to the workspace's large model at run time", which is the historical
-- behavior, so existing rows are unaffected.
ALTER TABLE sessions ADD COLUMN model_provider TEXT;
ALTER TABLE sessions ADD COLUMN model_id TEXT;
ALTER TABLE sessions ADD COLUMN model_reasoning_effort TEXT;
ALTER TABLE sessions ADD COLUMN model_think INTEGER;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the columns in place on down.

-- +goose StatementEnd
