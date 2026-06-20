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

---

# Design delta — post-grilling (commits §16–§22 below)

The sections below SUPERSEDE the matching parts of §1–§15 wherever they
conflict. They were ratified after the design grilling on the initial
draft and reflect the actual product as we are building it.

## 16. Identity, devices, and recovery

### 16.1 Tenants
- A **tenant** is the primary identity. Tenant id is an opaque uuidv7
  minted in D1 on first successful auth. **Not** the SSH fingerprint.
- A tenant owns **N pubkeys** (devices) and exactly **one recovery code**.
- DO names are `tenant:<tenant_uuid>:<project_fingerprint>`. Tenants
  never share DOs; cross-tenant access is rejected at the edge.

### 16.2 Devices
- Each device is one ed25519 pubkey + a user-facing name (default
  `$HOSTNAME`, renameable via `crush auth rename <old> <new>`).
- D1 schema sketch:
  ```
  devices(id, tenant_id, pubkey_fp, pubkey, name, added_at,
          last_seen_at, revoked_at)
  ```
- Linking a new device: code-based. Existing trusted device approves.
  - `crush auth link` on new device prints a 6-word code + a short ttl
    challenge id.
  - `crush auth approve <code>` on a trusted device signs an "authorize"
    message bound to the challenge id; Worker verifies and registers
    the new pubkey.
  - No dashboard required to add a device.
- Listing/revoking: `crush auth devices`, `crush auth revoke <name|id>`.

### 16.3 Recovery code (single, rotatable)
- Exactly **one** recovery code per tenant.
- Generated client-side on first auth (forced before first push).
  Shown in plaintext exactly twice: at generation, and at any later
  rotate. Stored at rest as an argon2 hash.
- Consumption: redeeming the code registers a new pubkey on the
  tenant; sets `used_at`, `used_from_ip`, `used_from_ua`.
- After consumption, **recovery is disabled** until rotated. The CLI
  banner and dashboard both show "your recovery code was used on
  `<date>`, rotate now." This is the tripwire.
- Rotation: `crush auth recovery rotate` from any authed device. No
  need to enter the prior code. Generates and prints a fresh code,
  resets `used_at`.
- Pre-signup gate: a tenant cannot push until a recovery code exists.

### 16.4 Dashboard auth from CLI
- `crush auth web` mints a **single-use 5-minute JWT**, prints a URL
  like `https://dashboard.example.com/login#token=<jwt>`, and opens it
  via `open`/`xdg-open`.
- Dashboard browser session: **24h sliding**.

## 17. Sync timing — the only rule

The sync engine fires on exactly two events per database, and never
in the middle:

```
on db open  : push  (response carries pull payload)  -> apply
on db close : push  (response carries pull payload)  -> apply
```

Push and pull are **one round trip** (§6.2 already returns `pull` on the
push response). The "two pulls" wording in the original spec is
obsolete — there is one combined push+pull on open and one on close.

### 17.1 No mid-session pulls
- Once a session is running, the engine MUST NOT apply remote changes
  to that session's database. Period.
- A user can force a sync via the command-palette `Sync now`, but
  even then no remote rows are applied to the **currently open**
  database mid-session — see §17.3.

### 17.2 Open-time pull UX (TUI alert popup, not splash)
- The pull on open runs in the background once the TUI is up, surfaced
  as a popup alert (same component as other alerts) of the form
  "Pulling 14 sessions, 1 device since 6h ago."
- Apply policy on the **current** database during open: option (c) —
  the engine attempts to apply pulled rows at the next idle boundary
  (between tool calls, before the next prompt is sent). If no idle
  boundary is reached before the user closes, the apply happens at
  close-time as part of the normal close cycle. The user is never
  blocked typing.
- Because DOs are per `crush.db` (not per session), the engine
  prioritizes **the session the user just opened** in the changeset
  apply order; other sessions in the same db sync in the background.

### 17.3 Other databases on the same machine
- A `crush` server may have many `.crush/crush.db` files known via
  the root scanner. Open-time pull is **single-project** for the db
  being opened. Server-process startup (not session open) does the
  full root refresh.

### 17.4 Close-time push
- "Close" means **the database closed** (refcount→0 on the connection
  pool entry), not "the TUI quit" and not "a session ended." Multiple
  sessions inside one db do not each trigger a sync; the db closing
  triggers it once. This is deliberate: it's the corruption-avoidance
  story.
- It is acceptable for many days of activity to accumulate between
  syncs if the db stays open — that is the intended trade.
- AI inference requires network anyway; we do not maintain an
  offline-first posture for sync. Sync failures are still fail-open
  (queued for next close), but offline UX is not a design goal.

### 17.5 Sync now
- Command-palette entry: `Sync now`. Triggers a push+pull for the
  currently open db without closing it. Apply rules of §17.1/§17.2
  still hold (no mid-session apply on the active session).

## 18. Conflict rules — additions

(Supplements §7.)

- **Delete vs update race on the same session:** **update wins**,
  delete is reverted. Destructive operations lose to constructive
  ones. The deleting device sees the session reappear on its next
  pull and may delete again if intentional.
- **Tombstone retention:** deletes in `_changelog` persist until the
  server acks them via the push response (`applied` includes the
  delete's seq). They are not truncated by `TruncateChangelog` until
  acked. This is the "deletion needs to propagate even if the row
  is gone" requirement.
- **Server-side delete in dashboard:** D1 dashboard index uses
  soft-delete (`deleted_at`) for audit. DO mirror hard-deletes.

## 19. Schema-version negotiation

- Every push carries `X-Schema-Version: <int>` = count of goose
  migrations the client has applied to its `crush.db`.
- The DO carries its own embedded migration set (translated from the
  Crush goose migrations). On receiving a push, the DO migrates
  itself **up to** the client's version if needed.
- New client + old DO: DO upgrades itself, accepts push.
- Old client + new DO: DO answers `200` and includes a `pull` payload
  the client may not be able to fully apply; client surfaces "Crush
  is out of date — please upgrade" and refuses to apply rows it
  doesn't understand. We never reject the push.

## 20. Quotas, plans, and billing

### 20.1 Defaults
- Default plan: **free, 1 GB total per tenant**, summed across DO
  storage + R2 storage. (DO bytes are weighted 1:1 in user-facing
  display; internally tracked as separate counters because their unit
  costs differ.)
- Per-project hard ceiling: **0.9 × DO storage limit** (currently
  0.9 GB), to leave headroom inside the DO's own SQLite cap.
- Quotas are **soft**. We allow ~10% overage (e.g. 1.1 GB on a 1 GB
  plan) before any enforcement.

### 20.2 Behavior at thresholds
| Usage | Behavior |
|---|---|
| ≥ 90% of plan | CLI banner + dashboard banner: "approaching quota" |
| ≥ 100% of plan | Persistent "upgrade to keep syncing" warning, pushes still accepted |
| ≥ 110% of plan | Pushes rejected with a clear error; **pulls and bootstraps still succeed** so users can extract data |

### 20.3 Accounting
- D1 schema sketch:
  ```
  tenant_storage(tenant_id, do_bytes, r2_bytes, updated_at)
  tenant_plans(tenant_id, plan, plan_started_at, archived_at,
               subscription_id)
  ```
- DOs report their own `do_bytes` to D1 on every successful push.
  R2 ops report `r2_bytes` deltas on PUT/DELETE.
- A nightly cron Worker recomputes the truth from the source of
  record. D1 is the hot path; cron is reconciliation.
- Plan column exists from day one with `'free'` default. Future
  plans (`pro`, `team`) drop in without schema changes.
- `archived_at` exists from day one. Projects untouched ≥ 90d may
  be migrated to a cheaper backing store later.

### 20.4 Billing
- v1 ships **without Stripe**, but with `subscription_id` and `plan`
  columns ready. Overages are tracked but not charged.
- "Going commercial" lands when Stripe is wired in; no schema
  migration required at that point.

## 21. Logging & observability

### 21.1 Targets
- Worker: `console.log` is captured by Cloudflare Logs; **Logpush is
  configured to an R2 bucket** (`crush-sync-logs/`) for cheap
  long-term retention and ad-hoc query. External sinks (Datadog /
  Axiom) are a v2 question, not v1.
- Crush client: `slog.With("component", "sync")` group. Verbose
  payloads (changeset sizes, fingerprints, request ids) only when
  `CRUSH_SYNC_DEBUG=1`.

### 21.2 Correlation
- The client generates a ULID per push and sends it as
  `X-Request-ID`. The Worker echoes it on response headers and
  includes it in every log line for that request.
- The DO logs include the request id, tenant id, fingerprint, and
  schema version on entry/exit of every RPC.

### 21.3 PII rules
- Conversation contents are plaintext in the DO; **never** log them.
  Logs include row counts, byte counts, table names, and ids only.
- Pubkeys, fingerprints, and tenant ids are loggable.
- IPs and UAs are loggable (used for the recovery-code tripwire and
  general abuse defense).

## 22. Build/test layout (revised)

### 22.1 Crush client (this repo)
```
internal/sync/                        // protocol types, identity, changelog
internal/sync/synctest/                // in-memory fake Backend for unit tests
internal/sync/cf/                       // real CF backend implementation (later)
```

### 22.2 Sync server (separate repo: ../crush-sync)
```
crush-sync/
  src/index.ts                          // Hono router, auth middleware, logging
  src/auth/                             // SSH challenge/verify, JWT mint, devices
  src/projects/                         // /v1/projects/* handlers
  src/durable_objects/project.ts        // ProjectDurableObject + merge
  src/durable_objects/migrations/       // DO sqlite schema set
  src/d1/                               // global index, tenants, devices, plans
  src/d1/migrations/                    // D1 migration set (drizzle)
  src/r2/                               // blob storage helpers
  schema/                               // shared TS types mirroring crush.db
  test/                                 // vitest-pool-workers (Miniflare) tests
  wrangler.jsonc
  package.json
  drizzle.config.ts
  tsconfig.json
```

### 22.3 Test stack
- Server: `vitest` + `@cloudflare/vitest-pool-workers` running tests
  inside a real Workers runtime via Miniflare. No mocks of the DO
  storage API.
- Client: Go unit tests against `internal/sync/synctest` fake.
- E2E: Go tests behind `CRUSH_SYNC_E2E_URL` env var pointing at
  `wrangler dev`, run from the `crush-sync` repo's CI on every PR.

### 22.4 Routing libraries
- Worker: **Hono** (routing + middleware).
- D1 access: **Drizzle**.
- DO SQLite: raw `state.storage.sql.exec`. The DO mirror schema is
  generated from Crush's goose migrations via a small build script
  that lives in `crush-sync/scripts/translate-migrations.ts`.

### 22.5 License
- The sync server is licensed **All Rights Reserved** during private
  development. The FSL note in §13 still applies once Crush itself
  goes public; for now the constraint is just "don't redistribute."
