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
  Optionally pass `model` to choose what the new session runs on: a
  configured role name (`large`, `small`, `worker`, or a user-defined
  role), `provider/model`, or a bare model id, resolved in the TARGET
  workspace's config on every turn. Omitted, the new session runs its
  workspace's large model (the default). `model` is rejected for
  existing addresses.
  Optionally pass `working_dir` (absolute) to pin the directory the new
  session's tools run in; it must be inside the target workspace's
  project (a subdirectory or a linked git worktree of it). When `path`
  is given, `working_dir` defaults to `path`, so a worker spawned into
  a sibling worktree of an already-running workspace runs there rather
  than in the tree that attached first.
  The new session records the sender as its spawner
  (`spawned_by_session_id`), which UIs use to nest it under this
  session; it stays a normal, addressable top-level session. The new
  session is picked up by any attached client's sidebar; the agent
  runs immediately.

Guaranteed replies:

- `require_reply: true` (any address, including `new`) makes the target
  owe this session a reply. The delivered text gains a
  `[reply required: ...]` trailer naming your address, so the target
  knows up front. When the target tries to end its turn without having
  sent you a swarm message, it is given a continuation turn reminding
  it to reply (up to two nudges). If it still does not, its final
  assistant message is forwarded to you automatically, prefixed
  `[auto-forwarded: ...]`; if its turn fails or is canceled, you are
  told that instead. Either way you will hear back. Use this when
  spawning workers whose outcome you need to act on.
- Any swarm message from the target to you satisfies the obligation
  (mode does not matter). The result metadata reports
  `fulfilled_reply: true` when a send you make settles a reply you
  owed.

The receiving session sees the message as a user turn prefixed with
`message from <color-animal>:`. The sender's color/animal and workspace
are also stored as structured metadata on the delivered message so
the target's UI can render a colored header.

Every successful call also returns structured metadata
(`workspace_id`, `session_id`, `color`, `animal`, `address`,
`working_dir`, `delivery`, `btw`, `created`, `reply_required`,
`fulfilled_reply`) alongside the prose result, so callers never need
to parse the text to find the target.

Restrictions:

- `model` and `working_dir` only apply with `address = "new"`. Messages
  to an existing session never change that session's model or working
  directory.

- Sub-agent sessions (task tool children, title/summary sessions) are
  not addressable.
- Sessions cannot address themselves.
