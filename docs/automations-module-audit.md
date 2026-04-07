# Automations Module Audit

Date: 2026-04-07
Scope: `atlas-runtime/internal/modules/automations`, `atlas-runtime/internal/features/automations.go`, automation run storage, comms delivery, web automation API usage.
Goal: identify migration bugs, regressions, security issues, and dashboard-readiness gaps before using automations as the base layer for deferred dashboards.

## Scorecard

| Area | Score | Notes |
| --- | ---: | --- |
| API shape | 7/10 | Clear CRUD/run endpoints, but raw file endpoint and missing summary fields limit dashboard use. |
| Execution reliability | 5/10 | Manual run path works, but no scheduler loop, no retry semantics, no cancellation, and delivery failures do not affect run status. |
| Data model durability | 5/10 | Definitions are markdown-backed while runs are SQLite-backed; useful for migration, weak for dashboards. |
| Security posture | 6/10 | Routes are protected, but raw full-file write and unvalidated destination metadata are high-leverage mutation points. |
| Migration compatibility | 6/10 | Legacy `notify_telegram` is preserved, but duplicate execution paths can regress new destination behavior. |
| Dashboard readiness | 4/10 | No next-run state, no last-run enrichment in list response, no run artifacts, no source/trigger metadata, and no scheduled executor. |
| Test coverage | 6/10 | Current module, feature, comms, and integration tests pass; missing tests for duplicates, raw file damage, delivery failure, workflow-bound automations, and concurrent writes. |

Overall readiness: 5.6/10.
Recommendation: usable as a migration bridge, not yet ready as the base layer for deferred dashboards without hardening the execution and persistence model.
Production readiness plan: `docs/automations-production-readiness-plan.md`.

## Findings

### P1 - `gremlin.run_now` bypasses the automations module

Files:
- `atlas-runtime/internal/skills/gremlin.go:358`
- `atlas-runtime/cmd/atlas-runtime/main.go:235`
- `atlas-runtime/internal/modules/automations/module.go:217`

The tool path for `gremlin.run_now` calls `skillsRegistry.SetRunAutomationFn`, which directly invokes `chatSvc.HandleMessage` with the raw prompt. It does not create a `gremlin_runs` row, does not use `buildAutomationPrompt`, does not deliver to `CommunicationDestination`, does not set `Platform: "automation"`, and does not publish automation bus events.

Impact:
Dashboard data will be incomplete and inconsistent depending on whether a run came from the web route or the skill route. This is also a regression risk for the Telegram/WhatsApp delivery behavior that was just fixed in the HTTP module path.

Recommendation:
Create a module-owned automation service and agent controller. `gremlin.run_now` should become a deprecated compatibility alias; the canonical agent path should be an automations-module action that uses the same runner as `/automations/{id}/run`. The runner should own run row creation, prompt wrapping, delivery, events, and status updates.

### P1 - Workflow-bound automations are configured in the UI but not executed by the module

Files:
- `atlas-web/src/screens/Automations.tsx:150`
- `atlas-web/src/screens/Automations.tsx:211`
- `atlas-runtime/internal/features/automations.go:27`
- `atlas-runtime/internal/modules/automations/module.go:266`

The UI persists `workflowID` and `workflowInputValues`, and the feature model stores them. The automations module ignores both and only sends `buildAutomationPrompt(item)` to the agent.

Impact:
Users can configure an automation that appears bound to a workflow, but the workflow does not actually run. Deferred dashboards will inherit a broken mental model if they rely on automations as scheduled workflow/dashboard jobs.

Recommendation:
Decide whether automations are prompt-only, workflow-only, or hybrid. If hybrid, run workflow definitions through the workflow execution path first, persist `workflow_run_id`, then optionally run the prompt step.

### P1 - No scheduler exists in the migrated automations module

Files:
- `atlas-runtime/internal/modules/automations/module.go:59`
- `atlas-runtime/internal/modules/automations/module.go:217`
- `atlas-runtime/internal/skills/gremlin.go:425`

`Start` and `Stop` are no-ops. The module exposes CRUD and manual run routes, but no due-run scanner, timer, schedule parser, or persisted next-run state. The only schedule handling found is a best-effort `estimateNextRun` for display in the gremlin skill.

Impact:
The module currently cannot serve as a scheduler-backed base layer for dashboards. It can store schedule text and run on demand, but it does not execute automatically.

Recommendation:
Add a scheduler component with parsed schedule records, last/next run state, and a tick loop. Keep schedule parsing deterministic and store the canonical schedule separately from display text.

### P1 - Markdown-backed definitions are unsafe for concurrent mutation and weak for dashboard queries

Files:
- `atlas-runtime/internal/features/automations.go:70`
- `atlas-runtime/internal/features/automations.go:354`
- `atlas-runtime/internal/features/automations.go:380`
- `atlas-runtime/internal/features/automations.go:502`

Automation definitions are rewritten through `GREMLINS.md` without a module-level mutex. `AppendGremlin`, `UpdateGremlin`, `DeleteGremlin`, and `SetAutomationEnabled` all read/parse/write independently. Concurrent UI actions, tool calls, or future scheduler updates can lose writes.

Impact:
Dashboards need reliable, queryable metadata. Markdown definitions make it hard to compute last/next run, status, owners, dependencies, and tags without reparsing the whole file, and concurrent writes can corrupt or drop changes.

Recommendation:
Move canonical automation definitions to SQLite and keep `GREMLINS.md` as an import/export compatibility layer. If that is too large for the next step, add a shared mutex around all GREMLINS mutations immediately.

### P2 - Update semantics can unintentionally clear fields

Files:
- `atlas-runtime/internal/features/automations.go:400`
- `atlas-runtime/internal/features/automations.go:404`
- `atlas-runtime/internal/features/automations.go:405`
- `atlas-runtime/internal/features/automations.go:408`

`UpdateGremlin` treats zero values as real updates for several fields. `Prompt` is always overwritten, `IsEnabled` is always overwritten, and `WorkflowID`, `WorkflowInputValues`, `TelegramChatID`, and `CommunicationDestination` are always replaced. This works for full-object web updates, but it is risky for partial updates from skills or future dashboard API clients.

Impact:
A partial update can erase prompt text, disable an automation, or clear delivery/workflow settings.

Recommendation:
Separate full replacement from patch semantics. Add a `PatchGremlin` type with pointer fields, or require PUT to be full replacement and use PATCH for partial updates.

### P2 - Run status does not reflect delivery failure

Files:
- `atlas-runtime/internal/modules/automations/module.go:289`
- `atlas-runtime/internal/modules/automations/module.go:290`
- `atlas-runtime/internal/modules/automations/module.go:298`

The run is marked completed before delivery. If delivery fails, the module logs an error but leaves the run status as completed and does not populate `error_message`.

Impact:
Dashboards will show a successful automation even when Telegram/WhatsApp/Slack/Discord delivery failed.

Recommendation:
Add delivery status fields, for example `execution_status`, `delivery_status`, `delivered_at`, and `delivery_error`. Do not collapse generation success and delivery success into a single status.

### P2 - `error_message` is never written

Files:
- `atlas-runtime/internal/storage/db.go:189`
- `atlas-runtime/internal/storage/db.go:932`
- `atlas-runtime/internal/modules/automations/module.go:276`

The schema has an `error_message` column and the JSON response includes it, but `UpdateGremlinRun` only updates `finished_at`, `status`, and `output`. Failures are stored in `output`.

Impact:
Run history consumers cannot reliably distinguish user-facing output from failure details. Deferred dashboards need structured failure fields.

Recommendation:
Change `UpdateGremlinRun` to accept output and error message separately, or add a richer `CompleteGremlinRun` method.

### P2 - Raw full-file endpoint is a high-risk mutation surface

Files:
- `atlas-runtime/internal/modules/automations/module.go:100`
- `atlas-runtime/internal/modules/automations/module.go:106`

`GET /automations/file` returns raw `GREMLINS.md` and `PUT /automations/file` overwrites it. These routes are protected, but they bypass schema validation, destination validation, ID uniqueness checks, and schedule validation.

Impact:
A UI bug, compromised remote session, or malformed edit can break all automations. This is risky if automations become the base layer for dashboards.

Recommendation:
Keep this endpoint as an admin/import-export escape hatch only. Gate it behind an explicit advanced/admin route, add schema validation on parsed output, and prefer typed CRUD for normal UI.

### P2 - IDs are slug-derived and not guaranteed unique

Files:
- `atlas-runtime/internal/features/automations.go:198`
- `atlas-runtime/internal/features/automations.go:312`
- `atlas-runtime/internal/features/automations.go:355`
- `atlas-web/src/screens/Automations.tsx:200`

IDs are generated from names. Duplicate names or similar names can collide, and `createAutomation` returns the first parsed item matching the name.

Impact:
Update, delete, enable, and run operations can target the wrong automation. Dashboards need stable IDs that survive renames and deduplication.

Recommendation:
Use generated stable IDs for new automations, preserve slug aliases only for legacy imports, and enforce uniqueness on create/import.

### P2 - Run records lack dashboard-grade metadata

Files:
- `atlas-runtime/internal/storage/db.go:189`
- `atlas-runtime/internal/modules/automations/module.go:243`
- `atlas-runtime/internal/modules/automations/module.go:329`

Run records include IDs, status, output, conversation ID, and optional workflow run ID. They do not include trigger source, schedule version, provider/model, token/cost linkage, selected destination, delivery status, duration, retry count, or structured artifacts.

Impact:
Deferred dashboards will not be able to show useful provenance, operational health, or drill-down state without joining against logs or reparsing output.

Recommendation:
Add structured fields or a run metadata JSON column before dashboards depend on this table.

### P3 - List response does not enrich automations with last/next run state

Files:
- `atlas-runtime/internal/modules/automations/module.go:81`
- `atlas-web/src/api/contracts.ts:311`
- `atlas-web/src/screens/Automations.tsx:477`

The frontend model expects optional `nextRunAt`, `lastRunAt`, and `lastRunStatus`, but `/automations` returns parsed `GREMLINS.md` items without joining run history or schedule estimates.

Impact:
The UI cannot reliably surface automation health or upcoming work. Dashboards will need this as a first-class query.

Recommendation:
Return an `AutomationSummary` DTO from `/automations` with definition plus computed last run, next run, and health.

### P3 - Schedule validation is too permissive and contains a bug

Files:
- `atlas-runtime/internal/skills/gremlin.go:553`
- `atlas-runtime/internal/skills/gremlin.go:557`

`gremlin.validate_schedule` accepts broad strings such as "daily" without canonicalizing them. The weekday check uses `strings.ContainsAny(lower, "monday tuesday ...")`, which checks any character from that string rather than matching weekday names.

Impact:
Bad schedules can be accepted, and future scheduler behavior will be unpredictable.

Recommendation:
Use one canonical schedule parser for API validation, skill validation, display, and scheduler execution. Store the canonical representation.

### P3 - Tests pass, but important cases are missing

Current command:

```sh
go test ./internal/modules/automations ./internal/features ./internal/comms ./internal/integration
```

Result: passed.

Missing coverage:
- duplicate automation IDs
- concurrent GREMLINS writes
- raw file parse failures
- delivery failure status
- workflow-bound automation execution
- `gremlin.run_now` parity with HTTP run route
- disabled automation manual-run policy
- schedule validation canonicalization
- last/next run enrichment

## Security Notes

Routes are mounted through `MountProtected`, and the protected router applies `RequireSession`, remote HTTPS checks, and CSRF for remote state-changing requests. That is a good baseline.

The main security concerns are not unauthenticated access. They are high-impact authenticated mutation paths:
- raw full-file write can mutate all automation definitions at once
- destination metadata is trusted once stored
- prompt text is stored and then executed by the agent with tool access
- `gremlin.run_now` is an execute-class tool but bypasses the newer automation execution guardrails

## Dashboard Base Layer Recommendation

Before deferred dashboards depend on automations, introduce a small internal automation service with this shape:

1. `AutomationDefinitionStore`: SQLite canonical records, legacy GREMLINS import/export.
2. `AutomationRunner`: one execution path for HTTP, module-owned agent actions, scheduler, and future dashboards.
3. `AutomationScheduler`: parsed schedules, next-run state, due-run scanner.
4. `AutomationRunStore`: structured status, output, error, delivery, retry, trigger, and metadata.
5. `AutomationSummaryAPI`: list definitions with last run, next run, health, and delivery state.
6. `AutomationAgentController`: canonical `automation.*` agent actions; `gremlin.*` remains only as a temporary compatibility alias.

Suggested order:

1. Unify execution paths behind `AutomationRunner`.
2. Add structured run completion fields and delivery status.
3. Add a GREMLINS mutation mutex and duplicate ID checks.
4. Add canonical schedule parsing and next-run state.
5. Move definitions to SQLite, keeping GREMLINS as a migration/import-export layer.
6. Move agent control into the automations module and deprecate direct `gremlin.*` implementations.
7. Add dashboard summary DTOs and tests.

## Remediation Progress - 2026-04-07

Closed or substantially reduced:

- `P1 gremlin.run_now bypasses the automations module`: `gremlin.run_now` now routes through the automations module runner and canonical `automation.run` exists.
- `P1 no scheduler exists`: a conservative module scheduler now runs enabled daily, weekly, and hourly automations through the shared runner.
- `P2 run status does not reflect delivery failure`: delivery status and delivery errors are now persisted separately from generation status/output.
- `P2 error_message is never written`: generation failures now persist to `error_message` through `CompleteGremlinRun`.
- `P2 run records lack dashboard-grade metadata`: run records now include trigger source, execution status, delivery status, delivery error, destination JSON, duration, retry count, and artifacts JSON.
- `P3 list response does not enrich automations`: `/automations/summaries` now provides a dashboard-focused summary DTO.
- `P3 tests missing important cases`: added regression tests for duplicate IDs, module-owned agent action execution, delivery failure state, summaries, and scheduler deduplication.

Still open:

- SQLite is not yet canonical for automation definitions; `GREMLINS.md` remains the definition store.
- Workflow-bound automations still need an execution adapter.
- Destination validation against known/authorized communication channels still needs to be enforced before save and delivery.
- Raw full-file import/export still exists and should remain advanced-only until SQLite migration lands.

Updated readiness estimate after this slice: 8.3/10.

## Remediation Progress - Continued 2026-04-07

Additional closed or substantially reduced items:

- `P1 markdown-backed definitions are unsafe for dashboard queries`: the automations module now uses SQLite as its canonical definition store and keeps `GREMLINS.md` as compatibility import/export.
- `P1 workflow-bound automations are configured but not executed`: workflow-bound automations now create linked workflow runs and route the composed workflow prompt through the automation runner.
- `P2 IDs are slug-derived and not guaranteed unique`: SQLite definitions preserve stable IDs across rename and duplicate IDs are rejected on create/import.
- `P2 destination metadata is trusted once stored`: new/updated destinations and delivery attempts now validate against known communication sessions.

Residual risks:

- Schedule next-run state is still derived rather than persisted as durable `next_run_at`.
- Legacy `gremlin.*` write actions still exist in the skills package and should be converted to compatibility wrappers or removed.
- Workflow execution logic is duplicated between workflows and automations and should be extracted into a shared service.

Updated readiness estimate after this slice: 9.1/10.

## Remediation Progress - Final Hardening 2026-04-07

Additional closed items:

- Persisted schedule state: canonical automation definitions now carry `schedule_json` and `next_run_at`, and the scheduler advances `next_run_at` before launching due runs.
- Legacy gremlin control drift: `gremlin.*` actions now delegate to canonical `automation.*` module-owned actions, and the old `SetRunAutomationFn` callback was removed.

Residual risks:

- Workflow execution logic is still duplicated between workflows and automations and should be extracted into a shared service.
- Raw full-file import/export still exists and should remain advanced-only in the UI.

Updated readiness estimate after this slice: 9.4/10.

## Remediation Progress - Final Cleanup 2026-04-07

Additional closed items:

- Workflow execution drift: workflow prompt resolution, input composition, workflow-run creation, and workflow-run completion now live in a shared helper used by both workflows and workflow-bound automations.
- Raw full-file import/export risk: explicit advanced import/export endpoints now exist, the web API client uses those names, and the legacy `/automations/file` endpoints remain only for compatibility.

Residual risks:

- The advanced raw import route is still powerful by design. It should stay out of normal UI flows and remain documented as an import/repair tool only.
- Future dashboard work should consume structured automation definition and summary APIs rather than raw markdown export/import.

Updated readiness estimate after this slice: 9.6/10.

## Remediation Progress - P1/P2 Closure 2026-04-07

Closed the follow-up production-readiness findings from the final audit pass:

- Existing automation table migration now adds `schedule_json` and the other late-added definition columns through idempotent ALTER statements.
- Advanced raw import now validates imported communication destinations before saving rows into the canonical SQLite definitions table.
- HTTP automation updates are presence-aware, so omitted fields no longer disable automations or clear workflow/delivery routing.
- Agent schedule validation now matches scheduler execution support: daily/hourly/weekly schedules are valid, while cron and one-time schedules are reported as unsupported until implemented.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Updated readiness estimate after this slice: 9.5/10.
