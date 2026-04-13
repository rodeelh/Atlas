# Atlas Teams V1 Implementation Spec

**Status:** Implemented  
**Date:** 2026-04-12  
**Completed:** 2026-04-12  
**Supersedes:** implicit / partial behavior in former `modules/agents` AGENTS.md-first implementation  
**Related:** [`PLAN.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/PLAN.md), [`docs/agent-boundary.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/docs/agent-boundary.md), [`docs/architecture.md`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/docs/architecture.md)

## Implementation Status

All core V1 goals are implemented and deployed. Key deviations from the original spec are noted inline.

| Spec Area | Status | Notes |
|---|---|---|
| Agent/Teams boundary | ✅ | Agent owns delegation decisions via `team.delegate`. Teams owns execution. |
| DelegationPlan / DelegationTaskSpec | ✅ | Structured input with backward-compat flat-arg shim. |
| ExecutionMode: sync_assist / async_assignment | ✅ | Async pre-generates task ID before goroutine spawn. |
| Sequence delegation | ✅ | Full V1 metadata preserved per step — not flattened. |
| Parallel delegation | ⏳ | Defined in types, rejected at validation with clear error. Not yet implemented. |
| Three-layer worker prompt | ✅ | Identity → Assignment → Context → Execution contract. Five template roles. |
| Team HQ projection | ✅ | DB-only reads. Status normalized at read time. BlockingKind/Detail surfaced. |
| DB-first CRUD | ✅ | All create/update/delete/enable/disable write to SQLite only. |
| AGENTS.md deprecation | ✅ | One-time import on first startup if DB empty. File is now export-only. |
| Roster from DB | ✅ | `chat.RosterReader` reads `ListEnabledAgentDefinitions()` each turn. |
| Bounded autonomy triggers | ✅ | `triggerCoordinator` + cooldowns (shipped as M6 in this migration). |

---

## 1. Purpose

This document defines the V1 implementation target for Atlas Teams.

It converts the intended product behavior into an implementation-ready architecture with:

- a clear Agent/Teams boundary
- canonical data contracts
- a hybrid sync/async delegation model
- a single Teams execution engine
- a canonical persistence model
- a Team HQ projection model

This spec is intentionally focused on architecture and runtime behavior. It does not prescribe exact package names or migration PR boundaries.

## 2. Core Architectural Rule

Atlas Teams must follow this rule:

**Agent owns delegation decisions. Teams owns delegated execution.**

That means:

- Agent decides whether to delegate
- Agent decides sync vs async
- Agent decides `single` vs `sequence` vs `parallel`
- Agent chooses target team members
- Agent defines the delegated task shape
- Teams validates feasibility
- Teams persists delegated tasks
- Teams prepares worker context
- Teams executes workers
- Teams tracks runtime and lifecycle state
- Teams stores results and step logs
- Teams returns structured results to Agent

The rule must not be inverted.

## 3. Canonical Product Behavior

### 3.1 Atlas remains primary

Atlas is always:

- the primary user-facing intelligence
- the orchestrator
- the memory owner
- the final presenter of results
- the fallback worker when no specialist is appropriate

Subagents extend Atlas. They do not replace it.

### 3.2 Delegation modes

V1 supports:

- `solo`
- `specialist_assist`
- `team_lead`

These are Atlas-facing operating modes, not task engine states.

### 3.3 Execution modes

V1 supports two delegation execution modes:

- `sync_assist`
- `async_assignment`

#### `sync_assist`

Use when the delegated result is needed to complete the current Atlas turn.

Properties:

- Teams persists and executes the task
- Agent waits for the result
- Atlas reintegrates the result into the same turn

#### `async_assignment`

Use when the delegated work has its own lifecycle and does not need to finish inside the current turn.

Properties:

- Teams persists and executes the task
- Agent does not wait for completion
- Team HQ becomes the primary surface for the task lifecycle

### 3.4 Delegation patterns

V1 supports:

- `single`
- `sequence`
- `parallel`

#### `single`

Default path. Use when one specialist can complete one bounded subtask.

#### `sequence`

Use when downstream work depends on prior delegated output.

#### `parallel`

Use only when delegated subtasks are independent and Atlas can reintegrate them after completion.

V1 should prefer:

1. `single`
2. `sequence`
3. `parallel`

## 4. Storage Decision

`AGENTS.md` is **not** canonical in V1.

### 4.1 Canonical source of truth

Canonical team-member and task state must live in Teams-owned persistence.

### 4.2 `AGENTS.md`

V1 removes `AGENTS.md` from the runtime architecture as a primary source of truth.

It may be reintroduced later only as:

- export
- import bootstrap
- human-readable snapshot

But Agent must not read team-member definitions directly from markdown.

## 5. Internal API Boundary

Agent should interact with Teams through a small internal API.

### 5.1 Required Agent -> Teams operations

- `ListTeamMembers()`
- `Delegate(plan DelegationPlan) -> DelegationExecutionResult`
- `GetTask(taskID)`
- `ResumeTask(taskID, resolution)`

### 5.2 Boundary rules

Agent must not:

- parse team-member files directly
- build worker prompts directly
- manage worker runtime state directly
- inspect Teams storage internals directly

Teams must not:

- choose whether delegation should happen
- choose a different orchestration pattern
- substitute a different team member unless explicitly directed by Agent in a later revision
- redesign the plan during validation

Teams may only:

- validate feasibility
- reject invalid or impossible plans
- execute accepted plans

## 6. Canonical Types

### 6.1 TeamMember

```text
TeamMember
- id
- name
- template_role
- mission
- persona_style
- allowed_skills
- allowed_tool_classes
- autonomy_mode
- activation_rules
- provider_override
- model_override
- is_enabled
- created_at
- updated_at
```

### 6.2 DelegationPlan

```text
DelegationPlan
- mode
- pattern
- tasks[]
```

### 6.3 DelegationTaskSpec

```text
DelegationTaskSpec
- agent_id
- title
- objective
- scope
- success_criteria
- input_context
- expected_output
- depends_on[] (optional)
```

### 6.4 DelegationTask

Persisted delegated task record created by Teams from `DelegationTaskSpec`.

### 6.5 DelegationTaskResult

Structured worker output returned by Teams to Agent and persisted for Team HQ.

## 7. Task Payload Schemas

V1 task payloads must be structured JSON, not a free-form task string.

### 7.1 `scope_json`

Purpose: define the bounds of the delegated task.

Suggested shape:

```json
{
  "included": [],
  "excluded": [],
  "boundaries": [],
  "time_horizon": "current turn"
}
```

Field meanings:

- `included`: work explicitly inside scope
- `excluded`: work explicitly outside scope
- `boundaries`: environmental or domain limits
- `time_horizon`: expected temporal scope, such as `current turn` or `background`

### 7.2 `success_criteria_json`

Purpose: define what counts as task completion.

Suggested shape:

```json
{
  "must": [],
  "should": [],
  "failure_conditions": []
}
```

Field meanings:

- `must`: required completion conditions
- `should`: desirable but non-blocking conditions
- `failure_conditions`: conditions that mean the task should not be considered successful

### 7.3 `input_context_json`

Purpose: provide the minimum context needed for specialist execution.

Suggested shape:

```json
{
  "user_request": "",
  "conversation_excerpt": [],
  "atlas_task_frame": "",
  "known_constraints": [],
  "prior_results": [],
  "artifacts": []
}
```

Field meanings:

- `user_request`: original request or concise summary
- `conversation_excerpt`: only relevant excerpts
- `atlas_task_frame`: why Atlas delegated this task
- `known_constraints`: task-specific or environmental constraints
- `prior_results`: prior delegated outputs for sequence flows
- `artifacts`: files, paths, IDs, URLs, or references needed by the worker

### 7.4 `expected_output_json`

Purpose: make delegated outputs predictable and easy for Atlas to reintegrate.

Suggested shape:

```json
{
  "type": "summary",
  "format_notes": []
}
```

Allowed `type` values in V1:

- `summary`
- `findings_list`
- `structured_brief`
- `artifact_update`

V1 tasks must have exactly one primary expected output type.

## 8. Worker Prompt Composition

Teams owns worker prompt assembly.

Agent provides the task spec and target team member. Teams constructs the worker context from canonical data.

### 8.1 Layering model

Worker prompts are composed from three layers:

1. template layer
2. member layer
3. task layer

Conflict precedence:

1. task layer
2. member layer
3. template layer

### 8.2 Required prompt sections

Each worker prompt must contain four sections:

1. Identity
2. Assignment
3. Context
4. Execution contract

### 8.3 Identity section

Includes:

- name
- template role
- mission
- persona style
- allowed skills
- allowed tool classes

### 8.4 Assignment section

Includes:

- title
- objective
- scope
- success criteria
- expected output type

### 8.5 Context section

Includes:

- user request summary
- relevant conversation excerpts
- prior results if sequence
- known constraints
- artifacts

### 8.6 Execution contract section

Includes rules such as:

- work only within scope
- use only allowed tools
- do not broaden the task
- clearly report blockers
- return output in the expected format
- do not present final user-facing prose as if you are Atlas

### 8.7 Explicit non-inheritance

Workers must not inherit the full Atlas prompt by default.

They should not receive:

- Atlas’s full persona prompt
- broad memory dumps
- unrelated team roster data
- unrelated tool policies
- unrelated conversation history

## 9. V1 Template Contracts

### 9.1 Scout

Default behavior:

- gather facts, references, comparisons, and external context
- prefer evidence over speculation
- surface uncertainty clearly
- avoid implementation unless explicitly required

Preferred output types:

- `findings_list`
- `structured_brief`

### 9.2 Builder

Default behavior:

- produce a first-pass implementation or artifact result
- optimize for useful forward progress
- make reasonable assumptions within scope
- avoid turning the task into a critique pass

Preferred output types:

- `artifact_update`
- `summary`

### 9.3 Reviewer

Default behavior:

- inspect for correctness, risk, regressions, gaps, and edge cases
- prioritize issues
- stay within review scope
- avoid rewriting work unless explicitly requested

Preferred output types:

- `findings_list`
- `structured_brief`

### 9.4 Operator

Default behavior:

- execute bounded operational or tool-driven tasks reliably
- log meaningful state changes and blockers
- prefer deterministic completion over open-ended reasoning
- stop cleanly when blocked on approval or prerequisites

Preferred output types:

- `summary`
- `artifact_update`

### 9.5 Monitor

Default behavior:

- watch for trigger conditions, failures, stale states, or anomalies
- report only meaningful change
- remain quiet when no action is required
- escalate with concise recommendations

Preferred output types:

- `structured_brief`
- `findings_list`

## 10. Teams Task Engine

Teams must use a single execution engine for both sync and async delegation.

The difference between sync and async is **wait semantics**, not execution architecture.

### 10.1 Shared engine rule

Both `sync_assist` and `async_assignment` must use the same task engine for:

- validation
- materialization
- worker preparation
- execution
- finalization
- resume

### 10.2 Engine stages

#### `validate`

Verify:

- plan shape
- target members
- capabilities
- dependency validity
- execution limits

#### `materialize`

Create:

- persisted task rows
- initial runtime state
- parent turn linkage

#### `prepare_worker`

Build:

- worker prompt
- worker tool surface
- task-specific execution context

#### `execute`

Run:

- delegated worker loop
- step logging
- approval handling
- blocker handling

#### `finalize`

Persist:

- final task state
- structured result
- artifacts
- events
- updated runtime state

#### `resume`

Resume:

- paused or waiting tasks
- preserving identity, history, and linkage

## 11. Teams Validation Rules

Teams validates feasibility. It does not redesign plans.

### 11.1 Plan-level validation

- `mode` is valid
- `pattern` is valid
- `tasks` is non-empty
- `pattern` is consistent with task dependency shape

### 11.2 Member validation

- target member exists
- target member is enabled
- target member is executable in current runtime state
- autonomy mode allows the invocation type

### 11.3 Task validation

- `title` is present
- `objective` is present
- `scope` is present
- `success_criteria` is present
- `expected_output.type` is valid
- `input_context` is present

### 11.4 Capability validation

- target member has a tool surface consistent with the task
- if no feasible tool surface exists, reject early

### 11.5 Runtime validation

- sync work fits execution limits
- parallel work does not exceed configured cap
- dependency references are valid

### 11.6 Non-authority rule

Teams may reject. Teams may not silently mutate the orchestration plan.

## 12. Persistence Model

### 12.1 `team_members`

Canonical member definition table.

Fields:

- `id`
- `name`
- `template_role`
- `mission`
- `persona_style`
- `allowed_skills_json`
- `allowed_tool_classes_json`
- `autonomy_mode`
- `activation_rules_json`
- `provider_override`
- `model_override`
- `is_enabled`
- `created_at`
- `updated_at`

### 12.2 `team_member_runtime`

Live mutable member runtime state.

Fields:

- `member_id`
- `status`
- `current_task_id`
- `last_active_at`
- `last_error`
- `updated_at`

### 12.3 `delegation_tasks`

One row per delegated task.

Fields:

- `task_id`
- `parent_conversation_id`
- `parent_turn_id`
- `delegated_by`
- `requested_by`
- `mode`
- `pattern`
- `agent_id`
- `title`
- `objective`
- `scope_json`
- `success_criteria_json`
- `input_context_json`
- `expected_output_json`
- `status`
- `depends_on_json`
- `created_at`
- `started_at`
- `finished_at`
- `updated_at`

### 12.4 `delegation_task_steps`

Execution trace for delegated work.

Fields:

- `step_id`
- `task_id`
- `sequence_number`
- `step_type`
- `status`
- `content`
- `tool_name`
- `tool_call_id`
- `created_at`
- `updated_at`

### 12.5 `delegation_task_results`

Structured worker result table.

Fields:

- `task_id`
- `output_type`
- `summary`
- `output_json`
- `artifacts_json`
- `risks_json`
- `blockers_json`
- `recommended_next_action`
- `created_at`
- `updated_at`

### 12.6 `delegation_events`

Team HQ activity feed table.

Fields:

- `event_id`
- `task_id`
- `agent_id`
- `event_type`
- `title`
- `detail`
- `payload_json`
- `created_at`

## 13. Lifecycle and Status Model

### 13.1 Team member runtime status

V1 canonical member runtime statuses:

- `idle`
- `working`
- `waiting`
- `blocked`
- `needs_review`
- `done`

### 13.2 Delegation task status

V1 canonical task statuses:

- `created`
- `assigned`
- `in_progress`
- `waiting`
- `blocked`
- `completed`
- `failed`
- `canceled`

Later extensions may add:

- `planned`
- `awaiting_review`
- `recovered`

### 13.3 Step status

V1 canonical step statuses:

- `queued`
- `active`
- `waiting`
- `blocked`
- `done`
- `failed`

## 14. Approval and Blocking Model

Approvals are Teams-owned execution states.

### 14.1 Core rule

If delegated execution hits an approval gate:

- Teams pauses the task
- Teams persists blocking state
- Teams returns a blocked or waiting result
- Agent explains the blocked state if the task is sync
- Team HQ surfaces the blocked item regardless of sync or async origin

### 14.2 Sync behavior

For `sync_assist`:

- Teams returns a blocked result to Agent
- Atlas explains what approval is needed
- the task remains resumable

### 14.3 Async behavior

For `async_assignment`:

- Teams pauses the task
- Team HQ becomes the main interaction surface

### 14.4 Blocking metadata

Persist at minimum:

- `blocking_kind`
- `blocking_detail`
- `resume_token` or equivalent resumable reference
- `pending_action_id` where applicable

Suggested `blocking_kind` values:

- `approval`
- `missing_input`
- `tool_failure`
- `dependency_wait`

## 15. Atlas Reintegration Rules

Atlas remains the final narrator for sync delegated work.

### 15.1 By output type

#### `summary`

Atlas may absorb directly into its answer.

#### `findings_list`

Atlas should interpret and prioritize findings before presenting them.

#### `structured_brief`

Atlas should synthesize the brief into a cohesive response, preserving risks and open questions when relevant.

#### `artifact_update`

Atlas should report what changed, reference produced artifacts, and explain implications.

### 15.2 Exposure of delegation

Atlas may mention a specialist when useful, but delegated outputs should not be surfaced as raw worker transcripts by default.

## 16. Tool Availability Rule

The team/delegation capability must remain available to Agent as part of the core orchestration surface.

At minimum, when one or more team members exist, Agent should always have access to:

- `team.list`
- `team.get`
- `team.delegate`

and any future orchestration actions needed for V1 behavior.

Delegation must not depend entirely on raw user wording such as “agent” or “delegate.”

## 17. Team HQ Projection Model

Team HQ must be a projection of Teams-owned state, not a custom inferred state model.

### 17.1 Atlas station

Show:

- Atlas operating mode
- current turn summary
- active delegation count
- blocked delegation count

### 17.2 Team member stations

Show:

- name
- template role
- short mission
- runtime status
- current task title
- last active
- latest result snippet

### 17.3 Task inspector

Show:

- title
- objective
- target member
- mode
- pattern
- status
- success criteria
- current blocker
- structured result
- execution step log

### 17.4 Activity rail

Show event-driven items such as:

- Atlas delegated to Scout
- Builder completed an artifact update
- Reviewer flagged risks
- Operator is waiting on approval
- Monitor detected a trigger condition

### 17.5 Blocked strip

Show active attention items:

- tasks waiting on approval
- blocked tasks
- failed tasks needing review

## 18. Migration Direction

### 18.1 Current state

The current implementation has:

- markdown-backed member definitions
- a thin delegated task model
- direct Agent reads of team roster markdown
- generic worker prompt framing

### 18.2 Target migration sequence

Recommended order:

1. define canonical Teams-owned internal types
2. add new persistence tables or evolve existing ones toward the V1 model
3. move Agent team awareness behind the Teams API
4. replace string delegation with structured `DelegationPlan`
5. replace generic worker prompt assembly with layered prompt construction
6. route sync and async through the same Teams engine
7. reproject Team HQ from the new canonical model
8. remove direct architectural reliance on `AGENTS.md`

### 18.3 Compatibility

If temporary compatibility is needed:

- legacy team member records may be imported once into canonical Teams storage
- markdown should not remain a live source of truth after migration

## 19. Explicit Non-Goals For V1

V1 does not require:

- free-form autonomous agents
- unlimited parallel worker orchestration
- custom output schemas per member
- full multi-turn subagent memory ownership
- dynamic agent self-reconfiguration
- Agents-as-files architecture

## 20. Acceptance Criteria

The V1 implementation should satisfy all of the following:

- Atlas can delegate without losing its role as primary agent
- Teams owns all delegated execution paths
- sync and async use the same task engine
- team members have bounded identity beyond simple tool filtering
- delegated tasks are structured, not string-only
- delegated results are structured and reintegratable
- Team HQ reflects actual runtime state from canonical persistence
- Agent no longer depends on `AGENTS.md` for runtime team awareness

