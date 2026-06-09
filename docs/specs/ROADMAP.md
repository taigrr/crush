# Crush Improvements — Roadmap & Technical Plan

## Overview

This document tracks 14 in-flight improvements to Crush, ordered into
implementation phases at the end. Each item records the problem, current
findings from the codebase, the proposed approach, and the files most likely
to change. Check items off as they land; split any item into its own spec
file under `docs/specs/` once design work gets deep enough.

Status legend: `TODO` · `IN PROGRESS` · `BLOCKED` · `DONE`

---

## 1. First-class theming support — `TODO`

**Goal.** Every color used anywhere in the app is referenced through a
semantically named theme token. No inline hex constants, no raw `charmtone.*`
references leaking into consumers.

**Findings.**
- A theme system already exists in `internal/ui/styles/`:
  `themes.go` (theme definitions per provider), `quickstyle.go`
  (`quickStyle`/`quickStyleOpts` builder, ~964 lines), `styles.go`
  (`Styles` struct, 633 lines), `grad.go`.
- Tokens are already semantic at the builder level (`primary`, `accent`,
  `fgSubtle`, `bgLeastVisible`, `destructive`, `warning`, `success`, …).
- Inline hex colors still exist in:
  - `internal/ui/diffview/style.go`
  - `internal/ui/styles/quickstyle.go`
- `ThemeForProvider` only switches on `"hyper"` vs default — there is no
  user-selectable theme yet.

**Approach.**
1. Audit every `#rrggbb` literal and every direct `charmtone.*` use outside
   `themes.go`; route them through the `Styles` struct so consumers only
   ever read semantic fields.
2. Add any missing semantic tokens (e.g. diff add/remove/context,
   syntax-highlight roles) to `quickStyleOpts` and `Styles`.
3. Introduce a user-facing theme selection mechanism: a `theme` field in
   `crush.json` and a registry of named themes, with `ThemeForProvider`
   becoming a fallback rather than the only selector.
4. Add a lint/test that fails on raw hex or `charmtone.` imports outside the
   `styles` package.

**Files.** `internal/ui/styles/*`, `internal/ui/diffview/style.go`,
`internal/config/config.go`, `internal/config/load.go`, plus consumers found
by the audit. Update the `crush-config` skill for the new `theme` option.

---

## 2. Neovim bridge — edits not flashing in — `DONE`

**Goal.** Fix the bridge bug so edit/location events render correctly in
`neocrush.nvim`.

**Findings.** `/tmp/neocrushErr.log` shows the failure is on the **plugin**
side, but caused by data Crush sends:

```
locations.lua:123: bad argument #3 to 'format' (number expected, got nil)
  entry_maker → async_static_finder → show_telescope → handler → bridge.lua:142
```

A location entry reaches `string.format` with a `nil` where a line/column
number is expected. Most likely Crush emits a location/edit event with a
missing or zero-based-vs-one-based line field, or omits the field entirely
for certain edit types (which would also explain edits "not flashing in").

**Approach.**
1. Find the bridge event emitter on the Crush side (search for the location/
   edit payload sent to Neovim) and confirm the line/column fields are always
   populated and 1-based.
2. Make the plugin defensive (`locations.lua:123`) so a nil coalesces to a
   sane default instead of erroring — but fix the root cause in Crush first.
3. Add a regression test around the payload builder.

**Files.** Crush-side neovim bridge emitter (TBD — locate via search);
plugin repo `neocrush.nvim/lua/neocrush/{locations,bridge}.lua` (separate
repo).

---

## 3. WebSocket session-mediated transport — `TODO`

**Goal.** Stop re-sending the full conversation on every turn. Send the full
conversation once at session start; the server upgrades to a WebSocket and
thereafter only individual request/response messages cross the wire.

**Design.**
- **Handshake:** client opens a session, POSTs the full conversation, server
  caches it in memory keyed by session ID and upgrades to WS.
- **Steady state:** only the new user message + streamed assistant response
  travel over the socket.
- **Resilience:** if the socket drops, the client transparently re-sends the
  full conversation and re-establishes the WS. Fully stateless from a
  durability standpoint — the server never persists or logs conversation
  content; it only holds it in memory to reduce LAN traffic.
- **Scope:** this is a transport optimization for the existing client/server
  split (see `internal/server/`, `internal/client/`).

**Open questions.**
- Memory cap / eviction policy for cached conversations.
- Interaction with the fantasy provider layer — does mediation happen before
  or after provider request construction?
- Auth on the WS upgrade.

**Files.** `internal/server/`, `internal/client/`, proto definitions. Likely
warrants its own spec (`docs/specs/WEBSOCKET_TRANSPORT.md`) before coding.

---

## 4. Bedrock/Mantle 200-with-error handling — `DONE`

**Goal.** Detect provider errors that arrive with HTTP 200 so Crush doesn't
enter a wedged state.

**Findings.** Mantle on Bedrock returns its error body with a `200 OK`
status code. The mantle endpoint is OpenAI-compatible and routes through
`buildOpenaiCompatProvider`. On a 200 the SDK parses the body as a
successful (but empty) response, leaving Crush with no content and no
error. Researched shape: Bedrock Mantle returns the OpenAI error envelope
`{"error":{"message",...,"code"}}` (and in-stream SSE error chunks) with
200.

**Fix (shipped).**
- Added `mantleErrorTransport` (an `http.RoundTripper`) in
  `internal/agent/mantle.go`. For Mantle 200 responses with an
  `application/json` body, it detects the OpenAI error envelope
  (`error` object present, no `choices`) and rewrites the status to the
  embedded code (clamped to 400-599) or `502 Bad Gateway`, so the provider
  SDK raises a real error. The body is preserved for the caller. Streaming
  (SSE) responses are left untouched.
- Wired into `buildOpenaiCompatProvider` only when
  `providerID == bedrock-mantle`, wrapping the existing transport.
- Tests in `internal/agent/mantle_test.go` cover envelope detection
  (numeric/string/missing codes, error-with-choices, non-JSON, SSE) and
  the RoundTripper (status rewrite, body preservation, success/SSE
  pass-through).

**Files.** `internal/agent/mantle.go`, `internal/agent/mantle_test.go`,
`internal/agent/coordinator.go`.

---

## 5. Fork copies the fork-point message (off-by-one) — `DONE`

**Goal.** Forking from message *N* should copy messages `1..N-1` into the new
session and **prepopulate the input bar** with message *N* (the most recent),
instead of copying *N* into the fork.

**Findings.** `internal/fork/service.go` `ForkParams.MessageID` is documented
as: *"The new session will include all messages up to **and including** this
one."* That inclusive copy is the bug.

**Approach.**
1. Change fork to copy up to **but not including** the selected message
   (`n-1`).
2. Return the excluded message's text in `ForkResult` so the UI can seed the
   editor with it.
3. Wire the UI fork flow to drop that text into the input component.

**Files.** `internal/fork/service.go`, `internal/workspace/`,
`internal/client/snapshots.go`, fork UI trigger in `internal/ui/`.

---

## 6. Models underusing `multi_view` — `DONE`

**Goal.** Get the model to batch file reads via `multi_view` instead of
sequential `view` calls.

**Findings.** No mention of `multi_view`/`multiedit` batching guidance in
`internal/agent/templates/coder.md.tpl` beyond a passing reference.

**Approach.** Add explicit guidance to the coder (and task) prompt:
"When you need to read 2+ files, call `multi_view` once instead of multiple
`view` calls." Mirror the existing parallel-tool-call guidance.

**Files.** `internal/agent/templates/coder.md.tpl`,
`internal/agent/templates/task.md.tpl`.

---

## 7. Models reluctant to use `context7` — `DONE`

**Goal.** Surface the `context7` tool more prominently so the model reaches
for up-to-date library docs.

**Findings.** No `context7` guidance in the prompt templates.

**Approach.** Add a prompt section instructing the model to use `context7`
for any third-party library/framework/SDK question — even familiar ones —
because training data may be stale. Reference it in the tool-usage section.

**Files.** `internal/agent/templates/coder.md.tpl` (and `task.md.tpl`).

---

## 8. "Buddy" companion feature — `RESEARCH`

**Goal.** A companion that knows the Crush codebase, observes how the user
drives the harness (including live keystrokes/screen state), has access to
the main model's view, and proactively suggests better ways to use Crush.

**Research (what Claude actually shipped).**
- **Explanatory & Learning output styles** (Aug 15 2025, `/output-style`,
  later `/config`): "personal coach" modes. Explanatory narrates
  architectural trade-offs; Learning inserts `TODO(human)` markers for
  pair-programming. Neither watches CLI usage.
- **`/buddy`**: a gamified terminal pet (Tamagotchi-style ASCII companion
  reacting to coding sessions) — an easter egg, not a coach.
- No shipped Claude feature passively monitored CLI usage to suggest harness
  improvements. So this is genuinely new territory for us.

**Proposed mechanics.**
- A side-channel observer with read access to: the input editor buffer (as
  the user types), the rendered screen/diff state, and the main agent's
  message stream.
- Full knowledge of the Crush codebase (embed/RAG over the repo, or a
  curated context bundle) so suggestions reference real features/keybindings.
- Emits non-blocking tips ("you could've used `multi_view` here",
  "Ctrl+G shows all keybindings", "queue this instead of waiting").
- Must be opt-in, rate-limited, and never steal focus.

**Open questions.** Which model? Cost/latency budget for always-on
observation? Privacy/opt-in defaults? Delivery surface (toast, sidebar,
pill)? — warrants its own spec.

**Files.** New package (e.g. `internal/buddy/`), UI surface in
`internal/ui/`, hooks into the editor and agent streams.

---

## 9. Procedures incomplete — `TODO`

**Goal.** Flesh out the procedures system (reusable workflow templates stored
in the user config dir).

**Approach.** Audit current procedure support end-to-end (discovery, listing,
viewing, creating from a conversation, updating), enumerate gaps, and
complete them. Define what "all there" means as an explicit checklist before
implementing.

**Files.** Procedure loading/storage code (locate), prompt wiring,
`internal/ui/` surface for invoking/creating procedures.

---

## 10. Bundle personal skills into the distribution — `CONSIDER`

**Goal.** Decide whether to ship the user's personal skills as defaults.

**Tradeoffs.**
- *Pro:* available out of the box; consistent experience.
- *Con:* harder to edit (embedded vs. on-disk in `~/.config/crush/skills/`);
  versioning/divergence; bloat; opinionated defaults may not suit all users.

**Decision pending.** Lean toward keeping them on-disk and shippable as an
optional, copy-on-first-run starter set rather than embedded. Builtin skills
already live in `internal/skills/builtin/` — that's the mechanism if we do
embed. Revisit after items 1–9.

---

## 11. Milestones skip messages / inconsistent — `DONE`

**Goal.** Auto-generate a milestone every 10 messages, counting **all**
messages (user, assistant, tool calls, system, etc.), with no skipped
batches.

**Findings.**
- Trigger logic lives in `internal/agent/agent.go` (in the Run setup) and
  `internal/agent/milestone.go`.
- Root cause: the trigger only ran once per `Run()` at
  `turnCount = len(msgs)+1` and generated a **single** milestone at that
  turn. A single run emits many messages (assistant + tool calls), so when
  a run jumped past several 10-boundaries at once only one milestone was
  created (skipping the intermediate ones). `backfillMilestones` only ran
  when `count == 0`, so it didn't help mid-conversation.

**Fix (shipped).**
1. Added a pure, tested helper `milestoneBoundaries(afterTurn, totalTurns)`
   returning every multiple-of-10 boundary in `(afterTurn, totalTurns]`.
2. Unified `generateMilestone` + `backfillMilestones` into a single
   `generateMilestones(...)` that loops over every crossed boundary,
   chaining the prior summary for continuity. Milestones now land on exact
   multiples of 10 (10, 20, 30, …), making generation consistent and
   idempotent (re-running with the same `lastTurn` produces no new
   boundaries).
3. Removed the now-dead `buildMilestonePrompt` and the trailing
   non-boundary milestone.
4. Added `internal/agent/milestone_test.go` covering single-run multi-
   boundary crossings and incremental generation.

**Files.** `internal/agent/agent.go`, `internal/agent/milestone.go`,
`internal/agent/milestone_test.go`.

---

## 12. Fork progress indicator — `TODO`

**Goal.** Show a progress bar / loading status during forking so the UI
doesn't appear frozen.

**Approach.** `internal/fork/service.go` already implements
`pubsub.Subscriber[ForkResult]`; emit intermediate progress events
(copying messages, restoring snapshot, creating worktree) and render a
progress bar in the fork UI. Pairs naturally with item 5.

**Files.** `internal/fork/service.go` (progress events), `internal/ui/`
(progress component on the fork flow).

---

## 13. Queue model/reasoning changes while agent is busy — `DONE`

**Goal.** Replace the "agent busy" error when changing the model or reasoning
mode mid-run with queuing: apply the change before the next user message.

**Fix (shipped).** The wait lives **server-side**, on the real agent
lifecycle — the UI just fires the RPC and shows a "queued" message.
- `sessionAgent` gained an event-driven idle signal: `clearActiveRequest`
  (called from every place that releases an active request) closes-and-
  replaces an `idleCh`, and `WaitForIdle(ctx)` blocks on it until
  `IsBusy()` is false (no polling). Covered by `internal/agent/idle_test.go`
  (wake-on-clear, context cancel, concurrent waiters, all under `-race`).
- `coordinator.UpdateModelsWhenIdle(ctx)` applies immediately when idle;
  when busy it spawns a goroutine that `WaitForIdle`s (detached context,
  bounded 30m) then applies — returning immediately so the RPC never hangs.
- `app.UpdateAgentModel` now calls `UpdateModelsWhenIdle`, so the existing
  `UpdateAgent` RPC path (`client.UpdateAgent` → `backend.UpdateAgent` →
  `app.UpdateAgentModel`) defers on the server where the agent's mutex
  actually lives.
- UI handlers (`handleSelectModel`, `ActionSelectReasoningEffort`,
  `ActionSelectContextMode`) no longer reject when busy: they write config +
  fire the RPC and, if `isAgentBusy()`, show "<change> queued; applies when
  the agent finishes". No UI-side polling or pending-action queue.

Note: an earlier attempt queued on the UI side (polling `AgentIsBusy`); that
was replaced — the UI can't reach the agent's mutex across the client/server
RPC boundary, so the wait belongs on the server.

**Files.** `internal/agent/agent.go`, `internal/agent/coordinator.go`,
`internal/agent/idle_test.go`, `internal/app/app.go`,
`internal/ui/model/ui.go` (plus test-mock updates for the new interface
method).

---

## 14. Message queueing fixes — `DONE`

**Goal.** (a) Ensure queued messages actually make it into the conversation.
(b) Stop `Esc` from clearing queued messages — `Esc` should only clear a
queued message that is in the **expanded** state.

**Findings.** Queue-related code spans `internal/ui/model/ui.go`,
`internal/ui/model/pills.go` (queued-message pills), and
`internal/ui/dialog/commands.go`. The agent-side queue lives in
`internal/agent/agent.go` (`messageQueue`, `enqueueCall`,
`drainQueueForStep`, `QueuedPrompts`, `ClearQueue`).

- **Esc behavior (b): FIXED.** `cancelAgent` now only clears the queue when
  `pillsExpanded`; help text gated to match (`internal/ui/model/ui.go`).
- **Dropped messages (a): already fixed upstream — verified.** My original
  note flagged a release-before-drain window (active request deleted before
  the queue was drained), but that referenced pre-refactor line numbers. The
  current dispatch is fully serialized:
  - Interactive prompts route `AgentRun` → `client.SendMessage` →
    `backend.SendMessage` → `BeginAccepted` + `RunAccepted`, i.e. the
    **accepted** dispatch path. In `sessionAgent.Run` that path takes the
    per-session `sessionMu`, and the `IsSessionBusy` check + `enqueueCall`
    + `activeRequests.Set` all happen **under that lock**
    (agent.go:587-648). The post-loop handoff re-acquires `sessionMu`
    before draining the queue (agent.go:1324), so there is no idle window
    where a concurrent submit can bypass the queue.
  - The only non-accepted (`accept == nil`) path is single-shot
    `coordinator.Run` (e.g. `crush run` / `app.go:396`), which has no
    concurrent submissions to drop.
  - This was hardened by upstream commits `21390f52` (atomic queue
    mutation so steering messages aren't dropped), `d7a814c5`,
    `cbec4916`, `34995e93`, with coverage in
    `internal/agent/queued_runid_test.go`,
    `internal/agent/dispatch_cancel_test.go`, and
    `internal/backend/accepted_run_integration_test.go`
    (uses a `gatedStreamModel` to hold a run active and enqueue behind it).

**Files.** `internal/ui/model/ui.go` (Esc gating). No further agent change
required.

---

## 15. Bang-mode (shell) history lag — `DONE`

**Goal.** Up-arrow history should include the most recent shell/user command
immediately, not n-1.

**Findings.** Submit handlers ran
`tea.Batch(m.runShellCommand(command), m.loadPromptHistory())`
(`internal/ui/model/ui.go`). `tea.Batch` executes both commands
concurrently, so `loadPromptHistory` queried `ListUserMessages` before
`AgentRunShellCommand` persisted the message — the new entry only appeared on
the next reload. The plain message path had the same latent race.

**Fix (shipped).** Reload history off the `pubsub.CreatedEvent` for `User`
and `Shell` messages (fires after persistence); dropped the concurrent
`loadPromptHistory()` from both submit batches. `historyReset()` still runs
immediately so navigation starts clean.

---

## Implementation Phases

**Phase 1 — Quick, high-leverage fixes (prompt + small bugs)**
- #6 multi_view prompt
- #7 context7 prompt
- #5 fork off-by-one + input prepopulate
- #14 message queueing fixes
- #2 neovim bridge nil location

**Phase 2 — Correctness & robustness**
- #11 milestone boundary fix
- #4 Bedrock/Mantle 200-error detection
- #13 queue model/reasoning changes
- #12 fork progress indicator

**Phase 3 — Larger features (own specs likely)**
- #1 theming (audit + user-selectable themes)
- #9 procedures completion
- #3 WebSocket transport → `WEBSOCKET_TRANSPORT.md`
- #8 buddy companion → `BUDDY.md`

**Phase 4 — Decisions**
- #10 bundling personal skills (revisit after Phase 3)

---

## Notes

- Module path is `github.com/taigrr/crush` (this fork).
- When config options change (#1, #3), update the `crush-config` builtin
  skill. When new keybindings are added (#8, #13, #14), add them to
  `FullHelp()` in `internal/ui/model/ui.go`.
