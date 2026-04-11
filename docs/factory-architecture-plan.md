# Atlas Factory Architecture Plan

**Last updated: 2026-04-11**

Related:
- [`docs/architecture.md`](architecture.md)
- [`docs/agent-boundary.md`](agent-boundary.md)
- [`docs/internal-modules.md`](internal-modules.md)
- [`docs/workflows-module-audit.md`](workflows-module-audit.md)
- [`docs/automations-module-audit.md`](automations-module-audit.md)
- [`docs/custom-skills.md`](custom-skills.md)

## Purpose

This document defines the target architecture and implementation plan for the
next major evolution of Atlas capabilities.

The direction is:

- `Agent` decides what to do now.
- `Forge` creates new reusable capabilities.
- `Skills` provide atomic reusable capability units.
- `Workflows` compose skills and other execution steps.
- `Automations` trigger execution and route outputs.

This is a shift from:

- Forge as a custom-script generator
- Workflows as prompt wrappers
- Automations as prompt runners with delivery bolted on

to:

- Forge as a factory
- Workflows as the execution substrate
- Skills as reusable user-facing entrypoints
- Automations as trigger/delivery bindings

## Why This Change Is Needed

Atlas already has strong pieces, but they do not yet line up cleanly:

- Forge can propose and install capabilities, but its default runtime is still a
  subprocess custom skill in `internal/forge/codegen.go`.
- Workflows already sit closest to a process layer, but they are still mostly
  prompt-oriented in `internal/workflowexec/workflowexec.go`.
- Automations already try to sit above workflows, but the current model mixes
  direct prompt execution and workflow execution in `internal/modules/automations/module.go`.
- Custom skills are useful, but as documented in `docs/custom-skills.md`, they
  are limited by short-lived subprocess execution, weak artifact handling,
  no durable state, and no first-class vault/runtime integration.

The product goal is not to remove these features. It is to align them into one
coherent capability system.

## Core Principles

### 1. Atlas remains the primary agent

Atlas is always the top-level orchestrator.

- Atlas owns the user relationship.
- Atlas owns request-time decisions.
- Atlas owns final presentation of results.
- Atlas may invoke Forge, but Forge does not replace Atlas.

This is consistent with [`docs/agent-boundary.md`](agent-boundary.md).

### 2. Forge is a factory, not a parallel agent

Forge exists to create new reusable capabilities when Atlas detects that the
current system cannot fulfill a need cleanly with what already exists.

Forge should not become a second top-level orchestrator.

### 3. Skills and workflows must not overlap semantically

- A `Skill` is a reusable callable capability surface.
- A `Workflow` is an execution graph that composes multiple steps.

A workflow may use multiple skills. A skill may be backed by a workflow.
But they are not the same concept.

### 4. Automations are triggers, not a second execution runtime

An automation should not define its own capability semantics.

An automation should:

- decide when something runs
- supply default inputs
- specify delivery behavior
- track operational status

The executable target should be a skill, workflow, or narrow command surface.

### 5. Prefer composition before creation

If Atlas can satisfy a request by composing existing capabilities, it should do
that before invoking Forge to build something new.

### 6. Prefer structured capability contracts over free-form scripts

The runtime should understand:

- what a capability requires
- what it produces
- what permissions it needs
- what artifacts it emits

This should not be implicit in generated code.

## Responsibility Matrix

### Agent

Owns:

- user-intent understanding
- capability lookup
- gap detection
- build-vs-run decision
- request-time orchestration
- approvals and recovery
- execution of existing capabilities

Does not own:

- capability synthesis
- workflow definition storage semantics
- automation scheduling semantics

### Forge

Owns:

- capability planning for reusable assets
- skill creation
- workflow creation
- capability packaging
- validation and installation
- updates and distribution metadata

Does not own:

- top-level user request orchestration
- day-to-day execution of existing capabilities

### Skills

Own:

- reusable callable capability surfaces
- parameter schemas
- permissions and action classes
- output/artifact contracts
- discoverability metadata

Do not own:

- scheduling
- graph execution
- delivery routing

### Workflows

Own:

- multi-step execution graphs
- step state
- retries/checkpoints
- artifact propagation
- trust-bounded composition

Do not own:

- user-facing reusable capability identity by default
- scheduling or delivery policy

### Automations

Own:

- triggers and schedules
- default inputs
- delivery routes
- operational run history

Do not own:

- capability semantics
- custom orchestration logic beyond trigger/delivery concerns

## Target Concept Model

The target system introduces three normalized capability records and one shared
execution target abstraction.

### SkillDefinition

Represents a reusable callable capability.

Suggested fields:

```json
{
  "id": "theme-to-pdf",
  "name": "Theme To PDF",
  "description": "Generate themed text and save it as a PDF.",
  "category": "productivity",
  "source": "builtin | forge | custom",
  "executorType": "native | workflow | custom_subprocess",
  "executorRef": "workflow:theme-to-pdf.v1",
  "inputSchema": {},
  "outputSchema": {},
  "artifactTypes": ["file.pdf"],
  "riskLevel": "low | medium | high",
  "requiredCapabilities": ["llm.generate", "fs.create_pdf"],
  "requiredSecrets": [],
  "requiredRoots": [],
  "isReusable": true,
  "isEnabled": true,
  "createdAt": "RFC3339",
  "updatedAt": "RFC3339"
}
```

### WorkflowDefinition

Represents a durable multi-step execution graph.

Suggested fields:

```json
{
  "id": "theme-to-pdf.v1",
  "name": "Theme To PDF",
  "description": "Generate themed content and save it as a PDF.",
  "version": 1,
  "kind": "workflow",
  "inputSchema": {},
  "steps": [],
  "outputSchema": {},
  "artifactTypes": ["file.pdf"],
  "trustScope": {},
  "retryPolicy": {},
  "isReusable": true,
  "isEnabled": true
}
```

### AutomationDefinition

Represents a trigger binding for an executable target.

Suggested fields:

```json
{
  "id": "weekly-theme-pdf",
  "name": "Weekly Theme PDF",
  "trigger": {
    "type": "schedule",
    "schedule": "every friday at 18:00"
  },
  "target": {
    "type": "skill",
    "ref": "theme-to-pdf"
  },
  "defaultInputs": {
    "theme": "weekly reflection",
    "path": "/Users/you/Desktop/weekly-reflection.pdf"
  },
  "delivery": {
    "mode": "file | chat | channel | email | multiple"
  },
  "isEnabled": true
}
```

### ExecutableTarget

Represents the normalized target an automation or Agent can run.

```json
{
  "type": "skill | workflow | command",
  "ref": "string"
}
```

## Target Workflow Step Model

Workflows should become typed execution graphs. The first version should remain
strictly sequential.

### Supported step types in V1

- `llm.generate`
- `llm.transform`
- `atlas.tool`
- `http.request`
- `local.script`
- `return`

### Example

```json
{
  "id": "theme-to-pdf.v1",
  "steps": [
    {
      "id": "draft",
      "type": "llm.generate",
      "prompt": "Write a concise PDF-ready document about theme: {inputs.theme}"
    },
    {
      "id": "save",
      "type": "atlas.tool",
      "tool": "fs.create_pdf",
      "args": {
        "path": "{inputs.path}",
        "title": "{inputs.theme}",
        "content": "{steps.draft.output.text}"
      }
    },
    {
      "id": "done",
      "type": "return",
      "value": {
        "message": "Saved PDF to {inputs.path}",
        "artifacts": ["{steps.save.artifacts[0].id}"]
      }
    }
  ]
}
```

### Step execution rules

- Steps execute in order.
- Each step emits structured output.
- Each step may emit zero or more artifacts.
- Each step may reference:
  - `inputs.*`
  - `steps.<id>.output.*`
  - `steps.<id>.artifacts[*]`
- Unsupported step types fail validation rather than degrade silently.

## Artifact Model

Plain text output is not enough for Atlas's target product.

Introduce typed artifacts with a stable contract.

### Initial artifact types

- `text`
- `json`
- `table`
- `file`
- `image`
- `message`
- `email_draft`

### Example artifact

```json
{
  "id": "artifact-123",
  "type": "file",
  "subtype": "application/pdf",
  "name": "weekly-reflection.pdf",
  "path": "/Users/you/Desktop/weekly-reflection.pdf",
  "sizeBytes": 48122,
  "createdAt": "RFC3339"
}
```

## Agent Decision Pipeline

The Agent should use a strict deterministic pipeline to reduce confusion.

### Step 1: Understand goal

Convert the user request into a normalized desired outcome.

Example:

- input: "Every Friday make a themed PDF recap and send it to me"
- normalized goal:
  - generate text
  - create PDF
  - schedule execution
  - deliver output

### Step 2: Resolve required capability types

Map the goal to capability requirements.

Example:

- `generate_text`
- `create_pdf`
- `schedule_execution`
- `deliver_chat`

### Step 3: Match capability requirements against capability registry

The Agent should query one canonical capability inventory rather than infer from
raw tool names.

### Step 4: Detect gaps

A gap must be classified as one of:

- `capability_missing`
- `prerequisite_missing`
- `permission_missing`
- `policy_disallowed`
- `delivery_unavailable`

### Step 5: Choose exactly one action path

Allowed action paths:

- `run_existing`
- `compose_existing`
- `forge_new`
- `ask_for_prerequisite`

### Hard rules

- Never forge a new capability if existing capabilities compose cleanly.
- Never ask for a new capability before checking composition.
- Never fail late when a missing prerequisite can be detected early.
- Never silently degrade the plan without telling the user.

## Forge Factory Model

Forge should become the factory for:

- new skills
- new workflows
- skill + workflow bundles
- later: workflow + automation bundles

### Forge output types

- `skill`
- `workflow`
- `skill_workflow_bundle`
- `automation_bundle` (future)

### Forge planner rules

When invoked by the Agent, Forge should:

1. inspect existing capabilities
2. decide whether a new reusable skill is needed
3. decide whether a workflow is sufficient
4. build the smallest reusable asset that solves the identified gap

### Forge default behavior

Forge should prefer:

- workflow-backed skills
- native Atlas tool composition
- narrow adapters for real external integrations

Forge should avoid:

- generating one-off local scripts for capabilities Atlas already has
- encoding reusable product logic as subprocess code by default

## Automation Model

Automations should target normalized executable targets.

### Valid targets

- `skill`
- `workflow`
- `command` (narrow escape hatch only)

### Automation responsibilities

- trigger
- schedule
- default inputs
- delivery
- run history
- operational health

### Automation must not own

- business logic for how the task is executed
- custom workflow semantics

## Current Gaps To Address

The following missing elements must be added to make the new architecture work:

### 1. Capability registry

Atlas currently lacks one canonical capability inventory spanning:

- built-in skills
- forged skills
- workflow-backed skills
- step types
- delivery options

### 2. Agent gap analyzer

The Agent currently lacks a structured build-vs-run decision system.

### 3. Typed workflow runtime

Current workflows are not yet a true step graph runtime.

### 4. Workflow-backed skill executor

There is currently no stable native executor type for "this skill is backed by
a workflow".

### 5. Artifact-aware execution

Current outputs are mostly strings or flat JSON payloads.

### 6. Prerequisite manager

Atlas needs a normalized way to reason about:

- credentials
- approved roots
- communication channels
- trust scopes

### 7. Forge packaging beyond custom scripts

Forge currently defaults to codegen of subprocess skills.

## Implementation Phases

## Phase 1 - Freeze Contracts

Goal:
define the target model before changing runtime behavior.

Deliverables:

- this design doc
- responsibility matrix
- shared target vocabulary
- initial schema drafts

Primary files:

- `docs/factory-architecture-plan.md`
- future shared types under `atlas-runtime/internal/platform` or a new package

Exit criteria:

- one canonical vocabulary for capability creation and execution

## Phase 2 - Capability Registry

Goal:
introduce one inventory for the Agent and Forge.

Deliverables:

- capability registry service
- capability metadata for built-ins, forge outputs, and workflow-backed skills
- basic query surface for Agent planning

Likely file areas:

- `atlas-runtime/internal/skills/registry.go`
- `atlas-runtime/internal/features/skills.go`
- new capability package/module

Exit criteria:

- Agent can ask "what exists?" and "what is missing?"

## Phase 3 - Agent Gap Analysis

Goal:
make run/compose/forge/ask deterministic.

Deliverables:

- planning IR
- gap classifier
- build-vs-run policy

Likely file areas:

- `atlas-runtime/internal/chat/service.go`
- `atlas-runtime/internal/chat/tool_router.go`
- possibly new planner files under `atlas-runtime/internal/agent` or `internal/chat`

Exit criteria:

- Agent stops improvising unsupported capability plans

## Phase 4 - Typed Workflow Runtime

Goal:
turn workflows into the main execution substrate.

Deliverables:

- typed step executor
- sequential step persistence
- artifact propagation
- step-level approval hooks

Likely file areas:

- `atlas-runtime/internal/workflowexec/workflowexec.go`
- `atlas-runtime/internal/modules/workflows/module.go`
- `atlas-runtime/internal/storage/db.go`

Exit criteria:

- workflows can execute `llm.generate` and `atlas.tool` steps

## Phase 5 - Workflow-Backed Skills

Goal:
allow reusable skills to be backed by workflow definitions.

Deliverables:

- new executor type: `workflow`
- skill records that reference workflow executors
- migration support for legacy custom subprocess skills

Likely file areas:

- `atlas-runtime/internal/customskills/manifest.go`
- `atlas-runtime/internal/skills/registry.go`
- skill feature/catalog code

Exit criteria:

- skill identity is separated from implementation backend

## Phase 6 - Forge As Factory

Goal:
make Forge synthesize the right reusable asset instead of defaulting to scripts.

Deliverables:

- Forge output types
- workflow-backed capability packaging
- custom subprocess generation as fallback only

Likely file areas:

- `atlas-runtime/internal/forge/service.go`
- `atlas-runtime/internal/forge/codegen.go`
- `atlas-runtime/internal/skills/forge_skill.go`

Exit criteria:

- Forge creates workflow-backed skills by default when composition is the right answer

## Phase 7 - Automation Target Normalization

Goal:
make automations clean trigger bindings.

Deliverables:

- target ref support
- target-based execution path
- standardized delivery records

Likely file areas:

- `atlas-runtime/internal/modules/automations/module.go`
- `atlas-runtime/internal/modules/automations/agent_actions.go`
- `atlas-runtime/internal/storage/db.go`

Exit criteria:

- automations no longer rely on prompt-only execution as the default model

## Phase 8 - Artifacts, Delivery, and Prerequisites

Goal:
support rich outputs and accurate failure handling.

Deliverables:

- artifact store contract
- typed delivery payloads
- prerequisite manager

Exit criteria:

- outputs can be sent to file, chat, channel, or email from one shared runtime model

## Phase 9 - UI and API Alignment

Goal:
make the new architecture visible and operable in the product.

Deliverables:

- Forge UX for creating skill/workflow/automation bundles
- workflow step UI
- automation target UI
- skill executor metadata UI

Likely file areas:

- `atlas-web/src/screens/Forge.tsx`
- `atlas-web/src/screens/Skills.tsx`
- `atlas-web/src/screens/Workflows.tsx`
- `atlas-web/src/screens/Automations.tsx`
- `atlas-web/src/api/contracts.ts`

Exit criteria:

- users can understand and manage the new system without internal docs

## First End-To-End Milestone

The first milestone should prove the architecture with one complete path:

### "Theme to PDF"

Desired flow:

1. Forge creates a workflow-backed skill named `theme-to-pdf`
2. The workflow uses:
   - `llm.generate`
   - `atlas.tool` calling `fs.create_pdf`
3. An automation can target that skill on a schedule
4. The output can be saved locally and optionally delivered through a configured channel

This milestone proves:

- workflow-backed skills
- typed step execution
- native Atlas tool composition
- automation target execution
- artifact-aware output

## Risks

### 1. Replacing custom skills too early

Custom subprocess skills are still useful as a compatibility path. They should
be demoted from the default model, not removed immediately.

### 2. Over-generalizing workflows before V1 step types stabilize

The first typed workflow runtime should stay sequential and explicit.

### 3. Weak artifact contracts

If outputs remain free-form strings, the system will continue to confuse
generation with execution and delivery.

### 4. Agent and Forge role ambiguity

This must stay explicit during migration:

- Agent decides
- Forge creates

### 5. Prompt-centric automations lingering too long

Automations should transition to target-based execution once the workflow-backed
skill path exists.

## Non-Goals For The First Cut

The initial migration does not need:

- a visual DAG editor
- parallel step execution
- complex branching/looping
- public plugin marketplace support
- distributed worker infrastructure

Those may come later, but they are not required to establish the architecture.

## Recommendation

Start implementation at the contract and execution layers, not the UI layer.

Recommended immediate order:

1. freeze schemas and target vocabulary
2. add capability registry
3. add Agent gap analysis
4. implement typed workflow runtime
5. add workflow-backed skills
6. refactor Forge into a factory
7. normalize automations onto target refs

This preserves a working Atlas at every step while steadily moving the center
of gravity away from ad hoc subprocess scripts and toward reusable, durable,
inspectable capabilities.
