# Bang mode & LSP tools: fork vs upstream comparison

## Bang mode

### Architectural difference (fundamental)
- **Ours**: server-side, **blocking**, buffered. `runShellCommand` →
  `AgentRunShellCommand` → backend `RunShellCommand` runs `shell.Run` with
  `bytes.Buffer` stdout/stderr, then persists a **Shell-role message**
  (command part + output part). Detection: `!` prefix at start of the
  editor value. Client/server-aware cwd, shebang dispatch, process-group
  kill helper.
- **Upstream**: client-side **streaming `ShellItem`** widget with
  incremental `AppendOutput`, pending spinner, tail-while-running, ANSI16
  remap, in-widget copy, and mid-run cancel.

These do not map 1:1 — a full streaming port would need shell output
events plumbed through our SSE bus.

### What upstream does better (candidate improvements)
| # | Upstream feature | Our status | Effort | Worth? |
|---|------------------|-----------|--------|--------|
| 1 | **Streaming output + pending spinner** (`6814ccfc`,`6db54b3a`) | We block; long builds show nothing until done | Large (SSE shell events) | ~ maybe |
| 2 | **Cancel mid-run** (`47ed0f3a`) | `runShellCommand` uses `context.Background()` — NOT cancelable; process-group kill wiring exists but unreachable | Medium | [x] yes |
| 3 | **Interleave stderr/stdout** (`48e9cca2`) | We append stderr after all stdout — can misorder | Small (shared writer) | [x] yes |
| 4 | **Include bang cmds in history** (`f342edf0`) | Check: does `!cmd` enter prompt history? | Small | [~] |
| 5 | **Engage on paste starting with `!`** (`7e4bd6a0`) | Prefix-at-start only | Small | [~] |
| 6 | **Activate when `!` preceded by whitespace** (`11662010`) | No | Small | [~] |
| 7 | **Don't add extra `!` browsing history** (`cb129202`) | N/A unless we add #4 | Small | [~] |
| 8 | **ANSI16 → theme remap** (`1c2da893`) | No `RemapANSI16` | Small-med | [n] cosmetic |
| 9 | **Copy result** (`f46387bb`) | Check `ShellMessageItem` copy support | Small | [~] |
| 10 | **Sync bang with external editor** (`d20e29ae`) | No | Small | [n] |

Recommended now: **#2 (cancelable)** and **#3 (interleaved output)** — both
fit our blocking architecture and are correctness/UX wins. Streaming (#1)
is a larger, separate project.

## LSP tools

### Both have
definition, references, rename, document_symbols (upstream: `lsp_symbols`),
diagnostics, lsp_restart. Implementations differ but cover the same ground.

### Upstream-only (candidates to add)
- **`lsp_call_hierarchy`** (`cd8c06ce`) — incoming/outgoing calls for a
  symbol. Genuinely new navigation capability. [x] worth adding.
- **`lsp_replace_symbol`** (`cd8c06ce`) — replace a whole symbol body via
  LSP ranges. Precise structural edit; overlaps our edit/multiedit but
  safer for whole-function replaces. [~] maybe.
- **`lsp_helpers.go`** — shared helpers (symbol resolution, formatting)
  underpinning the above; would come along with them.

### Naming/polish
Upstream prefixes every tool `lsp_*`; ours is mixed
(`definition.go` + `lsp_restart.go`). Cosmetic; our tool *names* exposed to
the model already are `lsp_definition` etc. (see tool registration), so no
functional gap — skip renaming files.

### Recommendation
Add **`lsp_call_hierarchy`** (new capability); consider
`lsp_replace_symbol`. Our word-boundary symbol grep + LSP validation
(`b9a1182b`) — verify we have the equivalent; if not, cheap to add.

## Next actions (proposed)
1. Bang: make `runShellCommand` cancelable + interleave stdout/stderr.
2. LSP: port `lsp_call_hierarchy` (+ helpers), evaluate `lsp_replace_symbol`.
