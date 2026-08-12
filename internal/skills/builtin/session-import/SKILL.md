---
name: session-import
description: Use when the user wants to import, migrate, or pull in a session or conversation transcript from another coding agent (Claude Code, Codex, Grok Build, or Pi) into Crush, or asks how to re-import / sync an external session.
---

# Session Import — Bring External Transcripts Into Crush

Crush can import conversation transcripts from other coding-agent harnesses
and turn them into native Crush sessions. This is driven through Crush's own
CLI, which you can invoke from the bash tool.

## Supported Harnesses

| Harness      | `--from` value | Source location                 |
| ------------ | -------------- | ------------------------------- |
| Claude Code  | `claude`       | `~/.claude/projects`            |
| Codex        | `codex`        | `~/.codex/sessions`             |
| Grok Build   | `grok`         | `~/.grok/sessions`              |
| Pi           | `pi`           | `~/.pi/agent/sessions`          |
| (autodetect) | `auto`         | detected from the file contents |

## Command

```bash
crush session import <path> [--from auto|claude|codex|grok|pi] [--json]
```

- `<path>` is the transcript file (or, for Grok, the session directory).
- `--from` defaults to `auto`, which detects the harness from the file.
- `--json` emits the structured `Result` (id, source, source_id, title,
  messages, imported, warnings, already_exists, modified) instead of a
  human-readable line. Prefer `--json` when you need to parse the outcome.

## Re-import Semantics (important)

Imports are **idempotent and consistent**: the Crush session ID and each
message ID are derived deterministically from the harness + source ID, so
re-importing the same transcript maps to the same session.

- **First import** — writes the whole transcript.
- **Re-sync** — if the source gained messages since the last import, only the
  new tail is appended; unchanged transcripts are a no-op (`already_exists`).
- **Never clobbers local work** — if the user continued the session inside
  Crush, or deleted its imported messages, the import is skipped and reported
  as `modified` rather than overwriting.

Because IDs are stable, it is safe to re-run an import to pull in new turns.

## Discovering What Can Be Imported

There is no `list` subcommand; the interactive picker (command palette →
"Import Sessions") is the discovery UI for humans. From the CLI, locate
transcripts directly under the source roots above, e.g.:

```bash
ls -t ~/.codex/sessions/**/*.jsonl | head
```

Then import a specific one:

```bash
crush session import ~/.codex/sessions/2026/01/01/rollout-....jsonl --json
```

## Notes

- Malformed JSONL lines are skipped and surfaced as warnings, not fatal
  errors.
- Generated/system context (instruction blocks, environment context,
  command scaffolding) is filtered out; only real conversation turns import.
- The import writes into the current workspace's database, so run it from
  the workspace you want the session to appear in.
