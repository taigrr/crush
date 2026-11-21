# Hotspots (baseline)

Measured on Apple M6, `GOMAXPROCS=4`, go1.27.3, HEAD `1e770dc7`. Raw data
and profiles live in `bench/out/baseline/` (`*.cpu.pprof`, `*.mem.pprof`,
`*.test`). Ranked by expected user-facing impact.

## 1. CLI cold start: `gh extension list` subprocess at package init

- Evidence: `crush --version` 206 ms, `--help` 214 ms, `dirs` 228 ms
  (hyperfine medians). `GODEBUG=inittrace=1` shows
  `internal/agent/tools` init at **~234 ms clock**, next largest is
  `chroma/v2/styles` at 14 ms.
- Cause: `ghStackAvailable` in `internal/agent/tools/tools.go:112` runs
  `exec.Command("gh", "extension", "list")` in a package-level var
  initializer, so every invocation (even `--version`) forks `gh`.
- Idea: make `ghAvailable`/`ghStackAvailable` lazy (`sync.OnceValue`),
  evaluated only when tool descriptions are rendered. Expect ~10x faster
  cold start. Secondary: chroma styles/lexers init (~26 ms, 72k allocs)
  could be deferred.

## 2. TUI frame: fresh 200x60 `ScreenBuffer` allocated per frame

- Evidence: `BenchmarkUIView` 4.1 ms/frame, 1.64 MiB/frame, 2.6k allocs.
  alloc_space: `uv.NewScreenBuffer` -> `uv.NewBuffer` **84.9%** of bytes.
  CPU: `Buffer.Render`/`renderLine` 28%, `UI.Draw` 32%, GC mark workers
  17%.
- Cause: `UI.View` (`internal/ui/model/draw.go:194`) allocates a new canvas
  every frame, then `canvas.Render()` + `ReplaceAll` + `Split` + per-line
  `TrimRight` + `Join` copies the whole screen string several times.
- Ideas: reuse the canvas across frames (clear instead of reallocate,
  realloc only on resize); trim trailing spaces and normalize newlines in
  one pass into a reused `strings.Builder`; `BenchmarkUIScrollFrame`
  (7.2 ms, 2.6 MiB) benefits from both.

## 3. Diff rendering: chroma re-highlighting and lipgloss per segment

- Evidence: `BenchmarkDiffRender` unified 201 ms / 21.9 MiB / 549k allocs,
  split 235 ms / 29 MiB / 768k allocs (400 line file). CPU:
  `lipgloss.Style.Render` **46.9%**, `DiffView.hightlightCode` 44.4%
  (chroma lexing `matchRules` 23%). alloc: `Style.Render` 55%,
  `strings.Builder.WriteString` 36% flat.
- Ideas: highlight each side once and slice per line instead of per
  hunk/line; cache line-number/gutter styles as prerendered strings;
  avoid `Style.Render` for plain padding (write spaces directly).

## 4. Server wire encoding of full messages

- Evidence: `BenchmarkWireFullMessage` 104 ms / 42.6 MiB / 41k allocs.
  alloc: `bytes.Clone` **74.9%** flat; `wrapEvent`/`envelope` 59%;
  `proto.Message.MarshalJSON` 38%. CPU: `jsontext.reformatObject` 26%.
- Cause: payload is marshalled, then cloned and re-encoded (reformatted)
  while wrapping into the envelope.
- Ideas: marshal envelope + payload in a single encoder pass (embed raw
  payload as `jsontext.Value` without reformat), drop the `bytes.Clone`,
  pool encode buffers.

## 5. Session load: JSON decode of message parts

- Evidence: `BenchmarkMessageList` 500 msgs 16.1 ms / 2.9 MiB,
  2000 msgs 64.5 ms / 12 MiB / 74k allocs (linear). CPU:
  `message.UnmarshalParts` **41%**, json/v2 decode 38%, SQLite
  `database/sql` 17%. alloc: `fromDBItem` 69%, `jsontext.Value.UnmarshalJSON`
  25% (raw part copies), `ListMessagesBySession` 25%.
- Ideas: decode parts with a single pass into typed structs (avoid
  intermediate `jsontext.Value` copies of `data`); avoid re-copying the
  `parts` column string to `[]byte`; paginate/lazy-load older messages in
  the TUI.

## 6. UI session load and markdown

- Evidence: `BenchmarkUISessionLoad` 500 msgs 21 ms / 145k allocs, 2000
  msgs 29 ms / 175k allocs. `BenchmarkMarkdownRender` 37 ms / 359k allocs
  for a ~4 KB reply (glamour + chroma on code blocks).
- Ideas: markdown is the dominant per-item cost when items become visible;
  cache rendered output per (content, width) across reloads and consider a
  lighter code-block highlighter path. Session load allocations grow with
  history size even though only the tail is visible: defer item
  construction for off-screen messages.

## Not hot

- `BenchmarkBrokerPublish`: 0.79 us, 0 allocs.
- `BenchmarkLoadFromConfigPaths`: 206 us, 396 allocs.
- `BenchmarkWireDeltas`: 2.7 ms, 715 KiB.

## Cluster (attempt 12 baseline)

Profiles in `bench/out/cluster-baseline/*.top.txt`.

1. **Replica full sync is by far the slowest path**: 1.33 s for 200
   sessions x 10 messages (2.2k rows, 842 KiB). `Apply` dominates (Serve is
   negligible): one unprepared `upsert` per row inside the tx plus a
   per-row `owner()` `SELECT` for every message, with sqlite prepare ~12%
   of CPU and >50% of samples in goroutine wakeups (`pthread_cond_signal`)
   from per-statement context handling. Ideas: cache owner lookups per
   session within a batch, prepare one statement per table/column set,
   batch multi-row upserts.
2. **Cold join**: 194 ms for 3 nodes, of which mesh routing is 84 ms (TLS
   route handshakes ~27% of CPU; NATS pools several route connections per
   peer, each with its own handshake). The other ~110 ms is pairing; real
   `Join` polls approval every **2 s** by default (`ops.go`
   `PollInterval`), so an interactive join waits up to 2 s after approval.
3. **Reconnect**: 28 ms when the restarted peer dials out. Survivors rely
   on NATS route reconnect (`ConnectRetries: -1`, default 1 s reconnect
   delay), so if the restarted peer cannot dial back, recovery is bounded
   by that interval instead.
4. **CallAPI** (0.44 ms) is ~3x a NATS round trip (0.14 ms): HTTP + JSON +
   mTLS per call on a keep-alive transport; `CallAPI` builds a new
   `http.Client` per call (cheap) and resolves the endpoint each time.
5. **Overview fetch**: 1.9 ms for 2 x 200 sessions, mostly JSON decode
   (472 KiB/op). The server's remote view also does a separate
   sync-position call before each overview fetch (serial, two round
   trips per node).
6. Not hot: NATS throughput 175 MiB/s; ServeDelta 0.48 ms.

## Repeated TUI attach: FS + DB discovery (attempt 26 baseline)

Measured with `bench/startup.py --mode {cold,warm-new,reuse,reuse-teardown}
--fixture {large,small}` (10 runs + 1 discarded warmup, medians). Large
fixture: committed git monorepo, 9,100 files incl. gitignored nested
`node_modules`, 20 project commands, 10 project skills, plus 8 extra
registered projects with 200-session DBs. All timings come from
`startuptrace.Span`/`Count` (every occurrence recorded; labels only).
`cli.` = TUI process, `srv.` = server process. Syscall tracing
(dtruss/ktrace) needs SIP changes, so counts are Go-level (walk entries,
calls).

**Harness caveat.** The previous `--warm` mode (now `warm-new`) reused the
server but created a *new non-git temp project* every run: every "warm"
number paid full workspace creation (DB create + 35 ms goose migrate,
first registry add) yet *skipped* all git-dependent work (checkpoints,
git status, worktree root), because the temp dirs are not repos. It
measured neither re-attaching to a loaded workspace nor relaunching in a
real repo. `reuse` (same path, workspace still loaded within the 2 min
reconnect grace) and `reuse-teardown` (same path, workspace torn down
between launches, server still running: what quitting and relaunching
the TUI does) are the repeat-attach cases.

| ready (ms) | cold | warm-new | reuse | reuse-teardown |
|---|---|---|---|---|
| large | 503 | 257 | 308 | 728 |
| small | 479 | 246 | 240 | 365 |

`main` (exec to `main()`) is ~118 ms in every mode and is not part of
this section.

### (a) Client, before/around connect (every launch)

| call chain | cost | freq |
|---|---|---|
| `cmd.connectToServer` -> `ResolveCwd` | 0.02 ms | 1 |
| `cmd.ensureServer` (socket stat + `restartIfStale` version RPC) | 1.5 ms | 1 |
| `ui.loadCustomCommands` -> `commands.LoadCustomCommands` (WalkDir of 3 command dirs) | 0.1 ms (3 ms w/ 20 cmds) | 1 |
| `ListSkills` RPC (server returns cached manager) | 1-6 ms | 1 |

No client-side project walk, git detection, or config discovery runs on
launch: the TUI gets config from the server. The `@` file completion walk
(`completions.loadFiles` -> `fsext.ListDirectory`) is lazy (first `@`).

### (b) Server, on every connect even when the workspace is loaded (`reuse`)

| call chain | large | small | freq/connect |
|---|---|---|---|
| `backend.CreateWorkspace` -> `resolveWorkspaceKey` -> `config.ProjectRoot` -> `worktreeRoot` | 0.3 ms (cache hit); 23 ms `git rev-parse` fork when the 5 s TTL expired | same | 1 |
| `adoptExistingWorkspace` -> `reloadWorkspaceConfig` -> `ConfigStaleness` (stats) | 0.04 ms | 0.04 ms | 1 |
| `InitAgent` RPC -> `App.InitCoderAgent` -> `agent.NewCoordinator` (full coordinator rebuild) | 0.8 ms sync | 0.8 ms | 1 |
| `agent/update` RPC -> coordinator rebuild | 5-9 ms | 9 ms | 1 |
| -> async `coordinator.buildAgent` readiness -> `prompt.Build` -> `promptData` | 264 ms each, 1,851 ms total | 132 ms each, 923 ms total | **7** |
| -> `getGitStatus` (3 `git` execs via shell: branch, `status --short`, `log -n 3`) | 254 ms each, 1,780 ms total | 127 ms each, 888 ms total | **7** |
| -> `skills.Discover` (fastwalk of skill dirs, Follow) + builtin parse | 9 ms each, 63 ms total, 147 entries walked each | 2 ms each | **7** |
| -> `processContextPath` (16 context-file stats) | 0.2 ms each | 0.1 ms | 7 |
| `ListWorkspaceOverviews` -> `registry.List` + `peekCache` over 8 DBs | 6.4 ms in-handler, 39 ms HTTP (CPU contention with the git forks above) | 4.6 / 5 ms | 1 |

### (c) Server, once per workspace build (fresh path, and *again* after every teardown)

| call chain | large repo (`reuse-teardown`) | non-git temp (`warm-new`) |
|---|---|---|
| `buildWorkspace` total | 298 ms | 58 ms |
| `config.Init` -> `Load` (`lookupConfigs` 0.3, `loadFromConfigPaths` 0.4, `configureProviders` 3) | 7 ms | 12 ms |
| `db.Connect` (open + ping + `schemaCurrent`; migrate only if new) | 3.9 ms | 38 ms (35 ms goose migrate) |
| `skills.DiscoverFromConfig` | 2 ms | 0.7 ms |
| `app.New` -> `checkpoint.NewService` -> `InitRepoAt` -> `gitignore.ReadPatterns` (go-git walk of the whole work tree reading every `.gitignore`) | **273 ms** | 0 (not a repo) |
| `app.New` -> `InitCoderAgent` (then the client calls `InitAgent` again: 2 builds) | 2.4 ms x2 | 1.9 ms x2 |
| `prompt.Build` (async) | 12 calls, 5,345 ms total | 12 calls, 27 ms |
| `wireSwarmBackend` | 5 ms | 3 ms (93 ms on a cold server) |
| `registry.Add` | 1 ms | 0.3 ms |

Once per server process only: `enableCluster` (no-op without cluster
config, not measurable here) and registry store construction.

A non-persistent auto-spawned server exits when its last workspace tears
down (2 min after the last TUI quits), so a later launch pays `cold`
(+~250 ms) on top of (c).

### Ranked caching candidates

1. **`prompt.Build` git status, 7-12x per connect** (1.8-5.3 s CPU
   per attach on the large repo, 0.9-2.4 s on an empty repo). Build the
   system prompt once per coordinator generation, or at least cache
   `getGitStatus` per working dir with a short TTL / invalidate on
   `.git/index` + `HEAD` mtime. Also avoid the double coordinator rebuild
   (`app.New` InitCoderAgent + client `InitAgent` + `agent/update`).
   Invalidation: git index/HEAD change, config change (provider/model,
   context paths, skills paths), date rollover.
2. **`checkpoint.InitRepoAt` gitignore walk, 273 ms on the critical path
   of every rebuild.** Cache the matcher in the backend keyed by project
   root so it survives workspace teardown, or load patterns lazily on the
   first snapshot. Invalidation: any `.gitignore`/`.git/info/exclude`
   change (mtime of the files found by the previous walk; new
   directories can add new `.gitignore`s, so lazily re-walk at snapshot
   time).
3. **Skills discovery 7-13x per connect** (fastwalk with Follow). Reuse
   the workspace `skills.Manager` result in `promptData` instead of
   rediscovering. Invalidation: existing skills watcher / config
   skills_paths change.
4. **Workspace teardown itself**: keeping a torn-down workspace's config,
   ignore matcher and DB handle warm for a while (or not tearing down
   the last workspace on a persistent server) turns `reuse-teardown`
   (728 ms) into `reuse` (308 ms).
5. `worktreeRoot` 5 s TTL: a 23 ms `git rev-parse` fork whenever a launch
   comes >5 s after the previous lookup. Could key on `.git` HEAD/
   commondir mtime instead of time. Invalidation: `git init`, worktree
   add/remove.

Not hot: config lookup/parse (<1 ms), DB open of an existing DB (4 ms),
registry list (0.4 ms), overview peeks of 8x200-session DBs (3 ms),
client-side command loading (0.1-3 ms).

## Final validation: repeated TUI attach FS/DB caching

See RESULTS.md attempt 31 for the full table. Ready (2..N, n=20) f32dc805
-> final: small reuse 228.7 -> 165.6 ms, small reuse-teardown 324.2 ->
191.7, large reuse 302.3 -> 165.7, large reuse-teardown 765.3 -> 196.8.

Invalidation rules:
- Prompt git status: HEAD, index, packed-refs, refs/heads, current ref
  (worktree `.git` pointer + `commondir` followed), plus 5 s TTL.
- Prompt skills: each root's stat, entry stats, and each child's SKILL.md
  stat, plus 5 s TTL.
- Checkpoint gitignore: mtime+size of every walked dir and probed
  `.gitignore`/`info/exclude`; no TTL; revalidated in background, first
  snapshot waits.
- Caches bounded (256 prompt entries, 128 work trees).
- DB discovery (~3 ms) intentionally uncached.

## Scale: hundreds of workspaces x thousands of sessions

Attempt 32 (baseline only, HEAD b1de0504). Harness: `bench/scale/gen`
(deterministic fixture: N workspaces, each `work/wsNNNN/.crush/crush.db`
migrated by crush's own `db.Connect`, M sessions x 4 messages + 3
sessions x 300 messages, all registered in `workspaces.jsonl`; `-clone`
fills one DB and copies it, so session IDs repeat across workspaces),
`bench/scale.sh` (matrix + `bench/startup.py --fixture-root --probe`
against a private persistent server), `bench/scale/summary.py`, Go
benchmarks in `bench/scale/scale_test.go`. Fixtures in
`/Volumes/Store1/tmp/crush-scale-*`, deleted after each point (300x5000 =
3.8 GB, 34 s to generate; nothing skipped). Load avg 1.9-3.2.

### Consolidated: attempt-32 baseline vs final HEAD (attempt 37, back to back, same fixtures)

TUI requests the bounded (50/workspace) overview; probe = unlimited
`GET /v1/workspace-overviews` (peers/rush). Remaining cost at scale: the
unlimited overview encode (~4.3 s at 300x5000) and server RSS (~1 GB
peek cache, now LRU-capped at 2M sessions).

| bin | N ws | M sess | ready ms (med +-sd) | overviews req ms | srv list_overviews ms | full-overview probe ms | full payload MB | sessions (full) | unavailable | server RSS MB (after attach / after probes) |
|---|---|---|---|---|---|---|---|---|---|---|
| attempt-32 baseline 9550a3eb | 10 | 200 | 195 +-34 | 22 | 5.9 | 15 | 0.3 | 2000 | 0 | 116/116 |
| pre-37 HEAD daff1a1e | 10 | 200 | 184 +-5 | 15 | 6.0 | 11 | 0.3 | 2000 | 0 | 112/112 |
| final (attempt 37) | 10 | 200 | 185 +-13 | 16 | 5.8 | 10 | 0.3 | 2000 | 0 | 112/112 |
| attempt-32 baseline 9550a3eb | 100 | 2000 | 1207 +-682 | 1038 | 118.1 | 493 | 27.0 | 200000 | 0 | 495/484 |
| pre-37 HEAD daff1a1e | 100 | 2000 | 259 +-16 | 107 | 32.2 | 417 | 27.0 | 200000 | 0 | 365/475 |
| final (attempt 37) | 100 | 2000 | 264 +-25 | 112 | 34.1 | 415 | 27.0 | 200000 | 0 | 354/551 |
| attempt-32 baseline 9550a3eb | 100 | 5000 | 3492 +-787 | 2884 | 386.2 | 1698 | 67.5 | 500000 | 0 | 189/548 |
| pre-37 HEAD daff1a1e | 100 | 5000 | 389 +-32 | 214 | 69.7 | 1328 | 67.5 | 500000 | 0 | 751/628 |
| final (attempt 37) | 100 | 5000 | 368 +-29 | 210 | 72.6 | 1267 | 67.5 | 500000 | 0 | 752/550 |
| attempt-32 baseline 9550a3eb | 300 | 2000 | 4722 +-595 | 4290 | 3072.9 | 1943 | 80.7 | 600000 | 0 | 150/654 |
| pre-37 HEAD daff1a1e | 300 | 2000 | 400 +-28 | 232 | 39.1 | 1475 | 80.7 | 600000 | 0 | 837/846 |
| final (attempt 37) | 300 | 2000 | 392 +-25 | 230 | 41.0 | 1799 | 80.7 | 600000 | 0 | 816/820 |
| attempt-32 baseline 9550a3eb | 300 | 5000 | 4639 +-440 | 4439 | 3117.5 | 4403 | 40.0 | 295000 | 241 | 764/731 |
| pre-37 HEAD daff1a1e | 300 | 5000 | 1530 +-262 | 925 | 113.2 | 4290 | 202.3 | 1500000 | 0 | 1258/1106 |
| final (attempt 37) | 300 | 5000 | 631 +-33 | 460 | 75.9 | 4356 | 202.3 | 1500000 | 0 | 1861/913 |

### 1/2/4. TUI attach ready, overview fetch, payload, server RSS (10 runs, target = ws0000)

"ready" = overviews loaded into the sidebar. first_frame stayed ~175-180 ms
at every point (not O(N*M)); everything after it is the overview fetch.
"probe" = 5 direct `GET /v1/workspace-overviews` after the runs (warm
peek cache). RSS before = after the attach runs (attach already fetched
overviews once).

| mode | N ws | M sess | ready ms (med +-sd) | overviews req ms | srv list_overviews ms | probe overview ms (med) | payload MB | sessions returned | unavailable | RSS MB before/after | disk MB |
|---|---|---|---|---|---|---|---|---|---|---|---|
| reuse | 10 | 200 | 197 +-65 | 20 | 6.0 | 12 | 0.3 | 2000 | 0 | 114/114 | 11 |
| reuse | 10 | 2000 | 325 +-287 | 157 | 53.6 | 86 | 2.8 | 20000 | 0 | 166/168 | 55 |
| reuse | 10 | 5000 | 530 +-96 | 360 | 87.0 | 209 | 6.9 | 50000 | 0 | 264/264 | 128 |
| reuse | 100 | 200 | 273 +-144 | 117 | 10.4 | 56 | 2.7 | 20000 | 0 | 172/175 | 114 |
| reuse | 100 | 2000 | 1147 +-303 | 983 | 111.3 | 492 | 27.0 | 200000 | 0 | 540/540 | 551 |
| reuse | 100 | 5000 | 3015 +-892 | 2651 | 271.1 | 1618 | 67.5 | 500000 | 0 | 663/550 | 1284 |
| reuse | 300 | 200 | 462 +-766 | 302 | 23.7 | 142 | 8.1 | 60000 | 0 | 296/310 | 344 |
| reuse | 300 | 2000 | 3547 +-761 | 3036 | 309.3 | 1838 | 80.7 | 600000 | 0 | 368/723 | 1658 |
| reuse | 300 | 5000 | 4357 +-361 | 4150 | 3104.0 | 4622 | 75.0 | 555000 | 189 | 833/653 | 3853 |
| reuse-teardown | 10 | 200 | 265 +-56 | 24 | 8.3 | 24 | 0.3 | 2000 | 0 | 118/118 | 11 |
| reuse-teardown | 10 | 2000 | 406 +-35 | 178 | 70.2 | 61 | 2.8 | 20000 | 0 | 165/165 | 55 |
| reuse-teardown | 10 | 5000 | 561 +-26 | 345 | 88.5 | 134 | 6.9 | 50000 | 0 | 239/240 | 128 |
| reuse-teardown | 100 | 200 | 316 +-267 | 116 | 15.4 | 56 | 2.7 | 20000 | 0 | 172/172 | 114 |
| reuse-teardown | 100 | 2000 | 1202 +-56 | 987 | 115.5 | 467 | 27.0 | 200000 | 0 | 537/537 | 551 |
| reuse-teardown | 100 | 5000 | 3288 +-464 | 3005 | 566.6 | 1390 | 67.5 | 500000 | 0 | 607/504 | 1284 |
| reuse-teardown | 300 | 200 | 531 +-10 | 316 | 34.9 | 142 | 8.1 | 60000 | 0 | 284/284 | 344 |
| reuse-teardown | 300 | 2000 | 3693 +-616 | 3302 | 401.6 | 1448 | 80.7 | 600000 | 0 | 660/758 | 1658 |
| reuse-teardown | 300 | 5000 | 5511 +-1122 | 5240 | 3226.7 | 5299 | 110.7 | 820000 | 136 | 384/591 | 3853 |

Scaling: payload and ready time are linear in total sessions N*M
(~135 B/session on the wire; ~5-6 us/session ready). At 300x5000 the
response is **wrong**: 136-189 of 300 workspaces come back
`unavailable` ("timed out after 3s", peek stall guard), because 300
peeks run with unbounded concurrency and contend.

### 2b. Where the overview time goes (CPU pprof, server, 100x5000, warm cache, 10 s)

- `server.jsonEncode` / json/v2 struct marshal of 500k `SessionOverview`: 28% of CPU.
- `backend.sortSessionOverviews` (`sort.SliceStable` re-run every request on cached slices) via `peekOverviews.func1`: 13.5%.
- `db.PeekSessions` (first/stale peeks): 28%. GC (scanObject, bulkBarrierPreWrite, madvise): ~25%.
- Client decode (`BenchmarkDecodeOverviews`, 67 MB payload): **963 ms, 380 MB, 1.0M allocs** per fetch in the TUI process.

### 3. DB discovery / open / registry (Go benchmarks, GOMAXPROCS=4, count=5)

| bench | 200 | 2000 | 5000 |
|---|---|---|---|
| PeekSessions (one DB) | 5.1 ms / 190 KiB | 18.6 ms / 1.6 MiB | 45.3 ms / 5.3 MiB |
| session.Service.List (attached, TUI picker) | 2.2 ms / 385 KiB | 21.2 ms / 4.5 MiB | 57.4 ms / 12 MiB |
| db.Connect+Release (existing DB) | 3.6 ms | 3.8 ms | 3.8 ms |

| RegistryList (5 records per ws) | 10 | 100 | 300 | 1000 |
|---|---|---|---|---|
| time / allocs | 191 us / 124 | 786 us / 1.5k | 2.9 ms / 4.5k | 12.2 ms / 15k |

Connect is O(1) in M; peek and List are O(M) with no LIMIT; registry is
O(records) (append-only JSONL, re-parsed every overview request).

### 5. Prompt (256) / gitignore (128) caches with N > capacity

Both evict an arbitrary map entry when full (not LRU). Simulated round-robin
cycling over N workspaces (20 rounds): cap 128 -> hit 95% (N=100), 12%
(N=300), 0.1% (N=1000); cap 256 -> 95% / 69% / 2%. The prompt cache also
has a 5 s TTL, so a user switching workspaces slower than every 5 s gets
~0% hits regardless of size; capacity only matters for the gitignore
cache (stat-revalidated, no TTL).

### 6. Replica sync (per workspace, `internal/replica` benches, `CRUSH_BENCH_REPLICA_SESSIONS`)

| sessions (x10 msgs) | FullSync | wire | ServeDelta (no changes) |
|---|---|---|---|
| 200 | 430 ms, 31 MB alloc | 0.86 MB | 0.48 ms |
| 2000 | 4.53 s, 328 MB | 8.6 MB | 2.9 ms |
| 5000 | 11.7 s, 822 MB | 21.6 MB | 7.0 ms |

~2.3 ms/session full sync: 100 workspaces x 2000 sessions would be ~7.5 min
of CPU per new replica. The no-op delta poll is O(M) (~1.4 us/session), so
idle polling across 100x2000 costs ~0.3 s CPU per round (attempt 36:
no-op delta is now O(changes), ~0.18 ms flat at 200-5000 sessions;
full sync paging still open). A 3-node
in-process run at 100x2000 was not done (time budget).

### 7. Session list for one workspace (5000 sessions)

Attached `session.Service.List` 57 ms / 12 MiB; the sidebar also receives
every other workspace's sessions through the overview, so the picker's
cost is dominated by item 1, not by its own workspace.

### O(workspaces) / O(sessions) on the TUI launch path

1. `internal/ui/model/sessionsidebar_wiring.go:50` -> `client.ListWorkspaceOverviews` -> `backend.listAllOverviews` (`internal/backend/overview.go:42`): returns every session of every workspace (O(N*M)), blocks "ready".
2. `backend.peekOverviews` (`overview.go:122`): one goroutine per workspace, unbounded; at N=300 peeks hit the 3 s stall guard and workspaces are reported unavailable.
3. `sortSessionOverviews` (`overview.go:195`): re-sorts cached peeks per request.
4. `server.jsonEncode` of the whole slice + client `json.Unmarshal` (~1 s / 67 MB at 100x5000).
5. `registry.Store.List` (`internal/registry/registry.go:96`): re-reads and dedups the JSONL each request, O(records).
6. `db.PeekSessions` / `session.Service.List`: no LIMIT, O(M).
7. `backend.peekCache` (`peek_cache.go`): unbounded, holds every detached workspace's full listing (RSS 550-830 MB at 100-300 x 5000).

### Status after attempt 33

Fix 1 landed in bounded form: the TUI requests `limit=50` per workspace
(`session_count` carries totals); ready at 100x2000 / 100x5000 / 300x2000
dropped 1196 / 2696 / 3527 ms -> 341 / 571 / 620 ms. 300x5000 is now bound
by fix 2 (peek fan-out, 3.1 s server list, ~200 ws unavailable). Fix 3
(SQL LIMIT + index) and bounding the peer `local=1` fetch remain open; the
peer fetch must stay full while device-session sync reads RemoteOverviews.

### Status after attempt 34

Fixes 2, 4 (sorted-cache half) and 5 landed: peeks run in a
max(4, GOMAXPROCS) pool and fall back to the last listing marked stale;
sorted overviews are cached per dbStamp; the registry is cached by file
stat. Ready 100x5000 / 300x2000 / 300x5000: 1151 / 651 / 4005 ->
370 / 399 / 1656 ms, server list_overviews at 300x5000 3159 -> 124 ms,
unavailable 215 -> 0. Remaining at 300x5000: the cold first peek of all
DBs (fix 3, SQL LIMIT), splitByOwner copying every session per request,
JSON encode of the full (peer/mobile) response, and the unbounded peek
cache (fix 7).

### Status after attempt 35

Fix 7 partial: prompt-input and gitignore caches are LRU with cap 1024;
300-workspace cycling hit rate 72.8% -> 100% (prompt) and gitignore read
906 -> 176 us. The 5 s prompt TTL stays (fingerprints miss unstaged and
nested-skill edits). Still open: bounding the backend peek cache.

### Ranked fix list

1. **Paginate/limit the overview**: per-workspace top-K sessions (busy, unread, favorites + newest K, e.g. 50) plus a total count; fetch more on expand/scroll. Cuts payload ~100x at M=5000 and removes the O(N*M) from ready. Highest impact.
2. **Bound peek concurrency** (worker pool ~GOMAXPROCS) and serve stale cached results instead of marking unavailable; fixes the 300x5000 correctness bug.
3. **Push LIMIT/ORDER into SQL** for peek and sidebar list (index on `sessions(parent_session_id, archived_at, updated_at DESC)`), so per-workspace cost is O(K) not O(M).
4. **Cache the sorted, encoded overview per workspace** (keyed by dbStamp) and assemble incrementally; skip re-sort/re-encode for unchanged workspaces.
5. **Compact/cached registry**: keep the deduped registry in memory, revalidate by file stat; compact on write.
6. **Replica delta**: use a seq/updated index so the no-op poll is O(changes); stream full sync in pages.
7. **LRU-size caches**: make the peek cache an LRU bounded by sessions held; switch prompt/gitignore caches to LRU (and size gitignore >= registry count), noting prompt TTL dominates.
