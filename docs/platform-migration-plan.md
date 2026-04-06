# Atlas Platform Migration Plan

**Last updated: 2026-04-05** · Status: Tranche 1 in progress

---

## Why This Plan Changed

The previous migration plan correctly identified a real problem: Atlas runtime development is bottlenecked by a growing private core. The original proposal leaned too heavily on a bus-first internal platform as the primary solution.

That is no longer the guiding principle.

The new goal is simpler and stricter:

**Keep the private core thin so product development can move faster.**

This migration is about:
- stopping feature logic from accreting in `main.go`, large domain handlers, and central runtime services
- introducing a small private platform layer that internal modules can build on
- preserving a future public plugin architecture without turning it into the center of the runtime today

This is **not** a public plugin migration and **not** a full module extraction pass yet.

---

## Target Architecture

Atlas will evolve toward three distinct layers:

### 1. Private Core

Core remains private and trusted. It owns only shared runtime infrastructure:
- runtime bootstrap and lifecycle
- config access
- auth/session
- router mounting
- storage contracts and migration framework
- agent runtime
- context assembly seam
- selective internal event bus
- internal module registry

Core should not absorb product feature logic.

### 2. Private First-Party Internal Modules

These are Atlas product features implemented behind strict internal boundaries.

Examples:
- approvals
- automations
- communications
- teams
- forge
- mind
- engine
- voice
- workflows

Rules:
- modules depend on core contracts
- modules do not import each other directly unless a core-declared contract explicitly allows it
- modules own their own routes, background jobs, and orchestration
- modules use scoped storage contracts instead of informal cross-feature DB access

### 3. Future Public Extension Layer

This migration does not implement the public extension platform, but the architecture must leave room for it.

The current intended direction is:
- hosted subprocess plugins
- Atlas-owned HTTP surface
- curated public event catalog
- explicit capabilities and manifests
- promotion path for selected first-party skills/plugins

This public layer is separate from the private internal module system.

---

## Migration Principles

- Optimize for **core slimming**, not abstraction purity
- Prefer **strict internal boundaries** over convenience imports
- Use an event bus **selectively**, only where asynchronous decoupling clearly helps
- Keep the runtime fully functional throughout the migration
- Separate **private internal architecture** from the future **public extension architecture**
- Design selected first-party capabilities so they can later be promoted outward with limited rework

---

## Tranche 1: Phase 0 + Core Skeleton

### Goal

Create the private platform seams and refresh the migration source of truth without extracting feature logic yet.

### Definition of success

- this document is the new canonical migration plan
- baseline tests reflect the real current Go runtime API and major flows
- a private platform skeleton exists for host/module lifecycle, core contracts, and selective events
- no feature module has been extracted yet
- future module extractions have a clear landing zone

### What Tranche 1 includes

#### 1. Replace the old migration framing

Retire the bus-first framing and make thin-core architecture the primary success criterion.

#### 2. Baseline current runtime behavior

Add or refresh tests against the actual current runtime shape, including:
- current route names and response shapes
- chat SSE behavior
- approval resolution flow
- automation execution flow
- communications ingress/management baseline

These tests must reflect the real Go runtime API, not older conceptual route shapes.

#### 3. Introduce a private platform skeleton

Add a new private platform layer for:
- `Host`
- `Module`
- `ModuleRegistry`
- `EventBus`
- router mounting contract
- scoped storage contracts
- `AgentRuntime`
- `ContextAssembler`

This layer is a landing zone for future extractions. It does not own feature logic yet.

#### 4. Keep runtime behavior intact

Do not extract modules in this tranche.
Do not move feature behavior out of existing domains/services yet.
Only make the minimum bootstrapping changes required so the new platform skeleton can coexist with the current runtime.

---

## Planned Extraction Order

Default next extraction order:
1. Approvals
2. Automations
3. Communications

Reason:
- all three currently contribute to central wiring pressure
- approvals and automations have especially clear async seams
- communications is a strong alternative first extraction when inbound/outbound orchestration becomes the immediate pain point

If we intentionally choose an alternative first extraction, **Communications** is the preferred override candidate.

---

## Core Contracts to Stabilize Early

The following private contracts should exist before feature extraction begins:

- `Host`
- `Module`
- `ModuleRegistry`
- `EventBus`
- router mounting contract
- scoped storage contracts for early modules
- `AgentRuntime`
- `ContextAssembler`

These are internal runtime contracts only. They are not public plugin APIs.

---

## Event Bus Policy

Atlas will use a **selective internal event bus**, not a universal one.

Use the event bus for:
- approval resolution and continuation signals
- automation triggers and completion events
- communications inbound events
- lifecycle and health events
- future team activation triggers

Do not force ordinary synchronous request/response flows onto the bus when direct interfaces are simpler.

---

## Storage Policy

Core owns storage contracts and the migration framework.

Modules should consume storage through scoped contracts/adapters rather than raw, informal, cross-feature DB access. The first scoped contracts should support the early extraction candidates:
- approvals
- automations
- communications

This keeps future module boundaries explicit without requiring a full storage rewrite up front.

---

## Startup Composition Policy

Startup should move toward a private module registry model:
- core bootstraps shared services
- core constructs the private host
- internal modules register themselves against the host
- modules mount routes and subscribe to events through that host

In early tranches, existing feature wiring may still remain in `main.go` while the platform skeleton is introduced.

---

## Enforcement

Architecture rules should be enforced both socially and mechanically.

Required:
- documentation that defines what belongs in core vs internal modules
- lightweight CI checks that catch forbidden dependency direction

Target dependency rules:
- core must not import internal product modules
- modules should depend on core contracts, not each other directly
- future public extension code must not import private runtime internals

---

## What This Plan Explicitly Does Not Do Yet

- it does not implement the public plugin platform
- it does not extract internal modules in tranche 1
- it does not require a bus-first runtime rewrite
- it does not collapse private internal modules and future public extensions into one contract

---

## Success Criteria For The Migration

The migration is succeeding when:
- new product work no longer requires repeated edits to core wiring
- feature ownership is moving outward into bounded internal modules
- central files stop growing as the default place for new behavior
- the runtime still ships continuously while the architecture improves

The migration is **not** judged primarily by how “pure” the platform abstraction looks.
It is judged by whether Atlas core stays small enough that development speeds up.
