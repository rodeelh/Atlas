# Atlas Architecture

**Last updated: 2026-04-18** · Custom Skills guide: [`docs/custom-skills.md`](custom-skills.md) · Internal modules: [`docs/internal-modules.md`](internal-modules.md) · Agent boundary: [`docs/agent-boundary.md`](agent-boundary.md) · Migration verification: [`docs/migration-verification.md`](migration-verification.md) · Manual smoke: [`docs/manual-smoke-checklist.md`](manual-smoke-checklist.md) · Teams spec: [`docs/teams-v1-implementation-spec.md`](teams-v1-implementation-spec.md)

Atlas is a local AI operator. A Go binary runs as a launchd daemon (`Atlas`), serves a web UI, and connects to any supported AI provider. No Swift required.

Note: the package currently named `internal/chat` is architecturally the Atlas **Agent** subsystem. See [`docs/agent-boundary.md`](agent-boundary.md).

Atlas Teams V1 is implemented. The architectural rule that governs it:
**Agent owns delegation decisions. Teams owns delegated execution.**

---

## 1. Project Structure

```
Atlas/
├── atlas-runtime/                      # Go runtime — the product backend
│   ├── cmd/atlas-runtime/
│   │   └── main.go                     # Entry point — flags, wiring, http.ListenAndServe
│   ├── com.atlas.runtime.plist.tmpl    # launchd LaunchAgent template
│   ├── Makefile                        # build, install, daemon-*, daemon-logs
│   └── internal/
│       ├── agent/
│       │   ├── loop.go                 # Multi-turn agent execution loop (FSM: stream → tool-exec → upgrade → next-turn)
│       │   ├── provider.go             # Non-streaming helpers, message conversion, shared HTTP plumbing
│       │   ├── adapter.go              # ProviderAdapter interface + TurnEvent/TurnRequest types + parseProviderErrorBody
│       │   ├── adapter_factory.go      # NewAdapter(ProviderConfig) → concrete adapter
│       │   ├── adapter_oaicompat.go    # OAI-compat adapter (Gemini, OpenRouter, LMStudio, Ollama, AtlasEngine)
│       │   ├── adapter_openai.go       # OpenAI Responses API adapter (stream via SSE)
│       │   ├── adapter_anthropic.go    # Anthropic Messages API adapter (stream via SSE + prompt caching)
│       │   ├── adapter_mlx.go          # Atlas MLX adapter (stream → non-stream fallback, 503 retry loop)
│       │   ├── adapter_local.go        # Local non-streaming adapter (LMStudio/Ollama coalesce path)
│       │   ├── embed.go                # Embed(ctx, provider, text) dispatcher + sidecar URL override (atomic.Pointer)
│       │   ├── selector.go             # ToolSelector interface + IdentitySelector (zero-value default)
│       │   └── stream/                 # stdlib-only SSE parsing primitives (no agent imports)
│       │       ├── types.go            # Chunk, ToolDelta, StreamUsage
│       │       ├── sse.go              # Scanner — bufio wrapper, strips data: prefix, stops at [DONE]
│       │       ├── tool_assembly.go    # Assembler — accumulates ToolDelta fragments by index
│       │       └── oaicompat.go        # ParseOAICompatStream(ctx, r) <-chan Chunk
│       ├── auth/
│       │   ├── service.go              # HMAC-SHA256 session tokens, bootstrap, middleware
│       │   └── ratelimit.go            # Per-IP rate limiting
│       ├── browser/
│       │   ├── manager.go              # BrowserManager — singleton Chrome process via go-rod
│       │   ├── loginwall.go            # Login wall heuristics (URL, title, DOM)
│       │   └── twofa.go                # 2FA challenge detection
│       ├── chat/
│       │   ├── service.go              # HandleMessage, RegenerateMind, ResolveProvider, Resume; selectTurnTools
│       │   ├── pipeline.go             # Pipeline — ordered turn stages: buildInput→selectTools→execute→postTurn
│       │   ├── hooks.go                # HookRegistry — post-turn TurnHook adapter (memory, mind, skills learning)
│       │   ├── selector.go             # NewSelector factory + scopedSelector (policy-filtered "off" mode)
│       │   ├── selector_lazy.go        # lazySelector — smart/lazy mode: LLM router → stage-1 short → stage-2 broad
│       │   ├── selector_llm.go         # llmSelector — full LLM routing for explicit "llm" mode
│       │   ├── selector_heuristic.go   # heuristicSelector — signal-scoring SelectiveToolDefs
│       │   ├── tool_router.go          # selectToolsWithLLM — background provider call + capability-group manifest
│       │   ├── broadcaster.go          # SSE fan-out to connected clients
│       │   ├── keychain.go             # resolveProvider — config + Keychain → ProviderConfig
│       │   ├── thought_surfacing.go    # [T-NN] marker detection → pending surfacing records
│       │   ├── classifier.go           # Post-turn engagement classifier (like/neutral/dislike)
│       │   └── greeting.go             # HandleGreeting — drain pending-greetings queue → model call → SSE
│       ├── comms/
│       │   ├── service.go              # Platform lifecycle, channel management
│       │   ├── keychain.go             # Comms credential reads
│       │   ├── validate.go             # Platform credential validation
│       │   ├── telegram/               # Telegram long-poll bridge
│       │   ├── discord/                # Discord gateway bridge
│       │   ├── whatsapp/               # WhatsApp bridge (self-chat scoped)
│       │   └── slack/                  # Slack bridge (stub)
│       ├── config/
│       │   ├── snapshot.go             # RuntimeConfigSnapshot (shared with web UI)
│       │   ├── store.go                # Atomic JSON read/write with in-process cache
│       │   ├── paths.go                # SupportDir, DBPath, ConfigPath
│       │   └── goconfig.go             # Go-only sidecar config (BrowserShowWindow, etc.)
│       ├── creds/
│       │   ├── bundle.go               # Keychain API-key bundle reader (security CLI)
│       │   └── vault.go                # Agent credential vault (separate Keychain item)
│       ├── domain/
│       │   ├── auth.go                 # /auth/* routes
│       │   ├── chat.go                 # /message, /conversations, /memories, /mind, /skills-memory
│       │   ├── control.go              # /status, /config, /api-keys, /link-preview, /models
│       │   ├── approvals.go            # /approvals, /action-policies
│       │   ├── communications.go       # /communications, /channels, /platforms
│       │   ├── handler.go              # Handler interface
│       │   └── helpers.go              # writeJSON, writeError, decodeJSON
│       ├── features/
│       │   ├── automations.go          # GREMLINS.md parse/append/update/delete, gremlin runs
│       │   ├── diary.go                # Diary entry persistence
│       │   ├── files.go                # Workflow JSON persistence helpers
│       │   ├── skills.go               # builtInSkills catalog, ListSkills, SetSkillState
│       ├── modules/
│       │   ├── approvals/              # Private module — approvals routes + resolution
│       │   ├── automations/            # Private module — automation routes + execution
│       │   ├── communications/         # Private module — comms routes + bridge lifecycle
│       │   ├── forge/                  # Private module — forge proposal/install flows
│       │   ├── workflows/              # Private module — workflow routes + runs
│       │   ├── skills/                 # Private module — skills routes + fs roots
│       │   ├── engine/                 # Private module — engine control routes
│       │   ├── usage/                  # Private module — usage reporting routes
│       │   ├── apivalidation/          # Private module — API validation history routes
│       │   ├── mind/                   # Private module — mind-thoughts HTTP surface (/mind/*)
│       │   ├── dashboards/             # Private module — schema-driven widget grid, live SSE refresh, AI authoring via dashboard.* skills
│       │   │   ├── module.go           #   Lifecycle, wiring, dependency injection (SetDatabase, SetSkillExecutor, SetProviderResolver)
│       │   │   ├── routes.go           #   HTTP handlers: GET /dashboards, GET/DELETE /{id}, POST /{id}/resolve, POST /{id}/refresh, GET /{id}/events
│       │   │   ├── skills.go           #   dashboard.* skill family — list/get/create/update/delete/add_data_source/add_widget/publish
│       │   │   ├── safety.go           #   Runtime endpoint allowlist, web SSRF guard, SQL lexer
│       │   │   ├── resolve.go          #   resolveSource dispatcher + shared resolver deps/types
│       │   │   ├── resolve_skill.go    #   Skill resolver — calls registry, prefers Artifacts over Summary
│       │   │   ├── resolve_runtime.go  #   Runtime loopback resolver
│       │   │   ├── resolve_sql.go      #   Read-only SQL resolver
│       │   │   ├── resolve_chat.go     #   Chat analytics resolver
│       │   │   ├── resolve_gremlin.go  #   Gremlin run history resolver
│       │   │   ├── resolve_live.go     #   live_compute resolver (dispatches to LiveComputeRunner)
│       │   │   ├── live_runner.go      #   AILiveComputeRunner — calls AI provider, strips fences, parses JSON
│       │   │   ├── refresh.go          #   SSE coordinator — fan-out, per-dashboard subscriptions, cache replay
│       │   │   ├── compile.go          #   TSX/esbuild compilation for custom widget authoring
│       │   │   ├── pack.go             #   Dashboard pack/unpack helpers
│       │   │   ├── store.go            #   JSON persistence (atomic temp+rename writes)
│       │   │   └── types.go            #   Dashboard, Widget, DataSource, source kind constants
│       │   └── agents/                 # Private module — Atlas Teams V1: DB-first agent registry, delegation engine, task orchestration
│       │       ├── module.go           #   Routes, DB-first CRUD, Team HQ snapshot, approval/cancel, sync/export, trigger coordinator
│       │       ├── agent_actions.go    #   team.*/agent.* skills — delegate (single/sequence), create/update/delete/enable/disable
│       │       ├── prompt.go           #   composeWorkerPrompt — three-layer prompt (identity→assignment→context→contract)
│       │       └── agents_file.go      #   AGENTS.md parse/render — used by POST /agents/sync and GET /agents/export only
│       ├── platform/
│       │   ├── host.go                 # Private module host + route mounts
│       │   ├── module.go               # Internal module contract
│       │   ├── registry.go             # Module registration + lifecycle ordering
│       │   ├── bus.go                  # Selective in-process event bus
│       │   ├── storage.go              # Scoped storage contracts for modules
│       │   ├── agent.go                # Agent runtime contract
│       │   └── context.go              # Context assembly seam
│       ├── forge/
│       │   ├── types.go                # ForgeProposal, ResearchingItem, ProposeRequest
│       │   ├── store.go                # forge-proposals.json + forge-installed.json
│       │   ├── service.go              # AI research pipeline, in-memory researching list
│       │   └── codegen.go              # GenerateAndInstallCustomSkill — skill.json + Python run script
│       ├── logstore/
│       │   ├── sink.go                 # In-memory ring buffer (500 entries) — backs GET /logs
│       │   └── action_log.go           # ActionLogEntry type, WriteAction helper
│       ├── memory/
│       │   ├── extractor.go            # Per-turn memory extraction: Stage 1 (regex) + Stage 2 (LLM) + Stage 3 (entities)
│       │   ├── llm.go                  # extractWithLLM (memory candidates) + extractEntitiesNonBlocking (entity graph)
│       │   └── recall.go               # HyDEVector — hypothetical document embedding for semantic recall
│       ├── mind/
│       │   ├── reflection.go           # Two-tier MIND.md pipeline (Today's Read + deep reflect)
│       │   ├── skills.go               # SKILLS.md learned-routine detection + selective injection
│       │   ├── seed.go                 # First-run seeding of MIND.md and SKILLS.md
│       │   ├── types.go                # TurnRecord, SkillLine — reflection + nap inputs
│       │   ├── util.go                 # atomicWrite, truncate helpers, content validators
│       │   ├── nap.go                  # RunNap — nap execution, NapDeps, NapResult
│       │   ├── nap_prompt.go           # buildThoughtsBlock — prompt engineering for nap calls
│       │   ├── nap_scheduler.go        # Scheduler — idle + floor triggers, NotifyTurnNonBlocking
│       │   ├── dispatcher.go           # Dispatcher — auto-execute gate, propose-to-approval flow
│       │   ├── thoughts_section.go     # ReadThoughtsSection / UpdateThoughtsSection MIND.md I/O
│       │   ├── config_sync.go          # applyConfigToThoughtsImpl — bridge config → thoughts pkg vars
│       │   ├── lock.go                 # nap-lock.json advisory file lock (cross-process)
│       │   ├── thoughts/               # Thought data model, Apply engine, engagement sidecar
│       │   │   ├── thought.go          # Thought struct, ActionClass, score formula
│       │   │   ├── apply.go            # Apply — OpAdd/OpReinforce/OpDiscard batch apply
│       │   │   └── engagement.go       # engagement sidecar R/W (thought-engagement.jsonl)
│       │   └── telemetry/              # Mind telemetry emitter + Aggregate stats
│       │       ├── emitter.go          # Emitter.Emit → storage.SaveMindTelemetry
│       │       └── aggregate.go        # Aggregate — counts by kind for dashboard widgets
│       ├── runtime/
│       │   └── service.go              # RuntimeStatus (port, started_at, version)
│       ├── server/
│       │   └── router.go               # BuildRouter — chi, CORS, RequireSession, /web static
│       ├── customskills/
│       │   └── manifest.go             # CustomSkillManifest types + filesystem scanning (leaf pkg)
│       ├── skills/
│       │   ├── registry.go             # Registry, ToolDef (RawSchema), SkillEntry, IsStateful()
│       │   ├── custom.go               # LoadCustomSkills — subprocess executor, 30s timeout
│       │   ├── weather.go              # weather.*
│       │   ├── web.go                  # web.*
│       │   ├── websearch.go            # websearch.query (Brave Search)
│       │   ├── filesystem.go           # fs.*
│       │   ├── system.go               # system.*
│       │   ├── terminal.go             # terminal.*
│       │   ├── applescript.go          # applescript.*
│       │   ├── finance.go              # finance.*
│       │   ├── image.go                # image.generate (DALL-E 3)
│       │   ├── diary.go                # diary.*
│       │   ├── browser.go              # browser.* (27 actions, stateful — serialised)
│       │   ├── vault.go                # vault.* (6 actions)
│       │   ├── gremlin.go              # gremlin.*
│       │   ├── forge_skill.go          # forge.*
│       │   └── info.go                 # atlas.info
│       ├── storage/
│       │   └── db.go                   # SQLite — all tables, all queries
│       └── validate/
│           ├── types.go                # ValidationRequest/Result/AuditRecord
│           ├── catalog.go              # Built-in example catalog
│           ├── inspector.go            # HTTP response confidence scoring
│           ├── audit.go                # api-validation-history.json
│           └── gate.go                 # Gate.Run — 3-phase validation
│
└── atlas-web/                          # Preact + TypeScript web UI
    └── src/
        ├── screens/                    # Chat, Forge, Skills, Approvals,
        │                               #   Memory, Automations, Workflows, Comms, Settings
        ├── api/
        │   ├── client.ts               # Typed HTTP client
        │   └── contracts.ts            # Shared TypeScript types
        ├── theme.ts                    # CSS custom-property theme engine
        ├── App.tsx                     # Root — sidebar nav, screen routing
        └── styles.css
```

---

## 2. System Diagram

```
[User — Browser at localhost:1984/web]
        │  HTTP / SSE
        ▼
[Atlas Go Binary — single process, port 1984]
   │
   ├── /auth/*          Auth            HMAC session tokens, bootstrap
   ├── /status, /config Control         Runtime state, config R/W, API keys
   ├── /message, /…     Chat            Agent loop, SSE streaming, memories
   ├── /approvals, /…   Approvals       Approval queue, action-policies
   ├── /communications  Comms           Telegram / Discord / WhatsApp platform management
   └── /skills, /forge, Modules         Private module-backed feature surfaces
       /automations, /team, …
            │
            ├── internal/agent      ← OpenAI (Responses API) / Anthropic / Gemini / OpenRouter / LM Studio / Ollama / Atlas Engine / Atlas MLX
            ├── internal/skills     ← 16 built-in skill groups, 90+ actions + custom skills
            ├── internal/customskills ← manifest types + filesystem scanning (leaf pkg)
            ├── internal/browser    ← Headless Chrome via go-rod
            ├── internal/platform   ← Private host, module registry, event bus, scoped storage
            ├── internal/modules    ← First-party feature modules (incl. teams/)
            ├── internal/forge      ← Forge research pipeline
            ├── internal/validate   ← API validation gate
            ├── internal/mind       ← MIND.md reflection + SKILLS.md learning
            ├── internal/logstore   ← In-memory log ring buffer (GET /logs)
            └── internal/storage    ← SQLite
```

---

## 3. Agent Loop

### 3.1 Turn pipeline (`internal/chat/pipeline.go`)

`HandleMessage` runs a `Pipeline` — an ordered sequence of named stages. Each stage reads from and writes to a `TurnState` struct:

```
HandleMessage(req)
    │
    ▼
Pipeline.Run(ctx, req)
    ├── buildInput     — build OAI message array (system + history + user); resolve CapabilityPlan
    ├── selectTools    — selectTurnTools: resolve mode + NewSelector → sel.Initial() → SelectedTools
    ├── execute        — agent.Loop.Run (streaming FSM, see §3.2)
    └── postTurn       — HookRegistry: memory extraction, MIND reflection, skills learning (non-blocking)
    │
    ▼
Pipeline.BuildResponse → MessageResponse (text, tool summaries, usage, etc.)
```

### 3.2 Agent loop FSM (`internal/agent/loop.go`)

Each iteration of `Loop.Run` is a single streaming call through a `ProviderAdapter`:

```
Loop.Run(ctx, LoopConfig, messages, convID)
    │
    ├── if Selector == nil → IdentitySelector{}  (zero-value default, full tool list)
    │
    ▼  (up to maxIter times)
drainAdapter(ctx, cfg, messages, tools)     ← calls ProviderAdapter.Stream()
    │
    ├── EventTextDelta  → emit SSE assistant_delta token → accumulate FinalText
    ├── EventToolCall   → collect into sr.ToolCalls
    └── EventDone       → sr populated (FinalText, ToolCalls, Usage, timing)
    │
    ▼
emit assistant_done SSE
    │
    ├── no tool calls → return RunResult{Status: "complete"}
    │
    ├── request_tools call?
    │       └── cfg.Selector.Upgrade(tc) → upgraded tools + summary
    │           appendRequestToolsDef → capToolsForProvider → re-enter loop
    │
    └── real tool calls
            │
            ├── ToolPolicy.blockedByToolPolicy? → inject blocked result
            │
            ├── needs approval? → defer ALL, emit approvalRequired SSE
            │                     resolved via POST /approvals/:id/approve
            │
            └── auto-approve → three-pass parallel execution:
                    ├── Pass 1 (concurrent) — stateless tools (goroutine per call, WaitGroup)
                    ├── Pass 2 (serial)     — stateful tools (browser.* share go-rod session)
                    └── Pass 3 (ordered)    — emit SSE events + append tool messages in original order
```

### 3.3 Provider adapters (`internal/agent/adapter_*.go`)

Each adapter owns its full HTTP + SSE lifecycle. All emit typed `TurnEvent` values on a channel; the loop drains them via `drainAdapter`:

```
ProviderAdapter.Stream(ctx, TurnRequest) (<-chan TurnEvent, error)
    │  goroutine
    │
    ├── build request body
    ├── HTTP POST → provider endpoint
    ├── stream response body through internal/agent/stream primitives
    └── emit:  EventTextDelta | EventToolCall | EventDone | EventError
```

| Adapter | Provider(s) | Stream path |
|---------|-------------|-------------|
| `adapter_openai.go` | `openai` | Responses API SSE (`response.output_text.delta`, `response.completed`) |
| `adapter_anthropic.go` | `anthropic` | Messages SSE (`content_block_delta`, `message_delta`) + prompt caching |
| `adapter_oaicompat.go` | `gemini`, `openrouter`, `lm_studio`, `ollama`, `atlas_engine` | OAI-compat `/chat/completions` SSE via `stream.ParseOAICompatStream` |
| `adapter_mlx.go` | `atlas_mlx` | OAI-compat with 503-retry loop + non-streaming fallback when model is loading |
| `adapter_local.go` | `lm_studio` (non-streaming path) | `callOpenAICompatNonStreaming` → single delta + done |

**`internal/agent/stream/`** — stdlib-only SSE parsing (no imports from `internal/agent`):
- `Scanner` — `bufio.Scanner` wrapper, strips `data:` prefix, stops at `[DONE]`
- `Assembler` — accumulates `ToolDelta` fragments by index, emits complete `OAIToolCall` values
- `ParseOAICompatStream(ctx, r io.Reader) <-chan Chunk` — goroutine-based OAI SSE parser

**Error parsing:** `parseProviderErrorBody` (in `adapter.go`) decodes HTTP error responses in priority order: `error.metadata.raw` (OpenRouter upstream message) → `error.message` → raw body. Applied by all four adapters.

### 3.4 Tool selection (`internal/chat/selector*.go`)

`NewSelector(mode, policy, ...)` returns a `ToolSelector` appropriate for the configured mode. The policy's `AllowedToolPrefixes` pre-filters the registry before any selector receives it.

| Mode | Selector | `Initial()` behavior | `Upgrade()` behavior |
|------|----------|----------------------|----------------------|
| `lazy` | `lazySelector` | LLM router → `request_tools` only | stage 1: short list; stage 2: broad/categories |
| `llm` | `llmSelector` | Full LLM routing (explicit mode) | same as initial |
| `heuristic` | `heuristicSelector` | Signal-score `SelectiveToolDefs` | same as initial |
| `off` / unknown (no policy) | `IdentitySelector` | nil (loop uses full registry) | nil (no upgrade) |
| `off` / unknown (with policy) | `scopedSelector` | filtered `ToolDefinitions()` | filtered `ToolDefinitions()` |

**Tool cap:** `capToolsForProvider` enforces a 128-tool limit for `openai`, `openrouter`, `lm_studio`, `atlas_engine`. Anthropic has no documented limit. Priority order when trimming: `forge.*` → `atlas.*` / `info.*` / `request_tools` → `vault.*` / `gremlin.*` / `diary.*` / `image.*` → everything else.

**Provider dispatch:**

| Provider | API | Endpoint | Notes |
|---|---|---|---|
| `openai` | Responses API | `POST /v1/responses` | `store: false`, `max_output_tokens: 4096`, system prompt → `instructions` field |
| `anthropic` | Messages API | `POST /v1/messages` | `max_tokens: 4096`, prompt caching on system + tools |
| `gemini` | OAI-compat | `POST /v1beta/openai/chat/completions` | `max_tokens: 4096`, `stream_options: {include_usage: true}` |
| `openrouter` | OAI-compat | `POST /api/v1/chat/completions` | `max_tokens: 4096`, `HTTP-Referer` + `X-Title` headers; 128-tool cap |
| `lm_studio` / `ollama` / `atlas_engine` | OAI-compat (local) | `POST /v1/chat/completions` | coalesce adjacent same-role messages |
| `atlas_mlx` | OAI-compat (local) | `POST /v1/chat/completions` | 503-retry loop while model loads; non-streaming tool fallback |

**Timeouts:** 30s standard, 90s `browser.*`. **Concurrency:** stateless tools run in parallel per turn (40–70% latency reduction). `browser.*` serialised via `IsStateful()`. **Max iterations:** configurable, default 10.

Health checks mirror each provider's live endpoint. Model lists fetched live from `/models` with curated fallback for all providers including OpenRouter.

---

## 4. Skills

Skills live in `internal/skills/registry.go`. Each entry:

```go
type SkillEntry struct {
    Def         ToolDef        // OpenAI function schema
    PermLevel   string         // "read" | "draft" | "execute"
    ActionClass ActionClass    // read | local_write | destructive_local |
                               //   external_side_effect | send_publish_delete
    Fn          func(ctx context.Context, args json.RawMessage) (string, error)
    FnResult    func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
```

`ToolDef.RawSchema map[string]any` — when set, passed directly as the OpenAI
`parameters` object instead of building from `Properties`. Custom skills use this
to declare arbitrary JSON Schema from their `skill.json` manifest.

`Fn` returns a plain string. `FnResult` returns a structured `ToolResult` with
success/failure, artifacts, warnings, and dry-run support. Use one or the other.

**Skill classification — three tiers:**

| Tier | Description | Source tag |
|------|-------------|------------|
| **Core built-in** | Needs Go internals: SQLite, SSE broadcaster, Keychain, go-rod Chrome | `builtin` |
| **Standard built-in** | Self-contained API / system calls compiled in for convenience | `builtin` |
| **Custom** | User-installed executable (`~/…/ProjectAtlas/skills/<id>/run`), called via subprocess JSON protocol | `custom` |

Custom skills are registered at startup by `LoadCustomSkills()` and appear in
`GET /skills` with `"source": "custom"` or `"source": "forge"` (for Forge-generated
skills). The Skills UI groups them under **Custom Extensions**; Forge-generated
extensions use a **Generated** badge. The model cannot distinguish them from built-ins.

The Skills UI also exposes module-owned **Automation Control**, **Workflow Control**,
and **Communication Bridge** catalog entries. These map to canonical `automation.*`,
`workflow.*`, and `communication.*` agent actions. Legacy `gremlin.*` aliases remain
compatibility-only and should not be the visible user surface.

The agent should use `communication.list_channels` as the source of truth for
Telegram/WhatsApp/Slack/Discord destinations. `automation.create` and
`automation.update` accept the returned channel `id` as `destinationID`, so the
agent does not need to ask the user for bot tokens, chat IDs, or Saved Messages
when an authorized bridge session already exists.

**Subprocess protocol (custom skills):**
```
stdin:  {"action": "search", "args": {"query": "…"}}   ← one JSON line
stdout: {"success": true,  "output": "…"}               ← one JSON line
stdout: {"success": false, "error":  "…"}               ← on failure
```
Process is spawned fresh per call with a 30s deadline. Output is capped at 1 MB.

See **[`docs/custom-skills.md`](custom-skills.md)** for the full authoring guide, manifest
reference, credential patterns, and worked examples (Linear, GitHub, shell, Slack).

**Built-in + module skills (~147 actions across 18 capability groups):**

| Group | Key actions |
|-------|-------------|
| weather | current, forecast, hourly, brief, dayplan, activity_window |
| web | search, fetch_page, research, news, check_url, multi_search, extract_links, summarize_url |
| websearch | query (Brave Search API) |
| fs | list_directory, read_file, search, get_metadata, content_search, write_file, patch_file, create_directory |
| system | open_app, open_file, open_folder, clipboard_read/write, notification, running_apps, get_display_info |
| terminal | run_command, run_script, read_env, list_processes, kill_process, get_working_directory, which |
| applescript | calendar, reminders, contacts, mail_read, mail_wait_for_message, mail_write, safari, notes, music, run_custom |
| finance | quote, history, portfolio |
| image | generate (DALL-E 3) |
| diary | record |
| browser | navigate, screenshot, read_page, find_element, scroll, session_check, wait_for_element, wait_network_idle, tabs_list, tabs_new, tabs_switch, tabs_close, switch_frame, switch_main_frame, click, hover, select, type_text, fill_form, submit_form, eval, upload_file, session_login, session_store_credentials, session_submit_2fa, session_clear, solve_captcha |
| vault | store, lookup, list, update, delete, totp_generate |
| automation | create, update, delete, list, get, enable, disable, run, run_history, next_run, duplicate, validate_schedule |
| workflow | create, update, delete, list, get, run, run_history, duplicate, validate, explain |
| communication | list_channels, send_message |
| **team** | **`team.*`** (list, get, delegate) + **`agent.*`** (create, update, delete, enable, disable, pause, resume, sequence, assign) — all routed to the "team" capability group |
| forge | orchestration.propose |
| atlas | info, list_skills, capabilities |

**Capability group routing:** `toolCapabilityGroup()` in `registry.go` maps action IDs to groups for `SelectiveToolDefs`. Both `team.*` and `agent.*` prefixes resolve to the "team" group so all agent management skills surface together when team-related intent is detected.

**Action classes** control the approval gate:
- `read` — auto-approved, no user prompt
- `local_write` — auto-approved (creates/modifies local state)
- `destructive_local` — requires approval (deletes local state)
- `external_side_effect` — requires approval (clicks, form submissions, external API calls)
- `send_publish_delete` — requires approval (messages, posts, account deletion)

---

## 5. Browser Control

`internal/browser/Manager` owns a singleton headless Chrome process via go-rod.
Multiple tabs are supported; the active tab is tracked by index. Cookies persist
to SQLite so sessions survive Atlas restarts.

**Stealth:** Every new page (tab) is patched with `go-rod/stealth` JS before any
navigation, suppressing `navigator.webdriver`, canvas fingerprint, and CDP signals.

**Multi-tab:** `TabsNew`, `TabsSwitch`, `TabsClose`, `TabsList` manage open tabs.
Switching tabs resets any active iframe context.

**iframe context:** `SwitchFrame(selector)` enters an iframe; all element operations
(`click`, `type`, `find`, etc.) target the frame until `SwitchMainFrame()` is called.

**Session flow:**
```
browser.navigate(url)
    → reset iframe context
    → inject stored cookies (SQLite browser_sessions)
    → Navigate + WaitLoad
    → DetectLoginWall (URL pattern → title keyword → DOM input[type=password])
    → FormSelector uses CSS.escape() to prevent CSS-injection via crafted page IDs
    → persist cookies after load

browser.session_login(url)
    → look up credentials in vault by hostname (fuzzy match)
    → AutoLogin: fill username/password → submit → WaitLoad
    → if 2FA detected: auto-generate TOTP from vault, submit
    → persist cookies on success

browser.eval(expression)
    → runs JS in current page/frame context with bounded ctx
    → returns JSON-serialised result

browser.upload_file(selector, file_path)
    → validates file exists on disk first
    → el.SetFiles([file_path])

browser.wait_network_idle(timeout_ms)
    → page.WaitLoad() with bounded context
```

Chrome runs **headless by default**. To open a visible window for debugging, set
`"browserShowWindow": true` in `~/Library/Application Support/ProjectAtlas/go-runtime-config.json`.

---

## 6. Vault

`internal/creds/vault.go` — a separate Keychain item (`com.projectatlas.vault`, account `credentials`) that stores agent-managed credentials as a JSON array.

```go
type VaultEntry struct {
    ID          string  // random 16-hex-char ID
    Service     string  // hostname or service name
    Label       string  // human-readable name
    Username    string
    Password    string
    TOTPSecret  string  // base32 TOTP seed for 2FA (RFC 6238)
    Notes       string
    SessionName string  // optional label for multi-account support (e.g. "work", "personal")
    CreatedAt   string
    UpdatedAt   string
}
```

`vault.totp_generate` calls `totp.GenerateCode` (pquerna/otp) and returns the current
6-digit code with seconds remaining. Used by `browser.session_login` for automatic 2FA.

---

## 7. Voice Pipeline

`internal/voice/Manager` owns two subprocesses — whisper-server (STT) and a Python Kokoro HTTP server (TTS) — and routes audio through a provider adapter layer.

```
POST /voice/transcribe
    │  multipart audio (WAV for local, any format for cloud)
    ▼
Manager.Transcribe()
    │
    ├─ resolveAdapters() → STTAdapter (local | openai | gemini | elevenlabs)
    │
    ├─ localSTT.Transcribe()
    │     ├─ auto-start whisper-server (DYLD_LIBRARY_PATH set)
    │     ├─ WaitWhisperReady (15 s)
    │     └─ POST audio/wav → http://127.0.0.1:11987/inference
    │
    └─ cloudSTT.Transcribe() → cloud API → text

POST /voice/synthesize
    │  { text, voice? }
    ▼
Manager.Synthesize()
    │
    ├─ localTTS → StartKokoro (lazy) → stream PCM from kokoro HTTP server
    └─ cloudTTS → cloud API → stream PCM chunks
         └─ SSE: voice_audio { chunk:base64, index, sampleRate }
```

**Provider selection** — `config.activeAudioProvider` (`"local"` | `"openai"` | `"gemini"` | `"elevenlabs"`). Adapters are rebuilt in-process when the provider changes; no restart required. Falls back to local if the cloud provider has no API key.

**Subprocess lifecycle** — whisper-server starts lazily on first transcribe (or explicit `POST /voice/session/start`). Kokoro starts lazily on first synthesize. Both are killed together on session end or idle timeout. Idle timer is reset at the *start* of each transcription, not the end, so long requests are never evicted mid-flight.

**macOS dylib fix** — whisper-server is launched with `DYLD_LIBRARY_PATH=$INSTALL_DIR/voice` so its bundled `libwhisper.*.dylib` is found regardless of cmake rpath baking. Durable across whisper rebuilds because the fix is in the Go launcher, not the binary.

**Python venv** — Kokoro runs inside `$INSTALL_DIR/voice/venv`. kokoro-onnx 0.5+ requires Python `<3.14`; `UpgradeKokoro` detects a Python 3.14 venv and recreates it with the best available Python 3.10–3.13 via `findBestVoicePython()`.

## 8. Forge Pipeline

```
POST /forge/proposals  {name, description, apiURL}
    │
    ▼
forge.Service.Propose — background goroutine
    ├── Add ResearchingItem (visible at GET /forge/researching)
    ├── AI call → structured JSON proposal
    ├── Parse → ForgeProposal{status: "pending"} → forge-proposals.json
    └── Remove ResearchingItem

POST /forge/proposals/:id/install
    → UpdateProposalStatus → "installed"
    → BuildInstalledRecord → forge-installed.json
    → GenerateAndInstallCustomSkill (forge/codegen.go)
         ├── Parse SpecJSON → ForgeSkillSpec
         ├── Parse PlansJSON → []ForgeActionPlan
         ├── Parse ContractJSON → APIResearchContract (parameter hints)
         ├── Generate skill.json  (source: "forge", actions with JSON Schema)
         ├── Generate Python run  (stdlib urllib, auth, URL-template substitution)
         └── Write to skills/<skillID>/  → picked up at next LoadCustomSkills()

POST /forge/installed/:skillID/uninstall
    → DeleteInstalled → forge-installed.json
    → RemoveCustomSkillDir → skills/<skillID>/  (skill disappears from registry)
    → SetForgeSkillState → "uninstalled"
```

Forge-generated skills appear in `GET /skills` with `"source": "forge"` and are
shown in the **Custom Extensions** group with a Generated badge. The agent calls them
identically to any other custom skill — one JSON line in, one JSON line out.

---

## 9. API Validation Gate

`internal/validate/gate.go` — `Gate.Run(ctx, req) ValidationResult`

```
Phase 1 — Pre-flight
    method check (non-GET → skipped), shape check, auth type, credential readiness

Phase 2 — Live execution (max 2 attempts)
    Attempt 1: resolve example inputs → build URL → GET → inspect response
               ├── confidence ≥ 0.6  → approve
               ├── hard reject (401/403/5xx) → abort
               └── needsRevision → attempt 2 with alternate example

Phase 3 — Audit
    Append AuditRecord to api-validation-history.json (max 100, atomic)
```

---

## 10. Memory System

Atlas maintains long-term memory across conversations through three coordinated subsystems: per-turn extraction, MIND.md reflection, and the nightly dream cycle.

### 9.1 Post-Turn Flow

After every agent turn completes, three non-blocking goroutines fire concurrently — the user's response is delivered immediately and none of these block it:

```
assistant message → store in SQLite → emit done SSE
    │
    ├── memory.ExtractAndPersist   (internal/memory/extractor.go)
    ├── mind.ReflectNonBlocking    (internal/mind/reflection.go)
    └── mind.LearnFromTurnNonBlocking  (internal/mind/skills.go)
```

### 9.2 Memory Extraction Pipeline

`memory.ExtractAndPersist` runs a three-stage pipeline after each turn:

```
Stage 1 — Regex extraction (fast, no API call)
    Seven category extractors run in order:
      commitment  — "always", "never", "from now on" → confidence 0.99, is_user_confirmed=true
      explicit    — "remember that …", "please remember …" → structured parse → profile/preference/…
      profile     — name ("my name is"), location ("I live in"), environment signals
      preference  — response style, approval visibility, temperature units
      project     — Atlas context, active focus areas
      workflow    — tool combos, feature sequencing preference
      episodic    — success signals ("working perfectly", "validated successfully")

    Threshold check: confidence ≥ cfg.MemoryAutoSaveThreshold (default 0.75) to save
    Deduplication: FindDuplicateMemory(category, title) — merge (take max scores, union tags)
    Each saved/updated memory triggers asyncEmbed (search_document: prefix, outlives turn ctx)
    │
Stage 2 — LLM extraction (skipped if explicit "remember" command found in Stage 1)
    Runs when: novel facts may exist OR tool results are present
    Catches facts the regex misses; also detects tool_learning signals from tool outcomes
    │
Stage 3 — Entity extraction (non-blocking goroutine, internal/memory/llm.go)
    LLM returns {entities[], edges[]} JSON
    UpsertEntity → memory_entities; entity embeddings stored async
    SupersedeEdge closes prior same-pair edge; SaveEdge adds directed edge with valid_from/valid_until
```

**Memory categories:** `commitment`, `profile`, `preference`, `project`, `workflow`, `episodic`, `tool_learning`

**Memory recall:** `RelevantMemories(query, limit, queryVec)` — hybrid retrieval pipeline:
1. FTS5 BM25 selects top-50 candidates; native `rank` column normalized to [0,1]
2. Cosine re-rank over candidate embeddings when `queryVec` is present (HyDE path)
3. Score: `cosine×0.45 + bm25×0.25 + importance×0.20 + recency×0.10 − diversity` (or `bm25×0.50 + importance×0.30 + recency×0.20` without vector)

**HyDE:** `memory.HyDEVector(ctx, provider, userMessage)` in `internal/memory/recall.go` generates a hypothetical memory sentence, embeds it with the `search_query:` Nomic prefix, and passes the vector into `RelevantMemories`. 4-second timeout; degrades to BM25-only on failure.

Commitment memories receive a +0.20 importance boost so they always surface first. Injected into the system prompt before each turn via `<recalled_memories>`. Invalidated memories (`valid_until` in the past) are excluded.

**Entity context:** `buildEntityContext(db, queryVec)` in `internal/chat/prompt.go` finds the nearest entity nodes via cosine scan, BFS-traverses valid edges up to 2 hops, and injects a `<entity_context>` block (e.g. `Rami → works_at → RXA Labs`) alongside recalled memories when the HyDE vector is available.

**Opinion reinforcement** (dream cycle):
- `reinforce` → +0.20 confidence
- `weaken` → −0.20 confidence
- `contradict` → −0.40 confidence + sets `valid_until = now` (memory excluded from future recall)

### 9.3 MIND Reflection Pipeline

`internal/mind` runs non-blocking after every agent turn via `ReflectNonBlocking`. Serialized via `reflectMu` (TryLock — drops rather than queues if another run is in progress).

Also fires concurrently after every turn (when `ThoughtsEnabled`):
- `mind.NotifyTurnNonBlocking()` — resets the idle timer on the nap scheduler
- `chat.detectAndRecordSurfacings()` — scans assistant reply for `[T-NN]` markers
- `chat.classifyPendingIfAny()` — classifies the most recent pending surfacing via one-shot LLM call

```
End of turn
    │
    ▼
reflectMu.TryLock()  — if locked, drop (best-effort, never blocks user)
    │
    ▼
Tier 1 — Today's Read (always runs, ~60 tokens out)
    Update the "## Today's Read" section of MIND.md with 2-3 specific sentences
    about the turn energy, pace, and focus.
    │
    ▼
Diary — append one-line entry to DIARY.md (max 3 per day, enforced by AppendDiaryEntry)
    │
    ▼
Significance gate  — YES/NO: did this turn reveal something meaningfully new?
    │
    ├── NO  → done
    │
    └── YES → Deep reflection (Tier 2)
                Rewrite narrative sections (Understanding of You, Patterns,
                Active Theories, Our Story, What I'm Curious About).
                Validates size (≤ 50 KB) and header before committing.
                Splices saved Today's Read back in — no extra AI call needed.
```

**Protected MIND.md sections** (never overwritten by Tier 2): `## Identity`, `## Current Frame`, `## Commitments`, `## Today's Read`

**SKILLS.md learning** runs in parallel via `LearnFromTurnNonBlocking` (fires when ≥ 2 tool calls in a turn):
- Explicit phrases ("next time I ask", "always do") → immediate routine write
- Repeated identical tool sequence (3+ turns) → routine write
- On concurrent write conflict detected inside lock → **abort** (not overwrite)

### 9.4 Dream Cycle

`StartDreamCycle` (launched from `main.go`) runs a **5-phase consolidation cycle** daily at **3 AM local time**. If the daemon was offline at the scheduled time, a catch-up run fires 60s after startup. State is persisted in `dream-state.json`.

```
Phase 1 — Prune stale memories
    Delete: confidence < 0.5 AND age > 30 days
    Delete: never retrieved AND age > 60 days AND importance < 0.7

Phase 2 — Merge near-duplicate memories (AI)
    Scan memories by category + importance; LLM identifies near-duplicates;
    merge content, union tags, take max scores, delete the weaker copy.

Phase 3 — Tool outcome synthesis → SKILLS.md (AI)
    Scan tool_learning memories; LLM synthesizes patterns into SKILLS.md routines.
    Marks processed memories as synthesized.

Phase 4 — Diary synthesis → memories (AI)
    Read recent DIARY.md entries; LLM extracts structured memories;
    saves to SQLite with tags: ["dream", "diary_synthesis"].

Phase 5 — MIND.md refresh (AI)
    Full MIND.md rewrite incorporating current memories + diary context.
    Validates header + size before committing.
```

Phases 2–5 require an AI provider. Phases 1 (prune) always runs even if no provider is configured.

### 9.5 Mind-Thoughts (Proactive Loop)

Opt-in proactivity system gated on `ThoughtsEnabled` (master) and `NapsEnabled` (sub-flag for autonomous scheduling). Toggle lives on the Mind screen in the web UI.

**THOUGHTS section** lives inside MIND.md under `## THOUGHTS`. Each thought has: `id`, `body`, `confidence` (0–100), `value` (0–100), `class` (ActionClass), `score = confidence × value × safety_multiplier[class] / 100`, `provenance`, optional `ProposedAction`.

**Score ceiling by class:**
| Class | Safety multiplier | Max achievable score |
|-------|------------------|---------------------|
| `read` | 1.00 | 100 |
| `local_write` | 0.97 | 97 |
| `destructive_local` | 0.90 | 90 |
| `external_side_effect` | 0.95 | 95 |
| `send_publish_delete` | 0.85 | 85 |

Only `read`-class thoughts can reach the `AutoExecuteThreshold` (95). All other classes are mathematically capped below it and go through the approval flow instead.

**Nap cycle** (`internal/mind/nap.go`):

```
RunNap (triggered by scheduler idle/floor or POST /mind/nap)
    │
    ▼
Acquire nap-lock.json (advisory, cross-process, 2-min timeout)
    │
    ▼
Read THOUGHTS section → build prompt (nap_prompt.go)
    │
    ▼
One-shot LLM call → JSON ops: add/reinforce/discard
    │
    ▼
thoughts.Apply — validate + write ops → updated THOUGHTS section
    │
    ▼
Dispatcher.Dispatch — for each surviving thought:
    score ≥ 95 + read class  → auto-execute skill → EnqueueGreeting
    score ≥ 80 (non-read)    → create Approval record
    │
    ▼
Emit nap_completed telemetry → release lock
```

**Nap scheduler** (`internal/mind/nap_scheduler.go`):
- **Idle trigger** — `NapIdleMinutes` (default 60) after last chat turn. Reset by `NotifyTurnNonBlocking()`.
- **Floor trigger** — every `NapFloorHours` (default 6) regardless of activity.
- Deduplication window: 2 min. Concurrent runs serialized via `runningMu.TryLock()`.

**Engagement lifecycle:**
1. Agent reply contains `[T-NN]` → `detectAndRecordSurfacings` writes `pending` engagement record
2. User's next turn → `classifyPendingIfAny` runs one-shot LLM classifier → `like | neutral | dislike`
3. Nap cycle reads engagement signal → reinforces or discards thought accordingly
4. Stale pending surfacings (> 24h) → nap marks them `ignored`

**Decay rules** (nap curation):
- 2 `dislike` signals → thought discarded
- 3 `ignore` signals → thought discarded
- Confidence drift: each nap reduces non-reinforced thought confidence by −5

**Live greeting** (`internal/chat/greeting.go`):
- Auto-executed thoughts enqueue a `GreetingEntry` (thought body + skill result)
- On next chat open: `POST /chat/greeting` drains queue → one-shot model call → saved as assistant message → SSE

**HTTP surface** (`internal/modules/mind/module.go`):
```
POST /mind/nap              — manual nap trigger (sync, returns NapResult)
POST /mind/dispatch         — run dispatcher on current thoughts without nap
GET  /mind/thoughts         — read THOUGHTS section as JSON array
POST /mind/thoughts/seed    — inject a hand-crafted thought (dev/test)
DELETE /mind/thoughts/{id}  — surgical thought removal (dev/test)
GET  /mind/telemetry        — raw telemetry rows (filterable)
GET  /mind/telemetry/summary — counts by kind (feeds dashboard widgets)
```

All endpoints return `409 Conflict` when `ThoughtsEnabled: false`.

**Telemetry kinds:** `nap_completed`, `nap_skipped`, `thought_added`, `thought_discarded`, `thought_reinforced`, `auto_execute_attempted`, `auto_execute_succeeded`, `auto_execute_failed`, `approval_proposed`, `engagement_recorded`, `engagement_classified`, `greeting_delivered`, `greeting_skipped`

---

## 11. Data Storage

**SQLite** — `~/Library/Application Support/ProjectAtlas/atlas.sqlite3`

| Table | Purpose |
|-------|---------|
| `conversations` | Conversation records |
| `messages` | All messages (user + assistant + tool) |
| `memories` | Extracted long-term memories — `valid_until` for contradiction; `embedding` BLOB for cosine recall; `memories_fts` FTS5 index for BM25 candidate selection |
| `memory_entities` | Named entities extracted per-turn (person/place/org/concept/technology/event/other) with embeddings for vector-nearest search |
| `memory_edges` | Directed temporal relations between entities (`valid_until NULL` = currently true; set by `SupersedeEdge` when a fact changes) |
| `gremlin_runs` | Automation run records |
| `deferred_executions` | Pending approval tool calls; `agent_id` column (nullable) identifies sub-agent requester |
| `web_sessions` | HMAC session tokens |
| `browser_sessions` | Per-host browser cookie snapshots (7-day expiry); `session_name` column for multi-account |
| `mind_telemetry` | Mind-thoughts event log — nap outcomes, auto-executes, engagement signals, greetings |
| `agent_definitions` | Atlas Teams — parsed AGENTS.md records, one row per team member |
| `agent_runtime` | Atlas Teams — live agent state: status, currentTaskID, lastActiveAt |
| `team_tasks` | Atlas Teams — delegated task records with status and result |
| `team_task_steps` | Atlas Teams — sub-agent message log per task (system/user/assistant/tool) |
| `team_events` | Atlas Teams — activity log powering Team HQ activity rail |
| `token_usage` | Token consumption and cost per LLM call — covers chat turns, delegated agent runs, and background system calls (memory extraction, reflection, forge research, classifier) |

**JSON files** — `~/Library/Application Support/ProjectAtlas/`

| File | Purpose |
|------|---------|
| `config.json` | RuntimeConfigSnapshot |
| `go-runtime-config.json` | Go-only sidecar config |
| `MIND.md` | Agent system prompt — updated each turn by `internal/mind` Tier 1/Tier 2 reflection pipeline |
| `SKILLS.md` | Skills-layer memory — learned routines appended by `internal/mind` skills learner + dream Phase 3 |
| `DIARY.md` | Per-day diary entries (max 3/day) — written by `diary.record` skill and reflection pipeline |
| `dream-state.json` | Last successful dream cycle timestamp — used for catch-up detection at startup |
| `AGENTS.md` | Atlas Teams — canonical team member definitions; synced to `agent_definitions` table on startup and file change |
| `GREMLINS.md` | Legacy/import-export automation definitions; SQLite is canonical for module-owned automation definitions |
| `workflow-definitions.json` | Legacy/import workflow definitions; SQLite is canonical |
| `workflow-runs.json` | Legacy workflow run records; SQLite is canonical |
| `forge-proposals.json` | Forge proposal records |
| `forge-installed.json` | Installed Forge skill records |
| `go-skill-states.json` | Skill enable/disable overrides |
| `action-policies.json` | Per-action approval policies |
| `fs-roots.json` | Approved filesystem roots |
| `api-validation-history.json` | API validation audit log (max 100) |
| `nap-state.json` | Last nap timestamp — idle/floor scheduler catch-up after restart |
| `nap-lock.json` | Advisory cross-process nap lock (auto-released after 2 min) |
| `pending-greetings.json` | Queued auto-executed thought results awaiting greeting delivery |
| `thought-engagement.jsonl` | Per-thought engagement records (pending → like/neutral/dislike/ignored) |

**Keychain** — `com.projectatlas.credentials` / account `bundle` → JSON blob with all API keys.
**Vault** — `com.projectatlas.vault` / account `credentials` → JSON array of VaultEntry.

---

## 12. Auth Model

| Request type | Auth mechanism |
|-------------|----------------|
| Localhost (loopback peer) | Bypass — process-trust model |
| Remote LAN (`remoteAccessEnabled: true`) | `/auth/remote-gate` + `POST /auth/remote` with remote access key (Keychain), issuing remote session cookie |
| Remote LAN transport | HTTPS required for non-Tailscale remote requests (or trusted loopback TLS-terminating proxy) |
| Remote state-changing calls | Session-bound CSRF token from `GET /auth/csrf` required in `X-CSRF-Token` header |
| Tailscale (`tailscaleEnabled: true`) | Direct Tailnet peer trust path (no Atlas remote key/session required) |
| Web sessions | HMAC-SHA256 launch token bootstrap (`/auth/token` local-only, `/auth/bootstrap`) persisted in `web_sessions`, validated by `RequireSession` |

---

## 13. Atlas Teams

Atlas Teams is the delegated multi-agent capability for V1.0. See [`PLAN.md`](../PLAN.md) for the full product specification and milestone roadmap. See [`docs/agent-boundary.md`](agent-boundary.md) for the architectural boundary and delegation mechanism.

**Core rule:** Agent owns delegation decisions. Teams owns delegated execution.

### How team management and delegation work

```
Explicit "create an agent" request
    │
    └── planner prefers team-control over workflow/automation creation
              │
              ▼
         Atlas calls team.create / team.update / team.delete / team.enable ...
              │
              ▼
         Teams module rewrites canonical AGENTS.md + syncs DB/runtime state
              │
              └── Team HQ reads the updated snapshot through /team APIs

Delegation request
    │
    └── Atlas calls team.delegate (standard skill call)
              │
              ▼
         Teams module FnResult closure
              │
              ▼
         TeamOrchestrator.Delegate()
              ├── load AgentDefinitionRecord from DB (synced from AGENTS.md)
              ├── build filtered tool slice (allowedSkills prefixes only)
              ├── build sub-agent system prompt (role + mission + goal)
              ├── loop.Run() directly with NoopEmitter
              │       (NOT via AgentRuntime.HandleMessage — avoids chat.Service pipeline)
              ├── persist TeamTask + TeamTaskStep records
              ├── emit task/activity events
              └── return ToolResult → back to Atlas's loop as tool call result
```

### AGENTS.md canonical file

`~/Library/Application Support/ProjectAtlas/AGENTS.md` is the source of truth for team member definitions. Structured runtime persistence (`AgentDefinitionRecord`, `AgentRuntimeRecord`) holds operational state. The UI reads runtime snapshots, not the file directly.

File format:
```markdown
# Atlas Team

## Atlas
(Atlas's own station — always present)

## Team Members

### Scout
- ID: scout
- Role: Research Specialist
- Mission: Gather facts, references, and external context
- Style: concise and factual
- Allowed Skills: web., websearch., fs.read_file, fs.search
- Allowed Tool Classes: read
- Autonomy: assistive
- Activation: atlas_in_task_assist
- Enabled: yes
```

### Runtime components

| Component | File | Purpose |
|---|---|---|
| `TeamsModule` | `internal/modules/teams/module.go` | `/team` routes, Team HQ snapshot, AGENTS sync, watcher lifecycle, runtime state transitions |
| `TeamActions` | `internal/modules/teams/agent_actions.go` | Registers `team.*` management and delegation skills, runs delegated sub-agent work |
| `AgentsFile` | `internal/modules/teams/agents_file.go` | Canonical AGENTS.md parse/write helpers used for sync and edits |
| `Storage layer` | `internal/storage/db.go` | AgentDefinitionRecord + AgentRuntimeRecord + TeamTask + TeamTaskStep + TeamEvent persistence |
| `Platform interface` | `internal/platform/storage.go` | Teams-facing storage contracts used by the module |

### Storage tables (added in M1 + M4)

| Table | Purpose |
|---|---|
| `agent_definitions` | Parsed AGENTS.md records — one row per team member |
| `agent_runtime` | Live agent state: status, currentTaskID, lastActiveAt |
| `team_tasks` | Delegated task records with status + result |
| `team_task_steps` | Sub-agent message log (system/user/assistant/tool per task) |
| `team_events` | Activity log — powers Team HQ activity rail |

`deferred_executions` has an additive nullable `agent_id` column (M4) so approval records can identify which sub-agent requested an action.

### Forge integration

No direct module coupling. Forge-installed skills surface in the global skills registry automatically. When creating or editing a team member, `allowedSkills` can reference any registered skill ID including Forge-generated ones. The Teams module subscribes to `forge.skill.installed` EventBus events to surface new skills in the Team HQ activity rail.

### HTTP routes

```
GET    /team                          — full Team HQ snapshot (Atlas node + agents + activity)
GET    /team/agents                   — list all agent definitions
GET    /team/agents/:id               — single agent with runtime state
POST   /team/agents                   — create agent
PUT    /team/agents/:id               — update agent definition
DELETE /team/agents/:id               — delete agent definition
POST   /team/agents/:id/enable        — enable agent
POST   /team/agents/:id/disable       — disable agent
POST   /team/agents/:id/pause         — pause agent
POST   /team/agents/:id/resume        — resume agent
GET    /team/tasks                    — list task records
GET    /team/tasks/:id                — task detail + steps
POST   /team/tasks/:id/cancel         — cancel in-progress task
GET    /team/events                   — activity event log
POST   /team/sync                     — re-sync AGENTS.md → DB
```

### Agent-facing team skills

Atlas sees team management as a first-class skill group rather than falling back to workflow or automation creation for explicit team-member requests.

```
team.list
team.get
team.create
team.update
team.delete
team.enable
team.disable
team.pause
team.resume
team.delegate
```

---

## 14. Deferred (V1.0)

| Feature | Status |
|---------|--------|
| Per-agent provider override | Deferred to Teams M5 — all sub-agents use Atlas's global provider config in M4 |
| Parallel squad execution | Deferred to Teams M5 — requires resolving `turnCancels` single-turn-per-conv constraint |
| Sub-agent SSE streaming | Deferred to Teams M5 — sub-agents use NoopEmitter in M4; TeamEmitter planned for M5 |
| Full approval UI for sub-agents | Deferred to Teams M5 — agent_id stored in M4 but approval surface shows agent identity in M5 |
| Custom skill live-reload | Daemon restart required after install or remove |
| Custom skill ZIP/URL install | Local path only; URL download deferred |
| Custom skill vault credential injection | Skills read credentials from env; direct vault injection deferred |
| Parallel squad execution | Deferred to Teams M5 — requires resolving `turnCancels` single-turn-per-conv constraint |
