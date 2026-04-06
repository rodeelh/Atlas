# Atlas Migration Verification

**Last updated: 2026-04-06**

This document defines how Atlas should verify large structural changes such as:
- module extractions
- core service decomposition
- router ownership changes
- subsystem boundary changes
- runtime architecture migrations

The goal is not just to prove that Atlas still builds.

The goal is to prove:
- behavior is still correct
- architectural boundaries still hold
- the new structure is safe to extend

---

## Verification Layers

Structural changes must be verified in layers.

### 1. Contract Tests

These protect the runtime surface that Atlas clients depend on.

Required coverage:
- route names and response shapes
- auth boundary behavior
- SSE lifecycle behavior
- approval resolution flow
- communications ingress and management flow
- workflow and automation execution entry points

Current examples:
- `atlas-runtime/internal/integration/runtime_baseline_test.go`

Pass criteria:
- API shape stays stable unless an intentional contract change was made
- async flows finish cleanly in test, not just “start successfully”

### 2. Subsystem Tests

Each extracted module or newly decomposed core service should have direct tests.

Required coverage:
- route registration
- empty state behavior
- invalid input behavior
- persistence usage
- lifecycle behavior when applicable

Current examples:
- `atlas-runtime/internal/modules/*/module_test.go`

Pass criteria:
- subsystem behavior can be validated without requiring the full runtime

### 3. Architecture Guardrails

Structural migrations need tests that enforce the intended architecture.

Required coverage:
- removed legacy route owners stay removed
- extracted routes do not quietly reappear in legacy domains
- platform does not import product modules
- modules do not casually cross-import each other

Current examples:
- `atlas-runtime/internal/integration/architecture_guardrails_test.go`

Pass criteria:
- architecture regressions fail the test suite early

### 4. Composition Tests

The runtime must still compose and boot correctly after structural change.

Required coverage:
- startup wiring succeeds
- module registration succeeds
- lifecycle ordering is correct
- optional dependencies degrade safely
- shutdown is clean

Current examples:
- `atlas-runtime/internal/platform/registry_test.go`
- runtime integration harness boot in `runtime_baseline_test.go`

Pass criteria:
- composition root remains dependable

### 5. Golden Path Integration Flows

These are the user-facing flows that must stay green after large changes.

Required flows:
- message turn -> SSE -> persisted conversation
- approval-required action -> approve -> resume -> completion
- automation run -> persisted run record
- workflow run -> persisted workflow run state
- communications inbound -> agent reply path
- runtime status / config / usage / engine info flows

Pass criteria:
- Atlas still works as a product, not just as isolated code

### 6. Changeability Tests

Architecture is only successful if the new structure is easier to change.

Suggested checks:
- can a new module be registered without central surgery?
- can a core service be tested in isolation?
- can a new caller invoke the Agent boundary cleanly?

Pass criteria:
- the architecture measurably improves development ergonomics

### 7. Failure Injection

Structural work must also be verified under failure.

Test cases should include:
- provider resolution failure
- DB failure where practical
- invalid config inputs
- module startup failure
- approval lookup miss
- bridge handler failure
- async execution timeout or cancellation behavior

Pass criteria:
- failures remain contained and debuggable

### 8. Operational Verification

After automated coverage passes, the repo still needs operational smoke checks.

Required commands:
- `make build`
- `make test`
- `make check`

Recommended smoke checks:
- open the web UI and send a message
- verify one approval flow
- verify one extracted module route in the live runtime

Pass criteria:
- repo-level workflows are green
- a human can still drive the runtime successfully

---

## Required Exit Criteria For Structural Changes

A migration or architectural shift is not complete until:

1. Contract tests are green
2. Subsystem tests are green
3. Architecture guardrails are green
4. Composition tests are green
5. Golden path flows are green
6. Repo verification commands are green
7. Any new architectural principles are documented

If one of these is missing, the migration is still in progress.

---

## Current Verification Commands

Default repo commands:

```bash
make build
make test
make check
```

Runtime-focused commands:

```bash
cd atlas/atlas-runtime
go test ./...
go test ./internal/integration -count=1
go test ./internal/modules/... ./internal/platform ./internal/domain -count=1
```

These are the minimum verification layers for architecture-sensitive work.

---

## Migration Scorecard

When evaluating a structural migration, score each category from 0 to 10:

- Contract stability
- Subsystem isolation
- Architecture enforcement
- Composition cleanliness
- Operational confidence
- Changeability improvement
- Documentation quality

Then produce:
- category scores
- total average score
- major strengths
- major residual risks
- recommended next hardening step

This scorecard should be used for post-migration reviews so Atlas can compare migrations consistently over time.
