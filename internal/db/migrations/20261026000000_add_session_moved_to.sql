-- +goose Up
-- +goose StatementBegin

-- Cluster ownership hand-off. When a session moves to another cluster
-- node, the source keeps its copy (archived) and records the node it moved
-- to so the UI can explain where it went. NULL means the session is owned
-- here (or was never moved).
ALTER TABLE sessions ADD COLUMN moved_to_node TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Leave the column in place on down; see add_session_model_ref.

-- +goose StatementEnd
