-- +goose Up
-- +goose StatementBegin

-- Vectors for hybrid/semantic history search. Local to each project's
-- crush.db. Vectors are only comparable within the same embedding space,
-- captured by `signature` (see
-- docs/specs/EMBEDDINGS_AND_VECTOR_SEARCH.md §2). Switching embedders
-- yields a new signature; stale rows are dropped, never converted.
--
-- Phase A: this table is NOT registered for sync (no _changelog
-- triggers) -- vectors are a per-machine local cache.
CREATE TABLE IF NOT EXISTS embeddings (
    source_type TEXT    NOT NULL,            -- 'message' | 'file_chunk'
    source_id   TEXT    NOT NULL,            -- e.g. messages.id
    chunk_idx   INTEGER NOT NULL DEFAULT 0,  -- for chunked sources
    signature   TEXT    NOT NULL,            -- embedding-space identity
    dim         INTEGER NOT NULL,
    vec         BLOB    NOT NULL,            -- float32 little-endian, dim*4 bytes
    session_id  TEXT,                        -- denormalized for scoped search/cleanup
    created_at  INTEGER NOT NULL,            -- ms epoch
    PRIMARY KEY (source_type, source_id, chunk_idx, signature)
);

CREATE INDEX IF NOT EXISTS idx_embeddings_signature ON embeddings (signature);
CREATE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings (source_type, source_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_session ON embeddings (session_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS embeddings;
-- +goose StatementEnd
