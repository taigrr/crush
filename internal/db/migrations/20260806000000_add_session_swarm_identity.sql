-- +goose Up
-- +goose StatementBegin

-- Add per-session swarm identity: a stable human-readable color name
-- (from the configured colorhash palette, e.g. "aliceblue") and animal
-- name (from the animals package, e.g. "tiger"). Together with a short
-- suffix of the session id these form the "color-animal[-shorthash]"
-- address the swarm tool uses to route messages between sessions.
ALTER TABLE sessions ADD COLUMN color TEXT;
ALTER TABLE sessions ADD COLUMN animal TEXT;

CREATE INDEX IF NOT EXISTS idx_sessions_color_animal
    ON sessions(color, animal);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and
-- goose would need to recreate the table; leave the columns in place
-- on down.
DROP INDEX IF EXISTS idx_sessions_color_animal;

-- +goose StatementEnd
