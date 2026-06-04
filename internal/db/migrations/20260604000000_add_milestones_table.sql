-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS milestones (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn_number INTEGER NOT NULL,
    short_summary TEXT NOT NULL,    -- 5-8 word summary
    full_summary TEXT NOT NULL,     -- 2-3 sentence summary
    created_at INTEGER NOT NULL,    -- Unix timestamp in milliseconds
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_milestones_session_id ON milestones (session_id);
CREATE INDEX IF NOT EXISTS idx_milestones_session_turn ON milestones (session_id, turn_number);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_milestones_session_turn;
DROP INDEX IF EXISTS idx_milestones_session_id;
DROP TABLE IF EXISTS milestones;
-- +goose StatementEnd
