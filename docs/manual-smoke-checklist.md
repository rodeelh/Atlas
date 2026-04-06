# Atlas Manual Smoke Checklist

**Last updated: 2026-04-06**

Use this checklist after structural changes that affect:
- routing
- core services
- Agent behavior
- module registration
- runtime/web integration

This is the manual complement to [`docs/migration-verification.md`](migration-verification.md).

---

## Before You Start

Recommended prep:

```bash
make build
make test
make check
make install
```

Then open Atlas in the browser against the installed runtime.

---

## Runtime Smoke

Verify:
- the runtime starts successfully
- the web UI loads without a blank screen
- the sidebar renders and the primary screens open
- `/status` reflects a running runtime

Good signs:
- no startup crash
- no missing-route errors in obvious first navigation

---

## Agent Smoke

This is the most important section.

Verify:
- send a basic message from the web UI
- confirm streaming tokens appear
- confirm the final response completes cleanly
- confirm the conversation persists and reloads correctly

This should be treated as the baseline health check for the Atlas Agent subsystem.

If future Teams work is in progress, remember the hard rule from [`docs/agent-boundary.md`](agent-boundary.md):

**Agent owns delegation decisions. Teams owns delegated execution.**

That rule must not be violated during smoke-driven debugging or quick fixes.

---

## Approval Smoke

Verify:
- trigger one approval-required action
- confirm the approval appears
- approve it
- confirm the Agent resumes and completes the turn

Optional:
- deny an approval and confirm the turn resolves cleanly

---

## Module Smoke

Verify at least one route or interaction from each critical extracted area:
- communications
- automations
- workflows
- forge
- skills
- engine
- usage

You do not need exhaustive manual testing every time.
You do need evidence that module mounting and runtime integration still work in the live app.

---

## Control Smoke

Verify:
- config can be read
- one small config update succeeds
- usage screen or usage route loads
- engine status route loads
- location/preferences screen or route loads

This confirms the decomposed `internal/control` package still behaves correctly through `ControlDomain`.

---

## Regression Checks

Verify:
- removed dashboard surfaces do not appear in the UI
- extracted routes are still served from modules, not legacy domain ownership
- no obvious legacy/dead navigation paths remain visible

---

## Record Outcome

After the smoke pass, record:
- date
- commit SHA or branch
- commands run
- flows checked
- failures found
- whether the change is safe to continue building on

This does not need a formal system yet, but it should be captured in the thread, PR, or release notes for the change.
