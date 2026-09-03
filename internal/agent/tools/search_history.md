Search past conversation history. Useful for "did the user already tell me how to deploy this", "where did we discuss auth", or finding an exact error string or flag.

Matching is hybrid: an exact substring signal and (when an embedder is configured) a semantic vector signal are fused by Reciprocal Rank Fusion. Each hit is tagged exact / semantic / both. When no embedder is configured, or `semantic: false` is passed, it degrades to pure substring search.

Filters (all optional and independent):

- `session_id`: limit to one session. Pass `current` for the active session.
- `scope`: `user` (default, user/shell messages) or `all` (include assistant replies and reasoning).
- `semantic`: force the vector signal on/off for this query.
- `all_workspaces`: search across every known workspace (attached and detached), not just the current one. Cannot be combined with `session_id`. Each hit additionally reports its originating workspace root.
- `limit` / `offset`: paginate (default 20, max 50). The footer reports the next offset.

Archived sessions are included in results (their titles are resolved too).

Each result shows the full session id and message id (lining up with list_sessions), the match type, timestamp, role, and a snippet.
