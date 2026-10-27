# Crush Cluster — Design

Status: all six rollout phases implemented (see "Known limitations").

## Goals

- Any number of machines (1 to N) form a peer-to-peer cluster. There is no
  central server; every node can reach every other node.
- A single TUI sees and drives every session on every node: one unified
  inbox, alerts, sounds, and permission gates. Other nodes may be fully
  headless daemons.
- Any session ("job") can be moved to any node and back, any number of
  times. Moving is transparent: the session keeps its ID and history and
  just shows a new owner.
- Offline-capable: push a session to a node and close the laptop.
- One machine works with zero cluster setup.

## Decisions (from design review)

| Topic | Decision |
| --- | --- |
| Pairing | `crush cluster join <addr> <token>`; an existing node approves the request (TUI or `crush cluster approve`). |
| Discovery | Explicit address. mDNS is a nice-to-have, later. |
| Transport | Direct IP + port, TLS secured by the cluster key. Not SSH. |
| Addresses | Peers store a list of typed endpoints (`direct` today) so relays can be added later without config changes. |
| Writers | Single owner per session. Two nodes never write the same session concurrently. |
| Replication | Everything (sessions, messages, files, embeddings, credentials) replicates to every node. |
| Embeddings | Replicated, not recomputed (same model cluster-wide). |
| On-disk state | Bootstrap creates the empty directory skeleton of each project on every node and syncs crush DBs. Moving a job ships the working tree as-is (uncommitted changes, untracked files, main repo and managed worktrees), skipping regenerable dirs (`node_modules`, …), git-ignored files, and the live `.crush` DB files. Files the destination already has are reused, not resent. |
| Path conflicts | If the destination has diverging state, refuse and prompt. When both sides are clean, check out the same ref (possibly fetching from the source as a remote). Never risk data loss. |
| Source copy | Left in place, untouched. The session just shows its new owner node. |
| Remote control | When the TUI drives a remote session, every setting (API keys, provider, model) applies to the owning node. |
| Credentials | Synced across the cluster. |
| Scheduling | Free CPU/memory weighting with manual override. |
| Versions | Nodes on a different release refuse jobs. `crush upgrade` installs the latest GitHub release; `crush cluster upgrade` upgrades every node. Dev builds are not supported for clustering. |

## Rollout

1. Node identity, persistent daemon, cluster versioning, pairing.
2. NATS mesh: swarm + attention over NATS, unified inbox, remote control.
3. Cluster dashboard (last seen, ping, CPU, memory, disk, jobs, version).
4. Single-job move with on-disk state.
5. DB replication with single-owner sessions, credential sync.
6. Migrate-all scheduler.

## Phase 1 details

### Files

All cluster state lives in `<global data dir>/cluster/` (next to the
global `crush.json` data file):

- `node.json` — stable node identity: `id` (UUID), `name` (hostname).
- `node.key` — the node's ed25519 private key (PEM, 0600).
- `cluster.json` — membership: cluster ID, current key, key version,
  members, and pending join requests (0600).

### Trust model

- The cluster key is a 32-byte secret. A cluster CA keypair is derived
  from it deterministically (HKDF → ed25519 seed), so every node holding
  the key can verify certificates without shipping a CA around.
- Each node mints a leaf certificate for its own key, signed by the
  cluster CA, with the node ID as the subject. All node-to-node traffic is
  mutual TLS 1.3 over these certificates.
- Holding the key only lets a node *ask* to join. Membership is decided by
  approval: approved members are pinned by node ID **and** public key, and
  every cluster RPC except `join` requires a pinned member.
- `crush cluster rotate-key` creates a new key and distributes it to every
  member over the existing mTLS channel. Old join tokens stop working;
  pinned members keep working.

### Token

`crush cluster token` prints `crushc1_<base64url(cluster_id || key)>`.
Treat it like a password.

### Pairing flow

1. Node A: `crush cluster init` (once). Prints the join token.
2. Node B: `crush cluster join <A-addr> <token>`. B stores the key, sends
   a signed join request, and waits.
3. Node A shows the pending request in the TUI (or
   `crush cluster pending`); the user approves (`crush cluster approve
   <node>`).
4. A pins B, returns the full member list; B pins every member. A tells
   the other members about B.

### Daemon

- `crush server --persistent` never auto-exits when its last workspace
  closes. The cluster listener starts automatically when the node belongs
  to a cluster.
- `crush cluster install-service` writes and loads a launchd agent
  (macOS) or a systemd user unit (Linux) that runs
  `crush server --persistent` with restart-on-exit.

### Ports

The cluster listener defaults to TCP `7731` (`--cluster-addr` overrides
it). The local client socket is unchanged.

### Versions

`proto.VersionInfo` carries a `protocol` number. Nodes compare
`version` + `protocol` on join and on every cluster RPC; mismatches are
rejected with an upgrade hint.

## Commands

| Command | Purpose |
| --- | --- |
| `crush cluster init` | Create a cluster; prints the join token. |
| `crush cluster join <addr[:port]> <token>` | Request to join via any member; waits for approval. |
| `crush cluster status` | Members, versions, last seen, pending requests. |
| `crush cluster approve <request\|name>` / `deny` | Decide a pending request (the TUI also prompts). |
| `crush cluster token` | Print the current join token. |
| `crush cluster rotate-key` | New key; old tokens stop working. |
| `crush cluster ping` | Reachability, RTT, and version of each member. |
| `crush cluster name <name>` | Rename this node. |
| `crush cluster leave` | Leave the cluster. |
| `crush cluster upgrade [version]` | Upgrade this node, then every member, to one release, and wait until each reports it (`--no-wait`, `--timeout`). |
| `crush cluster install-service` / `uninstall-service` | Manage the launchd/systemd daemon. |
| `crush upgrade [version]` | `go install` a release over the running binary (same directory) and restart the local daemon. |
| `crush cluster top` | Live dashboard of every node. |
| `crush session move <session> <node>` | Move a session and its files (`--on-conflict=backup\|keep`, `--no-resume`, `--gc`). |
| `crush cluster migrate` | Move every running job off this machine (`--all`, `--to`, `--strategy`, `--yes`). |

`--advertise host:port` on `init`/`join` overrides auto-detected
addresses (useful to pin the WireGuard IP).

### Known phase 1 limitations

- The TUI approval prompt only appears for requests that arrive while a
  TUI is attached; older requests show in `crush cluster status`.
- Denials are remembered until the server restarts.
- Members unreachable during `rotate-key` keep the old key (still trusted
  as `prev_key`) until the next rotation reaches them.
- `crush cluster upgrade` restarts remote nodes by exiting; nodes not
  running under `install-service` stay down until started again.

## Phase 2: mesh and unified control

- Every node embeds a NATS server (`--mesh-addr`, default `:7732`) and
  routes to every member over mutual TLS. Route certificates must belong
  to a **pinned member**, so a leaked join token cannot read mesh
  traffic. The NATS client port is loopback-only and token-protected;
  Crush talks to it in-process.
- Subjects: `crush.attention` (permission/question/busy/idle/join events),
  `crush.swarm.lookup` (scatter-gather address resolution),
  `crush.swarm.send.<node>` (delivery), `crush.heartbeat`,
  `crush.ping.<node>`, `crush.repl.changed`, `crush.creds`.
- Each node exposes its full local API to members at `/node/v1/api/*`
  over the cluster listener; the local server proxies
  `/v1/nodes/<node-id>/<path>` to it. The TUI drives a remote session by
  retargeting its client at that prefix, so every action — prompts,
  permissions, API keys, provider/model changes — applies on the owning
  node. Approved members can fully control each other.
- The sidebar lists sessions from every node (`@node` prefix on remote
  workspaces), `n` cycles a per-machine filter, `/` search matches node
  names, and the header shows `@node` while a remote session is open.
- Swarm: lookups fall back to the cluster when no local session matches;
  sends to remote sessions are stamped with the trusted sender identity
  on the sender's node, then delivered by the owner.

## Phase 3: dashboard

`Cluster Dashboard` in the command palette (or `crush cluster top`) shows
each node's online state, last seen, mesh RTT, CPU (with load), memory,
disk, uptime, workspace/session counts, and running jobs, refreshed
every 2 seconds from 5-second heartbeats. A node is offline after three
missed beats.

## Phase 4: moving a job

`m` on a sidebar row, `Move Session to Another Machine` in the palette,
or `crush session move <session> <node>`:

1. A running turn is paused (cancelled at a step boundary).
2. The destination runs a **preflight**: same schema version, not
   already the owner, and a plan for the files:
   - `fresh` — nothing there (or only the mirrored skeleton/replica DB):
     ship everything, including `.git`, Crush's snapshot repo, and
     managed worktrees.
   - `overlay` — a clean checkout at the same commit: ship working files
     only.
   - otherwise **refuse**. The TUI then offers *back up and replace*
     (rename the destination aside) or *conversation only* (keep their
     files).
3. The bundle streams source → destination over the cluster listener:
   `manifest.json`, typed DB rows for the session tree, then the tree.
   It is staged beside the destination and moved into place only after
   it fully arrives. Paths are traversal-checked, symlinks are created
   last and writes never follow symlinks.
4. Ownership flips: the destination clears `owner_node`; the source
   sets it to the destination and keeps its copy as a replica. The
   paused turn resumes on the destination. The TUI follows the session.

Skipped directories: `node_modules`, `.venv`, `venv`, `__pycache__`,
`.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.next`, `.turbo`,
`.gradle`, `.parcel-cache`, and the live `crush.db*`, locks, and logs.

Git-ignored paths (per `git ls-files --ignored --exclude-standard`, which
includes your global excludes file) are skipped too, except the Crush
data dir, which always travels minus its live DB. Ignored files such as
`.env` therefore stay behind; recreate them on the destination.

When the destination already has a tree (overlay or backup plans), its
preflight answer lists the sizes and SHA-256 hashes of its largest files.
The sender replaces matching files with reuse markers and the receiver
copies its own verified copy into the staged tree, so re-moves and moves
back to a machine send only what changed. A file that changed in the
meantime fails the import (nothing is touched); just retry.

Pick "clean up, then move" in the move dialog (or `--gc`) to
garbage-collect the `.crush/git` snapshot repository first.

## Phase 5: replication

- `sessions.owner_node` records the single writer (`NULL` = this node).
  Writes (`SendMessage`) to a session owned elsewhere are refused.
- Owners serve only rows they own from `_changelog`
  (`/v1/replication/changes`), full snapshots on first contact or after a
  changelog gap. Peers pull on `crush.repl.changed` and every 30 s.
- Embeddings are append-only and not in `_changelog`, so they replicate
  continuously by rowid cursor, paged 2000 at a time; deleting a message
  drops its embeddings on replicas too.
- `replica.Apply` only writes rows whose session is owned by the
  sending peer, uses upserts (never `REPLACE`, which would cascade
  deletes), and restores trigger-maintained counters afterwards. A node
  never overwrites a session it owns.
- Replicas live in each project's own `.crush/crush.db` at the same
  path; the first full snapshot also mirrors the directory skeleton.
- If an owner is unreachable, its sessions still appear (read-only,
  labelled `@node offline`) from the local replica.
- The global data config (API keys, OAuth tokens) syncs across nodes,
  newest write wins, over the mesh only. A node that starts or joins asks
  every peer to announce, so a fresh install gets credentials within
  seconds of approval. The user config (`~/.config/crush/crush.json`:
  custom providers, MCP, LSP) is not synced.

## Phase 6: migrate all

`Migrate All Jobs to Cluster` (palette) or `crush cluster migrate`
plans moves for every running job (`a`/`--all` adds idle sessions),
keeping each project's sessions together on one node. Placement prefers
spare CPU/memory (`capacity`) or round-robins (`even`); `--to` limits
targets. The plan is shown before anything moves; failures are reported
per session.

## Testing

`task test:cluster` runs a two-container harness (`test/cluster/`) that
pairs nodes, moves a session with its files, checks single ownership,
replicates an edit back, and moves the session home.

## Known limitations

- A paused turn resumes with a new prompt; a tool call that was mid-way
  is not replayed.
- Sessions of the same project owned by different nodes live in separate
  working copies; merging their file changes is a git task.
- Join prompts only reach TUIs connected when the request arrives.

## Event stream wire format

TUIs subscribe to a workspace's events over SSE. Clients ask for
`wire=2`; servers that predate it keep sending wire 1 (full events),
which wire 2 clients still decode.

- **Deltas:** a streamed message is sent in full when it first appears,
  after any change that is not a pure append (a finished tool call, a
  new part), and every 1000 deltas; otherwise only appended text is
  sent (`message_delta`, with each field's prior length so a diverged
  client drops the delta and waits for the next full message).
- **Frames:** clients of remote nodes add `frame_ms=50`, merging
  per-message updates into at most one per frame. Other events flush
  pending updates first, so ordering is preserved.
- **Compression:** wire 2 streams are gzipped when the request accepts
  it. Go's transport asks for gzip on the cluster hop and decompresses
  transparently; the local unix-socket client disables it.

For a typical 8.5 KB answer streamed token by token, wire 1 sends 7.9 MB;
wire 2 sends 284 KB, 70 KB with frames, and 11 KB with frames and gzip
(`CRUSH_WIRE_REPORT=1 go test ./internal/server -run TestWireFormatReport -v`).
