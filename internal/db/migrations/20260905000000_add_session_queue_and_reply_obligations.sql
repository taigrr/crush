-- +goose Up
-- +goose StatementBegin

-- Journal of each session's queued (accepted but not yet running)
-- prompts. The in-memory queue in the agent is the source of truth at
-- runtime; every mutation rewrites the session's rows here so a server
-- that drains and exits for an update leaves its queue on disk for the
-- next server to rehydrate. Callback-only state (OnComplete, the accept
-- reservation) cannot be persisted: a rehydrated prompt runs as a
-- normal turn with a fresh run id and no waiter.
CREATE TABLE IF NOT EXISTS session_queue (
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    run_id TEXT,
    prompt TEXT NOT NULL,
    attachments TEXT NOT NULL DEFAULT '[]',
    swarm_parts TEXT,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (session_id, seq)
);

-- Outstanding require_reply obligations: obligated_session_id owes a
-- swarm reply to owed_to_session_id. Mirrors swarm.ReplyTracker, which
-- is otherwise process-local.
CREATE TABLE IF NOT EXISTS swarm_reply_obligations (
    obligated_session_id TEXT NOT NULL,
    owed_to_session_id TEXT NOT NULL,
    owed_to_workspace_id TEXT NOT NULL DEFAULT '',
    owed_to_address TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    nudges INTEGER NOT NULL DEFAULT 0,
    -- 1 while the message that created the obligation is still queued
    -- (deferred during a drain, or a replayed tail) and has not been
    -- shown to the agent; such obligations are not enforced yet.
    undelivered INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (obligated_session_id, owed_to_session_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS swarm_reply_obligations;
DROP TABLE IF EXISTS session_queue;

-- +goose StatementEnd
