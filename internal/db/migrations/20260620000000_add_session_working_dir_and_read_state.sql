-- +goose Up
-- +goose StatementBegin

-- Track the working directory a session was started from so tools run in
-- the correct cwd when the session is resumed from a different client, and
-- track run-completion vs last-seen timestamps to compute per-session
-- read/unread state (a session is "unread" when it finished a run more
-- recently than the viewing client last opened it).
ALTER TABLE sessions ADD COLUMN working_dir TEXT;
ALTER TABLE sessions ADD COLUMN last_finished_at INTEGER;
ALTER TABLE sessions ADD COLUMN last_seen_at INTEGER;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- SQLite does not support DROP COLUMN cleanly on older versions and goose
-- would need to recreate the table; leave the columns in place on down.

-- +goose StatementEnd
