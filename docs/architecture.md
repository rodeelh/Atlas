# Atlas Architecture

**Last updated: 2026-04-07** · Custom Skills guide: [`docs/custom-skills.md`](custom-skills.md) · Internal modules: [`docs/internal-modules.md`](internal-modules.md) · Agent boundary: [`docs/agent-boundary.md`](agent-boundary.md) · Migration verification: [`docs/migration-verification.md`](migration-verification.md) · Manual smoke: [`docs/manual-smoke-checklist.md`](manual-smoke-checklist.md)

Atlas is a local AI operator. A Go binary runs as a launchd daemon (`Atlas`), serves a web UI, and connects to any supported AI provider. A Bubbletea TUI (`atlas`) provides a terminal interface. No Swift required.

Note: the package currently named `internal/chat` is architecturally the Atlas **Agent** subsystem. See [`docs/agent-boundary.md`](agent-boundary.md).

Future delegated multi-agent work should follow this rule:
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
│       │   ├── loop.go                 # Multi-turn agent execution loop
│       │   ├── provider.go             # AI provider dispatch (OpenAI/Anthropic/Gemini/LM Studio)
│       │   ├── openai.go               # OpenAI-compatible streaming + non-streaming calls
│       │   └── anthropic.go            # Anthropic Messages API client
│       ├── auth/
│       │   ├── service.go              # HMAC-SHA256 session tokens, bootstrap, middleware
│       │   └── ratelimit.go            # Per-IP rate limiting
│       ├── browser/
│       │   ├── manager.go              # BrowserManager — singleton Chrome process via go-rod
│       │   ├── loginwall.go            # Login wall heuristics (URL, title, DOM)
│       │   └── twofa.go                # 2FA challenge detection
│       ├── chat/
│       │   ├── service.go              # HandleMessage, RegenerateMind, ResolveProvider, Resume
│       │   ├── broadcaster.go          # SSE fan-out to connected clients
│       │   └── keychain.go             # resolveProvider — config + Keychain → ProviderConfig
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
│       │   └── apivalidation/          # Private module — API validation history routes
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
│       │   └── extractor.go            # Per-turn memory extraction, deduplication
│       ├── mind/
│       │   ├── reflection.go           # Two-tier MIND.md pipeline (Today's Read + deep reflect)
│       │   ├── skills.go               # SKILLS.md learned-routine detection + selective injection
│       │   ├── seed.go                 # First-run seeding of MIND.md and SKILLS.md
│       │   ├── types.go                # TurnRecord — input to reflection and skills learning
│       │   └── util.go                 # atomicWrite, truncate helpers, content validators
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
├── atlas-tui/                          # Bubbletea TUI — terminal interface
│   ├── main.go                         # Entry point — loads config, starts Bubbletea
│   ├── config/                         # Config load/save (~/.config/atlas-tui/config.json)
│   ├── client/                         # HTTP client for the runtime API
│   ├── ui/                             # Bubbletea models and views
│   └── onboarding/                     # First-run onboarding flow
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
       /automations, …
            │
            ├── internal/agent      ← OpenAI / Anthropic / Gemini / LM Studio
            ├── internal/skills     ← 16 built-in skill groups, 90+ actions + custom skills
            ├── internal/customskills ← manifest types + filesystem scanning (leaf pkg)
            ├── internal/browser    ← Headless Chrome via go-rod
            ├── internal/platform   ← Private host, module registry, event bus, scoped storage
            ├── internal/modules    ← First-party feature modules
            ├── internal/forge      ← Forge research pipeline
            ├── internal/validate   ← API validation gate
            ├── internal/mind       ← MIND.md reflection + SKILLS.md learning
            ├── internal/logstore   ← In-memory log ring buffer (GET /logs)
            └── internal/storage    ← SQLite
```

---

## 3. Agent Loop

One message turn in `internal/agent/loop.go`:

```
Incoming message
    │
    ▼
Build messages array (system prompt + history + new user message)
    │
    ▼
AI provider call (streaming or non-streaming)
    │
    ├── text delta  → emit SSE token → accumulate
    │
    └── tool_calls  → look up each in skills.Registry
                         │
                         ├── needs approval? → defer ALL, emit approvalRequired SSE
                         │                     resolved via POST /approvals/:id/approve
                         │
                         └── auto-approve?  → three-pass parallel execution:
                                                │
                                                ├── Pass 1 (concurrent) — stateless tools
                                                │     goroutine per call, WaitGroup
                                                │     results[i] written at original index
                                                │
                                                ├── Pass 2 (serial) — stateful tools
                                                │     browser.* share go-rod Chrome session
                                                │     run in original call order
                                                │
                                                └── Pass 3 (ordered assembly)
                                                      emit SSE events + append tool messages
                                                      strictly in original index order
                                                      (OpenAI protocol requirement)
    │
    ▼
assistant message assembled → store in SQLite → emit done SSE
```

**Timeouts:** 30s for standard skills, 90s for `browser.*`.
**Concurrency:** stateless tools (weather, web, finance, fs, etc.) run in parallel per turn,
cutting multi-tool latency by 40–70%. `browser.*` are serialised via `IsStateful()`.
**Max iterations:** configurable per provider (default 10).
**Vision:** screenshots from `browser.screenshot` are routed through vision content blocks —
OpenAI gets `image_url`, Anthropic gets `base64`.

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

The Skills UI also exposes module-owned **Automation Control** and **Workflow Control**
catalog entries. These map to canonical `automation.*` and `workflow.*` agent actions;
legacy `gremlin.*` aliases remain compatibility-only and should not be the visible user
surface.

**Subprocess protocol (custom skills):**
```
stdin:  {"action": "search", "args": {"query": "…"}}   ← one JSON line
stdout: {"success": true,  "output": "…"}               ← one JSON line
stdout: {"success": false, "error":  "…"}               ← on failure
```
Process is spawned fresh per call with a 30s deadline. Output is capped at 1 MB.

See **[`docs/custom-skills.md`](custom-skills.md)** for the full authoring guide, manifest
reference, credential patterns, and worked examples (Linear, GitHub, shell, Slack).

**Built-in skills (16 groups, 90+ actions):**

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
| gremlin | create, update, delete, list, get, enable, disable, run_now, run_history, next_run, duplicate, validate_schedule |
| forge | orchestration.propose |
| atlas | info, list_skills, capabilities |

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

## 7. Forge Pipeline

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

## 8. API Validation Gate

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

## 9. Memory System

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

`memory.ExtractAndPersist` runs a two-stage pipeline after each turn:

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
    │
Stage 2 — LLM extraction (skipped if explicit "remember" command found in Stage 1)
    Runs when: novel facts may exist OR tool results are present
    Catches facts the regex misses; also detects tool_learning signals from tool outcomes
```

**Memory categories:** `commitment`, `profile`, `preference`, `project`, `workflow`, `episodic`, `tool_learning`

**Memory recall:** `RelevantMemories(query, limit)` — BM25 FTS5 keyword search on `memories_fts`, ranked by importance. Commitment memories receive a +0.20 importance boost so they always surface first. Injected into the system prompt before each turn. Invalidated memories (`valid_until` in the past) are excluded.

**Opinion reinforcement** (dream cycle):
- `reinforce` → +0.20 confidence
- `weaken` → −0.20 confidence
- `contradict` → −0.40 confidence + sets `valid_until = now` (memory excluded from future recall)

### 9.3 MIND Reflection Pipeline

`internal/mind` runs non-blocking after every agent turn via `ReflectNonBlocking`. Serialized via `reflectMu` (TryLock — drops rather than queues if another run is in progress).

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

---

## 10. Data Storage

**SQLite** — `~/Library/Application Support/ProjectAtlas/atlas.sqlite3`

| Table | Purpose |
|-------|---------|
| `conversations` | Conversation records |
| `messages` | All messages (user + assistant + tool) |
| `memories` | Extracted long-term memories (with `valid_until` for contradiction; `memories_fts` FTS5 index for BM25 recall) |
| `gremlin_runs` | Automation run records |
| `deferred_executions` | Pending approval tool calls |
| `web_sessions` | HMAC session tokens |
| `browser_sessions` | Per-host browser cookie snapshots (7-day expiry); `session_name` column for multi-account |

**JSON files** — `~/Library/Application Support/ProjectAtlas/`

| File | Purpose |
|------|---------|
| `config.json` | RuntimeConfigSnapshot |
| `go-runtime-config.json` | Go-only sidecar config |
| `MIND.md` | Agent system prompt — updated each turn by `internal/mind` Tier 1/Tier 2 reflection pipeline |
| `SKILLS.md` | Skills-layer memory — learned routines appended by `internal/mind` skills learner + dream Phase 3 |
| `DIARY.md` | Per-day diary entries (max 3/day) — written by `diary.record` skill and reflection pipeline |
| `dream-state.json` | Last successful dream cycle timestamp — used for catch-up detection at startup |
| `GREMLINS.md` | Legacy/import-export automation definitions; SQLite is canonical for module-owned automation definitions |
| `workflow-definitions.json` | Legacy/import workflow definitions; SQLite is canonical |
| `workflow-runs.json` | Legacy workflow run records; SQLite is canonical |
| `forge-proposals.json` | Forge proposal records |
| `forge-installed.json` | Installed Forge skill records |
| `go-skill-states.json` | Skill enable/disable overrides |
| `action-policies.json` | Per-action approval policies |
| `fs-roots.json` | Approved filesystem roots |
| `api-validation-history.json` | API validation audit log (max 100) |

**Keychain** — `com.projectatlas.credentials` / account `bundle` → JSON blob with all API keys.
**Vault** — `com.projectatlas.vault` / account `credentials` → JSON array of VaultEntry.

---

## 11. Auth Model

| Request type | Auth mechanism |
|-------------|----------------|
| Localhost (loopback peer) | Bypass — process-trust model |
| Remote LAN (`remoteAccessEnabled: true`) | `/auth/remote-gate` + `POST /auth/remote` with remote access key (Keychain), issuing remote session cookie |
| Remote LAN transport | HTTPS required for non-Tailscale remote requests (or trusted loopback TLS-terminating proxy) |
| Remote state-changing calls | Session-bound CSRF token from `GET /auth/csrf` required in `X-CSRF-Token` header |
| Tailscale (`tailscaleEnabled: true`) | Direct Tailnet peer trust path (no Atlas remote key/session required) |
| Web sessions | HMAC-SHA256 launch token bootstrap (`/auth/token` local-only, `/auth/bootstrap`) persisted in `web_sessions`, validated by `RequireSession` |

---

## 12. Deferred (V1.0)

| Feature | Status |
|---------|--------|
| Multi-agent supervisor | Not built — single-agent loop handles all turns |
| Custom skill live-reload | Daemon restart required after install or remove |
| Custom skill ZIP/URL install | Local path only; URL download deferred |
| Custom skill vault credential injection | Skills read credentials from env; direct vault injection deferred |
| Embedding-based memory retrieval | BM25 FTS5 keyword search implemented; vector/embedding retrieval deferred |
