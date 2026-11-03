# Device Bridge Wire Format (v1)

Status: normative contract for phase 2b of [MOBILE_PAIRING.md](MOBILE_PAIRING.md).
It refines and supersedes the informal envelopes in
[Subject namespace](MOBILE_PAIRING.md#subject-namespace),
[NATS-to-HTTP bridge](MOBILE_PAIRING.md#nats-to-http-bridge), and
[Event streams](MOBILE_PAIRING.md#event-streams).
Where they disagree, this document wins.

Audience: the rush client (Expo / React Native, `@nats-io/nats-core`) and the node-side bridge (`internal/server/devicebridge.go`).

## Conventions

- `<dev>` is the device ID (lowercase, as issued at pairing). `<node>` is a node ID. `<stream>` is a stream ID issued by a node.
- All payloads are UTF-8 JSON objects, one per NATS message, no NATS headers required (nodes ignore headers they do not know).
- Field names are `snake_case`. Integers fit in an IEEE double (at most 2^53 - 1). Timestamps are RFC 3339 strings in UTC with millisecond precision (`2026-11-02T20:16:44.123Z`).
- Binary data is base64url without padding.
- "MUST", "SHOULD", "MAY" are used as in RFC 2119.
- Every envelope carries `"v": 1`. A message without `v` is treated as `v: 1` (compatibility with phase 2a clients).

## Connection requirements

- The device connects with its nkey and `inboxPrefix: "_INBOX.<dev>"` (plain `_INBOX.>` is denied by its NATS permissions).
- Every request MUST use a reply subject under `_INBOX.<dev>.`.
  A node never answers a request whose reply subject is anything else; the request is dropped silently (no error, since the reply subject is the only place an error could go).
- Nodes only ever publish to `crush.client.v1.<dev>.*`, `crush.client.v1.broadcast.*`, and `_INBOX.<dev>.*`.

## Subject map

| Subject | Direction | Pattern | Purpose |
| --- | --- | --- | --- |
| `crush.client.v1.<dev>.info` | device → node | request / reply | Hello: versions, features, limits, the device's role and scopes. |
| `crush.client.v1.<dev>.api.<node>` | device → node | request / reply | One bridged `/v1` call executed on `<node>`. `<node>` = `any` lets the entry node route (see [Routing](#cross-node-routing)). |
| `crush.client.v1.<dev>.sub` | device → node | request / reply | Open (or resume) an event stream. |
| `crush.client.v1.<dev>.ev.<stream>` | node → device | publish | Event frames for one stream. |
| `crush.client.v1.<dev>.ack.<stream>` | device → node | publish | Flow control: consumed sequence and credit. |
| `crush.client.v1.<dev>.unsub` | device → node | request / reply (reply optional) | Close a stream. |
| `crush.client.v1.<dev>.control` | node → device | publish | `revoked`, `scopes_changed`, `reauth`. |
| `crush.client.v1.<dev>.attention` | node → device | publish | Attention events filtered for this device (see [Attention](#attention)). |
| `crush.client.v1.broadcast.attention` | node → devices | publish | Deprecated alias kept for phase 2a clients; same payload, unfiltered by scope beyond `sessions:read`. |
| `crush.client.v1.broadcast.nodes` | node → devices | publish | Cluster heartbeat summary (unchanged from MOBILE_PAIRING.md). |
| `crush.client.v1.<dev>.trust`, `.members` | node → device | publish | Unchanged from MOBILE_PAIRING.md. |

The device SHOULD subscribe to `crush.client.v1.<dev>.>` once (covers `ev`, `control`, `attention`, `trust`, `members`) plus `crush.client.v1.broadcast.nodes`.

Mesh-internal (members only, never visible to devices): `crush.client.fwd.<node>` carries a forwarded device request from the entry node to the owning node, wrapped as described in [Cross-node routing](#cross-node-routing).

## Hello (`info`)

Request (all fields optional):

```json
{ "v": 1, "client": "rush/1.4.0 (ios 19.1)", "features": ["json_body", "stream_resume"] }
```

Reply:

```json
{
  "v": 1,
  "versions": [1],
  "node_id": "n_7f3a9c",
  "node_name": "studio",
  "device_id": "d_q2w3e4r5t6y7u8i9",
  "role": "admin",
  "scopes": ["sessions:read", "sessions:prompt", "permissions:answer", "permissions:always", "config:model", "sessions:move", "devices:approve", "shell", "cluster:approve"],
  "features": ["json_body", "stream_resume", "stream_session_filter", "heartbeat", "swarm_target", "device_attention"],
  "limits": {
    "max_request_bytes": 4194304,
    "max_response_bytes": 4194304,
    "max_inflight_requests": 8,
    "max_streams": 16,
    "max_credit": 1024,
    "request_timeout_ms": 30000,
    "heartbeat_ms": 15000,
    "resume_window_ms": 60000,
    "resume_buffer_frames": 512
  }
}
```

Rules:

- `features` in the reply is the set the node supports; the device uses a feature only if it appears in the reply. The request's `features` are informational (logged, used for metrics).
- `limits` are per device per node. The device MUST treat absent limits as the defaults shown above.
- Rush SHOULD call `info` after every (re)connect; it is cheap and also confirms the device is not revoked (a revoked device gets no reply and its connection is closed).

Defined feature flags (v1):

| Feature | Meaning |
| --- | --- |
| `json_body` | Requests and responses may use `json` instead of base64 `body`. |
| `swarm_target` | `target.swarm` routing is supported on `api.any` and `sub`. |
| `stream_resume` | `sub` accepts `resume`. |
| `stream_session_filter` | Workspace streams accept `session_id` in `query`. |
| `heartbeat` | Streams emit `heartbeat` frames. |
| `device_attention` | Attention is published on `crush.client.v1.<dev>.attention`. |
| `idempotency` | `idempotency_key` is honored on `POST`. |

## API request / response

### Request envelope

Published on `crush.client.v1.<dev>.api.<node>` as a NATS request.

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `v` | integer | yes | `1`. |
| `id` | string | yes | Client request ID, 1 to 128 bytes, echoed in the reply. Unique per device for at least 10 minutes. |
| `method` | string | yes | `GET` or `POST`. Anything else: `405 method_not_allowed`. |
| `path` | string | yes | Concrete canonical `/v1/...` path (`/v1/workspaces/ws_1/sessions`), no query, no `.`/`..` segments, no `//`, no `%`, no `\`. Otherwise `400 bad_request`. |
| `query` | object | no | `{ "name": "value" }` or `{ "name": ["a", "b"] }`; values are strings. `client_id` is stripped. |
| `headers` | object | no | String to string. Only `content-type` is honored. |
| `json` | any | no | Request body as inline JSON (feature `json_body`); implies `content-type: application/json`. |
| `body` | string | no | Request body as base64url. At most one of `json` and `body`. |
| `target` | object | no | `{ "node": "<id>" }` or `{ "swarm": "<color-animal[-xxxx]>" }`, only valid on `api.any` (see [Routing](#cross-node-routing)). |
| `idempotency_key` | string | no | 1 to 64 bytes `[A-Za-z0-9_-]`. For `POST` only (feature `idempotency`). |
| `timeout_ms` | integer | no | 1000 to 30000; default and cap `request_timeout_ms`. |

### Response envelope

Delivered on the request's reply subject.

| Field | Type | Always | Notes |
| --- | --- | --- | --- |
| `v` | integer | yes | `1`. |
| `id` | string | yes | Echo of the request `id` (empty if the request was unparseable). |
| `status` | integer | yes | HTTP status of the `/v1` call, or the bridge's own status for bridge errors. |
| `node_id` | string | yes | Node that executed (or refused) the call. |
| `headers` | object | no | Only `content-type` today. |
| `json` | any | no | Response body if it is `application/json` and the request used `json` or the node supports `json_body`. |
| `body` | string | no | Response body as base64url otherwise. |
| `error` | object | no | Present iff the bridge (not the `/v1` handler) refused or failed the call, or the `/v1` handler returned a non-2xx JSON error. See [Errors](#errors). |
| `replayed` | boolean | no | `true` when served from the idempotency cache. |

A `/v1` handler error (for example `404` for an unknown session) is returned as-is in `status` and `json`, and the node additionally fills `error` with `code` derived from the status (`not_found`, `conflict`, `bad_request`, `internal`) so clients can switch on a single field.

### Limits, encoding, chunking

- Request and response bodies are capped at `max_request_bytes` / `max_response_bytes` (4 MiB) **after decoding**. Over the limit: `413 payload_too_large`. The whole NATS message (envelope plus base64 body) is capped by the server's `max_payload` (8 MiB).
- v1 has **no chunking**. Large data is fetched with the route's own pagination (`limit`, `before`, `after` query parameters where the route supports them). A feature flag `chunked` is reserved for a later version; v1 clients MUST NOT send chunk fields and nodes MUST ignore them.
- `GET` requests MUST NOT carry a body (`400 bad_request`).

### Idempotency

When `idempotency_key` is set on a `POST`, the owning node stores the response under `(device_id, idempotency_key)` for 10 minutes.
A retry with the same key and the same `method`, `path`, and body returns the stored response with `replayed: true`; with a different method, path, or body it returns `409 idempotency_conflict`.
A retry that arrives while the first call is still running waits for it.
Rush SHOULD set a key on every `POST` (prompts and permission answers especially) and reuse it when it retries after `timeout` or `node_unreachable`.

## Errors

`error` object:

```json
{ "code": "device_scope_denied", "message": "route requires scope permissions:answer", "retry_after_ms": 0, "scope": "permissions:answer" }
```

| Code | Status | Meaning | Client action |
| --- | --- | --- | --- |
| `bad_request` | 400 | Malformed envelope, non-canonical path, both `json` and `body`, body on `GET`, bad `target`. | Fix the request. |
| `device_revoked` | 401 | The device is revoked or unknown. Usually not delivered (the node stays silent); appears only on races where a reply is already in flight. | Wipe credentials, show "unpaired". |
| `device_scope_denied` | 403 | The route exists and is device-reachable, but the device lacks the scope (`scope` names it). Not produced now that devices have full access (kept for compatibility with older nodes). | Hide or disable the action. |
| `route_not_allowed` | 403 | The route is not device-reachable (no `x-crush-scope`, or `never`), or is a stream route called via `api`. | Bug in client; do not retry. |
| `not_found` | 404 | Unknown route, workspace, session, or stream (from `/v1` or the bridge). | |
| `node_not_found` | 404 | `<node>`, `target.node`, or `target.swarm` does not name a known member or session. | Refresh members / sessions. |
| `session_not_on_node` | 404 | A session-addressed route (see [Session routes](#session-routes)) named a session this node does not own. | Re-route to the session's `node_id` from `/v1/session-overviews`. |
| `unknown_root` | 404 | A `root` is not in the node's registry of known workspaces. Arbitrary paths are never opened. | Refresh `/v1/workspace-roots`. |
| `invalid_cursor` | 400 | A `cursor` or `before` value is malformed or no longer names a message. | Restart paging from the first page. |
| `method_not_allowed` | 405 | Method other than `GET`/`POST`. | |
| `conflict` | 409 | `/v1` conflict. | |
| `idempotency_conflict` | 409 | Same key, different request. | |
| `payload_too_large` | 413 | Body over the limit. | Paginate. |
| `rate_limited` | 429 | Over `max_inflight_requests`, `max_streams`, or a per-route limit. `retry_after_ms` set. | Back off. |
| `resume_unavailable` | 410 | `sub` with `resume` but the stream state is gone. | Re-fetch state, open a fresh stream. |
| `internal` | 500 | Unexpected node error. | Retry with backoff (same idempotency key). |
| `node_unreachable` | 502 | The owning node is a member but did not answer the forward. | Retry later; show node offline. |
| `unavailable` | 503 | The node is shutting down or the bridge is not ready. | Reconnect (possibly to another URL). |
| `timeout` | 504 | The call exceeded `timeout_ms`. The action may still have happened. | Retry only with the same idempotency key. |

Unknown codes MUST be handled by `status` class. Codes are stable public API and never renumbered or renamed.

## Permissions (route → scope)

Every operation in `internal/swagger/swagger.yaml` carries `x-crush-scope`, whose value is a scope from [Permissions](MOBILE_PAIRING.md#permissions) or `never`.
Deny by default: an operation without the annotation is unreachable, and `TestSwaggerRoutesHaveDeviceScope` fails the build.
Every route registered on the `/v1` server mux must also be documented and annotated (`TestServerRoutesHaveDeviceScope`), so a handler cannot be added without deciding its device scope; the only exceptions are method-less prefix routes (`/v1/nodes/{node}/{rest...}`, `/v1/docs/`), which the device router never matches.
The bridge's allow-list (`internal/server/devicescopes.go`) is generated from or checked against these annotations.

Every paired device holds every scope ([Permissions](MOBILE_PAIRING.md#permissions): there are no roles), so the table is in effect the **route allow-list**: a route tagged with any scope is reachable by every paired device, and `never` or untagged routes by none.
The allow-list and the device (not revoked) are checked on the **entry node** and again on the **owning node** against its own route table and registry copy; either may refuse.

Initial v1 set (everything else is `never` until annotated otherwise):

| Method and path | Scope | Bridge |
| --- | --- | --- |
| `GET /v1/workspaces` | `sessions:read` | api |
| `GET /v1/workspace-overviews` | `sessions:read` | api |
| `GET /v1/workspaces/{id}` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/archived` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/history` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/pending` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/messages` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/messages/user` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/milestones` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/agent` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/agent/sessions/{sid}` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/agent/sessions/{sid}/goal` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/list` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/agent/sessions/{sid}/prompts/queued` | `sessions:read` | api |
| `GET /v1/cluster/dashboard` | `sessions:read` | api |
| `GET /v1/swarm/resolve` | `sessions:read` | api |
| `POST /v1/workspaces/{id}/history/search` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/providers` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/lsps` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/lsps/{lsp}/diagnostics` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/mcp/states` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/sessions/{sid}/model` | `sessions:read` | api |
| `GET /v1/workspaces/{id}/events` | `sessions:read` | **sub only** |
| `GET /v1/events` | `sessions:read` | **sub only** |
| `POST /v1/workspaces/{id}/agent` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/sessions` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/tools/cancel` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/goal` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/goal/clear` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/title` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/summarize` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/clear` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/prompts/promote-btw` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/runs/{rid}/drop` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/sessions/{sid}/seen` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/sessions/{sid}/favorite` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/sessions/{sid}/archive` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/sessions/{sid}/unarchive` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/lsps/start` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/lsps/stop` | `sessions:prompt` | api |
| `POST /v1/voice/client-secret` | `sessions:prompt` | api |
| `POST /v1/workspaces/{id}/permissions/grant` | `permissions:answer` | api |
| `POST /v1/workspaces/{id}/questions/answer` | `permissions:answer` | api |
| `POST /v1/workspaces/{id}/permissions/skip` | `permissions:always` | api |
| `POST /v1/workspaces/{id}/config/model` | `config:model` | api |
| `POST /v1/workspaces/{id}/sessions/{sid}/model` | `config:model` | api |
| `POST /v1/sessions/move` | `sessions:move` | api |
| `POST /v1/workspaces/{id}/agent/sessions/{sid}/shell` | `shell` (opt-in) | api |

Body rules enforced by the bridge on top of the route scope:

- `POST .../agent`: `client_id` is overwritten with `device:<dev>`; `permission_policy` other than empty or `prompt` needs `permissions:always`.
- `POST .../permissions/grant`: `action: "allow_session"` needs `permissions:answer`; persistent grants need `permissions:always`.
- `never` (every device): provider keys and OAuth (`/providers` is a redacted view, see [Providers, LSPs, and MCP](#providers-lsps-and-mcp)), `config/set`, `config/remove`, cluster key ops, `debug`, replication, transfer, member API, `/v1/nodes/...` proxy paths.

### Roles

Device roles are gone: `info.role` is always `"admin"` and `info.scopes` lists every scope, whatever the stored record says (records written as `viewer` or `operator` are kept and parse unchanged).
`POST /v1/sessions/move` (`sessions:move`) is therefore reachable by every paired device; migrate and autobalance (`/v1/migrate/*`) stay `never`.
`scopes_changed` is no longer sent when a record's role is edited.

### Favorite is the pin

Rush's "pin" is the session favorite flag: `POST /v1/workspaces/{id}/sessions/{sid}/favorite` (`sessions:prompt`) with query `favorite=true` pins and `favorite=false` unpins (absent means `true`), and sessions carry it as `favorite` (omitted when false).
Favorites sort to the top of the inbox in the TUI too, so a pin on the phone and on the desktop is the same state.
There is no separate pin route or field.

## Providers, LSPs, and MCP

`GET /v1/workspaces/{id}/providers` returns a redacted array of providers: the catalog merged with the workspace's configured providers, configured ones first.
It never carries API keys, OAuth tokens, extra headers, extra body, base URLs, or any other secret material:

```json
[{"id": "anthropic", "name": "Anthropic", "type": "anthropic", "configured": true,
  "has_credential": true, "oauth": false,
  "default_large_model_id": "claude-...", "default_small_model_id": "claude-...",
  "models": [{"id": "claude-...", "name": "Claude ...", "context_window": 200000,
              "default_max_tokens": 50000, "can_reason": true, "reasoning_levels": ["low", "high"],
              "supports_attachments": true, "cost_per_1m_in": 3, "cost_per_1m_out": 15}]}]
```

`has_credential` reports that an API key or OAuth token is set; `oauth` that it is an OAuth token; `disabled` is present when the provider is disabled.

`GET .../lsps` (map of name to `{name, state, error, diagnostic_count, connected_at}`), `GET .../lsps/{lsp}/diagnostics`, `POST .../lsps/start` (`{"path": "..."}`), `POST .../lsps/stop`, and `GET .../mcp/states` (map of name to `{name, state, error, tool_count, prompt_count, resource_count, connected_at}`) are the `/v1` routes unchanged.

## Per-session model

`GET /v1/workspaces/{id}/sessions/{sid}/model` returns the model a session runs on:

```json
{"model_ref": "anthropic/claude-...", "large": {"provider": "anthropic", "model": "claude-..."},
 "small": {"provider": "anthropic", "model": "claude-haiku-..."}, "default": false}
```

`POST` (or `PUT` over HTTP) the same path with `{"model_ref": "..."}` (a role name, `provider/model`, or bare model id) or `{"model": {"provider": "...", "model": "..."}}` points the session's top-level turns at that model without touching the workspace default (`POST .../config/model`); it answers with the new view.
An empty body (`{}`) clears it, so the session follows the workspace's large model again (`default: true`).
Only the large slot is per-session: titles and summaries always use the workspace's small model, and `"model_type": "small"` is a 400.
An unknown model is a 400, an unknown session a 404, and a session owned by another node a 409 (route to its owner).
The session row's `model_ref` is the same field swarm workers are spawned with; it replicates with the session.

## Voice

`POST /v1/voice/client-secret` mints a short-lived xAI realtime secret so the phone can open `wss://api.x.ai/v1/realtime` directly without holding the node's credential.

Request: `{"session_id": "<sid>"}` (required; it selects the workspace whose config supplies the credential, falling back to the server's).
Response (`Cache-Control: no-store`):

```json
{"value": "<ephemeral secret>", "expires_at": 1767225600}
```

`expires_at` is unix seconds. The node calls `POST https://api.x.ai/v1/realtime/client_secrets` with `{"expires_after": {"seconds": 300}}`, authenticated with the first credential found:

1. `options.voice.api_key` (shell-expanded, so `$XAI_API_KEY` or `$(cmd)` work),
2. the `XAI_API_KEY` environment variable,
3. the Grok OAuth login (`providers.grok` OAuth token),
4. the `xai`, then `grok`, provider `api_key`.

`options.voice.base_url` overrides the xAI base URL.
Errors: missing `session_id` is 400; no credential is **424** with a message naming the options; xAI refusing is 502 (the upstream body is not echoed).
Neither the credential nor the minted secret is logged.

## History search

`POST /v1/workspaces/{id}/history/search` (`sessions:read`, api) **is** Crush's semantic (embedding) search: the same hybrid search the TUI palette and the `search_history` tool use (`internal/historysearch` over `internal/embedding`), fusing a substring signal and, when an embedder is configured, a vector signal by Reciprocal Rank Fusion.
It is the only search route; `/v1/workspaces/{id}/embeddings/*` (status, pending, backfill) stay `never`, and the bridge adds no search logic of its own.
It runs on the node that executes the request: over `api.any` without a target that is the entry node; send it to `api.<node>` to search another member's workspaces.

Request body (`proto.SearchHistoryParams`):

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `query` | string | required | Search text; the substring signal matches it case-insensitively. |
| `scope` | `"user"` or `"all"` | `"user"` | `user`: user and shell messages; `all`: also assistant replies and reasoning. |
| `semantic` | bool or absent | absent | Absent or `null`: follow the node's `hybrid_search` config. `true`: use the vector signal for this call (if an embedder is available). `false`: substring only. |
| `limit` | int | 50 (capped at 200) | Maximum sessions returned. |
| `all_workspaces` | bool | `false` | Also search every other workspace known to that node (attached and registry-detached) and merge the results. |
| `offset` | int | 0 | Reserved; ignored. |

Response body (`proto.SearchHistoryResult`): hits are per session (its best-scoring message wins), ranked by fused `score`.
This is the same search, ranking, and limits as `crush search` (the route, the TUI palette, and `crush search --all-workspaces` all call `historysearch.SearchAcross` with `historysearch.ResolveLimits`; plain `crush search` lists per-message hits from the same `historysearch.Search` underneath, and its `--json` carries the same `mode` and `fallback_reason`).

| Field | Type | Meaning |
| --- | --- | --- |
| `hits[].session_id`, `session_title` | string | The session. |
| `hits[].workspace_id` | string | The node's workspace ID; empty for a detached workspace found with `all_workspaces`. |
| `hits[].workspace_root` | string | Project root of the hit's workspace. |
| `hits[].score` | number | Fused Reciprocal Rank Fusion score (higher is better; only comparable within one response). |
| `hits[].match` | string | `exact` (substring signal only), `semantic` (vector signal only), or `both`. |
| `hits[].snippet` | string | Text around the match. |
| `hits[].message_id`, `role`, `created_at` | string | The best-matching message, its role (`user`, `assistant`, ...), and its time. |
| `total` | int | Distinct sessions found in the candidate window (before `limit`). |
| `mode` | string | `hybrid`: the vector signal ran and at least one candidate message has a stored vector, fused with the substring signal. `text`: substring only. Additive; absent from older nodes. |
| `fallback_reason` | string | Why `mode` is `text` (absent when `hybrid`): `no_embedder` (none configured), `disabled_by_request` (`semantic: false`), `disabled_by_config` (`semantic` absent and `hybrid_search` off), `no_embeddings` (embedder ran, but no candidate message has a vector yet, e.g. before backfill), `embed_error` (embedding the query failed). With `all_workspaces` the reason is the first one met in workspace order; it is absent if no workspace could be searched. A workspace that fails is skipped; the request fails only if every workspace fails. Additive; unknown values mean "text only, for some other reason". |
| `semantic_used` | bool | Kept for compatibility: whether the vector signal was attempted and did not error (true with `no_embeddings`). Prefer `mode`. |

When no embedder is configured, the search is still served (substring only): `mode: "text"`, `fallback_reason: "no_embedder"`, `semantic_used: false`, every `match` is `exact`. It is not an error.
`all_workspaces` only reaches workspaces on that node, all of which a paired device can already list and read (`GET /v1/workspace-overviews`, session routes), so it widens nothing.

```json
{ "v": 1, "id": "r7", "method": "POST", "path": "/v1/workspaces/ws_1/history/search", "json": { "query": "login bug", "scope": "all", "limit": 10 } }
```

```json
{
  "v": 1, "id": "r7", "status": 200, "node_id": "n_7f3a9c", "headers": { "content-type": "application/json" },
  "json": {
    "hits": [
      {
        "session_id": "ses_1", "session_title": "Fix login", "workspace_id": "ws_1", "workspace_root": "/Users/tai/code/app",
        "score": 0.0325, "match": "both", "snippet": "...the login bug only shows up when...",
        "message_id": "msg_9", "role": "user", "created_at": "2026-11-02T20:16:44Z"
      }
    ],
    "total": 1,
    "mode": "hybrid",
    "semantic_used": true
  }
}
```

Text-only fallback (no embedder configured on that node):

```json
{ "hits": [ { "session_id": "ses_1", "session_title": "Fix login", "workspace_id": "ws_1", "workspace_root": "/Users/tai/code/app", "score": 0.0164, "match": "exact", "snippet": "...the login bug...", "message_id": "msg_9", "role": "user", "created_at": "2026-11-02T20:16:44Z" } ], "total": 1, "mode": "text", "fallback_reason": "no_embedder", "semantic_used": false }
```

Errors: `400 bad_request` for a malformed body, `404 not_found` for an unknown workspace, and `500 internal` for an empty `query` (the existing handler's behavior).

## Event streams

### Open

Request on `crush.client.v1.<dev>.sub`:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `v` | integer | yes | `1`. |
| `id` | string | yes | Request ID, echoed. |
| `node` | string | no | Node ID or `any` (default). |
| `target` | object | no | As in API requests; `{ "swarm": "..." }` picks the session's owner node and implies `session_id`. |
| `path` | string | yes | `/v1/workspaces/{id}/events` or `/v1/events`. |
| `query` | object | no | `{ "wire": "2", "frame_ms": "100", "session_id": "<sid>" }`. `session_id` requires `stream_session_filter`. |
| `credit` | integer | no | Initial credit, 1 to `max_credit`, default 256. |
| `resume` | object | no | `{ "stream": "<old id>", "after_seq": n }` (feature `stream_resume`). |

Reply: `{ "v": 1, "id": "...", "status": 200, "stream": "s_8d1f", "node_id": "n_7f3a9c", "first_seq": 0 }` or a response with `error` (`route_not_allowed`, `device_scope_denied`, `not_found`, `rate_limited`, `resume_unavailable`).

The device MUST subscribe to `crush.client.v1.<dev>.ev.<stream>` (or the `.>` wildcard) **before** sending `sub`; frames may arrive before the reply.

### Frames

Published on `crush.client.v1.<dev>.ev.<stream>`:

```json
{ "v": 1, "stream": "s_8d1f", "seq": 41, "type": "event", "at": "2026-11-02T20:16:44.123Z", "session_id": "ses_1", "data": "<one SSE data payload>" }
```

| Field | Type | Notes |
| --- | --- | --- |
| `seq` | integer | Starts at `first_seq`, +1 per `event` frame, no gaps. `heartbeat` and `end` frames repeat the last `seq` and do not consume credit. |
| `type` | string | `event`, `heartbeat`, or `end`. Unknown types MUST be ignored. |
| `session_id` | string | Set when the payload concerns one session (message and permission deltas). |
| `data` | string | `event` only: exactly one SSE `data:` line of the underlying endpoint (wire 2 envelope for workspace streams, `proto.AttentionEvent` JSON for `/v1/events`). Rush parses it with the same code as the SSE client. |
| `reason` | string | `end` only: `closed`, `revoked`, `credit_timeout`, `idle`, `node_shutdown`, `error`. |

Session/message deltas are the wire 2 `message`, `session`, `permission`, `question`, and `agent` events already sent on `/v1/workspaces/{id}/events`; the bridge adds nothing and removes nothing besides applying `session_id` filtering.

### Flow control

- The node keeps `sent - acked <= credit`. With no credit left it stops reading the source stream (backpressure into the workspace subscriber, which coalesces to the next `frame_ms` window as SSE clients do). It never drops or reorders `event` frames.
- Ack on `crush.client.v1.<dev>.ack.<stream>`: `{ "v": 1, "stream": "s_8d1f", "seq": 41, "credit": 256 }` meaning "consumed through 41, allow 256 beyond it". Rush SHOULD ack every 16 frames and on every heartbeat. Acks are fire-and-forget and idempotent; a lower `seq` than the last ack is ignored.
- Zero credit for 60 s ends the stream (`end`, `credit_timeout`).
- Heartbeats every `heartbeat_ms` (15 s) when no other frame was sent. A device that sees no frame for `3 × heartbeat_ms` treats the stream as dead and resumes.
- No interest in the frame subject from `CLIENTS` for 30 s ends the stream (`idle`).

### Resume and replay

The node retains, per stream, the last `resume_buffer_frames` frames for `resume_window_ms` after the stream stops (device disconnect, idle).
`sub` with `resume: { stream, after_seq }` (same `path` and `query`) returns the **same** stream ID and replays from `after_seq + 1`, then continues live.
If the stream or the frames are gone, or `path`/`query` differ, the reply is `410 resume_unavailable`; rush then re-fetches state via `api` (messages, pending, overviews) and opens a fresh stream.
A stream's frames are only replayed to the device that opened it.

### Close

Request on `crush.client.v1.<dev>.unsub`: `{ "v": 1, "id": "...", "stream": "s_8d1f" }`.
Reply (if a reply subject is given): `{ "v": 1, "id": "...", "status": 200 }`; unknown streams also return `200` (idempotent).
The node sends a final `end` frame with `reason: "closed"` and discards resume state.

## Attention

Published on `crush.client.v1.<dev>.attention` (feature `device_attention`) to each connected device holding `sessions:read`, and on the legacy `crush.client.v1.broadcast.attention`:

```json
{
  "v": 1,
  "type": "attention",
  "at": "2026-11-02T20:16:44.123Z",
  "event": {
    "workspace_id": "ws_1",
    "workspace_root": "/Users/tai/code/crush",
    "session_id": "ses_1",
    "tool_call_id": "call_9",
    "kind": "blocked_permission",
    "detail": "bash",
    "node_id": "n_7f3a9c",
    "node_name": "studio"
  },
  "swarm": "aliceblue-tiger-1a2b",
  "actionable": true
}
```

- `event` is `proto.AttentionEvent`; `kind` is one of `busy`, `idle`, `blocked_permission`, `blocked_question`, `resolved`, `overviews`. `cluster_join` is never sent to devices. Tool input and message content are never included.
- Each node publishes only its own events (not relayed ones); `node_id` is always set.
- `actionable` is `true` when the device holds `permissions:answer` and `kind` is `blocked_*`.
- The per-device subject lets a node filter by scope; rush SHOULD prefer it when advertised and ignore the broadcast copy (dedupe by `node_id + session_id + tool_call_id + kind`).

## Control

Published on `crush.client.v1.<dev>.control`:

| `type` | Extra fields | Meaning |
| --- | --- | --- |
| `revoked` | `reason`, `by_node`, `at` | Device revoked cluster-wide. Every node publishes it once; repeats are idempotent. Streams end with `reason: "revoked"`, in-flight requests get no reply, and the NATS user is removed 500 ms later. |
| `scopes_changed` | `role`, `scopes`, `at` | New effective permissions; rush updates UI. Not sent for role edits now that every device has full access. Streams that lost their scope end with `reason: "error"`. |
| `reauth` | `reason`, `at` | Reconnect (for example after trust rotation). |

```json
{ "v": 1, "type": "revoked", "device_id": "d_q2w3e4r5t6y7u8i9", "reason": "operator", "by_node": "n_7f3a9c", "at": "2026-11-02T20:20:00.000Z" }
```

`reason` for `revoked`: `operator`, `key_rotation`, `expired`, `replaced`. Unknown `type` values MUST be ignored.

## Cross-node routing

The device can reach any session in the cluster through whichever node it is connected to.

1. **Explicit node**: `api.<node>` / `sub` with `node`. If `<node>` is the entry node, it runs locally.
2. **Swarm address**: `api.any` with `target: { "swarm": "aliceblue-tiger-1a2b" }`. The entry node resolves the address with the same logic as `/v1/swarm/resolve` (session owner from `sessions.owner_node`) and routes to the owner. Ambiguous two-part addresses give `400 bad_request` with `message` listing the need for the `-xxxx` suffix.
3. **`any` without target**: runs on the entry node; `/v1` routes that already proxy by session owner behave as over HTTP.

Forwarding: the entry node checks the device and scope, then sends a mesh request on `crush.client.fwd.<owner>`:

```json
{ "v": 1, "device_id": "d_q2w3e4r5t6y7u8i9", "entry_node": "n_7f3a9c", "request": { "...": "the device's envelope with target removed" } }
```

The owning node re-checks the device (not revoked) and scope against its own registry copy, executes, and returns the response envelope, which the entry node relays unchanged to the device inbox (`node_id` is the owner's).
`device_id` is trusted only on `crush.client.fwd.*`, which is mesh-only.
No answer within the timeout: `502 node_unreachable` (owner known but silent) or `504 timeout` (owner accepted, call ran long).
Streams opened for a remote node are relayed frame-by-frame by the entry node; acks and `unsub` are forwarded the same way.

## Versioning and compatibility

- `v` is the envelope major version. Nodes answer `v: 1` requests with `v: 1` envelopes. A request with an unsupported `v` gets `400 bad_request` with `message: "unsupported version"` and the reply lists `versions`.
- Unknown fields MUST be ignored by both sides. Unknown enum values (`type`, `kind`, `reason`, error `code`) MUST be ignored or handled by status class.
- Additive changes (new fields, routes, feature flags, error codes, frame types) do not bump `v`. Removing or changing the meaning of a field does, and the subject prefix moves to `crush.client.v2`.
- Optional behavior is gated by `info.features`; a client MUST NOT rely on a feature the node did not advertise.
- Phase 2a clients (no `v`, base64 `body` only, `{"seq","data"}` frames) keep working: nodes accept missing `v`, always accept `body`, and the extra frame fields are ignorable.

## Implementation notes (v1, additive)

These clarify behavior the node implements; none changes a field, subject, or code above.

- **Idempotency** keys are scoped to the authenticated device and remembered by the node that executed the request.
  The fingerprint covers method, path, query, and body (a different query is an `idempotency_conflict`).
  A retry through `api.any` without a target can land on another node after a reconnect and run again; retry with `target.node` (or `api.<node_id>`) set to the first reply's `node_id`.
  A retry whose first attempt never completed (device revoked, node stopping) gets `503 unavailable`.
- **Timeouts** outside 1000 to 30000 ms are clamped. A timed-out handler keeps running for up to 30 s more and keeps its in-flight slot; its eventual result is what an idempotent retry receives.
- **Unsupported `v`**: the error response also carries `"versions": [1]`.
- **Unknown versus refused routes**: a path no documented route matches (including `/v1/nodes/...` proxy paths) gives `404 not_found`; a documented route annotated `never`, or a stream route on `api`, gives `403 route_not_allowed`.
- **Body rules** decode the body exactly as the `/v1` handler does (case-insensitive field names, last duplicate wins), and the bridge forwards the re-encoded body for `POST .../agent` and `POST .../permissions/grant` (unknown fields are dropped there).
- **Stream opens** (`sub`) count against `max_inflight_requests` while they are routed.
- **Heartbeat and end frames** carry the last event `seq`, or `-1` before the first event.
- **Idle streams** stay open (and resumable) for `resume_window_ms` after the last sign of interest (subscription or ack), then end with `idle`; this is at least the 30 s stated above.
- **Resume buffer** is bounded by `resume_buffer_frames` and by 4 MiB of encoded frames per stream, whichever is smaller. An event too large for one NATS message ends the stream with `reason: "error"` instead of leaving a gap.
- **Session filter** also drops session events it cannot attribute (a `message_delta` whose message the stream never saw, or an event type the node does not know to be workspace-wide). It applies to `/v1/events` too.
- **Unknown filtered session**: opening a workspace stream whose `session_id` names a session that workspace does not have (on the node that runs the stream) fails with `404 not_found`, as does an unknown workspace; a lookup failure gives `500 internal`. Global streams are not checked (they carry other nodes' sessions).
- **Pinning**: `VerifyPinnedChain` also requires the leaf to carry the DNS name `crush-node`, matching what `TrustedTLSConfig` checks through the server name.
- **Member device ports**: each member advertises its own bound device listeners; pairing uses them for `members[]`. See [Member device ports](#member-device-ports).
- **Resume replay** sends every buffered frame after `after_seq` at once, outside the credit window (the buffer bounds it); credit applies again to live frames.
- **`swarm_parts`** in a device's `POST .../agent` body is dropped: structured swarm sender metadata is set only by the backend. `run_id` is passed through.
- **Remote streams**: the owning node publishes frames to the device subject itself (the device account spans the cluster); there is no extra relay hop, which is not observable on the wire.
- **`unsub` replies** come from the stream's node only (any node answers for a stream ID no member owns).
- **Legacy `broadcast.attention`** carries the same notice, plus the `proto.AttentionEvent` fields at the top level for phase 2a readers, with `actionable: false`. Every device can read it; per-device subjects are the scope-filtered path.
- **`reauth`** is sent to every active device when the cluster key version changes, with `reason: "key_rotation"`.

## Member device ports

Each member advertises where its device listeners (the `/client/v1` client listener and the device NATS websocket) are actually bound, so pairing on one node hands the phone correct addresses for every other member.

### Advertisement (member to member)

The record rides in the member's cluster record (`cluster.Member.device`, cluster RPC `members` push):

```json
{
  "node_id": "n_b", "name": "mini", "public_key": "...", "endpoints": [ { "kind": "direct", "addr": "10.0.0.2:7731" } ],
  "device": {
    "v": 1,
    "seq": 1793760000123456789,
    "client": { "state": "on", "addrs": ["10.0.0.2:7733", "mini.tailnet.ts.net:7733"] },
    "ws": { "state": "off" }
  }
}
```

- `v` is the record version (1). A reader that does not know `v` gives phones no addresses for that member rather than guessing.
- `seq` increases with every advertisement (wall-clock nanoseconds, kept strictly increasing); a member ignores a record whose `seq` is not newer than the one it holds, so reordered pushes cannot roll the state back.
- `client` and `ws` each have `state`:
  - `on`: bound now; `addrs` are the bound addresses.
  - `idle`: enabled but not bound right now (the listener is gated on an open offer or an active device); `addrs` are the addresses of the last bind, or empty if it never bound.
  - `off`: disabled on that member (`--client-addr ""` / `--client-ws-addr ""`); no `addrs`.
- `addrs` always carry the port from the bound `net.Listener` address, never a configured value, so `:0` binds advertise the real port.
  A listener bound to a specific host is advertised for that host only; a wildcard bind (`:7733`, `0.0.0.0`, `[::]`) is advertised on every host of the member's `direct` endpoints.
- A member re-advertises on startup, when it joins or creates a cluster, and whenever either listener binds, unbinds, or rebinds (including the gate opening and closing).
  A failed push to a member is retried until it succeeds or a newer advertisement supersedes it.
- Only the member itself may set or change its `device` record: a record relayed by any other member (a `members` push or a join response) is ignored, so a member learned through a relay has no record until its own advertisement arrives (pushes it could not deliver yet, for example to a member that has not learned of it, are retried).
- A listener configured with port `0` rebinds its previous port when it can, so advertised `idle` addresses stay valid across gate cycles; if that port is taken it binds a new one and re-advertises.
- Absence of `device` means the member predates advertising (legacy).

### What phones see

`members[]` in the poll response gains two additive fields:

| Field | Meaning |
| --- | --- |
| `device_ports` | `"advertised"`: `endpoints` and `nats_urls` come from the member's own record; a listener that member reports `off` contributes nothing. `"legacy"`: the member predates advertising; `endpoints` and `nats_urls` use the pairing node's ports on the member's hosts (the v1 rule). `"unknown"`: the member's record has a version this node does not understand; both lists are empty. |
| `nats_urls` | The member's device websocket URLs (`wss://host:port`), possibly empty. |

Rules for rush:

- `device_ports` absent (older pairing node): use the v1 rule, the member's endpoint hosts with the pairing node's websocket port.
- `device_ports: "legacy"`: use `nats_urls` as given (they apply the same fallback).
- `device_ports: "advertised"` or `"unknown"`: use exactly `endpoints` and `nats_urls`; an empty list means that member has no address of that kind (for example its websocket is `off`), and rush MUST NOT synthesize one.
- Unknown `device_ports` values: treat as `"advertised"`.
- The pairing node's own entry, `nats.urls`, and the `endpoints` it puts in offers and `/client/v1/info` come from its own listeners: the bound address when bound, else the last bound address, else (an offer is created just before the gate binds) the configured port when it is not `0`.
- Members whose listeners are `idle` are listed with their last bound addresses: the listener binds once the new device is active.
- A listener bound on a wildcard host (`[::]`, `0.0.0.0`) is advertised on every usable address of the host's current interfaces merged with the member's recorded cluster endpoints, deduplicated and ranked by priority (below). Loopback, link-local, and down-interface addresses are never advertised. This applies to the poll response (the pairing node's own entry and the record each member gossips), `/client/v1/info`, and the addresses `crush pair` prints. Interfaces are re-read every 30 seconds and the record is re-gossiped when it changes.
- `crush pair --host` / `--advertise` overrides win: they replace the detected client endpoints with kind `override`, priority `0`, in the given order. A listener configured off advertises nothing.

### Multipath discovery

Every entry of `endpoints[]` (in `/client/v1/info`, offers, and `members[]`) is `{addr, kind, priority}`, and `members[].nats_endpoints[]` lists the same websocket URLs as `nats_urls` as `{url, kind, priority}`. Both lists are additive: `endpoints[]` keeps its LAN-first order and `nats_urls` stays a flat string list for older rush builds.

| `kind` | Addresses | `priority` |
| --- | --- | --- |
| `override` | `crush pair --host` | 0 |
| `lan` | RFC 1918 / ULA on a physical interface (other members' private addresses: 15) | 10–15 |
| `public` | public addresses on a physical interface, hostnames | 16–18 |
| `wireguard` | `wg*`, point-to-point `utun*` | 20 |
| `tailscale` | `tailscale*`, `100.64.0.0/10`, `fd7a:115c:a1e0::/48` | 30–31 |
| `vpn` | other tunnels (`tun`, `tap`, `ipsec`, `ppp`, non-p2p `utun`, …) | 40 |
| `bridge` | `docker*`, `br-*`, `veth*`, `vmnet*`, `bridge*`, `vboxnet*`, `virbr*`, `172.17.0.0/16` | 90–95 |

Lower priority is preferred; clients may race several endpoints and keep the first that completes. A member classifies its own addresses (it knows its interfaces); entries from members that predate this classification are classified by address range only.

The TLS pin is per node: every address of a node serves the same certificate. Clients MUST verify the pinned node key / CA fingerprint and MUST NOT verify the hostname or IP, since the same node is reached under many addresses.

## Session routes

Runtime workspace IDs change whenever a workspace is re-attached, and
detached workspaces (most sessions) have none. These routes address a
session by its ID, plus an optional `root` from the overview, and are
served by the node that owns the session (route with `target.node` =
the session's `node_id`). A session that is not local answers 404
`session_not_on_node`. `root` (and the data dir) must already be in the
node's registry: an unknown root is 404 `unknown_root`. Reads of detached
workspaces open their database read-only and never attach.

| Route | Scope | Notes |
| --- | --- | --- |
| `GET /v1/session-overviews?limit=&cursor=&local=1` | `sessions:read` | Cluster-wide (this node only with `local`), flat, sorted `updated_at` desc then `id`. `limit` 1..200, default 50. `cursor` is opaque (keyset on `updated_at`,`id`, stable under concurrent updates). |
| `GET /v1/sessions/{sid}?root=` | `sessions:read` | Session metadata plus `message_count`. |
| `GET /v1/sessions/{sid}/messages?root=&limit=&before=` | `sessions:read` | Newest page first, returned in chronological order. `limit` 1..500, default 200. Pass `next_before` as `before` for the next older page. Same message shape as `GET /v1/workspaces/{id}/sessions/{sid}/messages`. |
| `POST /v1/sessions/{sid}/open` `{"root"}` | `sessions:prompt` | Attaches the workspace if needed (idempotent) and returns `{"workspace_id","session_id","root"}` for event streams and prompts. |
| `POST /v1/sessions` `{"root","prompt","model"?}` | `sessions:prompt` | Opens the root if needed, creates a session, sends the first prompt. Returns `{"session_id","workspace_id","root","node_id"}`. The bridge stamps `client_id`; `idempotency_key` is honored. |
| `POST /v1/history/search` `{"query","limit","scope"}` | `sessions:read` | Hybrid search over every workspace on this node, attached or detached. Hits as in `/v1/workspaces/{id}/history/search` plus `node_id` and `root`, at most one per session; `hits` is `[]` when empty. |
| `GET /v1/workspace-roots` | `sessions:read` | `{"roots":[{"root","data_dir","attached","workspace_id"?,"last_used_at","session_count"}]}`, most recently used first. |

`GET /v1/session-overviews` response:

```json
{
  "sessions": [
    {
      "id": "…", "title": "…", "updated_at": 1730000000, "is_busy": false, "unread": true,
      "color": "aliceblue", "animal": "tiger", "favorite": false,
      "node_id": "…", "node_name": "laptop", "root": "/src/app", "data_dir": "/src/app/.crush",
      "workspace_id": "…", "attached": true
    }
  ],
  "next_cursor": "MTczMDAwMDAwMDphYmM",
  "total": 812,
  "unavailable": [{ "node_id": "…", "node_name": "desktop", "error": "timeout" }]
}
```

`workspace_id` is present only while the workspace is attached. An
`unavailable` entry with a `root` is one workspace of that node whose
database could not be read.

## Worked examples

### List sessions

Request `crush.client.v1.d_q2w3.api.any`, reply to `_INBOX.d_q2w3.k8J2...`:

```json
{ "v": 1, "id": "r1", "method": "GET", "path": "/v1/workspaces/ws_1/sessions", "query": { "limit": "50" } }
```

```json
{ "v": 1, "id": "r1", "status": 200, "node_id": "n_7f3a9c", "headers": { "content-type": "application/json" }, "json": [ { "id": "ses_1", "title": "Fix login", "message_count": 12, "updated_at": 1793760000 } ] }
```

### Send a prompt

```json
{ "v": 1, "id": "r2", "method": "POST", "path": "/v1/workspaces/ws_1/agent", "idempotency_key": "p-5b1e2c", "json": { "session_id": "ses_1", "prompt": "Run the tests and fix failures" } }
```

```json
{ "v": 1, "id": "r2", "status": 202, "node_id": "n_7f3a9c" }
```

The prompt is accepted asynchronously (`202`, no body; corrected from an earlier draft of this example that showed `200` with a `run_id`).
To correlate the run, set `run_id` in the `json` body yourself; it is echoed on the run's `agent_event` and `run_complete` stream events.

A route tagged `never` gives (for every device):

```json
{ "v": 1, "id": "r2", "status": 403, "node_id": "n_7f3a9c", "error": { "code": "route_not_allowed", "message": "route is not available to devices" } }
```

### Subscribe to a session's message deltas

Subscribe to `crush.client.v1.d_q2w3.>` first, then request on `crush.client.v1.d_q2w3.sub`:

```json
{ "v": 1, "id": "s1", "path": "/v1/workspaces/ws_1/events", "query": { "wire": "2", "frame_ms": "100", "session_id": "ses_1" }, "credit": 256 }
```

```json
{ "v": 1, "id": "s1", "status": 200, "stream": "s_8d1f", "node_id": "n_7f3a9c", "first_seq": 0 }
```

Frames on `crush.client.v1.d_q2w3.ev.s_8d1f`:

```json
{ "v": 1, "stream": "s_8d1f", "seq": 0, "type": "event", "at": "2026-11-02T20:16:44.123Z", "session_id": "ses_1", "data": "{\"type\":\"message\",\"payload\":{...}}" }
{ "v": 1, "stream": "s_8d1f", "seq": 0, "type": "heartbeat", "at": "2026-11-02T20:16:59.123Z" }
```

Ack on `crush.client.v1.d_q2w3.ack.s_8d1f`:

```json
{ "v": 1, "stream": "s_8d1f", "seq": 15, "credit": 256 }
```

After a reconnect:

```json
{ "v": 1, "id": "s2", "path": "/v1/workspaces/ws_1/events", "query": { "wire": "2", "frame_ms": "100", "session_id": "ses_1" }, "credit": 256, "resume": { "stream": "s_8d1f", "after_seq": 87 } }
```

### Answer a permission

```json
{ "v": 1, "id": "r3", "method": "POST", "path": "/v1/workspaces/ws_1/permissions/grant", "idempotency_key": "g-call_9", "json": { "action": "allow", "permission": { "id": "perm_4", "session_id": "ses_1", "tool_call_id": "call_9", "tool_name": "bash" } } }
```

```json
{ "v": 1, "id": "r3", "status": 200, "node_id": "n_7f3a9c", "headers": { "content-type": "application/json" }, "json": { "resolved": true } }
```

### Attention event

See [Attention](#attention); delivered on `crush.client.v1.d_q2w3.attention`.

### Revoked notice

See [Control](#control); delivered on `crush.client.v1.d_q2w3.control`, followed about 500 ms later by the server closing the connection.

### Cross-node request

The phone is connected to `n_7f3a9c` (studio); session `aliceblue-tiger-1a2b` lives on `n_b2c4e6` (laptop):

```json
{ "v": 1, "id": "r4", "method": "GET", "path": "/v1/workspaces/ws_9/sessions/ses_77/messages", "target": { "swarm": "aliceblue-tiger-1a2b" } }
```

Published on `crush.client.v1.d_q2w3.api.any`. Studio checks the scope, forwards on `crush.client.fwd.n_b2c4e6`, laptop re-checks and answers:

```json
{ "v": 1, "id": "r4", "status": 200, "node_id": "n_b2c4e6", "headers": { "content-type": "application/json" }, "json": [ { "id": "msg_1", "role": "user", "parts": [ { "type": "text", "data": { "text": "hi" } } ] } ] }
```

Laptop offline:

```json
{ "v": 1, "id": "r4", "status": 502, "node_id": "n_7f3a9c", "error": { "code": "node_unreachable", "message": "node n_b2c4e6 did not answer", "retry_after_ms": 5000 } }
```
