# Workflows Production Readiness Plan

Date: 2026-04-07
Target readiness: 9/10 or better
Strategic goal: make workflows a powerful standalone process layer that automations, the agent, HTTP, and future dashboards can invoke through one execution path.

## Readiness Definition

Workflows reach production-ready status when they meet these gates:

| Gate | Required State |
| --- | --- |
| Standalone value | Workflows remain reusable process templates, not folded into automations. |
| Automation integration | Automations can bind to workflows and persist linked workflow run IDs. |
| Agent control | Module-owned `workflow.*` actions expose create, update, delete, list, get, run, run history, duplicate, validate, and explain. |
| Durable definitions | SQLite is canonical for workflow definitions; `workflow-definitions.json` is import compatibility only. |
| Structured runs | Workflow runs persist status, outcome, inputs, summary, errors, conversation ID, trigger source, duration, and step-run placeholders. |
| One runner | HTTP, agent, and automation-bound runs use the same workflow execution helper. |
| Dashboard/UI readiness | `/workflows/summaries` returns health, last run, error, enabled state, and step count; the web UI consumes it. |
| Safety boundaries | Workflow control actions are auto-approved local operations; runtime tools inside workflows still follow action safety and trust scope. |
| Tests | Regression tests cover SQLite-backed run persistence, route shape, automation binding, and full runtime integration. |

## Architecture Target

The intended mental model is:

| Layer | Owns |
| --- | --- |
| Automations | Triggers, schedules, enablement, and delivery. |
| Workflows | Reusable process instructions, inputs, trust scope, runs, and future step execution. |

Automations should call workflows when a scheduled task needs a reusable process. Workflows should remain callable directly by chat/agent, HTTP, and future dashboard buttons.

## Completed Slice - 2026-04-07

1. Added canonical SQLite workflow tables:
   - `workflows`
   - `workflow_runs`
2. Added `platform.WorkflowStore` and SQLite adapters.
3. Refactored the workflows module to use the workflow store instead of direct JSON-file CRUD.
4. Preserved legacy `workflow-definitions.json` import compatibility, including lazy import for missing legacy IDs.
5. Upgraded `workflowexec` into the shared execution helper used by workflows and workflow-bound automations.
6. Added structured workflow run persistence for status, outcome, inputs, summary, error, trigger source, conversation ID, duration, and record JSON.
7. Added module-owned agent actions:
   - `workflow.create`
   - `workflow.update`
   - `workflow.delete`
   - `workflow.list`
   - `workflow.get`
   - `workflow.run`
   - `workflow.run_history`
   - `workflow.duplicate`
   - `workflow.validate`
   - `workflow.explain`
8. Added `/workflows/summaries` for dashboard/UI refresh readiness.
9. Added `workflow` as a selective tool capability group.
10. Wired the runtime to register workflow agent actions.
11. Kept control actions as local-write/read operations so the agent can manage workflows without unnecessary approval friction.
12. Added prompt-time trust-scope guardrails and simple prompt-step composition so stored workflow metadata begins influencing execution.
13. Added agent-loop `ToolPolicy` enforcement for workflow runs, blocking disallowed tools, live writes, sensitive reads, and filesystem paths outside workflow-approved roots before tool execution or approval deferral.
14. Added sequential prompt-step execution with persisted step-run status/output/error metadata.

## Remaining Work

### Phase 1 - Trust Scope Enforcement

Status: implemented for the agent-loop tool boundary.

What now works:

1. Workflow runs pass a `ToolPolicy` into the agent loop.
2. Disallowed tool prefixes are blocked before execution or approval deferral.
3. `allowsLiveWrite=false` blocks local writes, destructive local actions, external side effects, and send/publish/delete actions.
4. `allowsSensitiveRead=false` blocks sensitive read families such as vault, memory, browser, mail, and contacts.
5. Filesystem tool calls are constrained to workflow-approved root paths.
6. Blocked calls return structured tool-denial results to the model and emit tool-failed events.

### Phase 2 - Step Execution Model

Status: first production slice implemented.

What now works:

1. Prompt steps execute sequentially in one workflow run.
2. Step runs persist `pending`, `running`, `completed`, `failed`, or `skipped` status.
3. Step outputs and errors persist in `step_runs_json`.
4. Unsupported step kinds are represented as skipped placeholders and warned by `workflow.validate`.
5. Branching/retry DAG behavior remains intentionally out of scope.

15. Refreshed the Workflows web UI so workflows are presented as reusable processes with health, step count, trust scope, tags, and last-run state.
16. Refreshed the Automations web UI so automations are presented as triggers/delivery surfaces with task mode, destination health, next run, and last-run state.
17. Updated the Skills catalog/UI language so canonical `automation.*` and `workflow.*` controls replace the legacy Gremlin-facing catalog entry.
18. Added the module-owned `communication.*` agent surface so workflows and automations can discover authorized chat bridge channels and send user-facing messages through them.

## UI Contract - 2026-04-07

The web UI now reflects the product boundary:

| Page | Primary job |
| --- | --- |
| Automations | Configure schedule, enabled state, delivery destination, and linked workflow/prompt task. |
| Workflows | Configure reusable process instructions, steps, trust scope, tags, and run history. |
| Skills | Show canonical agent control surfaces: `Automation Control`, `Workflow Control`, and `Communication Bridge`. |

Automations can bind to a workflow, but workflows remain independently runnable by the agent, HTTP, and the web UI.
Workflows that need to reach the user should allow the `communication`/`chat bridge`
tool family in trust scope and allow live-write behavior for that run.

### Phase 3 - UI Refresh

Status: implemented first production slice.

What now works:

1. Workflows page consumes `/workflows/summaries`.
2. Workflows rows surface health, step count, trust scope, approval mode, tags, and last run.
3. Automations page consumes `/automations/summaries`.
4. Automations rows surface task mode, delivery destination/health, next run, last run, and last error.
5. Secondary row actions are grouped behind a compact action menu to keep mobile density under control.

Follow-up:
1. Do a visual mobile pass with seeded automations/workflows data.
2. Consider adding linked-automation context to workflow summaries when dashboards need it.

## Current Readiness Estimate

Current backend readiness: 9.4/10.
Current product/UI readiness: 9.0/10.

The core architecture is now aligned with automations: durable storage, module-owned agent control, structured runs, one execution path, runtime tool-policy enforcement, persisted prompt-step execution, and a refreshed web UI. Remaining gaps are mainly future advanced DAG behavior such as branching/retries and mobile visual QA with real data.
