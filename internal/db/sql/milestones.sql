-- name: CreateMilestone :one
INSERT INTO milestones (id, session_id, turn_number, short_summary, full_summary, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListMilestonesBySession :many
SELECT * FROM milestones
WHERE session_id = ?
ORDER BY turn_number ASC;

-- name: GetLatestMilestone :one
SELECT * FROM milestones
WHERE session_id = ?
ORDER BY turn_number DESC
LIMIT 1;

-- name: DeleteMilestonesBySession :exec
DELETE FROM milestones
WHERE session_id = ?;

-- name: CountMilestonesBySession :one
SELECT COUNT(*) FROM milestones
WHERE session_id = ?;
