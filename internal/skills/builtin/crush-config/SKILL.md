---
name: crush-config
description: Use when the user needs help configuring Crush — working with crush.json, setting up providers, configuring LSPs, adding MCP servers, managing skills or permissions, or changing Crush behavior.
---

# Crush Configuration

Crush uses JSON configuration files with the following priority (highest to lowest):

1. `.crush.json` (project-local, hidden)
2. `crush.json` (project-local)
3. `$XDG_CONFIG_HOME/crush/crush.json` or `$HOME/.config/crush/crush.json` (global)

## Basic Structure

```json
{
  "$schema": "https://charm.land/crush.json",
  "models": {},
  "providers": {},
  "mcp": {},
  "lsp": {},
  "hooks": {},
  "options": {},
  "permissions": {},
  "tools": {}
}
```

The `$schema` property enables IDE autocomplete but is optional.

## Shell Expansion

Crush runs selected string fields through an embedded bash-compatible
shell at load time, so values can pull from env vars, files, or helper
commands.

Supported constructs (match the `bash` tool):

- `$VAR` and `${VAR}`
- `${VAR:-default}`, `${VAR:+alt}`, `${VAR:?message}`
- `$(command)` with full quoting and nesting
- Single- and double-quoted strings, escapes

Default semantics match bash: an unset variable expands to an empty
string, no error. A failing `$(command)` is always a hard error. For
required credentials, use `${VAR:?message}` so a missing variable
fails loudly at load time with your message.

```json
{ "api_key": "${CODEBERG_TOKEN:?set CODEBERG_TOKEN}" }
```

### Which fields expand

| Surface                                        | Expansion                          |
| ---------------------------------------------- | ---------------------------------- |
| Provider `api_key`, `base_url`, `api_endpoint` | yes                                |
| Provider `extra_headers`                       | yes                                |
| Provider `extra_body`                          | **no**                             |
| MCP `command`, `args`, `env`, `headers`, `url` | yes                                |
| LSP `command`, `args`, `env`                   | yes                                |
| Hook `command`                                 | runs via `sh -c`, not the resolver |

`extra_body` is a JSON passthrough. If you need env-driven values in
a request body, put them in `extra_headers`, `api_key`, or
`base_url` instead.

### Empty-resolved headers are dropped

When a header value resolves to the empty string (unset variable,
`$(echo)`, or literal `""`), the header is omitted from the
outgoing request. This keeps optional env-gated headers like
`"OpenAI-Organization": "$OPENAI_ORG_ID"` working cleanly when the
var isn't set. Applies to MCP `headers` and provider `extra_headers`.

### Security note

`crush.json` is trusted code. Any `$(...)` in it runs at load time
with the invoking user's shell privileges, before the UI appears.
Don't launch Crush in a directory whose `crush.json` you haven't
reviewed.

## Common Tasks

- Add a custom provider: add an entry under `providers` with `type`, `base_url`, `api_key`, and `models`.
- Disable a builtin or local skill: add the skill name to `options.disabled_skills`.
- Add an MCP server: add an entry under `mcp` with `type` and either `command` (stdio) or `url` (http/sse).

## Model Selection

```json
{
  "models": {
    "large": {
      "model": "claude-sonnet-4-20250514",
      "provider": "anthropic",
      "max_tokens": 16384
    },
    "small": {
      "model": "claude-haiku-4-20250514",
      "provider": "anthropic"
    }
  }
}
```

- `large` is the primary coding model; `small` is for summarization.
- Only `model` and `provider` are required.
- Optional tuning: `reasoning_effort`, `think`, `max_tokens`, `temperature`, `top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `provider_options`.

## Custom Providers

```json
{
  "providers": {
    "deepseek": {
      "type": "openai-compat",
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "$DEEPSEEK_API_KEY",
      "models": [
        {
          "id": "deepseek-chat",
          "name": "Deepseek V3",
          "context_window": 64000
        }
      ]
    }
  }
}
```

- `type` (required): `openai`, `openai-compat`, or `anthropic`
- `api_key`, `base_url`, `api_endpoint`, and `extra_headers` are shell-expanded (see [Shell Expansion](#shell-expansion)).
- `extra_body` is a JSON passthrough and is **not** expanded.
- Additional fields: `disable`, `system_prompt_prefix`, `extra_headers`, `extra_body`, `provider_options`.

## LSP Configuration

```json
{
  "lsp": {
    "go": {
      "command": "gopls",
      "env": { "GOPATH": "$HOME/go" }
    },
    "typescript": {
      "command": "typescript-language-server",
      "args": ["--stdio"]
    }
  }
}
```

- `command` (required), `args`, `env` cover most setups.
- `command`, `args`, and `env` values are shell-expanded (see [Shell Expansion](#shell-expansion)).
- Additional fields: `disabled`, `filetypes`, `root_markers`, `init_options`, `options`, `timeout`.

## MCP Servers

```json
{
  "mcp": {
    "filesystem": {
      "type": "stdio",
      "command": "node",
      "args": ["/path/to/mcp-server.js"]
    },
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {
        "Authorization": "Bearer $GH_PAT"
      }
    }
  }
}
```

- `type` (required): `stdio`, `sse`, or `http`
- `command`, `args`, `env`, `headers`, and `url` are shell-expanded (see [Shell Expansion](#shell-expansion)).
- Additional fields: `env`, `disabled`, `disabled_tools`, `timeout`.

## Options

```json
{
  "options": {
    "skills_paths": ["./skills"],
    "disabled_tools": ["bash", "sourcegraph"],
    "disabled_skills": ["crush-config"],
    "tui": {
      "compact_mode": false,
      "diff_mode": "unified",
      "transparent": false,
      "theme": "charmtone"
    },
    "auto_lsp": true,
    "debug": false,
    "debug_lsp": false,
    "attribution": {
      "trailer_style": "assisted-by",
      "generated_with": true
    }
  }
}
```

> [!IMPORTANT]
> The following skill paths are loaded by default and DO NOT NEED to be added to `skills_paths`:
> `.agents/skills`, `.crush/skills`, `.claude/skills`, `.cursor/skills`

Other options: `context_paths`, `progress`, `disable_notifications`, `disable_auto_summarize`, `disable_metrics`, `disable_provider_auto_update`, `disable_default_providers`, `data_directory`, `initialize_as`.

## Themes

`options.tui.theme` selects the UI color theme by name. Because local config
overrides global, a theme can be set per-workspace (set it in a project
`crush.json` to override the global one).

Builtin themes: `charmtone` (default), `hypercrush`, `tokyo-night`,
`catppuccin-mocha`, `dracula`, `nord`, `gruvbox-dark`, `rose-pine`.

User themes are Lua files in `$XDG_CONFIG_HOME/crush/themes/*.lua` (typically
`~/.config/crush/themes/`). Each file returns a table with a `name`, an
optional `is_dark` boolean (default `true`), and hex color fields. Omitted
fields fall back to the default palette, so partial themes work. A theme
whose `name` collides with a builtin is ignored.

```lua
-- ~/.config/crush/themes/midnight.lua
return {
  name = "Midnight",
  is_dark = true,

  -- brand
  primary = "#7c6f9f",
  secondary = "#f5e0dc",
  accent = "#a6e3a1",
  keyword = "#f38ba8",

  -- foreground ramp
  fg_base = "#cdd6f4",
  fg_subtle = "#bac2de",
  fg_more_subtle = "#a6adc8",
  fg_most_subtle = "#6c7086",
  on_primary = "#1e1e2e",

  -- background ramp
  bg_base = "#1e1e2e",
  bg_least_visible = "#181825",
  bg_less_visible = "#313244",
  bg_most_visible = "#45475a",
  separator = "#313244",

  -- statuses
  destructive = "#eba0ac",
  error = "#f38ba8",
  warning = "#fab387",
  warning_subtle = "#f9e2af",
  denied = "#eba0ac",
  busy = "#f9e2af",
  info = "#89b4fa",
  info_more_subtle = "#74c7ec",
  info_most_subtle = "#585b70",
  success = "#a6e3a1",
  success_more_subtle = "#94e2d5",
  success_most_subtle = "#40a02b",

  -- diff view (add/remove: fg, code bg, line-number gutter bg)
  diff_add_fg = "#a6e3a1",
  diff_add_bg = "#26332b",
  diff_add_bg_emph = "#1f2a23",
  diff_remove_fg = "#f38ba8",
  diff_remove_bg = "#3a2b30",
  diff_remove_bg_emph = "#2f2227",

  -- brand accent for the Hypercredit icon/count
  hypercredit = "#f5e0dc",

  -- syntax highlighting roles
  syntax_link = "#89b4fa",
  syntax_image = "#f5c2e7",
  syntax_comment_preproc = "#f9e2af",
  syntax_keyword_reserved = "#cba6f7",
  syntax_keyword_type = "#94e2d5",
  syntax_operator = "#f38ba8",
  syntax_name_builtin = "#f5c2e7",
  syntax_name_tag = "#cba6f7",
  syntax_name_attribute = "#fab387",
  syntax_name_class = "#f9e2af",
  syntax_name_decorator = "#f9e2af",
  syntax_literal_string = "#a6e3a1",

  -- brand surfaces (optional). When omitted these cascade to the brand
  -- pair (secondary/primary) so most themes don't need to set them.
  --   header_charm     → "Charm™" label, logo "CRUSH" wordmark, version.
  --   header_diagonals → ╱ separators, sidebar logo accents.
  --   logo_grad_*      → header logo wordmark gradient.
  --   working_grad_*   → animated "thinking" indicator gradient.
  header_charm = "#f5c2e7",
  header_diagonals = "#cba6f7",
  logo_grad_from = "#f5c2e7",
  logo_grad_to = "#cba6f7",
  working_grad_from = "#cba6f7",
  working_grad_to = "#f5c2e7",
}
```

Select a theme from the command palette ("Select Theme"). The picker shows a
live preview as you move the selection; `enter` confirms and persists it,
`esc` cancels and restores the previous theme.

## Snapshots

Crush automatically snapshots the filesystem before each user message, enabling restore points.

```json
{
  "snapshots": {
    "enabled": true,
    "exclude": [
      "node_modules",
      "**/node_modules",
      "vendor",
      ".venv",
      "dist",
      "build"
    ]
  }
}
```

- `enabled` (default `true`): Toggle automatic snapshots.
- `exclude`: Glob patterns to exclude from snapshots. Defaults include `node_modules`, `vendor`, `.venv`, `__pycache__`, `target`, `dist`, `build`, `.next`, `.cache`, etc.

## Worktrees

Crush can manage git worktrees for isolated work environments.

```json
{
  "worktree": {
    "enabled": true,
    "post_create": [
      { "if_exists": "bun.lockb", "run": "bun i" },
      { "if_exists": "package-lock.json", "run": "npm ci" },
      { "if_exists": "go.sum", "run": "go mod download" }
    ]
  }
}
```

- `enabled` (default `true`): Toggle worktree management.
- `post_create`: Commands to run after creating/restoring a worktree. Each entry has:
  - `if_exists`: File to check for before running the command.
  - `run`: Command to execute.

Default `post_create` hooks handle common package managers: `bun i`, `pnpm i`, `yarn`, `npm ci`, `go mod download`, `cargo fetch`, `pip install -r requirements.txt`.

## Hooks

Hooks are user-defined shell commands that fire on agent events. Currently only `PreToolUse` is supported, which runs before a tool is executed.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^(edit|write|multiedit)$",
        "command": ".crush/hooks/protect-files.sh"
      },
      {
        "matcher": "^bash$",
        "command": ".crush/hooks/no-haskell.sh"
      }
    ]
  }
}
```

### Hook Properties

- `command` (required): Shell command to execute. Runs via `sh -c`.
- `matcher` (optional): Regex pattern tested against the tool name. Empty or absent means match all tools.
- `timeout` (optional): Timeout in seconds. Defaults to 30.

### Event Name Normalization

Event names are case-insensitive and accept snake_case variants: `PreToolUse`, `pretooluse`, `pre_tool_use`, and `PRE_TOOL_USE` all work.

### How Hooks Work

1. When a tool is about to be called, all `PreToolUse` hooks with a matching `matcher` (or no matcher) run in parallel.
2. Duplicate commands are deduplicated — each unique command runs at most once.
3. The hook receives JSON on **stdin** and hook-specific **environment variables**.

### Hook Input (stdin)

A JSON payload is piped to the hook command:

```json
{
  "event": "PreToolUse",
  "session_id": "abc-123",
  "cwd": "/path/to/project",
  "tool_name": "bash",
  "tool_input": { "command": "ls -la" }
}
```

### Hook Environment Variables

| Variable                     | Description                                       |
| ---------------------------- | ------------------------------------------------- |
| `CRUSH_EVENT`                | Event name (e.g. `PreToolUse`)                    |
| `CRUSH_TOOL_NAME`            | Name of the tool being called                     |
| `CRUSH_SESSION_ID`           | Current session ID                                |
| `CRUSH_CWD`                  | Current working directory                         |
| `CRUSH_PROJECT_DIR`          | Project root directory                            |
| `CRUSH_TOOL_INPUT_COMMAND`   | Value of `command` from tool input (if present)   |
| `CRUSH_TOOL_INPUT_FILE_PATH` | Value of `file_path` from tool input (if present) |

### Hook Output

**Exit code 0** — the hook succeeded. Stdout is parsed as JSON:

```json
{ "decision": "allow", "context": "optional context appended to tool result" }
```

- `decision`: `allow` to explicitly allow, `deny` to block, `none` (or omit) for no opinion.
- `reason`: Explanation text (used when denying).
- `context`: Extra context appended to the tool result.
- `updated_input`: Replacement JSON for the tool input. Last non-empty value wins.

**Exit code 2** — the tool call is blocked. Stderr is used as the deny reason.

```bash
echo "No Haskell allowed" >&2
exit 2
```

**Any other exit code** — non-blocking error. The tool call proceeds as normal.

### Claude Code Compatibility

Crush also supports the Claude Code hook output format:

```json
{
  "hookSpecificOutput": {
    "permissionDecision": "allow",
    "permissionDecisionReason": "Auto-approved",
    "updatedInput": { "command": "echo rewritten" }
  }
}
```

Existing Claude Code hooks should work without modification.

### Decision Aggregation

When multiple hooks match, their decisions are aggregated:

- **Deny wins over allow** — if any hook denies, the tool call is blocked.
- **Allow wins over none** — if no hook denies but at least one allows, the call proceeds.
- All deny reasons are concatenated (newline-separated).
- All context strings are concatenated (newline-separated).
- For `updated_input`, the last non-empty value wins.

## Tool Permissions

```json
{
  "permissions": {
    "allowed_tools": ["view", "ls", "grep", "edit"]
  }
}
```

## Environment Variables

- `CRUSH_GLOBAL_CONFIG` - Override global config location
- `CRUSH_GLOBAL_DATA` - Override data directory location
- `CRUSH_SKILLS_DIR` - Override default skills directory
- `CRUSH_DISABLE_PROVIDER_AUTO_UPDATE` - Disable automatic provider updates
- `CRUSH_DISABLE_DEFAULT_PROVIDERS` - Disable default provider configurations

## Procedures

Procedures are reusable workflow templates stored as markdown files in
`$XDG_CONFIG_HOME/crush/procedures/` (typically `~/.config/crush/procedures/`).

They can be created from existing conversations or written manually. Crush
discovers all `.md` files in the procedures directory automatically.

### Directory Structure

```
~/.config/crush/procedures/
├── refactor-large-components.md
├── upstream-merge-workflow.md
└── deploy-and-verify.md
```

### Creating Procedures

Procedures are created in two ways:

1. **From a conversation**: Tell Crush to "save this as a procedure" and it
   will use the session milestones (periodic 5-8 word summaries generated
   every 10 turns) to construct a reusable workflow template.
2. **Manually**: Create a `.md` file in the procedures directory. Use
   imperative, step-by-step instructions.

### Updating Procedures

Tell Crush to "update procedure X based on this conversation" and the
current milestones + conversation context will be used to refine it.

### Viewing Procedures

- Use the View tool on files in `~/.config/crush/procedures/`.
- Ask Crush to "show me procedure X" and it will display the contents.
- Edit with the Edit tool or by prompting corrections.

## Milestones

Milestones are periodic summaries generated every 10 user messages by a
background agent (using the small model). They appear in the Milestones
dialog (Ctrl+Q or command palette). Each milestone has:

- A 5-8 word short summary (displayed in the list).
- A 2-3 sentence full summary (used as context for procedure generation
  and future milestone summarization).

Milestones persist in the session database and survive restarts.
