---
name: crush
description: Delegate a self-contained coding task to Crush, a separate coding agent with LSP diagnostics, project skills, MCP servers, snapshots, and parallel sessions. Use when the user says "use crush", "/crush", or asks to hand off, parallelize, or background a multi-file change, refactor, migration, or test fix.
---

# Delegating to Crush

Crush runs its own full agent loop and reports back with the final answer, the files it changed, and the LSP errors left in them.
Prefer the `crush_*` MCP tools when they are available (Crush serves them with `crush mcp` over stdio); otherwise use the CLI below.

## Write the task

Crush does not see this conversation.
Give it everything it needs in one prompt: the goal, the relevant files or symbols, constraints, and how to verify (for example "run `go test ./internal/config/...`").
Use one Crush session per independent task; several can run in parallel.

## With the MCP tools

1. `crush_start` with `prompt` (and `cwd` if the task is in another project). Keep the returned `session_id`.
2. `crush_wait` with the session. Repeat while `status` is `timeout`.
3. If `status` is `blocked`, read `pending`:
   - For a permission request, decide (or ask the user when it is risky, such as deleting files, pushing, or network access) and call `crush_respond` with `decision` `allow`, `allow_session`, or `deny`, and `wait: true`.
   - For a question, answer it with `crush_respond` `answer`.
4. When `status` is `completed`, check `diagnostics.errors` and `files_changed`, look at `crush_diff`, and verify before reporting success.
5. For follow-ups, call `crush_start` again with the same `session`.

`permission_denied` means a tool call was refused and the run stopped; `error` and `cancelled` mean it did not finish.
Tell the user the session's `attach_command` if they want to watch or take over in the Crush TUI.

## With the CLI

Run from the project directory. Keep each command under the Bash tool's timeout with `--timeout`.

```sh
crush run -o json --permission-mode delegate --timeout 9m "<task>"
crush session wait <session_id> --timeout 9m -o json      # exit 124: still running, wait again
crush session status <session_id> -o json                 # what a blocked run (exit 2) is waiting on
crush session answer <session_id> --allow --wait --timeout 9m -o json
crush session answer <session_id> --answer "<reply>" --wait --timeout 9m -o json
crush session cancel <session_id>
```

Exit codes: 0 completed, 2 blocked, 3 permission denied, 4 cancelled, 5 agent error, 124 timeout, 1 other failure.
