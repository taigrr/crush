-- name: CreateSession :one
INSERT INTO sessions (
    id,
    parent_session_id,
    title,
    message_count,
    prompt_tokens,
    completion_tokens,
    cost,
    summary_message_id,
    working_dir,
    updated_at,
    created_at
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    null,
    ?,
    strftime('%s', 'now'),
    strftime('%s', 'now')
) RETURNING *;

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = ? LIMIT 1;

-- name: GetLastSession :one
SELECT *
FROM sessions
ORDER BY updated_at DESC
LIMIT 1;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
  AND archived_at IS NULL
ORDER BY updated_at DESC;

-- name: ListArchivedSessions :many
SELECT *
FROM sessions
WHERE parent_session_id is NULL
  AND archived_at IS NOT NULL
ORDER BY archived_at DESC;

-- name: UpdateSession :one
UPDATE sessions
SET
    title = ?,
    prompt_tokens = ?,
    completion_tokens = ?,
    summary_message_id = ?,
    cost = ?,
    todos = ?
WHERE id = ?
RETURNING *;

-- name: UpdateSessionTitleAndUsage :exec
UPDATE sessions
SET
    title = ?,
    prompt_tokens = prompt_tokens + ?,
    completion_tokens = completion_tokens + ?,
    cost = cost + ?,
    updated_at = strftime('%s', 'now')
WHERE id = ?;


-- name: RenameSession :exec
UPDATE sessions
SET
    title = ?
WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE id = ?;

-- name: ArchiveSession :exec
UPDATE sessions
SET archived_at = strftime('%s', 'now')
WHERE id = ?;

-- name: UnarchiveSession :exec
UPDATE sessions
SET archived_at = NULL,
    updated_at = strftime('%s', 'now')
WHERE id = ?;

-- name: MarkSessionFinished :exec
UPDATE sessions
SET last_finished_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)
WHERE id = ?;

-- name: MarkSessionSeen :exec
UPDATE sessions
SET last_seen_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)
WHERE id = ?;

-- name: SetSessionWorkingDir :exec
UPDATE sessions
SET working_dir = ?
WHERE id = ?;

-- name: SetSessionFavorite :exec
UPDATE sessions
SET favorite = ?
WHERE id = ?;

-- name: SetSessionModel :exec
-- Stamps (or clears, when all three are NULL) the session's own model
-- selection. NULL columns mean "resolve to the workspace large model".
UPDATE sessions
SET model_provider = ?,
    model_id = ?,
    model_reasoning_effort = ?,
    model_think = ?
WHERE id = ?;

-- name: SetSessionSwarmIdentity :execrows
-- Only assign the identity if the row does not already have BOTH
-- fields set, so concurrent writers (startup backfill + Created-event
-- subscriber) can't clobber a persisted identity with a
-- differently-derived one when the palette/animal config changes
-- between callers. The conjunction (rather than disjunction) protects
-- half-populated rows from having the set field overwritten.
UPDATE sessions
SET color = ?,
    animal = ?
WHERE id = ?
  AND (color IS NULL OR color = '')
  AND (animal IS NULL OR animal = '');

-- name: ListSessionsMissingSwarmIdentity :many
SELECT id
FROM sessions
WHERE color IS NULL OR animal IS NULL OR color = '' OR animal = '';

-- name: FindSessionsByColorAnimal :many
SELECT *
FROM sessions
WHERE color = ? AND animal = ? AND archived_at IS NULL;
