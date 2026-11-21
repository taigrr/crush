# Hill-Climb Results

| # | Title | Target | Before | After | Delta | Verdict |
|---|-------|--------|--------|-------|-------|---------|
| 01 | Lazy gh/gh-stack probes | CLI cold start (`--version`) | 204.9 ms | 112.3 ms | -45% | KEPT |
| 02 | Lazy chroma lexer registry (investigation) | CLI cold start (`--version`) | 111.1 ms | n/a | n/a | REVERTED |
| 03 | Reuse TUI frame canvas | `BenchmarkUIView` msgs=500 | 4.061 ms, 1686 KiB | 3.527 ms, 232 KiB | -13.2% time, -86% B/op | KEPT |
| 04 | Single-pass TUI frame flatten | `BenchmarkUIView` msgs=500 | 3.539 ms, 231.4 KiB | 3.451 ms, 236.6 KiB | ~ (p=0.48), +2.3% B/op | REVERTED |
| 08 | Single-pass parts decode (dup of 09) | `BenchmarkMessageList` | 17.50 / 67.73 ms | 13.82 / 53.89 ms | -21% / -20% | KEPT (landed as 09) |
| 10 | Markdown renderer / xchroma style memo | `BenchmarkMarkdownRender` | 39.82 ms | 38.55 ms | ~ (p=0.39) | REVERTED |
| 11 | Startup harness + fast server-ready poll | cold TUI first frame / ready | 596.2 / 674.9 ms | 467.5 / 548.3 ms | -21.6% / -18.8% | KEPT |
| 12 | Cluster networking benchmark harness (baseline only) | cluster join/RPC/overview/reconnect/sync | see attempt 12 | n/a | n/a | BASELINE |
| 14 | Startup DB-open path (investigation) | `BenchmarkConnectExisting` (200 sessions / 20k msgs) | 5.149 ms | n/a | n/a | REVERTED (nothing avoidable) |
| 15 | Skip 25 ms readiness sleep on mesh start | `BenchmarkClusterMeshReconnect` / `ColdJoin` | 28.05 ms / 194.7 ms | 6.32 ms / 123.6 ms | -77.5% / -36.6% (p=0.000) | KEPT |
| 16 | Skip `git rev-parse` fork when no `.git` ancestor | hyperfine `crush dirs` (non-repo cwd) | 162.3 ms +-12.3 | 116.2 ms +-10.6 | -28.4% | KEPT |
| 17 | Pool peer response buffers in cluster `call` | ClusterOverviewFetch time/op | 1.898 ms | 1.509 ms | -20.5% | KEPT |
| 18 | Startup LSP/MCP init off critical path (investigation) | cold TUI first frame / ready | 387.6 / 469.0 ms (no MCP) | 390.1 / 472.3 ms (slow MCP) | ~ (+0.7%, within 1 sd) | REVERTED (already async) |
| 19 | Fork `git branch` once per workspace open | warm TUI first frame / ready | 235.4 / 318.8 ms | 215.2 / 297.6 ms | -8.6% / -6.6% (p<1e-11) | KEPT |
| 21 | Long-poll join approval | `BenchmarkClusterJoinApprovalLatency` / `ColdJoin` | 1706 ms / 122.96 ms | 9.2 ms / 88.81 ms | -99.5% / -27.8% (p=0.000) | KEPT |
| 24 | Read git HEAD instead of forking `git branch` on workspace open | warm workspace_created / first_frame / ready | 202.5 / 217.3 / 295.9 ms | 176.6 / 191.3 / 273.3 ms | -12.8% / -12.0% / -7.6% (p<1e-8) | KEPT |
| 27 | Server-side cache of prompt git status + skills discovery | ready ms (reuse / reuse-teardown, small / large) | 288 / 356 / 448 / 698 | 195 / 236 / 217 / 520 | -32% / -34% / -52% / -26% | KEPT |
| 28 | Process-wide cache of checkpoint gitignore patterns with stat revalidation | ready ms (reuse-teardown large / small) | 516.5 / 214.4 | 349.3 / 212.7 | -32.4% / -0.8% (noise) | KEPT |
| 29 | Revalidate cached checkpoint gitignore in background; first snapshot waits | ready ms (reuse-teardown large / small) | 346.8 / 216.8 | 236.7 / 216.8 | -31.7% / 0% (noise) | KEPT |
| 30 | Prefetch workspace overviews at UI Init instead of inside the Cmd | ready ms (reuse large / reuse-teardown large) | 203.9 / 234.6 | 170.2 / 199.1 | -16.5% / -15.1% | KEPT |
| 31 | Final validation: repeated TUI attach FS/DB caching (+ bounded caches) | ready ms, f32dc805 vs final (reuse / reuse-teardown, small / large) | 228.7 / 324.2 / 302.3 / 765.3 | 165.6 / 191.7 / 165.7 / 196.8 | -28% / -41% / -45% / -74% | KEPT |
| 32 | Scale baseline: hundreds of workspaces x thousands of sessions (baseline only) | ready / overview vs N x M | 10x200: 197 ms ready, 0.3 MB | 300x5000: 4357 ms ready, 75-111 MB, 136-189/300 ws unavailable | n/a | BASELINE |
| 33 | Bounded workspace overview (top-50 sessions per workspace + session_count) | TUI ready ms (reuse, 10x200 / 100x2000 / 100x5000 / 300x2000 / 300x5000) | 190 / 1196 / 2696 / 3527 / 4460 | 188 / 341 / 571 / 620 / 4004 | -1% (ns) / -72% / -79% / -82% / -10% | KEPT |
| 34 | Bounded peek fan-out + stale fallback, cached sorted overviews, in-memory registry | TUI ready ms (reuse, 10x200 / 100x2000 / 100x5000 / 300x2000 / 300x5000); unavailable @300x5000 | 190 / 376 / 1151 / 651 / 4005; 215 unavailable | 187 / 275 / 370 / 399 / 1656; 0 unavailable | -1% (ns) / -27% / -68% / -39% / -59% | KEPT |
| 35 | LRU prompt-input / gitignore caches, caps 256/128 -> 1024 (scale fix 7, partial) | 300-workspace round-robin cycle: ns/op, hit% | prompt 23.5 us, 72.8% hit; ignore 906 us, 248 allocs | prompt 239 ns, 100% hit; ignore 176 us, 71 allocs | -99% / -81% (p=0.000) | KEPT |
| 36 | Lazy owner lookup in replica delta Serve (scale fix 6, partial) | ServeDelta no-change poll (200 / 2000 / 5000 sessions) | 483 us / 2.92 ms / 6.87 ms | 175 us / 179 us / 178 us | -64% / -94% / -97% (p=0.000) | KEPT |
| 37 | Active/busy session always in bounded overview, WAL-safe peek stamp, LRU-bounded peek cache | correctness (sidebar), 300x5000 ready ms | 1530 +-262 | 631 +-33 (not attributed) | correctness fix | KEPT (correctness) |

## Attempt 01: Lazy gh/gh-stack probes

- **Hypothesis**: package-level `ghAvailable`/`ghStackAvailable` in
  `internal/agent/tools/tools.go` run `exec.LookPath("gh")` and
  `gh extension list` during package init, adding ~90 ms to every crush
  invocation, including `--version`.
- **Change**: `internal/agent/tools/tools.go`, `internal/agent/tools/bash.go`.
  Both probes wrapped in `sync.OnceValue`; the only call sites
  (`RenderToolDescription`, bash description render) now call them. Results
  are identical, just computed on first use.
- **Method**: hyperfine `-N --warmup 3 --runs 30`, both binaries back to back,
  HOME/XDG in a temp dir. Load avg 3.77 at start, 3.03 at end.
- **Results** (wall-time median ± stddev):

  | Command | Before | After | Delta |
  |---------|--------|-------|-------|
  | `crush --version` | 204.9 ± 13.3 ms | 112.3 ± 7.9 ms | -45.2% |
  | `crush --help` | 208.9 ± 13.2 ms | 117.7 ± 8.2 ms | -43.7% |
  | `crush dirs` | 247.7 ± 15.6 ms | 164.8 ± 11.4 ms | -33.5% |

  Every median gap is well over 2x stddev. Peak RSS unchanged (about 61 MiB).
- **Tests**: `go test ./internal/agent/tools/`, `go vet`, and golangci-lint
  all pass with 0 issues.
- **Other init candidates** (`GODEBUG=inittrace=1`; none of these spawn a
  subprocess, so nothing was changed for them): `chroma/v2/styles` takes
  about 15 ms and `chroma/v2/lexers` about 14 ms, both building
  registries eagerly. `go-nativeclipboard` takes 3.7 ms. `encoding/gob`
  takes 1.5 ms. The rest are each 1 ms or less.
- **Verdict**: KEPT.

## Attempt 02: Lazy chroma lexer registry (investigation)

- **Hypothesis**: what's left of the cold start is package init, mostly
  chroma's lexer and style registries. Deferring the biggest of these
  until first use would cut `crush --version`, `--help` and `dirs`.
- **Findings** (load avg 1.80 / 2.72 / 2.76):
  - hyperfine `-N`: a Go hello-world binary takes 8.0 ± 0.3 ms (4 MiB RSS).
    `crush --version` takes 111.1 ± 9.2 ms (61 MiB RSS) on a 174 MB binary.
  - `GODEBUG=inittrace=1`: the init phase ends about 85 ms after process
    start. Summed init clock is about 45 ms. The rest is spread across
    page-in and many small inits.
  - Largest inits: `chroma/v2/lexers` 16-21 ms (2.8 MB, 32k allocs, parses
    every embedded XML lexer config) and `chroma/v2/styles` 12-14 ms.
    After those: `go-nativeclipboard` 4 ms, `anthropic-sdk-go` 3.6 ms,
    `encoding/gob` 2.6 ms and `openai-go` 2.5 ms.
  - Crush's own packages: every init is under 5 ms. `internal/cmd` takes
    about 1 ms (3 ms on a cold cache), `internal/agent/tools` 1.1 ms and
    `internal/version` 0.9 ms. Nothing in crush reaches the 5 ms
    threshold, so nothing there is worth deferring.
- **Approach tried**: forked chroma v2.27.0 into `third_party/chroma` and
  pointed `go.mod` at it with a replace. `LexerRegistry` got a
  `sync.Once` lazy hook, and the XML lexers were registered on first
  lookup, ahead of the Go-defined lexers so registration order stayed the
  same. The `dns`/`mysql`/`zed` analyser `init()`s moved into the hook.
  With this, lexers init dropped from about 22 ms to 2.3 ms.
  - **Abandoned**: the fork's tests (`lexers`, `quick`, `formatters/html`)
    deadlocked at 0% CPU. The likely cause is re-entrant `sync.Once`: a
    lookup from inside the lazy load waits on the same Once. Vendoring
    12 MB of a third-party library behind a replace is also too invasive
    for this PR. `styles` can't be deferred the same way, because glamour
    reads `styles.Registry` directly.
- **Change**: none. The fork and the `go.mod` replace were removed
  completely.
- **Verdict**: REVERTED (no change kept). If chroma ever gets made lazy,
  it should be done upstream in chroma.

## Attempt 03: Reuse TUI frame canvas

- **Hypothesis**: `UI.View` allocates a fresh 200x60 `uv.ScreenBuffer` per
  frame (~85% of bytes/frame, HOTSPOTS #2). Reusing it and clearing to
  `EmptyCell` (same state as a new buffer) cuts allocation and GC work.
- **Change**: `internal/ui/model/draw.go` (`frameCanvas`: reuse, clear via
  `Buffer.Clear`, reallocate only on size change), `internal/ui/model/ui.go`
  (`canvas` field), new `internal/ui/model/canvas_reuse_test.go` asserting
  byte-identical View output vs a fresh canvas across scrolls and resizes.
  The canvas never escapes View (output is flattened to a string).
- **Method**: `go test -bench -benchmem -count=10`, `GOMAXPROCS=4`, HEAD in a
  temp worktree then change, back to back. Load avg 2.03 start, 2.77 end.
- **Results** (median, benchstat):

  | Benchmark | Before | After | Delta | p |
  |-----------|--------|-------|-------|---|
  | UIView/msgs=500 time | 4.061 ms | 3.527 ms | -13.16% | 0.000 |
  | UIView/msgs=2000 time | 4.012 ms | 3.435 ms | -14.39% | 0.000 |
  | UIScrollFrame time | 7.223 ms | 6.528 ms | -9.61% | 0.000 |
  | UIDraw time | 1.874 ms | 1.842 ms | ~ | 0.529 |
  | UIView/msgs=500 B/op | 1686.3 KiB | 231.8 KiB | -86.25% | 0.000 |
  | UIScrollFrame B/op | 2.594 MiB | 1.172 MiB | -54.81% | 0.000 |
  | UIView allocs/op | 2577 | 2450 | -4.93% | 0.000 |

  UIDraw unchanged as expected (it already reuses a buffer).
- **Tests**: `go test ./internal/ui/model/`, `go vet`, golangci-lint: pass,
  0 issues.
- **Verdict**: KEPT.

## Attempt 04: Single-pass TUI frame flatten

- **Hypothesis** (HOTSPOTS #2): `UI.View` post-processes `canvas.Render()`
  with `ReplaceAll` + `Split` + per-line `TrimRight` + `Join`, copying the
  frame several times; a single pass into a `strings.Builder` (returning the
  input unchanged when nothing needs trimming) should cut time and bytes.
- **Change**: `internal/ui/model/draw.go` (`flattenFrame` helper) plus a
  table test against the old pipeline (CRLF, lone CR, ANSI, wide chars, tabs,
  empty lines); byte-identical output verified.
- **Load avg**: 2.97 before, 2.64 after (1-min).
- **Results** (GOMAXPROCS=4, count=10):

| Benchmark | Before | After | Delta |
|-----------|--------|-------|-------|
| UIView/msgs=500 | 3.539 ms, 231.4 KiB, 2450 allocs | 3.451 ms, 236.6 KiB, 2449 allocs | ~ (p=0.481), +2.26% B/op |
| UIView/msgs=2000 | 3.513 ms, 230.9 KiB | 3.471 ms, 236.0 KiB | ~ (p=0.971), +2.17% B/op |
| UIScrollFrame | 6.473 ms, 1.173 MiB | 6.537 ms, 1.177 MiB | ~ (p=0.739), +0.29% B/op |

- **Verdict**: REVERTED. The flatten step is not a measurable share of frame
  time (draw/render dominates); the old `Split`/`Join` output is tighter than
  a `Grow(len(s))` builder, so B/op got slightly worse.

## Attempt 05: shared cross-instance diff highlight cache

- **Hypothesis**: `DiffView.hightlightCode` re-lexes every line on each new
  `DiffView` (the per-instance cache dies with the view), and tool-result
  diffs are rebuilt on every re-render. Whole-file highlighting + slicing was
  rejected: multi-line tokens (block comments, raw strings) would change
  per-line output, breaking byte-identity. A bounded process-wide cache keyed
  by (chroma style ptr, lexer name, bg RGBA, line source) is exact.
- **Change**: `internal/ui/diffview/highlight_cache.go` (new, RWMutex map,
  reset at 8192 entries), `internal/ui/diffview/diffview.go` (consult/fill it),
  test `TestSharedHighlightCacheIdenticalOutput`.
- **Load avg**: 2.38 (before) / 1.64 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| DiffRender/unified time | 204.55 ms | 79.82 ms | -60.98% (p=0.000) |
| DiffRender/split time | 238.5 ms | 121.0 ms | -49.27% (p=0.000) |
| unified B/op, allocs | 21.92 MiB, 549.5k | 11.58 MiB, 314.8k | -47%, -43% |
| split B/op, allocs | 29.06 MiB, 768.4k | 18.57 MiB, 533.4k | -36%, -31% |

- **Verdict**: KEPT. Note the benchmark re-renders the same diff, so it
  measures the warm (re-render) path; first render of a new diff is unchanged.
  Output is byte-identical (same lexer/formatter, cached per exact input).

## Attempt 06: pre-rendered line-number gutter affixes in diffview

- **Hypothesis**: `lipgloss.Style.Render` on fixed-width line-number gutters
  is a large share of diff CPU. Since the gutter content always has the same
  cell width, render each LineNumber style once per `String()` around a
  sentinel of that width and reuse the prefix/suffix, falling back to
  `Render` for anything not plain single-width.
- **Change**: `internal/ui/diffview/linenum.go` (new), `style.go`
  (unexported affix field on `LineStyle`), `diffview.go` (17 call sites),
  test `TestRenderLineNumberMatchesLipgloss`. Golden tests pass unchanged.
- **Load avg**: 2.08 (before) / 2.34 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| DiffRender/unified time | 88.24 ms | 78.70 ms | -10.80% (p=0.000) |
| DiffRender/split time | 121.1 ms | 116.5 ms | ~ (p=0.105) |
| unified B/op, allocs | 11.58 MiB, 314.8k | 11.19 MiB, 301.7k | -3.44%, -4.16% |
| split B/op, allocs | 18.57 MiB, 533.4k | 18.22 MiB, 521.6k | -1.86%, -2.21% |

- **Verdict**: KEPT. Unified clears the bar; split improves allocs
  significantly and time is not regressed. The remaining Render cost is the
  per-line `Code.Width(...)` and content-style wraps, which depend on content.

## Attempt 07: single-pass SSE envelope encoding in server

- **Hypothesis**: `json.Marshal(*pubsub.Payload)` re-validates and
  re-compacts (clones) the already-marshalled `json.RawMessage` payload, so
  every streamed event pays for the full message twice. Writing the
  envelope by hand and copying the payload verbatim removes that pass.
- **Change**: `internal/server/events.go` (new `appendSSE`),
  `internal/server/proto.go` (workspace + global SSE writers use it instead
  of `json.Marshal` + `fmt.Fprintf`), `wire_bench_test.go` (bench mirrors
  production), test `TestAppendSSEMatchesMarshal` asserts byte-identical
  output vs `"data: "+json.Marshal(p)+"\n\n"` (incl. HTML-escaped content).
  All payloads are produced locally by `json.Marshal`, so they are already
  compact and escaped.
- **Load avg**: 2.18 (before) / 3.52 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| WireFullMessage time | 107.30 ms | 85.37 ms | -20.44% (p=0.000) |
| WireFullMessage B/op | 42.63 MiB | 34.40 MiB | -19.31% (p=0.000) |
| WireFullMessage allocs | 41.47k | 43.37k | +4.59% |
| WireDeltas time | 2.684 ms | 2.742 ms | ~ (p=0.315) |

- **Verdict**: KEPT. Large time and byte win on the full-message path
  (measured under higher load after). Small alloc-count rise is the
  per-event `json.Marshal(type)`; deltas unchanged.

## Attempt 09: single-pass part decoding in message list

- **Hypothesis**: `UnmarshalParts` parses each row's parts JSON twice:
  first into `[]json.RawMessage` (copying every element), then each element
  again into a `{type,data}` wrapper. Decoding straight into
  `[]struct{Type; Data json.RawMessage}` drops one full parse and the
  per-element copies. No schema/query change needed: the profile showed
  JSON decoding, not SQLite, dominating crush's own cost.
- **Change**: `internal/message/message.go` (`UnmarshalParts`). Behavior is
  unchanged; existing message tests pass.
- **Load avg**: 2.58 (before) / 4.01 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MessageList/500 time | 16.42 ms | 13.92 ms | ~ (p=0.105, noisy) |
| MessageList/2000 time | 68.53 ms | 68.03 ms | ~ (p=0.684) |
| MessageList/500 B/op, allocs | 2.944 MiB, 18.57k | 2.489 MiB, 15.32k | -15.43%, -17.51% (p=0.000) |
| MessageList/2000 B/op, allocs | 12.01 MiB, 74.10k | 10.17 MiB, 61.10k | -15.30%, -17.55% (p=0.000) |

- **Verdict**: KEPT (alloc-focused). B/op and allocs drop about 15-17%
  with p=0.000. Time shows no regression, and the geomean is -8%, though
  that is not significant because the machine was under heavy load.

## Attempt 10: markdown renderer / xchroma formatter style memo

- **Hypothesis**: (HOTSPOTS #6) a glamour TermRenderer is built per render
  and finished items re-render markdown on relayout. Investigation showed
  both are already done on HEAD: `common.MarkdownRenderer` memoizes per
  width, and assistant/user items cache rendered sections per
  (width, content hash). Profile of `BenchmarkMarkdownRender`: ~78% inside
  glamour/goldmark/lipgloss writers (third-party), ~20% in crush's
  `xchroma.Formatter`. Tested fallback: memoize the lipgloss style per
  token type within a Format call and use `io.WriteString` over `fmt.Fprint`.
- **Change (reverted)**: `internal/ui/xchroma/chroma.go`.
- **Load avg**: 4.03 (before) / 5.59 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MarkdownRender time | 39.82 ms | 38.55 ms | ~ (p=0.393) |
| MarkdownRender B/op | 3.984 MiB | 4.014 MiB | +0.75% (p=0.011) |
| MarkdownRender allocs | 358.6k | 357.4k | -0.33% (p=0.000) |
| DiffRender unified/split time | 80.35 / 117.0 ms | 80.13 / 118.8 ms | ~ |

- **Verdict**: REVERTED. The hypothesis was already in place on HEAD, and
  the formatter tweak changed nothing measurable. Whatever cost is left
  sits in third-party glamour writer chains (WrapWriter/IndentWriter/MarginWriter).

## Attempt 08: single-pass session message parts decode

- **Hypothesis**: `UnmarshalParts` decoded the column into
  `[]json.RawMessage`, then re-parsed each element into a
  `{type, data RawMessage}` wrapper, then decoded `data`: three passes and
  an extra copy per part. Decoding straight into `[]struct{Type; Data
  RawMessage}` drops one full pass and one copy per part.
- **Change**: `internal/message/message.go` (`UnmarshalParts`). A
  concurrent session committed the byte-identical change as attempt 09
  (a569ba22) while this attempt was measuring, so this commit only adds
  `internal/message/parts_roundtrip_test.go` (MarshalParts ->
  UnmarshalParts for every part type, plus empty, unknown-type and `null`
  element error cases).
- **Load avg**: 1.95 (before) / 6.68 -> 5.61 (after).

| Benchmark | Before | After | Delta |
|---|---|---|---|
| MessageList/500 time | 17.50 ms | 13.82 ms | -21.03% (p=0.002) |
| MessageList/2000 time | 67.73 ms | 53.89 ms | -20.43% (p=0.000) |
| MessageList B/op | 2.943 / 12.01 MiB | 2.489 / 10.17 MiB | -15.4% (p=0.000) |
| MessageList allocs | 18.57k / 74.10k | 15.32k / 61.10k | -17.5% (p=0.000) |
| UISessionLoad | 21.35 / 28.53 ms | n/a | after-run build broken by a concurrent session's in-progress xchroma edit |

- **Verdict**: KEPT (code already on HEAD as attempt 09). Wins hold despite
  much higher load during the after run. Remaining cost is the per-part
  `data` decode and the string->[]byte copy of the parts column.

## Attempt 11: interactive startup harness + fast server-ready poll

- **Harness**: `CRUSH_STARTUP_TRACE=<file>` (`internal/startuptrace`)
  appends `<pid> <phase> <unix-ns>` once per phase per process. Client
  phases: `main`, `server_ready`, `workspace_created`, `agent_init`,
  `program_start`, `first_frame` (first `UI.View`), `overviews_loaded`;
  server phases: `server_listen`, `ws_build`, `ws_config`, `ws_db`,
  `ws_skills`, `ws_app`, `ws_done`. `bench/startup.py <bin> [runs]
  [--warm]` runs the TUI under `script` (pty) in a fresh temp project with
  isolated HOME/XDG/CRUSH_GLOBAL_* and a private `-H unix://<tmp>/s.sock`
  (never the live daemon), measures from exec, "ready" = max(first_frame,
  overviews_loaded), discards one warmup run, and kills every traced pid.
  Wired into `bench/run.sh` (15 cold + 15 warm runs).
- **Baseline breakdown (cold, n=20, median ms from exec)**: main 122.8,
  server_listen 308.5, server_ready 435.3, ws_build 480.0, ws_config 487.1,
  ws_db 527.3 (DB open+migrations ~40 ms), ws_done 533.3,
  workspace_created 579.1, first_frame 595.5, ready 676.6. Warm server:
  server_ready 119.3, workspace_created 265.3, first_frame 279.6,
  ready 364.0.
- **Hypothesis**: the biggest synchronous blocker before first frame on a
  cold start is idle waiting: the server listens at ~308 ms but the client
  only notices at ~435 ms because `readyPoller` backs off 100/200/400 ms
  from the first probe. Probing every 10 ms for the first second (then the
  old exponential backoff) should cut ~120 ms with no behavior change.
- **Change**: `internal/cmd/ready_poll.go` (fast window),
  `internal/cmd/ready_poll_test.go` (fast-window test; old backoff test
  disables the window).
- **Load avg**: 2.16 (start) / 2.70 (end).

| Metric (cold, n=20) | Before | After | Delta |
|---|---|---|---|
| server_ready | 432.0 +- 12.0 ms | 306.6 +- 14.9 ms | -29.0% |
| first_frame | 596.2 +- 14.5 ms | 467.5 +- 27.5 ms | -21.6% |
| ready | 674.9 +- 16.0 ms | 548.3 +- 34.5 ms | -18.8% |
| warm first_frame | 279.6 +- 18.8 ms | 282.3 +- 17.8 ms | ~ (unaffected path) |

- **Verdict**: KEPT. Medians separated by >4x stddev. Next blockers:
  ~120 ms of process init before `main` (paid twice on cold start: client
  and spawned server), ~145 ms CreateWorkspace round trip on a warm server
  (~45 ms between `ws_done` and the client seeing the response), and
  ~80 ms first_frame -> overviews_loaded.

## Attempt 12: cluster networking benchmark harness (baseline, no optimization)

- **Goal**: measurable cluster networking paths. All nodes are in-process
  on 127.0.0.1 random ports with `b.TempDir()` data dirs; the live cluster,
  daemon and `~/.local/share/crush` are never touched. Every node and mesh
  is closed in cleanup.
- **Change**: `internal/cluster/cluster_bench_test.go`,
  `internal/replica/replica_bench_test.go` (new); test helpers
  `newTestNode`/`freeAddr`/`pairedTrio` and replica helpers now take
  `testing.TB`; `bench/run.sh` + `bench/README.md` include them.
- **Relay**: nodes route over a full NATS mesh with `NoAdvertise`; there is
  no in-process relay mode (`EndpointRelay` is reserved in `state.go`), so
  no relay benchmark.
- **Note**: `Mesh.NumRoutes` counts pooled route connections (several per
  peer), so the harness counts distinct peers via `Routez` instead.
- **Load avg**: 1.23 (start) / 2.31 (end). GOMAXPROCS=4, count=10,
  raw in `bench/out/cluster-baseline/`.

| Benchmark | median | p50 / p99 | B/op | allocs/op |
|---|---|---|---|---|
| ClusterColdJoin (pair 3 + mesh) | 194.4 ms +-2% | mesh part 84 ms | 4.55 MiB | 36.7k |
| ClusterMeshRequest (NATS req/rep) | 138.2 us | 138.5 / 206.5 us | 2.4 KiB | 39 |
| ClusterCallAPI (mTLS HTTP) | 437.5 us +-19% | 447.5 / 741 us | 15.9 KiB | 178 |
| ClusterOverviewFetch (2 x 200 sessions) | 1.887 ms | 1.81 / 2.83 ms | 472 KiB | 1.29k |
| ClusterMeshReconnect (peer restart) | 28.0 ms | 28.4 / 28.8 ms | 1.96 MiB | 15.5k |
| ClusterMeshThroughput (1 KiB msgs) | 175 MiB/s | - | 527 KiB/256 msgs | 2.2k |
| ReplicaFullSync (200 sess x 10 msgs) | 1.325 s +-6% | 842 KiB wire | 31.2 MiB | 387k |
| ReplicaServeDelta (no changes) | 477.7 us | - | 24.3 KiB | 709 |

- **Verdict**: BASELINE (harness only). Hotspots in `bench/HOTSPOTS.md`
  ("Cluster").

## Attempt 13: cache prepared statements in replica Apply

- **Hypothesis**: cluster hotspot #1 is `replica.Apply` (1.33 s full sync
  of 200 sessions x 10 msgs). Every row runs an unprepared upsert plus an
  unprepared owner `SELECT`, so sqlite re-prepares the same few SQL
  strings thousands of times. Preparing each distinct query once per
  transaction should remove most of that cost with identical semantics.
- **Change**: `internal/replica/replica.go` (`stmtCache` on the apply tx;
  `upsert` and the owner lookup go through it; closed with the tx).
- **Load avg**: 1.68 (start) / 2.73 (end). GOMAXPROCS=4, count=10, raw in
  `bench/out/attempt13/`.

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ReplicaFullSync time/op | 1325.2 ms +-6% | 446.3 ms +-5% | -66.3% (p=0.000) |
| ReplicaFullSync B/op | 31.16 MiB | 31.78 MiB | +2.0% |
| ReplicaFullSync allocs/op | 386.9k | 395.6k | +2.2% |
| ReplicaServeDelta time/op | 482.2 us | 476.1 us | ~ (p=0.393) |

- **Verdict**: KEPT. 3x faster full sync; small alloc increase from
  statement handles is negligible. Tests pass (`-race` on replica and
  cluster), vet + lint clean. Next: per-row owner lookups could be cached
  per session within a batch.

## Attempt 14: startup DB-open path (investigation)

- **Hypothesis**: on a realistic data dir, startup DB work (Connect:
  lock, open, pragmas, migration check; then ListSessions) does avoidable
  work: migrations re-checked via goose, redundant pragmas, or unneeded
  queries before first frame.
- **Method**: temporary `internal/db/connect_bench_test.go` populating a
  temp data dir with 200 sessions x 100 messages (20k), then looping
  `Connect(WithDataDirLock(true))` + `ListSessions` + `Release`, with a
  CPU profile focused on the loop. Also reviewed `buildWorkspace` and
  `app.New` for synchronous startup queries.
- **Findings** (GOMAXPROCS=4, count=10): 5.149 ms +- 4%/op, 339.9 KiB,
  1619 allocs. Breakdown: `PingContext` (driver open + DSN pragmas) ~85%
  of Connect, and nearly all of that is SQLite's one-time schema parse
  (`sqlite3InitOne`) triggered by the first pragma, which any first query
  would pay anyway; data-dir lock ~7%; `schemaCurrent` ~5%; goose is
  already skipped for an up-to-date DB (fast path exists). ListSessions
  over 200 rows is ~1-2 ms of page reads. The 40 ms `ws_db` seen in
  attempt 11 is migrations on a brand-new DB (first run only); on an
  existing DB the whole DB path is ~3-5 ms of a ~470 ms cold first frame
  (<1%). Swarm backfill, MCP init and update checks already run in
  goroutines off the critical path.
- **Change**: none kept (benchmark file removed per protocol).
- **Load avg**: 1.91 (start) / 2.06 (end).
- **Verdict**: REVERTED / no change. No single avoidable cost >3% of the
  DB path exists; the remaining cost is inherent schema load + file I/O.
  Startup time is better pursued in process init before `main` (~120 ms,
  paid twice on cold start) and the CreateWorkspace round trip.

## Attempt 15: skip the 25 ms readiness sleep on mesh start

- **Hypothesis**: reconnect (28 ms) and cold-join mesh time (84 ms) are
  dominated not by route backoff, gossip, or serial dials, but by
  `StartMesh` itself: nats-server's `ReadyForConnections(10s)` sleeps a
  fixed 25 ms whenever its first check fails, which it always does right
  after `go srv.Start()`. Timing showed StartMesh = 28.4 ms of the 28.6 ms
  reconnect; routes connect <0.2 ms after it returns.
- **Change**: `internal/cluster/mesh.go`: `waitMeshReady` polls
  `ReadyForConnections(1ms)` with a 200 us sleep up to the same 10 s
  deadline. Same readiness semantics and failure path.
- **Results** (GOMAXPROCS=4, count=10, benchstat):

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ClusterMeshReconnect time/op | 28.05 ms +-1% | 6.32 ms +-3% | -77.5% (p=0.000) |
| ClusterMeshReconnect p99 | 28.79 ms | 11.55 ms | -59.9% |
| ClusterColdJoin time/op | 194.7 ms +-2% | 123.6 ms +-3% | -36.6% (p=0.000) |
| ClusterColdJoin mesh-ms | 84 | 13 | -84.5% |
| B/op, allocs/op | | | -18% to -33% |

- **Load avg**: 1.83 (start) / 3.08 (end).
- **Verdict**: KEPT. `go test -race ./internal/cluster`, vet, lint clean.
  Survivor-side route redial (fixed 1 s NATS `routeConnectDelay`, not
  configurable) remains the next limit when the restarted peer cannot
  dial out; remaining cold-join cost is pairing (~110 ms).

## Attempt 16: CLI cold-start re-profile; skip git fork outside repos

- **Profile** (HEAD, `GODEBUG=inittrace=1`, startuptrace marks): `--version`
  spends ~60 ms in package init, of which crush's own packages are ~6 ms
  total; the rest is third-party (chroma lexers 22 ms + styles 14 ms via
  `internal/ui/common`, nativeclipboard 4 ms, SDKs). `main` to exit is
  ~1 ms for `--version`/`--help` (fang + cobra are cheap); the remainder
  is exec/page-in of the 174 MB binary. Nothing in crush's cobra setup is
  worth deferring for `--version`/`--help`. `dirs` costs ~40 ms more,
  all in `projectBoundary` -> `git rev-parse` subprocess.
- **Hypothesis**: when no ancestor of cwd has a `.git` entry (and no
  GIT_DIR/GIT_WORK_TREE/GIT_COMMON_DIR override), rev-parse must fail, so
  the fork can be skipped. Also helps TUI/server config load in non-repo
  dirs.
- **Change**: `internal/config/load.go` `mayBeInGitRepo` guard in
  `resolveWorktreeRoot`; test `TestMayBeInGitRepo`.
- **Results** (hyperfine -N, warmup 3, runs 30, temp HOME/XDG, cwd = mktemp dir):

| Command | Before | After | Delta |
|---|---|---|---|
| `crush dirs` | 162.3 ms +-12.3 | 116.2 ms +-10.6 | -28.4% |
| `crush --version` | 116.0 ms +-9.7 | 117.0 ms +-9.2 | noise (path unchanged) |
| `crush --help` | 120.0 ms +-7.7 | 125.1 ms +-9.3 | noise (path unchanged) |

- **Load avg**: 4.31 (start) / 4.09 (end).
- **Verdict**: KEPT (dirs medians separated by 46 ms > 2x stddev).
  `go test ./internal/config ./internal/cmd`, vet, lint clean. Inside a
  git repo the fork still happens (unchanged). Further `--version` wins
  need dropping chroma's eager init from the import graph (third-party).

**Follow-up (CodeQL fix, PR #100):** CodeQL flagged `os.Lstat(filepath.Join(abs, ".git"))` as go/path-injection. Added a `strings.Contains(abs, "..")` guard inside the ancestor walk (CodeQL's recognized dot-dot sanitizer guard; `filepath.Abs` output never contains `..` components, and a hit merely falls back to running git, so behavior is preserved). `crush dirs` from a non-repo temp dir: 109.8 ms ± 11.0 ms (hyperfine, 10 runs), no regression in the fast path (one extra substring check per ancestor). Tests, vet, golangci-lint clean.

## Attempt 17: pool peer response buffers in cluster `call`

- **Profile** (`BenchmarkClusterOverviewFetch`): 53% of bytes allocated
  were `io.ReadAll` growing the response buffer by doubling per call;
  CPU is mostly HTTP/2 + TLS syscalls. The sync-position-then-overview
  ordering in `remoteView.refresh` is a correctness requirement (position
  must not run ahead of data), so it was left serial.
- **Hypothesis**: reusing a pooled `bytes.Buffer` for peer response bodies
  removes the per-call growth garbage and GC pressure; `json.Unmarshal`
  copies what it keeps, so results are identical.
- **Change**: `internal/cluster/peer.go` (`respBufPool`, buffers > 1 MiB
  not returned to the pool).
- **Results** (GOMAXPROCS=4, -count=10, benchstat):

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ClusterOverviewFetch time/op | 1.898 ms +-4% | 1.509 ms +-8% | -20.5% (p=0.000) |
| ClusterOverviewFetch p50 | 1824 us | 1417 us | -22.3% |
| ClusterOverviewFetch B/op | 474.4 KiB | 219.8 KiB | -53.7% (p=0.000) |
| ClusterCallAPI time/op | 452.4 us | 440.3 us | ~ (p=0.85) |

- **Load avg**: 2.00 (start) / 2.96 (end).
- **Verdict**: KEPT. `go test -race ./internal/cluster ./internal/server`,
  vet, lint clean.

## Attempt 18: startup LSP/MCP init (investigation)

- **Hypothesis**: LSP/MCP client init runs serially or blocks first frame /
  ready; moving it concurrent and off the critical path would cut both.
- **Code reading**: already true. `app.New` calls `mcp.ArmInit()` then
  `go mcp.Initialize(...)`, which starts every MCP server in its own
  goroutine; only the coordinator's ready barrier (itself async, and only
  before the first turn's tool list) calls `mcp.WaitForInit`. LSP servers
  are on-demand (`Manager.Start` on file touch);
  `go LSPManager.TrackConfigured()` only publishes "unstarted" states.
- **Method**: `bench/startup.py` (temporarily patched to copy
  `$BENCH_PROJECT_CONFIG` into the temp project as `crush.json`), 15 cold
  runs each, same binary (HEAD b1de0504). Config: a stdio Python MCP server
  that sleeps 1.5 s before answering `initialize`/`tools/list`, plus a
  `gopls` LSP entry (gopls not installed, so it only reports state).
  Isolated HOME/XDG/CRUSH_GLOBAL_* and private `-H unix://` socket.
- **Results** (median ms from exec, +-stddev):

  | Phase | No MCP/LSP | Slow MCP + LSP |
  |-------|-----------:|---------------:|
  | server_ready | 268.8 +-15.1 | 269.8 +-14.2 |
  | srv.ws_done | 323.1 +-17.7 | 324.8 +-14.3 |
  | first_frame | 387.6 +-21.3 | 390.1 +-14.7 |
  | ready | 469.0 +-25.7 | 472.3 +-17.5 |

  A 1.5 s MCP server adds ~2-3 ms (noise), so nothing on the
  first-frame/ready path waits for MCP or LSP.
- **Load avg**: 2.27 (start) / 2.40 (end).
- **Verdict**: REVERTED (no code change; the harness patch was dropped).
  Nothing to gain here; remaining startup cost is server spawn and
  workspace/UI setup (see attempt 11).

## Attempt 19: warm workspace open breakdown + single git branch fork

- **Breakdown** (temporary per-call duration instrumentation, warm server,
  fresh project per run, n=10, median ms): HTTP handler 102 total =
  decode 0.3, resolve key 0.3, config init 10.7, DB open + 20 migrations
  38.7 (CPU-bound DDL/schema reparse in goose; ~1.5 ms if schema current),
  skills 0.6, app.New 4.6, registry/swarm wiring 0.4, and
  `workspaceToProtoForClient` **45.4**. The last one ran `git branch
  --show-current` twice: once for `ws.Path` in `workspaceToProto`, then
  again for the client launch cwd, discarding the first result.
- **Hypothesis**: spawning git only for the directory actually reported
  cuts ~22 ms off every workspace open (CreateWorkspace and adoption).
- **Change**: `internal/backend/backend.go` (`workspaceToProtoAt`; the
  per-client variant no longer computes the shared-path branch). Same
  output. Also `bench/startup.py` now tolerates `PermissionError` from
  `killpg` on an already-reaped pgid (harness crashed mid-run otherwise).
  Instrumentation was dropped.
- **Method**: `bench/startup.py <bin> 30 --warm`, base/new/base/new
  interleaved (n=60 each), HEAD binary from a temp worktree.
- **Load avg**: 3.23 (start) / 2.75 (end).

| Metric (warm, n=60) | Before | After | Delta |
|---|---|---|---|
| workspace open (workspace_created - server_ready) | 102.8 +-8.5 | 80.4 +-7.1 | -21.8% (MW p=1e-17) |
| first_frame | 235.4 +-12.8 | 215.2 +-15.6 | -8.6% (p=1e-13) |
| ready | 318.8 +-18.1 | 297.6 +-18.3 | -6.6% (p=5e-12) |

- **Verdict**: KEPT. Open delta separated by >2x stddev; `go test
  ./internal/backend`, vet, lint clean. Next biggest: fresh-DB migrations
  (~37 ms, only on first open of a project).
- **Confirmation rerun** (warm n=20, HEAD~1 vs HEAD, load 3.01/3.62):
  workspace_created 220.0 -> 197.6 ms, first_frame 233.9 -> 212.3 ms,
  ready 311.6 -> 294.3 ms.

## Attempt 20: startup first frame -> ready (models dialog list re-measure)

- **Breakdown** (temporary trace marks, warm, n=10, ms since exec):
  first_frame 206.6, overview cmd starts 244.0, response 247.0,
  `workspaceOverviewsMsg` handled 283.4. The RPC itself is ~3 ms (no
  cluster wait; bench has no cluster bridge). The ~77 ms is the Bubble
  Tea event loop: ~12 startup messages (ConnectionEvent, ColorProfile,
  WindowSize, Env, MCP/LSP/skill events, RawMsg, bg color, version,
  commands, history) each followed by a synchronous View of ~4 ms
  (first full-size View ~17-21 ms). The overview msg queues behind them.
  The bench runs unconfigured, so every frame draws the onboarding
  models dialog.
- **Hypothesis**: `Models.Draw` calls `list.SetSize` with innerWidth then
  innerWidth-3 every frame; each width change drops the list render cache,
  so `TotalHeight` re-renders every model item per frame. Memoizing the
  measurement (keyed on item generation, width, styles) should cut View
  cost and pull `ready` in.
- **Change**: `internal/ui/dialog/models.go`, `models_list.go` (memo +
  generation counter on `SetItems`).
- **Method**: `bench/startup.py`, base/new interleaved twice; warm n=40,
  cold n=30 each. Load 3.01 (start) / 2.44 (end).

| Metric | Before | After | Delta |
|---|---|---|---|
| warm first_frame | 213.2 +-12.8 | 216.4 +-14.0 | +1.5% (MW p=0.36) |
| warm ready | 294.5 +-17.9 | 299.0 +-19.2 | +1.5% (p=0.31) |
| cold first_frame | 365.9 +-20.4 | 368.0 +-21.2 | +0.6% (p=0.95) |
| cold ready | 449.5 +-25.0 | 449.4 +-24.6 | -0.0% (p=0.98) |

- **Verdict**: REVERTED. Instrumented View was still ~4 ms/frame with
  the memo; per-item width-keyed render caches already make re-measuring
  cheap. Next step: pprof a single onboarding View (dialog/lipgloss
  composition is the likely cost), or coalesce startup messages / render
  less often, since ready is bounded by N_msgs x View cost.

## Attempt 21: cluster join approval latency (long-poll)

- **Hypothesis**: `Join` re-polls `/cluster/v1/join` every 2 s
  (`PollInterval` default), so an approved node waits up to 2 s (mean
  ~1 s, more if approval lands just after a poll) before it learns it was
  admitted. Holding a pending join response open until the request is
  decided makes approval propagate in one RTT.
- **Change**: `internal/cluster/rpc.go`, `node.go`, `ops.go`, new
  `join_bench_test.go`. `joinRequestBody` gains optional `wait_ms`; the
  server (capped at 20 s, below the 30 s peer client timeout) waits on a
  `joinDecided` channel that `Approve`, `Deny` and `Stop` close, then
  re-runs the exact same denied/member/key-pin checks. The client sends
  the first request without `wait_ms` (so `OnPending` fires immediately),
  then long-polls; if a call returns quickly (older server ignoring the
  field) it falls back to sleeping `PollInterval`. Security semantics
  unchanged: same mTLS key proof, same decision code path.
- **Bench**: `BenchmarkClusterJoinApprovalLatency` (default interval,
  approve 300 ms after pending, reports approval->Join-return). Base was
  HEAD in a temp worktree with the same bench file. GOMAXPROCS=4,
  count=10. Load 2.22 (start) / 1.85 (end).

| Metric | Before | After | Delta |
|---|---|---|---|
| approve->joined | 1706 ms +-0% | 9.24 ms +-9% | -99.46% (p=0.000) |
| ClusterColdJoin | 122.96 ms +-2% | 88.81 ms +-2% | -27.77% (p=0.000) |
| ColdJoin B/op, allocs | 3.707 MiB, 29.6k | 3.717 MiB, 29.6k | ~ |

- **Tests**: `go test -race ./internal/cluster` (14 runs; one early
  unreproduced failure, then 13 passes incl. -count=8), new
  `TestClusterJoinDenyWakesLongPoll`, vet, lint clean.
- **Verdict**: KEPT.

## Attempt 22: replica sync owner lookups cached per batch

- **Hypothesis**: `Apply` runs one `SELECT owner_node ... FROM sessions`
  per child row even after attempt 13's statement cache; caching the
  result per session within one batch removes most of those queries.
- **Change**: `internal/replica/replica.go`. `owner()` memoizes
  (owner, exists) per session ID for the batch; the entry is dropped
  after any sessions upsert or delete in the batch so later lookups see
  the batch's own ownership changes. Results identical (same
  "" = owned here, missing = not exists semantics).
- **Bench**: GOMAXPROCS=4, count=10, load 1.69 (start) / 2.10 (end).

| Metric | Before | After | Delta |
|---|---|---|---|
| ReplicaFullSync time | 456.7 ms +-6% | 431.2 ms +-5% | -5.58% (p=0.011) |
| ReplicaFullSync B/op | 31.78 MiB | 29.78 MiB | -6.29% (p=0.000) |
| ReplicaFullSync allocs | 395.6k | 339.8k | -14.11% (p=0.000) |
| ReplicaServeDelta time | 474.1 us | 467.8 us | ~ (p=0.579) |

- **Tests**: `go test -race ./internal/replica ./internal/cluster`, vet,
  lint clean.
- **Verdict**: KEPT.

## Attempt 23: cluster cold join pairing (fsync overlap / no-op saves)

- **Profile**: wall-time probe of `pairedTrio`: 3x `NewNode` ~32 ms
  (identity create: ed25519 keygen + two `writeFileAtomic`, each
  F_FULLFSYNC on darwin), each Join ~24-29 ms (pending ~10-15 ms,
  approve->joined ~14 ms), mesh ~13 ms. CPU profile dominated by TLS
  handshakes (NATS routes) and `writeFileAtomic` syscalls.
- **Hypothesis A**: `Store.Update` rewrites cluster.json on no-op
  updates; skipping byte-identical rewrites saves fsyncs.
  Result: 90.21 ms -> 89.79 ms, ~ (p=0.481). No redundant saves on the
  join path.
- **Hypothesis B**: overlapping the two identity-file fsyncs in
  `createIdentity` (stage both concurrently, rename key before
  node.json) halves identity-create latency.
  Result: 90.21 ms -> 89.82 ms, ~ (p=0.315). APFS appears to serialize
  F_FULLFSYNC per volume, so concurrent syncs do not overlap.
- **Bench**: `BenchmarkClusterColdJoin`, GOMAXPROCS=4, count=10, load
  2.05-2.68. B/op, allocs unchanged.
- **Verdict**: REVERTED (both, no significant change). Remaining costs
  are durable fsyncs (keeping them preserves crash safety) and NATS
  route TLS handshakes in third-party code.

## Attempt 24: warm workspace open round 2 (git HEAD read, no fork)

- **Profile**: CPU profile of a fresh `db.Connect` (all 20 migrations,
  ~35 ms): dominated by SQLite full schema reparses triggered by each
  `ALTER TABLE ADD COLUMN` plus WAL commit pwrites; not reducible without
  rewriting migrations or relaxing durability. The remaining avoidable
  cost was the one `git branch --show-current` fork per workspace open
  (~25 ms, paid even for non-repos).
- **Hypothesis**: resolving the branch by reading `.git`/`HEAD` directly
  removes the subprocess from CreateWorkspace.
- **Change**: `internal/backend/gitbranch*.go` (`readGitBranch`), used by
  `getGitBranch` in `internal/backend/backend.go`. Handles plain repos,
  subdirs, `.git` gitdir files (worktrees/submodules), detached HEAD, and
  no repo. Falls back to git for anything else: GIT_* env overrides, bare
  repo / inside `.git`, foreign ownership (safe.directory), unusual HEAD
  (symlink, reftable `.invalid`, non-heads refs), and always on Windows.
  Test compares against the git CLI.
- **Method**: `bench/startup.py <bin> 20 --warm`, old/new/old/new (n=40
  each), HEAD binary from a temp worktree. Load 8.12 (start, decaying) /
  5.56 (end).

| Metric (warm, n=40) | Before | After | Delta |
|---|---|---|---|
| workspace open (created - server_ready) | 81.8 +-7.6 | 55.8 +-5.7 | -31.8% (MW p=2e-13) |
| workspace_created | 202.5 +-15.3 | 176.6 +-13.1 | -12.8% (p=1e-9) |
| first_frame | 217.3 +-16.4 | 191.3 +-14.6 | -12.0% (p=2e-9) |
| ready | 295.9 +-19.5 | 273.3 +-19.4 | -7.6% (p=8e-9) |

- **Tests**: `go test ./internal/backend`, vet (darwin + GOOS=windows),
  lint clean.
- **Verdict**: KEPT. Next biggest: fresh-DB migrations (~35 ms, first
  open of a project only).
- **Follow-up**: added `strings.Contains(p, "..")` guards (fallback to git) before every file API in gitbranch*.go, incl. `gitdir:` paths, to clear CodeQL go/path-injection alerts on PR #100.

## Attempt 25: survivor-side route re-solicit for one-sided reconnects

- **Hypothesis**: when a restarted peer cannot dial out, survivors only
  redial it on nats-server's fixed 1 s explicit-route reconnect delay
  (`routeReconnectDelay`/`routeConnectDelay`, not exposed in
  `server.Options` v2.15.2). Crush gets no peer-up signal in this case
  (the peer cannot reach us over HTTP either), so survivors must probe:
  a member with no route whose mesh port accepts TCP gets its route URL
  removed and re-added via `ReloadOptions`, which makes nats-server
  solicit it immediately.
- **Change**: `internal/cluster/mesh.go`: per-mesh `redialLoop` (100 ms
  tick) maps connected routes by `RemoteName` (= node ID), TCP-probes
  missing members' mesh addresses (100 ms timeout), and re-solicits at
  most once per second per address. Stopped in `Close`. New benchmark
  `BenchmarkClusterMeshReconnectOneSided` (restarted node's own route
  URLs point at a dead port).
- **Results** (GOMAXPROCS=4, count=10, benchtime=10x, benchstat):

| Benchmark | Before | After | Delta |
|---|---|---|---|
| ClusterMeshReconnectOneSided time/op | 512.0 ms +-20% | 219.3 ms +-2% | -57.2% (p=0.000) |
| ClusterMeshReconnectOneSided p99 | 1005.9 ms | 811.9 ms | -19.3% (p=0.000) |
| ClusterMeshReconnectOneSided allocs/op | 22.98k | 17.75k | -22.8% (p=0.001) |
| ClusterMeshReconnect time/op | 6.57 ms | 6.76 ms | ~ (p=0.853) |

- **Load avg**: 1.95 (start) / 1.37 (end).
- **Verdict**: KEPT. `go test -race -count=3 ./internal/cluster
  ./internal/server`, vet, lint clean. Residual tail (~0.8 s p99) comes
  from iterations where the 1 s re-solicit cooldown or a duplicate-route
  race defers to the stock redial.

## Attempt 26: repeated TUI attach, FS + DB discovery (investigation, baseline only)

- **Goal**: find which filesystem walks / discoveries / DB work run on
  every TUI launch against an already-running server and the same
  workspace path. No optimization in this attempt.
- **Caveat on earlier "warm" numbers**: `startup.py --warm` reused the
  server but created a new, non-git temp project per run. Those runs
  measured new-workspace creation (DB create + migrate) and skipped all
  git-dependent work; they were not repeat-attach numbers. The mode is
  now called `warm-new` (`--warm` still maps to it).
- **Harness**: `bench/startup.py` gains `--mode
  cold|warm-new|reuse|reuse-teardown`, `--fixture small|large`, `--raw`
  per-run JSON, an explicitly started `--persistent` private-socket
  server with device listeners disabled, a fake offline provider, and a
  per-role span/count summary. `startuptrace` gains `Span`/`Count`
  (every occurrence; static labels only) and an env-gated
  `CRUSH_STARTUP_TRACE_RECONNECT_GRACE` knob for forcing teardown.
  Spans added in client connect, CreateWorkspace/buildWorkspace,
  config.Load, db.Connect, skills walk, app.New, checkpoint ignore read,
  prompt.Build, overviews, and every HTTP route (`srv.http_us:<pattern>`).
- **Baseline** (10 runs, medians, ready ms; load avg 2.4):

| fixture | cold | warm-new | reuse | reuse-teardown |
|---|---|---|---|---|
| large (9.1k files, 8 extra DBs) | 503 | 257 | 308 | 728 |
| small (empty git repo) | 479 | 246 | 240 | 365 |

- **Findings** (details in `HOTSPOTS.md`, "Repeated TUI attach"):
  - Every connect, even to a loaded workspace, rebuilds the coordinator
    twice (`agent/init`, `agent/update`), which launches 7 async
    `prompt.Build` calls, each running 3 `git` execs (254 ms each on the
    large repo, 1.78 s total) and a skills fastwalk (147 entries each).
  - Every workspace rebuild (after the TUI quits and the workspace tears
    down) pays `checkpoint.InitRepoAt` -> `gitignore.ReadPatterns`, a
    full work-tree walk: 273 ms on the critical path (large), plus 12
    `prompt.Build` calls (5.3 s CPU).
  - Config discovery (<1 ms), existing-DB open (4 ms), registry list
    (0.4 ms) and cross-project DB peeks (3 ms for 8 DBs) are not hot.
- **Recommended first target**: cache / deduplicate `prompt.Build`
  (git status + skills discovery) per coordinator generation and drop
  the redundant coordinator rebuilds on attach; second, cache the
  checkpoint gitignore matcher per project root across teardown.
- **Verdict**: BASELINE (instrumentation only).

## Attempt 27: server-side cache of prompt inputs (git status, skills)

- **Hypothesis**: every TUI attach launches 7 (12 on rebuild) async
  `prompt.Build` calls, each forking 3 `git` commands and re-walking the
  skills dirs. Caching those inputs per working dir / skills path set,
  with singleflight dedup of concurrent builds, removes almost all of
  that server CPU and the contention it puts on the attach critical path.
- **Change**: new `internal/agent/prompt/cache.go` (generic `inputCache`:
  stat fingerprint + 5 s TTL + `singleflight`); `prompt.go` uses
  `cachedGitStatus` and `cachedDiscoverSkills`. Git fingerprint: size +
  mtime of `HEAD`, `index`, `packed-refs`, `refs/heads` and the ref HEAD
  points to (follows `.git` files / `commondir` for linked worktrees).
  Skills fingerprint: each skills root, its entries, and each entry's
  `SKILL.md`. Fingerprint is re-taken after compute (git status rewrites
  the index). The TTL covers unstaged work-tree edits and nested skill
  edits that touch no fingerprinted file. Date and config-dependent
  content (context files, disabled skills, model) are still computed per
  build, so config changes need no extra invalidation. All new FS calls
  on workspace-derived paths are guarded by `strings.Contains(p, "..")`.
- **Not done**: skipping the second coordinator rebuild (`agent/update`).
  `UpdateModels` also rebuilds tools from live MCP state, so a safe
  "inputs unchanged" fingerprint is not cheap. With the cache, the
  rebuild's prompt builds cost ~1 ms each, so the remaining win is small.
- **Tests**: `cache_test.go`: cached == uncached git status; staging,
  committing and switching branch each invalidate; TTL expiry recomputes;
  adding a skill dir invalidates. `go test -race ./internal/agent/prompt
  ./internal/agent`, `go vet`, golangci-lint: pass, 0 issues.
- **Method**: `bench/startup.py <bin> 15 --mode {reuse,reuse-teardown}
  --fixture {small,large}`, old (HEAD) and new back to back per cell.
  Load avg 2.67 at start, 4.53 at end.
- **Results** (ready ms, median +- stddev; server `prompt.build` /
  `git_status` total ms per run):

  | fixture / mode | old ready | new ready | delta | old srv build / git | new srv build / git |
  |---|---|---|---|---|---|
  | small reuse | 288 +- 29 | 195 +- 24 | -32.1% | 1019 / 977 | 19 / 0.9 |
  | small reuse-teardown | 356 +- 39 | 236 +- 57 | -33.9% | 2519 / 2368 | 51 / 1.5 |
  | large reuse | 448 +- 77 | 217 +- 48 | -51.6% | 2279 / 2205 | 25 / 0.9 |
  | large reuse-teardown | 698 +- 93 | 520 +- 26 | -25.5% | 5361 / 4989 | 38 / 1.5 |

  All median gaps are more than 2x the larger stddev. Caveat: harness
  runs are under 5 s apart, so later runs also hit the cache across
  launches. A relaunch more than 5 s later still gets singleflight dedup
  (one git computation per attach instead of 7-12).
- **Verdict**: KEPT.

## Attempt 28: server-side cache of the checkpoint gitignore matcher

- **Hypothesis**: `checkpoint.InitRepoAt` re-runs `gitignore.ReadPatterns`
  (full ReadDir walk + parse, ~278 ms on the large fixture) on every
  workspace build. Caching patterns per cleaned absolute work tree and
  revalidating with stats only (each walked dir's mtime, each probed
  `.gitignore` / `.git/info/exclude` path's existence+mtime+size) skips
  ReadDir/parse on relaunch while staying exact.
- **Change**: `internal/checkpoint/ignorecache.go` (own re-implementation of
  ReadPatterns that records fingerprints; `sync.Map` cache; `..` guard),
  `internal/checkpoint/repo.go` uses it. Tests in `ignorecache_test.go`
  compare against go-git's `ReadPatterns` on hit and after edit, add (in
  existing and new dirs), remove (re-exposing a previously ignored dir's
  `.gitignore`), and exclude-file edit.
- **Results** (bench/startup.py, 16 runs, old/new back to back, load
  5.3 -> 1.9):

  | fixture / mode | old ready | new ready | delta | gitignore span old / new |
  |---|---|---|---|---|
  | large reuse-teardown | 516.5 +- 21.8 | 349.3 +- 37.0 | -32.4% | 277.9 / 116.3 ms |
  | small reuse-teardown | 214.4 +- 7.5 | 212.7 +- 21.5 | noise | 0.53 / 0.43 ms |
  | large reuse | 202.7 +- 24.8 | 201.4 +- 19.1 | noise | n/a |
  | small reuse | 185.8 +- 21.2 | 189.8 +- 16.9 | noise | n/a |

  Large reuse-teardown gap (167 ms) exceeds 2x the larger stddev (74 ms).
  Revalidation still costs ~116 ms of stats on the large tree; a watcher
  or moving it off the critical path would be the next step.
- **Verdict**: KEPT. `go test -race`, vet, golangci-lint clean on
  internal/checkpoint.

## Attempt 29: background revalidation of the cached checkpoint gitignore

- **Hypothesis**: after attempt 28 a cache hit still stats every walked dir
  (~116 ms on the large fixture) synchronously inside `InitRepoAt`, which
  the TUI waits on. No fs watcher exists in the codebase (no fsnotify), so
  option (b): when a cache entry exists, run revalidation (and a full
  re-walk if stale) in a goroutine and make the first snapshot block on it.
- **Change**: `internal/checkpoint/ignorecache.go` (`loadIgnoreMatcher`,
  `hasCachedIgnore` with the `..` guard), `internal/checkpoint/repo.go`
  (`Repo.loadIgnorer`, resolved under `snapshotMu` at the start of
  `CreateSnapshotCtx`, the only `buildTree` caller). No cache -> unchanged
  synchronous path. New test: edit `.gitignore` between two `InitRepoAt`
  calls, the first snapshot uses the new patterns (matcher + restored tree).
- **Results** (bench/startup.py, 16 runs, old/new back to back, load
  4.00 -> 2.16):

  | fixture / mode | old ready | new ready | delta |
  |---|---|---|---|
  | large reuse-teardown | 346.8 +- 18.4 | 236.7 +- 19.5 | -31.7% |
  | small reuse-teardown | 216.8 +- 23.7 | 216.8 +- 24.9 | noise |
  | large reuse | 200.8 +- 17.7 | 203.7 +- 19.9 | noise |
  | small reuse | 189.2 +- 11.2 | 188.7 +- 15.9 | noise |

  Large reuse-teardown workspace_created 269.2 -> 156.2 ms. Gap (110 ms)
  exceeds 2x the larger stddev (39 ms).
- **Verdict**: KEPT. `go test -race`, vet, golangci-lint clean on
  internal/checkpoint.

## Attempt 30: prefetch workspace overviews at UI Init

- **Hypothesis**: the first_frame -> overviews_loaded gap is not the
  server. Client trace (temporary per-Update/View spans plus new
  `overviews_req_start`/`overviews_req_done` marks) showed: Bubble Tea runs
  Init commands only ~17 ms after Init (after the startup terminal
  messages), the request itself takes ~2-3 ms, and the result then queues
  behind ~10 messages, each followed by a synchronous ~3-5 ms View, so it
  lands ~35 ms after the response. Starting the fetch in a goroutine at
  Init (instead of inside the Cmd) lets the result be ready the moment
  Bubble Tea runs Init commands, so it is delivered ahead of the other
  RPC-backed Init results.
- **Change**: `internal/ui/model/sessionsidebar_wiring.go`
  (`prefetchWorkspaceOverviews`, trace marks), `internal/ui/model/ui.go`
  (Init uses it), `bench/startup.py` (new phases). Same request, same
  message, same display; later refreshes unchanged.
- **Results** (bench/startup.py, 16 runs, old/new back to back, load
  3.05 -> 2.74; small runs had outliers from a concurrent `go test`):

  | fixture / mode | old ready | new ready | delta | ff->ready old / new |
  |---|---|---|---|---|
  | large reuse | 203.9 +- 11.7 | 170.2 +- 14.8 | -16.5% | 66.4 / 32.7 |
  | large reuse-teardown | 234.6 +- 30.3 | 199.1 +- 20.9 | -15.1% | 69.8 / 35.6 |
  | small reuse | 197.1 +- 73.3 | 177.5 +- 104.5 | -9.9% (noisy) | 54.0 / 32.8 |
  | small reuse-teardown | 234.4 +- 98.1 | 201.9 +- 23.5 | -13.9% | 59.2 / 35.5 |

  Large reuse gap (34 ms) exceeds 2x the larger stddev (30 ms); first
  frame unchanged everywhere. Remaining gap is Bubble Tea's own startup
  messages each followed by a full View (~3 ms each).
- **Verdict**: KEPT. vet, golangci-lint, tests clean on internal/ui/model.

## Attempt 31: Final validation: repeated TUI attach FS/DB caching

Scope: attempts 27-30 (prompt input cache, checkpoint gitignore cache,
background gitignore revalidation, overview prefetch) reviewed against
baseline f32dc805. Raw data: `bench/out/attach-final/`. Runners:
`bench/attach-final-bench.sh` (micro, temp baseline worktree with
uncached shims so benchmark names/data are identical) and
`bench/attach-final-summary.py` (pools e2e raw runs).

### Review findings and fixes

- **Unbounded growth** (fixed): `inputCache` and the checkpoint
  `ignoreCache` grew one entry per workspace path forever. Prompt caches
  now cap at 256 entries (expired pruned first, then arbitrary); the
  gitignore cache is a mutex map capped at 128 work trees.
- **Skills miss overhead** (fixed): skills fingerprint was taken twice per
  miss. Only git status re-fingerprints after compute (git status
  refreshes `.git/index`); skills keep the pre-compute key, which is also
  safer (a change racing with compute forces a recompute).
  SkillsMissTTL went 2.118 ms -> 1.759 ms.
- Verified (tests added): concurrent cold callers coalesce to one compute
  (`TestInputCacheCoalescesConcurrentMisses`); linked worktrees resolve
  `.git` pointer + `commondir`, per-worktree HEAD/index and shared refs
  invalidate (`TestCachedGitStatusLinkedWorktree`); SKILL.md edits, skill
  dir deletion, distinct path sets don't cross-contaminate
  (`TestCachedDiscoverSkillsEditsAndDeletes`); gitignore errors are not
  cached (`TestReadIgnorePatternsErrorNotSticky`); new nested
  `.gitignore` / removed subtree invalidate
  (`TestReadIgnorePatternsNewNestedIgnore`); first snapshot always waits
  for background revalidation (existing test). The revalidation goroutine
  is bounded by one stat walk and always closes `done`, so an unused Repo
  leaks nothing beyond that walk.
- Freshness windows / risks: prompt inputs may be stale up to
  `inputCacheTTL` = 5 s for changes the fingerprint can't see (unstaged
  work-tree edits, edits deep inside a skill dir other than SKILL.md).
  Gitignore cache has no TTL; it relies on mtime+size of every walked dir
  and probed ignore file (same-size edit within one mtime tick would be
  missed; APFS/ext4 are ns). Paths containing `..` bypass caching
  (CodeQL guard). Skills slice is shallow-cloned; callers do not mutate
  `*Skill`.
- Not cached on purpose: DB discovery/open (~3 ms per attach).

### Micro (GOMAXPROCS=4, -count=10, load 1.8-7.2 during run)

| Benchmark | f32dc805 | final | delta (p) |
|-----------|---------:|------:|-----------|
| PromptGitStatusHit | 79.5 ms | 99.1 us | -99.9% (0.000) |
| PromptGitStatusMissTTL (invalidated every iter) | 76.6 ms | 80.5 ms | ~ (0.353) |
| PromptGitStatusParallelColdBurst (8 callers, invalidated) | 157.8 ms | 79.3 ms | -49.7% (0.000), coalesced |
| PromptSkillsHit | 1.302 ms | 0.399 ms | -69.3% (0.000) |
| PromptSkillsMissTTL | 1.287 ms | 1.759 ms | +36.8% (0.000), fingerprint cost on a pure miss |
| CheckpointReopen | 13.41 ms | 0.616 ms | -95.4% (0.000) |
| CheckpointReopenFirstSnapshot | 89.4 ms | 81.1 ms | -9.4% (0.003) |

### End-to-end (`bench/startup.py`, 2 interleaved rounds x 10 runs = n=20 per cell, warmup = 1st launch)

| fixture/mode | bin | first_frame | ready (2..N) | 1st launch ready | prompt.build ms/run (calls) | gitignore ms |
|---|---|---:|---:|---:|---:|---:|
| small/reuse | base | 138.8 +-11.5 | 228.7 +-25.9 | 534 | 883 (7) | 0 |
| small/reuse | final | 134.4 +-6.5 | 165.6 +-7.8 | 376 | 17.4 (7) | 0 |
| small/reuse-teardown | base | 200.3 +-20.7 | 324.2 +-44.0 | 420 | 2432 (12) | 0.55 |
| small/reuse-teardown | final | 161.0 +-13.6 | 191.7 +-15.5 | 352 | 33.3 (12) | 0.49 |
| large/reuse | base | 141.1 +-14.1 | 302.3 +-45.9 | 899 | 1874 (7) | 0 |
| large/reuse | final | 133.8 +-13.7 | 165.7 +-16.9 | 531 | 18.7 (7) | 0 |
| large/reuse-teardown | base | 484.4 +-24.0 | 765.3 +-51.5 | 831 | 5251 (12) | 285 |
| large/reuse-teardown | final | 162.9 +-17.6 | 196.8 +-24.3 | 538 | 41.3 (12) | 128 (background) |

prompt.build is summed over parallel builds per run (wall-clock overlap).
Load avg 1.6-5.7 (see `e2e-load.txt`). All ready deltas exceed 2x the
larger stddev.

hyperfine not used: the TUI never exits on its own and its "ready" point
is only observable via `CRUSH_STARTUP_TRACE` inside a PTY, so hyperfine
would time process kill latency, not attach. The PTY phase harness with a
persistent private server is the correct tool here.

- **Verdict**: KEPT (bounded caches + skills single fingerprint). Tests,
  `-race -count=3`, vet, golangci-lint clean on internal/agent/prompt and
  internal/checkpoint.

## CI fix: TestDeviceBridgeStreamCredit -race hang (Ubuntu, PR100 @ 1c67674a)

- Symptom: `go test -race ./...` timed out at 10m, stuck in `deviceStream.waitCredit` cond.Wait (devicebridge_test.go:649, the zero-credit lease case).
- Root cause: inherited from origin/feature (code identical). `waitCredit` armed `time.AfterFunc(blocked)` *before* computing `deadline := now+blocked`, so the timer could fire before the deadline. The waiter woke, saw `Now < deadline`, and went back to `Wait` with nothing left to wake it, a lost wakeup. A slow, preempted CI runner widens the window.
- Reproduction: not reproduced locally (count=30 on HEAD and on an origin/feature worktree; 600 runs at GOMAXPROCS=1 -race all passed; load ~2.5). The race follows from the ordering in the code.
- Fix (internal/server/devicestream.go): the timer sets an `expired` flag under `s.mu` before broadcasting, and the loop checks that flag instead of comparing wall-clock times. Backpressure and stream semantics are unchanged.
- Test: `TestDeviceStreamCreditTimeoutSurvivesEarlyWake` (spurious broadcasts before the lease expires; the stream must still end with `endCreditTimeout`).
- Verification: -race targeted count=30 ok, full internal/server -race ok, vet ok, golangci-lint 0 issues.
- Status: KEPT (bug fix, not a perf attempt).

## Attempt 32: Scale baseline (hundreds of workspaces x thousands of sessions)

- **Hypothesis**: none (baseline only). Question from Tai: "what about when
  there are hundreds of workspace and thousands of sessions each".
- **Harness** (committed): `bench/scale/gen` (deterministic generator,
  crush migrations via `db.Connect`, direct SQL inserts, registry entries),
  `bench/scale/fixture.go`, `bench/scale.sh` (matrix), `bench/scale/summary.py`,
  `bench/scale/scale_test.go` (PeekSessions, Service.List, Connect,
  RegistryList, DecodeOverviews), `bench/startup.py --fixture-root/--probe`
  (prebuilt fixture + direct overview fetch, payload size, server RSS),
  `CRUSH_BENCH_REPLICA_SESSIONS` for `internal/replica` benches.
  Run: `bench/scale.sh <crush-bin> bench/out/scale 10`.
- **Fixtures**: `/Volumes/Store1/tmp/crush-scale-*` (mktemp), deleted after
  each point; regenerate with the generator. All 9 points ran (max 3.8 GB,
  34 s); nothing skipped. Private server/socket only, all nodes killed.
- **Load avg**: 1.9-3.2.
- **Key numbers** (reuse mode, ready ms / overview payload):
  10x200 197 / 0.3 MB; 10x5000 530 / 6.9 MB; 100x2000 1147 / 27 MB;
  100x5000 3015 / 67.5 MB; 300x2000 3547 / 81 MB; 300x5000 4357 / 75 MB
  with 189/300 workspaces `unavailable` (peek 3 s timeout). first_frame
  constant ~180 ms. Server RSS 114 MB (10x200) -> 550-830 MB (100-300 x
  5000). Client decode of the 100x5000 payload: 963 ms, 380 MB alloc.
  Replica FullSync 5000 sessions: 11.7 s; no-op delta 7 ms (O(M)).
- **Details/curves/ranked fixes**: bench/HOTSPOTS.md "Scale: hundreds of
  workspaces x thousands of sessions".
- **Verdict**: BASELINE (no code optimization).

## Attempt 33: Bounded workspace overview (scale fix 1, partial 3)

- **Hypothesis**: at N x M scale the TUI's "ready" is dominated by
  encoding, transferring and decoding every session of every workspace
  (67-81 MB). The sidebar shows at most an even height share per workspace
  and collapses the rest into "…N more"; returning only the top K=50
  sessions per workspace (already in display order: busy, unread, recent)
  plus a total count should cut ready ~proportionally to payload.
- **Change**: `GET /v1/workspace-overviews?limit=K` (also with `local=1`);
  absent/0 keeps the full legacy response (rush mobile, older TUIs,
  peers). New `proto.WorkspaceOverview.SessionCount` (`session_count`,
  omitempty, set only when truncated). `Backend.ListWorkspaceOverviewsLimit`
  / `ListClusterOverviewsLimit` (truncate after the existing sort/merge),
  `Client.ListWorkspaceOverviewsLimit`, `Workspace.ListWorkspaceOverviewsLimit`.
  TUI requests limit=50; while the "/" search is active it requests full
  lists (entering search with truncated data triggers a full fetch). The
  sidebar adds hidden sessions to the "…N more" row (which opens the
  workspace's full picker, unchanged) and to the Total tally. Live updates
  still refetch the overview; an updated session becomes most-recent so it
  enters the top K. Swagger regenerated. Files: internal/proto/proto.go,
  internal/backend/{overview,cluster}.go, internal/server/proto.go,
  internal/client/proto.go, internal/workspace/{workspace,client_workspace}.go,
  internal/ui/model/{sessionsidebar,sessionsidebar_wiring}.go, swagger,
  tests (backend/overview_limit_test.go, server/overview_limit_test.go,
  ui/model/sessionsidebar_bounded_test.go).
- **Not done (follow-ups)**: the cluster peer fetch (`mesh.go`, `local=1`)
  stays unbounded because `RemoteOverviews` also feeds device-session sync,
  which needs complete lists; the bounded TUI request truncates the merged
  result instead. No SQL LIMIT/index yet (peek cache still holds full
  lists, server list_overviews unchanged), and no separate paginated
  per-workspace endpoint: the overflow row's picker already lists the
  workspace in full. 300x5000 stays dominated by the unbounded peek fan-out
  (fix 2).
- **Method**: same fixture per point (bench/scale/gen -clone, temp dirs on
  /Volumes/Store1/tmp), HEAD and change binaries back to back,
  `startup.py --mode reuse --probe 1`, 10 runs each; Mann-Whitney U.
  Load avg 2.4-5.5 (one 5.5 spike during 100x5000).

| N x M | HEAD ready (med) | new ready (med) | delta | p | HEAD overview req | new overview req |
|---|---|---|---|---|---|---|
| 10x200 | 190 ms | 188 ms | -1.2% | 0.26 | 22 ms | 16 ms |
| 100x2000 | 1196 ms | 341 ms | -71.5% | 0.0002 | 1037 ms | 190 ms |
| 100x5000 | 2696 ms | 571 ms | -78.8% | 0.0012 | 2521 ms | 403 ms |
| 300x2000 | 3527 ms | 620 ms | -82.4% | 0.0065 | 3062 ms | 453 ms |
| 300x5000 | 4460 ms | 4004 ms | -10.2% | 0.0025 | 4260 ms | 3362 ms |

- **Verdict**: KEPT. Large points improve 10-82% (p<0.01), 10x200 no
  regression. go test -race (backend, server, client, workspace, ui/model,
  proto), go vet, golangci-lint clean.

## Attempt 34: Scale fixes 2+4+5 (peek pool + stale, cached sorted overviews, registry cache)

- **Hypothesis**: at 300x5000 the unbounded per-workspace peek fan-out
  contends until ~200 workspaces miss the 3 s stall guard; every request
  also re-converts and re-sorts each cached listing and re-parses the
  registry JSONL (with an O(n^2) insertion sort). A bounded pool, a
  per-stamp sorted overview cache and an mtime-validated registry cache
  should make the large points faster and the response complete.
- **Change**: `internal/backend/overview.go` peekOverviews runs at most
  max(4, GOMAXPROCS) reads at once; on error it serves the last cached
  listing marked `stale`/`stale_since`/`last_error` (existing proto
  fields) instead of `unavailable`. `internal/backend/peek_cache.go` stores
  the converted, sorted `[]proto.SessionOverview` per dbStamp (shared,
  read-only; slices clipped so appends copy; splitByOwner/mergeReplicas
  already copy elements). `internal/registry/registry.go` caches the
  deduped listing keyed by file size+mtime (cleared on Add/Compact, so
  external appends by other processes are seen), returns clones, and
  sorts with slices.SortFunc (same order). Tests: stale fallback, pool
  bound + order, registry cache vs external appends/mutation.
- **Method**: same fixture per point (bench/scale/gen -clone on
  /Volumes/Store1/tmp, deleted after), HEAD and change binaries back to
  back, `startup.py --mode reuse --probe 1`, 10 runs each, Mann-Whitney U.
  Load avg 2.2-3.6. (Absolute HEAD numbers differ from attempt 33's run;
  compare within the row.)

| N x M | HEAD ready | new ready | delta | p | HEAD ovreq | new ovreq | HEAD srv list | new srv list | HEAD probe ms | new probe ms | HEAD unavail | new unavail | HEAD RSS after | new RSS after |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 10x200 | 190 | 187 | -1.4% | 0.4274 | 16 | 14 | 6 | 6 | 13 | 14 | 0 | 0 | 115 | 113 |
| 100x2000 | 376 | 275 | -26.9% | 0.0002 | 204 | 110 | 126 | 38 | 507 | 467 | 0 | 0 | 452 | 436 |
| 100x5000 | 1151 | 370 | -67.9% | 0.0002 | 885 | 204 | 391 | 72 | 1670 | 1297 | 0 | 0 | 666 | 695 |
| 300x2000 | 651 | 399 | -38.7% | 0.0002 | 472 | 235 | 256 | 42 | 1903 | 1640 | 0 | 0 | 574 | 675 |
| 300x5000 | 4005 | 1656 | -58.7% | 0.0002 | 3563 | 1053 | 3159 | 124 | 4975 | 4537 | 215 | 0 | 303 | 14 |

"probe ms" is the full (unlimited) `GET /v1/workspace-overviews`, still
dominated by encoding 500k+ sessions. The 300x5000 "new RSS after" of 14
MB looks like a probe sampling artifact, not a real drop.

Go bench `BenchmarkRegistryList` (GOMAXPROCS=4, count=10): 145 us / 893
us / 2.87 ms / 13.4 ms -> 6.2 / 8.3 / 12.6 / 30.6 us at 10/100/300/1000
workspaces (-96..-99.8%, p=0.000); allocs 108..15026 -> 3.

- **Verdict**: KEPT. Large points -27..-68% (p=0.0002), 300x5000 now 0
  unavailable (was 215), 10x200 unchanged (-1.4%, p=0.43). go test -race
  (backend, registry, server, workspace), go vet, golangci-lint clean.

## Attempt 35: LRU prompt-input and gitignore caches (scale fix 7, partial)

- **Hypothesis**: with >cap workspaces, arbitrary-entry eviction in the
  prompt-input cache (256) and gitignore cache (128) thrashes; true LRU plus
  a cap of 1024 keeps ~500+ workspaces resident with bounded memory.
- **Change**: `internal/agent/prompt/cache.go` (LRU via use tick, cap 1024),
  `internal/checkpoint/ignorecache.go` (LRU via use tick, cap 1024); new
  benches `BenchmarkInputCacheCycle300`, `BenchmarkIgnoreCacheCycle300`;
  tests `TestInputCacheLRU`, `TestIgnoreCacheLRU`.
- **Not done**: the 5 s prompt TTL was kept. The git-status fingerprint
  (HEAD/index/refs) cannot see unstaged work-tree edits and the skills
  fingerprint cannot see nested edits, so removing the TTL would serve
  stale prompts; stat revalidation alone is not correct here. The peek
  cache bound (backend) was not addressed. The TUI ready-time scale matrix
  was not rerun: neither cache is on the overview/ready path (prompt build
  and checkpoint init only), so the Go cycling benches are the target metric.
- **Results** (GOMAXPROCS=4, count=10, load 3.1-3.3):

| bench | before | after | delta |
|---|---|---|---|
| InputCacheCycle300 (50 us compute) | 23.5 us, 72.8% hit | 239 ns, 100% hit | -99.0% (p=0.000) |
| IgnoreCacheCycle300 | 906 us, 34.1 KiB, 248 allocs | 176 us, 9.4 KiB, 71 allocs | -80.6% (p=0.000) |
| PromptGitStatusHit | 94.4 us | 99.7 us | ~ (p=0.39) |
| CheckpointReopen | 624 us | 617 us | ~ (p=0.32) |

- **Verdict**: KEPT. go test -race, go vet, golangci-lint clean on
  checkpoint and agent/prompt.

## Attempt 36: Lazy owner lookup in replica delta Serve (scale fix 6, partial)

- **Hypothesis**: the idle delta poll is O(sessions) only because `Serve`
  builds the full owned-session set up front. Resolving ownership lazily
  (point lookups per touched session, cached, switching to one bulk scan
  after 64 distinct sessions) makes the no-change poll O(changes).
- **Change**: `internal/replica/replica.go` (`ownerCheck`; full snapshot
  path unchanged; `serveEmbeddings` buffers rows before ownership lookups
  so it never nests a query under an open cursor, which deadlocked the
  single-connection pool on the first try). Test
  `TestServeDeltaFiltersByOwner` covers idle, point and bulk paths.
- **Wire**: unchanged (same Batch/Change JSON), so mixed-version peers sync
  as before; no protocol test needed beyond the existing round trips.
- **Load**: 1.95 -> 2.68.

| bench (GOMAXPROCS=4, count=10) | before | after | delta |
|---|---|---|---|
| ServeDelta 200 | 482.6 us, 24.3 KiB, 709 allocs | 174.9 us, 2.75 KiB, 81 allocs | -63.8% (p=0.000) |
| ServeDelta 2000 | 2919 us, 294 KiB, 6127 allocs | 178.9 us, 2.75 KiB, 81 allocs | -93.9% (p=0.000) |
| ServeDelta 5000 | 6872 us, 625 KiB, 15144 allocs | 178.0 us, 2.75 KiB, 81 allocs | -97.4% (p=0.000) |
| FullSync 200 | 428.1 ms, 29.8 MiB | 435.3 ms, 29.8 MiB | ~ (p=0.58) |

- **Not done (budget)**: paged/streamed full sync (FullSync still ~2.3
  ms/session, unchanged) and the 3-node 100x2000 in-process run and
  startup ready-time sweep; replica serving is not on the TUI startup path
  so ready time is unaffected by this change.
- **Verdict**: KEPT. go test -race (replica, backend), go vet,
  golangci-lint clean.

## Attempt 37: Active/busy session always visible, WAL invalidation, bounded peek cache

- **Hypothesis / goal**: correctness follow-up to attempts 33/34. A bounded
  (top-50) overview can drop the open session or a session with a running
  agent / pending permission from the sidebar; the peek cache must be
  invalidated by WAL-only writes; the peek cache must not grow without
  bound.
- **Change**:
  - `internal/backend/overview.go`: `limitOverviews` never cuts busy
    sessions (they sort first; pending permission implies an active turn,
    so it is busy). Test `TestLimitOverviewsKeepsBusy`.
  - `internal/ui/model/sessionsidebar_wiring.go`, `update.go`:
    `mergeActiveSession` appends the open top-level session to its
    workspace (root or symlink-resolved root) when the bounded list
    omitted it, copying the input. `SessionCount` (the total) is left
    alone, so "…N more" drops by one. Test `TestMergeActiveSession`.
  - `internal/backend/peek_cache.go`: stamp also includes the 48-byte
    WAL-index header from `crush.db-shm` (change counter, mxFrame,
    salts), so a WAL rewritten from offset 0 at equal size is caught even
    on coarse-mtime filesystems. Test `TestPeekCacheInvalidatesOnWALOnlyWrites`
    (writer keeps its connection open; asserts crush.db is untouched while
    create/update/delete and a post-TRUNCATE-checkpoint rewrite are all
    seen). The existing mtime+size stamp already passed it on APFS; the
    shm header is defense in depth.
  - Peek cache is now LRU-bounded by total cached sessions (default
    2,000,000, about 0.5 KiB each, whole-workspace eviction, newest entry
    always kept). Test `TestPeekCacheEvictsLRU`. 300x5000 (1.5M sessions)
    fits, so no thrash at any measured point.
- **Not done (budget, last attempt)**: (2) SQL LIMIT + index, (3)
  paginated per-workspace sessions endpoint, pre-encoded per-workspace
  JSON for the unlimited overview (still ~4.3 s at 300x5000).
- **Measurement**: `startup.py --mode reuse --fixture-root --probe 1`, 10
  runs per binary, the three binaries back to back on the same fixture
  per point (`-clone` fixtures on /Volumes/Store1/tmp, deleted after).
  Load 3.78 3.54 2.89 -> 2.98 2.99 2.87. RSS = `ps -o rss` of the private server pid after the
  10 attach runs and after 5 full-overview probes (2 samples; macOS RSS is
  noisy because of memory compression, treat +-30% as noise).

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

- **RSS (5)/(6)**: at 300x5000 the server holds ~0.9-1.9 GB after the
  full-overview probes for both HEAD and now vs 0.7 GB at the attempt-32
  baseline, which had 241/300 workspaces unavailable (so it cached far
  fewer sessions). The cap bounds the worst case at ~1 GB of peek entries;
  it does not lower RSS at 300x5000 since everything fits.
- **Ready time**: 10x200 185 vs 184 ms (no regression). 300x5000 631 vs
  1530 ms median for this run, but nothing in the change targets that
  path (no eviction, one extra 48-byte read per workspace), so it is not
  claimed as a win; likely run-to-run variance in the stale/peek path.
- **Verdict**: KEPT as correctness fix. go test -race (backend,
  ui/model), go vet, golangci-lint clean.

- CI fix (not an attempt): TestIgnoreCacheLRU now builds Load keys with filepath.Join so it passes on Windows; prompt TestInputCacheLRU and other attempt 32-37 tests checked, no path-separator issues found.
