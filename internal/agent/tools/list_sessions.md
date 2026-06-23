List past conversation sessions, most recent first. Returns each session's full id, title, message count, and last-activity time, with the active session marked `*`.

Use this to find a session id to pass to `search_history`'s `session_id`. To search the current conversation you do not need this tool — pass `session_id: "current"` to `search_history` directly.

Results are paginated: `limit` caps sessions per page (default 50, max 100) and `offset` skips ahead (the footer tells you the next offset). Optional `include_archived` includes archived sessions.
