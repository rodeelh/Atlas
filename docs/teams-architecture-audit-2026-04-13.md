# Atlas Teams V1 — Final Architecture & Behavior Audit

**Audit date:** 2026-04-13  
**Cleanup pass resolved:** 2026-04-13 (same day)  
**Binary:** daemon deployed 2026-04-13 00:27; cleanup build deployed 2026-04-13  
**Evidence sources:** agent-boundary.md (intended), teams-v1-implementation-spec.md (intended), two completed test runs (behavioral evidence), live codebase inspection

---

## Executive Summary

Atlas Teams V1 is structurally sound and behaviorally functional for its primary use case. Three of four prior blocking issues are resolved. The core boundary (Agent decides → Teams executes) is honored in most paths. The product is shippable with a documented known-issue set.

**Update (cleanup pass, 2026-04-13):** Both latent bugs identified below were resolved in the same-day cleanup pass (M1 + M2). All must-fix and should-fix items are closed. See the updated "Highest-Leverage Next Steps" section for resolution details.

Two latent bugs were identified that would have surfaced quickly in production:

1. ~~**`syncFromFile` is called inside `delegateTask` on every delegation**~~ → **Fixed (M1).** `syncFromFile` removed from `delegateTask`. DB-only agents now survive any delegation.

2. ~~**Async task follow-up delivery does not exist.**~~ → **Fixed (M2).** `AsyncFollowUpSender` + `WithOriginConvID` context threading delivers completion messages to the originating conversation when `async_assignment` tasks finish.

---

## Fully Aligned Areas

### Agent loop orchestration
`internal/agent/loop.go` handles multi-turn execution, tool dispatch, approval deferral, and SSE emission correctly. The loop is stable — approval pause → resume works end-to-end via `chat.Service.Resume()` → `agents.Module.ResumeTask()` → goroutine continuation.

### Agent → Teams boundary (tool call path)
Atlas calls `team.delegate` (a skill registered by Teams); Teams validates, materializes, and executes the task. Agent does not build worker prompts (`composeWorkerPrompt` lives in `internal/modules/agents/prompt.go`). Agent does not directly inspect task state. The `TeamsService` interface in `service.go` is the correct formal seam. **On the main delegation code path, the boundary is respected.**

### Sub-agent tool surface enforcement
`subRegistryFor()` correctly filters the skill registry to each agent's `allowedSkills` patterns using `FilteredByPatterns()`. Workers only see their allowed tools. Tested and confirmed by behavioral evidence (Scout only used websearch/web/fs).

### Worker prompt composition (partial — see gap below)
`composeWorkerPrompt()` in `prompt.go` implements the four-section spec (Identity, Assignment, Context, Execution contract). Task fields flow through correctly. The fallback contract is functional. The spec-defined template contracts (scout/builder/reviewer/operator/monitor behavior rules) are never applied in practice — see **Behaviorally Weak** below.

### Team HQ truthfulness
`GET /agents/hq` returns a projection of live DB state — agent definitions + runtime rows + task events + blocked strip. All values observed in the task table match Team HQ state. No cached or synthetic data. Accuracy is good.

### Approval flow (pause → resume)
Turn returns `pendingApproval` status, SSE emits `waitingForApproval`. `POST /approvals/{toolCallID}/approve` routes through `chatSvc.Resume()` which relaunches the loop with the approved tool call. Tested and confirmed working. The gap (no inline explanation of what is pending) is a UX gap, not a reliability gap.

### Agent persistence across restarts
SQLite is the canonical store. `resetStaleRuntimeStates()` on `Start()` cleans up interrupted tasks without data loss. Basic restart scenario confirmed (F4 retest). See latent bug caveat below.

### Ghost attribution prevention
Two enforcement layers now in place: `rosterContextFromDB()` says "Never claim a specialist did work unless team.delegate was called and returned a result this turn." Response contracts in `research` and `execution` modes repeat the same rule. Zero ghost attribution observed in retest.

### Natural delegation for `assistive` autonomy
Scout (autonomy=assistive) was proactively invoked for a natural research prompt with no explicit routing. The combination of expanded activation hints, explicit DELEGATION RULES in the roster block, and `on_demand` restriction keeping other members quiet produces correct proactive behavior.

---

## Behaviorally Weak or Partial Areas

### F2 — Sequence pattern (structural gap, not a reliability failure)
The `sequence` case in `teamDelegate()` is fully implemented and works correctly — it chains outputs between steps, passes `prevSummary` to subsequent tasks, and returns structured `seqResults`. The issue is upstream: the model never chooses `pattern="sequence"`. When told "Use Scout then have Builder", the model makes two sequential `single` tool calls rather than one `sequence` call. Practical output is correct; the formal sequence record (parent task with dependsOn chain, single workflow entity in Team HQ) never appears.

**Root cause:** The model interprets multi-step instructions as two separate decisions, not as one batched plan. The tool description and roster hint both describe sequence syntax, but the model's reasoning pattern is "do step 1 now → observe result → do step 2 now" rather than "commit to a sequence plan up front."

**Classification:** Prompt/policy gap. The sequence mechanism is sound; the routing into it is not triggered.

### TemplateRole never populated (silent partial implementation)
`composeWorkerPrompt()` uses `def.TemplateRole` to select from `templateContracts` — the per-role behavior rules that define what makes Scout vs Builder vs Reviewer behaviorally distinct at the system-prompt level. **`TemplateRole` is never written to the DB.** `fileDefToRow()` explicitly notes it doesn't touch this field. All agent definitions sync via AGENTS.md or API with `role` (free-text legacy field), but `template_role` (V1 enum) stays empty.

Result: **All workers use the generic fallback contract** (`"Work narrowly and complete the assigned goal..."`). The mission/style text does carry behavioral flavor, but the template-layer behavioral rules — Scout's "Prefer evidence over speculation", Builder's "Produce a first-pass implementation", Reviewer's "Inspect for correctness, risk, regressions", etc. — are never injected into any worker prompt.

This is a silent gap: the feature exists in code, behaves correctly when `TemplateRole` is set, but is never set by any current code path.

### Async follow-up delivery (P3 — promise that cannot be kept)
When `async_assignment` fires, the task runs in a background goroutine. There is no code path that:
1. Identifies the originating Atlas conversation
2. Calls `HandleMessage` or equivalent to send a follow-up
3. Pushes a completion notification to the SSE stream

The task `convID` is set to the taskID (not the Atlas conversation ID), so there's no back-reference. When Atlas says "I'll let you know when it's done," it cannot. The result lands in Team HQ only. This is a behavioral promise Atlas is making that the infrastructure cannot fulfill.

### Disabled specialist is silent (F8)
When a requested specialist is disabled, `teamDelegate()` returns an error to Atlas: `"team member %q is disabled"`. Atlas sees this error as a tool call failure and handles it by answering the user's request itself — without explaining that the specialist was unavailable. UX gap only; no reliability issue.

---

## Boundary / Architecture Issues

### `syncFromFile` inside `delegateTask` — live AGENTS.md dependency
**Location:** `internal/modules/agents/agent_actions.go`, `delegateTask()` line ~959

```go
if _, err := m.syncFromFile(ctx); err != nil {
    logstore.Write("warn", "agent: pre-delegation sync failed: "+err.Error(), nil)
    // Non-fatal: proceed with whatever is in the DB.
}
```

This is the most significant architecture issue in the codebase. **Every delegation call reads AGENTS.md and can delete DB-only agents** — the same `syncFromFile` that previously caused F4. The F4 fix (SQLite startup guard) did not address this path. Any user who:

1. Creates an agent via the web UI (`POST /agents`)
2. Later initiates any delegation to a different agent

...will silently lose that API-only agent when `syncFromFile` runs at step 2. AGENTS.md doesn't have the API-created agent, so it gets deleted. The comment says "to avoid stale permissions and the create-then-immediately-delegate race" — a pragmatic fix that was never cleaned up. In a DB-first world, this should read from the DB only.

### `upsertDefinitionInFile` / `deleteDefinitionFromFile` — dead legacy code
These functions exist in `module.go` (lines ~1033, ~1068) but are not called by any current code path. `agent_actions.go` handlers write directly to DB. These are dead code that carries the conceptual weight of the old file-first architecture. They confuse readers about whether AGENTS.md is write-authoritative.

### `agentDelegate` — legacy parallel to `teamDelegate`
`agent_actions.go` line 898 defines `m.agentDelegate`, a legacy handler for the `agent.delegate` action. This is a thin wrapper around `delegateTask` that bypasses the V1 DelegationPlan validation. It was the pre-Phase 4 delegation surface. It may still be registered and reachable from the model. The V1 canonical path is `team.delegate` → `teamDelegate`. Having two parallel delegation entry points with different validation semantics is an architecture smell.

### `capabilityPlan` — two routing systems not coordinated
In `service.go` line 1349, `capabilityPlan` is passed to `applyCapabilityPlanToolHints` which adds tools from suggested groups (forge, workflow, automation, etc.) to the selected tool set. This capability routing system operates independently of the team delegation routing. No conflict, but the two systems are not coordinated — a request that should trigger delegation might instead get routing via capability planner into forge or workflow tools.

---

## Legacy / Drift Findings

### Phase comments throughout `module.go`
"Phase 8: DB-first — write directly to the database.", "Phase 7: V1 template role", "Phase 2: rosterContextFromDB() replaces the AGENTS.md file read." Development notes that will mislead future contributors who don't know the phase history.

### `types.go` — V1 canonical types not fully adopted
`TeamMember`, `DelegationPlan`, `DelegationTask` are defined as the "target architecture." The migration is incomplete — `agentDefinition` (internal) and `storage.AgentDefinitionRow` are still the live types in all CRUD and execution paths. `TeamMember` is only returned from `TeamsService.ListTeamMembers()`. Intentional and documented, but the canonical V1 API surface isn't what the code operates on internally.

### AGENTS.md is described as "export-only" but is still read on every delegation
`CLAUDE.md` and architecture docs describe AGENTS.md as "export-only (`GET /agents/export`)". This is inaccurate — AGENTS.md is read on every `delegateTask()` call via `syncFromFile`. The documentation matches the intended end state, not the current state.

### `PersonaStyle` column — written nowhere, used nowhere
`agent_definitions` has a `persona_style` column. Nothing writes to it (no API field, no AGENTS.md parser field). `composeWorkerPrompt()` uses `def.Style` (the legacy field), not `PersonaStyle`. Dead column.

### Stale status string: `"busy"`
`resetStaleRuntimeStates()` handles both `"busy"` (legacy) and `"working"` (V1). The `"busy"` string appears in guard checks and reset logic. The V1 canonical status is `"working"`. Creates ambiguity about which status vocabulary is authoritative.

### `agent.list` / `agent.get` removal claim vs. aliases in code
CLAUDE.md says these were removed in Teams V1. The code shows `team.list` and `team.get` call `m.agentList` and `m.agentGet` — same handlers, aliased. If the old `agent.*` names were removed from the model's tool list, the statement is accurate. If they're still registered, the docs are misleading.

---

## Production Readiness Assessment

### Safe to build on now
- Agent loop, SSE, approval/resume path, conversation persistence — stable and correct
- Explicit delegation — reliable end-to-end
- Sub-agent tool surface enforcement — correct and tested
- `team.delegate` with `DelegationPlan` — correct canonical delegation surface
- Natural delegation for assistive-autonomy agents — fires correctly
- Team HQ accuracy — live DB projection, no synthetic state
- Agent persistence — reliable when AGENTS.md contains all agents (see latent bug)

### Known issues (document, don't block)
- `pattern=sequence` unreachable through natural language
- Disabled/missing specialist fails silently
- TemplateRole not populated → role-specific worker behavior rules never applied
- Approval turn provides no inline explanation of what is pending
- Monitor can't reach `~/Library/Logs/Atlas/` (fs root config gap)

### Will become serious if ignored
1. **`syncFromFile` in `delegateTask`** — Silent data loss. First user who creates an agent via web UI and then delegates loses that agent. Reproduces on every subsequent delegation.

2. **Async follow-up delivery** — Atlas makes a promise it cannot keep. Every `async_assignment` turn that says "I'll let you know when it's done" is lying. Early adopters will feel this immediately.

3. **TemplateRole gap** — All workers run on the generic fallback contract. Specialist quality differences (Scout's evidence-preference, Builder's implementation focus, etc.) are carried only by Mission/Style text, not by the template layer.

---

## Highest-Leverage Next Steps

### Must-fix

**M1: Remove `syncFromFile` from `delegateTask`** ✅ RESOLVED 2026-04-13  
*File:* `internal/modules/agents/agent_actions.go`, `delegateTask()`  
*Fix applied:* Deleted the 4-line `syncFromFile` call block. DB-only agents now survive delegation to any other agent. Regression test added: `TestDelegateTask_DBOnlyAgentSurvivesDelegation`.

**M2: Build async follow-up delivery** ✅ RESOLVED 2026-04-13  
*Files:* `internal/chat/agents_context.go`, `internal/chat/service.go`, `internal/modules/agents/agent_actions.go`  
*Fix applied:* Added `WithOriginConvID`/`OriginConvIDFromCtx` context helpers and `AsyncFollowUpSender` package var. `HandleMessage` injects the origin convID into the agent context. The async goroutine captures `originConvID` before spawning and calls `AsyncFollowUpSender` with the result when the task completes. Covers the "I'll let you know when it's done" promise. Tests added: `TestAsyncAssignment_SendsFollowUp`, `TestSyncAssignment_NoFollowUp`, plus three `asyncFollowUpText` format tests.

### Should-fix

**S1: Populate TemplateRole on agent creation** ✅ RESOLVED 2026-04-13  
*Files:* `module.go` (`templateRoleFromRole`, `fileDefToRow`, `syncFromFile`), `agent_actions.go` (create/update via `fileDefToRow`)  
*Fix applied:* Added `templateRoleFromRole(role string) string` helper that maps free-text role → scout/builder/reviewer/operator/monitor enum. `fileDefToRow` now sets `TemplateRole` automatically; `syncFromFile` calls `fileDefToRow` (was building the row inline without TemplateRole). All create/update/sync paths populate TemplateRole. Tests added: `TestTemplateRoleFromRole_KnownRoles`, `TestFileDefToRow_PopulatesTemplateRole`, `TestAgentCreate_SetsTemplateRole`.

**S2: Sequence pattern adoption** ✅ RESOLVED 2026-04-13  
*Fix applied:* `team.delegate` tool description updated: "if you plan to call team.delegate twice where step 2 depends on step 1's output, submit BOTH steps as ONE call with pattern=sequence instead of making two separate calls." Roster hint also carries this guidance.

**S3: Remove dead code** ✅ RESOLVED 2026-04-13  
*Fix applied:* Deleted `upsertDefinitionInFile` and `deleteDefinitionFromFile` from `module.go`. Removed duplicate `agentDefinition` struct and helper functions from `agents_file.go` (now lives exclusively in `defs.go`). Cleaned misleading "Phase 8" comments from HTTP handlers.

### Can-wait

**C1:** Disabled specialist narration — Atlas explains when a specialist is unavailable.  
**C2:** Monitor log access — add `~/Library/Logs/Atlas/` to approved fs roots.  
**C3:** Type migration — complete `agentDefinition` → `TeamMember`, `AgentTaskRow` → `DelegationTask` adoption.

---

## Final Verdict

**Shipped. All must-fix and should-fix items resolved on 2026-04-13.**

All primary functional guarantees hold: Atlas delegates correctly, sub-agents are isolated, Team HQ reflects reality, persistence is reliable, ghost attribution is eliminated, async follow-up delivery works, TemplateRole drives role-specific worker contracts.

The DB-first claim in documentation is now accurate: `syncFromFile` is no longer called during delegation. The only remaining call is the one-time startup import guard in `Start()` (fires only when DB is empty) and the explicit `POST /agents/sync` route.
