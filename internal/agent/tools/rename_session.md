Rename any Crush session, in any workspace, to a new title.

Use this when the user asks to rename a session (or "this session", "the
current chat", another session by its color-animal name, etc.). The
`address` is resolved across every running workspace, so you can rename
sessions that do not belong to the current workspace.

Address formats accepted:

- `color-animal` (e.g. `aliceblue-tiger`) — the human-readable form. If
  two sessions share the same pair the call fails as ambiguous; retry
  with the shorthash suffix.
- `color-animal-shorthash` (e.g. `aliceblue-tiger-1a2b`) — disambiguated
  form (last 4 characters of the session UUID).
- A raw session UUID — always unambiguous.

`title` is the new title to set. Sub-agent sessions (task tool children,
title/summary generators) are not renamable.
