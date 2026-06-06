# Crush DB Sync — Design Spec (B2: Cloudflare Durable Objects)

Status: design committed. Backend = Cloudflare Workers + Durable Objects + R2.

## 1. Goals

- Conversations survive `.crush/` deletion (branch switches, `git clean`,
  worktrees) and live forever in the cloud.
- Client and server have independent lifetimes (client/server split is
  already done).
- Multi-tenant with SSH-pubkey auth.
- Offline-first: sync never blocks the user; we fail open.
- Server is the source of truth and control plane — it powers a dashboard
  and, later, cloud-agent orchestration.
- Conflict resolution happens server-side.
- Eager pull at startup so projects are ready before the user opens them.

## 2. The decision that drives everything

A Durable Object's SQLite is reachable **only** through `sql.exec()`. There is
**no binary export** of the underlying `.db` file. Therefore we cannot ship
whole compressed database files to/from the server.

**Consequence: sync is a row-level changeset protocol, not file transfer.**

This is a feature, not a workaround:

- The schema is UUID-keyed everywhere (sessions, messages, files, snapshots,
  worktrees, milestones) — inserts never collide across machines.
- Messages are append-only — perfect for changeset replay.
- The DO sees *logical rows*, so it can do real **session-level** conflict
  resolution (the thing that whole-file LWW corrupts).

## 3. Topology

```
crush client (.crush/crush.db, real SQLite)
      │  HTTPS, SSH-signed bearer token
      ▼
Cloudflare Worker (edge: auth, routing, R2 streaming)
      │  idFromName(project_fingerprint)
      ▼
Durable Object  "project:<fingerprint>"   ← single-writer authority per project
      │  ctx.storage.sql  (canonical relational mirror of one crush.db)
      │  ctx.storage PITR bookmarks (30-day history, free)
      ▼
R2  (overflow blobs: oversized message parts > ~1.5 MB; optional full exports)

D1 (global index)  ← DOs upsert per-project summaries for dashboard fan-out
```

- **One Durable Object per project.** Addressed by `idFromName(fingerprint)`.
  DOs guarantee global uniqueness, so every push for a project serializes
  through one single-threaded actor — exactly the consistency model conflict
  resolution needs. No locks, no races.
- **The DO is the merge engine.** It holds the canonical relational copy of
  that project's `crush.db` and decides what is truth.
- **R2 for overflow.** DO SQLite caps strings/blobs/rows at 2 MB. Large
  `messages.parts` (big tool outputs) are offloaded to R2 and referenced by
  key. Optional: periodic full row-dump export to R2 for cold backup.
- **D1 global index** holds denormalized per-project/per-session summaries
  (title, counts, cost, last activity, owner) so the dashboard lists
  thousands of projects without waking every DO. DOs write to it on change.

## 4. Identity

New migration adds a `sync_metadata` table to every `crush.db`:

```sql
CREATE TABLE IF NOT EXISTS sync_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- db_id              uuidv7, globally unique, generated on first connect
-- project_fingerprint SHA256(normalized_git_remote + ":" + repo_relative_.crush_path)
--                     fallback SHA256(absolute_path) flagged non-portable
-- push_cursor        highest local change seq already accepted by server
-- pull_cursor        highest server change seq already applied locally
-- last_sync_at       epoch seconds
```

`project_fingerprint` is the DO name and the re-association key: when `.crush/`
is gone, we derive the fingerprint from the working dir and ask the server for
that project's DO.

## 5. Change tracking (client side)

Each row needs a monotonic, comparable change marker so we can compute deltas.

- Add a per-database **Lamport-style counter** `change_seq` (stored in
  `sync_metadata`, bumped on every local mutation) and stamp it on rows.
- Implement via either:
  - a `_changelog` table populated by triggers (op, table, pk, change_seq), or
  - a `synced_seq` / `local_seq` column per syncable table.
- Decision: **`_changelog` table + triggers**, so we don't widen existing
  tables and we capture deletes/updates uniformly. Triggers added in the same
  migration as `sync_metadata`.

Changeset = all `_changelog` entries with `change_seq > push_cursor`, resolved
to current row values at push time.

## 6. Protocol

All requests carry `Authorization: Bearer <token>` (see §8). JSON bodies,
gzip/zstd on the wire. Per-project routing key = `project_fingerprint`.

### 6.1 Provision / lookup

```
POST /v1/projects/lookup
  { "fingerprint": "<hex>" }
  → 200 { "exists": true, "db_id": "...", "server_seq": 812 }
  → 404 (not yet provisioned)

POST /v1/projects
  { "fingerprint": "...", "db_id": "...", "hint": {"name":"crush","remote":"..."} }
  → 201 { "db_id": "...", "server_seq": 0 }   // creates the DO
```

### 6.2 Push (on Release)

```
POST /v1/projects/:fingerprint/push
  X-Base-Seq: <push_cursor>          // what the client last had from server
  { "changes": [ {op, table, pk, row}... ], "client_seq": <local change_seq> }

  → 200 {
      "server_seq": 815,             // new canonical seq after applying
      "applied": 12,
      "resolutions": [ ... ],        // see §7 (forks/archives)
      "pull": [ ... ]                // rows client is missing (server > base_seq)
    }
```

Push is **delta + pull in one round trip**: the DO applies the client's
changes, resolves conflicts, and returns everything the client is behind on.

### 6.3 Pull / bootstrap

```
GET /v1/projects/:fingerprint/pull?since=<pull_cursor>
  → 200 { "server_seq": 815, "changes": [ ... ] }

GET /v1/projects/:fingerprint/bootstrap            // full row dump
  → streamed NDJSON of every row, for reconstructing a deleted .crush/crush.db
```

### 6.4 Bulk status (startup)

```
POST /v1/sync/status
  { "projects": [ {fingerprint, push_cursor, pull_cursor}... ] }
  → { "stale": [{fingerprint, server_seq}], "orphaned": [{fingerprint, name}] }
```

### 6.5 Large parts overflow

When a `messages.parts` value exceeds ~1.5 MB, the client first
`PUT /v1/blobs/:hash` (streamed to R2 by the Worker, multipart), then sends the
row with `parts` replaced by `{"$blob": "<hash>"}`. Pull resolves blob refs
lazily or eagerly per config.

## 7. Conflict resolution (server-side, in the DO)

v1 = **last-write-wins per session, archive the loser** (matches earlier
decision). The DO, being single-writer, processes one push at a time:

```
for each incoming session S:
  if S unknown            -> insert S and its messages
  if S identical          -> no-op
  if incoming ⊇ server    -> fast-forward (append new messages)
  if server ⊇ incoming    -> drop incoming tail (client is behind; pull fixes it)
  if diverged (both added messages after a common prefix):
        v1: keep higher updated_at as canonical S;
            duplicate the loser as a new session (fresh db_id-scoped id),
            set archived_at on it, title += " (diverged <ts>)"
        v2: fork-on-diverge -> loser becomes a real fork with
            forked_from_snapshot_id at the common-prefix message
```

Non-session tables:

- `messages`  : INSERT OR IGNORE (append-only, unique ids)
- `files`     : take higher `version`
- `read_files`: union
- `snapshots` : INSERT OR IGNORE (immutable)
- `worktrees` : INSERT OR IGNORE
- `milestones`: INSERT OR IGNORE

The DO assigns the canonical `server_seq`. PITR bookmarks give 30-day
point-in-time recovery for free if a merge ever goes wrong.

## 8. Auth — SSH pubkey, 2-week tokens

```
POST /v1/auth/challenge   { "public_key": "ssh-ed25519 ..." }
   → { "challenge": "<base64 32B>", "expires": 30 }
POST /v1/auth/token       { "public_key", "challenge", "signature" }
   → { "token": "<jwt>", "expires_at": "<+336h>" }
```

- Verified at the edge in the Worker using `golang.org/x/crypto/ssh`
  equivalents (or WASM) — pure crypto, no CGO.
- **Token TTL = 2 weeks (336h)**, sliding refresh on use, hard cap 90d before
  re-signing.
- **Auto-registration** on first valid signature: `(pubkey_fingerprint →
  tenant_id)` stored in D1. Each pubkey = one tenant.
- A project DO records its owning tenant; cross-tenant access is rejected.

## 9. Client integration (in crush)

```
internal/sync/
  backend.go     // interface: Lookup, Provision, Push, Pull, Bootstrap, Status
  cf.go          // Cloudflare Worker/DO backend (first impl)
  identity.go    // fingerprint + db_id + cursors in sync_metadata
  changelog.go   // read/clear _changelog, build changesets
  apply.go       // apply remote changes into the live pool connection
  scanner.go     // walk root for .crush dirs (reuse crunch skip list)
  reconcile.go   // startup: status -> pull stale in background
  config.go      // ~/.config/crush/sync.json
```

Wiring points:

- **`db.Connect`** (`internal/db/connect.go`): if no `crush.db` exists, derive
  fingerprint → `Lookup` → `Bootstrap` to reconstruct the file; else create
  fresh + `Provision`. Never block: a 5 s timeout falls back to offline create.
- **`db.Release`** (refcount→0): `wal_checkpoint(TRUNCATE)`, build changeset
  from `_changelog`, fire async `Push`. Failure re-queues; never blocks
  Release.
- **Startup** (server process): background goroutine scans root, `Status`,
  pulls stale DOs’ changes, applies via `apply.go`. Orphaned projects
  (server-known, locally-missing) optionally reconstructed via `Bootstrap`.

Applying remote rows goes **through the pool connection** as
INSERT/UPSERT — we do not swap the file under a live server. Whole-file
reconstruction only happens during `Bootstrap` of a missing DB.

## 10. Offline behavior

| Scenario | Behavior |
|---|---|
| Server down at startup | Skip eager pull, log, proceed |
| Server down on Release | Persist pending changeset cursor, retry next Release/startup |
| Server down on Connect (missing DB) | Create fresh offline; associate on next sync |
| 5xx | Exp backoff, max 3 retries |
| Mid-transfer drop | DO applies atomically per push; partials discarded |

Hard timeouts: 5 s metadata, 30 s transfers. Exceed → proceed offline.

## 11. Config

`~/.config/crush/sync.json` (global):

```jsonc
{
  "enabled": true,
  "server": "https://sync.example.com",
  "root": "~",
  "exclude_paths": ["~/Library", "~/go", "~/.cache"],
  "scan_interval": "5m",
  "push_on_release": true,
  "pull_on_startup": true,
  "ssh_key": "~/.ssh/id_ed25519",
  "timeout_metadata_ms": 5000,
  "timeout_transfer_ms": 30000
}
```

## 12. Server repo layout (separate project)

```
crush-sync/                       (Cloudflare Worker, separate repo)
  src/worker.ts                   // router, auth verify, R2 streaming
  src/project_do.ts               // ProjectDurableObject: sql mirror + merge
  src/merge.ts                    // session-level conflict resolution
  src/auth.ts                     // SSH challenge/verify, token mint
  src/index_d1.ts                 // global dashboard index upserts
  wrangler.jsonc                  // DO bindings, R2 bucket, D1, cpu_ms limit
  schema/                         // DO sqlite schema = crush.db mirror + meta
```

## 13. Licensing note (FSL-1.1-MIT)

The sync server is a separate work, not a copy/derivative of Crush, and does
not substitute for Crush's functionality — selling access to it is a Permitted
Purpose. Danger zone: selling **hosted Crush instances / cloud agents running
current Crush** may be a Competing Use. Keep the product as the
sync/dashboard/orchestration layer with a bring-your-own Crush client, or build
cloud-agent hosting on a 2-year-old (now-MIT) Crush release, or get a
commercial license from Charm. Not legal advice.

## 14. Build order

1. Migration: `sync_metadata` + `_changelog` triggers (client).
2. `internal/sync` backend interface + identity/changelog/apply.
3. Cloudflare Worker + ProjectDurableObject: provision, push (delta+pull),
   bootstrap, status. v1 LWW merge.
4. SSH auth (challenge/token, 2w sliding).
5. Wire `db.Connect` / `db.Release`.
6. Startup scanner + reconcile.
7. D1 global index + dashboard read API.
8. v2: session fork-on-diverge; cloud-agent orchestration on merge events.

## 15. Open items

- Exact `_changelog` trigger set and whether to stamp `change_seq` on rows vs.
  a side table (lean: side table).
- Blob overflow threshold tuning (start 1.5 MB).
- Whether cloud agents write via the same push protocol (yes) and how the DO
  signals "new work" to orchestration (DO alarms + event to a queue).
- E2E encryption is intentionally **out** — server must read plaintext to
  merge and to power the dashboard.
