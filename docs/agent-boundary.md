# Atlas Agent Boundary

**Last updated: 2026-04-05**

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

Today the Agent boundary is primarily represented by:
- `atlas-runtime/internal/chat/service.go`
- `atlas-runtime/internal/chat/tool_router.go`
- `atlas-runtime/internal/chat/keychain.go`
- `atlas-runtime/internal/chat/broadcaster.go`
- `atlas-runtime/internal/domain/chat.go`

The package name may still be `chat`, but architecturally this subsystem should be understood as **Agent**.

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

Future delegated multi-agent work in Atlas will follow this rule:

**Agent owns delegation decisions. Teams owns delegated execution.**

This rule is architectural, not optional.

That means:
- Agent decides whether work should be delegated
- Agent defines the goal, scope, and success criteria
- Agent remains accountable for the final outcome returned to the user
- Teams creates and manages subordinate workers or subagents
- Teams tracks delegated task state and gathers results
- Teams reports completed work back to Agent

This rule must not be inverted.

In particular:
- Teams must not become the top-level decision maker
- Agent must not absorb the worker-management system

The reason is simple:
- Agent is the orchestration core of Atlas
- Teams is the delegated execution system used by Agent

If Atlas adds subagents, minions, or parallel worker systems in the future,
they should be designed around this principle.

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
