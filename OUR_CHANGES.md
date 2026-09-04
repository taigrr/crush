# Our fork's changes since the fork point

Fork point (merge-base with `upstream/main`): `5631ab28` (2026-06-08).
Our commits not in upstream: 349 (~327 meaningful). Grouped by area below.

Legend: readme = should document in README · uniq = unique to us

---

## Snapshots / Checkpoints (git-backed) — MAJOR, uniq, readme
- `0dadddb6` config: snapshots + worktree configuration
- `691170f9`/`e9cb2a72` db: snapshots table migration + sqlc
- `6a8f1a72` go-git repository wrapper
- `8419d05f` snapshot service w/ DB coordination
- `5561dd86` integrate checkpoint service with message flow
- `18596db9` sort git tree entries using git's sort order
- `977a7a4e` show snapshots reverse-chronological
- `2c2a7a1c` skip snapshots for home dir / non-git projects
- `ef455c68` skip re-reading/recompressing unchanged files
- `27be0bdc` prevent snapshot system from crashing server
- `2bbc36f0` respect .gitignore in snapshot tree walk
- `ed582369`/`65b610d8` snapshot GC via git gc
- `d68625fd` exclude nested .pyc/.log from snapshots
- `e567c0ab`/`6ecc7ecd`/`e2b245c9` tests

## Worktrees (real git worktrees) — MAJOR, uniq, readme
- `f1477c54`/`bc0e7fb1` db: worktrees table + sqlc
- `f33a49bb` worktree service for parallel development
- `761e2884` wire worktree service into app
- `c1d064dc` active worktree indicator in header
- `722e7102` startup validation for worktrees
- `b02cdbb7` use active worktree path as working dir
- `616b1c4a` WorkingDirFunc for dynamic working directory
- `5a122bf2` ListAllWorktrees API (fix 500)
- `5a5c5ab3` switch to worktrees from different sessions
- `479e4d8d` Manage Worktrees without active session
- `97ca3c46` merge/rebase worktree dialog
- `9eda2408`/`4d615033`/`2b33cbbc` worktree path caching + negative caching
- `55234c2c` real git worktrees, per-session cwd, per-client editor bridge
- `47108952` canonicalize project root + auto-switch from cwd
- `87a090aa` restore worktree support after session working-dir override

## Conversation forking — MAJOR, uniq, readme
- `b41f0b7c` fork service
- `90b20390` wire fork service
- `6defe9a4` prevent message-ordering race during fork
- `4ab0f921` fork dialog
- `d053b72c` fork copies n-1 msgs, prefills input
- `a0c99d26`/`b23cf20b`/`c0a71023` staged fork progress events + live dialog

## Client/server by default + multi-client — MAJOR (some overlaps upstream)
- `d674dcaf` enable client/server mode by default
- `04c9735a` remove setupLocalWorkspace codepath
- `d0e87ca2`/`11a31762` dedup workspaces by data dir (SQLite contention)
- `c75ca45a` share one workspace per directory across clients
- `cc1e3b71` idempotent permission resolution across clients
- `c5b1c9d8` broadcast config changes to all clients
- `20e7d02b` auto-close permission prompt when another client responds
- `778e8375`/`88931cf7`/`0eea6a15` in-progress flag, per-client viewed session, watcher count
- `7a848fe9`/`8a497330` multi-client tests + docs
- `2b675c49`/`ab6f1a5a`/`1f464cfc` data-directory lock (client-server only)
- `b4810426` connection pool to avoid corrupted writes
- `c5351be1` keep server alive while agent busy after clients detach
- `18c2a123`/`929beadf` per-session permission events, cancel on shutdown
- `2d3b1e64` detect stale server during dev with BuildID
- `114d95c7`/`8f0135fc` stale-socket removal + wait for readiness
- `28932e02` configurable pprof port
- Server RPC/HTTP/client plumbing for snapshots/worktrees/forks: `5a34da3f 3b32c322 63f526f7 a9f91b04 d296b63d 777af782`

## Skills state in client/server — uniq
- `eced043b`/`dec84d5e`/`454ae786`/`7ab3914b`/`f5f349fd` SkillEvent + SSE forwarding
- `3b92eecf`/`503300e2`/`904c7836`/`55c2cb25` SkillsGetStates + UI loading
- `3fec82a8` skills discovery for monorepos

## Milestones + Procedures — MAJOR, uniq, readme
- `67f1436c` db: milestones table + sqlc
- `45d9e11d` milestone service
- `1ddc2361`/`c72df629` milestone generation + backfill per 10-msg boundary
- `3f0360cf`/`c00340de` procedures discovery + inject into system prompt
- `c747e766`/`e706e4cd`/`42cea863` wire through coordinator/app/API/UI (Ctrl+Q)
- `afff9d9b`/`fc3d865a` docs

## Theming system (named + Lua user themes) — MAJOR, uniq, readme
- `b9ba9ed2` named theme registry + builtin lookup
- `9cd318be` load user themes from Lua
- `e8438f92` options.tui.theme + GlobalThemesDir
- `e0e1eea9`/`f4ef804e` apply at startup + theme picker dialog
- `32140a52`/`07ab6215` themeable diff/syntax tokens; forbid raw hex
- `e4182232`/`b50f6aa8` cascading brand tokens exposed to Lua
- `25ddc141` 6 community themes (Tokyo Night, Catppuccin, Dracula, Nord, Gruvbox, Rose Pine)
- `62fdc062` cyberpunk theme
- `21ae5c5f`/`10149020` vscode dark theme
- `26dc0d9f`/`554c6e98`/`7ce011c9` docs

## Dynamic 1M context window — uniq, readme
- `b8114cb2` ContextMode type preserved during reload
- `bac5a67f` dynamic 1M context with auto-switching
- `bd8ba4e9` wire IsExtendedContext through coordinator/proto
- `1b66f765`/`3023881b`/`1c7ede43` rainbow gradient indicator + dialog + sidebar
- `9d6c9f2f` extended context for summarization in extended mode

## New tools — uniq, readme
- `8038f76f` lsp_definition
- `0b82be69` lsp_document_symbols
- `034bfff6` lsp_rename
- `b07ad87c` multi_view (batched file reads)  + `fda26d4c` prompt nudge
- `8c7d5d85` search_history + `a2e7a63b` hybrid embedding search, list_sessions, CLIs
- `7715d1b7`/`d737cd28`/`894bb4a3` native context7 tool + agentic-fetch + coder prompt
- `b07ad87c` (multi_view)
- `d7f62be5` full embedding
- `18758223` db migration tool CLI

## Bang mode (ours) — overlaps upstream, readme
- `a31b0368` bang mode for direct shell execution
- `b158ea53` Shell message role w/ expandable UI
- `45567fe0` store shell command + output as separate parts
- `552d6c18` create session for bang command as first message
- `398ce4c7` dedup bang-mode shell message on auto-create
- `dda4c2b9`/`2f3b88e2`/`393dabe0` fixes
- `84b6c4e6`/`1da061c9`/`0615cbd0` non-interactive env vars in shell

## Neovim editor bridge — uniq, readme
- `dfe6d97b` direct Neovim bridge replacing neocrush daemon
- `0a431514` powernap v0.1.6 for definition/rename/document_symbols
- `71f30358` msgpack Location tags
- `3c787a6a` valid 1-indexed col to neovim location picker

## Image rendering (KGP) — uniq, readme
- `ad0b1378` image rendering for attachments using kgp
- `27b365a1` per-image + aggregate image limits
- `566de53a` source image limits from catwalk metadata
- `2099fa9d` accept large images, downscale instead of reject

## Session management rework — uniq, readme
- `26bac9d3`/`20d5737d`/`012c7626` session archive (db + service + delete snapshots)
- `58192803`/`ca1b466e` archive through client-server + Ctrl+A
- `be0bf0da`/`1772e2a2` archived sessions w/ separator + timestamp fixes
- `aa17d539` session read/unread state, per-session working dir, workspace registry
- `19baf8fd` cross-workspace session listing + runtime switching
- `3df7c358` left session navigator sidebar
- `d9615085` recent sessions on landing screen
- `86b29e28`/`e6cb70c5`/`ed841c35`/`cdaab0f4` navigator polish
- `236a82a5`/`31f6337d` re-title session after 10 msgs + animated reveal
- `b88ff6f2` never resume child session with --continue-last

## Sysadmin mode — uniq, readme
- `58017614` ephemeral sysadmin mode toggle to bypass command filter
- `9865160c` rename banned commands
- `57ab6fb6` require permission for redirect/background in bash safe check

## Reduced-motion / low-bandwidth — uniq, readme
- `6b720d1e` low-bandwidth / reduced-motion mode
- `318b184b` downshift running spinners on toggle

## Review flow (bun-style reviewers + slash command) — uniq, readme
- `95c401e6` bun-style reviewer agents
- `60f218a5`/`c508cd0f` review flow updates
- `cc4d2d8f` harden parallel review fan-out
- `deedc0bb` review slash command

## Swarm lineage + per-session working dir for spawned sessions — uniq, readme
- `sessions.spawned_by_session_id` / `spawned_by_workspace_id` (migration `20260904010000`); stamped from the trusted sender on `swarm new`, never model-supplied; separate from `parent_session_id` so workers stay listed and addressable
- `working_dir` param on `swarm new` (defaults to `path`); validated to resolve to the target workspace (subdir or linked git worktree), persisted via `session.CreateOptions`
- `coordinator.workingDir`: on worktree-enabled workspaces a turn with no client cwd (swarm/API) falls back to the session's recorded working dir, so a worker pinned to a sibling worktree runs there
- structured swarm tool metadata (`workspace_id`, `session_id`, `address`, `working_dir`, `delivery`, `created`)
- proto `Session`/`SessionOverview` carry `working_dir` + lineage; sidebar nests workers under their spawner; session picker shows `by <color-animal>`

## Notifications — overlaps upstream (terminal-notifier)
- `79293d97` ssh terminal notifications
- `9227a9bf` configurable backend + bell support
- `d1ad6027` migrate disabled + picker

## Yolo mode toggle (Ctrl+Y) — overlaps upstream
- `6416029f`/`881e1427`/`3ba135da`/`3ba135da` ctrl+y toggle
- `cfa6a9e2` notification when toggling

## Providers — overlaps + uniq
- `8b14120c`/`169a969c`/`de08bc0a`/`4b4fc3df` GPT-5.5 on Bedrock / bedrock-mantle
- `7d57c13e` surface Bedrock Mantle HTTP-200 errors
- `8b14120c` us-east-2 override
- `abcf4f24`/`c5693f1e`/`b99e11a4` aws bedrock europe
- `ce978c74` copilot additional responses models (also upstream)
- `c99d7287` qwen3.7-max on opencode
- `6f8571a8`/`3e9e2788` catwalk/fantasy fork bumps (mantle sol/terra/luna, opus 4.8)
- `248fc9c0`/`13f05dc2` taigrr forks + embedded providers only

## De-charm / rebrand — uniq
- `a12bb345` rename module to github.com/taigrr/crush
- `40f5af37` remove PostHog telemetry
- `15f33b8a` update checker -> taigrr/crush
- `bf385843`/`b85d8faf` remove attribution
- `2841b732` update refs to charmbracelet

## UI misc — some overlap upstream
- `1ac8692e` scrollbar to sessions dialog (#3005)
- `a22a4a69` scrollbar track position fix
- `9b3f78f1`/`600ebf63`/`9b3f78f1` auto-expand pills
- `181ba641` wrap tool headers, render tools full width
- `c85c75a7` ctrl+f fullscreen chat, image picker -> ctrl+i
- `12651e46`/`2f3b88e2` H/L navigate between user messages
- `56419464` git branch + base dir in header
- `3b5bb219` diff view for denied tools (also upstream)
- `d54f5f5b` improve model-changed notification
- `c77c4ff3` render model name in summarize

## Agent / queue / reliability — some overlap upstream
- `ff03eaa8`/`21390f52` atomic prompt queue (steering msgs not dropped)
- `ae3da3f9`/`fe6bd6af`/`45d73435` queue model/reasoning/context changes while busy
- `8433e550` reliable esc-cancel via generation guard
- `79277490` only clear queue on esc when pills expanded
- `46944776` don't apply stale session files across switch
- `9f5e48d2` cancellation bug fix
- `6431cb37`/`0d74ea7e` client cancel doesn't 500 others
- bash `description` optional (defaults to first line of command); deterministic `RepairToolCall` hook repairs truncated argument JSON and fills omitted descriptive params instead of failing the call
- glob tool is cancellable and bounded: fastwalk fallback honors ctx, stops following symlinks (rg `-L` dropped to match), and a 30s budget returns partial results instead of hanging the turn when `**` is rooted at a broad dir like `$HOME`

## Perf / refactor / hygiene — mostly internal
- `27569d1f` remove hot-path slog from SSE delivery
- `21134bd5` parallelize openInLSPs iteration
- `befbe0ee`/`57e8e7e2`/`c9207e62` slices pkg, membership sets, dead code
- many small dedupe/refactor commits: `3cc2c95b 51a86bca 6ba92181 5ce9e22f 4fdfbac6 d0e36695 94cf761c f42f995e 1d2b09e6 284d6cb6`
- `ea4cc024` pin u-root for moreinterp
- `ce995807` add ripgrep

## Docs / roadmap — internal
- `e3c0aadd`/`393dabe0`/`6e1e6b76`/`f0cbc1a1`/`b6ae7f96`/`fc3d865a` roadmap + status
- `6854ef5b` worktrees+snapshots technical spec
- `de5d36e1` document snapshots/worktrees config

---

# THREE-WAY COMPARISON

## A. Things WE BOTH have (independent or cross-pollinated impls)
Compare implementations; likely keep ours, cherry-pick upstream fixes.
- **Bang mode** — ours `a31b0368+`, upstream `99a5fad5+`. We inspired it.
  Upstream has more polish (ansi remap, stream, cancel, history sync).
- **Yolo toggle (Ctrl+Y)** — ours `6416029f+`, upstream `ctrl-y-yolo`.
- **Notifications (terminal/ssh/bell)** — ours `79293d97+`, upstream
  `terminal-notifier`.
- **Diff view for denied tools** — ours `3b5bb219`, upstream `view-denied-diff`.
- **Sessions dialog scrollbar** — ours `1ac8692e`, upstream chat scrollbar.
- **Multi-client / client-server hardening** — both heavily; diverged.
- **Copilot additional responses models** — ours `ce978c74`, upstream `8ccf6945`.
- **LSP tools / restart** — ours lsp_definition/rename/document_symbols +
  `51a86bca`, upstream LSP superpowers `17ad5c7f+`. Different tool sets.
- **Context mode / large context** — ours dynamic 1M `bac5a67f`, upstream
  `use-large-as-small` branch.

## B. Things ONLY UPSTREAM has (candidates to add — see UPSTREAM_CHANGES.md)
- Question tool (structured interactive UI)
- MCP OAuth 2.1 + MCP channels
- Local-model auto-discovery + enrichers (ollama/litellm/lmstudio/mlx/llamacpp)
- Fireworks provider; alibaba-us; gpt-5.6 prep
- Sidebar scrolling; responsive dialog width overhaul
- Chroma memoization + render perf
- User-level + /etc/crush system-wide config files
- Copy verification URL in OAuth dialog
- illumos build support
- Elapsed-seconds timer
- Recover from mid-stream provider connection resets
- Grep: report every matching line per file

## C. Things ONLY WE have (document in README)
- Git-backed **snapshots/checkpoints** + GC
- Real **git worktrees** with per-session cwd + auto-switch
- **Conversation forking** with live progress
- **Milestones** + **procedures** (auto-injected workflows)
- **Lua theming** system + named theme registry + 9 builtin themes
- **Neovim editor bridge** (replaces neocrush daemon)
- **KGP image rendering** for attachments
- **multi_view**, **search_history** (hybrid embeddings), **list_sessions**,
  native **context7** tool, **crush search/embeddings/migrate** CLIs
- **Session archive**, read/unread, cross-workspace navigator sidebar,
  recent sessions on landing
- **Ephemeral sysadmin mode** toggle
- **Low-bandwidth / reduced-motion** mode
- **Review flow** (parallel adversarial reviewers + slash command)
- **Swarm lineage** (`spawned_by_*`), `working_dir` on `swarm new`, structured swarm tool metadata, nested sidebar
- **Bedrock Mantle** (GPT-5.5 via mantle) + Bedrock Europe
- De-charmed: taigrr module path, no PostHog telemetry, own update checker

---

## Next steps
1. For (A): diff impls, cherry-pick upstream polish/fixes into ours.
2. For (B): triage in UPSTREAM_CHANGES.md, reimplement wanted ones.
3. For (C): update README to advertise our unique features.
