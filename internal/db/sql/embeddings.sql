-- name: UpsertEmbedding :exec
INSERT INTO embeddings (
    source_type, source_id, chunk_idx, signature, dim, vec, session_id, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT (source_type, source_id, chunk_idx, signature)
DO UPDATE SET dim = excluded.dim, vec = excluded.vec, session_id = excluded.session_id, created_at = excluded.created_at;

-- name: ListEmbeddingsBySignature :many
SELECT source_type, source_id, chunk_idx, signature, dim, vec, session_id, created_at
FROM embeddings
WHERE signature = ?
ORDER BY created_at DESC;

-- name: ListEmbeddingsBySignatureAndSession :many
SELECT source_type, source_id, chunk_idx, signature, dim, vec, session_id, created_at
FROM embeddings
WHERE signature = ? AND session_id = ?
ORDER BY created_at DESC;

-- name: CountEmbeddingsBySignature :one
SELECT COUNT(*) FROM embeddings WHERE signature = ?;

-- name: CountEmbeddingsTotal :one
SELECT COUNT(*) FROM embeddings;

-- name: HasEmbedding :one
SELECT EXISTS(
    SELECT 1 FROM embeddings
    WHERE source_type = ? AND source_id = ? AND chunk_idx = ? AND signature = ?
);

-- name: ListSourceIDsForSignature :many
SELECT DISTINCT source_id
FROM embeddings
WHERE source_type = ? AND signature = ?;

-- name: DeleteEmbeddingsExceptSignature :exec
DELETE FROM embeddings WHERE signature != ?;

-- name: DeleteAllEmbeddings :exec
DELETE FROM embeddings;

-- name: DeleteEmbeddingsBySource :exec
DELETE FROM embeddings WHERE source_type = ? AND source_id = ?;

-- name: DeleteEmbeddingsBySession :exec
DELETE FROM embeddings WHERE session_id = ?;
