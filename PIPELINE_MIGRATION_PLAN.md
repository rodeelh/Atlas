# Atlas Chat/Agent Pipeline Rewrite — Implementation Plan

**Status:** In progress  
**Strategy:** Incremental (phases 1–6 complete/partial; phases 7–12 address all remaining open items)  
**Created:** 2026-04-17  
**Updated:** 2026-04-17 (phases 7–12 added after full scorecard audit)

---

## Decisions

| Decision | Choice |
|---|---|
| Migration | Incremental — each phase ships behind the existing API surface |
| Provider I/O | `<-chan TurnEvent` — channel-based, ctx-cancellable |
| Loop FSM | Named states, one method per state |
| Pipeline state | Single mutable `TurnState` struct |
| Post-turn hooks | `HookRegistry` registered in `main.go` |
| Tool selection | Stateful per-turn `ToolSelector`, `LazySelector` owns upgrade-stage |

---

## Known Limitations & Constraints

### L1 — Phase 6 keep-list (non-streaming functions MUST NOT be deleted)

The adapter layer (Phase 1 / Phase 4) replaces **streaming** functions only.
The following functions in `internal/agent/provider.go` are called by non-agentic
background subsystems and must survive Phase 6 cleanup:

| Function | Called by |
|---|---|
| `CallAINonStreamingExported` | `forge/service.go`, `chat/service.go` (RegenerateMind), `modules/dashboards/live_runner.go` |
| `CallVision` | browser skill screenshot analysis |
| `callAINonStreaming` (internal) | dispatched by all non-streaming exported callers |
| `callAnthropicNonStreaming` | via `callAINonStreaming` |
| `callOpenAICompatNonStreaming` | via `callAINonStreaming` |
| `callOpenAIResponsesNonStreaming` | via `callAINonStreaming` |
| `NonAgenticUsageHook` var | wired in `NewService`, fires for every non-agentic call |

**Action in Phase 6:** Split `provider.go` into:
- `provider_nonstreamng.go` — all of the above (kept forever)
- `provider_streaming.go` — old streaming functions (deleted after Phase 4 adapters own HTTP)

### L2 — Three global seams between `chat` and `agents` (do not move)

The agents module wires three things into the `chat` package at startup.
These are intentional seams — they must stay in place through all phases:

| Seam | Where set | Where read | Migration note |
|---|---|---|---|
| `chat.RosterReader` | `agents/module.go Register()` | `buildSystemPrompt()` | Must stay callable from `buildInput` stage (Phase 3). Do not inline away. |
| `chat.AsyncFollowUpSender` | `chat.NewService` → `s.SendProactive` | async delegation goroutines in agents module | NOT a post-turn hook. Do not move to HookRegistry. Stays on Service throughout. |
| `chat.WithOriginConvID` / `chat.OriginConvIDFromContext` | `HandleMessage` (Phase 3: `execute` stage) | agents module async delegation | Context key stays in `chat` package. Only call-site moves from inline to `execute` stage. |

### L3 — Workflows ToolPolicy must flow into NewSelector

Workflows pass a `ToolPolicy` in `MessageRequest`. `NewSelector` must accept the
`ToolPolicy` from `LoopConfig` so `HeuristicSelector` and `LazySelector` honour
the `AllowedToolPrefixes` constraint the same way `selectTurnTools` does today.
Failing to thread this breaks workflow tool restrictions silently.

### L4 — Dashboards bypass the pipeline entirely

`modules/dashboards/live_runner.go` (`AILiveComputeRunner`) calls
`agent.CallAINonStreamingExported` directly — it never goes through
`HandleMessage` or the agent loop. It is unaffected by all phases.
Do not try to route it through the pipeline.

### L5 — Non-streaming callers use AddSessionTokens / NonAgenticUsageHook

`callAINonStreaming` calls `AddSessionTokens` and fires `NonAgenticUsageHook`
for token tracking. These hooks must remain in the non-streaming path after Phase 6
splits `provider.go`. They are not part of the pipeline's `HookRegistry`.

---

## Phase 1 — Provider Adapters

**Goal:** Define the `ProviderAdapter` interface and wrap each existing provider in a
concrete adapter. The agent loop is **not changed** — adapters are new code alongside
`provider.go`.

### New files

| File | Purpose |
|---|---|
| `internal/agent/adapter.go` | `TurnRequest`, `TurnEvent`, `EventType`, `ProviderAdapter` interface |
| `internal/agent/channel_emitter.go` | Bridge: adapts existing `Emitter` interface → `chan TurnEvent` |
| `internal/agent/adapter_factory.go` | `NewAdapter(ProviderConfig) ProviderAdapter` |
| `internal/agent/adapter_anthropic.go` | Wraps `streamAnthropicWithToolDetection` |
| `internal/agent/adapter_openai.go` | Wraps `streamOpenAIResponsesWithToolDetection` |
| `internal/agent/adapter_oaicompat.go` | Wraps `streamOpenAICompatWithToolDetection` |
| `internal/agent/adapter_local.go` | Wraps `nonStreamingAsStream` (llama, LM Studio, Ollama) |
| `internal/agent/adapter_mlx.go` | Wraps `mlxStreamer.Stream` |
| `internal/agent/adapter_test.go` | Unit tests per adapter via mock HTTP server |

### No changes to existing files

`provider.go`, `loop.go`, `service.go` — untouched in Phase 1.

### Migration seam

`NewAdapter` is callable but nothing in the loop calls it yet.

### Gate condition

- `go build ./...` clean
- All adapter tests green
- No modifications to `provider.go`, `loop.go`, `service.go`

---

## Phase 2 — Tool Selector Interface

**Goal:** Define `ToolSelector`, move lazy upgrade-stage state into `LazySelector`.
Pass selector through `LoopConfig`. Loop's `resolveToolUpgrade` is replaced; all else unchanged.

### New files

| File | Purpose |
|---|---|
| `internal/chat/selector.go` | `ToolSelector` interface, `RequestToolsArgs`, `NewSelector` factory |
| `internal/chat/selector_lazy.go` | `lazySelector` — stateful, owns upgrade stage |
| `internal/chat/selector_heuristic.go` | `heuristicSelector` — `registry.SelectiveToolDefs` |
| `internal/chat/selector_llm.go` | `llmSelector` — existing `selectToolsWithLLM` call |
| `internal/chat/selector_identity.go` | `identitySelector` — returns full tool list |
| `internal/chat/selector_test.go` | Unit tests for all selector types |

### Changes to existing files

`internal/agent/loop.go`:
- Add `Selector ToolSelector` field to `LoopConfig` (nil = backward compat)
- Replace `resolveToolUpgrade` call-site with `cfg.Selector.Upgrade(args)` when non-nil
- Old `resolveToolUpgrade` + `toolUpgradeStage` remain as dead-code fallback until Phase 6

`internal/chat/service.go`:
- `selectTurnTools` returns `(ToolSelector, []map[string]any, string)` instead of `([]map[string]any, string)`
- Set `loopCfg.Selector = sel`
- `NewSelector` receives `req.ToolPolicy` to honour workflow restrictions (L3)

### Gate condition

- Build clean
- Selector tests green
- Existing loop tests green (backward compat path not broken)

---

## Phase 3 — Turn Pipeline

**Goal:** Decompose `HandleMessage` into pipeline stages. `HandleMessage` becomes a thin
shell. `Resume()` re-enters at `execute` stage — no more duplication.

### New files

| File | Purpose |
|---|---|
| `internal/chat/pipeline.go` | `TurnState`, `Pipeline`, all stage methods |
| `internal/chat/hooks.go` | `TurnRecord`, `TurnHook`, `HookRegistry` (Phase 5 wires hooks; Phase 3 creates the registry, `postTurn` fires goroutines directly as before) |

### Pipeline stages

| Stage | Responsibility |
|---|---|
| `prepareContext` | Resolve/create convID, save user message, load history, run engagement classifier |
| `resolveProvider` | Resolve primary + heavy-bg provider, vision degradation, OpenRouter image fallback |
| `buildInput` | Build system prompt (calls `RosterReader` — L2), windowed messages, capability policy |
| `selectTools` | Create `ToolSelector`, call `Initial()` |
| `execute` | Build `LoopConfig`, run `agentLoop.Run`, inject `WithOriginConvID` (L2), manage `turnCancels` |
| `persist` | Save assistant message, detect surfacings, scan generated files, emit SSE done |
| `postTurn` | Fire `HookRegistry` (Phase 3: direct goroutines; Phase 5: hook funcs) |

### Changes to existing files

`internal/chat/service.go`:
- `HandleMessage` → calls `s.pipeline.Run(ctx, req)`, returns `buildResponse(state)`
- `Resume()` → calls `s.pipeline.ResumeFrom(ctx, toolCallID, approved)`
- `Service` gains `pipeline *Pipeline` field, wired in `NewService`
- `AsyncFollowUpSender` wiring stays in `NewService` (L2 — not moved)

### Gate condition

- `HandleMessage` body ≤ 15 lines
- `Resume()` body ≤ 25 lines
- All existing tests green
- Approval/resume integration path validated

---

## Phase 4 — Agent Loop State Machine

**Goal:** Rewrite `loop.go` as a named-state machine. Cut loop over to use
`ProviderAdapter` channel. Old streaming functions in `provider.go` become dead code.

### State machine

```
stateStreaming → stateToolDispatch | stateLazyUpgrade | stateComplete | stateError
stateLazyUpgrade → stateStreaming (doesn't count as iteration)
stateToolDispatch → stateToolExec | stateAwaitingApproval
stateToolExec → stateStreaming (next iteration)
stateAwaitingApproval → (returns RunResult immediately)
stateComplete → (returns RunResult)
stateError → (returns RunResult)
```

### Key design points

- `doStream`: drains adapter channel, emits SSE from TurnEvents; `assistant_started` emitted by loop before draining
- `doLazyUpgrade`: calls `cfg.Selector.Upgrade`, appends messages, returns `stateStreaming`
- `doToolDispatch`: splits blocked/needApproval/canRun, returns `stateAwaitingApproval` or `stateToolExec`
- `doToolExec`: parallel stateless + serial stateful, appends tool result messages, returns `stateStreaming`
- `RunFromDeferred`: entry point for Resume — enters at `stateStreaming` with pre-appended tool results

### Adapter rewrite

Phase 1 adapters used `channelEmitter` to bridge existing functions. In Phase 4:
- Each adapter owns its HTTP logic directly (extracted from `provider.go`)
- `channelEmitter.go` is deleted
- The streaming functions in `provider.go` (`streamAnthropicWithToolDetection`, etc.) become dead code

The non-streaming functions in `provider.go` are NOT touched (L1).

### Gate condition

- `loop.go` has no `ProviderType` switch
- `loop.go` imports only `skills`, `storage`, `logstore` from internal packages (no HTTP)
- `loop_parallel_test.go` green
- All loop tests green

---

## Phase 5 — Post-turn Hook Registry

**Goal:** Remove `chat` package's direct imports of `memory` and `mind`.
Move hook registration to `main.go`.

### New/modified files

`internal/memory/hook.go` — `HookFunc(db, cfgStore) chat.TurnHook`  
`internal/mind/hook.go` — `ReflectHookFunc`, `LearnHookFunc`, `NapNotifyHook`  
`internal/chat/thought_surfacing.go` — expose `SurfaceHookFunc` or wire inline  
`cmd/atlas-runtime/main.go` — register all hooks in order matching current goroutine launch order  

### Changes to `service.go`

- Remove imports: `atlas-runtime-go/internal/memory`, `atlas-runtime-go/internal/mind`
- `postTurn` stage fires `p.hooks.Fire(bgCtx, record)` instead of direct goroutine calls
- `NewService` accepts `*HookRegistry` parameter

### Hook registration order in main.go

Must match current execution order to avoid behavioral regression:
1. `memory.HookFunc` (ExtractAndPersist)
2. `mind.ReflectHookFunc` (ReflectNonBlocking)
3. `mind.LearnHookFunc` (LearnFromTurnNonBlocking)
4. `mind.NapNotifyHook` (NotifyTurnNonBlocking)
5. thought surfacing (detectAndRecordSurfacings — sync, before hooks fire)

Note: `detectAndRecordSurfacings` is **synchronous** (must run before next turn's classifier).
It stays as a direct call in `persist` stage, not in the async hook registry.

### Gate condition

- `internal/chat/service.go` import block has no `memory` or `mind`
- Build clean
- Integration turn test verifies memory extraction still fires

---

## Phase 6 — Cleanup

**Goal:** Delete all dead code left by the migration. Enforce keep-list (L1).

### Files to split

`provider.go` → split into:
- `internal/agent/provider_types.go` — `ProviderConfig`, `ProviderType` constants, `TokenUsage`, `NonAgenticUsageHook`, `AddSessionTokens`
- `internal/agent/provider_nonstreaming.go` — all non-streaming functions (KEPT, see L1)
- *(streaming functions deleted — now live in adapter files)*

### Functions to delete

| Function | Location | Replaced by |
|---|---|---|
| `streamAnthropicWithToolDetection` | provider.go | `adapter_anthropic.go` |
| `streamOpenAICompatWithToolDetection` | provider.go | `adapter_oaicompat.go` + `adapter_local.go` |
| `streamOpenAIResponsesWithToolDetection` | provider.go | `adapter_openai.go` |
| `nonStreamingAsStream` | provider.go | `adapter_local.go` |
| `coalesceForLocalProvider` | provider.go | moved to `adapter_local.go` |
| `resolveToolUpgrade` | loop.go | `ToolSelector.Upgrade` (Phase 2) |
| `toolUpgradeStage` var | loop.go | owned by `LazySelector` |
| `channelEmitter` | channel_emitter.go | entire file deleted |
| `selectTurnTools` | service.go | `selectTools` pipeline stage |
| `buildTurnMessages` | service.go | `buildInput` pipeline stage |
| `shouldCompactHistory` | service.go | `buildInput` pipeline stage |

### Functions to keep (L1 — non-streaming)

`callAINonStreaming`, `callAnthropicNonStreaming`, `callOpenAICompatNonStreaming`,
`callOpenAIResponsesNonStreaming`, `CallAINonStreamingExported`, `CallVision`,
`NonAgenticUsageHook`, `AddSessionTokens` and all supporting types.

### Final import graph audit

- `internal/chat` → no `memory`, `mind` imports
- `internal/agent` → no `chat` import (circular)
- `internal/memory`, `internal/mind` → may import `agent` for provider calls (fine)
- `internal/modules/*` → still use `platform.AgentRuntime` (unchanged)

### Gate condition

- `go vet ./...` clean
- `go build ./...` clean
- `provider.go` deleted (replaced by split files)
- Full test suite green
- Migration scorecard generated

---

## Risk Register

| Risk | Mitigation |
|---|---|
| Phase 4 adapter rewrite breaks a provider | Each adapter has HTTP mock test before old functions deleted |
| Resume() regression after Phase 3 | Approval integration test added in Phase 3 before old code removed |
| Hook firing order changes behavior | Hooks registered in main.go in same order as current goroutine launches |
| MLX 3-level fallback lost in adapter rewrite | `adapter_mlx_test.go` tests empty-response path before old code deleted |
| Phase 6 deletes non-streaming functions | Keep-list (L1) explicitly checked before deletion |
| Workflow ToolPolicy silently broken | Phase 2 test verifies NewSelector respects AllowedToolPrefixes |
| stream/ package circular import | stream/ imports stdlib only; agent/ imports stream/ — no cycle possible |
| Shared HTTP helpers split incorrectly | All helpers used by both streaming + non-streaming stay in provider_nonstreaming.go |

---

## Architecture Decision Record — Phases 7–12

### ADR-1: Platform-first adapter architecture

Atlas is a platform, not a fixed-provider product. The adapter layer must accommodate
providers with meaningfully different streaming wire formats (Anthropic, OpenAI Responses,
OAI-compat, local non-streaming, and future providers not yet known).

**Decision:** Each adapter file owns its provider's complete streaming logic. Shared stable
primitives (SSE line parsing, OAI-compat delta assembly, tool-call accumulation) live in a
dedicated `internal/agent/stream/` subpackage. Adding a new provider = one new file.
Nothing else changes.

**Rejected alternatives:**
- *Split+relocate only* (keep shared streaming in `provider_streaming.go`): that file
  becomes a growth magnet for "almost-OAI-compatible but slightly different" providers,
  recreating the `provider.go` monolith problem.
- *Full extraction with duplication*: copies shared SSE parsing into each adapter,
  creating drift when the wire format evolves.

### ADR-2: Zero-value Selector default, hard enforce Adapter

`LoopConfig.Adapter` is already hard-enforced (nil = panic at drainAdapter).
`LoopConfig.Selector` will be soft-enforced: `nil` is normalised to `identitySelector{}`
at the top of `Loop.Run()`. This gives a consistent no-selector semantic (full tool list,
no upgrade) without breaking tests that build `LoopConfig` manually.

`resolveToolUpgrade` and `toolUpgradeStage` are deleted. The backward-compat path is
removed entirely — the Selector interface is the sole upgrade mechanism.

### ADR-3: Phase 5 gate correction

The original Phase 5 gate ("service.go has no memory or mind imports") was overclaiming.
Two uses remain in `service.go` and are CORRECT:

- `mind.ReadThoughtsSection` in `lookupThoughtBody` — engagement classifier read, not
  pipeline post-turn work. Cannot be a hook (it is synchronous and called during prepareContext).
- `memory.ExtractRegexOnly` in `buildTurnMessages` — compaction path read, fires during
  history trimming. Not a post-turn side effect.

The correct gate is: **`pipeline.go` has no `memory` or `mind` imports** — already met.

---

## Phase 7 — `stream/` Subpackage Foundation

**Goal:** Create the shared streaming primitives package with zero changes to existing code.
All existing tests pass unmodified. This phase establishes the platform-ready foundation
that all adapter rewrites (Phase 8) will build on.

### Architectural boundary

`internal/agent/stream/` is a pure parsing library. It:
- Imports stdlib only (`bufio`, `bytes`, `context`, `encoding/json`, `io`, `strings`)
- Has NO dependency on `internal/agent` (no circular import possible)
- Exposes clean Go types — adapters convert stream types → `TurnEvent` at the boundary

### New files

| File | Contents |
|---|---|
| `internal/agent/stream/sse.go` | `Scanner` — reads `io.Reader`, yields `data:` line contents, stops on `[DONE]` or ctx cancel |
| `internal/agent/stream/tool_assembly.go` | `ToolAssembler` — accumulates partial `tool_calls` deltas from OAI streaming into complete assembled tool calls. Used by OAI-compat and Responses API adapters. |
| `internal/agent/stream/oaicompat.go` | `ParseOAICompatStream(ctx, r io.Reader) <-chan Chunk` — full OAI chat-completions streaming parser. Emits `Chunk` values (text delta, tool delta, usage, finish). Used by `adapter_oaicompat.go` and `adapter_mlx.go`. |
| `internal/agent/stream/types.go` | `Chunk`, `ToolDelta`, `StreamUsage` — stream-internal types returned by the parsers. No dependency on agent types. |
| `internal/agent/stream/stream_test.go` | Unit tests for `Scanner`, `ToolAssembler`, `ParseOAICompatStream` against mock payloads |

### No changes to existing files

`provider.go`, all `adapter_*.go`, `channel_emitter.go`, `loop.go` — untouched.

### Key design: `Chunk` type

```
Chunk {
    TextDelta    string         // non-empty on text delta events
    FinishReason string         // non-empty on terminal event
    ToolDelta    *ToolDelta     // non-nil on tool_calls delta event
    Usage        *StreamUsage   // non-nil on usage event
    Err          error          // non-nil on parse error (terminal)
}
```

Adapters iterate the channel and convert each `Chunk` → `TurnEvent`.

### Gate condition

- `go build ./internal/agent/stream/...` clean
- `stream_test.go` green — `Scanner`, `ToolAssembler`, `ParseOAICompatStream` tested
  against hand-crafted SSE payloads (text chunks, tool call assembly, usage line, `[DONE]`)
- Zero changes to `provider.go`, existing adapter files, or loop.go
- No import cycle: `internal/agent` does NOT import `internal/agent/stream` yet

---

## Phase 8 — Adapter Full Rewrite

**Goal:** Each adapter file owns its provider's complete HTTP + streaming logic.
`channel_emitter.go` is deleted. All adapters use `stream/` primitives directly.
Production streaming path is fully self-contained per provider.

### Design principle

Shared HTTP helpers (`doOpenAICompatRequest`, `applyOpenAICompatHeaders`,
`convertToAnthropicMessages`, etc.) remain in the `agent` package — they are called
by both streaming AND non-streaming paths and must not be split. Adapters call them
as package-internal functions. No duplication.

What moves INTO each adapter file is only the streaming response parsing loop —
the code that consumes the HTTP response body and emits events.

### File changes

#### `adapter_oaicompat.go` — rewrite

Before: bridges `streamOpenAICompatWithToolDetection` via `channelEmitter`.  
After: owns the streaming loop directly.

```
1. Build request body (OAI completions format)
2. Call doOpenAICompatRequest (shared HTTP helper — stays in provider_nonstreaming.go)
3. Pipe response body through stream.ParseOAICompatStream(ctx, resp.Body)
4. Convert each Chunk → TurnEvent, send to channel
5. Assemble tool calls from ToolDeltas via stream.ToolAssembler
```

#### `adapter_openai.go` — rewrite

Before: bridges `streamOpenAIResponsesWithToolDetection` via `channelEmitter`.  
After: owns Responses API streaming loop.

```
1. Build Responses API request body (convertChatToolsToResponsesTools, etc.)
2. Call doOpenAIResponsesRequest (shared — stays in provider_nonstreaming.go)
3. Parse Responses API SSE events (different event types than completions)
4. Convert → TurnEvent channel
```

Note: Responses API uses different SSE event types (`response.output_text.delta`,
`response.completed`, etc.) — NOT OAI-compat format. This adapter uses `stream.Scanner`
for SSE line reading but writes its own event parsing, not `stream.ParseOAICompatStream`.

#### `adapter_anthropic.go` — rewrite

Before: bridges `streamAnthropicWithToolDetection` via `channelEmitter`.  
After: owns full Anthropic streaming loop.

```
1. Build Anthropic request (convertToAnthropicMessages, anthropicCachedTools — shared)
2. POST to api.anthropic.com/v1/messages with stream:true
3. Parse Anthropic SSE events (content_block_delta, message_delta, etc.) via stream.Scanner
4. Convert → TurnEvent channel
```

Anthropic tool call format differs from OAI — adapter owns its own `ToolAssembler`
logic or uses `stream.ToolAssembler` if the delta structure is compatible (evaluate
during implementation).

#### `adapter_local.go` — rewrite

Before: bridges `nonStreamingAsStream` via `channelEmitter`.  
After: owns the non-streaming → channel path directly.

```
1. coalesceForLocalProvider (moves INTO this file — local-only concern)
2. callOpenAICompatNonStreaming (shared — stays in provider_nonstreaming.go)
3. Emit result as single TurnEvent sequence (started → text delta → done)
```

#### `adapter_mlx.go` — rewrite

Before: bridges `mlxStreamer{}` (3-level fallback) via `channelEmitter`.  
After: owns the 3-level retry directly using `stream.ParseOAICompatStream` as the base.

```
1. Apply MLX request options (applyMLXRequestOptions — stays in provider_nonstreaming.go)
2. Attempt streaming via stream.ParseOAICompatStream
3. On empty result: retry with callOpenAICompatNonStreaming + tools
4. On second empty result: retry without tools
5. Emit results → TurnEvent channel
```

`mlxStreamer` struct is deleted from `provider.go`.

### Deleted files

- `internal/agent/channel_emitter.go` — replaced by direct channel management in each adapter

### Gate condition

- `go build ./...` clean
- All existing `adapter_test.go` tests pass unmodified
- New per-adapter streaming tests added covering: text stream, tool call assembly,
  context cancel, usage reporting
- `channel_emitter.go` is absent from the repo
- `provider.go` still exists (deletion is Phase 9) but `mlxStreamer` is gone from it
- `streamAnthropicWithToolDetection`, `streamOpenAIResponsesWithToolDetection`,
  `streamOpenAICompatWithToolDetection`, `nonStreamingAsStream` still present in
  `provider.go` but no longer called by any adapter

---

## Phase 9 — `provider.go` Elimination & Reorganisation

**Goal:** Split the 1,930-line `provider.go` monolith. Delete the now-orphaned streaming
functions. `provider.go` is deleted. Every function has a clear, named home.

### File structure after Phase 9

| New file | Contents | LOC (est.) |
|---|---|---|
| `internal/agent/provider_types.go` | `ProviderType` constants, `ProviderConfig`, `TokenUsage`, `streamResult`, `MLXRequestOptions`, `NonAgenticUsageHook`, `AddSessionTokens`, `isLocalProvider` | ~120 |
| `internal/agent/provider_nonstreaming.go` | All L1 keep-list functions + ALL shared HTTP helpers used by both paths | ~900 |
| `internal/agent/adapter_anthropic.go` | Rewritten in Phase 8; now also contains Anthropic-specific HTTP helpers (`convertToAnthropicMessages`, `anthropicCachedTools`, etc.) if not shared | — |
| `internal/agent/adapter_oaicompat.go` | Rewritten in Phase 8 | — |

### Functions deleted in Phase 9

| Function | Location | Status |
|---|---|---|
| `streamAnthropicWithToolDetection` | provider.go:1747 | ✂️ Deleted — owned by `adapter_anthropic.go` |
| `streamOpenAIResponsesWithToolDetection` | provider.go:920 | ✂️ Deleted — owned by `adapter_openai.go` |
| `streamOpenAICompatWithToolDetection` | provider.go:1207 | ✂️ Deleted — owned by `adapter_oaicompat.go` via `stream/oaicompat.go` |
| `nonStreamingAsStream` | provider.go:294 | ✂️ Deleted — owned by `adapter_local.go` |
| `coalesceForLocalProvider` | provider.go:336 | ✂️ Deleted — moved to `adapter_local.go` |
| `mlxStreamer` struct + `Stream()` | provider.go:234 | ✂️ Deleted — logic absorbed into `adapter_mlx.go` |

### Functions kept (L1) — move to `provider_nonstreaming.go`

`CallVision`, `CallAINonStreamingExported`, `callAINonStreaming`,
`callAnthropicNonStreaming`, `callOpenAICompatNonStreaming`,
`callOpenAIResponsesNonStreaming`, `NonAgenticUsageHook`, `AddSessionTokens`

### Shared HTTP helpers — move to `provider_nonstreaming.go`

These are called by BOTH streaming (via adapters) and non-streaming paths. They must
remain accessible to the entire `agent` package:

`oaiCompatBaseURL`, `applyOpenAICompatHeaders`, `openAICompatHTTPClient`,
`isTransientLocalRequestError`, `doOpenAICompatRequest`, `withProviderTimeout`,
`responsesBaseURL`, `doOpenAIResponsesRequest`, `convertChatToolsToResponsesTools`,
`convertMessageContentPartsToResponses`, `extractSystemInstructions`,
`convertMessagesToResponsesInput`, `extractResponsesMessage`,
`acquireMLXRequestGate`, `mergedMLXChatTemplateKwargs`, `applyMLXRequestOptions`

### Anthropic helpers — move to `adapter_anthropic.go`

These are ONLY used by the Anthropic streaming path (no non-streaming caller):
`anthropicCachedSystem`, `anthropicCachedTools`, `convertContentToAnthropic`,
`convertToAnthropicMessages`, `convertToAnthropicTools`

Exception: `callAnthropicNonStreaming` uses `convertToAnthropicMessages` —
if this creates a cross-file dependency concern, move Anthropic helpers to
`provider_nonstreaming.go` instead. Evaluate during implementation.

### Gate condition

- `provider.go` does not exist in the repo
- `go build ./...` clean
- `go vet ./...` clean
- All tests green
- `grep -r "provider\.go" .` finds no references

---

## Phase 10 — Loop Selector Cleanup

**Goal:** Delete the backward-compat `resolveToolUpgrade` / `toolUpgradeStage` path.
Enforce `identitySelector{}` as the zero-value default for nil Selectors.

### Changes to `internal/agent/loop.go`

1. Add zero-value normalisation at the top of `Loop.Run()`:

```go
if cfg.Selector == nil {
    cfg.Selector = identitySelector{}
}
```

2. Delete `resolveToolUpgrade` method (~14 lines, loop.go:1024)

3. Delete `toolUpgradeStage int` local var declaration and all its usages (~6 lines)

4. Remove the backward-compat comment from `LoopConfig.Selector` field:
   > "Nil activates the backward-compat resolveToolUpgrade path."  
   → replace with: "Nil is normalised to identitySelector{} (full tool list, no upgrade)."

5. `identitySelector{}` must be visible from the `agent` package. Currently it lives in
   `internal/chat/selector_identity.go`. Two options:
   - Move `identitySelector` to `internal/agent/selector.go` (it already has `ToolSelector` interface)
   - Keep it in `chat` and pass `agent.NewIdentitySelector()` factory function

   **Decision:** Move `identitySelector` to `internal/agent/selector.go`. It belongs
   alongside the interface definition, not in `chat`. The `chat` package re-exports via
   `NewSelector` (which already returns `identitySelector{}` for unknown modes).

### Changes to `internal/chat/selector_identity.go`

- Delete the file — `identitySelector` now lives in `internal/agent/selector.go`
- Update `NewSelector` in `internal/chat/selector.go` to return `agent.IdentitySelector{}`
  (exported name, or keep unexported and wrap)

### Gate condition

- `resolveToolUpgrade` absent from `loop.go`
- `toolUpgradeStage` absent from `loop.go`
- Build clean, all selector tests green
- Tests that build `LoopConfig{}` without a Selector still pass (zero-value default kicks in)
- `go vet ./...` clean

---

## Phase 11 — L3 ToolPolicy Enforcement in Selectors

**Goal:** Wire `ToolPolicy.AllowedToolPrefixes` into all selector implementations.
Workflow-scoped runs correctly restrict available tools. Currently the `_` parameter
is accepted but ignored — this is a silent correctness bug for multi-agent workflows.

### Background

`MessageRequest.ToolPolicy` carries an `AllowedToolPrefixes` slice set by the workflows
module when executing a workflow step. The intent: restrict the agent to only the tools
the workflow declared. Today this field reaches `NewSelector` but is silently dropped.

### Changes

#### `internal/chat/selector.go`

Remove the `_` discard and thread `policy` into each selector:

```go
func NewSelector(mode string, policy *agent.ToolPolicy, ...) agent.ToolSelector {
    switch mode {
    case "lazy":
        return &lazySelector{..., policy: policy}
    case "llm":
        return &llmSelector{..., policy: policy}
    case "heuristic":
        return &heuristicSelector{..., policy: policy}
    default:
        return agent.IdentitySelector{policy: policy}
    }
}
```

#### `internal/chat/selector_lazy.go`, `selector_heuristic.go`, `selector_llm.go`

Each selector holds `policy *agent.ToolPolicy`. In `Initial()` and `Upgrade()`:

```go
tools := reg.ToolDefinitions()
if s.policy != nil && len(s.policy.AllowedToolPrefixes) > 0 {
    tools = reg.FilteredByPatterns(s.policy.AllowedToolPrefixes).ToolDefinitions()
}
```

`FilteredByPatterns` already exists in `internal/skills/registry.go` — no new
infrastructure needed.

#### `internal/agent/selector.go` — `IdentitySelector`

`IdentitySelector.Initial()` currently returns nil (full list from loop default).
With L3 enforcement, if a policy is set it must filter:

```go
func (s IdentitySelector) Initial() []map[string]any {
    if s.policy != nil && len(s.policy.AllowedToolPrefixes) > 0 {
        // Return filtered list — loop will use this instead of full registry
        // Implementation: IdentitySelector needs registry access, OR
        // enforcement happens at the loop level in selectTools step
    }
    return nil
}
```

**Note:** `IdentitySelector.Initial()` returning nil means "loop uses full tool list."
For policy enforcement to work with `identitySelector`, enforcement may need to happen
in the pipeline's `selectTools` stage (which already has registry access) rather than
inside the selector itself. Evaluate during implementation — the gate is behavioral
correctness, not a specific implementation shape.

#### `internal/chat/selector_test.go`

Add tests:
- `TestSelector_PolicyFiltersTools` — verify that a selector with `AllowedToolPrefixes: ["fs"]`
  only returns `fs.*` tools from `Initial()` and `Upgrade()`
- `TestNewSelector_NilPolicyUnrestricted` — nil policy returns full tool list

### Gate condition

- `NewSelector` no longer has `_` discard for `policy` parameter
- `selector_test.go` tests for policy filtering pass
- Workflow integration: a `MessageRequest` with `ToolPolicy{AllowedToolPrefixes: ["fs"]}` 
  results in the loop only seeing `fs.*` tools
- Build clean, all selector tests green

---

## Phase 12 — Phase 5 Gate Closure & Scorecard

**Goal:** Close all open audit items. Correct the Phase 5 gate definition.
Generate final migration scorecard.

### Phase 5 gate correction

The original gate ("service.go has no memory or mind imports") is incorrect.
Two uses are legitimate service-level concerns, not pipeline side-effects:

| Call site | Package | Why it stays |
|---|---|---|
| `mind.ReadThoughtsSection` in `lookupThoughtBody` | `mind` | Synchronous classifier read during `prepareContext`. Not a post-turn hook. Cannot be async. |
| `memory.ExtractRegexOnly` in `buildTurnMessages` | `memory` | Compaction-path read during `buildInput`. Fires when history is trimmed. Not a post-turn side effect. |

**Corrected gate:** `internal/chat/pipeline.go` has no `memory` or `mind` imports. ✅ (already met)

### Final import graph audit (target state after all phases)

```
internal/agent/stream/    → stdlib only
internal/agent/           → stream/, skills, storage, logstore, config, capabilities
internal/chat/            → agent/, skills, storage, config, memory*, mind*
                             (* two non-pipeline uses, intentionally kept)
internal/memory/          → agent/, config, storage, logstore
internal/mind/            → agent/, config, logstore
internal/modules/*        → platform contracts only
cmd/atlas-runtime/        → all of the above (wiring layer)
```

### Final file inventory — `internal/agent/` after all phases

| File | Status |
|---|---|
| `stream/sse.go` | ✅ New |
| `stream/oaicompat.go` | ✅ New |
| `stream/tool_assembly.go` | ✅ New |
| `stream/types.go` | ✅ New |
| `stream/stream_test.go` | ✅ New |
| `adapter.go` | ✅ Unchanged |
| `adapter_factory.go` | ✅ Minor (comment update) |
| `adapter_anthropic.go` | ✅ Rewritten |
| `adapter_openai.go` | ✅ Rewritten |
| `adapter_oaicompat.go` | ✅ Rewritten |
| `adapter_local.go` | ✅ Rewritten |
| `adapter_mlx.go` | ✅ Rewritten |
| `adapter_test.go` | ✅ Extended |
| `provider_types.go` | ✅ New (split from provider.go) |
| `provider_nonstreaming.go` | ✅ New (split from provider.go) |
| `provider.go` | ✂️ Deleted |
| `channel_emitter.go` | ✂️ Deleted |
| `loop.go` | ✅ Minor (`resolveToolUpgrade` deleted, nil-selector default added) |
| `selector.go` | ✅ Minor (`IdentitySelector` added, moved from chat) |

### Gate condition (final)

- `go build ./...` clean
- `go vet ./...` clean
- `provider.go` absent
- `channel_emitter.go` absent
- `resolveToolUpgrade` absent from `loop.go`
- `pipeline.go` has no `memory` or `mind` imports
- `NewSelector` threads `ToolPolicy` into all selectors
- Full test suite green (integration timeout pre-existing — excluded)
- This document updated with final scorecard

---

## Execution Order & Dependencies

```
Phase 7  (stream/ foundation)     — no dependencies, safe to start immediately
    ↓
Phase 8  (adapter rewrite)        — requires Phase 7
    ↓
Phase 9  (provider.go deletion)   — requires Phase 8 (streaming functions must be gone from adapters)
    ↓
Phase 10 (loop cleanup)           — requires identitySelector in agent package (Phase 10 itself)
                                    independent of Phases 7–9
Phase 11 (L3 ToolPolicy)          — independent of 7–9, can run in parallel after Phase 10
Phase 12 (closure)                — requires all above
```

Phases 10 and 11 are independent of the adapter rewrite and can be done at any time.
Phases 7 → 8 → 9 must be sequential.
