# PLAN

## V1.0

### Context
- Atlas Teams is the headline new capability for the next major product phase.
- Desktop Operator and Proactive Atlas already exist in partial form and should mature alongside Teams, but Teams is the primary new feature.
- Atlas remains the primary agent at all times. Team members extend Atlas but do not replace it.
- Before starting this work, the app will first be migrated to Go + TypeScript rather than SwiftUI.

### Product Pillars
- New: Atlas Teams
- Expanded: Desktop Operator
- Expanded: Proactive Atlas

### Atlas Teams Foundation

#### Core rule
- Atlas is always the primary agent.
- Team members extend Atlas’s capability, but they do not replace Atlas.
- Atlas remains:
  - the main user-facing intelligence
  - the orchestrator
  - the memory owner
  - the final presenter of results
  - the fallback worker when no suitable specialist exists

#### Atlas operating modes
- Solo Atlas
  - Atlas handles the task directly.
- Atlas + Specialist
  - Atlas owns the task but delegates one focused part to a specialist.
- Atlas as Team Lead
  - Atlas breaks a task into multiple parts and coordinates several specialists.

#### Product principles
- Atlas never refuses ordinary work because the user has no team.
- Delegation is additive, not required.
- A small team does not weaken Atlas.
- Atlas should explain delegation simply when useful.
- Atlas is never just a router.

### Team Member Templates

#### Scout
- Role: Research Specialist
- Job: gather facts, references, docs, comparisons, and external context
- Best for: research-heavy prompts, competitive scans, documentation lookups
- Default skills: search, documentation, web/research tools
- Default autonomy: assistive

#### Builder
- Role: Drafting and Implementation Specialist
- Job: produce first-pass artifacts
- Best for: code drafts, written drafts, structured plans, implementation passes
- Default skills: build/write/implementation-oriented skills
- Default autonomy: on-demand

#### Reviewer
- Role: Quality Specialist
- Job: inspect outputs for risk, clarity, correctness, and regressions
- Best for: code review, plan review, edge-case checking, polish checks
- Default skills: review, validation, testing, critique-oriented skills
- Default autonomy: assistive or post-task trigger

#### Operator
- Role: Execution Specialist
- Job: carry out bounded actions across desktop, workflows, and tools
- Best for: deterministic execution, operational steps, environment interaction
- Default skills: desktop/operator/workflow/action skills
- Default autonomy: on-demand

#### Monitor
- Role: Watcher
- Job: observe important state changes and surface issues or opportunities
- Best for: approvals, daemon issues, failed automations, stale workflows, connection problems
- Default skills: monitoring/status/logs/workflow/notification skills
- Default autonomy: bounded autonomous

### Agent Data Model
- Each Atlas Team member should have:
  - `id`
  - `name`
  - `templateRole`
  - `mission`
  - `personaStyle`
  - `allowedSkills`
  - `allowedToolClasses`
  - `autonomyMode`
  - `activationRules`
  - `status`
  - `currentTaskID`
  - `recentOutputs`
  - `taskHistory`
  - `usageStats`
  - `createdAt`
  - `updatedAt`
  - `isEnabled`

#### Suggested enums
- Autonomy mode
  - `on_demand`
  - `assistive`
  - `bounded_autonomous`
- Status
  - `idle`
  - `working`
  - `waiting`
  - `blocked`
  - `needs_review`
  - `done`

### Team HQ Interface

#### Core layout
- Atlas station
- team workshop floor
- activity rail
- task/output inspector

#### Atlas station
- Atlas should have a dedicated central station, visually distinct from team members.
- Show:
  - Atlas name
  - current mode
  - current task summary
  - active delegation summary
  - quick actions

#### Team member stations
- Each team member should appear as a station, not just a boring card.
- Show:
  - agent name
  - role
  - status
  - current task or idle message
  - recent output snippet
  - last active
  - quick actions

#### Activity rail
- Show:
  - Atlas assigned Scout a research task
  - Builder completed a draft
  - Reviewer flagged a risk
  - Monitor detected a daemon issue
  - Operator is waiting on approval

#### Empty state
- When no team members exist, Team HQ should still feel intentional.
- Reinforce:
  - Atlas already works on its own
  - specialists can help Atlas with focused jobs

### Task Lifecycle
- Task states:
  - created
  - planned
  - assigned
  - in_progress
  - blocked
  - awaiting_review
  - completed
  - failed
  - canceled
  - recovered
- Step states:
  - queued
  - active
  - waiting
  - blocked
  - done
  - failed
- Agent states:
  - idle
  - working
  - waiting
  - blocked
  - done

#### Delegation patterns
- Atlas direct
- Single specialist assist
- Sequential squad
- Parallel squad

### Creation And Editing

#### Creation entry points
- Team HQ
- Atlas chat
- Suggested actions

#### Creation flow
1. Choose template
2. Name the agent
3. Define mission
4. Choose allowed skills
5. Choose autonomy level
6. Review and create

#### Editing flow
- Editable fields:
  - name
  - mission
  - allowed skills
  - autonomy mode
  - enabled/disabled

#### Disable vs retire
- Disable
  - temporarily unavailable
- Retire / Remove
  - no longer part of the active team
  - history preserved

### Chat Command Grammar

#### Create
- “Create a new research agent”
- “Make a reviewer for release quality”
- “Add a monitor for approvals”

#### Edit
- “Rename Scout to Pathfinder”
- “Give Builder access to documentation skills too”
- “Turn Reviewer into on-demand only”

#### Assign
- “Ask Scout to research this”
- “Have Builder draft a first pass”
- “Send this to the team”

#### Inspect
- “What is Scout doing?”
- “Show me blocked agents”
- “What did Builder finish today?”

#### Recommend
- “Do I need another team member?”
- “What agent am I missing?”

### Bounded Autonomy + Trigger Model

#### Core rule
- Agents never free-roam.
- Autonomy in V1.0 is always:
  - bounded
  - trigger-based
  - Atlas-mediated
  - auditable

#### Autonomy levels
- On Demand
  - never self-activates
- Assistive
  - Atlas may invoke when relevant inside a live task
- Bounded Autonomous
  - Atlas may consider activation when specific triggers occur

#### Trigger types
- System Health
- Workflow / Automation
- Approvals
- Communications
- Scheduled Review
- Atlas In-Task Assist

#### Trigger evaluation flow
1. Event occurs
2. Atlas receives trigger
3. Atlas filters candidates
4. Atlas decides
5. Agent runs
6. Atlas interprets result

#### Guardrails
- Atlas must evaluate first
- No risky silent action
- Cooldowns
- Trigger deduplication
- Atlas fallback

#### Recommended V1.0 autonomous scenarios
- Daemon issue detected
- Automation fails
- Approval backlog grows
- Build-review chain
- Morning workspace check

### Runtime Architecture (implemented)

#### Persistence model
- SQLite is the authoritative source for agent definitions and runtime state.
- `AGENTS.md` is export-only. It is no longer read on startup except for a one-time migration import if the DB is empty on first upgrade.

#### Implemented persistent entities
- `agent_definitions` — agent definition rows (id, name, role, mission, skills, autonomy, etc.)
- `agent_runtime` — per-agent runtime status (idle/working/paused/needs_review)
- `agent_tasks` — delegated task rows with full V1 structured payload (title, objective, scope JSON, success criteria JSON, expected output JSON, mode, pattern)
- `agent_task_steps` — per-step conversation records (system/user/assistant/tool roles)
- `agent_events` — team event log (task started/completed/failed/approved/rejected)
- `agent_metrics` — per-agent task completion and tool call counters

#### Key implementation files
| File | Role |
|---|---|
| `internal/modules/agents/module.go` | Routes, DB-first CRUD, Team HQ snapshot, approval/cancel, sync/export, trigger coordinator |
| `internal/modules/agents/agent_actions.go` | `team.*`/`agent.*` skills — delegate (single/sequence), CRUD, enable/disable/pause/resume |
| `internal/modules/agents/prompt.go` | `composeWorkerPrompt` — four-section layered prompt with five template contracts |
| `internal/modules/agents/agents_file.go` | AGENTS.md parse/render — used only by `POST /agents/sync` and `GET /agents/export` |

### `AGENTS.md` Role (updated)
- `AGENTS.md` is now export-only.
- SQLite is the canonical team-definition store.
- `GET /agents/export` renders current DB state as AGENTS.md format.
- `POST /agents/sync` does an explicit on-demand import from AGENTS.md into the DB.
- On first startup after upgrade, if the DB is empty and AGENTS.md exists, it is imported once automatically.

### `AGENTS.md` Format
- Structured markdown with one section per agent.

#### File structure
- `# Atlas Team`
- `## Atlas`
- `## Team Members`
- `### Agent Name`
  - `- ID: ...`
  - `- Role: ...`
  - `- Mission: ...`
  - `- Style: ...`
  - `- Allowed Skills: ...`
  - `- Allowed Tool Classes: ...`
  - `- Autonomy: ...`
  - `- Activation: ...`
  - `- Enabled: yes|no`

#### Required fields
- `ID`
- `Role`
- `Mission`
- `Allowed Skills`
- `Autonomy`
- `Enabled`

### Team HQ API Snapshot Schema

#### `GET /team`
- Returns:
  - Atlas node
  - agents
  - activity
  - blocked items
  - suggested actions

#### Additional endpoints
- `GET /team/agents`
- `GET /team/agents/:id`
- `POST /team/agents`
- `PUT /team/agents/:id`
- `DELETE /team/agents/:id`
- `POST /team/agents/:id/pause`
- `POST /team/agents/:id/resume`
- `POST /team/agents/:id/disable`
- `POST /team/agents/:id/enable`
- `GET /team/tasks`
- `GET /team/tasks/:id`
- `POST /team/tasks`
- `POST /team/tasks/:id/cancel`
- `GET /team/events`
- `POST /team/sync`

### Implementation Roadmap

#### ✅ Milestone 1: Foundations — COMPLETE
- AGENTS.md file support, parser and writer
- sync engine (`POST /agents/sync`)
- structured persistence for definitions and runtime state (SQLite)

#### ✅ Milestone 2: Team HQ Skeleton — COMPLETE
- `GET /agents/hq` Team HQ snapshot route
- Atlas station, agent stations, activity rail, blocked items, suggested actions
- `GET /agents`, `GET /agents/{id}`, `GET /agents/tasks`, `GET /agents/events`

#### ✅ Milestone 3: Creation and Editing — COMPLETE
- DB-first create/update/delete via `POST /agents`, `PUT /agents/{id}`, `DELETE /agents/{id}`
- enable/disable/pause/resume routes and skills
- `agent.create`, `agent.update`, `agent.delete` chat skills
- `GET /agents/export` — renders DB state as AGENTS.md (export-only)

#### ✅ Milestone 4: Single-Agent Delegation — COMPLETE
- `team.delegate` with `DelegationPlan` / `DelegationTaskSpec` structured input
- `sync_assist` (blocking) and `async_assignment` (fire-and-forget with pre-generated task ID)
- per-agent provider override (`providerType`, `model`)
- four-section layered worker prompt (`composeWorkerPrompt`) with five template contracts
- full approval flow: `POST /agents/tasks/{id}/approve`, `POST /agents/tasks/{id}/reject`
- task cancel: `POST /agents/tasks/{id}/cancel`
- `agent_task_steps` for sub-agent conversation records (not in `conversations` table)
- zero changes to `internal/agent/loop.go` or `internal/chat/service.go`

#### ✅ Milestone 5: Sequential Team Workflows — COMPLETE
- `sequence` pattern in `team.delegate` — preserves full `DelegationTaskSpec` metadata per step
- step-to-step output passing (prior summary injected into next step's task instruction)
- per-agent provider override applied per step
- structured task payload (title, objective, scope, success criteria, expected output) stored in DB
- Team HQ V1 projection: BlockingKind/BlockingDetail surfaced, status normalization at read time

#### ✅ Milestone 6: Bounded Autonomy — COMPLETE
- `triggerCoordinator` with cooldown atomicity
- bounded autonomy rules (on_demand / assistive / supervised)
- trigger event storage (`trigger_events`, `trigger_cooldowns`)
- `GET /agents/triggers`, `POST /agents/triggers/evaluate`

#### ✅ Milestone 7: DB-First Migration + Cleanup — COMPLETE (Teams V1)
- DB is now the authoritative source — AGENTS.md is export-only
- one-time import guard on startup
- stale comment removal, converter round-trip tests
- parallel delegation rejects at validation time with clear error
- multi-pending approval behavior made explicit and correct in `resumeDelegatedTask`

#### ⏳ Remaining / Future
- **Parallel delegation** — `pattern: "parallel"` defined in types, rejected at validation. Not yet implemented.
- **Metrics and polish** — richer detail views, suggested actions, workshop visual polish, token/cost efficiency tuning

### Risks
- Overbuilding the runtime before the first useful experience
- Overcomplicating autonomy
- Letting agents become full independent copilots too early
- UI charm outrunning clarity

### Success Criteria
- users can create a specialist quickly
- understand what each agent does
- assign work confidently
- see what agents are doing
- trust outputs and boundaries
- feel that Atlas is coordinating a real team, not just faking parallelism
