# Atlas Agent Boundary

**Last updated: 2026-04-17**

This document defines the architectural boundary for the Atlas `Agent` subsystem.

In the current codebase, most of this subsystem still lives in `atlas-runtime/internal/chat/`.
We intentionally use the term **Agent** here because the subsystem is no longer just “chat.”

It is the primary orchestration layer for Atlas.

---

## Purpose

The Agent is the operational center of Atlas.

It is the subsystem that:
- receives user or system intent
- builds turn context
- chooses providers and tools
- orchestrates execution
- pauses and resumes for approvals
- emits turn progress and final output
- triggers post-turn learning and memory work

The Agent must remain cohesive.

We do **not** want to fragment it across unrelated packages just to make file boundaries look cleaner.

---

## Current Code Mapping

The Agent boundary is represented by:

**Turn orchestration (`internal/chat/`)**
- `pipeline.go` — `Pipeline` struct: ordered turn stages (buildInput → selectTools → execute → postTurn)
- `service.go` — `HandleMessage`, `RegenerateMind`, `ResolveProvider`, `Resume`; `selectTurnTools`
- `hooks.go` — `HookRegistry`: post-turn hook adapters for memory, MIND reflection, skills learning
- `selector.go` — `NewSelector` factory; `scopedSelector` for policy-filtered tool sets
- `selector_lazy.go` / `selector_heuristic.go` / `selector_llm.go` — mode-specific `ToolSelector` implementations
- `tool_router.go` — `selectToolsWithLLM` — background LLM call + capability-group manifest
- `keychain.go` — `resolveProvider` — config + Keychain → ProviderConfig
- `broadcaster.go` — SSE fan-out to connected clients

**Agent loop + adapters (`internal/agent/`)**
- `loop.go` — `Loop.Run`: streaming FSM (drain → tool-exec → upgrade → next-turn)
- `adapter.go` — `ProviderAdapter` interface, `TurnEvent` / `TurnRequest` types
- `adapter_factory.go` — `NewAdapter(ProviderConfig)`
- `adapter_openai.go` / `adapter_anthropic.go` / `adapter_oaicompat.go` / `adapter_mlx.go` / `adapter_local.go` — per-provider stream adapters
- `selector.go` — `ToolSelector` interface + `IdentitySelector` zero-value default
- `stream/` — stdlib-only SSE primitives (`Scanner`, `Assembler`, `ParseOAICompatStream`)

**HTTP surface**
- `internal/domain/chat.go` — `/message`, `/conversations`, `/memories`, `/mind`, `/skills-memory`

The package names are still `chat` and `agent`, but architecturally this whole assembly should be understood as the **Agent** subsystem.

---

## Core Rule

The Agent should own orchestration.

The Agent should **not** own everything it orchestrates.

Use this rule when deciding ownership:
- if something defines, prepares, executes, pauses, resumes, or completes a turn, it probably belongs in Agent
- if something is a capability, infrastructure service, storage layer, or admin/system concern, it probably belongs outside Agent

---

## What Belongs In Agent

These concerns belong inside the Agent subsystem:

- turn intake
- turn request/response types
- conversation continuity
- history windowing and trim policy
- system prompt assembly
- memory recall for the current turn
- provider resolution for the current turn
- tool selection policy
- agent loop orchestration
- approval interruption/resume handling
- SSE / turn progress emission
- attachment handling policy
- post-turn triggers for memory extraction, reflection, and skills learning
- turn-level observability and token usage recording

These are all parts of the turn lifecycle.

---

## What Should Stay Outside Agent

These concerns should remain outside Agent even when Agent depends on them:

- auth/session
- system control and runtime admin APIs
- config persistence
- model catalog fetching
- engine process management
- credential/keychain storage
- link preview fetching
- location and preferences storage
- module registry and module lifecycle
- feature modules
- storage implementation
- communications platform lifecycle
- mind implementation details
- memory extraction/storage implementation details
- skills registry implementation details

These are systems the Agent uses, not systems the Agent should absorb.

---

## Borderline Areas

Some areas are split by trigger vs implementation.

### Memory

- belongs in Agent:
  - recall during a turn
  - deciding when to trigger extraction after a turn

- stays outside Agent:
  - extraction implementation
  - storage/query implementation

### Mind / Reflection / Skills Learning

- belongs in Agent:
  - deciding when these run after a turn

- stays outside Agent:
  - reflection and learning implementation

### Approvals

- belongs in Agent:
  - turn pause/resume semantics
  - continuation after approval resolution

- stays outside Agent:
  - approval queue product surface
  - policy storage and management routes

### Communications

- belongs in Agent:
  - normalized turn ingress once a bridge hands off a request

- stays outside Agent:
  - Telegram/Discord/WhatsApp/Slack lifecycle, setup, validation, and credentials

---

## Ingress Model

Multiple surfaces feed the Agent:
- web UI
- TUI
- communications bridges
- internal runtime triggers such as automations and workflows

This is why the Agent should not be treated as a normal feature service.

It is the central intent orchestration layer for Atlas.

---

## Teams Principle

Atlas Teams V1 is implemented. The architectural rule that governs it:

**Agent owns delegation decisions. Teams owns delegated execution.**

This rule is implemented and must not be violated by future changes.

That means:
- Agent decides whether work should be delegated
- Agent defines the goal, scope, and success criteria (via `DelegationPlan` / `DelegationTaskSpec`)
- Agent remains accountable for the final outcome returned to the user
- Teams validates feasibility, persists delegated tasks, runs workers, and tracks state
- Teams reports completed work back to Agent as a standard tool call result

This rule must not be inverted:
- Teams must not become the top-level decision maker
- Agent must not absorb the worker-management system

### Team Management Boundary

Agent may call:
- `team.list`, `team.get` — inspect roster
- `team.delegate` — delegate a task (single/sequence patterns)
- `agent.create`, `agent.update`, `agent.delete`, `agent.enable`, `agent.disable`, `agent.pause`, `agent.resume` — manage team members

Teams owns how those operations write to the SQLite `agent_definitions` table, initialize runtime state, and update task/event records. AGENTS.md is no longer in the write path — it is export-only.

Explicit requests like "create an agent" or "add a teammate" must resolve to team management, not silently fall back to workflow or automation creation.

### Delegation Mechanism (implemented)

1. Atlas (the Agent) decides a task is suitable for a specialist — this decision is made inside the agent loop, not by any external trigger.
2. Atlas calls `team.delegate` with a `DelegationPlan` — a structured input containing pattern (`single`/`sequence`), execution mode (`sync_assist`/`async_assignment`), and per-task specs (agent ID, title, objective, scope, success criteria, expected output).
3. `team.delegate` is registered by `modules/agents` via `skillsReg.RegisterExternal()` during `Register()`.
4. The skill handler validates the plan, resolves agent definitions from SQLite, and calls `delegateTask()` per step.
5. `delegateTask()` runs the sub-agent via `agent.Loop.Run()` directly — **not** via `AgentRuntime.HandleMessage()`.
6. For `async_assignment`, the task ID is pre-generated before the goroutine spawns, so it can be returned in the immediate tool result.
7. For `sequence`, each step calls `specToDelegateArgs()` to preserve the full `DelegationTaskSpec` metadata (scope, success criteria, expected output) — not flattened to a bare task string.
8. The sub-agent result comes back as a standard `ToolResult` in Atlas's conversation.
9. Atlas interprets the result and presents it to the user.

**Why `AgentRuntime.HandleMessage()` must not be used for sub-agents:**
- `HandleMessage` routes through `chat.Service`: conversation persistence, SSE broadcasting, memory extraction, MIND reflection.
- None of those must happen for sub-agent work.
- Sub-agent messages are stored in `agent_task_steps`, not the `conversations` table.

**Import rule:**
The agents module imports `internal/agent` directly for `agent.Loop`, `agent.LoopConfig`, `agent.OAIMessage`. Same pattern as the Forge module.

**Tool filtering:**
Sub-agents receive a pre-filtered `*skills.Registry` built from the agent's `allowedSkills` patterns via `subRegistryFor()`. The model only sees its permitted tools.

**Worker prompt:**
`composeWorkerPrompt(def, task)` in `prompt.go` builds a four-section prompt:
- `## Identity` — template role contract + member name/role/mission/style
- `## Assignment` — title/objective (or goal fallback), scope, success criteria, expected output
- `## Context` — prior results and artifacts (omitted when empty)
- `## Execution contract` — template-specific rules that sub-agents must not pass on to their own sub-calls

Five template roles are defined: `scout`, `builder`, `reviewer`, `operator`, `monitor` (and `""` for default).

---

## Cohesion Policy

The Agent should remain one cohesive subsystem.

That means:
- keep Agent-owned code grouped under a single subsystem boundary
- prefer internal files within the same subsystem over scattering logic into unrelated packages
- avoid splitting Agent purely for cosmetic simplification
- optimize for debuggability and turn-trace clarity

If Agent internals are split, they should still remain **Agent-owned**.

Examples of acceptable internal organization:
- `prompt_builder.go`
- `provider_resolver.go`
- `turn_policy.go`
- `resume.go`

Examples of bad decomposition:
- moving turn logic into unrelated feature packages
- making Agent depend on many tiny cross-package wrappers with unclear ownership

---

## Naming Guidance

The current implementation still uses `chat` in many file and type names.

Architecturally, contributors should think about it this way:

- package name today: `internal/chat`
- subsystem meaning: `Agent`

Future renames should be driven by clarity and migration safety, not urgency.

The important thing right now is the architectural boundary, not the package rename.

---

## Practical Decision Test

Before moving code into or out of Agent, ask:

1. Is this part of the lifecycle of a turn?
2. Does this improve or weaken the cohesion of the Agent subsystem?
3. Will this make debugging turn execution easier or harder?
4. Is this orchestration, or is it a capability being orchestrated?

If the answer is “capability being orchestrated,” it probably should stay outside Agent.

---

## Current Recommendation

Treat `internal/chat` as the Atlas Agent subsystem.

Do not aggressively split it apart.

Instead:
- preserve its cohesion
- clarify its boundary
- improve its observability
- rename concepts toward `Agent` over time when it is safe to do so

This document should be used as the reference point for future Agent-related refactors.
