# Stubs Reference

Remaining stub implementations in the Go runtime as of the Phase 8/9 migration. Updated 2026-03-31.

---

## Fixed (2026-03-31)

| Route / Function | File | Resolution |
|---|---|---|
| `GET /memories/{id}/tags` | `domain/chat.go` | Implemented — fetches memory from SQLite and returns parsed `tags_json` array |
| `GET /conversations/search` | `domain/chat.go` + `storage/db.go` | Implemented — `SearchConversationSummaries` searches message content via LIKE |
| `GET /logs` | `domain/control.go` + `internal/logstore/sink.go` | Implemented — 500-entry ring buffer populated by agent loop and chat service; returns entries in chronological order |

---

## Deferred — Forge Skill Tool Calls

The skill-callable forge actions in `skills/forge_skill.go` return informational redirect messages pointing users to the Forge web UI. They do not execute any AI logic themselves — the agent-loop-based forge pipeline lives in the private forge module and `forge/service.go`.

| Skill Name | Handler | Deferred To |
|---|---|---|
| `forge.plan` | `forgePlan()` | Forge web UI |
| `forge.review` | `forgeReview()` | Forge web UI |
| `forge.validate` | `forgeValidate()` | Forge web UI |

`forge.propose` is intentional — it redirects to the web UI by design.

---


---
