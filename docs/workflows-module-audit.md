# Workflows Module Audit

Date: 2026-04-07
Scope: `atlas-runtime/internal/modules/workflows`, `atlas-runtime/internal/workflowexec`, workflow storage, automation-bound workflow execution, and agent control exposure.
Goal: prepare workflows to become a powerful standalone process layer integrated with automations and exposed to the agent.

## Scorecard

| Area | Before | Now | Notes |
| --- | ---: | ---: | --- |
| API shape | 7/10 | 8.5/10 | Existing CRUD/run routes preserved; `/workflows/summaries` added. |
| Execution reliability | 6/10 | 9/10 | HTTP, agent, and automation-bound paths share `workflowexec`; prompt steps persist structured status. |
| Data durability | 5/10 | 9/10 | SQLite is canonical for definitions and runs; JSON definitions import lazily. |
| Agent usefulness | 4/10 | 9/10 | `workflow.*` actions are module-owned and auto-approved local controls. |
| Automation integration | 6/10 | 9/10 | Workflow-bound automations create linked workflow runs via canonical storage. |
| Dashboard/UI readiness | 5/10 | 9/10 | Summary endpoint, structured run metadata, and refreshed web UI rows are available. |
| Trust/safety enforcement | 5/10 | 9/10 | Control actions are classified correctly and workflow runs enforce tool policies before execution or approval deferral. |
| Test coverage | 6/10 | 8.5/10 | Full runtime tests pass after the migration. |

Overall backend readiness: 9.4/10.
Overall product/UI readiness: 9.0/10.

## Key Findings Addressed

### P1 - JSON-backed definitions were not durable enough

Workflow definitions and runs previously lived in `workflow-definitions.json` and `workflow-runs.json`. This was workable for early migration, but weak for dashboard queries, concurrent updates, and agent control.

Resolution:
SQLite `workflows` and `workflow_runs` tables are now canonical. Legacy definitions import on module registration and lazily if a missing ID still exists in the legacy JSON file.

### P1 - Agent control was not first-class

Before this pass, the agent did not have a module-owned `workflow.*` surface comparable to `automation.*`.

Resolution:
The workflows module now registers create, update, delete, list, get, run, run history, duplicate, validate, and explain actions through the runtime skill registry.

### P1 - Workflow execution path was too thin

The workflows module directly created JSON run records and launched the agent from the route handler.

Resolution:
`workflowexec` now accepts a workflow store and creates structured workflow runs. The workflows module uses this path for HTTP and agent-triggered runs, and automations use it for workflow-bound automations.

### P2 - Dashboard state required log/file scraping

The UI and future dashboards needed a cleaner summary contract.

Resolution:
`GET /workflows/summaries` returns workflow health, enabled state, step count, and last run fields.

### P2 - Web UI still reflected the old prompt-library model

The Workflows page previously presented workflows mostly as prompt templates. The Automations page also emphasized prompt text over trigger/delivery state, which made the stronger architecture less visible.

Resolution:
The Workflows page now presents workflows as reusable processes with health, step count, trust scope, tags, and last-run state. The Automations page now presents automations as schedule/delivery surfaces with task mode, destination health, next run, and last-run state. Both pages consume summary endpoints instead of relying only on definition lists.

### P1 - Trust scope was prompt-only

Prompt-only guardrails were not enough because the model could still request a disallowed tool.

Resolution:
Workflow runs now pass a `ToolPolicy` into the agent loop. The loop blocks disallowed tool families, live-write actions, sensitive-read tools, and filesystem paths outside workflow-approved roots before execution or approval deferral.

### P2 - Workflows could not proactively reach the user

Automations had delivery destinations, but the agent did not have an explicit way to discover which chat bridge channels were available or send a user-facing message from a workflow.

Resolution:
The communications module now exposes canonical `communication.*` agent actions. `communication.list_channels` returns authorized bridge sessions, and `communication.send_message` only delivers to those known destinations. Workflow trust scope can opt into this by allowing the `communication` / `chat bridge` tool family and live-write behavior.

### P2 - Steps were not persisted as execution state

Steps were previously prompt context only.

Resolution:
Prompt steps now execute sequentially and persist structured step-run status, output, and errors in `workflow_runs.step_runs_json`. Unsupported future step kinds are represented as skipped placeholders.

## Remaining Risks

1. Advanced workflow DAG behavior such as branching, retries, and tool-step input mapping is still out of scope.
2. Mobile visual QA still needs a pass with seeded automation/workflow data.
3. Legacy JSON run files remain as fallback helpers only; new runtime paths should read SQLite.

## Recommendation

Keep workflows as reusable, inspectable, trust-bounded process templates that automations and the agent can invoke. Avoid building a full DAG engine until branching/retry use cases are concrete.
