-- name: InsertSessionQueueEntry :exec
INSERT INTO session_queue (session_id, seq, run_id, prompt, attachments, swarm_parts, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteSessionQueue :exec
DELETE FROM session_queue
WHERE session_id = ?;

-- name: DeleteAllSessionQueues :exec
DELETE FROM session_queue;

-- name: ListSessionQueue :many
SELECT * FROM session_queue
ORDER BY session_id ASC, seq ASC;

-- name: InsertSwarmReplyObligation :exec
INSERT INTO swarm_reply_obligations (obligated_session_id, owed_to_session_id, owed_to_workspace_id, owed_to_address, body, nudges, undelivered, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteSwarmReplyObligations :exec
DELETE FROM swarm_reply_obligations
WHERE obligated_session_id = ?;

-- name: ListSwarmReplyObligations :many
SELECT * FROM swarm_reply_obligations
ORDER BY obligated_session_id ASC, created_at ASC;

-- name: DeleteAllSwarmReplyObligations :exec
DELETE FROM swarm_reply_obligations;
