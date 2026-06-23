# Embeddings and Vector Search — Technical Specification

Status: design draft. Depends on the embedding support added to the
`fantasy` fork (`fantasy.EmbeddingProvider` / `EmbeddingModel`) and the
catwalk fork (`Provider.EmbeddingModels`, `Model.Dimensions`,
`Model.IsEmbedding`).

This spec is written to compose with the cloud sync design
(`docs/sync-spec.md`) and the project-root/fingerprint model
(`docs/specs/WORKTREES_AND_SNAPSHOTS.md` §1). Where the two interact,
this document defers to the sync spec's mechanisms (`_changelog`
triggers, push-on-open/close, per-project Durable Object, tenant
identity) rather than inventing parallel ones.

## 1. Goals

- Let a user pick **one** embedding model and use it everywhere, with
  BYOK, set **once** in global config.
- Store vectors per-project in the existing `crush.db` (SQLite, CGO-free,
  no extension), searchable in-process in Go.
- Make embedder changes safe: switching embedders invalidates every
  stored vector, and the system must never silently mix embedding
  spaces.
- Work in both local and cloud (client/server + sync) modes, keeping
  API keys client-side.

## 2. The core invariant: the embedding-space signature

Vectors are only comparable within the same embedding space. We capture
that space as a stable signature:

```
signature = sha256(provider_id "\n" model_id "\n" dimensions "\n" normalize)
```

Rules:

- Every stored vector records the signature that produced it.
- Search only ever compares vectors sharing the **active** signature.
- A change to provider, model, dimensions, or normalization yields a new
  signature and therefore invalidates all prior vectors. There is no
  partial migration — vectors are recomputed, never converted.

The signature — not the raw model id — is the unit of compatibility
everywhere below (local DB, sync DO, dashboard).

## 3. Configuration — global only, set once

### 3.1 Where it lives

A dedicated block in the **global** config (`~/.config/crush/crush.json`,
i.e. `config.GlobalConfig()`). It reuses the existing provider /
BYOK resolution path so keys come from the same place as chat models
(env, config, keyring), and `provider` matches a key in the providers
config exactly like `SelectedModel.Provider`.

```jsonc
// ~/.config/crush/crush.json
{
  "embedding": {
    "provider": "bedrock",
    "model": "amazon.titan-embed-text-v2:0",
    "dimensions": 1024,
    "normalize": true,
    "hybrid_search": true
  }
}
```

New type in `internal/config/config.go`:

```go
// EmbeddingConfig selects the single, global text-embedding model.
// It is intentionally NOT part of Config.Models: embeddings are a
// singular, global choice, not a per-task selection, and must never be
// overridden per workspace.
type EmbeddingConfig struct {
    Provider   string `json:"provider"`
    Model      string `json:"model"`
    Dimensions int64  `json:"dimensions,omitempty"`
    Normalize  bool   `json:"normalize,omitempty"`
    // HybridSearch controls whether search_history fuses the semantic
    // (vector) signal with substring matching. Default true. When false,
    // search_history is pure substring search and no query embeddings are
    // computed. Vectors may still be stored for other consumers.
    HybridSearch *bool `json:"hybrid_search,omitempty"`
}

// Signature returns the embedding-space identity (see §2). Note:
// HybridSearch is NOT part of the signature — toggling it does not
// invalidate stored vectors.
func (e EmbeddingConfig) Signature() string { /* sha256 of provider/model/dims/normalize */ }
```

`Config.Embedding *EmbeddingConfig` (nil = embeddings disabled, which
also means `search_history` is pure substring search). `HybridSearch`
defaults to `true` when an embedder is configured; a `*bool` so "unset"
is distinguishable from an explicit `false`.

### 3.2 Global-scope enforcement

Unlike `options.tui.theme` (which deliberately allows a workspace
override — ROADMAP #1), `embedding` is **rejected at workspace scope**.
If an `embedding` block appears in a project `.crush/crush.json`, load
emits a warning and ignores it. Rationale: a per-project embedder would
fragment the embedding space across projects, defeating "same embedder
everywhere" and silently producing incomparable vectors.

Implementation: in `load.go`, after merging, if the workspace layer
contributed an `embedding` key, drop it and warn. Writes go through
`SetConfigField(ScopeGlobal, "embedding", …)` only; a workspace-scoped
write of `embedding` returns an error.

### 3.3 Choosing the embedder (CLI + TUI)

- `crush embeddings` (new cobra command, `internal/cmd/`):
  - `crush embeddings list` — embedding models from catwalk
    (`Provider.EmbeddingModels`) across configured providers.
  - `crush embeddings set <provider> <model> [--dimensions N]
    [--normalize]` — writes the global block, recomputes the signature,
    and triggers the mismatch flow (§5) on next project open.
  - `crush embeddings status` — active signature, and for the current
    project: vector count, how many match vs. are stale.
- Command-palette entry mirrors the picker pattern used by the theme
  dialog (`internal/ui/dialog/theme.go`): a filterable list of embedding
  models, confirm persists to global config. Because this is a global,
  rarely-changed setting, the picker shows a confirmation when changing
  away from a signature that already has stored vectors ("this will
  invalidate N vectors across M projects on this machine").

## 4. Per-project storage

### 4.1 Schema (new migration)

```sql
-- Vectors for searchable content. Local to each project's crush.db.
CREATE TABLE embeddings (
    source_type TEXT    NOT NULL,           -- 'message' | 'file_chunk'
    source_id   TEXT    NOT NULL,           -- messages.id, etc.
    chunk_idx   INTEGER NOT NULL DEFAULT 0, -- for chunked sources
    signature   TEXT    NOT NULL,           -- embedding space (§2)
    dim         INTEGER NOT NULL,
    vec         BLOB    NOT NULL,           -- float32 little-endian, dim*4 bytes
    created_at  INTEGER NOT NULL,           -- ms epoch
    PRIMARY KEY (source_type, source_id, chunk_idx, signature)
);

CREATE INDEX idx_embeddings_signature ON embeddings(signature);
CREATE INDEX idx_embeddings_source ON embeddings(source_type, source_id);
```

The signature is part of the primary key so a re-embed under a new
signature can coexist transiently with the old (enabling background
re-embed before dropping the stale set), and so a stale set is a trivial
`DELETE WHERE signature != ?`.

Storage cost: 1024-dim float32 = 4 KiB/vector; well under the DO 2 MB
row cap (§sync 2). Thousands of messages ≈ single-digit MB.

### 4.2 Active signature record

The project's currently-populated signature is tracked in the existing
`sync_metadata` table (no new table needed):

```
sync_metadata['embedding_signature'] = <hex>   -- what THIS db is populated with
```

On open, compare `config.Embedding.Signature()` (the truth) against this
recorded value to detect a switch (§5).

## 5. Switching embedders — lazy, per-database invalidation

We cannot cheaply enumerate every `.crush` on a machine to migrate them
eagerly. So each database self-heals when it is opened (the scanner in
the sync spec can opportunistically pre-warm, but correctness lives at
open time):

1. `crush embeddings set …` updates global config; the new signature
   takes effect immediately as the active one.
2. On the next open of any project db, if
   `active_signature != sync_metadata['embedding_signature']`:
   - Vectors under the old signature are **stale**. Search refuses to
     run against a stale space (returns "index out of date" rather than
     mixing or returning garbage).
   - Resolution policy (config `embedding.on_mismatch`, default
     `reindex`):
     - `reindex` — background re-embed of existing sources under the new
       signature; when complete, `DELETE WHERE signature != active`,
       then set `sync_metadata['embedding_signature'] = active`.
     - `wipe` — `DELETE FROM embeddings`; set the active signature; index
       lazily on demand.
   - Re-embedding is rate-limited and resumable; it is plain API calls
     and costs money, so it surfaces progress (popup alert, same
     component as sync's open-time pull, §sync 17.2) and is cancellable.

## 6. Search

Brute-force cosine in Go, scoped to the active signature:

```go
// internal/embedding (new package)
func Search(ctx, query string, k int, filter SourceFilter) ([]Hit, error)
```

- Embed the query with the active model, then scan
  `embeddings WHERE signature = active` for the top-k by cosine
  similarity. With `normalize=true` (Titan/Cohere/OpenAI all support it)
  cosine reduces to a dot product.
- For a single user's history (≤ ~10^4–10^5 vectors) brute force is
  single-digit ms; no ANN index, no extension, no new dependency. An ANN
  layer is explicitly out of scope until a real corpus exceeds ~10^5
  vectors.
- Optional small per-process cache of decoded float32 slices keyed by
  signature to avoid repeated BLOB decode.

### 6.1 Hybrid search — one tool, both signals

There is **one** history-search surface, not two. Exact substring and
semantic similarity are different signals over the **same** message
rows, so they are fused into a single tool (`search_history`) rather
than split into competing tools the agent has to choose between. The
agent should never have to guess "is this a keyword query or a meaning
query" — it asks once and gets the best of both.

The two signals it fuses:

- **Exact (substring):** "find the literal `AWS_BEARER_TOKEN_BEDROCK`",
  an error message, a path, a flag. Case-folded literal matching; no
  model call; always available.
- **Semantic (vector):** "where did we discuss auth token rotation".
  Matches meaning; requires the active embedder; scoped to the active
  signature (§2).

Fusion model:

- Run both, merge with **Reciprocal Rank Fusion** (RRF):
  `score(d) = Σ 1/(k + rank_i(d))` over each signal that returned `d`
  (k≈60). RRF needs no score calibration between the two scales and
  naturally rewards documents both signals agree on. Exact-only and
  semantic-only hits still surface; agreement just ranks higher.
- Each result is tagged with why it matched (`exact`, `semantic`, or
  `both`) so the output stays legible.
- Existing `search_history` filters (session/`current`, role scope,
  pagination) apply to the fused result set unchanged.

Degradation and the toggle:

- **`vector` toggle, default on.** A global config flag
  (`embedding.hybrid_search`, default `true`) controls whether the
  semantic signal participates. When off — or when embeddings are
  genuinely unavailable (no embedder configured, signature stale
  mid-reindex, offline, query embed fails) — `search_history`
  **transparently degrades to pure substring search**. Same tool, same
  params, same output shape; it just stops contributing the semantic
  signal. The agent never has to fall back to a different tool.
- A per-call override (`semantic: false`) lets the agent force
  pure-exact for a single query (e.g. when it truly wants a literal
  match and nothing fuzzier), without changing global config.
- Because degradation is silent-but-safe, there is no "index
  unavailable" error path that blocks a search — worst case it behaves
  exactly like today's substring `search_history`.

### 6.2 User-facing search surfaces

The same hybrid search the agent uses is exposed directly to the user in
two places. Both call the **one** search service (§6.1) — no separate
code path — so ranking is identical to what the agent sees.

A single ranked-hit shape backs all surfaces:

```go
// internal/embedding (or internal/search)
type Hit struct {
    Rank         int     // 1-based fused rank
    Score        float64 // RRF score
    Match        string  // "exact" | "semantic" | "both"
    SessionID    string  // full id
    SessionTitle string
    MessageID    string  // full id — the jump target
    Role         string
    CreatedAt    time.Time
    Snippet      string  // matched region with context
}
```

#### CLI — `crush search`

```
crush search [flags] <query>
  --session <id|current>   scope to one session
  --scope user|all         role filter (default user)
  --limit N                max hits (default 20)
  --offset N               pagination
  --no-vector              force pure substring (overrides hybrid_search)
  --json                   machine-readable output
```

- **Default output is a table** with **all columns**: rank, score,
  match (exact/semantic/both), session id, title, message id, role,
  timestamp, snippet. Rendered with the same lipgloss table styling used
  by `crush session list` (truncate snippet/title to terminal width,
  pager when long). Rank order is the fused RRF order.
- **`--json`** emits the `[]Hit` array verbatim (RFC3339 timestamps,
  full ids, `SetEscapeHTML(false)`), mirroring the `--json` convention
  on `crush session`. This is the agent/script path.
- Reuses the read-only `db.Connect` + service setup pattern from
  `internal/cmd/session.go` (`sessionSetup`). No agent, no LLM unless
  the semantic signal needs a query embedding — which it does only when
  hybrid is on and an embedder is configured.

#### Command palette — "Search History"

- New command-palette entry (`internal/ui/dialog/commands.go`) opening a
  search dialog (`internal/ui/dialog/search.go`), styled like the
  sessions picker: a query input on top, a live-updating ranked result
  list below. Each row shows match-type badge, title, role, timestamp,
  and snippet with the matched span highlighted.
- Debounced query → hybrid search → results; arrow keys move the
  selection, `enter` **jumps to that message** (§6.3).
- A toggle in the dialog flips semantic on/off for the session without
  touching global config (maps to the per-call `semantic` override).

If the palette entry gets a global keybinding, add it to `FullHelp()` in
`internal/ui/model/ui.go` (Ctrl+G overlay) per the repo convention, and
document `embedding.hybrid_search` in the `crush-config` builtin skill.

### 6.3 Jump to conversation and message

Selecting a result (palette `enter`, or a future `crush search --open`)
navigates straight to the matched message. The mechanics already exist:

- **Switch conversation:** `m.loadSession(sessionID)` (the existing
  `ActionSelectSession` path, `internal/ui/model/ui.go`).
- **Scroll to the message:** resolve `MessageID` → chat-list index after
  the session loads, then `m.chat.SetSelected(idx)` +
  `m.chat.ScrollToSelectedAndAnimate()` — the same mechanism milestones
  use via `ActionScrollToTurn`. A new `ActionJumpToMessage{SessionID,
  MessageID}` carries the target; the handler loads the session (if not
  current) and, once messages are present, maps id→index and scrolls.
- Index resolution: the chat list is built from the session's messages
  in `created_at` order, so id→index is a lookup over the loaded set.
  If the message is filtered from the visible list (e.g. a tool-result
  part), fall back to the nearest visible ancestor message.

## 7. Cloud mode

### 7.1 Identity: the embedder is a tenant-global choice

In the sync model a **tenant** is the user identity and owns N devices
(`docs/sync-spec.md` §16). "Same embedder across all projects" maps
cleanly onto "one embedding signature per tenant":

- The canonical embedding signature is recorded **server-side at the
  tenant level**, established by the first device that pushes vectors.
- API keys and the embedder client stay **client-side**. The server
  never needs the key; it only stores the resulting vectors and the
  signature string. BYOK is preserved.
- A device whose local `config.Embedding.Signature()` disagrees with the
  tenant's canonical signature is warned on auth/open ("this account is
  indexed with `<model>`; change your embedder or re-index the account")
  and is blocked from pushing mismatched vectors — preventing a second
  machine from corrupting the shared space.

### 7.2 Do embeddings sync? Phased.

The sync transport (`_changelog` triggers, per-project DO merge,
push-on-open/close, schema-version negotiation) can carry an
`embeddings` table, and the DO holds plaintext (server-readable), which
would enable **dashboard semantic search** and **cloud-agent
orchestration** (stated sync goals). But syncing vectors adds a syncable
table, a DO merge rule, a schema-version bump, quota weight, and the
cross-device signature guard. Therefore:

- **Phase A (local cache):** `embeddings` is **not** a syncable table —
  no `_changelog` triggers on it. Each device computes and stores its own
  vectors locally. Zero impact on the sync protocol or quotas. This is
  the default and the starting point.
- **Phase B (synced vectors):** add `_changelog` triggers for
  `embeddings` (INSERT/DELETE only — vectors are immutable per
  signature, like `snapshots`), a DO merge rule (`INSERT OR IGNORE`
  keyed on `(source_type, source_id, chunk_idx, signature)`), a
  schema-version bump, and the §7.1 tenant-signature guard enforced in
  the DO: a push carrying vectors whose signature ≠ the tenant canonical
  is rejected with a clear "re-index required" error (pulls/bootstrap
  still succeed, mirroring the quota-overage rule §sync 20.2).

### 7.3 Why not put vectors in R2 / blobs

Vectors are tiny (KB) and queried in aggregate; they belong in the
relational mirror where a future server-side search can scan them, not
in R2 overflow (which exists for >1.5 MB message parts, §sync 6.5).

## 8. What gets embedded (initial scope)

- **Phase 1:** assistant/user **messages** (`source_type='message'`,
  `source_id=messages.id`, `chunk_idx=0`). Append-only, UUID-keyed —
  ideal for both local storage and (Phase B) changeset replay.
- **Later:** file chunks for repo-aware retrieval
  (`source_type='file_chunk'`), which needs a chunker and a
  re-embed-on-change story keyed off the existing `files.version`.

Chunking, retrieval-augmented prompting, and how search results feed the
agent are deliberately out of scope here; this spec covers the storage,
configuration, invalidation, and sync substrate only.

Note the role asymmetry this closes: exact `search_history` defaults to
user/shell messages (assistant text only when a session is scoped),
whereas embeddings index **all roles** uniformly. Once the semantic
signal is fused into `search_history` (§6.1), the same tool can recall
prior assistant reasoning across sessions — which pure substring search
cannot do today. That is a primary motivation for vector search.

## 9. Package / file layout

```
internal/embedding/
  embedding.go     // Service: Embed, Search, signature handling
  store.go         // sqlc-backed CRUD over the embeddings table
  cosine.go        // float32 encode/decode + similarity
  reindex.go       // background re-embed on signature mismatch
  fuse.go          // RRF merge of exact + semantic result lists
internal/db/sql/embeddings.sql        // sqlc queries
internal/db/migrations/<ts>_add_embeddings.sql
internal/config/ (Embedding field, Signature, global-scope guard)
internal/cmd/embeddings.go            // list/set/status
internal/cmd/search.go                // `crush search` (table + --json)
internal/agent/tools/search_history.go // hybrid: substring + (optional) vector, fused
internal/ui/dialog/embedding.go       // embedder picker (mirrors theme dialog)
internal/ui/dialog/search.go          // search palette dialog + result list
internal/ui/dialog/{commands,actions}.go // "Search History" entry, ActionJumpToMessage
```

## 10. Build order

1. **fantasy + catwalk** embedding support — **done** (separate forks).
2. Config: `Embedding` field + `Signature()` + global-scope enforcement
   + schema regen + `crush-config` skill update.
3. Migration + sqlc + `internal/embedding` store and cosine search.
4. CLI (`crush embeddings list/set/status`) + TUI picker.
5. Wire message embedding on persist; lazy mismatch handling (§5).
6. Fuse the semantic signal into `search_history` (RRF, §6.1) behind the
   `hybrid_search` toggle; transparent degradation to substring-only.
7. User surfaces (§6.2): `crush search` (table + `--json`), command-palette
   "Search History" dialog, and `ActionJumpToMessage` for jump-to (§6.3).
8. **Phase B only:** `embeddings` changelog triggers, DO merge rule,
   schema-version bump, tenant-signature guard.

## 11. Open items

- **RRF tuning** — `k` constant and whether to weight the two signals
  (e.g. boost exact for short/code-like queries). Start unweighted,
  k=60; revisit with real queries.
- **`on_mismatch` default** — `reindex` (nicer, costs API calls silently
  in the background) vs `wipe` (cheaper, indexes on demand). Leaning
  `reindex` with a visible, cancellable progress alert.
- **Phase A vs B timing** — ship local-cache first; gate synced vectors
  behind real demand for dashboard/cloud-agent search.
- **Quota weighting** for vectors in Phase B (they count toward the
  tenant 1 GB; 4 KB/vector is cheap but unbounded over time).
- **Multi-provider signature portability** — two providers that both
  output 1024-dim normalized vectors are still **not** comparable; the
  signature includes `provider_id` precisely to prevent that. Confirm we
  never want a "same dims = compatible" shortcut (we don't).
- **Worktree fingerprint collision** (§sync 15) interacts with Phase B:
  sibling worktrees sharing a fingerprint would share a DO and thus a
  vector set; fine as long as the signature matches (it always will,
  being tenant-global).
