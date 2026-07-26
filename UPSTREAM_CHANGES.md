# Upstream changes since fork point

Fork point (merge-base with `upstream/main`): `5631ab28` (2026-06-08).
Upstream commits since: 334 total. Below are the meaningful ones grouped by
area. Version-bump tags, "auto-update files", gofmt/golden-file regen, CLA/CoC
signings, and dependabot bumps are omitted.

Legend for our decision: [ ] undecided · [x] want · [~] maybe · [n] skip · [DONE] ported

## Ported into this fork (impl comparison round 1)
- [DONE] `18c27999` grep: report every matching line per file (+ stable sort).
  Ported to `internal/agent/tools/grep.go` (`fileMatches`, `lineMatch`,
  `SortStableFunc`) + regression test.
- [DONE] `8db57337` recover from mid-stream provider connection resets.
  Ported `Message.ResetStreamedContent` (`internal/message/content.go`),
  OnRetry reset + transport-error finish in `internal/agent/run.go`, and added
  `IsTransportError`/`NewTransportError`/`WrapTransportError` to the
  `taigrr/fantasy` fork (tagged `v0.27.0-fork`).
- [DONE] `f8d855d5` scrollable right sidebar with focus-based navigation.
  Adapted to our diverged multi-sidebar UI: new `uiFocusRightSidebar`,
  `l`/`h` focus toggle, j/k/g/G scroll, mouse wheel + click, focus-only
  scrollbar. (`ui.go`, `sidebar.go`, `keys.go`, `help.go`.)
- [DONE] `132a8c89` MCP: clear tools + close session on error; reap stdio
  process groups. (`mcp/init.go`, `mcp/tools.go`, `process_unix.go`,
  `process_other.go` + tests.)
- [DONE] `8246dff5` MCP: wait for init before building tool list
  (coordinator gating + WaitForInit contract).
- [DONE] `009ce621` MCP: serialize renewals, restore all registries, arm init
  gate (`ArmInit`, per-server renew mutex, prompt/resource re-registration).
- [n] `533fcf1e` MCP auth-signal double-close panic — N/A, we don't have the
  fantasy auth-refresh signal plumbing (`WaitForTokenChange`/`SignalAuthComplete`).

---

Legend for remaining items below: [ ] undecided · [x] want · [~] maybe · [n] skip

---

## Client / Server + daemon mode
- [ ] `18391023` server: helper to detect stale unix sockets
- [ ] `a442f26e` server: store runtime socket in per-user runtime dir
- [ ] `62baa95e` server: log earlier so socket cleanup is recorded
- [ ] `443d0ab3` server: clear leftover sockets so server can always start
- [ ] `2e9f6342` server: detect+remove dead socket before starting
- [ ] `4abf2066` server: correct socket location test on macOS
- [ ] `f98791e9` server: show LSP status in client-server mode
- [ ] `3c51a9f2` client/server: fix non-interactive init
- [ ] `ee00ec7b` respect client-server mode during login (#3315)
- [ ] `e3074f14` server: show update notices in client-server mode
- [ ] `a283fbd9` prevent new sessions hanging in client-server mode
- [ ] `086dc626` client: add MCP prompts to UI in client-server mode
- [ ] `fd55b5d1` server: don't shut down while a workspace is being created
- [ ] `6f8a7fe8` client: reconnect event stream after it drops
- [ ] `b2386962` ui: distinguish lost connection from uninitialized agent
- [ ] `227696ab` server: in-flight create counter guarding shutdown
- [ ] `fa6ec09d` server: wait before shutdown so back-to-back sessions don't race
- [ ] `fix-tool-call-output` family (see MCP channels below)

## Bang mode (`!` direct shell execution) — large feature
- [ ] `99a5fad5` add bang mode for direct shell command execution (#3013)
- [ ] `03bfdc66` set title in bang mode
- [ ] `6db54b3a` show pending spinner
- [ ] `6814ccfc` stream results in
- [ ] `1c2da893` remap ansi 16 colors
- [ ] `47ed0f3a` cancel command execution
- [ ] `f46387bb` copy message result
- [ ] `175ce34c` strip ansi in copy and context
- [ ] `48e9cca2` interleave stderr and stdout
- [ ] `8305eb03` fix duplicate command message race
- [ ] `ee47eb80` allow prefixing a string with bangmode
- [ ] `11662010` activate when ! preceded by whitespace
- [ ] `7e4bd6a0` engage when pasting text starting with !
- [ ] `f342edf0` include bang commands in history
- [ ] `d20e29ae` sync bang mode with external editor
- [ ] `cb129202` don't add extra ! when browsing history
- Note: we have our own `feat/bang-mode` branch — compare before reimplementing.

## Question tool (structured interactive UI) — large feature
- [ ] `c2a6f765` add question tool with structured UI
- [ ] `75e7195f` client server integration
- [ ] `321c661e` mouse support
- [ ] `1b5994c0` paste support in text areas
- [ ] `cbb9d4f2` fix scrollbar disappearing single-select
- [ ] `81a8ee4b` tab resizing + mouse->keyboard transition
- [ ] `3bf40358` escape cancels instead of submitting empty
- [ ] `f69a91e5` extend length limits
- [ ] plus review/tweak commits: `9f4f145a 82e88985 9e6175a2 ca5a19d7 6f33b666 70c64b57 5e611a70 a5a5c6c5 29f5691d 1937ac54`

## LSP superpowers — new tools
- [ ] `17ad5c7f` add LSP superpowers tools
- [ ] `cd8c06ce` add 4 new lsp tools
- [ ] `8ad59d22` update system prompt to recommend lsp tools
- [ ] `b9a1182b` word boundaries in symbol grep + validate against LSP
- [ ] `07044a9b` fix TrackConfigured startup race + relative path resolution
- [ ] `70cd684a` fix lsp restart failing
- [ ] `3b9f0193` fix stale sidebar state on render
- [ ] `7eca7a04` fancy diff view renders
- [ ] `cc971bd6` perf: filter servers before searching $PATH (#3370)
- Note: we have `lsp-lock`, `feat/mcp-lsp-restart` — cross-check.

## MCP: OAuth + channels + reliability
- [ ] `d338af88` OAuth support for HTTP MCP servers (later reverted/redone)
- [ ] `67e748e1` revert #3348 OAuth (superseded)
- [ ] `37fc995d` OAuth 2.1 authorization for HTTP MCP servers
- [ ] `1588ae86` SSE OAuth, metadata fixups, resource param stripping
- [ ] `7946b21e` OAuth config guide + schema
- [ ] `2ca5a8c3` normalize oauth metadata redirects (#3415)
- [ ] `c3fd60ea` remove orphaned tokens from oauth MCP (#3418)
- [ ] `26399fc2` OAuth review findings (timeout/refresh/concurrency)
- [ ] `533fcf1e` prevent panic when auth signal fires twice (#3403)
- [ ] MCP channels: `a1cf0022 d291d9f2 fed86b00 ca9ef6ad a09cb366 f7c53cbc 2cb0bdb2 bc8ff341 1f623ec0 14fa1de8 2af939d8` (claude channel capability, --channels flag)
- [ ] `132a8c89` clear tools + close session on MCP error; reap stdio pgroups
- [ ] `8246dff5` wait for MCP init before building tool list
- [ ] `009ce621` serialize renewals, restore registries, arm init gate
- [ ] `e68041a6` pin go-sdk to main for protocol version header fix
- Note: we have `bugfix/sort-mcps` — separate concern.

## Providers / model discovery
- [ ] `73031584` auto-discover models from openai-compat providers
- [ ] `0f279057` litellm enricher
- [ ] `9299ad47` ollama enricher
- [ ] `89b680d2` omlx enricher
- [ ] `3c4e6547` lmstudio enricher
- [ ] `c1a48226` llamacpp enricher
- [ ] `cfdca358` correct model discovery enrichment for local providers
- [ ] `9886d223` add fireworks provider
- [ ] `d1489a60` wire LM Studio vision -> SupportsImages (#3280)
- [ ] `ebf6e826` add alibaba us
- [ ] `d341d84b` fix missing thinking traces on deepseek via alibaba
- [ ] `3ac7f1c4` baseten thinking on/off toggle
- [ ] `aca40878` baseten "none" reasoning level
- [ ] `78a205cd` copilot: guard nil request body in initiator transport
- [ ] `8ccf6945` copilot: add additional responses models
- [ ] `a882695e` minimax m3 thinking blocks on opencode providers
- [ ] `d0dc9fc9` prepare for gpt-5.6
- [ ] `a07d304f` refresh hyper oauth token before fetching credits
- [ ] `f45712a3` extract hypercredits from fantasy
- [ ] `14483cac` switch back to upstream openai sdk

## Auth / config
- [ ] `64bbbebc` integrate fantasy OnAuthRefresh for transparent auth retry
- [ ] `de679203` stop two Crush instances invalidating each other's login
- [ ] `4be77c56` log provider warnings from fantasy step results
- [ ] `6242e4f4` load user-level context files
- [ ] `213ad794` load system-wide config from /etc/crush/crush.json (#2984)
- [ ] `b10f890f` race-free config via copy-on-write
- [ ] `55b2f0d1` prevent data race reading config during reload (#3362)
- [ ] `f5b996bf` fast model selection + config reload
- [ ] `1535ebb7` prevent stale ReasoningEffort leaking across providers
- [ ] `d4dc84e9` prevent startup deadlock when configured model ID invalid
- [ ] `a06fd034` fix reflection on provider options (schema)
- [ ] `461976d0` scope logout command to oauth providers
- [ ] `f75435a2` fall back to first reasoning level when default unset

## Agent / conversation reliability
- [ ] `1cfa9a15` fix subagents returning empty responses
- [ ] `8db57337` recover cleanly from mid-stream provider connection resets
- [ ] `0d4c2bbc` keep spinner animating when response restarts
- [ ] `21a457d5` validate tool call JSON before storing (stuck conversations)
- [ ] `d3d68045` prevent session bricking when non-vision models get tool media
- [ ] `492460a8` make coordinator use a struct
- [ ] `ae9257b9` serialize in-process run dispatch (no concurrent turns)
- [ ] `fbf59341` close dispatch completion-boundary cancel race
- [ ] `ebd845c0` send session hash header for cache affinity
- [ ] `b5cd46c2` suppress context cancellation error
- [ ] `001a2f85` preserve attachment chips in user messages after sending
- [ ] `12e96776` copy verification URL in OAuth dialog

## Tools (edit/grep/glob/bash/shell/stats)
- [ ] `6b6fab5e` simplify edit tools + enforce Sourcegraph result limits
- [ ] `4f4b8469` refactor edit tool to not duplicate find/replace
- [ ] `18c27999` report every matching line per file in internal grep (#2994)
- [ ] `67f50014` keep file search fast/bounded on large dirs (glob)
- [ ] `a08e3329` keep TruncateOutput from splitting UTF-8
- [ ] `d3af321b` isolate child processes from Crush's session (shell) (#3097)
- [ ] `a9e3a57f` shell: skip persistence when session no longer exists
- [ ] `3446255d` add --all and --crawl-dir modes to stats subcommand
- [ ] `3e6f95eb` address indentation on commit message trailings
- [ ] `aad8cbad` improve git commit + PR message standards

## Hooks
- [ ] `021e3773` add name field to hooks
- [ ] `bb44fb1f` bridge Claude Code additionalContext -> HookResult.Context
- Note: our hooks system already diverged significantly.

## UI / TUI
- [ ] `ac4bd9c1` scrolling: move to delta coalescing filter
- [ ] `72069811` fix stale scrolling acceleration
- [ ] `b72f9aab` add scrollbar to chat view (#3018)
- [ ] `ff12fa01` scrollbar only on human scroll
- [ ] `6437f0d7` reliable follow-scroll when content grows
- [ ] `6d272018` keep chat pinned to bottom after resize
- [ ] `db8add71` allow expanding toolcall names
- [ ] `cbb6daaa` preserve newlines in expanded tool content
- [ ] Sidebar scrolling feature: `f8d855d5 d5007646 5d9d37bc c5bf189c 4b729a93 4244f520 79eedf79 885fe4d3 c3b32034 5b7d7f71 efbe3083` and fixes
- [ ] Attachment chips remove button: `f604d989 4413344c 15e73964 1be84086`
- [ ] `f413c9a9` auto-expand reasoning dialog based on count
- [ ] `dc160997` elapsed seconds timer
- [ ] `74e725b3` resolve timer display conflicts in thinking/tool spinners
- [ ] Dialog responsive-width overhaul (many): `3e2bd1cc 151b38d4 f6528f72 6407b852 31550cb4 2b4a01ae e271cecb e8c2772d d6c63d31 bbf654fb aaf92f86 a69ace5e 0c61aeee 686a3f46 605dfc9e f3592bd8 352e2651 e456a902 2c6424c1`
- [ ] `13501672` render pills box reliably when todos/queue appear mid-session
- [ ] `1ef42ef8` fix pill border on queued messages
- [ ] `a02a9809` quit dialog: hint on skipping confirmation
- [ ] `c880f929` use fgmostsubtle for canceled text
- [ ] `f6a841ec` track last closed dialog in open-with-grace
- [ ] `46c1799a` fallback title generation
- [ ] `b75d6bc2` fix UI freeze loading model endpoints
- [ ] `b6b6f... ` clipboard migration: `98d79e95 4bd01166` (golang.design/x/clipboard)
- [ ] `1b4ef73f` keep shell progress output from corrupting TUI
- [ ] `e4175c52` avoid quadratic shell output rendering (#3381)

## Performance
- [ ] `1bfc53f6` memoize chroma syntax-highlight style
- [ ] `d29fc2ca` memoize chroma lexer lookups by filename
- [ ] `4d901d1b` keep chat resize smooth on large conversations
- [ ] `81cb9d99` optimize model UI rendering
- [ ] `173b2be6` skip theme rebuild when provider keeps same theme
- [ ] `3295a085` cache streaming thinking renders (CPU burn)
- [ ] `bd232eab` keep synchronous workspace probes off per-message Update path
- [ ] `d1626158` prevent stale background refreshes overwriting UI state

## Misc / integrations / platform
- [ ] `b39a85ec` herdr socket integration
- [ ] `188dea64` DirTrim wrong char for non-ASCII dir names
- [ ] `9c61c7d0` / `3a99992e` use ansi.truncate in todos/everywhere
- [ ] `677046d2` support building/running on illumos
- [ ] `00a18434` fix nil pointer crash in Windows CI
- [ ] `1bb945c8` prevent flaky diffview golden tests on Windows
- [ ] `5b614329` drop broken .termux.deb release artifact
- [ ] `72c5668e` system prompt: avoid emdashes in source code
- [ ] `a1b19ebc` unindent heredoc in prompts
- [ ] `5e6890de` bail on precancelled ctx in handleJQ
- [ ] `e34e707c` restore file picker event

---

## Next step
Triage the [ ] items into [x]/[~]/[n]. Many overlap with our existing
feature branches (bang-mode, lsp-lock, mcp-lsp-restart, sort-mcps,
1m-context, etc.) — check those before reimplementing.
