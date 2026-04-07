# Atlas Internal Module Contract

**Last updated: 2026-04-07**

This document defines the private internal module model used by the Go runtime.

It is an internal architecture contract for Atlas contributors. It is **not** the public plugin API.

---

## Purpose

Atlas uses internal modules to keep the private core thin.

A module should let a contributor add or evolve a product feature without:
- expanding `cmd/atlas-runtime/main.go` into a feature orchestration file
- reintroducing large cross-feature domain aggregators
- adding informal feature-to-feature dependencies through the back door

The goal is faster development through bounded ownership.

---

## Architecture Layers

Atlas now has three distinct layers:

### 1. Private Core

Core owns shared runtime infrastructure:
- bootstrap and lifecycle
- config access
- auth/session
- shared router construction
- storage contracts and adapters
- agent runtime
- context assembly seam
- selective internal event bus
- module registry and host

Core should not absorb feature orchestration just because many features need it.

### 2. Private First-Party Internal Modules

Internal modules own Atlas product behavior behind core contracts.

Current extracted modules:
- approvals
- automations
- communications
- forge
- workflows
- skills
- engine
- usage
- api-validation

### 3. Future Public Extensions

Third-party plugins and custom skills are a separate system.

They may share concepts with internal modules later, but they do not share the same trust model or API surface.

---

## What Belongs In Core

Core is the smallest trusted platform Atlas needs to run:
- runtime bootstrap
- module registration and lifecycle
- auth/session and middleware
- shared router composition
- storage contracts
- agent execution primitives
- context assembly contract
- event bus where async decoupling is useful

Core is allowed to provide concrete implementations of those platform concerns.

Core is not where new product behavior should land by default.

---

## What Belongs In A Module

A feature should be an internal module when it owns one or more of:
- HTTP routes
- background jobs or lifecycle hooks
- async event handling
- feature-specific orchestration
- feature-specific storage access through a scoped contract

A module should have a clear owner and a bounded responsibility.

Examples:
- `communications` owns bridge management routes and lifecycle
- `approvals` owns approval resolution routes and policy behavior
- `automations` owns schedules, trigger execution, run state, delivery, and the canonical `automation.*` agent control surface
- `workflows` owns reusable process definitions, trust-bounded workflow runs, summaries, and the canonical `workflow.*` agent control surface

---

## Dependency Rules

The dependency direction is:

`core/platform -> modules`

with modules depending on core contracts, not the reverse.

Rules:
- core must not import internal product modules
- modules may depend on `internal/platform` contracts
- modules should not import each other directly unless a core-declared interface explicitly allows it
- route ownership should live in the module that owns the feature
- `cmd/atlas-runtime/main.go` should compose modules, not implement feature behavior

If a feature needs another feature, prefer:
1. a core-declared interface
2. a selective event publication/subscription

Direct imports between modules should be the exception, not the default.

---

## Module Shape

Each private module should implement the platform contract:

- `ID() string`
- `Manifest() platform.Manifest`
- `Register(host platform.Host) error`
- `Start(ctx context.Context) error`
- `Stop(ctx context.Context) error`

Normal expectations:
- `Register` mounts public/protected routes and subscribes to events
- `Start` begins background work or service lifecycles
- `Stop` shuts them down cleanly

Modules should keep `Start` and `Stop` lightweight and deterministic.

---

## Storage Rules

Modules should use scoped storage contracts exposed through `internal/platform/storage.go`.

Avoid:
- feature code reaching directly into unrelated DB helpers
- informal cross-feature table access
- using the shared DB as the module boundary

If a module needs new persistence behavior, prefer extending the platform storage contract deliberately.

---

## Event Bus Policy

Atlas uses a selective internal bus, not a bus-first architecture.

Good bus use cases:
- approval continuation signals
- automation trigger/completion events
- communications inbound events
- lifecycle and health notifications

Poor bus use cases:
- simple synchronous request/response flows
- module internals that do not need decoupling

Direct calls are preferred when they are simpler and clearer.

---

## Route Ownership

If a feature is extracted, the module should be the route owner.

That means:
- no duplicate legacy domain handler for the same surface
- no parallel registration path in `internal/server/router.go`
- no dead route owner left behind “just in case”

The router should apply auth/session boundaries and then hand off to the module host.

---

## Testing Rules

Each extracted module should have:
- direct module-level route/behavior tests
- integration coverage for current runtime API shape where appropriate

The repo also includes architecture guardrails in runtime integration tests to catch regressions like:
- reintroducing removed legacy domain files
- reintroducing legacy router hooks for extracted modules

Those checks only help if they run in the default verification path.

---

## Current Non-Goals

This contract does not define:
- the public plugin API
- third-party permissions/sandboxing
- UI extension contracts
- a universal event-driven runtime

Those are future layers and should not be conflated with the internal module system.
