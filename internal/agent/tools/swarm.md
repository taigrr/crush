The swarm tool sends a message from the current session to another
session identified by a color-animal address (or raw session id). The
target may live in any running workspace on this Crush backend.

Address formats accepted:

- `color-animal` (e.g. `aliceblue-tiger`) — canonical human-readable
  form. Fails with an ambiguous error if two sessions happen to share
  the same pair; retry with the 4-character shorthash suffix.
- `color-animal-shorthash` (e.g. `aliceblue-tiger-1a2b`) — disambiguated
  form. The shorthash is the last 4 characters of the target session's
  UUID (lowercased).
- A raw session UUID — always unambiguous.

Modes:

- `queue` (default): the message is dispatched as a new user turn on
  the target session. If the target is idle it starts running
  immediately; if it is already running, the message is enqueued and
  drains after the current turn finishes.
- `btw`: identical dispatch, but the delivered text is additionally
  prefixed with `[btw]` — the same marker the `/btw` slash command
  uses. If the target session is currently running, the target's
  agent treats the marker as an aside to fold into the ongoing turn
  rather than a fresh instruction. If the target is idle the marker
  is inert; the behavior collapses to `queue`.

Special addresses:

- `new` — create a new session and send `prompt` as its initial user
  message. Choose the target workspace one of two ways:
  - `workspace_id` — an existing, currently-running workspace. Defaults
    to the sender's own workspace when omitted.
  - `path` — a directory path. The workspace rooted there is brought up
    if it is not currently running (created on disk if new, or attached
    if previously detached), then the session is created in it. `path`
    takes precedence over `workspace_id` when both are set.
  The new session is picked up by any attached client's sidebar; the
  agent runs immediately.

The receiving session sees the message as a user turn prefixed with
`message from <color-animal>:`. The sender's color/animal and workspace
are also stored as structured metadata on the delivered message so
the target's UI can render a colored header.

Restrictions:

- Sub-agent sessions (task tool children, title/summary sessions) are
  not addressable.
- Sessions cannot address themselves.
