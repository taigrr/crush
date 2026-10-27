-- +goose Up
-- +goose StatementBegin

-- Cluster ownership. Exactly one node may write a session at a time; the
-- others hold read-only replicas. NULL means "owned by the node holding
-- this database", which is every session that never left its machine.
-- Replicated rows carry the owning node's ID.
ALTER TABLE sessions ADD COLUMN owner_node TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Leave the column in place on down; see add_session_model_ref.

-- +goose StatementEnd
