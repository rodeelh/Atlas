# Atlas Runtime API v1 Draft

This is the first versioned runtime API contract draft for Project Atlas.

Status: draft compatibility contract.

It describes the current Atlas runtime contract that the web UI and companion clients rely on.

Primary sources:

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/domain`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/domain)
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/client.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/client.ts)

## Contract goals

- Preserve current web UI behavior as the runtime evolves.
- Give Atlas clients a stable compatibility target.
- Separate stable API behavior from incidental implementation details.

## Versioning rule

- This document defines `v1`.
- The current runtime does not yet expose an explicit `v1` path prefix.
- Compatibility should be judged against `v1` behavior even if the live routes remain unprefixed.
- If a breaking change is needed later, it should become an explicit `v2` contract rather than an untracked route drift.

## Transport model

- Transport: HTTP/1.1 JSON API plus SSE for message streaming.
- Encoding: JSON using the runtime encoder in `AtlasJSON`.
- Authentication:
  - local native bootstrap via launch token and session cookie
  - remote browser access via remote access key and session cookie
  - remote state-changing calls require `X-CSRF-Token` from `GET /auth/csrf`
- Static web shell is served from `/web`.

## Contract rules

### Rule 1: the runtime is the source of truth

The web UI and native shell are clients. Runtime config, onboarding state, sessions, approvals, conversations, and communications state must be runtime-owned.

### Rule 2: secrets never leave the secret layer unnecessarily

`/api-keys` exposes presence and key names, not raw provider secrets.

### Rule 3: onboarding is a runtime concern

Onboarding completion is part of the shared runtime config state and must remain readable and writable by the web UI.

### Rule 4: route groups migrate together

Go migration units should follow contract groups such as auth, config, approvals, communications, chat, and workflows rather than arbitrary package boundaries.

## Stable route groups in v1

### Auth and session bootstrap

Required behavior:

- trusted local clients can mint a launch token
- a browser can exchange a launch token for a session cookie
- remote access uses a separate login gate and remote key flow
- LAN remote access requires HTTPS (or trusted loopback TLS-terminating proxy)
- remote session revocation is explicit

Core routes:

- `GET /auth/token`
- `GET /auth/bootstrap`
- `GET /auth/remote-gate`
- `GET /auth/https-required`
- `POST /auth/remote`
- `GET /auth/remote-status`
- `GET /auth/csrf`
- `GET /auth/remote-key`
- `DELETE /auth/remote-sessions`

Route-specific constraints:

- `GET /auth/token` is local-only (loopback peer).
- `GET /auth/bootstrap` consumes `?token=` and sets session cookie.
- `GET /auth/csrf` requires authenticated session and returns `{ "token": string }`.
- For remote LAN requests (non-Tailscale), runtime enforces HTTPS before auth/session processing.

### Runtime status and config

Required behavior:

- the web app can poll runtime state and logs
- config reads and writes use a shared snapshot model
- config writes return both updated config and restart impact

Core routes:

- `GET /status`
- `GET /logs`
- `GET /config`
- `PUT /config`
- `GET /onboarding`
- `PUT /onboarding`

Required payload expectations:

- `/status` must include runtime state, port, pending approvals, and communications snapshot
- `/config` must round-trip the fields in `RuntimeConfigSnapshot`
- `/onboarding` must expose `{ "completed": boolean }`

### Chat and conversation history

Required behavior:

- send-message requests work without the web app having to understand internal agent orchestration
- streaming remains SSE-based for incremental assistant progress
- conversation history remains queryable
- when a tool produces a local file, the runtime emits a `file_generated` SSE event and registers a short-lived download token redeemable at `GET /artifacts/{token}`

Core routes:

- `POST /message`
- `GET /message/stream`
- `GET /conversations`
- `GET /conversations/search`
- `GET /conversations/{id}`
- `GET /artifacts/{token}`

#### SSE event types (GET /message/stream)

All events are JSON-encoded `data:` lines. The `type` field determines the shape:

| type | key fields | meaning |
|---|---|---|
| `assistant_started` | `role`, `conversationID` | model turn beginning |
| `assistant_delta` | `content`, `role` | incremental token |
| `assistant_done` | `role` | model turn complete |
| `tool_started` | `toolName`, `toolCallID` | tool execution beginning |
| `tool_finished` | `toolName`, `toolCallID` | tool execution complete |
| `tool_failed` | `toolName`, `toolCallID`, `error` | tool execution failed |
| `file_generated` | `filename`, `mimeType`, `fileSize`, `fileToken`, `toolName` | tool produced a local file; redeem token at `GET /artifacts/{token}` |
| `approval_required` | `approvalID`, `toolCallID`, `toolName`, `arguments` | tool call deferred for user approval |
| `done` | `status` (`completed` \| `waitingForApproval` \| `denied` \| `failed` \| `cancelled`) | turn lifecycle complete |
| `error` | `error` | unrecoverable turn error |
| `cancelled` | — | turn cancelled via `POST /message/cancel` |

#### GET /artifacts/{token}

Resolves a `fileToken` from a `file_generated` event to the underlying file.

- Token is a 32-hex-character random string (128-bit, unguessable).
- Images are served with `Content-Disposition: inline` so the browser can preview them.
- All other file types are served with `Content-Disposition: attachment`.
- Returns 404 if the token is not found or has been evicted (store holds up to 500 entries).
- Requires session auth (same as all other routes).

### Approvals and policies

Required behavior:

- pending approvals are listable
- approvals are resolved by `toolCallID`
- policy changes return the full updated policy map

Core routes:

- `GET /approvals`
- `POST /approvals/{toolCallID}/approve`
- `POST /approvals/{toolCallID}/deny`
- `GET /action-policies`
- `PUT /action-policies/{actionID}`

### Credentials and communications

Required behavior:

- provider credential presence can be queried without leaking values
- communication platforms can be validated before enablement
- communication state is returned as a normalized snapshot

Core routes:

- `GET /api-keys`
- `POST /api-keys`
- `DELETE /api-keys`
- `POST /api-keys/invalidate-cache`
- `GET /communications`
- `GET /communications/channels`
- `GET /communications/platforms/{platform}/setup`
- `PUT /communications/platforms/{platform}`
- `POST /communications/platforms/{platform}/validate`

Agent-facing communications surface:

- `communication.list_channels` lists authorized chat bridge channels from the normalized communication session store.
- `communication.send_message` sends only to an existing authorized channel returned by `communication.list_channels`; it cannot invent new targets or reach arbitrary contacts.
- Workflow trust scope can allow this through the `communication` / `chat bridge` app family, which maps to the `communication.*` tool prefix.

### Operator domains

These are already part of the current runtime surface and should remain grouped for migration:

- memories
- skills
- mind and skills memory
- automations
- workflows
- forge

### Automations API

Automations own trigger timing, enabled state, delivery, and optional workflow binding.

Core routes:

- `GET /automations`
- `GET /automations/summaries`
- `POST /automations`
- `PUT /automations/{id}`
- `DELETE /automations/{id}`
- `POST /automations/{id}/enable`
- `POST /automations/{id}/disable`
- `POST /automations/{id}/run`
- `GET /automations/{id}/runs`
- `GET /automations/advanced/file`
- `PUT /automations/advanced/import`

Required payload expectations:

- `/automations/summaries` returns health, delivery health, next run, last run, and destination label fields used by the web UI.
- Agent-created automations can set chat delivery with `destinationID` from `communication.list_channels`, or the explicit `platform` + `channelID` + optional `threadID` tuple.
- Advanced import/export routes are compatibility/repair tooling, not the normal UI path.

### Workflows API

Workflows own reusable process definitions, trust scope, structured runs, and direct execution.

Core routes:

- `GET /workflows`
- `GET /workflows/summaries`
- `POST /workflows`
- `GET /workflows/{id}`
- `PUT /workflows/{id}`
- `DELETE /workflows/{id}`
- `POST /workflows/{id}/run`
- `GET /workflows/{id}/runs`
- `GET /workflows/runs`
- `POST /workflows/runs/{runID}/approve`
- `POST /workflows/runs/{runID}/deny`

Required payload expectations:

- `/workflows/summaries` returns health, last run, error, enabled state, and step count fields used by the web UI.
- Workflow runs include structured `stepRuns`; prompt steps may complete, fail, or be skipped if unsupported.
- Runtime tools invoked inside workflow runs are constrained by the workflow trust scope and action safety policy.

### Dashboards API

Dashboards are user-visible compositions of widgets that pull live data from runtime endpoints, read-only skills, the open web, read-only SQL, chat analytics, gremlin run history, or AI-driven live-compute transformations. The agent authors them from chat via the `dashboard.*` skill family; the web UI ships a viewer. Creation and mutation are agent-driven — there are no direct HTTP create/update routes.

Core routes:

- `GET /dashboards` — list dashboard summaries (id, name, widget count, timestamps); optional `?status=` filter
- `GET /dashboards/{id}` — full definition with widgets and data sources
- `DELETE /dashboards/{id}` — remove
- `POST /dashboards/{id}/resolve` — body `{ "widgetId": "..." }` returns the resolved data for one widget (one-shot)
- `POST /dashboards/{id}/refresh` — force all sources to re-resolve; returns the array of `RefreshEvent` results
- `GET /dashboards/{id}/events` — SSE stream of `RefreshEvent` objects; one event per source per refresh cycle

Built-in widget kinds: `metric`, `table`, `line_chart`, `bar_chart`, `markdown`, `list`, `custom_html`. Custom HTML widgets render inside a sandboxed iframe (`sandbox="allow-scripts"`, opaque origin) with a strict CSP (`default-src 'none'`) that blocks all outbound network. The parent posts resolved data into the iframe via `postMessage`; the widget defines `window.atlasRender(data)` to consume it.

Data source kinds and safety rules:

- `runtime` — GET against an allowlisted runtime endpoint. Allowlist (in `internal/modules/dashboards/safety.go`): `/status`, `/logs`, `/memories`, `/diary`, `/mind`, `/skills`, `/skills-memory`, `/workflows`, `/workflows/`, `/automations`, `/automations/`, `/communications`, `/communications/`, `/forge/proposals`, `/forge/installed`, `/forge/researching`, `/usage/summary`, `/usage/events`, `/mind/thoughts`, `/mind/telemetry`, `/mind/telemetry/summary`, `/chat/pending-greetings`. Anything else returns 403.
- `skill` — calls a skill action via the runtime registry. The action must be registered and must have `ActionClass == ActionClassRead`; non-read or unknown actions are rejected at `dashboard.add_data_source` time. The resolver prefers the structured `Artifacts` map from `ToolResult` over parsing the human-readable `Summary`.
- `web` — proxied GET via the runtime. Scheme must be `http`/`https`; localhost, `.local`, all RFC1918, IPv6 loopback, and `0.0.0.0` are rejected. Response capped at 256 KB; redirects re-validated on every hop (max 3).
- `sql` — read-only `SELECT` (or `WITH … SELECT`) against `atlas.sqlite3`. Lexer rejects 16 forbidden keywords (DELETE, UPDATE, DROP, PRAGMA, ATTACH, …) and multi-statement input; the connection itself is opened with `?mode=ro&_pragma=query_only(1)` as defence in depth. 2 s timeout, default `LIMIT 500`.
- `chat_analytics` — allowlisted analytics queries against the conversations/messages SQLite tables. Requires the shared `*sql.DB` handle (wired via `SetDatabase`).
- `gremlin` — queries gremlin run history from SQLite. Requires the shared `*sql.DB` handle (wired via `SetDatabase`).
- `live_compute` — AI-driven transformation. Resolves all other named input sources first, then calls `AILiveComputeRunner` (backed by `agent.CallAINonStreamingExported`) with the prompt, input data, and optional output schema. Returns parsed JSON; falls back to `{"text": "..."}` if the model response is not valid JSON.

Required payload expectations:

- `POST /dashboards/{id}/resolve` returns `{ widgetId, success, data, error?, source, sourceKind, resolvedAt, durationMs }`. Safety/allowlist rejections return HTTP 403; upstream/runtime failures return 200 with `success=false` so the dashboard can render an error tile without losing the rest of the grid.
- `GET /dashboards/{id}/events` emits `data: <json>\n\n` lines. Each JSON object is a `RefreshEvent` with fields `{ source, success, data?, error?, resolvedAt, durationMs }`.
- Agent-authored dashboards (via `dashboard.create` / `dashboard.add_data_source` skills) validate source kinds and skill action classes at authoring time — invalid configurations are rejected before persisting.

### Teams API

AGENTS.md defines the roster of Atlas team members (agents). The teams module manages their definitions, runtime state, tasks, and events.

Core routes:

- `GET /team` — full team snapshot (Atlas station + all agents + recent activity + blocked items)
- `GET /team/agents` — list all team members with current runtime state
- `GET /team/agents/{id}` — get one team member by ID
- `POST /team/agents` — create a new team member (body: `agentDefinition`)
- `PUT /team/agents/{id}` — update an existing team member
- `DELETE /team/agents/{id}` — delete a team member
- `POST /team/agents/{id}/enable` — enable a disabled team member
- `POST /team/agents/{id}/disable` — disable a team member
- `POST /team/agents/{id}/pause` — pause a team member's runtime
- `POST /team/agents/{id}/resume` — resume a paused team member
- `POST /team/sync` — re-sync team definitions from AGENTS.md into SQLite
- `GET /team/tasks` — list recent tasks (last 100)
- `GET /team/tasks/{id}` — get one task with its step log
- `POST /team/tasks/{id}/cancel` — cancel a running task (409 if not running)
- `POST /team/tasks/{id}/approve` — approve a `pending_approval` task → sets status `completed` (409 if not pending)
- `POST /team/tasks/{id}/reject` — reject a `pending_approval` task → sets status `cancelled` (409 if not pending)
- `GET /team/events` — list team activity events (last 100)

Agent definition fields: `id`, `name`, `role`, `mission`, `style`, `allowedSkills`, `allowedToolClasses`, `autonomy`, `activation`, `enabled`.

Runtime state fields: `status` (`idle` | `busy` | `paused` | `approval_needed`), `currentTaskID`, `lastActiveAt`, `lastError`, `updatedAt`.

Task status lifecycle: `running` → `completed` | `error` | `cancelled` | `pending_approval`.

Skills registered by the teams module:

- `team.list` — list all team members
- `team.get` — get one team member by ID
- `team.create` — create a new team member (validates `allowedSkills` patterns against registered skills at create time)
- `team.update` — update an existing team member
- `team.delete` — delete a team member
- `team.enable` / `team.disable` / `team.pause` / `team.resume` — lifecycle controls
- `team.delegate` — delegate a focused task to a team member; enforces `allowedSkills` pattern filtering and `allowedToolClasses` class filtering on the sub-agent registry

Agent/automation distinction: `team.create` creates persistent team members in AGENTS.md; `automation.create` creates recurring scheduled jobs in GREMLINS.md. These are different things and must never be substituted for one another.

### Memory API

Long-term memories are stored in SQLite and recalled via BM25 FTS5 before each agent turn.

Core routes:

- `GET /memories` — list active memories, optionally filtered by `?category=` and `?limit=`
- `POST /memories` — create a memory (fields: `category`, `title`, `content`, `source`, `confidence`, `importance`, `tags`, `isSensitive`)
- `PUT /memories/{id}` — update a memory
- `DELETE /memories/{id}` — hard-delete a memory
- `POST /memories/{id}/confirm` — mark `is_user_confirmed = true`

Required payload expectations:

- `GET /memories` returns an array of `MemoryItem` with `id`, `category`, `title`, `content`, `source`, `confidence`, `importance`, `isUserConfirmed`, `isSensitive`, `tags`, `createdAt`, `updatedAt`
- Invalidated memories (`valid_until` in the past) are excluded from list results
- Categories: `commitment`, `profile`, `preference`, `project`, `workflow`, `episodic`, `tool_learning`

### Mind and Skills Memory API

MIND.md and SKILLS.md are Markdown files on disk that feed the agent system prompt. They are human-readable and AI-writable.

Core routes:

- `GET /mind` — returns `{ "content": "<MIND.md contents>" }`
- `PUT /mind` — overwrite MIND.md with `{ "content": "…" }`
- `POST /mind/regenerate` — trigger a full MIND.md refresh using current memories + diary (async, returns 202)
- `GET /skills-memory` — returns `{ "content": "<SKILLS.md contents>" }`
- `PUT /skills-memory` — overwrite SKILLS.md with `{ "content": "…" }`

Required payload expectations:

- Both GET routes return `{ "content": string }`
- PUT routes accept `{ "content": string }` and write atomically (temp-file then rename)
- `POST /mind/regenerate` triggers `mind.RegenerateMindSync` and returns 200 with updated content, or 500 on failure

### Diary API

DIARY.md stores per-day one-line entries (max 3 per day). Written by the `diary.record` skill and by the MIND reflection pipeline after each turn.

Core routes:

- `GET /diary` — returns `{ "content": "<DIARY.md contents>" }`
- `PUT /diary` — overwrite DIARY.md with `{ "content": "…" }`

Required payload expectations:

- Both routes use `{ "content": string }`
- `AppendDiaryEntry` enforces the max-3-per-day limit; direct PUT bypasses this limit

### Voice API

Mounted at `/voice/*` by `internal/modules/voice`. Manages STT (Whisper), TTS (Kokoro), and cloud audio provider routing.

**Provider model** — `config.activeAudioProvider` selects the backend: `"local"` (Whisper + Kokoro on-device), `"openai"`, `"gemini"`, or `"elevenlabs"`. Resolved at request time by `resolveAudioProvider()` in `internal/voice/keychain.go`. Falls back to `"local"` if the selected cloud provider has no API key configured.

**Routes:**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/voice/status` | Session state, running processes, version info |
| `GET` | `/voice/voices` | Curated voice list for active (or `?provider=`) provider |
| `POST` | `/voice/session/start` | Start Whisper subprocess; blocks until ready (15 s timeout) |
| `POST` | `/voice/session/end` | Kill Whisper + Kokoro subprocesses |
| `POST` | `/voice/transcribe` | Multipart `audio` field → transcript. Auto-starts session if needed. |
| `POST` | `/voice/synthesize` | SSE stream of PCM audio chunks from active TTS provider |
| `POST` | `/voice/kokoro/warmup` | Pre-warm Kokoro subprocess (idempotent) |
| `GET` | `/voice/models/{whisper\|kokoro}` | List downloaded models |
| `POST` | `/voice/models/{component}/download` | Download a model; SSE progress stream |
| `DELETE` | `/voice/models/{component}/{name}` | Delete a model file |
| `GET` | `/voice/models/download/status` | Current download progress snapshot |
| `DELETE` | `/voice/models/download` | Clear download progress state |
| `POST` | `/voice/whisper/update` | Rebuild whisper-server binary; SSE progress stream |
| `POST` | `/voice/kokoro/update` | `pip install --upgrade kokoro-onnx`; SSE progress stream |

**Key contracts:**

- `POST /voice/transcribe` — multipart form, field name `"audio"`, optional `?language=` query param. Returns `{ text, language, duration, sessionID }`.
- `POST /voice/synthesize` — JSON body `{ text, voice? }`. SSE events: `start`, `voice_audio` `{ chunk: base64, index, sampleRate }`, `voice_audio_end`, `error`.
- Update routes emit SSE: `progress { line }`, `done { version, ... }`, `error { error }`.

**Local provider constraints:**

- Whisper subprocess launched with `DYLD_LIBRARY_PATH=$INSTALL_DIR/voice` so its bundled dylibs are always found, even after rebuilds.
- `POST /voice/transcribe` with local provider requires **WAV format** (16 kHz mono PCM). The web client converts via `AudioContext` before upload. The `voice.transcribe` skill requires pre-converted WAV; non-WAV returns a descriptive error with an ffmpeg hint.
- Kokoro is a Python subprocess running `kokoro_server.py` from the venv at `$INSTALL_DIR/voice/venv`. Python 3.14+ requires venv recreation with Python ≤3.13 (kokoro-onnx constraint); `UpgradeKokoro` handles this automatically via `findBestVoicePython()`.
- Idle session timeout (default 300 s, configurable via `VoiceSessionIdleSec`) kills both subprocesses. Activity is recorded at the **start** of each transcription so long requests are not evicted mid-flight.

**Skills:** `voice.transcribe` (read, WAV required for local) and `voice.synthesize` (draft, writes WAV file). Both use the Manager's adapter dispatch, so they automatically follow `activeAudioProvider`.

## Compatibility baseline in code

The current codebase now has a compatibility baseline in the Go runtime and web contract layer:

- route ownership and handler shapes in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/domain`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/domain)
- auth/session behavior in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/auth`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/auth)
- current web-facing request and payload contracts in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/contracts.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/contracts.ts)
- current web client transport usage in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/client.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/client.ts)
  - onboarding state round-trips
  - config update restart semantics
  - remote access status and auth bootstrap
  - remote API-key authentication
  - API-key presence/status responses
  - communication setup value reads
  - communication validation and enable/disable mutation behavior
  - conversation summaries, search, and detail reads
  - approval listing and deny lifecycle for deferred execution
  - action-policy mutation responses returning the updated policy map
  - approval-linked chat/SSE lifecycle for denied and resumed conversations
  - message failure behavior, including conversation ID preservation and reusable conversation creation
  - `MIND.md`, `SKILLS.md`, and memory create/list routes preserving runtime-owned document semantics
  - workflow definition create/update/delete, workflow run execution, and workflow run history reads
  - forge researching state, proposal create/list/reject flows, install-enable behavior, installed skill listing, and uninstall behavior

This is now a broad compatibility baseline we can preserve while the runtime changes underneath.

The current contract artifacts now also include:

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-api-inventory.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-api-inventory.md) for human-readable grouping
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-api-manifest.json`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-api-manifest.json) for a machine-readable route baseline
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/contracts.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/contracts.ts) for the current web-facing contract layer

## What remains before v1 is strong

- reduce route-local response structs inside `AgentRuntime`
- derive web client types from shared contracts or generated schema where practical
- make versioning explicit at the API boundary if the unprefixed live routes need a future breaking change
