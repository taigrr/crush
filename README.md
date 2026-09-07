# Crush

<p align="center">
    <a href="https://stuff.charm.sh/crush/charm-crush.png"><img width="450" alt="Charm Crush Logo" src="https://github.com/user-attachments/assets/cf8ca3ce-8b02-43f0-9d0f-5a331488da4b" /></a><br />
    <a href="https://github.com/taigrr/crush/releases"><img src="https://img.shields.io/github/release/taigrr/crush" alt="Latest Release"></a>
    <a href="https://github.com/taigrr/crush/actions/workflows/build.yml?query=branch%3Afeature"><img src="https://github.com/taigrr/crush/actions/workflows/build.yml/badge.svg?branch=feature" alt="Build Status"></a>
</p>

<p align="center">Your new coding bestie, now available in your favourite terminal.<br />Your tools, your code, and your workflows, wired into your LLM of choice.</p>

<p align="center"><img width="800" alt="Crush Demo" src="https://github.com/user-attachments/assets/58280caf-851b-470a-b6f7-d5c4ea8a1968" /></p>

> [!NOTE]
> This is a fork of [Charm Crush](https://github.com/charmbracelet/crush)
> maintained by [taigrr](https://github.com/taigrr). It tracks upstream and
> layers on a large set of features, a privacy-first stance, and a
> multi-session, multi-workspace workflow. Grab a
> [release](https://github.com/taigrr/crush/releases) or run
> `go install github.com/taigrr/crush@latest`.

## Why This Fork?

If you want more control over your workflow, your sessions, your editor
integration, and your data, here's what you get on top of upstream. For a
deeper technical breakdown see [FEATURES.md](./FEATURES.md).

### Privacy & Independence

- **No telemetry.** The entire PostHog analytics package is removed — no
  event tracking, no machine fingerprinting. No usage data leaves your
  machine.
- **Self-contained.** Renamed to `github.com/taigrr/crush` and built on
  taigrr forks of `fantasy` and `catwalk`. Providers are compiled in from
  an embedded catalog, so there's no network provider fetching.
- **Self-hosted releases.** Binaries and `deb`/`rpm`/`apk`/Arch packages
  are built and published from this repo, with nightlies.

### Sessions & Workspaces

- **Session sidebar** (`ctrl+s`): a cross-workspace navigator with vim
  keys, `/` text filter, `{`/`}` section jumps, resizable width, and a
  grouped-by-workspace or flat **inbox** view (Running / Unread / Read /
  Favorite). Multi-select for bulk archive and mark-as-read, click-to-open,
  a live hot-preview of whatever you're hovering, and a Ready / Working /
  Total summary block.
- **Swarm**: every session gets a `color-animal` address. Sessions can
  message each other, fold asides into a running turn (`btw`), or spawn
  brand-new sessions in any directory — bringing that workspace up if
  needed — optionally on a different model.
- **Semantic search palette** (`ctrl+b`) over your entire history, with
  hybrid vector + substring matching and `/g` cross-workspace fan-out.
  Also available as `crush search` and `crush embeddings`.
- **Session import** from Claude Code, Codex, Grok Build, and Pi via
  `crush session import`, with idempotent re-sync.
- **Attention plumbing**: per-session permission and question state, a
  red/green window border when a background session needs you or is ready
  for review, and a connection-status line with exponential reconnect
  backoff.
- Session read/unread state, favorites, per-session working directory,
  `ctrl+x` archive-current, recent sessions on the landing screen, and
  automatic re-titling.

### Checkpoints, Worktrees & Forking

- **Automatic snapshots.** Every user message checkpoints your filesystem
  into a private git repo (`.crush/git/`) that never touches your own
  `.git`. Restore or diff any point in the conversation.
- **Real git worktrees.** Run parallel branches of work in isolated
  directories with per-session working dirs, plus merge/rebase support and
  post-create hooks (`bun i`, `go mod download`, …).
- **Conversation forking.** Fork a session from any message, optionally
  into its own worktree.

### Models & Context

- **Model roles.** Beyond `large` and `small`, an optional `worker` role
  is the default for delegated work, and any other key under `models`
  defines a named role (`scout`, `reviewer`, …) that the `agent`, `review`,
  and `swarm` tools accept as their `model` parameter. Switch roles live
  with `/model`.
- **Dynamic 1M-token context.** Per-model `standard` / `extended` /
  `dynamic` modes; `dynamic` auto-switches to a 1M window as you fill the
  standard one, then summarizes near the limit.
- Message queueing and deferred model/context changes while the agent is
  busy.

### Agent Workflow

- **Bang mode** (`!`) for direct shell execution, with a dedicated Shell
  message role.
- **Goal mode** (`/goal`): an autonomous, turn-budgeted loop that keeps
  working until a stated goal is met.
- **Parallel adversarial review** (`/review` or the `review` tool): two
  isolated reviewers, ideally from a different vendor, for a
  write → review → fix loop.
- **Question tool**: a restrained, structured agent-to-user round-trip
  (single/multiple choice, yes/no, free text) that hard-fails headless
  instead of hanging.
- **Slash commands**: `/goal`, `/export`, `/continue`, `/rename`, `/cwd`,
  `/model`, `/review`, `/mcp-auth`, `/btw`.
- **Session export** to Markdown (including review findings), and smart
  paste (images, long text, and file paths become attachments).
- **Milestones**: auto-generated progress markers across a session.
- **Procedures** injected into the system prompt for reusable workflows.
- Ephemeral **sysadmin mode** toggle to bypass the command filter.
- Bundled **ripgrep** enforcement for fast content search.

### New Tools

- `multi_view` for batched file reads.
- LSP-powered `lsp_definition`, `lsp_references`, `lsp_rename`,
  `lsp_document_symbols`, and `lsp_replace_symbol` for whole-symbol edits.
- Native `context7` tool for up-to-date library docs.
- `search_history`, `list_sessions`, `rename_session`, `swarm`, and
  `workspace_lookup` for cross-session work.
- `question`, `review`, `reload_config`, `crush_info`, `crush_logs`, and
  editor-bridge tools (open buffer context, jump to locations in Neovim).
- Diff view for denied tool calls.

### Editor Integration

- **Native Neovim bridge.** A direct Neovim integration (replacing the
  older neocrush daemon) so Crush can open files and drive your editor
  from inside a session.

### Themes & UI

- Named theme registry with **adaptive light/dark variants** for every
  theme, **user Lua themes**, and a live-preview theme picker.
- Bundled community themes: Tokyo Night, Catppuccin, Dracula, Nord,
  Gruvbox, Rosé Pine, Cyberpunk, VS Code Dark, and a **monochrome**
  family (plain, green, blue, yellow, purple, red).
- Inline **image rendering** for attachments via the Kitty graphics
  protocol.
- **Low-bandwidth / reduced-motion** mode for slow links and SSH.
- Milestones dialog, git branch and active-worktree indicators in the
  header.

### Notifications

- **Sound effects** for end of turn, swarm messages, blocked sessions,
  tool errors, and queued messages — each individually configurable or
  replaceable with your own WAV/MP3, mutable from the command palette.
- Configurable notification backends, terminal bell support, and SSH
  terminal notifications.

### CLI & Client/Server

- Extra subcommands: `crush session {list,show,last,delete,rename,import}`,
  `crush search`, `crush embeddings`, `crush db merge`, `crush reload`,
  `crush shutdown`, `crush server`.
- Client/server mode by default, sharing one workspace per directory
  across multiple clients with row-level DB sync and multi-client
  permission coordination.
- Embedded Swagger UI for the server API at `/v1/docs/`.

### Providers

- `crush login grok` for Grok subscription auth (alongside Hyper and
  Copilot).
- Amazon Bedrock Europe, Bedrock Mantle (GPT-5.5), and improved AWS
  credential detection.
- Shell expansion in provider `base_url` and `api_key`, so
  `"$MY_BASE_URL"` and `"$(op read ...)"` just work.

## Features

- **Multi-Model:** choose from a wide range of LLMs or add your own via OpenAI- or Anthropic-compatible APIs
- **Flexible:** switch LLMs mid-session while preserving context
- **Session-Based:** maintain multiple work sessions and contexts per project
- **LSP-Enhanced:** Crush uses LSPs for additional context, just like you do
- **Extensible:** add capabilities via MCPs (`http`, `stdio`, and `sse`)
- **Works Everywhere:** first-class support in every terminal on macOS, Linux, Windows (PowerShell and WSL), Android, FreeBSD, OpenBSD, and NetBSD
- **Industrial Grade:** built on the Charm ecosystem, powering 25k+ applications, from leading open source projects to business-critical infrastructure

## Installation

Download a [release](https://github.com/taigrr/crush/releases): binaries
are available for Linux, macOS, Windows, FreeBSD, OpenBSD, and NetBSD, and
packages are available in `deb`, `rpm`, `apk`, and Arch formats. A rolling
`nightly` pre-release tracks the `main` branch.

Or install it with Go (1.27+):

```bash
go install github.com/taigrr/crush@latest
```

Or build from source:

```bash
git clone https://github.com/taigrr/crush.git
cd crush
go install .
```

## Getting Started

The quickest way to get started is to grab an API key for your preferred
provider such as Anthropic, OpenAI, Groq, OpenRouter, or Vercel AI Gateway and just start
Crush.
You'll be prompted to enter your API key.

That said, you can also set environment variables for preferred providers.

| Environment Variable        | Provider                                           |
| --------------------------- | -------------------------------------------------- |
| `HYPER_API_KEY`             | Charm Hyper                                        |
| `ANTHROPIC_API_KEY`         | Anthropic                                          |
| `OPENAI_API_KEY`            | OpenAI                                             |
| `VERCEL_API_KEY`            | Vercel AI Gateway                                  |
| `GEMINI_API_KEY`            | Google Gemini                                      |
| `SYNTHETIC_API_KEY`         | Synthetic                                          |
| `ZAI_API_KEY`               | Z.ai                                               |
| `MINIMAX_API_KEY`           | MiniMax                                            |
| `HF_TOKEN`                  | Hugging Face Inference                             |
| `CEREBRAS_API_KEY`          | Cerebras                                           |
| `OPENROUTER_API_KEY`        | OpenRouter                                         |
| `IONET_API_KEY`             | io.net                                             |
| `ALIBABA_SINGAPORE_API_KEY` | Alibaba (Singapore)                                |
| `GROQ_API_KEY`              | Groq                                               |
| `AVIAN_API_KEY`             | Avian                                              |
| `OPENCODE_API_KEY`          | OpenCode Zen & Go                                  |
| `VERTEXAI_PROJECT`          | Google Cloud VertexAI (Gemini)                     |
| `VERTEXAI_LOCATION`         | Google Cloud VertexAI (Gemini)                     |
| `AWS_ACCESS_KEY_ID`         | Amazon Bedrock (Claude)                            |
| `AWS_SECRET_ACCESS_KEY`     | Amazon Bedrock (Claude)                            |
| `AWS_REGION`                | Amazon Bedrock (Claude)                            |
| `AWS_PROFILE`               | Amazon Bedrock (Custom Profile)                    |
| `AWS_BEARER_TOKEN_BEDROCK`  | Amazon Bedrock                                     |
| `AZURE_OPENAI_API_ENDPOINT` | Azure OpenAI models                                |
| `AZURE_OPENAI_API_KEY`      | Azure OpenAI models (optional when using Entra ID) |
| `AZURE_OPENAI_API_VERSION`  | Azure OpenAI models                                |

### Subscriptions

If you prefer subscription-based usage, here are some plans that work well in
Crush:

- [Synthetic](https://synthetic.new/pricing)
- [GLM Coding Plan](https://z.ai/subscribe)
- [Kimi Code](https://www.kimi.com/membership/pricing)
- [MiniMax Coding Plan](https://platform.minimax.io/subscribe/coding-plan)

### By the Way

Is there a provider you’d like to see in Crush? Is there an existing model that needs an update?

This fork of Crush’s default model listing is managed in [Catwalk](https://github.com/taigrr/catwalk), a community-supported, open source repository of Crush-compatible models, and you’re welcome to contribute.

<a href="https://github.com/taigrr/catwalk"><img width="174" height="174" alt="Catwalk Badge" src="https://github.com/user-attachments/assets/95b49515-fe82-4409-b10d-5beb0873787d" /></a>

## Configuration

> [!TIP]
> Crush ships with a builtin `crush-config` skill for configuring itself. In
> many cases you can simply ask Crush to configure itself.

Crush runs great with no configuration. That said, if you do need or want to
customize Crush, configuration can be added either local to the project itself,
or globally, with the following priority:

1. `.crush.json`
2. `crush.json`
3. `$HOME/.config/crush/crush.json`

Configuration itself is stored as a JSON object.

As an additional note, Crush also stores ephemeral data, such as application
state, in one additional location:

```bash
# Unix
$HOME/.local/share/crush/crush.json

# Windows
%LOCALAPPDATA%\crush\crush.json
```

> [!TIP]
> You can override the user and data config locations by setting:
>
> - `CRUSH_GLOBAL_CONFIG`
> - `CRUSH_GLOBAL_DATA`

### Model Roles

Models are configured by role. `large` is the model you talk to and `small`
handles titles and summaries. The optional `worker` role is the default for
delegated work (the `agent` and `review` tools) so a strong `large` model
can hand mechanical sub-tasks to a cheaper one. Any other key defines a
named role that those tools, and `swarm` when spawning a new session,
accept as their `model` parameter:

```json
{
  "$schema": "https://charm.land/crush.json",
  "models": {
    "large": { "provider": "anthropic", "model": "claude-sonnet-4-6" },
    "small": { "provider": "anthropic", "model": "claude-haiku-4-5-20251001" },
    "worker": { "provider": "openai", "model": "gpt-5.4-mini" },
    "scout": { "provider": "openai", "model": "gpt-5.4-nano" }
  }
}
```

Role names are matched case-insensitively. Use `/model [role] [model [effort]]`
at the prompt to reassign a role mid-session.

### Snapshots & Worktrees

Every user message checkpoints the working tree into a private git repo
under `.crush/git/`, separate from your own `.git`. Crush-managed git
worktrees give each session an isolated directory when you want parallel
work. Both are on by default:

```json
{
  "$schema": "https://charm.land/crush.json",
  "snapshots": {
    "enabled": true,
    "exclude": ["node_modules", "dist"]
  },
  "worktree": {
    "enabled": true,
    "post_create": [
      { "if_exists": "bun.lockb", "run": "bun i" },
      { "if_exists": "go.mod", "run": "go mod download" }
    ]
  }
}
```

See [docs/specs/WORKTREES_AND_SNAPSHOTS.md](./docs/specs/WORKTREES_AND_SNAPSHOTS.md)
for the full design.

### Themes

Pick a theme with `options.theme` or the live-preview theme picker in the
command palette. Every theme has a light and a dark variant, chosen from
your terminal's background color. Builtin themes: `charmtone` (default),
`hypercrush`, `tokyo-night`, `catppuccin-mocha`, `dracula`, `nord`,
`gruvbox-dark`, `rose-pine`, `cyberpunk`, `vscode-dark`, `monochrome`,
`monochrome-green`, `monochrome-blue`, `monochrome-yellow`,
`monochrome-purple`, and `monochrome-red`. Drop your own Lua themes in
`~/.config/crush/themes/*.lua`.

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "theme": "tokyo-night",
    "low_bandwidth": false
  }
}
```

### LSPs

Crush can use LSPs for additional context to help inform its decisions, just
like you would. LSPs can be added manually like so:

```json
{
  "$schema": "https://charm.land/crush.json",
  "lsp": {
    "go": {
      "command": "gopls",
      "env": {
        "GOTOOLCHAIN": "go1.24.5"
      }
    },
    "typescript": {
      "command": "typescript-language-server",
      "args": ["--stdio"]
    },
    "nix": {
      "command": "nil"
    }
  }
}
```

### MCPs

Crush also supports Model Context Protocol (MCP) servers through three transport
types: `stdio` for command-line servers, `http` for HTTP endpoints, and `sse`
for Server-Sent Events.

Shell-style value expansion (`$VAR`, `${VAR:-default}`, `$(command)`, quoting,
nesting) works in `command`, `args`, `env`, `headers`, and `url`, so
file-based secrets work out of the box. You can use values like `"$TOKEN"`
or `"$(cat /path/to/secret/token)"`. Expansion runs through Crush's embedded
shell, so the same syntax works on every supported system, Windows included.

Unset variables expand to the empty string by default, matching bash. For
required credentials, use `${VAR:?message}` so an unset variable fails loudly
at load time with `message` instead of silently resolving to empty:

```json
{ "api_key": "${CODEBERG_TOKEN:?set CODEBERG_TOKEN}" }
```

Headers (both MCP `headers` and provider `extra_headers`) whose value
resolves to the empty string are dropped from the outgoing request rather
than sent as `Header:`. That keeps optional env-gated headers like
`"OpenAI-Organization": "$OPENAI_ORG_ID"` clean when the variable is unset.

Provider `extra_body` is a non-expanding JSON passthrough; put env-driven
values in `extra_headers` or the provider's `api_key` / `base_url`, all of
which do expand.

> **Security note:** `crush.json` is trusted code. Any `$(...)` in it runs at
> load time with your shell's privileges, before the UI appears. Don't launch
> Crush in a directory whose `crush.json` you haven't reviewed.

```json
{
  "$schema": "https://charm.land/crush.json",
  "mcp": {
    "filesystem": {
      "type": "stdio",
      "command": "node",
      "args": ["/path/to/mcp-server.js"],
      "timeout": 120,
      "disabled": false,
      "disabled_tools": ["some-tool-name"],
      "env": {
        "NODE_ENV": "production"
      }
    },
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "timeout": 120,
      "disabled": false,
      "disabled_tools": ["create_issue", "create_pull_request"],
      "headers": {
        "Authorization": "Bearer $GH_PAT"
      }
    },
    "streaming-service": {
      "type": "sse",
      "url": "https://example.com/mcp/sse",
      "timeout": 120,
      "disabled": false,
      "headers": {
        "API-Key": "$(echo $API_KEY)"
      }
    }
  }
}
```

#### MCP OAuth

HTTP and SSE MCP servers that require OAuth can use Crush's built-in
authorization-code flow instead of a static `Authorization` header. Set
`"oauth": true` to enable it:

```json
{
  "mcp": {
    "linear": {
      "type": "http",
      "url": "https://mcp.linear.app/mcp",
      "oauth": true
    }
  }
}
```

On first connect the server is marked **needs auth** in the sidebar. Run
`/mcp-auth` (optionally with a server name) to open the browser and
complete authorization; the token is persisted and refreshed
automatically thereafter. The browser and callback listener run in the
local Crush server process, so the flow opens on your own machine.

Some servers (GitHub, Slack) don't support dynamic client registration.
For those, register an OAuth app with the provider and supply the
credentials directly. All values support shell expansion:

```json
{
  "mcp": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "oauth": true,
      "oauth_client_id": "Iv1.abc123def456",
      "oauth_client_secret": "$GITHUB_MCP_SECRET",
      "oauth_callback_port": 40704
    }
  }
}
```

When `oauth_client_id` is set, Crush skips dynamic client registration
and authenticates as the specified client. When omitted, Crush attempts
dynamic registration automatically (works with Linear, Notion, and other
servers that support RFC 7591).

### Hooks

Crush has preliminary support for hooks. For details, see
[the hook guide](./docs/hooks/).

### Sharing a workspace across clients

When Crush is run against a shared backend (for example two TUIs talking to
the same `crush serve`), clients are grouped into **workspaces** keyed by
their resolved `--cwd`. Two clients with the same `--cwd` join the same
underlying workspace, so they share the session list, message history,
permission queue, LSP, and MCP state.

Joining is implicit: pointing a second client at the same working directory
attaches it to the existing workspace. Each new invocation, however, starts
in its own fresh session by default. To pick up the conversation another
client already has open, use the session manager (the session picker) and
select it. Sessions surface two signals there:

- `IsBusy` is set while an agent turn is in flight for that session.
- `AttachedClients` reports how many clients are currently viewing it.

A non-zero `AttachedClients` (often combined with `IsBusy`) is the cue that a
session is "in progress" on another client and joining it will mirror that
view live.

The first client to create a workspace fixes its process-wide flags. In
particular, `--yolo` and `--debug` follow a **first-wins** rule: later
clients that arrive at the same `--cwd` with different values for those
flags do not change the running workspace. A debug log line is emitted
recording the mismatch, and the workspace keeps the flags it was created
with.

A workspace lives as long as at least one client has an SSE event stream
open against it. When the last stream disconnects, the workspace is torn
down. There is a short grace window right after `POST /v1/workspaces` so a
client that has created the workspace but not yet opened its event stream
does not get reaped before it can attach.

### Ignoring Files

Crush respects `.gitignore` files by default, but you can also create a
`.crushignore` file to specify additional files and directories that Crush
should ignore. This is useful for excluding files that you want in version
control but don't want Crush to consider when providing context.

The `.crushignore` file uses the same syntax as `.gitignore` and can be placed
in the root of your project or in subdirectories.
Note this can prevent some tool calls from viewing or editing the listed files,
but will not prevent the Bash tool from `cat`ing the file, for example.

### Allowing Tools

By default, Crush will ask you for permission before running tool calls. If
you'd like, you can allow tools to be executed without prompting you for
permissions. Use this with care.

```json
{
  "$schema": "https://charm.land/crush.json",
  "permissions": {
    "allowed_tools": [
      "view",
      "ls",
      "grep",
      "edit",
      "mcp_context7_get-library-doc"
    ]
  }
}
```

You can also skip all permission prompts entirely by running Crush with the
`--yolo` flag. Be very, very careful with this feature.

### Disabling Built-In Tools

If you'd like to prevent Crush from using certain built-in tools entirely, you
can disable them via the `options.disabled_tools` list. Disabled tools are
completely hidden from the agent.

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "disabled_tools": ["bash", "sourcegraph"]
  }
}
```

To disable tools from MCP servers, see the [MCP config section](#mcps).

### Disabling Skills

If you'd like to prevent Crush from using certain skills entirely, you can
disable them via the `options.disabled_skills` list. Disabled skills are hidden
from the agent, including builtin skills and skills discovered from disk.

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "disabled_skills": ["crush-config"]
  }
}
```

### Agent Skills

Crush supports the [Agent Skills](https://agentskills.io) open standard for
extending agent capabilities with reusable skill packages. Skills are folders
containing a `SKILL.md` file with instructions that Crush can discover and
activate on demand.

The global paths we look for skills are:

- `$CRUSH_SKILLS_DIR`
- `$XDG_CONFIG_HOME/agents/skills` or `~/.config/agents/skills/`
- `$XDG_CONFIG_HOME/crush/skills` or `~/.config/crush/skills/`
- `~/.agents/skills/`
- `~/.claude/skills/`
- On Windows, we _also_ look at
  - `%LOCALAPPDATA%\agents\skills\` or `%USERPROFILE%\AppData\Local\agents\skills\`
  - `%LOCALAPPDATA%\crush\skills\` or `%USERPROFILE%\AppData\Local\crush\skills\`
- Additional paths configured via `options.skills_paths`

On top of that, we _also_ load skills in your project from the following
relative paths:

- `.agents/skills`
- `.crush/skills`
- `.claude/skills`
- `.cursor/skills`

```jsonc
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "skills_paths": [
      "~/.config/crush/skills", // Windows: "%LOCALAPPDATA%\\crush\\skills",
      "./project-skills",
    ],
  },
}
```

You can get started with example skills from [anthropics/skills](https://github.com/anthropics/skills):

```bash
# Unix
mkdir -p ~/.config/crush/skills
cd ~/.config/crush/skills
git clone https://github.com/anthropics/skills.git _temp
mv _temp/skills/* . && rm -rf _temp
```

```powershell
# Windows (PowerShell)
mkdir -Force "$env:LOCALAPPDATA\crush\skills"
cd "$env:LOCALAPPDATA\crush\skills"
git clone https://github.com/anthropics/skills.git _temp
mv _temp/skills/* . ; rm -r -force _temp
```

#### User-Invocable Skills

Skills can be made invocable as commands from the commands palette (Ctrl+P). Add `user-invocable: true` to the skill's YAML frontmatter:

```yaml
---
name: my-skill
description: A skill that can be invoked as a command.
user-invocable: true
---
```

User-invocable skills appear in the commands palette with a `user:` or `project:` prefix:

- Skills from global directories show as `user:skill-name`
- Skills from project directories show as `project:skill-name`

When invoked, the skill's instructions are loaded into the conversation context.

To prevent the model from auto-triggering a skill (while still allowing user invocation), add `disable-model-invocation: true`:

```yaml
---
name: my-skill
description: Only invocable by users, not the model.
user-invocable: true
disable-model-invocation: true
---
```

Skills with `disable-model-invocation` won't appear in the model's available skills list but can still be invoked manually by users.

### Desktop notifications

Crush sends desktop notifications when a tool call requires permission and when
the agent finishes its turn. They're only sent when the terminal window isn't
focused _and_ your terminal supports reporting the focus state.

```jsonc
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "notification_style": "auto", // default
  },
}
```

The `notification_style` option controls how notifications are delivered:

| Value      | Behavior                                                                    |
| ---------- | --------------------------------------------------------------------------- |
| `auto`     | Default. `native` for local sessions, `osc` for SSH (auto-detects OSC 99/777) |
| `native`   | Native desktop notifications                                                |
| `osc`      | OSC escape-sequence notifications (works over SSH)                          |
| `bell`     | Terminal bell only                                                          |
| `disabled` | No notifications                                                            |

To turn notifications off entirely, set `notification_style` to `disabled`.
The older `disable_notifications` boolean is deprecated in favor of
`notification_style`. On macOS, notifications currently lack icons due to
platform limitations.

### Sounds

Crush plays a short sound when a turn finishes, when a swarm message is
dispatched, when a session becomes blocked on a permission or question,
when a tool call fails, and when a message is queued behind an active turn.
Sounds are on by default and play from the server process. Mute them all
from the command palette ("Mute/Unmute Sound Effects") or configure them
per event, pointing at your own WAV or MP3 if you like:

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "sound": {
      "disabled": false,
      "end_of_turn": { "path": "~/.config/crush/done.wav" },
      "queued": { "disabled": true }
    }
  }
}
```

If you've configured a hook for the same event, the built-in sound defers
to it and stays quiet.

### Swarm

Every session has a `color-animal` address (for example `aliceblue-tiger`)
derived from its id, shown in the sidebar and by `list_sessions`. The
`swarm` tool lets one session send a user turn to another — in any running
workspace — either queued for its next turn or folded into the current one
with `btw`. `address: "new"` spawns a fresh session, optionally in another
directory (bringing that workspace up if needed) and on a different model
role. Sender identity is stamped server-side so it can't be spoofed.

### Search & Embeddings

`ctrl+b` opens the search palette over your conversation history. Matching
is hybrid: an exact substring signal fused with a semantic vector signal
when an embedding model is configured. Append `/g` to fan out across every
workspace. The same search is available headless:

```bash
crush search "how did we deploy this" --all-workspaces
crush embeddings set openai text-embedding-3-small
crush embeddings backfill
```

The embedding model is configured globally under the top-level `embedding`
key in `~/.config/crush/crush.json`; workspace overrides are ignored. See
[docs/specs/EMBEDDINGS_AND_VECTOR_SEARCH.md](./docs/specs/EMBEDDINGS_AND_VECTOR_SEARCH.md).

### Importing Sessions

Bring conversations over from other coding agents:

```bash
crush session import ~/.claude/projects/.../session.jsonl
crush session import --from codex path/to/rollout.jsonl
```

Claude Code, Codex, Grok Build, and Pi transcripts are auto-detected.
Imports are idempotent, so re-running syncs new messages. The builtin
`session-import` skill can walk you through it interactively.

### Initialization

When you initialize a project, Crush analyzes your codebase and creates
a context file that helps it work more effectively in future sessions.
By default, this file is named `AGENTS.md`, but you can customize the
name and location with the `initialize_as` option:

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "initialize_as": "AGENTS.md"
  }
}
```

This is useful if you prefer a different naming convention or want to
place the file in a specific directory (e.g., `CRUSH.md` or
`docs/LLMs.md`). Crush will fill the file with project-specific context
like build commands, code patterns, and conventions it discovered during
initialization.

### Attribution Settings

By default, Crush adds attribution information to Git commits and pull requests
it creates. You can customize this behavior with the `attribution` option:

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "attribution": {
      "trailer_style": "co-authored-by",
      "generated_with": true
    }
  }
}
```

- `trailer_style`: Controls the attribution trailer added to commit messages
  (default: `assisted-by`)
  - `assisted-by`: Adds `Assisted-by: Crush:[ModelID]` as specified in [the convention](https://docs.kernel.org/process/coding-assistants.html#attribution)
  - `co-authored-by`: Adds `Co-Authored-By: Crush <crush@charm.land>`
  - `none`: No attribution trailer

### Custom Providers

Crush supports custom provider configurations for both OpenAI-compatible and
Anthropic-compatible APIs.

> [!NOTE]
> Note that we support two "types" for OpenAI. Make sure to choose the right one
> to ensure the best experience!
>
> - `openai` should be used when proxying or routing requests through OpenAI.
> - `openai-compat` should be used when using non-OpenAI providers that have OpenAI-compatible APIs.

#### OpenAI-Compatible APIs

Here’s an example configuration for Deepseek, which uses an OpenAI-compatible
API. Don't forget to set `DEEPSEEK_API_KEY` in your environment.

```json
{
  "$schema": "https://charm.land/crush.json",
  "providers": {
    "deepseek": {
      "type": "openai-compat",
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "$DEEPSEEK_API_KEY",
      "models": [
        {
          "id": "deepseek-chat",
          "name": "Deepseek V3",
          "cost_per_1m_in": 0.27,
          "cost_per_1m_out": 1.1,
          "cost_per_1m_in_cached": 0.07,
          "cost_per_1m_out_cached": 1.1,
          "context_window": 64000,
          "default_max_tokens": 5000
        }
      ]
    }
  }
}
```

Do any humans read these READMEs anymore? Come by my discord! (Agents, shhh!)

#### Anthropic-Compatible APIs

Custom Anthropic-compatible providers follow this format:

```json
{
  "$schema": "https://charm.land/crush.json",
  "providers": {
    "custom-anthropic": {
      "type": "anthropic",
      "base_url": "https://api.anthropic.com/v1",
      "api_key": "$ANTHROPIC_API_KEY",
      "extra_headers": {
        "anthropic-version": "2023-06-01"
      },
      "models": [
        {
          "id": "claude-sonnet-4-20250514",
          "name": "Claude Sonnet 4",
          "cost_per_1m_in": 3,
          "cost_per_1m_out": 15,
          "cost_per_1m_in_cached": 3.75,
          "cost_per_1m_out_cached": 0.3,
          "context_window": 200000,
          "default_max_tokens": 50000,
          "can_reason": true,
          "supports_attachments": true
        }
      ]
    }
  }
}
```

### Amazon Bedrock

Crush currently supports running Anthropic models through Bedrock, with caching disabled.

- A Bedrock provider will appear once you have AWS configured, i.e. `aws configure`
- Crush also expects the `AWS_REGION` or `AWS_DEFAULT_REGION` to be set
- To use a specific AWS profile set `AWS_PROFILE` in your environment, i.e. `AWS_PROFILE=myprofile crush`
- Alternatively to `aws configure`, you can also just set `AWS_BEARER_TOKEN_BEDROCK`

### Vertex AI Platform

Vertex AI will appear in the list of available providers when `VERTEXAI_PROJECT` and `VERTEXAI_LOCATION` are set. You will also need to be authenticated:

```bash
gcloud auth application-default login
```

To add specific models to the configuration, configure as such:

```json
{
  "$schema": "https://charm.land/crush.json",
  "providers": {
    "vertexai": {
      "models": [
        {
          "id": "claude-sonnet-4@20250514",
          "name": "VertexAI Sonnet 4",
          "cost_per_1m_in": 3,
          "cost_per_1m_out": 15,
          "cost_per_1m_in_cached": 3.75,
          "cost_per_1m_out_cached": 0.3,
          "context_window": 200000,
          "default_max_tokens": 50000,
          "can_reason": true,
          "supports_attachments": true
        }
      ]
    }
  }
}
```

### Local Models

Local models can also be configured via OpenAI-compatible API. Here are two common examples:

#### Ollama

```json
{
  "providers": {
    "ollama": {
      "name": "Ollama",
      "base_url": "http://localhost:11434/v1/",
      "type": "openai-compat",
      "models": [
        {
          "name": "Qwen 3 30B",
          "id": "qwen3:30b",
          "context_window": 256000,
          "default_max_tokens": 20000
        }
      ]
    }
  }
}
```

#### LM Studio

```json
{
  "providers": {
    "lmstudio": {
      "name": "LM Studio",
      "base_url": "http://localhost:1234/v1/",
      "type": "openai-compat",
      "models": [
        {
          "name": "Qwen 3 30B",
          "id": "qwen/qwen3-30b-a3b-2507",
          "context_window": 256000,
          "default_max_tokens": 20000
        }
      ]
    }
  }
}
```

## Client/Server API

Crush runs as a client/server pair by default. `crush server` starts a
standalone backend; `crush reload` and `crush shutdown` control a running
one. The HTTP API is documented by an embedded Swagger UI at `/v1/docs/`
(spec at `/v1/docs/doc.json`).

## Logging

Sometimes you need to look at logs. Luckily, Crush logs all sorts of
stuff. Logs are stored in `./.crush/logs/crush.log` relative to the project.

The CLI also contains some helper commands to make perusing recent logs easier:

```bash
# Print the last 1000 lines
crush logs

# Print the last 500 lines
crush logs --tail 500

# Follow logs in real time
crush logs --follow
```

Want more logging? Run `crush` with the `--debug` flag, or enable it in the
config:

```json
{
  "$schema": "https://charm.land/crush.json",
  "options": {
    "debug": true,
    "debug_lsp": true
  }
}
```

## Metrics

This fork does not collect usage metrics or telemetry. The `disable_metrics`
config option and `CRUSH_DISABLE_METRICS` environment variable are retained for
compatibility with upstream Crush, and the [`DO_NOT_TRACK`](https://donottrack.sh/)
convention is also respected, but no metrics are sent regardless.

```bash
export CRUSH_DISABLE_METRICS=1
```

```json
{
  "options": {
    "disable_metrics": true
  }
}
```

## Q&A

### Why is clipboard copy and paste not working?

Installing an extra tool might be needed on Unix-like environments.

| Environment         | Tool                     |
| ------------------- | ------------------------ |
| Windows             | Native support           |
| macOS               | Native support           |
| Linux/BSD + Wayland | `wl-copy` and `wl-paste` |
| Linux/BSD + X11     | `xclip` or `xsel`        |

## Contributing

Contributions to this fork are welcome — open an issue or pull request on
[github.com/taigrr/crush](https://github.com/taigrr/crush). For the upstream
project, see its [contributing guide](https://github.com/charmbracelet/crush?tab=contributing-ov-file#contributing).

## License

Upstream Crush is [FSL-1.1-MIT](https://github.com/taigrr/crush/raw/main/LICENSE.md),
converting to MIT on 2028-04-24. Fork modifications by taigrr are licensed
under MIT, effective immediately.

---

A fork of [Crush](https://github.com/charmbracelet/crush), part of [Charm](https://charm.land).

<a href="https://charm.land/"><img alt="The Charm logo" width="400" src="https://stuff.charm.sh/charm-banner-softy.jpg" /></a>

<!--prettier-ignore-->
Charm热爱开源 • Charm loves open source
