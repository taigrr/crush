# Driving Crush from other tools

Crush can be run by scripts, CI jobs, and other coding agents (Claude Code,
clawdbot, Codex, ...) without a person at the TUI.
Every invocation talks to the same local Crush server as the TUI, so a run
started by another program gets the full Crush loop: LSP diagnostics, skills,
MCP servers, hooks, and snapshots.

## `crush run`

`crush run` sends a single prompt and exits when the agent finishes its turn.
The prompt comes from the arguments, from stdin, or both.

```sh
crush run "Fix the failing test in internal/config"
git diff | crush run "Review this diff"
```

### Output formats

Use `--output-format` (`-o`) to choose how results are written to stdout.

| Format        | Output                                                       |
| ------------- | ------------------------------------------------------------ |
| `text`        | The final assistant text (default, unchanged behavior).      |
| `json`        | A single result object once the run ends.                    |
| `stream-json` | One JSON event per line as the run progresses, then a result. |

The spinner is never shown in the JSON formats, and logs only go to stderr
with `--verbose`.

#### Result object

The `json` format prints exactly one object, and `stream-json` ends with the
same object.
Errors that happen before the run starts (bad flags, server unreachable, no
providers configured) are reported the same way, so callers can always parse
stdout.

```json
{
  "type": "result",
  "status": "completed",
  "exit_code": 0,
  "session_id": "7f0c3b9e-...",
  "address": "coral-otter-3b9e",
  "workspace_id": "a1b2...",
  "run_id": "c3d4...",
  "message_id": "e5f6...",
  "cwd": "/home/me/project",
  "text": "Fixed the off-by-one in loadConfig and added a regression test.",
  "duration_ms": 48213,
  "usage": { "prompt_tokens": 51234, "completion_tokens": 2210, "cost_usd": 0.31 },
  "files_changed": ["/home/me/project/internal/config/load.go"],
  "diagnostics": {
    "errors": 0,
    "warnings": 1,
    "items": [
      {
        "file": "/home/me/project/internal/config/load.go",
        "line": 42,
        "column": 2,
        "severity": "warning",
        "message": "unused parameter: ctx",
        "source": "gopls"
      }
    ]
  },
  "attach_command": "crush --cwd /home/me/project --session 7f0c3b9e-..."
}
```

- `status` is `completed`, `error`, `cancelled`, `blocked`,
  `permission_denied`, `timeout`, or `running` (with `--detach`).
- `usage` counts only what this run consumed, not the whole session.
- `files_changed` lists files the agent edited during this run.
- `diagnostics` reports LSP errors and warnings still present in those files.
  It is omitted when no files changed or no LSP is running.
- `address` is the session's swarm address.
- `pending` lists what a `blocked` run is waiting on (see
  [Permissions](#permissions)).
- `attach_command` opens the session in the TUI so a person can review or
  take over.

#### Stream events

In `stream-json` mode every line is a JSON object with a `type` field.

| Type          | When                                     | Notable fields                       |
| ------------- | ---------------------------------------- | ------------------------------------ |
| `init`        | Before the prompt is sent.               | `session_id`, `address`, `run_id`, `provider`, `model`, `attach_command` |
| `text`        | New assistant text was streamed.         | `message_id`, `text` (the new chunk) |
| `tool_call`   | A tool call's input finished streaming.  | `tool_call_id`, `name`, `input`      |
| `tool_result` | A tool returned.                         | `tool_call_id`, `content` (max 4 KiB), `truncated`, `is_error` |
| `blocked`     | The run is waiting on a prompt.          | `pending`                            |
| `result`      | The run ended.                           | See above.                           |

Unknown event types may be added later; consumers should ignore them.

### Exit codes

| Code | Meaning                                                      |
| ---- | ------------------------------------------------------------ |
| 0    | The run completed (or was started with `--detach`).          |
| 1    | Generic failure (bad flags, server unreachable, ...).        |
| 2    | Blocked on a permission request or question.                 |
| 3    | A tool call was denied and the run stopped.                  |
| 4    | The run was cancelled (Ctrl+C, SIGTERM, or another client).  |
| 5    | The agent run failed (provider error, ...).                  |
| 124  | `--timeout` elapsed before the run finished.                 |

Interrupting `crush run` also cancels the run on the server, so it doesn't
keep using tokens in the background.

### Permissions

`--permission-mode` decides what happens when the agent wants to run a tool
that needs approval.
The mode applies to this run's own turn and to work it starts on its behalf:
sub-agents and the follow-up turn when a background job it launched finishes.
Messages it sends to other sessions with the swarm tool carry `deny` along,
but never `yolo` or `delegate`: the receiving session keeps its own prompting.
Other turns on the same session keep the usual behavior, including ones a
person sends from the TUI or swarm messages from elsewhere that arrive while
the run is going; those run as their own turn afterwards.
`deny` also ignores "allow for this session" grants made earlier in the
session.

| Mode       | Behavior                                                                 |
| ---------- | ------------------------------------------------------------------------ |
| `yolo`     | Approve everything (default, the historical `crush run` behavior).       |
| `deny`     | Deny anything not pre-approved by `allowed_tools` or a `PreToolUse` hook. A denied call of the run itself ends the turn with exit code 3; a denial inside a sub-agent ends only that sub-agent. Turning on yolo mode in the TUI does not override it. |
| `delegate` | Leave the request pending and exit with code 2 and `status: "blocked"`. The run stays alive on the server until it is answered with `crush session answer`. Questions from the agent are delegated the same way. Turning on yolo mode in the TUI does not auto-approve a delegated run. |

In `yolo` and `deny` mode the agent's question tool fails right away, so the
agent proceeds on its own assumptions instead of waiting.

`--permission-mode yolo` without `--timeout`, `--detach`, or JSON output
keeps the original live-streaming text output; the other modes print the
final text when the run ends.

### Background runs and timeouts

- `--detach` starts the run, prints a `running` result with the session ID,
  and exits immediately.
- `--timeout 9m` stops waiting after the given time and exits with code 124.
  The run keeps going, and `crush session wait` picks it up again.
  Use a timeout shorter than your caller's own tool timeout (Claude Code's
  Bash tool allows at most 10 minutes).

### Shared and isolated workspaces

By default `crush run` joins the same workspace as any TUI or other client in
that directory.
That makes the run visible to `crush session` commands and lets a person open
it with `attach_command` while it is still going.

`--isolated` runs in a private workspace instead.
It cannot be combined with `--detach` or `--permission-mode delegate`, since
nothing could reach the run afterwards, and an isolated run that times out is
cancelled.
`--small-model`, or `--model` together with `--session`/`--continue`, have to
rewrite the workspace's model configuration, so they switch to an isolated
workspace automatically.
`--model` on a new session sets that session's model only.

### Sessions

Each run creates a new session unless `--session <id>` or `--continue` is
given.
The session ID in the result can be passed to `--session` to continue the
conversation later.

## Controlling running sessions

These commands take a session ID, a hash prefix from `crush session list`, or
the session's swarm address, and accept `-o json` like `crush run`.
Sessions are looked up in the workspace for the current directory first, then
across every workspace the server knows about, so a swarm address works from
anywhere.

| Command                      | Purpose                                                       |
| ---------------------------- | ------------------------------------------------------------- |
| `crush session status <id>`  | Report `running`, `blocked` (with `pending`), or `idle` (with the last turn). |
| `crush session wait <id>`    | Wait until the run finishes or blocks; prints a result. `--timeout` exits with 124. Usage and files are session totals. |
| `crush session answer <id>`  | `--allow`, `--allow-session`, `--deny`, `--answer <text>` (repeatable), or `--cancel-question`. `--request <id>` picks one when several are pending. `--wait` waits for the result afterwards. |
| `crush session cancel <id>`  | Cancel the running turn.                                       |

Interrupting `crush session wait` never cancels the run.

### Example: delegating from another agent

```sh
# Start the work, but leave every approval to the caller.
crush run -o json --permission-mode delegate --timeout 9m "Upgrade the Go dependencies"
# => exit 2, {"status":"blocked","pending":{"permissions":[{"id":"…","tool_name":"bash","description":"go get -u ./..."}]},…}

# Approve it and keep going.
crush session answer 7f0c3b9e --allow --wait --timeout 9m -o json
# => exit 0, {"status":"completed","files_changed":[…],"diagnostics":{…},…}
```

## MCP server

`crush mcp` serves the same loop over the Model Context Protocol on
stdin/stdout, which is the most natural way for another agent to use Crush.

```sh
# Claude Code
claude mcp add crush -- crush mcp

# Any MCP client: run "crush mcp" as a stdio server in the project directory.
```

| Tool            | Purpose                                                                 |
| --------------- | ----------------------------------------------------------------------- |
| `crush_start`   | Start a run (or a follow-up with `session`) and return right away with the session, address, and `attach_command`. Accepts `cwd`, `model` (role name, `provider/model`, or model ID), `permission_mode`, and `title`. |
| `crush_wait`    | Wait up to `timeout_seconds` (default 300, max 540) and return the same result object as `crush run -o json`, with usage and files changed as session totals. Call again on `timeout`. |
| `crush_status`  | Report `running`, `blocked`, or `idle` without waiting.                 |
| `crush_respond` | Answer a blocked run: `decision` (`allow`, `allow_session`, `deny`), `answer`, or `cancel_question`; `wait: true` waits afterwards. |
| `crush_cancel`  | Cancel the running turn.                                                |
| `crush_list`    | List a project's sessions, newest first.                                |
| `crush_diff`    | Unified diff of every file the session changed.                         |

Runs started over MCP default to `permission_mode: "delegate"`, so a tool call
that needs approval comes back to the calling agent as `status: "blocked"`
instead of running unasked, even if someone turns on yolo mode in the TUI.
The server's `--permission-mode` is also the most permissive mode a caller may
request: under the default, `crush_start` accepts `delegate` or `deny` but
rejects `yolo`.
Start the server with `--permission-mode yolo` to let callers approve their
own runs, or `deny` to forbid delegation too (then `crush_respond` only
accepts `decision: "deny"`).

`crush_respond`, `crush_cancel`, and follow-ups with `crush_start` only work
on sessions the same `crush mcp` process started, so a caller cannot use
them to approve prompts in, or send work to, a person's TUI session.
(A run's agent can still message other sessions with its swarm tool; those
messages never carry auto-approval.)
The read-only tools work on any session.
Start the server with `--allow-foreign-sessions` to lift that restriction.

Long waits send MCP progress notifications every 15 seconds when the client
asks for progress, which keeps clients that reset their timeout on progress
from giving up.
`--max-wait` lowers the 9 minute cap on a single `crush_wait` for clients with
shorter tool timeouts.

The MCP server keeps each project's workspace attached while it runs, so LSPs
stay warm between calls, and any session it starts can be opened in the TUI
with its `attach_command` to watch or take over.

## Agent skill

`crush skill print` prints a `SKILL.md` that teaches another agent when and
how to hand work to Crush, using the MCP tools or the CLI above.
Save it wherever your agent loads skills from, or paste it into its
instructions.
The source lives in
[internal/headless/agent_skill.md](../internal/headless/agent_skill.md).

## Security

The Crush server listens on a per-user unix socket (`/tmp/crush-<uid>.sock`)
or named pipe by default; any process running as the same user can drive it,
exactly like the TUI.
Do not expose it with `--host tcp://...` on a shared or untrusted network.
