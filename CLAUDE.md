# Project Atlas — Claude Code Reference

Automatically loaded at the start of every session. Navigation map only — read actual source files for implementation details.

Current source of truth:
- System design: `Atlas/docs/architecture.md`
- Agent boundary: `Atlas/docs/agent-boundary.md`
- Teams V1 spec: `Atlas/docs/teams-v1-implementation-spec.md`
- Internal module contract: `Atlas/docs/internal-modules.md`
- Migration verification: `Atlas/docs/migration-verification.md`
- Manual smoke checklist: `Atlas/docs/manual-smoke-checklist.md`
- API reference: `Atlas/docs/runtime-api-v1.md`
- Migration history: `archive/MIGRATION.md`
- This file: contributor navigation and workflow shortcuts

---

## Package Map

| Package | Owns |
| --- | --- |
| `atlas-runtime` | HTTP server, agent loop, private module host, first-party feature modules, skills registry, forge pipeline, auth, config, SQLite storage, comms bridges. Runs as launchd daemon `Atlas` on port 1984. |
| `atlas-web` | All product UI — Chat, Forge, Skills, Approvals, Memory, Automations, Workflows, Communications, Settings |

Swift packages are archived at `archive/swift/`. They are not built and not referenced by any active code.

---

## `atlas-runtime` Internal Package Map

| Package | Role |
| --- | --- |
| `cmd/atlas-runtime` | Binary entry point — flags, service wiring, `http.ListenAndServe` |
| `internal/agent` | OpenAI/Anthropic/Gemini/LM Studio provider calls, agent loop (tool dispatch, approval deferral, SSE emission) |
| `internal/auth` | Session auth — bootstrap, token issuance, HMAC session validation |
| `internal/chat` | Atlas Agent subsystem in current package form: `Service` (turn orchestration), `Broadcaster` (SSE fan-out) |
| `internal/comms` | Telegram + Discord bridge lifecycle, channel management |
| `internal/config` | `RuntimeConfigSnapshot`, `Store` (atomic JSON read/write), `GoRuntimeConfig` |
| `internal/creds` | Keychain credential bundle reader (`security` CLI) |
| `internal/domain` | Core HTTP domains that remain private core-owned: `auth`, `chat`, `control`, plus shared handler helpers |
| `internal/features` | Automations (GREMLINS.md), workflows (JSON), API validation history, skills state, diary (DIARY.md) |
| `internal/platform` | Private runtime host, module registry, scoped storage, agent/context seams, selective event bus |
| `internal/modules` | First-party extracted feature modules: approvals, automations, communications, dashboards, forge, workflows, skills, agents, engine, usage, api-validation |
| `internal/forge` | Forge proposal lifecycle — AI research, JSON persistence, install/uninstall |
| `internal/logstore` | In-memory log ring buffer (500 entries) — written by agent loop and services, read by `GET /logs` |
| `internal/memory` | Per-turn memory extraction — two-stage pipeline (regex 7 categories + LLM); saves to SQLite `memories` table |
| `internal/mind` | MIND.md two-tier reflection pipeline + SKILLS.md learned-routine detection (both non-blocking after each turn) + nightly dream consolidation cycle (3 AM, 5 phases) |
| `internal/runtime` | Runtime introspection (port, start time) |
| `internal/server` | Chi router construction, middleware (session auth, CORS, remote-IP guard) |
| `internal/skills` | Built-in skill registry — weather, web, filesystem, system, terminal, applescript, finance, image, diary, browser, vault, gremlin, websearch, forge, info |
| `internal/storage` | SQLite — conversations, messages, memories, gremlin runs, deferred executions |
| `internal/validate` | API validation gate — pre-flight, live execution (max 2 attempts), audit |

---

## Dependency Rules

```
config, creds   ←  everything may read these
storage         ←  chat, features, modules, control
agent           ←  chat, forge
skills          ←  chat, forge, modules/skills
features        ←  modules for JSON-backed feature persistence
platform        ←  cmd/atlas-runtime, internal modules
modules/*       ←  platform contracts + feature/package dependencies they own
domain/*        ←  server (core-owned route registration only)
server          ←  cmd/atlas-runtime
```

Core rule:
- `internal/platform` must not import product modules
- extracted feature routes should live in `internal/modules/*`, not reappear in `internal/domain`

---

## Key Files — Quick Reference

| File | Role |
| --- | --- |
| `cmd/atlas-runtime/main.go` | Entry point — all service construction and wiring |
| `internal/platform/registry.go` | Private module registration and lifecycle ordering |
| `internal/platform/host.go` | Module host — route mounts, storage, agent runtime, event bus |
| `internal/agent/loop.go` | Single-turn agent execution loop |
| `internal/agent/provider.go` | AI provider dispatch (OpenAI/Anthropic/Gemini/LM Studio) |
| `internal/chat/service.go` | `HandleMessage`, `RegenerateMind`, `ResolveProvider`, `Resume` |
| `internal/chat/keychain.go` | `resolveProvider` — builds `ProviderConfig` from config + Keychain |
| `internal/config/snapshot.go` | `RuntimeConfigSnapshot` — all runtime config fields |
| `internal/features/skills.go` | `builtInSkills()` catalog, `ListSkills`, `SetSkillState`, `SetForgeSkillState` |
| `internal/modules/communications/module.go` | Communications module — routes + bridge lifecycle |
| `internal/modules/forge/module.go` | Forge module — proposal/install routes |
| `internal/modules/workflows/module.go` | Workflows module — definitions + run routes |
| `internal/modules/dashboards/module.go` | Dashboards module — lifecycle, wiring, dependency injection |
| `internal/modules/dashboards/routes.go` | HTTP handlers: list, get, delete, resolve, refresh, SSE events |
| `internal/modules/dashboards/skills.go` | `dashboard.*` skill family — list/get/create/update/delete/add_data_source/add_widget/publish |
| `internal/modules/dashboards/safety.go` | Runtime endpoint allowlist, web SSRF guard, SQL lexer |
| `internal/modules/dashboards/resolve.go` | `resolveSource` dispatcher + shared resolver types |
| `internal/modules/dashboards/resolve_skill.go` | Skill source resolver — calls registry, prefers `Artifacts` over `Summary` |
| `internal/modules/dashboards/resolve_runtime.go` | Runtime loopback source resolver |
| `internal/modules/dashboards/resolve_sql.go` | Read-only SQL source resolver |
| `internal/modules/dashboards/resolve_chat.go` | Chat analytics source resolver |
| `internal/modules/dashboards/resolve_gremlin.go` | Gremlin run history source resolver |
| `internal/modules/dashboards/resolve_live.go` | Live-compute source resolver (dispatches to `LiveComputeRunner`) |
| `internal/modules/dashboards/live_runner.go` | `AILiveComputeRunner` — calls AI provider, strips fences, parses JSON |
| `internal/modules/dashboards/refresh.go` | SSE coordinator — fan-out, per-dashboard subscriptions, cache replay |
| `internal/modules/dashboards/compile.go` | TSX/esbuild compilation for custom widget authoring |
| `internal/modules/dashboards/pack.go` | Dashboard pack/unpack helpers |
| `internal/modules/dashboards/store.go` | JSON persistence for dashboard definitions |
| `internal/modules/dashboards/types.go` | All shared types: `Dashboard`, `Widget`, `DataSource`, source kind constants |
| `internal/modules/skills/module.go` | Skills module — skills routes + fs roots |
| `internal/modules/agents/module.go` | Agents module — DB-first CRUD routes, Team HQ snapshot, task dispatch, approval/cancel, sync/export |
| `internal/modules/agents/agent_actions.go` | `team.*`/`agent.*` skills — delegate (single/sequence), create, update, delete, enable/disable, sequence |
| `internal/modules/agents/prompt.go` | `composeWorkerPrompt` — four-section layered prompt with five template role contracts |
| `internal/modules/agents/agents_file.go` | AGENTS.md parse/render — used only by `POST /agents/sync` and `GET /agents/export` |
| `internal/skills/registry.go` | `NewRegistry` — registers all built-in skills |
| `internal/forge/service.go` | `Propose` — AI research pipeline, in-memory researching list |
| `internal/forge/store.go` | `forge-proposals.json` and `forge-installed.json` persistence |
| `internal/storage/db.go` | All SQLite queries; `Conn() *sql.DB` exposes the raw handle for read-only consumers |
| `internal/validate/gate.go` | `Gate.Run` — 3-phase API validation |
| `internal/memory/extractor.go` | `ExtractAndPersist` — two-stage memory extraction (regex + LLM) after each turn |
| `internal/mind/reflection.go` | `ReflectNonBlocking` — two-tier MIND.md update after each turn |
| `internal/mind/skills.go` | `LearnFromTurnNonBlocking` — SKILLS.md routine learning |
| `internal/mind/dream.go` | `StartDreamCycle` — 5-phase nightly consolidation (prune, merge, tool synthesis, diary synthesis, MIND refresh) |
| `internal/features/diary.go` | `AppendDiaryEntry`, `ReadDiary`, `DiaryContext` — DIARY.md R/W |
| `internal/logstore/sink.go` | `logstore.Write` — global log sink, read by `GET /logs` |

---

## Skill Classification

Skills fall into three categories. The category determines where new capabilities belong.

| Category | Description | Location | Deploy |
| --- | --- | --- | --- |
| **Core built-in** | Requires Atlas internals: shared process state, SQLite, SSE broadcaster, Keychain, or go-rod Chrome. Cannot be decoupled without significant rework. | `internal/skills/` compiled into binary | `make install` + daemon restart |
| **Standard built-in** | Self-contained API calls or system operations compiled in for convenience. Could theoretically be custom skills but migration is low-ROI. Leave them as-is unless there is a specific reason to change them. | `internal/skills/` compiled into binary | `make install` + daemon restart |
| **Custom skill** | User-installed or third-party capability. An executable (any language) in its own directory. Atlas calls it via subprocess (stdin/stdout JSON). No recompile needed. | `~/Library/Application Support/ProjectAtlas/skills/<id>/` | Drop folder + daemon restart |

**Core built-in skills (must stay compiled-in):**
`browser.*` — shared go-rod Chrome process · `vault.*` — Keychain + internal creds · `gremlin.*` — SQLite + GREMLINS.md · `forge.*` — internal forge service · `atlas.*` / `info.*` — self-introspection · `diary.*` — internal diary.go integration

**Standard built-in skills (compiled-in, leave as-is):**
`weather.*` · `web.*` · `websearch.*` · `fs.*` · `system.*` · `terminal.*` · `applescript.*` · `finance.*` · `image.*`

**Decision rule for new skills:** Does it need direct access to a Go struct, the SQLite DB, the SSE broadcaster, or a shared process? → Core built-in. Is it a third-party API, personal automation, or domain-specific tool? → Custom skill.

---

## Custom Skills

Custom skills are user-installed executables that Atlas calls via subprocess. They appear in `GET /skills` alongside built-ins with `"source": "custom"`. From the model's perspective there is no difference.

**Directory layout:**
```
~/Library/Application Support/ProjectAtlas/skills/
  jira/
    skill.json     ← manifest
    run            ← executable (chmod +x, any language)
  github/
    skill.json
    run
```

**`skill.json` manifest:**
```json
{
  "id": "jira",
  "name": "Jira",
  "version": "1.0.0",
  "description": "Search and create Jira issues",
  "author": "Your Name",
  "actions": [
    {
      "name": "search",
      "description": "Search Jira issues by JQL query",
      "permission_level": "read",
      "action_class": "read",
      "parameters": {
        "type": "object",
        "properties": {
          "query": { "type": "string", "description": "JQL query string" }
        },
        "required": ["query"]
      }
    }
  ]
}
```

Actions register as `<id>.<name>` — e.g. `jira.search`. This matches the built-in naming convention (`weather.current`, `browser.navigate`).

**Subprocess protocol — one JSON line in, one JSON line out:**
```
stdin:  {"action": "search", "args": {"query": "bug priority=high"}}
stdout: {"success": true, "output": "Found 12 issues: ..."}
stdout: {"success": false, "error": "connection refused"}   ← on error
```

- Process is spawned fresh per call, killed after 30s timeout (same as built-ins)
- Working directory is the plugin's own folder — relative paths work
- Environment variables pass through — credentials can live in `.env` or the OS keychain
- `action_class` in the manifest is respected by the approval system — declaring `external_side_effect` triggers normal approval flow
- Output is size-limited to 1 MB before passing to the model

**HTTP routes (planned — not yet implemented):**
```
GET    /skills/custom          — list installed custom skills
POST   /skills/install         — install from local path or URL
DELETE /skills/:id             — remove custom skill directory
```

---

## Where to Add Things

| Task | File(s) to edit |
| --- | --- |
| New **core** built-in skill | Create `internal/skills/<name>.go` + call `r.register<Name>()` in `NewRegistry` + add to `builtInSkills()` in `internal/features/skills.go` |
| New team agent skill action | Add to `internal/modules/agents/agent_actions.go` + register in `registerSkills()` in `module.go` |
| New **custom** skill | Create `~/...ProjectAtlas/skills/<id>/skill.json` + `run` executable. Appears automatically after daemon restart. |
| New core-owned HTTP route | Add handler to appropriate `internal/domain/<file>.go` + register in `Register(r chi.Router)` |
| New feature HTTP route | Prefer a new or existing `internal/modules/<feature>/module.go`; mount via `Register(host)` |
| New config field | `internal/config/snapshot.go` + `Defaults()` function |
| New credential field | `internal/creds/bundle.go` `Bundle` struct + update `domain/control.go` `storeAPIKey` mapping |
| New web UI screen | `atlas-web/src/screens/<Name>.tsx` + route in `atlas-web/src/App.tsx` + types/methods in `atlas-web/src/api/contracts.ts` + `atlas-web/src/api/client.ts` |
| New Forge skill type | `internal/forge/types.go` |
| New dashboard widget kind | `atlas-web/src/screens/DashboardWidgets.tsx` (renderer) + add the constant to `internal/modules/dashboards/types.go` |
| New dashboard runtime data source | Add the path prefix to `runtimeEndpointAllowlist` in `internal/modules/dashboards/safety.go` |
| New dashboard skill data source | Only `ActionClassRead` skills are permitted; validation happens at `dashboard.add_data_source` time via `skills.Registry.HasAction` + `GetActionClass` in `skills.go` |
| New storage table | `internal/storage/db.go` `createSchema()` + add query methods |
| Add a log entry | Call `logstore.Write(level, message, meta)` — visible at `GET /logs` |
| Extend diary context | `internal/features/diary.go` — `DiaryContext` is injected into system prompt by `chat/service.go` |
| Add a memory category | `internal/memory/extractor.go` — add extractor function + call in `extractCandidates()` |
| Change memory recall behavior | `internal/storage/db.go` — `RelevantMemories()` controls BM25 scoring + commitment boost |
| Add a dream cycle phase | `internal/mind/dream.go` — add `phaseXxx()` function + call in `runDreamCycle()` |

---

## Build Commands

```bash
# Go runtime
cd Atlas/atlas-runtime
go mod tidy && go build ./...              # verify — clean = no output
go vet ./...                               # linter — clean = no output
go build -o Atlas ./cmd/atlas-runtime      # build binary
./Atlas -port 1984 -web-dir ../atlas-web/dist   # run locally (dev)

# Web UI
cd Atlas/atlas-web
npm install                                # first time only
npm run dev                                # dev server (hot reload)
npm run build                              # production build → dist/

# Install everything as daemon + deploy (from Atlas/atlas-runtime)
make install                               # build all, deploy, load launchd daemon
make daemon-start / daemon-stop / daemon-restart
make daemon-status                         # launchctl print — PID, state, exit code
make daemon-logs                           # tail -f ~/Library/Logs/Atlas/runtime.log
make uninstall                             # unload daemon, remove installed files
```

**Installed locations:**
- Runtime daemon: `~/Library/Application Support/Atlas/Atlas` (label: `Atlas`, plist: `~/Library/LaunchAgents/Atlas.plist`)
- Web assets: `~/Library/Application Support/Atlas/web/`
- Logs: `~/Library/Logs/Atlas/runtime.log` + `runtime-error.log`

---

## Skill Naming & Calling Conventions

This is the single authoritative reference. Do not invent new patterns.

### Action ID format

```
namespace.action_name
```

- **Separator**: dot (`.`). Always.
- **Namespace**: lowercase, short, matches the skill family (`fs`, `weather`, `team`, `dashboard`).
- **Action name**: lowercase, underscores for multi-word (`read_file`, `list_channels`).
- Examples: `fs.read_file`, `weather.current`, `team.delegate`, `dashboard.create`.

### Wire encoding (transparent — do not handle manually)

OpenAI function names cannot contain dots. The registry encodes them automatically:

| Layer | Format | Example |
|-------|--------|---------|
| Internal (Go) | `namespace.action` | `weather.current` |
| AI wire (OpenAI) | `namespace__action` | `weather__current` |
| AI wire (Anthropic/Gemini) | `namespace.action` | `weather.current` (no change) |

Encoding/decoding lives entirely in `internal/skills/registry.go` (`oaiName` / `fromOAIName`). Never reference `__` names in Go code — always use the dot form.

### Registering a new action

```go
// In internal/skills/<name>.go
r.register(skills.SkillEntry{
    Def: skills.ToolDef{
        Name:        "mynamespace.do_thing",   // ← dot-separated, lowercase
        Description: "...",
    },
    ActionClass: skills.ActionClassRead,
    FnResult: func(ctx context.Context, raw json.RawMessage) (skills.ToolResult, error) { ... },
})
```

### Pattern matching — always use the canonical function

**Never** write `strings.HasPrefix(actionID, pattern)` — it cannot handle the four accepted forms and will produce false positives (e.g. `"fs"` matching `"filesystem.check"`).

**Always use:**
```go
// Single canonical matcher — internal/skills/registry.go
skills.MatchesAnyPattern(actionID, patterns []string) bool

// Single canonical filter — returns a sub-registry with only matching actions
registry.FilteredByPatterns(patterns []string) *skills.Registry
```

### AllowedSkills / AllowedToolPrefixes — four accepted forms, one canonical

These fields (teams `allowedSkills`, workflow `allowedToolPrefixes`, `ToolPolicy.AllowedToolPrefixes`) all accept the same four forms:

| Form | Example | Matches |
|------|---------|---------|
| Bare prefix (**canonical**) | `"fs"` | `fs.read_file`, `fs.write_file`, … |
| Dot suffix | `"fs."` | same — normalized to bare prefix on save |
| Wildcard | `"fs.*"` | same — normalized to bare prefix on save |
| Exact action | `"fs.read_file"` | only that one action |

**Canonical form is bare prefix.** Normalization (`normalizeSkillPattern` in `agents_file.go`) converts `"fs.*"` and `"fs."` → `"fs"` at write time. Exact action IDs (`"fs.read_file"`) are preserved as-is.

Key guarantee: `"fs"` does **not** match `"filesystem.check"`. The bare-prefix match requires a dot boundary: `strings.HasPrefix(actionID, pattern+".")`.

### Aliases

Some skills expose the same action under two names. Aliases are transparent to callers — `MatchesAnyPattern` and `FilteredByPatterns` handle them automatically. Do not duplicate the alias logic anywhere else.

---

## Critical Conventions

**Skills**
- Register in `internal/skills/registry.go` `NewRegistry()` + list in `internal/features/skills.go` `builtInSkills()`.
- Permission levels: `"read"` (auto-approve), `"draft"` (requires approval), `"execute"` (requires approval unless policy overrides).
- Read-only credential fetches in skill `Fn` bodies — call `creds.Read()` inline; don't cache.

**HTTP routes**
- All routes live inside the `RequireSession` middleware group in `server/router.go` except `/auth/*` and `/web/*`.
- Localhost requests (no `Origin` header) bypass session auth — same as Swift runtime.
- SSE streams set `Content-Type: text/event-stream` and flush on every write.
- 204 No Content is valid — web client handles empty bodies.

**Keychain**
- Credential bundle: `security find-generic-password -s com.projectatlas.credentials -a bundle -w`
- All secrets are in one JSON bundle read by `internal/creds/bundle.go`.
- Custom/third-party keys live under `customSecrets` in the bundle — no code change needed.

**Config**
- Shared file: `~/Library/Application Support/ProjectAtlas/config.json` (`RuntimeConfigSnapshot`).
- Go-only sidecar: `~/Library/Application Support/ProjectAtlas/go-runtime-config.json` (`GoRuntimeConfig`).
- Atomic writes: temp file → rename. Never write directly to the config path.

**Forge**
- `POST /forge/proposals` runs AI research in a background goroutine; returns 202 immediately with a `ResearchingItem`.
- Proposals persist in `forge-proposals.json`; installed skills in `forge-installed.json`.
- `forge.BuildInstalledRecord(proposal)` converts a proposal into the `SkillRecord` shape for `GET /forge/installed`.

**Approvals**
- `POST /approvals/{toolCallID}/approve` takes the tool call ID, not the approval record ID.
- Approval resolution calls `chatSvc.Resume(toolCallID, approved)` in a goroutine.

- Mutating routes (POST create/install/reject/pin/access/widgets/execute) return 501 — **deferred to V1.0 rewrite**.

**Agents (Teams V1 — DB-first)**
- SQLite is the authoritative source for agent definitions. AGENTS.md is export-only (`GET /agents/export`). There is no startup import from AGENTS.md — the file is never read automatically. `POST /agents/sync` exists for manual one-off imports only. DB is the sole authority at all times.
- `allowedSkills` entries must be bare-prefix canonical form: `["fs", "terminal", "websearch"]`. See the Skill Naming conventions above.
- Canonical skill names: `team.list`, `team.get`, `team.delegate` for read/delegation; `agent.create`, `agent.update`, `agent.delete`, `agent.enable`, `agent.disable`, `agent.pause`, `agent.resume`, `agent.sequence`, `agent.assign` for management. `agent.list` and `agent.get` were removed in Teams V1.
- HTTP routes are under `/agents`: `/agents`, `/agents/{id}`, `/agents/hq`, `/agents/tasks`, `/agents/tasks/{id}`, `/agents/events`, `/agents/sync`, `/agents/export`, `/agents/triggers`.
- `team.delegate` accepts a structured `DelegationPlan` with `pattern` (`single`/`sequence`), `executionMode` (`sync_assist`/`async_assignment`), and per-task specs. Backward-compat flat `{agentID, task}` is still accepted.
- `pattern: "parallel"` is defined in types but rejected at validation time with a clear error — not yet implemented.
- `async_assignment` pre-generates the task ID before spawning the goroutine so the caller can return it immediately. On completion, the goroutine calls `chat.AsyncFollowUpSender` with the originating Atlas `convID` (injected via `chat.WithOriginConvID` in `HandleMessage`) to push a completion message to the chat.
- Sequence delegation preserves full `DelegationTaskSpec` metadata per step — not flattened to a bare task string.
- Worker prompts are built by `composeWorkerPrompt()` in `prompt.go`: four sections (Identity, Assignment, Context, Execution contract) with five template role contracts. `TemplateRole` is now populated on all create/update/sync paths via `templateRoleFromRole()` in `module.go` — all workers get role-specific prompt contracts.
- Task delegation runs in a goroutine (async) or blocks (sync). Poll `GET /agents/tasks/{id}` for async status.
- `normalizeSkillPattern` in `defs.go` and `MatchesAnyPattern` in `registry.go` must agree — if you change one, update both and their tests.
- Shared type/helper definitions live in `defs.go` (agentDefinition, normalizeDefinition, validateDefinition, slugID, etc.). `agents_file.go` handles only AGENTS.md parse/render I/O.

**Communications**
- `GET /communications`, `GET /communications/channels`, `PUT /communications/platforms/:platform`, `POST /communications/platforms/:platform/validate`.
- Telegram and Discord platform lifecycle managed by `internal/comms/`.

---

## Data Files (Application Support)

| File | Written by | Purpose |
| --- | --- | --- |
| `config.json` | Go runtime + web UI | `RuntimeConfigSnapshot` |
| `go-runtime-config.json` | Go runtime | Go-only sidecar config |
| `atlas.sqlite3` | Go runtime | Conversations, messages, memories, gremlin runs |
| `MIND.md` | User / AI / mind.reflection | System prompt for the agent — updated each turn by the reflection pipeline |
| `SKILLS.md` | User / AI / mind.skills | Skills-layer memory — learned routines written after repeated tool sequences |
| `DIARY.md` | Go runtime / diary.record | Per-day diary entries (max 3/day) |
| `dream-state.json` | Go runtime | Last successful dream cycle timestamp — catch-up detection at startup |
| `GREMLINS.md` | User / web UI | Automation definitions |
| `workflow-definitions.json` | Web UI | Workflow definitions |
| `workflow-runs.json` | Go runtime | Workflow run records |
| `forge-proposals.json` | Go runtime | Forge proposal records |
| `forge-installed.json` | Go runtime | Installed forge skill records |
| `dashboards.json` | Go runtime | Saved dashboard definitions (atomic temp+rename writes) |
| `go-skill-states.json` | Go runtime | Skill enable/disable overrides |
| `action-policies.json` | Web UI / approvals | Per-action approval policies |
| `fs-roots.json` | Web UI | Approved filesystem roots |
| `api-validation-history.json` | Validate gate | API validation audit log |
| `AGENTS.md` | User / `POST /agents/sync` | Legacy import format — no longer read on startup. Use `GET /agents/export` to regenerate it from the DB. |

All files live in `~/Library/Application Support/ProjectAtlas/`.
