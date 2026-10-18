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

- `status` is `completed`, `error`, or `cancelled`.
- `usage` counts only what this run consumed, not the whole session.
- `files_changed` lists files the agent edited during this run.
- `diagnostics` reports LSP errors and warnings still present in those files.
  It is omitted when no files changed or no LSP is running.
- `address` is the session's swarm address.
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
| `result`      | The run ended.                           | See above.                           |

Unknown event types may be added later; consumers should ignore them.

### Exit codes

| Code | Meaning                                                   |
| ---- | --------------------------------------------------------- |
| 0    | The run completed.                                        |
| 1    | Generic failure (bad flags, server unreachable, ...).     |
| 4    | The run was cancelled (Ctrl+C, SIGTERM, or another client). |
| 5    | The agent run failed (provider error, ...).               |

Interrupting `crush run` also cancels the run on the server, so it doesn't
keep using tokens in the background.

### Sessions

Each run creates a new session unless `--session <id>` or `--continue` is
given.
The session ID in the result can be passed to `--session` to continue the
conversation later.
