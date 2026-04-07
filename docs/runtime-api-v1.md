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

Core routes:

- `POST /message`
- `GET /message/stream`
- `GET /conversations`
- `GET /conversations/search`
- `GET /conversations/{id}`

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
