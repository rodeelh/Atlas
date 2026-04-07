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
| Dashboard readiness | `/workflows/summaries` returns health, last run, error, enabled state, and step count. |
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

### Phase 3 - UI Refresh

Goal: make the Workflows UI reflect the stronger backend contract.

Tasks:

1. Add summary cards using `/workflows/summaries`.
2. Surface last run, health, enabled state, linked automation context, and step count.
3. Improve the run history panel with error/summary/duration.
4. Clarify the relationship between workflows and automations in copy.
5. Keep creation simple: prompt-first, steps/trust scope as advanced sections.

Expected readiness after phase: 9.5/10.

## Current Readiness Estimate

Current backend readiness: 9.4/10.

The core architecture is now aligned with automations: durable storage, module-owned agent control, structured runs, one execution path, runtime tool-policy enforcement, and persisted prompt-step execution. The main remaining gap is the UI refresh plus any future advanced DAG features such as branching and retries.
