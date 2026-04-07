# Automations Production Readiness Plan

Date: 2026-04-07
Target readiness: 9/10 or better
Audit baseline: 5.6/10 in `docs/automations-module-audit.md`
Strategic goal: make automations safe and durable enough to become the base execution layer for the deferred dashboards feature.

## Readiness Definition

Automations reach production-ready status when they meet all of these gates:

| Gate | Required State |
| --- | --- |
| One execution path | HTTP run, scheduled run, and agent-requested run all use the automations module runner. |
| Module-owned agent control | Agent-facing automation tools are registered/implemented by the automations module, not by standalone GREMLINS skill helpers. |
| Durable definitions | SQLite is canonical for automation definitions; `GREMLINS.md` is import/export compatibility only. |
| Structured runs | Run records separate execution, delivery, errors, trigger source, retry metadata, and artifacts. |
| Scheduler | Enabled automations execute from parsed schedules with persisted next-run state. |
| Dashboard summaries | `/automations` returns last run, next run, health, and delivery summary without log scraping. |
| Safety | Raw file mutation is removed from normal UI flow or gated as an advanced import/export endpoint. |
| Migration | Legacy `GREMLINS.md` automations import once and preserve IDs/destinations. |
| Tests | Regression tests cover route parity, scheduler, delivery failure, migration, concurrency, and summaries. |

## Target Scorecard

| Area | Current | Target | Completion Criteria |
| --- | ---: | ---: | --- |
| API shape | 7/10 | 9/10 | Typed DTOs, summary endpoint, raw file endpoint gated. |
| Execution reliability | 5/10 | 9/10 | Shared runner, retries, cancellation, scheduler, delivery status. |
| Data model durability | 5/10 | 9/10 | SQLite canonical definitions and structured run records. |
| Security posture | 6/10 | 9/10 | Validated destinations, constrained raw import/export, and runtime action-safety boundaries. |
| Migration compatibility | 6/10 | 9/10 | One-time import with tests for legacy `notify_telegram` and `notify_destination`. |
| Dashboard readiness | 4/10 | 9/10 | Summary DTOs, next-run state, run artifacts, health state. |
| Test coverage | 6/10 | 9/10 | Focused unit, integration, and scheduler tests for all critical paths. |

## Architecture Target

Introduce an internal automation service split into six pieces:

1. `AutomationDefinitionStore`
2. `AutomationRunStore`
3. `AutomationRunner`
4. `AutomationScheduler`
5. `AutomationSummaryService`
6. `AutomationAgentController`

The HTTP module, scheduler, future dashboard surfaces, and agent tools should all call this module-owned service. The current `gremlin.*` skill package should become a temporary compatibility adapter and then be deleted once the module registers the canonical agent-facing tools.

## Phase 0 - Stabilize The Current Bridge

Goal: prevent additional regressions before deeper migration.

Tasks:

1. Add a shared GREMLINS mutex around `AppendGremlin`, `UpdateGremlin`, `DeleteGremlin`, `SetAutomationEnabled`, and raw writes.
2. Enforce duplicate ID checks on create/import.
3. Add typed validation for `CommunicationDestination`.
4. Mark raw `PUT /automations/file` as advanced/import-only in code comments and tests.
5. Add tests for duplicate IDs, concurrent writes, and malformed raw file input.

Acceptance:

1. Existing web automation CRUD still works.
2. Telegram and WhatsApp delivery still work.
3. `go test ./internal/features ./internal/modules/automations` passes.

Expected score after phase: 6.4/10.

## Phase 1 - Create The Shared Runner

Goal: eliminate execution-path drift.

Tasks:

1. Add `internal/automations` or `internal/modules/automations/service.go` with `Runner`.
2. Move run creation, prompt wrapping, agent invocation, delivery, status updates, and bus events into the runner.
3. Route `POST /automations/{id}/run` through the runner.
4. Route agent-requested runs through the automations module runner.
5. Persist trigger source: `manual`, `agent`, `schedule`, `dashboard`.
6. Preserve delivery guardrails and briefing template behavior in one place.

Acceptance:

1. HTTP run and agent-requested run create equivalent run records.
2. Both paths deliver to configured destinations.
3. Both paths publish `automation.triggered.v1` and `automation.completed.v1`.
4. Tests assert parity between HTTP and agent execution.

Expected score after phase: 7.2/10.

## Phase 2 - Structure Run State

Goal: make run history trustworthy for users and dashboards.

Tasks:

1. Add migration columns or metadata JSON for:
   - `trigger_source`
   - `execution_status`
   - `delivery_status`
   - `delivery_error`
   - `destination_json`
   - `started_at`
   - `finished_at`
   - `duration_ms`
   - `retry_count`
   - `artifacts_json`
2. Replace `UpdateGremlinRun` with `CompleteGremlinRun` or `UpdateGremlinRunFields`.
3. Store failures in `error_message`, not `output`.
4. Store delivery failures separately from generation failures.
5. Return the enriched shape from `/automations/{id}/runs`.

Acceptance:

1. Failed agent runs show `status=failed` and `errorMessage`.
2. Successful generation with failed delivery shows delivery failure without hiding generated output.
3. Run history UI can display meaningful status without parsing strings.

Expected score after phase: 7.9/10.

## Phase 3 - Canonical Schedule Parser And Scheduler

Goal: make automations actually run on schedule.

Tasks:

1. Add canonical schedule parser for supported schedules:
   - hourly interval
   - weekly day/time
   - daily time as a weekly shorthand
2. Store parsed schedule JSON and `next_run_at`.
3. Add `AutomationScheduler` in module `Start` and stop it cleanly in `Stop`.
4. Persist last scheduler tick or use DB locking to avoid double runs on restart.
5. Add manual-run policy for disabled automations: either allow with explicit manual trigger or reject consistently.
6. Add scheduler tests with fake clock.

Acceptance:

1. Enabled automations run when due.
2. Disabled automations do not run from the scheduler.
3. Next run moves forward after successful or failed run according to policy.
4. Scheduler does not double-run on rapid restart.

Expected score after phase: 8.5/10.

## Phase 4 - Move Definitions To SQLite

Goal: make automations queryable and safe as a dashboard foundation.

Tasks:

1. Add `automations` table:
   - `id`
   - `name`
   - `emoji`
   - `prompt`
   - `schedule_raw`
   - `schedule_json`
   - `is_enabled`
   - `source_type`
   - `created_at`
   - `updated_at`
   - `workflow_id`
   - `workflow_inputs_json`
   - `communication_destination_json`
   - `tags_json`
2. Import existing `GREMLINS.md` records once on startup.
3. Preserve legacy IDs where safe; generate stable IDs for collisions.
4. Keep `GREMLINS.md` export/import behind explicit advanced routes.
5. Update HTTP CRUD and module-owned agent tools to use the SQLite store.
6. Keep `gremlin.*` as deprecated wrappers only until the new module-owned agent tools are fully wired.

Acceptance:

1. Existing user automations migrate without loss.
2. Duplicate legacy slugs are resolved deterministically.
3. Rename does not change automation ID.
4. CRUD no longer reparses markdown on every request.

Expected score after phase: 9.0/10.

## Phase 4.5 - Move Agent Control Into The Automations Module

Goal: make the automations module the single owner of user-facing and agent-facing control.

Tasks:

1. Add `AutomationAgentController` under the automations module.
2. Register canonical agent actions from the module:
   - `automation.create`
   - `automation.update`
   - `automation.delete`
   - `automation.list`
   - `automation.get`
   - `automation.enable`
   - `automation.disable`
   - `automation.run`
   - `automation.run_history`
   - `automation.next_run`
   - `automation.duplicate`
   - `automation.validate_schedule`
3. Make the new actions call the same `AutomationDefinitionStore`, `AutomationRunner`, and `AutomationSummaryService` as the web API.
4. Add safe name resolution for agent requests:
   - exact ID match
   - exact name match
   - single fuzzy match only for read actions
   - clarification required for destructive or ambiguous matches
5. Mark `automation.*` control actions as auto-approved agent operations. The agent should not need an approval just to create, update, enable, disable, delete, duplicate, inspect, or run an automation.
6. Deprecate `gremlin.*` actions as compatibility aliases that delegate to `automation.*`.
7. Remove direct `features.AppendGremlin`, `features.UpdateGremlin`, `features.DeleteGremlin`, and `SetRunAutomationFn` usage from the skill package.

Acceptance:

1. The agent can manage automations through `automation.*` actions without touching `GREMLINS.md` helpers.
2. `gremlin.*` aliases produce the same run records and summaries while compatibility remains enabled.
3. `automation.*` control actions do not create approval friction for the agent.
4. Ambiguous agent requests do not guess the automation target.
5. Tools invoked inside an automation run still use the normal runtime action-safety policy.

Expected score after phase: 9.2/10.

## Phase 5 - Dashboard Summary API

Goal: provide the base layer deferred dashboards need.

Tasks:

1. Add `AutomationSummary` DTO:
   - definition fields
   - `lastRunAt`
   - `lastRunStatus`
   - `lastRunError`
   - `nextRunAt`
   - `health`
   - `deliveryHealth`
   - `destinationLabel`
2. Make `/automations` return summaries or add `/automations/summaries`.
3. Add `/automations/{id}/runs` pagination.
4. Add dashboard-focused filters:
   - enabled/disabled
   - unhealthy
   - due soon
   - destination platform
5. Update web contracts.

Acceptance:

1. Automations UI no longer needs to infer last-run state.
2. Deferred dashboards can consume summaries without reading logs or raw markdown.
3. Large run histories do not require unbounded reads.

Expected score after phase: 9.3/10.

## Phase 6 - Workflow-Bound Automation Execution

Goal: make saved workflow fields real rather than decorative.

Tasks:

1. Decide official behavior:
   - prompt-only automation
   - workflow-only automation
   - workflow plus prompt automation
2. Add a workflow execution adapter to the shared runner.
3. Persist `workflow_run_id` into automation run records.
4. Pass `workflowInputValues` into workflow execution.
5. Show workflow state in summaries.

Acceptance:

1. Workflow-bound automations execute the selected workflow.
2. Workflow failures are visible in automation run history.
3. The UI cannot configure impossible combinations.

Expected score after phase: 9.5/10.

## Phase 7 - Security And Safety Hardening

Goal: make automation execution safe enough for higher-value dashboards.

Tasks:

1. Validate destinations against discovered/authorized comms channels before saving and before delivery.
2. Add runtime action-safety policy metadata to automation runs.
3. Ensure high-risk tools invoked inside automations still follow the normal runtime safety policy, without adding approvals around the automation control command itself.
4. Limit output size stored in run records and delivery payloads.
5. Add structured logs for run lifecycle and delivery.
6. Add audit trail for definition changes.

Acceptance:

1. Automations cannot deliver to arbitrary destination IDs that are not configured.
2. Automation control remains low-friction for the agent.
3. High-risk runtime tools invoked by an automation still follow the configured safety policy.
4. Large outputs cannot bloat SQLite or crash the UI.
5. Definition changes are attributable and reviewable.

Expected score after phase: 9.7/10.

## Test Matrix

Required before declaring 9/10:

1. Unit tests for schedule parser.
2. Unit tests for definition store migration from `GREMLINS.md`.
3. Unit tests for destination validation.
4. Unit tests for run completion and delivery status.
5. Integration tests for HTTP run route.
6. Integration tests for `automation.run` parity with HTTP run route.
7. Compatibility tests for deprecated `gremlin.run_now` alias while it exists.
8. Integration tests for scheduled execution with fake clock.
9. Integration tests for workflow-bound automations.
10. UI contract tests or TypeScript build for summary DTOs.
11. Manual smoke: create, edit, run, schedule, deliver to Telegram, deliver to WhatsApp, inspect runs.

## Implementation Order

Recommended order for code work:

1. Phase 0: low-risk bridge hardening.
2. Phase 1: shared runner.
3. Phase 2: structured run state.
4. Phase 3: scheduler.
5. Phase 4: SQLite definitions.
6. Phase 4.5: module-owned agent control.
7. Phase 5: dashboard summaries.
8. Phase 6: workflow-bound automations.
9. Phase 7: security and safety hardening.

## Stop Criteria

Pause before merging if any of these happen:

1. Existing Telegram or WhatsApp delivery regresses.
2. Existing `GREMLINS.md` records cannot be imported losslessly.
3. Scheduler can double-run the same automation.
4. High-risk runtime tools invoked by automations bypass the configured safety policy.
5. Run history cannot distinguish generation success from delivery failure.

## Final Readiness Gate

Declare automations production-ready when:

1. The scorecard is recalculated at 9/10 or higher.
2. All P1 and P2 audit findings are closed.
3. The test matrix passes.
4. Manual smoke passes on web UI, Telegram, and WhatsApp.
5. Deferred dashboards can consume automation summaries without direct access to `GREMLINS.md`, logs, or raw run output parsing.

## Implementation Progress - 2026-04-07

Implemented in the current production-readiness slice:

1. Phase 0 bridge hardening:
   - Added a shared GREMLINS mutex around raw writes and typed mutations.
   - Added duplicate automation ID rejection for append/import paths.
   - Added duplicate-ID regression tests.

2. Phase 1 shared runner:
   - HTTP manual runs, scheduled runs, and agent-requested runs now use the automations module execution path.
   - `gremlin.run_now` compatibility now routes through `RunNowForAgent` instead of calling chat directly.
   - Run records now persist trigger source for manual, agent, and schedule paths.

3. Phase 2 structured run state:
   - Added run columns for trigger source, execution status, delivery status, delivery error, destination JSON, duration, retry count, and artifacts JSON.
   - Added `CompleteGremlinRun` so generation failures go to `error_message` and delivery failures stay separate.
   - Delivery failure no longer hides generated output.

4. Phase 3 scheduler foundation:
   - Added module lifecycle scheduler with daily, weekly, and hourly schedule support.
   - Scheduler skips disabled automations and deduplicates runs per scheduled slot in-process.
   - Scheduled runs use the same shared runner and persist `trigger_source=schedule`.

5. Phase 4.5 module-owned agent control:
   - Added canonical `automation.*` actions registered by the automations module.
   - `automation.*` control actions are auto-approved local-write/read operations.
   - Added exact ID/name resolution and conservative fuzzy matching for read-only lookups.

6. Phase 5 dashboard summary foundation:
   - Added `/automations/summaries` with last-run, next-run estimate, health, delivery health, and destination label.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Current readiness estimate: 8.3/10.

Remaining before declaring 9/10+:

1. Finish Phase 4 by making SQLite canonical for automation definitions and keeping `GREMLINS.md` as import/export compatibility.
2. Persist canonical parsed schedule/next-run state instead of deriving next run from schedule text.
3. Finish gremlin compatibility cleanup so all legacy `gremlin.*` write actions delegate to the module controller or are removed.
4. Implement Phase 6 workflow-bound automation execution.
5. Add destination validation against authorized communication channels before save and before delivery.

## Implementation Progress - Continued 2026-04-07

Additional production-readiness work completed:

1. Phase 4 SQLite definitions:
   - Added canonical `automations` table with stable IDs, prompt, schedule, workflow linkage, destination JSON, description, tags, and timestamps.
   - Automations module routes, summaries, scheduler, runner lookup, and `automation.*` actions now read/write through SQLite.
   - Existing `GREMLINS.md` definitions import on module registration.
   - Legacy markdown compatibility remains: module writes mirror definitions back to `GREMLINS.md`, and lookup/list fallback imports late legacy markdown writes.
   - Rename keeps the stable automation ID.

2. Phase 6 workflow-bound execution:
   - Workflow-bound automations now create a linked workflow run, compose the workflow prompt with automation instructions and workflow input values, and persist `workflow_run_id` to the automation run.
   - Workflow run status is updated to completed/failed with the automation execution result.

3. Phase 7 destination safety:
   - New/updated automation destinations must reference an authorized communication session.
   - Delivery also validates the destination before sending.
   - Agent-created or agent-updated automations can use `destinationID` from `communication.list_channels` to attach Telegram/WhatsApp/Slack/Discord delivery without asking for raw bot tokens or chat IDs.
   - Added a regression test for rejecting unauthorized destinations.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Current readiness estimate: 9.1/10.

Remaining hardening before calling this fully complete:

1. Persist parsed schedule JSON and durable `next_run_at` rather than deriving next-run estimates at read time.
2. Convert all legacy `gremlin.*` write actions into thin wrappers over module-owned `automation.*` actions, then remove direct GREMLINS helper usage from the skills package.
3. Add a shared workflow execution service so the workflows module and automations module do not duplicate prompt-running logic.
4. Treat raw `PUT /automations/file` as an explicitly advanced import route in the web UI, not normal editing flow.

## Implementation Progress - Final Hardening 2026-04-07

Additional hardening completed:

1. Durable schedule state:
   - Added `schedule_json` and `next_run_at` to the canonical automation definition flow.
   - Runtime `GremlinItem` now exposes `scheduleJSON` and `nextRunAt` for API/dashboard consumers.
   - Scheduler uses persisted `nextRunAt` when present and advances it before launching a due run to avoid same-slot double fires after restart.
   - Scheduler tests now assert persisted schedule metadata and `nextRunAt` advancement.

2. Legacy `gremlin.*` compatibility cleanup:
   - Removed the old `SetRunAutomationFn` callback path from the skills registry.
   - `gremlin.*` actions now delegate to canonical module-owned `automation.*` actions instead of directly mutating `GREMLINS.md` helpers.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Current readiness estimate: 9.4/10.

Remaining cleanup:

1. Extract a shared workflow execution service so the workflows module and automations module do not duplicate prompt-running logic.
2. Treat raw `PUT /automations/file` as an explicitly advanced import route in the web UI.

## Implementation Progress - Final Cleanup 2026-04-07

Completed the remaining cleanup items:

1. Shared workflow execution helper:
   - Added a lower-level workflow execution helper for workflow prompt resolution, workflow input composition, workflow run creation, and workflow run completion.
   - The workflows module and automations module now share this helper instead of duplicating prompt and workflow-run status logic.
   - Workflow-bound automations still use the automation runner for delivery, run metadata, and automation events.

2. Advanced raw import/export route:
   - Added explicit advanced routes for legacy markdown import/export: `GET /automations/advanced/file` and `PUT /automations/advanced/import`.
   - Kept the old `/automations/file` route as a backward-compatible alias only.
   - Updated the web API client to prefer the advanced route names, making raw full-file import/export an advanced escape hatch rather than a normal editing flow.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Current readiness estimate: 9.6/10.

## Implementation Progress - P1/P2 Closure 2026-04-07

Closed the final production-readiness findings from the last audit pass:

1. Migration durability:
   - Added idempotent migration coverage for `schedule_json` and the other late-added automation definition columns.

2. Advanced import safety:
   - Advanced markdown import now validates communication destinations before persisting imported definitions.

3. Patch safety:
   - HTTP automation updates now preserve omitted fields instead of treating zero-values as explicit clears.

4. Scheduler-aligned validation:
   - `automation.validate_schedule` now reports cron and one-time schedules as unsupported instead of valid, matching the scheduler's current executable schedule set.

Validation:

```sh
go test ./...
```

Result: passed in `atlas-runtime`.

Current readiness estimate: 9.5/10.
