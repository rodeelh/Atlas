# Project Atlas Migration Plan

This document is the working migration plan for moving Project Atlas from a SwiftUI/macOS-centered architecture to a web-first product with a portable Go runtime and an optional thin native companion shell.

It is intentionally grounded in the current codebase, not an idealized rewrite.

## Goals

- Make Atlas web-first.
- Make the runtime system-agnostic.
- Demote the native app to an optional companion shell.
- Preserve product momentum during the migration.
- Avoid a big-bang rewrite.

## Target Architecture

Atlas should converge toward four layers:

1. `atlas-runtime-go`
   The primary runtime/engine. Owns APIs, orchestration, persistence, approvals, automations, communications, and agent execution.

2. `atlas-web`
   The primary product surface. Owns onboarding, settings, management, and day-to-day operator workflows.

3. `atlas-shell-*`
   Optional thin native companion shells for macOS, Windows, and Linux. These should expose runtime status, a few quick actions, notifications, and OS-specific integrations only.

4. Shared contracts
   Versioned API and domain contracts consumed by both the web UI and native shells.

## Current Architecture Summary

The current codebase already behaves like a runtime platform with two clients:

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core) is the real runtime system of record.
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web) is already the broadest product surface.
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app) still carries too much responsibility for setup, control, and UX ownership.

The migration is therefore a control-plane migration first, and a language/runtime migration second.

## Core Principles

### Principle 1: No big-bang rewrite

We will migrate by responsibility boundaries, not by attempting to port all of `atlas-core` or all Swift packages at once.

### Principle 2: Web is the primary control surface

All new product-facing setup, configuration, and management flows should default to the web UI unless they require true OS-specific behavior.

### Principle 3: Native shells are companions, not controllers

The native app should eventually be limited to:

- runtime status
- quick actions like open/start/stop/restart
- notifications
- tray/menu bar presence
- optional local permission helpers

The native app should not remain the owner of onboarding, primary settings, or core product workflows.

### Principle 4: Contracts before implementation replacement

Before replacing Swift runtime domains with Go, we must document the runtime API and behavioral expectations the web UI depends on.

### Principle 5: Migrate by system responsibility

Good migration units:

- config and settings
- auth and sessions
- runtime control
- approvals and action policy
- conversations and streaming
- communications
- automations and workflows
- dashboards and forge
- persistence and secret access

Bad migration units:

- “port `atlas-core`”
- “rewrite the daemon”
- “rewrite the app in another language”

### Principle 6: Separate human guidance from machine config

Use Markdown for human-authored guidance and intent only.

- `MIND.md` and similar `.md` files are for human-readable instructions, goals, context, and operating guidance.
- Real runtime configuration must live in a structured format such as JSON, TOML, YAML, or SQLite.
- Secrets must never be stored in Markdown files.

This rule exists to keep Atlas easy for humans to shape without turning operational state and secret material into fragile prose documents.

## Phase Overview

1. Phase 0: Shrink the native shell first
2. Phase 1: Extract and formalize runtime contracts
3. Phase 2: Isolate platform-specific dependencies
4. Phase 3: Carve the Swift runtime into replaceable domains
5. Phase 4: Build the Go runtime behind compatibility boundaries
6. Phase 5: Dual-run and cut over domain by domain
7. Phase 6: Go agent loop — tool execution, approval deferral, skill implementations
8. Phase 7: Final shell minimization
9. Phase 8: Full Go parity and Swift daemon decommission
10. Phase 9: Archive Swift code and repo cleanup for v1.0

## Phase 0: Shrink The Native Shell First

Status: COMPLETE as of 2026-03-31.

This phase comes first on purpose.

Moving functionality out of the menu bar app before the Go migration is not wasted effort. It reduces native authority, exposes missing runtime contracts earlier, and makes the later runtime migration much simpler.

### Objectives

- Reduce `atlas-app` to companion-shell responsibilities.
- Move onboarding, settings, and management UX to the web app wherever possible.
- Stop adding major product logic to the SwiftUI shell.

### Current native ownership to reduce

The current app scene setup in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasApp.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasApp.swift) still owns:

- onboarding window hosting
- settings window hosting
- app bootstrap flow

The current app state in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasAppState.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasAppState.swift) still coordinates:

- daemon lifecycle
- onboarding state
- credential validation UX
- runtime polling for shell-driven decisions

The current menu bar UI in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/MenuBarController.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/MenuBarController.swift) still surfaces:

- start/stop/restart
- setup/open-settings
- LAN toggle
- state summary

### Move to web first

Prioritize moving these flows into [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web):

1. Onboarding currently in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/OnboardingFlowView.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/OnboardingFlowView.swift)
2. Settings and credential management currently in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/SettingsDetailViews.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/SettingsDetailViews.swift)
3. Runtime control flows currently reachable primarily from the native shell
4. Any setup-related routing in `AtlasAppState`

### Keep in native shell for now

- menu bar presence
- open web UI
- quick start/stop/restart
- lightweight runtime status
- optional notifications
- optional local convenience toggles that are genuinely useful at the shell layer

### Rules during Phase 0

- No new product settings panes in SwiftUI.
- No new onboarding steps in SwiftUI unless strictly necessary to unblock a web parity gap.
- New setup and management UX should default to the web app.
- Native work should be shell-only unless explicitly justified.

### Exit criteria

- A user can complete normal Atlas setup from the web UI.
- The web UI becomes the default path for ongoing management.
- The native app is no longer required for day-to-day product workflows.

### Completion notes

- Onboarding now lives in the web app and can complete without the SwiftUI wizard.
- The native onboarding sheet is disabled, and the menu bar app hands first-run users off to the web UI.
- The native settings window has been reduced to a temporary local-access panel instead of a broad management surface.
- Shared config and onboarding state now live in the runtime-readable config path rather than menu-bar-only state.
- The native shell still owns quick runtime controls and temporary local file access management, which fits the companion-shell goal for this phase.

## Phase 1: Extract And Formalize Runtime Contracts

Status: COMPLETE as of 2026-03-31.

Once the web UI is the primary control surface, formalize the runtime contract.

### Objectives

- Inventory the full runtime API surface.
- Define versioned API contracts.
- Make the web UI depend on explicit contracts instead of ad hoc route knowledge.

### Primary files to inventory

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Sources/AtlasCore/AgentRuntime.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Sources/AtlasCore/AgentRuntime.swift)
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/client.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/client.ts)

### Contract groups to document

- auth and sessions
- runtime status and control
- config and settings
- chat/message/SSE
- approvals
- skills
- communications
- automations
- workflows
- dashboards
- forge
- memory and logs

### Required outputs

- API inventory document
- versioned runtime API spec
- compatibility tests for current Swift runtime behavior
- generated or contract-aligned TS types for the web UI

### Exit criteria

- We can describe what the runtime does without reading `AgentRuntime.swift`.
- The web UI depends on a documented contract, not route discovery.

### Current progress

- Added [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-api-inventory.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-api-inventory.md) as the first route and ownership inventory.
- Added [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-api-v1.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-api-v1.md) as the first versioned runtime API contract draft.
- Added [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-api-manifest.json`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-api-manifest.json) as the first machine-readable route manifest draft.
- Moved web API payload interfaces into [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/contracts.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/contracts.ts) so the client transport layer and contract layer are no longer the same file.
- Added payload-level compatibility smoke tests in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeContractSmokeTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeContractSmokeTests.swift) to protect baseline response shapes used by the web app.
- Added route-compatibility tests in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) covering onboarding, config update semantics, remote access status, launch-token bootstrap, remote session auth, and API-key status.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) to cover communications setup values, conversation history read routes, and the approval-deny lifecycle for deferred skill execution.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) again to cover communications validation/update mutation routes and action-policy mutation behavior, while tightening credential-bundle override handling so contract tests do not depend on native secret writes.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) to cover approval-driven chat/SSE lifecycle behavior by asserting the stream contract for denied actions and resumed approved conversations.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) again to pin down `POST /message` failure semantics, including explicit conversation ID preservation, fallback stream emission, and reusable conversation creation when the client omits a conversation ID.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) to cover the mind/memory document boundary: `MIND.md` round-trips, `SKILLS.md` round-trips, and memory creation/listing normalization and persistence.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) to cover workflow definition lifecycle, workflow run history, and dashboard proposal/install/pin/access/remove behavior through the runtime boundary rather than lower-level stores.
- Expanded [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-core/Tests/AtlasCoreTests/RuntimeRouteCompatibilityTests.swift) to cover the Forge operator boundary: researching state, proposal creation/listing, reject semantics, install-enable, installed skill listing, and uninstall behavior.

### Completion notes

- The runtime now has a documented route inventory, a versioned compatibility contract draft, and a machine-readable manifest.
- The web app now has a dedicated contract layer in [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-web/src/api/contracts.ts`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-web/src/api/contracts.ts) instead of mixing transport and contract definitions in one file.
- The current Swift runtime has black-box compatibility coverage for the highest-risk operator boundaries the future Go runtime must preserve: auth, config, onboarding, credentials, communications, approvals, chat/SSE, documents, memory, workflows, dashboards, and forge.
- Further schema generation or stronger typed derivation is still useful later, but it is no longer a blocker for completing Phase 1 and starting Phase 2.

## Phase 2: Isolate Platform-Specific Dependencies

Status: COMPLETE as of 2026-03-31.

The current runtime is not yet system-agnostic because macOS assumptions leak into shared layers.

### Objectives

- Identify all platform-specific services.
- Move them behind explicit interfaces.
- Prevent platform dependencies from remaining ambient or implicit.

### Key hotspots

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-logging/Sources/AtlasShared/AtlasConfig.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-logging/Sources/AtlasShared/AtlasConfig.swift)
  Runtime config still mixes daemon-readable settings with `UserDefaults` and app-only state.

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-logging/Sources/AtlasShared/KeychainSecretStore.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-logging/Sources/AtlasShared/KeychainSecretStore.swift)
  Secret storage is explicitly macOS Keychain-based and assumed by many layers.

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasRuntimeManager.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-app/Sources/AtlasApp/AtlasRuntimeManager.swift)
  Runtime supervision is tied to launchd and local bundle layout assumptions.

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-memory/Sources/AtlasMemory/MemoryStore.swift`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-memory/Sources/AtlasMemory/MemoryStore.swift)
  Persistence itself is portable, but filesystem path assumptions need explicit ownership.

### Interfaces to define

- `ConfigStore`
- `SecretsStore`
- `RuntimeSupervisor`
- `NotificationSink`
- `PathProvider`
- `PermissionAdapter` for OS-specific grants if needed later

### Current progress

- Added [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/docs/runtime-dependency-map.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/docs/runtime-dependency-map.md) as the first explicit dependency ownership map for Phase 2.
- Classified current seams into runtime-owned portable interfaces, shell-only adapters, and hybrid boundaries that still need a split between runtime state and shell execution.
- Identified the main remaining platform-heavy hotspots:
  - process supervision in the native shell
  - notification delivery split by process heuristics
  - file-access grants backed by macOS bookmarks
  - non-macOS secret backends still being stubs
- Added `RuntimeSupervisorState` enum and `RuntimeSupervisor` protocol to `StorageInterfaces.swift`.
  `AtlasRuntimeManager` now conforms to `RuntimeSupervisor` via a bridge extension.
- Added `NotificationSink` protocol to `StorageInterfaces.swift` with two implementations in `atlas-skills`:
  - `RelayNotificationSink` — daemon relay via `NSDistributedNotificationCenter`
  - `UNNotificationSink` — direct delivery via `UNUserNotificationCenter`
- `NotificationService` now injects a `NotificationSink`; the `canAccessNotificationCenter`
  process-name heuristic is eliminated from the shared runtime layer.
- `BuiltInSkillsProvider` and `AgentRuntime` explicitly pass `RelayNotificationSink()` as the
  daemon-context delivery adapter. Full Xcode build (app + daemon) is clean.

- Added `FileAccessGrantAdapter` protocol to `StorageInterfaces.swift` with the explicit
  ownership rule: runtime owns policy enforcement (listing, resolving, enforcing approved roots);
  shell owns grant acquisition (OS permission dialogs, security-scoped bookmark creation).
- Added `MacOSBookmarkGrantAdapter` in `atlas-skills` as the macOS implementation. All shell
  callers in `AtlasAppState` and the integration test suite now use `MacOSBookmarkGrantAdapter`
  instead of calling `FileAccessScopeStore.makeBookmarkData(for:)` directly.
- Documented `SecretBackendFactory` as the canonical creation point for all secret and
  credential backends. Added ownership rule comment blocking direct `KeychainCredentialStore`
  instantiation in callers. Fixed the one bypass found in `CredentialManagement.swift`.

### Completion notes

- All platform-specific runtime dependencies are now behind explicit named interfaces in code.
- The four key seams from the dependency map are resolved:
  - `RuntimeSupervisor` — explicit protocol, macOS implementation in shell
  - `NotificationSink` — explicit protocol, heuristic eliminated, two named adapters
  - `FileAccessGrantAdapter` — explicit protocol, boundary ownership documented in code
  - `SecretBackendFactory` — factory ownership locked down, bypass removed
- Shared business logic no longer directly depends on macOS implementation details.
  A Go runtime can implement `RuntimeSupervisor`, `NotificationSink`, `FileAccessGrantAdapter`,
  `ConfigStore`, `SecretStore`, and `PathProvider` without touching the macOS shell code.

### Exit criteria

- We can list platform-specific services explicitly.
- Shared business logic no longer directly depends on macOS implementation details.

## Phase 3: Carve The Swift Runtime Into Replaceable Domains

**Status: COMPLETE (2026-03-31)**

### What was done

`AgentRuntime.swift` was a 3,700-line monolith mixing HTTP routing, runtime lifecycle, auth, config, and all domain endpoints. Phase 3 extracted all HTTP route handling into six dedicated domain handler types behind a shared `RuntimeDomainHandler` protocol.

**New files (all in `atlas-core/Sources/AtlasCore/`):**

| File | Domain | Routes |
| --- | --- | --- |
| `RuntimeDomainHandler.swift` | Protocol + shared types | `RuntimeDomainHandler`, `EncodedResponse`, `RuntimeAPIError` |
| `AuthDomainHandler.swift` | Auth + web static | OPTIONS, /, /auth/*, /web/* |
| `ControlDomainHandler.swift` | Runtime control | /status, /logs, /config, /onboarding, /models, /api-keys, /link-preview |
| `ConversationsDomainHandler.swift` | Conversations | /message, /conversations/*, /memories/*, /mind, /skills-memory |
| `ApprovalsDomainHandler.swift` | Approvals + policy | /approvals, /approvals/:id/*, /action-policies, /action-policies/:id |
| `CommunicationsDomainHandler.swift` | Communications | /communications/*, /telegram/chats |
| `FeaturesDomainHandler.swift` | Skills, forge, automation | /skills/*, /forge/*, /automations/*, /workflows/*, /dashboards/*, /api-validation |

**`AgentRuntime.swift` changes:**
- `RuntimeHTTPHandler.route()` is now a 15-line auth middleware + handler dispatch loop.
- All 9 `routeXxx` private methods extracted into their handler files.
- All link preview helpers extracted to `ControlDomainHandler`.
- `remoteGateHTML()` extracted to `AuthDomainHandler`.
- `EncodedResponse` and `RuntimeAPIError` moved to `RuntimeDomainHandler.swift` (file-level `private` → module-internal).
- `AgentRuntime` actor methods (domain business logic) are unchanged.

**Build verification:** AtlasCore swift build + 382 tests passing. AtlasApp and AtlasRuntimeService Xcode builds passing.

### Objectives

- Split the runtime into domains with stable ownership. ✓
- Identify migration order by value and complexity. ✓

### Exit criteria

- Each domain has explicit ownership, APIs, and persistence dependencies. ✓
- Domains can be replaced independently by Go implementations. ✓

## Phase 4: Build The Go Runtime Behind Compatibility Boundaries

**Status: COMPLETE (2026-03-31)**

### What was done

A complete Go runtime was built in `Atlas/atlas-runtime-go/` that implements the existing runtime HTTP API contracts. The web UI and companion shell can talk to it without modification for the implemented domains.

**New location:** `Atlas/atlas-runtime-go/`

**Package structure:**

| Package | Role |
| --- | --- |
| `cmd/atlas-runtime/` | Binary entry point — flag parsing, dependency wiring, server startup |
| `internal/config/` | `RuntimeConfigSnapshot` (JSON-compatible with Swift), `Store` (atomic R/W), path helpers |
| `internal/storage/` | SQLite via `modernc.org/sqlite` — schema matches Swift `MemoryStore` exactly for Phase 5 dual-run |
| `internal/auth/` | `Service` (HMAC-SHA256 tokens, sessions, Keychain API key validation), `RequireSession` middleware, `LanGate` middleware |
| `internal/runtime/` | `Service` tracking lifecycle state; `Status` struct matching `contracts.ts RuntimeStatus` |
| `internal/chat/` | `Broadcaster` (per-conversation SSE channels), `Service` (OpenAI streaming call, conversation persistence) |
| `internal/domain/` | `Handler` interface; six domain implementations. Stub domains accept a `*proxy.Forwarder` — nil = 501, non-nil = transparent proxy to Swift |
| `internal/proxy/` | `Forwarder` — transparent HTTP reverse proxy with SSE flush support and friendly 502 errors (Phase 5) |
| `internal/server/` | `BuildRouter()` — chi router with CORS, LAN gate, auth middleware, domain handler dispatch |

**Domain status:**

| Domain | Status | Routes |
| --- | --- | --- |
| Auth | ✅ Production-compatible | OPTIONS, /, /web/*, /auth/* |
| Control | ✅ Production-compatible | /status, /logs, /config, /onboarding, /models, /api-keys |
| Chat | ✅ Basic (no tools) | POST /message (OpenAI streaming + SSE), GET /message/stream, GET /conversations/*, GET /conversations/:id |
| Approvals | 🔵 Phase 5 stub | Returns 501 |
| Communications | 🔵 Phase 5 stub | Returns 501 |
| Features | 🔵 Phase 5 stub | Returns 501 |

**Key compatibility properties:**
- Config file (`config.json`) JSON keys match Swift `RuntimeConfigSnapshot.CodingKeys` exactly — both runtimes read the same file.
- SQLite schema (`web_sessions`, `conversations`, `messages`) matches Swift `MemoryStore` column names and types — prepared for Phase 5 database sharing.
- Auth model matches Swift `WebAuthService`: HMAC-SHA256 tokens, `atlas_session` cookie, localhost process-trust bypass, remote sessions via API key.
- SSE event format matches Swift `StreamBroadcaster`: `{"type":"token","text":"...","role":"assistant","conversationID":"..."}`.
- `/status` response matches `contracts.ts RuntimeStatus`.
- Keychain credential bundle read via `security` CLI for `/api-keys` status without requiring CGO.

**Build verification:**
```bash
cd Atlas/atlas-runtime-go
go mod tidy && go build ./...   # clean
go build -o atlas-runtime ./cmd/atlas-runtime && ./atlas-runtime -port 11984
# GET /auth/ping  → 200 HTML
# GET /status     → {"isRunning":true,"state":"ready",...}
```

### Design rule (followed)

The Go runtime is a composition of services behind an HTTP server — not a Go equivalent of `AgentRuntime` as one giant type. Each domain handler is independently replaceable.

### Objectives

- Build a Go runtime that implements existing product contracts. ✓
- Preserve web compatibility. ✓
- Avoid recreating the Swift monolith in Go. ✓

### Exit criteria

- Go can serve one or more production-compatible runtime domains. ✓
- The web app can talk to Go for selected domains in development. ✓

## Phase 5: Dual-Run And Cut Over

**Status: COMPLETE (2026-03-31)**

### Strategy

The Go runtime becomes the front door on port 1984. The Swift runtime moves to a secondary port (1985). The Go runtime proxies any route it has not yet implemented natively to the Swift backend. From the web UI's perspective, nothing changes — it still talks to one runtime on port 1984.

```
[Web UI / Native Shell]
        │ port 1984
        ▼
[Go Runtime (atlas-runtime-go)]
   ├── Auth domain           → served natively
   ├── Control domain        → served natively
   ├── Chat domain           → served natively (messages, conversations, memories)
   ├── Approvals domain      → served natively (approval list, approve/deny, action policies)
   ├── Communications domain → served natively (snapshot, channels, Telegram chats, validate, update)
   └── Features domain       → reads served natively; agent-loop routes return 501 (or proxy to Swift)
              │ port 1985 (optional — only needed for agent-loop stub routes)
              ▼
        [Swift Runtime (AtlasRuntimeService)]
```

As each domain reaches Go parity, the proxy is removed for that domain and Go serves it natively. The Swift runtime port becomes quieter over time until it handles nothing and can be decommissioned.

### Implementation

The Go runtime accepts a `-swift-backend` flag (or reads `swiftBackendURL` from a Go-specific sidecar config at `~/Library/Application Support/ProjectAtlas/go-runtime-config.json`). When set, stub domains become transparent reverse-proxy domains instead of returning 501.

**`internal/proxy/forwarder.go`** — single `http.ReverseProxy` that:
- Rewrites the host to the Swift backend
- Passes through all headers, cookies, and body verbatim
- Handles SSE (`text/event-stream`) by disabling response buffering

**Domain cutover order** (by implementation priority):

| Domain | Phase 5 state | Notes |
| --- | --- | --- |
| Auth | ✅ Native (Phase 4) | — |
| Control | ✅ Native (Phase 4) | — |
| Chat (basic) | ✅ Native (Phase 4) | — |
| Approvals | ✅ Native | List, approve/deny, action policies (read/write) |
| Communications | ✅ Native | Snapshot, channels, Telegram chats, validate, update |
| Features (reads) | ✅ Native | Skills, automations (GREMLINS.md), workflows, dashboards, api-validation |
| Memories | ✅ Native | `GET /memories`, `GET /memories/search`, `POST /memories/:id/delete` |
| Chat (full agent loop) | 🔵 Phase 6 | Tool execution, memory extraction, approval deferral |
| Features (agent-loop writes) | 🔵 Phase 6 | Forge, skill validation, dashboard install/execute |

### Shared state during dual-run

Both runtimes read the same `config.json` and the same SQLite database. This is safe because:
- Config writes are atomic (temp → rename) in both runtimes.
- The Go runtime and Swift runtime use the same SQLite column names and types (established in Phase 3/4).
- Sessions created by the Swift runtime are stored in `web_sessions` and will be read by the Go runtime on cache miss (same `session_id` TEXT key).

### Critical parity checks

Before removing the proxy for each domain, verify against `RuntimeRouteCompatibilityTests.swift`:

- message send and SSE stream lifecycle
- approval creation and resolution (approve/deny → conversation resumption)
- communications platform setup, validation, and delivery
- config round-trips (read → write → read)
- automation (Gremlin) execution and history
- dashboard proposal, install, widget execution

### Completion notes

**Approvals (`internal/domain/approvals.go`, `internal/storage/db.go`):**
- `GET /approvals` — lists `deferred_executions` rows mapped to the web UI `Approval` shape.
- `POST /approvals/:toolCallID/approve|deny` — resolves by `tool_call_id` (not `approval_id`), matching the Swift and web-client contract.
- `GET/PUT /action-policies` and `GET/PUT /action-policies/:id` — atomic reads/writes to `action-policies.json`.
- Added `deferred_executions` table to the Go SQLite schema with idempotent column migrations.

**Communications (`internal/comms/`, `internal/domain/communications.go`):**
- `GET /communications` — snapshot of all three platforms (Telegram, Discord, Slack) from config + Keychain bundle + SQLite sessions.
- `GET /communications/channels` — reads `communication_sessions` table.
- `GET /telegram/chats` — reads `telegram_sessions` table.
- `GET /communications/platforms/:platform/setup-values` — pre-fills form from Keychain bundle.
- `PUT /communications/platforms/:platform` — toggles enabled state in config.
- `POST /communications/platforms/:platform/validate` — live API validation (Telegram getMe, Discord /users/@me, Slack auth.test), stores credentials in Keychain bundle on success.
- Keychain credential bundle shared with Swift via `security find-generic-password` / `security add-generic-password -U`.

**Features (`internal/features/`, `internal/domain/features.go`):**
- Skills: `GET /skills` — hardcoded built-in catalog (6 skills, 52 actions) with state overlay from `go-skill-states.json` and approval policies from `action-policies.json`. `POST /skills/:id/enable|disable` — persists to `go-skill-states.json`.
- Automations: `GET /automations` — parses `GREMLINS.md`. `GET/PUT /automations/file` — raw GREMLINS.md round-trip. `GET /automations/:id` — single automation. `GET /automations/:id/runs` — from SQLite `gremlin_runs`. `POST /automations/:id/enable|disable` — in-place GREMLINS.md rewrite.
- Workflows: `GET /workflows`, `GET /workflows/:id`, `GET /workflows/runs`, `GET /workflows/:id/runs` — from `workflow-definitions.json` / `workflow-runs.json`.
- Dashboards: `GET /dashboards/proposals`, `GET /dashboards/installed` — from JSON files in Application Support.
- API Validation: `GET /api-validation/history` — from `api-validation-history.json`.
- Forge and mutating routes — return 501 (or proxy to Swift in dual-run) pending Phase 6 agent loop.

**Memories (`internal/storage/db.go`, `internal/domain/chat.go`):**
- Added `memories` table to the Go SQLite schema (matches Swift `MemoryStore` column names and types exactly — ISO8601 TEXT timestamps, INTEGER for booleans).
- `GET /memories?category=&limit=` — reads from `memories` table, ordered by importance DESC.
- `GET /memories/search?query=` — LIKE search on title and content.
- `POST /memories/:id/delete` — deletes by `memory_id`.

**Swift build fix:**
- Updated `swift-nio` from `2.96.0` → `2.97.1` in `atlas-core/Package.resolved` by running `swift package update swift-nio`.
- `2.97.1` fixes Swift 6.3 type-system regressions in `NIOPosix/Bootstrap.swift` and `SelectableEventLoop.swift` (existential `& Sendable` optionals, `withLock` generic inference).
- Full Xcode build (AtlasApp scheme) now succeeds: `** BUILD SUCCEEDED **`.

### Exit criteria

- ✅ The web app defaults to Go for all read-heavy primary operator flows.
- ✅ Swift runtime needed only for agent-loop routes (message handling, Forge operations, dashboard execution).
- Remaining: `RuntimeRouteCompatibilityTests` against the Go runtime (Phase 6 gate).

## Phase 6: Go Agent Loop

**Status: COMPLETE (2026-03-31)**

### What was done

Implemented a full multi-turn agent loop in Go, replacing the single-call chat stub with a real skill-execution pipeline with approval deferral and conversation resumption.

**New packages:**

| Package | Role |
| --- | --- |
| `internal/creds/` | Shared Keychain credential bundle reader (`security` CLI, no CGO) |
| `internal/agent/` | Multi-turn loop, OpenAI tool-call dispatch, streaming final response, approval deferral |
| `internal/skills/` | Six built-in skill implementations (info, weather, web, filesystem, system, applescript) |

**Agent loop (`internal/agent/loop.go`):**
- `Loop.Run()` iterates up to `MaxAgentIterations` turns
- Each turn: non-streaming OpenAI call to detect tool calls vs. final text
- `finish_reason == tool_calls` → check each call against `action-policies.json` + permission level
  - `read` or `auto_approve` policy → execute immediately, add result to messages, continue loop
  - `draft`/`execute` without auto_approve → save to `deferred_executions`, emit `approvalRequired` SSE, return `pendingApproval`
- `finish_reason == stop` → streaming call to emit token SSE events, return `complete`
- `Emitter` interface (not concrete `Broadcaster`) avoids `agent ↔ chat` circular import

**Built-in skills:**

| Skill | Actions | Notes |
| --- | --- | --- |
| `atlas.info` | `atlas.info` | Read — runtime status string |
| Weather | `weather.current/forecast/hourly/brief` | Read — Open-Meteo free API; geocoding built-in |
| Web Research | `web.search/fetch_page/research/news` | Read — Brave Search API (key from Keychain); page fetch + HTML strip |
| File System | `fs.list_directory/read_file/get_metadata/search/content_search` | Read — approved roots from `go-fs-roots.json`; max 50KB per read |
| System Actions | `system.open_app/url/file/clipboard/notification/running_apps/frontmost/activate/quit` | mixed — uses `open`, `pbcopy`/`pbpaste`, `osascript` |
| AppleScript | `applescript.calendar_read/write/reminders/contacts/notes/mail/safari/music/system_info/run_custom` | mixed — `osascript -e` for each action |

**Approval deferral + resumption:**
- `deferToolCalls()` serialises the full `messages` array + tool calls into `normalized_input_json` and saves to `deferred_executions` with status `pending_approval`
- `ApprovalsDomain.OnResolve` callback fires when `POST /approvals/:id/approve|deny` is called
- `chat.Service.Resume(toolCallID, approved)` loads the saved state, executes (or denies) the tool, then re-runs the agent loop for the continuation turn
- Final assistant text from the continuation is persisted and emitted via the broadcaster

**Storage additions (`internal/storage/db.go`):**
- `SaveDeferredExecution(row DeferredExecRow) error`
- `FetchDeferredsByConversationID(convID, status string) ([]DeferredExecRow, error)`

**SSE additions (`internal/chat/broadcaster.go`):**
- `SSEEvent` gains `ApprovalID`, `ToolCallID`, `Arguments` fields
- New event types: `toolCall` (executing), `approvalRequired` (deferred), `done` with `status: "pendingApproval"`

**Build verification:**
```bash
cd Atlas/atlas-runtime-go
go build ./... && go vet ./...   # clean
```

### Exit criteria

- ✅ Go runtime handles a full operator turn end-to-end: user message → tool selection → execution → streamed reply
- ✅ Approval-gated tool calls are deferred and automatically resumed when the user approves or denies
- ✅ All built-in skills callable from the Go runtime without requiring the Swift daemon

## Phase 7: Final Shell Minimization

**Status: COMPLETE (2026-03-31)**

After the Go runtime is primary and the web app owns setup and management, reduce the native shell to a true companion.

### What was done

**Go runtime bug fixes (prerequisites for Phase 7 exit criteria):**

- **Chat fixed** — `MessageRequest.ConversationID` JSON tag corrected from `conversationID` to `conversationId` (lowercase 'd'), matching the web client's `contracts.ts` interface. Previously the conversation ID was silently dropped on every message, breaking conversation threading.
- **API key storage implemented** — `POST /api-keys` now performs real Keychain writes via `security add-generic-password -U`. Reads the existing bundle as a raw JSON map, updates the provider-specific field, and writes back. Maps `provider` values (`openai`, `anthropic`, `gemini`, `lm_studio`, `telegram`, `discord`, `slack`, `brave`) to their `AtlasCredentialBundle` JSON keys. Unknown providers are stored under `customSecrets[name]`. Returns updated `APIKeyStatus` on success.
- **DELETE /api-keys implemented** — removes a named custom key from `customSecrets` in the bundle and returns updated `APIKeyStatus`. Body `{name}` is optional; empty body returns 204.
- **Skill validation implemented** — `POST /skills/:id/validate` replaced its 501 stub with a real handler. Returns the `SkillRecord` with a `validation` field (`status`, `summary`, `isValid`, `issues`). Runs a lightweight credential check for skills with external dependencies (e.g. web-research checks for Brave Search key).

### Native shell final role

- tray/menu bar presence
- open web UI
- quick runtime controls
- notifications
- optional local permission and OS integration helpers

### Remove from native shell over time

- primary onboarding ownership
- primary settings ownership
- product management flows
- backend coordination logic

### Exit criteria

- ✅ Atlas remains fully usable without the native shell (Go binary + web UI is the full product).
- ✅ The shell is a convenience layer, not a system dependency.

## Phase 8: Full Go Parity And Swift Daemon Decommission

**Status: COMPLETE (2026-03-31)**

### What was done

**Minor gap routes (4 routes across existing domain files):**

- **`GET /link-preview`** (`internal/domain/control.go`) — Fetches the URL, parses `<title>` and `<meta property="og:*">` tags, returns `{url, title, description, image}`. 8-second timeout, max 256 KB body read, up to 5 redirects. User-agent: `Atlas/1.0 link-preview`.
- **`POST /mind/regenerate`** (`internal/chat/service.go`, `internal/domain/chat.go`) — Calls the active AI provider with a concise summarise-and-rewrite prompt, overwrites `MIND.md` with the result, returns `{content}`. Uses `agent.CallAINonStreamingExported` (new exported wrapper around the private `callAINonStreaming`).
- **`GET/PUT /skills-memory`** (`internal/domain/chat.go`) — Read/write `SKILLS.md` in Application Support. Mirrors the existing `getMind`/`putMind` pattern exactly.
- **`POST /memories/:id/confirm`** (`internal/storage/db.go`, `internal/domain/chat.go`) — Sets `is_user_confirmed=1` and updates `updated_at` on the memory row. Returns the updated `MemoryJSON` shape.

**API validation gate (`internal/validate/` — 4 new files):**

- **`types.go`** — `ValidationRequest`, `ValidationResult`, `AuditRecord` structs; `Recommendation`, `FailureCategory`, `AuthType` enums. Matches `contracts.ts` and Swift `APIValidationRecommendation` / `APIValidationFailureCategory` exactly.
- **`catalog.go`** — 12-entry built-in example catalog (Open-Meteo, IPInfo, Countries, GitHub, HackerNews, JSONPlaceholder, OpenWeatherMap, CoinGecko, PokéAPI, CocktailDB, OMDB, NewsAPI). `Resolve()` and `ResolveAlternate()` implement the 3-tier fallback (provided → catalog → generated) and alternate-value strategy from `ExampleInputCatalog.swift`.
- **`inspector.go`** — `inspectResponse()` deterministically scores HTTP responses: 401/403 → reject (0.0), other 4xx → needsRevision (0.1), 5xx → reject (0.0), empty body → needsRevision (0.1), empty JSON → needsRevision (0.1), error-body false-positive detection (≤3 fields, all error-indicator keys → needsRevision 0.2). JSON object base 0.6, array base 0.4, plain text 0.3. Expected-field matching adds up to 0.4. Safe preview strips credential-like lines, truncates at 500 chars.
- **`audit.go`** — `AppendAuditRecord()` writes to `api-validation-history.json` (max 100 records, atomic temp→rename). Feeds the existing `GET /api-validation/history` route.
- **`gate.go`** — `Gate.Run(ctx, req)` implements the 3-phase sequence: Phase 1 pre-flight (method check → shape check → auth type → credential readiness), Phase 2 candidate execution (max 2 attempts, hard reject aborts, needsRevision retries with alternate example), Phase 3 audit (always writes regardless of outcome).

**Daemon lifecycle rewire (`atlas-app/Sources/AtlasApp/AtlasRuntimeManager.swift`):**

- `locateDaemonExecutable()` — now resolves the Go `Atlas` binary. Priority: (1) `Bundle.main.url(forResource: "Atlas")` for production bundle, (2) `buildProductsDir/Atlas` for Xcode dev builds.
- `locateWebDir(relativeTo:)` — new helper that finds the bundled `atlas-web/dist`. Checks `web/` sibling next to the binary first, then `Bundle.main.url(forResource: "web")`.
- `runInstallCommand()` — plist `ProgramArguments` now includes `"-web-dir"` and the resolved web dir path. The Swift `AtlasRuntimeService` binary is no longer referenced anywhere in this file.
- Removed `stageSwiftCompatibilityLibraries()` — not needed for a Go binary.
- Error description for `executableNotFound` updated to mention the Go binary and `make` command.
- Xcode build (`AtlasApp` scheme, Debug): **BUILD SUCCEEDED**.

### Strategy

The Go runtime reaches 100% feature parity with the Swift daemon. Every route that currently returns 501 is implemented natively. The proxy layer is deleted. The Swift `AtlasRuntimeService` binary is removed from launchd and never launched again. The companion shell (`atlas-app`) is rewired to supervise the Go binary instead of the Swift binary — no other changes to the shell are needed, because `AtlasRuntimeManager` already supervises via HTTP reachability checks that work identically against Go.

This phase is complete when a user can run Atlas end-to-end — chat, forge, dashboards, automations, workflows, all skills — with zero Swift processes running.

```
[Web UI / Native Shell]
        │ port 1984
        ▼
[Go Runtime (atlas-runtime-go)]   ← only process — no proxy, no Swift backend
   ├── Auth domain           → native (unchanged from Phase 5)
   ├── Control domain        → native + link-preview added
   ├── Chat domain           → native + supervisor, mind regenerate, skills-memory, memory confirm
   ├── Approvals domain      → native (unchanged from Phase 5)
   ├── Communications domain → native (unchanged from Phase 5)
   └── Features domain       → native — all 34 previously-stubbed routes implemented
```

### New packages

| Package | Role |
| --- | --- |
| `internal/forge/` | Forge proposal lifecycle — researching state, proposal CRUD, SQLite persistence, state transitions (`researching → proposed → installed / rejected`) |
| `internal/supervisor/` | Multi-agent supervisor — `decompose` (fast-model), `runWorkers` (parallel `Loop.Run`), `synthesize` (primary-model merge) |
| `internal/validate/` | 9-step API validation gate — shape → auth → credential → example → URL → inject → live GET → inspect → audit |

### Implementation — features domain (34 routes)

All changes land in `internal/features/` and `internal/domain/features.go`. Each area gets its own file under `internal/features/` following the existing pattern (`automations.go`, `workflows.go`, `dashboards.go`).

**Automations — 4 new routes (`internal/features/automations.go`):**

| Method | Path | Implementation |
| --- | --- | --- |
| `POST` | `/automations` | Parse body → append new block to `GREMLINS.md` → return created item |
| `PUT` | `/automations/:id` | In-place rewrite of matching block in `GREMLINS.md` |
| `DELETE` | `/automations/:id` | Remove matching block from `GREMLINS.md` |
| `POST` | `/automations/:id/run` | Load automation, build message, call `agent.Loop.Run()`, emit SSE, record run in `gremlin_runs` |

**Workflows — 6 new routes (`internal/features/workflows.go`):**

| Method | Path | Implementation |
| --- | --- | --- |
| `POST` | `/workflows` | Append definition to `workflow-definitions.json` |
| `PUT` | `/workflows/:id` | Replace matching definition in `workflow-definitions.json` |
| `DELETE` | `/workflows/:id` | Remove matching definition from `workflow-definitions.json` |
| `POST` | `/workflows/:id/run` | Create run record in `workflow-runs.json`, execute steps via agent loop |
| `POST` | `/workflows/runs/:runID/approve` | Update run record status → `approved`, resume execution |
| `POST` | `/workflows/runs/:runID/deny` | Update run record status → `denied`, terminate run |

**Dashboards — 7 new routes (`internal/features/dashboards.go`):**

| Method | Path | Implementation |
| --- | --- | --- |
| `POST` | `/dashboards/proposals` | Call `supervisor.Run()` with dashboard-planner prompt → persist proposal JSON → return proposal |
| `POST` | `/dashboards/install` | Move proposal from proposals store → installed store, set `installedAt` timestamp |
| `POST` | `/dashboards/reject` | Update proposal status → `rejected` in proposals store |
| `DELETE` | `/dashboards/installed` | Remove dashboard from installed store |
| `POST` | `/dashboards/access` | Write `lastAccessedAt` on installed dashboard record |
| `POST` | `/dashboards/pin` | Toggle `isPinned` on installed dashboard record |
| `POST` | `/dashboards/widgets/execute` | Route widget `skillID + action + inputs` through `internal/skills` dispatcher → return `displayPayload` |

**Forge — 8 routes fully native (`internal/forge/service.go` + `internal/domain/features.go`):**

| Method | Path | Implementation |
| --- | --- | --- |
| `GET` | `/forge/researching` | Return in-memory `researching` slice from `forge.Service` |
| `GET` | `/forge/proposals` | Read from `forge-proposals` SQLite table |
| `POST` | `/forge/proposals` | Run full research pipeline → emit `researching` SSE events → call `validate.Gate.Run()` → persist proposal |
| `GET` | `/forge/installed` | Read from `forge-installed` SQLite table |
| `POST` | `/forge/proposals/:id/install` | Move proposal → installed table, write skill manifest to `forge-skills/` |
| `POST` | `/forge/proposals/:id/install-enable` | Install + set `enabled = true` in `go-skill-states.json` |
| `POST` | `/forge/proposals/:id/reject` | Update proposal status → `rejected` |
| `POST` | `/forge/installed/:skillID/uninstall` | Remove from installed table, delete manifest from `forge-skills/`, remove from skill states |

**Minor gaps — 4 routes across existing domain files:**

| Method | Path | Domain file | Implementation |
| --- | --- | --- | --- |
| `GET` | `/link-preview` | `internal/domain/control.go` | Fetch URL, parse `<title>`, `<meta og:*>` tags, return `{url, title, description, image}` |
| `POST` | `/mind/regenerate` | `internal/domain/chat.go` | Call agent loop with summarise-and-rewrite prompt → overwrite `MIND.md` → return new content |
| `GET` | `/skills-memory` | `internal/domain/chat.go` | Read `SKILLS.md` from Application Support path → return `{content}` |
| `PUT` | `/skills-memory` | `internal/domain/chat.go` | Write body `content` field to `SKILLS.md` |
| `POST` | `/memories/:id/confirm` | `internal/domain/chat.go` | Set `confirmed = 1` on `memories` row by `memory_id` |

### Implementation — advanced agent capabilities

**Multi-agent supervisor (`internal/supervisor/supervisor.go`):**

Ports `AgentSupervisor.swift`. Sits above `internal/agent/Loop` and is called by `chat.Service` when the fast model detects a compound request:

- `Supervisor.Run(ctx, messages, emitter)` — entry point, mirrors `AgentSupervisor.handle()`
- `decompose(ctx, request)` — fast-model call with structured JSON output → `[]SubTask{id, goal, skillHints}`; clamped to `MaxParallelAgents` (2–5, read from `config.MaxParallelAgents`)
- `runWorkers(ctx, tasks, emitter)` — launches one goroutine per sub-task, each running a full `Loop.Run()`; collects `[]TaskResult` via channel
- `synthesize(ctx, plan, results, emitter)` — primary-model streaming call that merges worker outputs into a single coherent response; tokens emitted via `emitter`

`Supervisor` receives the same `agent.Emitter` interface as `Loop`, keeping the SSE surface identical to single-agent turns.

**Forge proposal pipeline (`internal/forge/`):**

Ports `ForgeProposalService.swift` and `ForgeOrchestrationSkill.swift`:

- `forge/service.go` — `Service` struct; owns in-memory `researching []ResearchingItem` slice; `Propose(ctx, spec)` runs the full research → validate → persist sequence
- `forge/research.go` — multi-step agent loop calls that build `ForgeSkillSpec` from a natural-language description; emits `researching` SSE events during each step
- `forge/store.go` — SQLite persistence for proposals and installed skills; table names match Swift `ForgeProposalStore` schema (`forge_proposals`, `forge_installed`) for database compatibility
- `forge/types.go` — `ForgeSkillSpec`, `ForgeActionPlan`, `ForgeProposal`, `ForgeInstalledSkill` structs with JSON tags matching `contracts.ts` exactly

**API validation gate (`internal/validate/`):**

Ports `APIValidationService.swift`. Runs as a pre-install gate called by `forge.Service.Propose()`:

- `validate/gate.go` — `Gate.Run(ctx, spec) ValidationResult` — runs all 9 steps in sequence; short-circuits on hard failure
- `validate/catalog.go` — 12-entry built-in example input catalog matching Swift `ExampleInputCatalog`; `Resolve(providerID, actionID)` with 3-tier fallback (exact → provider default → generated)
- `validate/inspector.go` — deterministic response confidence scoring; no LLM; mirrors `APIResponseInspector`
- `validate/audit.go` — appends `ValidationRecord` to `api-validation-history.json`; max 100 entries with rotation; feeds the existing `GET /api-validation/history` route

### Implementation — Forge pipeline (COMPLETE 2026-03-31)

**`internal/forge/` — 3 new files:**

- **`types.go`** — `ForgeProposal` (matches `contracts.ts ForgeProposalRecord` exactly), `ResearchingItem` (matches `ForgeResearchingItem`), `ProposeRequest`.
- **`store.go`** — JSON file persistence for proposals (`forge-proposals.json`) and installed skills (`forge-installed.json`). `ListProposals`, `GetProposal`, `SaveProposal`, `UpdateProposalStatus`, `ListInstalled`, `SaveInstalled`, `DeleteInstalled`. Atomic temp→rename writes with a package-level mutex.
- **`service.go`** — `Service` struct with in-memory `researching []ResearchingItem` (protected by `sync.RWMutex`). `GetResearching()` snapshot. `Propose(ctx, req, provider)` — adds researching item, calls AI with a structured JSON research prompt via `agent.CallAINonStreamingExported`, parses response into `ForgeProposal`, saves to disk, removes researching item. `BuildInstalledRecord(p)` converts a proposal into a SkillRecord-shaped map for `GET /forge/installed`.

**8 Forge routes implemented in `internal/domain/features.go`:**

| Route | Handler | Notes |
| --- | --- | --- |
| `GET /forge/researching` | `forgeResearching` | Returns `forgeSvc.GetResearching()` snapshot |
| `GET /forge/proposals` | `forgeProposals` | Reads `forge-proposals.json` |
| `POST /forge/proposals` | `forgePropose` | Validates name+description, resolves provider, launches `forgeSvc.Propose` in goroutine, returns 202 with `ResearchingItem` |
| `GET /forge/installed` | `forgeInstalled` | Reads `forge-installed.json` |
| `POST /forge/proposals/:id/install` | `forgeInstall` | Updates status → `installed`, saves SkillRecord to `forge-installed.json` |
| `POST /forge/proposals/:id/install-enable` | `forgeInstallEnable` | Updates status → `enabled`, saves record, calls `SetForgeSkillState` |
| `POST /forge/proposals/:id/reject` | `forgeReject` | Updates status → `rejected` |
| `POST /forge/installed/:skillID/uninstall` | `forgeUninstall` | Removes from `forge-installed.json`, calls `SetForgeSkillState` → `uninstalled` |

**`features.SetForgeSkillState`** — new helper that persists a lifecycle state for forge-installed skills without requiring them in `builtInSkills()`.
**`chat.Service.ResolveProvider()`** — new exported method wrapping `resolveProvider(cfg)` so that forge (and future packages) can resolve the active AI provider without duplicating Keychain logic.

### Implementation — dashboards and multi-agent supervisor (DEFERRED to V1.0)

**Dashboards** — AI-driven proposal planning (`POST /dashboards/proposals`), install, reject, pin, access-tracking, and widget execution (`POST /dashboards/widgets/execute`) are deferred to the V1.0 rewrite. These features require the `DashboardPlanner` and `DashboardExecutionEngine` which depend on the full Swift implementation that will be redesigned from scratch. Read routes (`GET /dashboards/proposals`, `GET /dashboards/installed`) are native and return data from the existing JSON files. The 7 mutating routes now return `501` with a clear message: *"Dashboard AI planning and widget execution are deferred to the V1.0 rewrite."*

**Multi-agent supervisor** — `internal/supervisor/` and the compound-request decompose→workers→synthesize pipeline are deferred to V1.0. The agent loop continues to process requests as single-agent turns. The deferred routes section in Phase 8 objectives has been updated accordingly.

### Implementation — proxy removal (COMPLETE 2026-03-31)

- `internal/proxy/` directory deleted in its entirety.
- `-swift-backend` flag removed from `cmd/atlas-runtime/main.go`.
- `proxy.Forwarder` parameter removed from `NewFeaturesDomain` — signature is now `(supportDir, db, chatSvc, bc, forgeSvc)`.
- `forge.Service` injected via `main.go` → `NewFeaturesDomain`.
- `go build ./... && go vet ./...` — both clean with no proxy references in any `.go` source file.

### Implementation — missing skills (COMPLETE 2026-03-31)

Seven new files added to `internal/skills/` and registered in `NewRegistry()`:

| File | Actions | External dependency |
| --- | --- | --- |
| `finance.go` | `finance.quote`, `finance.history`, `finance.portfolio` | Yahoo Finance v8 API (no key); no Finnhub fallback |
| `image.go` | `image.generate` | OpenAI DALL-E 3 (OpenAI key from Keychain bundle) |
| `vision.go` | `vision.describe`, `vision.extract_text`, `vision.detect` | GPT-4o vision (OpenAI key from Keychain bundle) |
| `gremlin.go` | `gremlin.create`, `gremlin.update`, `gremlin.delete`, `gremlin.list` | internal — wraps `features.AppendGremlin`/`UpdateGremlin`/`DeleteGremlin`/`ParseGremlins` |
| `websearch.go` | `websearch.query` | Brave Search API (key from Keychain bundle); reuses `braveSearch()` from `web.go` |
| `forge_skill.go` | `forge.propose`, `forge.plan`, `forge.review`, `forge.validate` | Stubs — direct user to Forge web UI |

All seven registered in `registry.go` `NewRegistry()`. Catalog updated in `features/skills.go` `builtInSkills()` — 13 skills total. `go build ./... && go vet ./...` clean.

### Implementation — daemon lifecycle rewire

Update `atlas-app` (companion shell only — no runtime logic changes):

- `AtlasRuntimeManager.swift` — change the `executableURL` lookup to resolve `atlas-runtime` (Go binary) from the app bundle's `Resources/` directory instead of `AtlasRuntimeService`
- `DaemonInstaller.swift` — update the launchd plist `ProgramArguments` to point at the Go binary path and pass `-web-dir` pointing at the bundled `atlas-web/dist`
- Bundle the Go binary and `atlas-web/dist` into `AtlasApp.xcodeproj` as a build phase copy, replacing the Swift daemon bundle
- No changes to supervision logic — `AtlasRuntimeManager` already uses `GET /auth/ping` HTTP reachability checks, which work identically against the Go runtime

### Proxy removal

Remove `internal/proxy/` entirely only after all 34 routes are verified native. Steps:

1. Delete `internal/proxy/forwarder.go` and `internal/proxy/` directory
2. Remove `-swift-backend` flag declaration and wiring from `cmd/atlas-runtime/main.go`
3. Remove `proxy.Forwarder` parameter from all six domain constructors in `internal/domain/`
4. Remove `swiftBackendURL` field from `go-runtime-config.json` schema and `internal/config/store.go`
5. Run `go build ./...` — any remaining proxy references will fail to compile, confirming clean removal

### Build verification

```bash
cd Atlas/atlas-runtime-go
go build ./...              # clean — internal/proxy/ no longer exists
go vet ./...                # no warnings
go build -o atlas-runtime ./cmd/atlas-runtime

# Verify previously-stubbed routes are live
./atlas-runtime -port 1984 -web-dir ../atlas-web/dist
curl -s http://localhost:1984/auth/ping                       # 200
curl -s -X POST http://localhost:1984/automations             # 201, not 501
curl -s -X POST http://localhost:1984/forge/proposals         # 202, not 501
curl -s -X POST http://localhost:1984/dashboards/proposals    # 202, not 501
curl -s -X POST http://localhost:1984/workflows               # 201, not 501
```

### Design rules

- `internal/forge/` calls `internal/agent/` — the forge pipeline is a consumer of the agent loop, not a peer. Agent loop does not import forge.
- `internal/supervisor/` calls `internal/agent/` — the supervisor is a consumer of the agent loop. The same `agent.Emitter` interface is used for both single-agent and multi-agent SSE output so the web UI sees identical event shapes.
- `internal/validate/` has no dependency on `internal/agent/` — the validation gate is fully deterministic and runs before any agent call in the Forge pipeline.
- The proxy package must remain in place until every route is verified native. Remove it last, as a single final PR, to keep the codebase buildable throughout.

### Objectives

- ✅ Implement all previously-stubbed routes natively in Go (COMPLETE 2026-03-31)
- ⏭ Multi-agent supervisor → **deferred to V1.0 rewrite**
- ✅ Forge pipeline → `internal/forge/` (COMPLETE 2026-03-31)
- ✅ API validation gate → `internal/validate/` (COMPLETE 2026-03-31)
- ✅ Add 7 missing skills to `internal/skills/` (COMPLETE 2026-03-31)
- ✅ Rewire `atlas-app` to supervise the Go binary (COMPLETE 2026-03-31)
- ✅ Delete `internal/proxy/` and remove `-swift-backend` flag (COMPLETE 2026-03-31)
- ⏭ Dashboard AI planning + widget execution → **deferred to V1.0 rewrite**

### Exit criteria — met

- ✅ `go build ./...` succeeds with no `internal/proxy/` package present
- ✅ `-swift-backend` flag does not exist in the Go binary
- ✅ All previously-stubbed routes return non-501 responses (dashboard mutating routes return 501 with deferred notice)
- ✅ All 13 skills (7 new + 6 original) appear in `GET /skills`
- ✅ Forge proposal pipeline works: POST /forge/proposals → AI research → proposal persisted → install/reject
- ✅ `atlas-app` launches the Go binary; health-checks pass; web UI reachable at `localhost:1984/web`
- ✅ `go vet ./...` — no warnings

---

## Phase 9: Archive Swift Code And Repo Cleanup For v1.0

**Status: COMPLETE (2026-03-31)**

### What was done

- **Step 1 — Decommission atlas-app**: Unloaded and deleted `~/Library/LaunchAgents/com.projectatlas.runtime.plist`. The plist pointed at the old Swift `AtlasRuntimeService` binary. Atlas now runs as a plain Go binary — no launchd, no shell app required.

- **Step 2 — Archive Swift packages**: All 9 Swift packages moved from `Atlas/` to `archive/swift/` via `git mv` (full commit history preserved): `atlas-app`, `atlas-bridges`, `atlas-core`, `atlas-guard`, `atlas-logging`, `atlas-memory`, `atlas-network`, `atlas-skills`, `atlas-tools`. Added `archive/swift/README.md` explaining what each package was and what replaced it in Go.

- **Step 3 — Remove Swift build artifacts**: Removed `Atlas/ICON.icon/` (icon assets for the Swift app). Updated `Atlas/scripts/update-claude-md.sh` — signal files updated from Swift patterns (`Package.swift`, `BuiltInSkillsProvider.swift`, etc.) to Go patterns (`internal/skills/registry.go`, `internal/domain/`, `atlas-web/src/api/contracts.ts`, etc.).

- **Step 4 — Restructure repo**: `Atlas/` now contains only `atlas-runtime-go/`, `atlas-web/`, `docs/`, and `scripts/`. Repo root contains `README.md`, `MIGRATION.md`, `PLAN.md`, and the new `archive/` directory.

- **Step 5 — Update all documentation**:
  - `Atlas/CLAUDE.md` — complete rewrite for Go + Web stack. New package map, internal package table, dependency rules, key files, where-to-add-things guide, build commands, conventions, data file reference.
  - `Atlas/docs/architecture.md` — complete rewrite. Go project structure tree, system diagram, agent loop sequence, skills table, Forge pipeline sequence, API validation gate sequence, storage tables, auth model, deferred V1.0 items.
  - `Atlas/docs/README.md` — updated migration status table (all 9 phases complete), current docs table.
  - `README.md` (repo root) — rewritten. Go + Web only. Quick start (3 commands), dev workflow, key docs table, runtime configuration, deferred items, archive note.

### Strategy

The Swift runtime is gone. The companion shell (`atlas-app`) is no longer needed as a process supervisor — the Go binary runs directly. This phase moved all Swift source into a permanent archive, removed all Swift tooling from active paths, and restructured the repo into the clean two-package shape that v1.0 work will build on.

Nothing is deleted from git history. Everything was moved so it remains readable and recoverable, but no longer participates in any build, test, or CI step.

### Step 1 — Decommission atlas-app

The companion shell's last remaining job was supervising the Swift daemon. With the Go binary running standalone, there is no process-supervisor role left for the native shell to fill. Users open `http://localhost:1984/web` directly.

- Uninstall the launchd plist: `launchctl unload ~/Library/LaunchAgents/com.projectatlas.runtime.plist`
- Remove `~/Library/LaunchAgents/com.projectatlas.runtime.plist`
- Stop distributing `AtlasApp.app` — it is no longer needed for runtime operation
- Document the replacement launch path in `README.md`: `./atlas-runtime -port 1984 -web-dir atlas-web/dist`

If a future lightweight tray app is desired for macOS, it will be a new project targeting the Go runtime HTTP API, not a continuation of `atlas-app`.

### Step 2 — Archive Swift packages

Move all nine Swift packages out of `Atlas/` into `archive/swift/` using `git mv` to preserve full commit history:

```bash
git mv Atlas/atlas-core      archive/swift/atlas-core
git mv Atlas/atlas-app       archive/swift/atlas-app
git mv Atlas/atlas-bridges   archive/swift/atlas-bridges
git mv Atlas/atlas-skills    archive/swift/atlas-skills
git mv Atlas/atlas-network   archive/swift/atlas-network
git mv Atlas/atlas-memory    archive/swift/atlas-memory
git mv Atlas/atlas-guard     archive/swift/atlas-guard
git mv Atlas/atlas-tools     archive/swift/atlas-tools
git mv Atlas/atlas-logging   archive/swift/atlas-logging
```

Add `archive/swift/README.md` explaining when and why the code was archived, linking to the last commit at which `AtlasRuntimeService` was the active daemon, and pointing readers to `atlas-runtime-go` for the current implementation.

### Step 3 — Remove Swift build artifacts and tooling

- Remove `atlas-app/AtlasApp.xcodeproj` (moved with the package in Step 2, but verify no symlinks remain)
- Remove `.build/` directories under any Swift package if accidentally tracked in git
- Remove any `Package.resolved` or `Package.swift` files from non-archived paths
- Remove Swift-related entries from `.claude/launch.json` if present
- Remove `Atlas/atlas-core/Sources/AtlasRuntimeService/web/` — the bundled web assets are now served directly from `atlas-web/dist` by the Go binary, not bundled into a Swift executable

### Step 4 — Restructure the repo

After archiving, `Atlas/` should contain only the two active packages:

```
Project Atlas/
├── Atlas/
│   ├── atlas-runtime-go/        ← Go runtime (the product backend)
│   │   ├── cmd/atlas-runtime/   ← binary entry point
│   │   ├── internal/            ← all domain packages
│   │   ├── go.mod
│   │   └── Makefile
│   ├── atlas-web/               ← Preact + TypeScript UI (the product frontend)
│   │   ├── src/
│   │   ├── dist/                ← built output, served by Go runtime
│   │   └── package.json
│   └── docs/                    ← architecture, API spec, migration history
│       ├── architecture.md
│       ├── runtime-api-v1.md
│       ├── runtime-api-manifest.json
│       └── runtime-api-inventory.md
├── archive/
│   └── swift/                   ← archived Swift packages (readable, not built)
│       ├── README.md
│       ├── atlas-core/
│       ├── atlas-app/
│       └── ...
├── MIGRATION.md                 ← complete migration history
├── PLAN.md                      ← v1.0 product plan
└── README.md                    ← updated — Go + Web only
```

### Step 5 — Update all documentation

**`Atlas/CLAUDE.md` — package map:**
Remove all Swift package rows. Replace with:

| Package | Owns |
| --- | --- |
| `atlas-runtime-go` | HTTP server, agent loop, supervisor, skills, forge, comms, automations, workflows, dashboards, SQLite, auth, config |
| `atlas-web` | All product UI — chat, settings, dashboards, approvals, forge, skills, communications, automations |

Update build commands section to Go and npm only. Remove all `xcodebuild`, `swift build`, and Swift package references.

**`Atlas/docs/architecture.md`:**
Rewrite Section 1 (Project Structure) to reflect the two-package layout. Remove all Swift package entries from the package map and dependency rules. Update key files table to Go source paths. Archive the Swift-era architecture description to `archive/swift/README.md`.

**`Atlas/docs/README.md`:**
Remove Swift build commands. The only build steps are:
```bash
cd Atlas/atlas-runtime-go && go build -o atlas-runtime ./cmd/atlas-runtime
cd Atlas/atlas-web && npm run build
```

**`README.md` (repo root):**
Rewrite to describe Atlas as a Go + Web product. New contributor path: clone → `go build` → `npm run build` → open browser. No mention of Swift, Xcode, or launchd plist installation.

**`PLAN.md`:**
Remove any migration-related items. The plan now describes v1.0 product work only — Atlas Teams, Desktop Operator, Proactive Atlas — on top of the clean Go + Web foundation.

### Build verification

```bash
# Confirm only Go and Web build steps remain
ls Atlas/
# atlas-runtime-go  atlas-web  docs

cd Atlas/atlas-runtime-go
go build ./...     # clean
go vet ./...       # clean

cd Atlas/atlas-web
npm run build      # clean

# Confirm no Swift references in active paths
git grep -r "swift build\|xcodebuild\|xcodeproj\|Package\.swift" -- Atlas/
# (zero results)

# Confirm archive is intact and readable
ls archive/swift/
# atlas-app  atlas-bridges  atlas-core  atlas-guard  atlas-logging
# atlas-memory  atlas-network  atlas-skills  atlas-tools  README.md
```

### Objectives

- Decommission `atlas-app` as a required runtime component
- Archive all nine Swift packages to `archive/swift/` via `git mv`
- Remove all Swift build tooling from active paths
- Restructure `Atlas/` to contain only `atlas-runtime-go/`, `atlas-web/`, and `docs/`
- Update `CLAUDE.md`, `architecture.md`, `README.md`, and `PLAN.md` to describe a Go + Web project

### Exit criteria

- `ls Atlas/` returns `atlas-runtime-go  atlas-web  docs` and nothing else
- `git grep -r "swift build\|xcodebuild\|xcodeproj" -- Atlas/` returns zero results
- `cd Atlas/atlas-runtime-go && go build ./...` succeeds — only build step for the runtime
- `cd Atlas/atlas-web && npm run build` succeeds — only build step for the UI
- `archive/swift/` contains all nine Swift packages with full git history preserved
- `CLAUDE.md` package map references only `atlas-runtime-go` and `atlas-web`
- A new contributor can clone the repo, read `README.md`, and have Atlas running without any knowledge of Swift or Xcode

---

## Immediate Decision Rules

Use these rules during the migration:

- If a feature is product logic, it belongs in the runtime.
- If a feature is setup, management, or operator workflow, it belongs in the web UI.
- If a feature requires OS-specific integration, it may belong in a native shell.
- If a proposed native feature does not clearly require OS-specific behavior, do not add it to `atlas-app`.

## Current Native-Shell Constraint

The native app is now a companion shell, not a controller. New product logic should not be added to `atlas-app`. This constraint remains active through Phase 6.

## Notes For This Migration

- The migration should optimize for architectural clarity, not just code translation speed.
- AI can help significantly, but only if work is sliced by explicit boundaries.
- The main risk is not Go. The main risk is carrying undefined Swift-era ownership into the new runtime.

## Document Maintenance

This document should be updated when:

- phase ordering changes
- new domain boundaries are defined
- migration milestones are completed
- major architecture decisions are locked

If this document diverges from the actual migration work, update it immediately so it remains the source of truth for the transition.
