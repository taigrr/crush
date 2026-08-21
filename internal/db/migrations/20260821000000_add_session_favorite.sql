-- +goose Up
-- +goose StatementBegin

-- Add a per-session "favorite" flag. A favorited session is stickied to
-- the top of the sidebar's inbox view (just below sessions blocked on a
-- permission prompt) so an orchestrator session controlling swarm workers
-- is easy to return to. Stored as an INTEGER boolean (0/1) defaulting to 0
-- so existing rows are non-favorites.
ALTER TABLE sessions ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the column in place on down.

-- +goose StatementEnd
