# Claude Code parity research: steering, backgroundify, graceful update

Research notes mapping three Claude Code behaviors onto Crush's current
code so implementation can move fast. Claude Code references are to the
reconstructed TypeScript source (v2.1.88, from the npm source map) checked
out read-only at `~/code/scratch/claude-code-src/src`; paths below are
relative to that `src/`. Crush references are relative to the repo root.
Nothing from the Claude Code tree is copied here; only paths and short
paraphrases.

Date: 2026-09-04. Branch: `research/claude-code-parity` (off
`port/upstream-feature`).

## 1. Mid-turn steering

### How Claude Code does it

**Storage.** A single module-level queue, not React state:
`utils/messageQueueManager.ts` (`commandQueue: QueuedCommand[]`, with a
frozen snapshot + signal for `useSyncExternalStore`). Every kind of
deferred input flows through it: user prompts, background-task
notifications, orphaned permission prompts. API surface: `enqueue`,
`enqueuePendingNotification`, `dequeue`, `getCommandsByMaxPriority`,
`remove`, `clearCommandQueue`.

**Priority model.** `types/textInputTypes.ts:277-357`:

| priority | meaning |
|----------|---------|
| `now`    | Abort the in-flight tool call (abort reason `'interrupt'`) and send immediately. Only used by the UDS chat-UI client, never by the TUI's Enter. |
| `next`   | **Default for Enter while the model is working.** Let the current tool finish, deliver between its `tool_result` and the next API round-trip. |
| `later`  | End-of-turn drain only. Default for task notifications. |

**Enqueue point.** `utils/handlePromptSubmit.ts:313-351`: if
`queryGuard.isActive` (or an external loader is running) the submitted
prompt is enqueued instead of executed. Only `prompt` and `bash` modes can
be queued; slash commands are never drained mid-turn (they run between
turns via `hooks/useQueueProcessor.ts`).

**Splice point.** `query.ts:1535-1643`. After *all* tool results for the
step are collected and *before* recursing into the next API call:

1. `getCommandsByMaxPriority('next')` (or `'later'` if a Sleep tool ran),
   filtered to non-slash commands for the main thread (sub-agents only
   drain `task-notification` entries tagged with their own `agentId`).
2. `getAttachmentMessages(...)` turns each into a `queued_command`
   attachment (`utils/attachments.ts:1046 getQueuedCommandAttachments`),
   which `utils/messages.ts:3739` converts into a **separate `UserMessage`**
   whose text is wrapped (`messages.ts:5510 wrapCommandText`) roughly as:
   "The user sent a new message while you were working: {raw} …
   IMPORTANT: After completing your current task, you MUST address the
   user's message above. Do not ignore it." inside a `<system-reminder>`.
3. The consumed commands are removed from the queue and a
   `started` lifecycle event is emitted for the UI.

The explanatory comment at `query.ts:1535` is the key constraint: drain
*after* tool calls are done because the API errors if `tool_result`
messages are interleaved with regular user messages.

**Provider constraint handling.** Two passes in `utils/messages.ts`:
`reorderAttachmentsForAPI` (L1481) bubbles attachments up to sit directly
after the tool_result user message, and `normalizeMessagesForAPI` (L1989)
merges consecutive user messages via `mergeUserMessages` →
`hoistToolResults` (L2470) so `tool_result` blocks come first. On the wire
it is **one user message `[tool_result…, text]`**; in the transcript and UI
they stay separate messages, so the steer text renders as an ordinary user
bubble.

**Abort semantics.** `'now'` → `abortControllerRef.current.abort('interrupt')`
(`screens/REPL.tsx:4100`). The `'interrupt'` reason is special-cased:
`query.ts:1044,1499` skip the "[Request interrupted by user]" synthetic
message; `services/tools/StreamingToolExecutor.ts:219-260` only marks tools
whose `interruptBehavior()` is `'cancel'` (`Tool.ts:416`; default
`'block'`; Sleep is the example) as interrupted; `utils/ShellCommand.ts:186`
ignores `'interrupt'` aborts entirely so a running shell can be
backgrounded instead of killed. A normal Enter-while-busy never aborts.

**Turn ends with no tool call.** No mid-turn drain happens
(`query.ts:1357`). `hooks/useQueueProcessor.ts:48` fires when
`isQueryActive` flips false and starts a fresh turn from the queue
(`utils/queueProcessor.ts:52 processQueueIfReady`); several queued prompts
become several user messages in one turn.

**UI.** No "queue vs steer" keybinding or mode: Enter is steer. Queued
items are rendered as normal `<Message>` components above the prompt
(`components/PromptInput/PromptInputQueuedCommands.tsx`) inside a
`QueuedMessageProvider` (`isQueued: true`); ↑ or Esc-when-idle pops them
back into the editor (`PromptInput.tsx:1257 popAllCommandsFromQueue`);
Esc-while-running cancels the query first. Spinner stays on while the queue
is non-empty (`REPL.tsx:1680`).

### Crush today

- `/btw` (`internal/ui/model/slash.go:46`) → `sendBTWMessage`
  (`internal/ui/model/compose.go:224`) → `ClientWorkspace.AgentRunBTW`
  (`internal/workspace/client_workspace.go:364`) → `SendMessage` with an
  **empty RunID** and a `[btw] ` text prefix.
- Backend `internal/backend/agent.go` builds the call; `sessionAgent.Run`
  (`internal/agent/run.go:107`) sees the session busy and
  `enqueueCall`s it (`internal/agent/queue.go:99`).
- `drainQueueForStep` (`queue.go:135`) partitions the queue under the
  per-session dispatch mutex: RunID-less calls are returned in `fold`,
  RunID-bearing calls stay queued and later run as their own turn via
  `dispatchNextQueued` (`run.go:904`).
- `PrepareStep` (`run.go:465-538`) appends each folded call as a
  persisted user message (`createUserMessage`,
  `internal/agent/messages.go:17`) to `prepared.Messages`.
- Provider merge: the fantasy fork's Anthropic provider
  (`github.com/taigrr/fantasy@v0.27.0-fork/providers/anthropic/anthropic.go:452-478`,
  `groupIntoBlocks`) already folds a Tool-role message followed by a
  User-role message into a single user block, i.e. the equivalent of
  `hoistToolResults` happens for free.
- Reload robustness: `preparePrompt` (`messages.go:53-125`,
  `toolResultsForAssistant` L166) re-attaches tool results directly after
  their assistant message and synthesizes error results for orphans, so a
  `/btw` persisted between a tool_use and its result is safe on the next
  turn (tests `agent_test.go:241-360`).
- Normal Enter while busy goes through `sendMessage` → `AgentRun` **with**
  a RunID, so it queues as its own turn (Claude Code's `later`
  semantics, plus a separate RunComplete). Pills (`internal/ui/model/pills.go`)
  show `N Queued`; Esc with pills expanded clears the queue
  (`internal/ui/model/cancel.go:54-59`).

So Crush's injection point is already Claude Code's `next` and the wire
format is already correct. Two things are missing/broken.

### Bug: a folded message survives only one step

Fantasy recomputes `stepInputMessages := append(initialPrompt,
responseMessages...)` at the top of every step
(`fantasy@v0.27.0-fork/agent.go:823`) and only appends the model's own
output + tool results to `responseMessages` (`agent.go:943-944`). Anything
`PrepareStep` adds to `prepared.Messages` is used for that single API call
and then discarded. Crush's `PrepareStep` starts from `options.Messages`
each time (`run.go:466`), so after the next tool call the model no longer
sees the `/btw` text; it reappears only on the next *turn* when the
history is reloaded from the DB.

Fix (local to `run.go`): keep a closure slice in `Run`, e.g.
`folded []struct{ idx int; msgs []fantasy.Message }`. When folding, record
`idx = len(options.Messages)` before appending. On every later
`PrepareStep`, re-insert each recorded fold at its `idx`.
`options.Messages` is a stable prefix + growing suffix, so the recorded
index lands exactly after the tool results the fold originally followed,
preserving the tool_use/tool_result adjacency the provider needs. Add a
multi-step test alongside `agent_test.go:241`.

### Recommendation

1. Fix the one-step bug above first; everything else builds on it.
2. Wrap the folded text like Claude Code does ("The user sent a new
   message while you were working … you MUST address it") instead of the
   raw `[btw] ` prefix; keep the `BTW` flag on the message so the UI can
   label it.
3. Product decision, either:
   - **(a) Claude Code parity:** Enter-while-busy → steer (empty RunID),
     modifier (alt+enter or a `/queue` prefix) → queue as own turn
     (RunID). `crush run`, swarm and other API callers keep RunID
     semantics unchanged.
   - **(b) Conservative:** keep Enter = queue, add a "send now" key that
     calls `sendBTWMessage`. `ctrl+b` is taken by Search
     (`internal/ui/model/keys.go:130`); candidates: alt+enter, ctrl+enter
     (check terminal support), or `ctrl+j`.
4. Show pending folds in the pills with a distinct label ("steering" vs
   "queued"); `AgentQueuedPromptsList` already exists for the list.
5. Do **not** copy `now` (abort-to-deliver) yet. Crush's `Cancel` cancels
   the run context wholesale, which kills the bash shell via `ctx.Done()`
   (`internal/agent/tools/bash.go:325-329`). A soft interrupt needs the
   per-tool-call registry described in §2; once that exists, `now` is the
   same mechanism with a different outcome.

## 2. Backgrounding a running command on request

### How Claude Code does it

**Signal mechanism.** No channel or abort. After a command has run for
`PROGRESS_THRESHOLD_MS = 2000` (`tools/BashTool/BashTool.tsx:55,1110-1125`)
the tool registers itself as a *foreground task* in app state
(`tasks/LocalShellTask/LocalShellTask.tsx:259 registerForeground`, state
holds the live `ShellCommand`) and injects a `<BackgroundHint/>`
(`tools/BashTool/UI.tsx:31-84`) reading "(ctrl+b to run in background)".
Keybinding `keybindings/defaultBindings.ts:181` (`ctrl+b` →
`task:background`, context `Task`; tmux users see "ctrl+b ctrl+b").
Handler → `LocalShellTask.tsx:390 backgroundAll` → `backgroundTask`
(L293) → `utils/ShellCommand.ts:349 background(taskId)`: flips
`#status = 'backgrounded'`, drops JS listeners, keeps output flowing to
the on-disk file (or `spillToDisk()` in pipe mode). The tool's poll loop
(`BashTool.tsx:1036,1089-1102`, ~1s tick) notices the status and returns
`{stdout:'', backgroundTaskId, backgroundedByUser:true}`.

**tool_result text** (`BashTool.tsx:606-622`): "Command was manually
backgrounded by user with ID: {id}. Output is being written to: {path}".
Auto/timeout variant: "Command running in background with ID: …".
`is_error` is false.

**Other background paths** in `runShellCommand` (`BashTool.tsx:850-1143`):
`run_in_background: true` (spawns as a background task before any wait);
timeout auto-background (`ShellCommand.ts:135 #handleTimeout`, disallowed
for `sleep`, telemetry `tengu_bash_command_timeout_backgrounded`); a 15s
"assistant-mode" blocking budget. All reuse the already-registered
foreground task to avoid duplicate events
(`LocalShellTask.tsx:420 backgroundExistingForegroundTask`).

**Completion → model.** `LocalShellTask.tsx:105 enqueueShellNotification`
runs from `shellCommand.result.then(...)` and pushes a
`mode: 'task-notification'` command into the same queue as steering
(`enqueuePendingNotification`, default priority `later`, `next` in some
paths). Body is XML-ish: `<task-notification>` with `task-id`,
`tool-use-id`, `output-file`, `status` (completed|failed|killed) and a
`summary` like "Background command "{description}" completed (exit code
N)". Drained mid-turn at `query.ts:1570` or starts a fresh turn when idle
(`queueProcessor.ts:52`); converted with `isMeta: true` and origin
`task-notification` so it is not rendered as a user bubble
(`messages.ts:3739-3746`; wrapper "A background agent completed a task:").
Dedup via an atomic `notified` flag; the race where the command finishes
before the loop observes backgrounding is handled at `BashTool.tsx:1047`
(`markTaskNotified`, strip the id). A stall watchdog (L46-104) emits a
warning after 45s of no output growth when the tail looks like an
interactive prompt. Consecutive notifications are collapsed for display
(`utils/collapseBackgroundBashNotifications.ts`).

**Prompt guidance.** `tools/BashTool/prompt.ts:39,317,319`: use
`run_in_background` for long-running commands, "you will be notified when
it completes — do not poll". `TaskOutputTool` is deprecated in favor of
reading the output file.

**UI.** Hint via `setToolJSX` only after 2s; progress row
`components/shell/ShellProgressMessage.tsx` shows "Running…" + elapsed +
tail of output; session-level hint `components/SessionBackgroundHint.tsx`.

### Crush today

- `internal/agent/tools/bash.go:291-372`: every command already starts
  via `shell.GetBackgroundShellManager().Start(context.Background(), …)`
  (detached context) and is polled in `waitLoop` with a 100ms ticker;
  after `auto_background_after` (default 60s) it returns "Command is
  taking longer than expected and has been moved to background… Background
  shell ID: X" with `BashResponseMetadata{Background: true, ShellID}`;
  on `ctx.Done()` it kills the shell. `run_in_background: true` returns
  immediately (L278-289).
- `internal/shell/background.go`: `BackgroundShellManager` (max 50 jobs,
  8h retention, `Kill` with 5s grace), `BackgroundShell` records
  `completedAt` in the runner goroutine (L124-131). No completion hook.
- `internal/agent/tools/job_output.go` (with `wait`) and `job_kill.go`
  are the polling tools; no notification path exists, so the model must
  poll.
- The tool function receives `call fantasy.ToolCall` with `call.ID`
  (`bash.go:200`), and ctx carries `SessionIDContextKey` /
  `MessageIDContextKey` (`internal/agent/tools/tools.go:31-38`).
- Cancel plumbing to mirror: `POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel`
  (`internal/server/server.go:163`) → `coordinator.Cancel`
  (`internal/agent/coordinator.go:1800`) → `sessionAgent.Cancel`;
  client side `ClientWorkspace.AgentCancel`
  (`internal/workspace/client_workspace.go:372`); TUI `cancelAgent`
  (`internal/ui/model/cancel.go:30`).
- TUI rendering of a running bash call: `internal/ui/chat/bash.go:43`
  (`pendingTool(...)` while `opts.IsPending()`); when the result carries
  `meta.Background` it already renders a job header (L57-61). Spinning
  state is `!toolCall.Finished` (`internal/ui/chat/tools.go:516-524`).

### Recommendation

Copy the shape (status flip observed by the tool's own poll loop; result
is a normal non-error tool_result naming the job id; completion is a
queued notification delivered through the steering path), differ in
mechanism (Crush is client/server, so the signal must cross the socket and
a channel is more idiomatic than a status field).

1. **Soft-interrupt registry** (`internal/agent/tools/interrupt.go`, new):
   `type ToolInterrupts` keyed by tool call ID (optionally scoped by
   session) with `Register(callID) <-chan struct{}` / `Background(callID)
   bool` / `IsRunning(callID) bool`; injected via ctx
   (`tools.WithInterrupts`) next to `SessionIDContextKey`. In `bash.go`
   register at the start of `waitLoop` and add `case <-bgCh:` that takes
   the existing "still running, keep as background job" branch with a
   distinct message ("Command was moved to the background by the user.
   Background shell ID: %s. Use job_output (wait=true to block) or
   job_kill.") and `BashResponseMetadata{Background: true, ShellID,
   BackgroundedByUser: true}`. Don't kill on later `ctx.Done()`; the tool
   has returned and the shell is already on `context.Background()`.
2. **Transport**: `POST /v1/workspaces/{id}/agent/sessions/{sid}/tools/{tool_call_id}/background`
   beside `/cancel`; `coordinator.BackgroundTool(sessionID, toolCallID)
   bool`; `ClientWorkspace.AgentBackgroundTool`. Unknown/finished id → 404.
3. **TUI**: after ~2s show a hint in the pending bash item
   (`internal/ui/chat/bash.go:43`), bind a key (not `ctrl+b`; `ctrl+j` or
   `alt+b`), find the current session's unfinished `bash` tool call from
   the chat list, call `AgentBackgroundTool`, add the binding to
   `FullHelp()` in `internal/ui/model/ui.go` (AGENTS.md rule).
4. **Completion notification** (Crush has none): add an `OnDone` hook (or
   per-shell `Done()` channel) to `BackgroundShellManager`; for shells
   started by a tool call in session S, enqueue a RunID-less
   `SessionAgentCall` on S — folds at the next step if busy (§1 path),
   runs as a short turn if idle — with a `<background-job-notification>`
   body (shell id, status, exit code, summary) and a meta flag so the UI
   renders it as a system note rather than a user bubble (`SwarmParts` is
   the precedent for structured user-message parts). Guard with a
   `notified` flag; skip if the model already saw completion via
   `job_output`. Update `bash.md`/`job_output.md` descriptions to say
   "you will be notified; don't poll". Independent of 1-3; ship second.

## 3. Graceful update / server handoff

### How Claude Code does it

Almost nothing: side-by-side install plus a passive banner.

- **Install layout** (`utils/nativeInstaller/installer.ts`):
  `versions/<ver>` under XDG data, `staging/`, `locks/`; `~/.local/bin/claude`
  is a symlink swapped atomically (tmp symlink + `rename`,
  `updateSymlink` L640-798; on Windows the running exe is renamed to
  `.old.<ts>` and cleaned up next start, L671-688, L1191-1216). The
  running process keeps executing the old version file it resolved at
  launch. `lockCurrentVersion()` (L1048, called from `setup.ts:303`) takes
  a PID lock so `cleanupOldVersions()` (L1184) never deletes a binary under
  a live session. Server kill switch `tengu_max_version_config`
  (`utils/autoUpdater.ts:110-133`); per-version install lock
  (`tryWithVersionLock` L580).
- **Cadence**: `components/AutoUpdater.tsx:164` and
  `NativeAutoUpdater.tsx:157` check on mount and every 30 min, guarded
  only by an `isUpdatingRef`; there is no reference to
  `queryGuard`/`isQueryActive` anywhere in updater code, so an install can
  land mid-turn (harmless given the above).
- **Notification only**: `hooks/useUpdateNotification.ts:16-34` dedups by
  version and shows "✓ Update installed · Restart to update"
  (`NativeAutoUpdater.tsx:182`) / "Restart to apply" (`AutoUpdater.tsx:188`)
  / "Update available! Run: brew upgrade claude-code"
  (`PackageManagerAutoUpdater.tsx:87`). No re-exec (`spawn(process.execPath`,
  `execve`, `relaunch` have zero hits), no restart-when-idle, no
  `process.exit` except in the separate `claude update` CLI
  (`cli/update.ts`). `assertMinVersion()` (`autoUpdater.ts:70`) is
  hard-disabled.
- **Resume makes restart cheap**: JSONL transcript per session
  (`utils/sessionStorage.ts:202 getTranscriptPath`), each entry stamped
  with `version`; the user message is written *before* the API call
  (`QueryEngine.ts:436-451`) so resume works "from the point the user
  message was accepted, even if no API response ever arrives";
  `utils/conversationRecovery.ts:262 detectTurnInterruption()` treats a
  trailing user/tool_use as an interrupted turn and auto-continues on
  `--resume`/`-c`. The in-memory `commandQueue` and pending permission
  prompts are logged as `queue-operation` entries for analytics but never
  replayed: **lost on restart**.
- **Version handshake**: no local client/server split; no
  `PROTOCOL_VERSION`. Only server-driven minimum-version floors for the
  Remote Control bridge (`bridge/bridgeEnabled.ts:160`,
  `envLessBridgeConfig.ts:147`: "too old… run `claude update`") and a
  `x-environment-runner-version` header (`bridge/bridgeApi.ts:82`). MCP
  `serverInfo.version` is standard.

### Crush today

- Client/server over a unix socket. `internal/cmd/root.go:559
  restartIfStale` compares `Version`+`BuildID` from `/v1/version` and, on
  any mismatch, calls `ShutdownServer`, waits for the socket to
  disappear, and force-removes it — cancelling every in-flight run in
  every workspace (`internal/backend/backend.go:260 Workspace.Shutdown`
  → `Permissions/Questions.CancelAll`, ctx cancel,
  `AgentCoordinator.CancelAll`).
- `internal/ui/model/compose.go:74-79` already preserves an unsent prompt
  on `versionMismatch` and tells the user to restart.
- `sessionAgent.messageQueue` (queue.go) and the swarm `ReplyTracker` are
  in-memory.
- `createUserMessage` persists the user message before streaming; the
  assistant message is created per step in `PrepareStep`; tool results are
  persisted in `OnToolResult`. `preparePrompt` synthesizes error results
  for orphaned tool calls ("tool call was interrupted… you may retry"), so
  a session killed mid-tool is resumable. `continueTurn`
  (`compose.go:continueTurn`) is a manual `[continue]` kick.

### Recommendation

Nothing to copy mechanically; Crush's architecture already exceeds Claude
Code here. Ideas worth borrowing, ranked:

1. **Never kill a busy server on version mismatch.** Add a
   `ProtocolVersion` to `/v1/version`; if it matches, connect regardless
   of build. If it does not and any workspace `IsBusy()`, do not shut
   down; show a "server busy, restart when idle" banner (extend the
   `versionMismatch` path).
2. **Drain, then hand off.** `POST /v1/control {action: shutdown, mode:
   drain}`: stop accepting `POST /agent` (409 "draining"), wait bounded for
   `IsBusy()` to drop, exit; clients poll `/v1/health` for `draining`.
   Persist or re-post the small `messageQueue` (`enqueueCall` already
   strips the non-serializable hooks) so queued prompts are not lost the
   way Claude Code's are.
3. **Auto-resume interrupted sessions.** Mark sessions busy at shutdown;
   on the new server's first attach, auto-issue the existing `[continue]`
   prompt for them (Crush's `toolResultsForAssistant` is the analogue of
   `detectTurnInterruption`).
4. **Binary lifetime.** The `go build -o crush.new && mv` dance in the
   global AGENTS.md is the correct analogue of `atomicMoveToInstallPath`;
   nothing further needed on macOS/Linux.
