# Feature Differences from Upstream

This document describes the features added in this fork compared to
[charmbracelet/crush](https://github.com/charmbracelet/crush).

---

## Module Rename

All import paths changed:

| Upstream                         | Fork                        |
| -------------------------------- | --------------------------- |
| `github.com/charmbracelet/crush` | `github.com/taigrr/crush`   |
| `charm.land/fantasy`             | `github.com/taigrr/fantasy` |
| `charm.land/catwalk`             | `github.com/taigrr/catwalk` |

Charm dependencies that are **not** forked (bubbletea, lipgloss, glamour,
fang, bubbles) remain on their upstream `charm.land/` paths.

---

## Telemetry Removal

The entire `internal/event/` package has been deleted. This removes:

- PostHog analytics client and all event tracking
- Machine ID fingerprinting (`machineid`, MAC address hashing)
- All `event.Init()`, `event.AppInitialized()`, `event.PromptSent()`,
  `event.Error()`, etc. call sites throughout the codebase
- The `posthog-go` dependency

No usage data leaves the machine.

---

## Provider Simplification

`internal/config/provider.go` has been reduced from ~236 lines to ~33 lines.

**Removed:**

- Network-based provider fetching (Catwalk URL sync, ETag caching)
- `UpdateProviders()` function
- `HyperSync()` and the entire `internal/config/hyper.go` module
- `internal/config/catwalk.go` (Catwalk URL resolution logic)
- Local JSON caching of provider data
- Background provider refresh

**Kept:**

- `embedded.GetAll()` — providers are compiled in from the catwalk
  embedded catalog. To update providers, update the `catwalk` dependency.

---

## Extended Context (1M Token Support)

`internal/agent/agent.go` gains dynamic context window management for
models that support 1M context (e.g., Gemini, Claude with beta flags).

### Context Modes

Configurable per-model via `context_mode` in `crush.json`:

| Mode       | Behavior                                                                          |
| ---------- | --------------------------------------------------------------------------------- |
| `standard` | Use the model's default context window                                            |
| `extended` | Always use 1M context                                                             |
| `dynamic`  | Auto-switch to 1M when 80% of standard window is consumed; summarize at 90% of 1M |

### Implementation

- New constants: `extendedContextWindow` (1M), switch ratio (0.8),
  summarize ratio (0.9)
- `extendedContextMode` map tracks per-session extended state
- `useExtendedContext()` injects beta flags for providers that require
  them (e.g., `context-1m-2025-08-07` for Anthropic)
- `IsExtendedContext(sessionID)` exposed on the agent interface

### UI

- New **Context Mode** dialog (`internal/ui/dialog/context_mode.go`) for
  switching between Standard/Extended/Dynamic
- Status bar shows current context mode

---

## Checkpoints & Snapshots

New package: `internal/checkpoint/`

A private git repository (stored in `.crush/git/`, never touches the
user's `.git/`) that captures filesystem state at each user message.

### Capabilities

- **Create snapshots** — content-addressed tree/blob storage via go-git
- **Restore snapshots** — checkout any historical state
- **Diff snapshots** — unified diff between any two points
- **Named refs** — `refs/snapshots/{session_id}/{message_id}`
- **Session-scoped listing** — list all snapshots for a session
- **Configurable exclusions** — `node_modules`, `vendor`, `dist`, etc.
  via `snapshots.exclude` in config

### Components

| File                         | Purpose                                 |
| ---------------------------- | --------------------------------------- |
| `checkpoint/repo.go`         | go-git repository wrapper (622 lines)   |
| `checkpoint/repo_test.go`    | Full test coverage (560 lines)          |
| `checkpoint/service.go`      | High-level snapshot service (521 lines) |
| `checkpoint/service_test.go` | Service tests                           |

### Database

Migration `20260511112917_add_snapshots_table.sql` adds:

```sql
CREATE TABLE snapshots (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    parent_snapshot_id TEXT,
    git_commit_hash TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMP NOT NULL
);
```

---

## Worktrees

New package: `internal/worktree/`

Manages isolated working directories (stored in `.crush/worktrees/`) so
multiple conversation branches can have independent filesystem state.

### Capabilities

- **Create worktrees** from any snapshot
- **Switch** between worktrees (moves uncommitted changes)
- **Delete** worktrees
- **Auto-name generation** via small model (conventional commit style)
- **Post-create hooks** — run `npm install`, `go mod download`, etc.
  based on lockfile detection
- **Merge worktrees** — bring changes from one worktree back
- **Startup validation** — detect orphaned/stale worktrees

### Database

Migration `20260511114224_add_worktrees_table.sql` adds:

```sql
CREATE TABLE worktrees (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    current_snapshot_id TEXT,
    is_active BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL
);
```

### UI Dialogs

- `internal/ui/dialog/worktrees.go` — worktree list/switch/merge dialog
- `internal/ui/dialog/merge_worktree.go` — merge confirmation dialog

---

## Conversation Forking

New package: `internal/fork/`

Fork a conversation from any message, creating a new session that shares
history up to the fork point.

### Flow

1. User invokes fork on a message
2. Creates a snapshot at that message (if not exists)
3. Creates a new session with `forked_from_snapshot_id`
4. Optionally creates a new worktree from the snapshot
5. Switches UI to the new session

### UI

- `internal/ui/dialog/fork.go` — fork dialog (session name, worktree
  toggle)
- Fork action available on each user message in the chat view

---

## Archived Sessions

`internal/session/session.go` adds:

- `Archive(ctx, id)` — soft-delete by setting `archived_at`
- `Unarchive(ctx, id)` — restore archived session
- `ListArchived(ctx)` — list only archived sessions

Database migration `20260512141646_add_session_archived_at.sql` adds the
`archived_at` column.

The session list dialog (`internal/ui/dialog/sessions.go`) has been
significantly expanded (~300 lines added) with archive/unarchive actions.

---

## Server Snapshot & Worktree API

`internal/server/snapshots.go` (451 lines) adds HTTP endpoints:

| Method | Path                                       | Description           |
| ------ | ------------------------------------------ | --------------------- |
| POST   | `/workspaces/{id}/snapshots`               | Create snapshot       |
| GET    | `/workspaces/{id}/snapshots`               | List snapshots        |
| GET    | `/workspaces/{id}/snapshots/{sid}`         | Get snapshot          |
| POST   | `/workspaces/{id}/snapshots/{sid}/restore` | Restore snapshot      |
| GET    | `/workspaces/{id}/snapshots/{sid}/diff`    | Diff against snapshot |
| POST   | `/workspaces/{id}/worktrees`               | Create worktree       |
| GET    | `/workspaces/{id}/worktrees`               | List worktrees        |
| DELETE | `/workspaces/{id}/worktrees/{wid}`         | Delete worktree       |
| POST   | `/workspaces/{id}/worktrees/{wid}/switch`  | Switch worktree       |
| POST   | `/workspaces/{id}/sessions/{sid}/fork`     | Fork session          |

Backend support: `internal/backend/snapshot.go`, `backend/worktree.go`,
`backend/fork.go`.

Client SDK: `internal/client/snapshots.go` (341 lines).

---

## Shell Improvements

- `internal/shell/process_unix.go` / `process_other.go` — platform-
  specific process group handling
- `internal/shell/background.go` — expanded background job management

---

## `app_workspace.go` Removal

The standalone `internal/workspace/app_workspace.go` (406 lines) has been
deleted. Its functionality is consolidated into the existing
`client_workspace.go` which grew to accommodate the new workspace
interface methods (worktrees, snapshots, forking, context mode).

---

## Workspace Interface Additions

`internal/workspace/workspace.go` gains these methods:

```go
// Session management
ListArchivedSessions(ctx) ([]session.Session, error)
ArchiveSession(ctx, id) error
UnarchiveSession(ctx, id) error
SetActiveSessionID(sessionID string)
ActiveSessionID() string

// Extended context
AgentIsExtendedContext(sessionID string) bool

// Snapshots
ListSnapshots(ctx, sessionID) ([]*checkpoint.Snapshot, error)
CreateSnapshot(ctx, sessionID, messageID, description) (*checkpoint.Snapshot, error)
RestoreSnapshot(ctx, snapshotID) error
DiffSnapshot(ctx, snapshotID) (string, error)

// Worktrees
ListAllWorktrees(ctx) ([]*worktree.Worktree, error)
CreateWorktree(ctx, sessionID, name, fromSnapshotID) (*worktree.Worktree, error)
SwitchWorktree(ctx, sessionID, worktreeID) error
DeleteWorktree(ctx, worktreeID) error
MergeWorktree(ctx, worktreeID) error

// Forking
ForkSession(ctx, sessionID, messageID, title, createWorktree) (session.Session, error)
```

---

## UI Additions

### New Dialogs

| File                       | Purpose                                         |
| -------------------------- | ----------------------------------------------- |
| `dialog/context_mode.go`   | Select context mode (Standard/Extended/Dynamic) |
| `dialog/fork.go`           | Fork conversation from a message                |
| `dialog/snapshots.go`      | Browse and restore snapshots                    |
| `dialog/worktrees.go`      | List, switch, delete, merge worktrees           |
| `dialog/merge_worktree.go` | Merge confirmation with options                 |

### Expanded Dialogs

- `dialog/sessions.go` — archive/unarchive, fork actions (+297 lines)
- `dialog/permissions.go` — refactored permission UI (+127/-67 lines)

### Model & Keybindings

- `model/ui.go` — major expansion (+429 lines) wiring new features
- `model/keys.go` — new keybindings for worktree/snapshot/fork/context
- `model/header.go` — worktree/branch indicators in status bar
- `model/sidebar.go` — expanded sidebar with worktree info

### Styles

- `styles/grad.go` — new gradient utilities (50 lines)
- `styles/quickstyle.go` — additional quick style helpers

---

## Configuration Additions

New fields in `crush.json`:

```json
{
  "snapshots": {
    "enabled": true,
    "exclude": ["node_modules", "vendor", "dist", "build", ...]
  },
  "worktree": {
    "post_create": [
      {"if_exists": "bun.lockb", "run": "bun i"},
      {"if_exists": "go.sum", "run": "go mod download"}
    ]
  }
}
```

Per-model `context_mode` field: `"standard"`, `"extended"`, or
`"dynamic"`.

---

## Native Neovim Bridge

New package: `internal/editor/`

A direct Neovim integration that replaces the older neocrush daemon. Crush
talks to Neovim over an msgpack bridge so it can open files and jump to
1-indexed line/column locations from inside a session. A per-client editor
bridge means each connected client drives its own editor.

---

## Milestones

New package: `internal/milestone/`

Auto-generated progress markers that summarize what happened across a
session. A milestone is generated for every crossed 10-message boundary
(with backfill for existing sessions).

- Wired through the coordinator and app
- Server API endpoint plus workspace method
- Milestones dialog (Ctrl+Q) that scrolls to the relevant turn
- Database migration `20260604000000_add_milestones_table.sql`

---

## Procedures

New package: `internal/procedures/`

Reusable, user-authored workflow templates discovered from disk and
injected into the coder system prompt, so the agent can follow saved
step-by-step procedures.

---

## Embeddings & Hybrid Search

New packages: `internal/embedding/`, `internal/historysearch/`

- **Embeddings table** (`20260615000000_add_embeddings_table.sql`) with
  full embedding generation and storage
- **Hybrid search** combining vector similarity and keyword matching over
  past sessions
- `crush search` and `crush embeddings` CLI subcommands
- `search_history` tool with filters and a `list_sessions` tool
- Sidebar embeddings status and an embeddings dialog with backfill
  confirmation

---

## New Tools

Beyond `search_history` / `list_sessions` above:

- **`multi_view`** — batched multi-file reads in a single call; the coder
  prompt nudges the model to use it.
- **`context7`** — native tool for pulling up-to-date, version-accurate
  library docs; also surfaced in the coder prompt and wired into agentic
  fetch.
- **LSP tools** — `lsp_definition`, `lsp_references`, `lsp_rename`, and
  `lsp_document_symbols` (backed by powernap).
- **`reload_config`** — reload `crush.json` from disk without restarting
  (also available as the `crush reload` CLI).
- **Editor bridge tools** — `editor_context`, `show_locations`, and editor
  notifications let the agent read the user's open buffer and push
  locations into Neovim.
- **Denied-tool diff** — show a diff view for tool calls that were denied.

---

## Goal Mode

New: `internal/agent/goal.go`

An autonomous, turn-budgeted agent loop invoked with `/goal`. After each
turn a cheap evaluator checks whether the stated goal is met; while it is
unmet and within the turn budget (default 25, hard-capped), Crush injects a
fresh continuation directive and runs another turn. Mirrors Claude Code's
agent-evaluator turn cap so a runaway evaluator can't loop forever.

---

## Slash Commands

New: `internal/ui/model/slash.go`

Inline, `/`-prefixed commands typed at the chat prompt — distinct from the
command palette and user-defined custom commands:

| Command     | Action                                            |
| ----------- | ------------------------------------------------- |
| `/btw`      | Inject an out-of-band note into the conversation  |
| `/export`   | Export the session transcript to Markdown         |
| `/continue` | Continue the last session                         |
| `/goal`     | Start autonomous goal mode (see above)            |
| `/rename`   | Rename the current session                        |
| `/cwd`      | Show or change the session working directory      |

---

## Session Export

New: `internal/ui/model/export.go`

Render the active session's full transcript to a Markdown file (via
`/export [name]`), resolved relative to the session working directory.

---

## Paste Handling

New: `internal/ui/model/paste.go`

Smart paste: detects MIME type, saves pasted images as attachments, treats
pasted text over a threshold as a file attachment, and recognizes pasted
file paths (attaching them when they all exist) instead of dumping raw
text into the prompt.

---

## Bang Mode & Shell Messages

- **Bang mode** (`!`) executes shell commands directly from the prompt.
- New **Shell** message role with expandable UI rendering; the command and
  its output are stored as separate parts.
- Non-interactive env vars are set for these shells to prevent editor
  hangs.
- Sessions auto-create on a bang command as the first message.

---

## CLI Subcommands

New commands under `internal/cmd/`:

| Command            | Purpose                                                   |
| ------------------ | --------------------------------------------------------- |
| `crush search`     | Search conversation history (hybrid embedding + keyword)  |
| `crush embeddings` | Manage the global embedding model (`set`/`list`/`status`/`backfill`) |
| `crush db`         | Low-level database maintenance and migrations             |
| `crush reload`     | Reload config from disk in the running server             |
| `crush shutdown`   | Shut down the background Crush server                      |

---

## Bundled Ripgrep

The bash tool now guarantees `rg` (ripgrep) is available and instructs the
model to use it instead of `grep` for content searches.

---

## Adversarial Review Workflow

Bun-style parallel reviewer agents power a write → review → fix loop. Two
independent read-only reviewers run in parallel over a diff, harden the
fan-out, and coordinate with per-session permission dialogs.

---

## Sysadmin Mode

An ephemeral, in-session toggle that temporarily bypasses the sysadmin
command filter (renamed from "banned" commands) when the user explicitly
opts in.

---

## Themes

New: `internal/ui/styles/lua.go`, `themes.go`, `themes_community.go`

- **Named theme registry** with builtin theme lookup and `ResolveTheme`
- **User Lua themes** loaded from `GlobalThemesDir`
- **Theme picker dialog** with live preview, esc-cancel, enter-confirm
- Configured theme applied at startup (falls back to provider default)
- Cascading header/logo/gradient **brand-surface tokens** exposed to Lua
- Diff and syntax colors extracted into themeable tokens (lint test
  forbids raw hex in themed UI)
- Bundled community themes: **Tokyo Night, Catppuccin, Dracula, Nord,
  Gruvbox, Rosé Pine, Cyberpunk, VS Code Dark**
- New `options.tui.theme` config field

---

## Image Rendering

Inline rendering of image attachments using the **Kitty graphics
protocol** (kgp). Large images in the file picker are downscaled instead
of rejected, with per-image and aggregate image limits sourced from
catwalk per-model metadata.

---

## Low-Bandwidth / Reduced-Motion Mode

A TUI toggle that reduces motion and downshifts already-running spinners,
for slow links and SSH sessions.

---

## Session Navigator & Session UX

- **Left session navigator sidebar** with cross-workspace session listing
  and runtime workspace switching (sessions capped per workspace with an
  overflow picker row)
- **Read/unread state**, per-session working directory, and a workspace
  registry (migration
  `20260620000000_add_session_working_dir_and_read_state.sql`)
- Recent sessions on the landing screen
- Animated session **title reveal** with blinking cursor; auto re-title
  after 10 user messages
- Ctrl+F toggles fullscreen chat; image picker moved to Ctrl+I

---

## Client/Server & Sync

- Client/server mode **enabled by default**
- One shared **workspace per directory** across multiple clients
- **Row-level DB sync protocol** (`internal/sync/`, migration
  `20260612120000_add_sync_metadata.sql`); see `docs/sync-spec.md`
- Multi-client permission coordination: prompts auto-close when another
  client responds; idempotent permission resolution
- Config changes broadcast to all connected clients
- Data-directory lock refuses to open a dir already in use by another
  Crush instance
- Server stays alive while the agent is busy after all clients detach

---

## Notifications

- Configurable notification **backend** with a picker
- Terminal **bell** support
- Notifications for **SSH terminals**
- Ctrl+Y yolo-mode toggle with notification

---

## Provider Additions

- **Amazon Bedrock Europe** region support
- **Bedrock Mantle** OpenAI endpoint for GPT-5.5 (us-east-2 override)
- Improved detection of pre-existing AWS credentials

---

## Swarm (Cross-Session Coordination)

Lets one session send a
user-turn message to another session on the same backend, including
across workspaces.

Highlights:

- Every session gets a deterministic `color-animal` identity derived
  from its UUID via `colorhash` + a sorted animals list. Full
  disambiguated form is `color-animal-<4hex>`.
- New `swarm` tool exposed to main agents only. Params: `address`
  (color-animal / with-shorthash / raw UUID / `"new"`), `prompt`,
  optional `mode` (`queue` default, `btw` prefixes `[btw]`),
  `workspace_id` / `path` / `title` / `model` (for `new`).
- Cross-workspace address resolution via `Backend.LookupSwarmAddress`;
  per-workspace DB errors are collected but never mask a real match.
- Delivered as a `SwarmMessage` content part with structured sender
  metadata (color, animal, workspace) — the TUI renders a colored
  square + address in the sender header; the LLM sees the prefixed
  text `"message from color-animal: <body>"`.
- Sender identity is stamped by the backend from the sender's
  session row (not from tool input), preventing prompt-injected
  spoofing.
- Sub-sessions (task-tool children, title/summary generators) and
  archived sessions are non-addressable.
- New session creation (`address: "new"`) is transactional: if the
  initial send fails, the freshly-created session is archived so
  retries don't accumulate ghosts.
- Palette + animal list are configurable per user theme via a
  top-level `swarm = { palette, animals }` sub-table in the Lua
  theme file.
- Identities are backfilled at startup and assigned to new sessions
  via a pubsub subscriber; both writers are idempotent.

---

## Per-Session Models and Roles (orchestrator tier)

A session carries its own optional model selection
(`sessions.model_provider` / `model_id` / `model_reasoning_effort` /
`model_think`). Nil means "resolve to the workspace `large` model at
run time", which is the historical behavior for every existing row.

Highlights:

- `models.orchestrator` (optional) is the default stamp for
  human-opened sessions (TUI new session, `crush run` without
  `--session`, the in-process runner). Swarm workers and task/review
  children are never stamped by default, so the person talks to the
  orchestrator while everything it spawns runs `large`. No global
  "tier" and no `spawned` flag: the stamp is the discriminator and it
  is inspectable per session.
- Any other `models.<name>` key is a named role (loom-style tier
  registry). Roles are validated at config load and dropped with a
  warning when unresolvable; they are never defaulted.
- `model` parameter on `agent`, `review`, and `swarm address:"new"`:
  role name, `provider/model`, or bare id (ambiguous bare ids error
  with a "qualify it as <provider>/<id>" hint). Resolution happens
  before any session is created, so a bad reference leaves no orphan.
  `model` on an existing swarm address is rejected.
- Resolution is per turn in `coordinator.modelForSession`: one
  coordinator serves every session in a workspace, so the override
  rides `SessionAgentCall.Model` and survives the busy-session queue.
  Sub-agent overrides no longer rebuild the agent; built models are
  memoized per (provider, model, think, sub-agent) and cleared on
  every `UpdateModels`.
- `crush run -m` stamps the run's session (and re-stamps a continued
  one) instead of rewriting the workspace `large` slot; `--small-model`
  still writes config because titles/summaries have no per-session home.
- TUI: the model picker opens on an **Orchestrator** tab (changing it
  re-stamps the open session when that session was following the old
  orchestrator) and gains a trailing **This session** tab (stamps only
  the open session; picking the current `large` clears the stamp). A
  `/model [role] [model [effort]]` slash command does the same inline,
  with fuzzy model resolution (`config.ResolveModelRefLoose`) and
  argument completions for roles, models, and effort levels. The
  sidebar, reasoning-effort dialog, and thinking toggle all read the
  session's effective model (`common.EffectiveModel`), so the header
  never claims `large` while the transcript records something else.
- `crush_info` prints `[model]` with orchestrator/large/small/roles and
  a `[model_catalog]` of every `provider/model` the agent may delegate
  to. The coder prompt gets a `<delegation_models>` paragraph.
- REST: `POST /v1/workspaces/{id}/sessions` accepts `model`;
  `POST /v1/workspaces/{id}/sessions/{sid}/model` stamps or clears.
  `proto.Session.model` carries the stamp to clients.

---

## Spec Documents

Full design specs live under `docs/specs/`:

- `WORKTREES_AND_SNAPSHOTS.md` — snapshots, worktrees, forking
- `EMBEDDINGS_AND_VECTOR_SEARCH.md` — embeddings and hybrid search
- `ROADMAP.md` — improvement roadmap and phase findings
- `docs/sync-spec.md` — client-side row-level DB sync protocol

---

## Summary by the Numbers

| Metric            | Value                                                                                                       |
| ----------------- | ----------------------------------------------------------------------------------------------------------- |
| Files changed     | ~601                                                                                                        |
| Lines added       | ~49,800                                                                                                     |
| Lines removed     | ~10,600                                                                                                     |
| Net new code      | ~39,200                                                                                                     |
| New packages      | 8+ (`checkpoint`, `worktree`, `fork`, `milestone`, `procedures`, `embedding`, `editor`, `historysearch`, …) |
| Deleted packages  | 1 (`event`)                                                                                                 |
| New DB migrations | 7                                                                                                           |
| New UI dialogs    | 10+                                                                                                         |
| New API endpoints | ~10                                                                                                         |
