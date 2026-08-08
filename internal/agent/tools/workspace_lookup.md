Resolve a directory path to the ID of the running Crush workspace rooted at that path.

Workspaces are keyed by project root: any directory inside a project — the main git working tree, a git worktree, or a subdirectory — resolves to the same workspace ID. Use this to discover the `workspace_id` needed by the `swarm` tool (for example, `address='new'` with `workspace_id`) when you only know a filesystem path.

Parameters:

- `path`: the directory path to resolve.

Returns the `workspace_id` on success. If no running workspace is rooted at that path, returns a clear "no running workspace at that path" message rather than an error — the workspace may not be attached, or the path may be outside any known project.
