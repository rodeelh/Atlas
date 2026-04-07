# Swift Runtime Archive

This directory contains the nine Swift packages that formed the original Project Atlas
runtime stack. They were the active codebase through **Phase 7** of the Go migration
and were archived on **2026-03-31** when the Go runtime reached full feature parity.

## Why archived, not deleted

Git history is preserved. Every commit that touched these packages remains fully
navigable. The code is kept here for reference, regression comparison, and as the
source of truth for any behaviour that needs to be re-implemented or verified in Go.

## What replaced each package

| Swift package | Go equivalent |
| --- | --- |
| `atlas-core` (AgentLoop, AgentOrchestrator, HTTP server) | `atlas-runtime-go/internal/agent/`, `internal/chat/`, `internal/server/` |
| `atlas-app` (SwiftUI shell, AtlasRuntimeManager) | Go binary runs standalone; no shell required |
| `atlas-bridges` (Telegram, Discord, OpenAI bridge) | `atlas-runtime-go/internal/comms/`, `internal/agent/` |
| `atlas-skills` (WeatherSkill, WebResearchSkill, etc.) | `atlas-runtime-go/internal/skills/` |
| `atlas-network` (OpenAI client, Telegram Bot API) | `atlas-runtime-go/internal/agent/provider.go` |
| `atlas-memory` (SQLite MemoryStore) | `atlas-runtime-go/internal/storage/` |
| `atlas-guard` (PermissionManager, approval workflow) | `atlas-runtime-go/internal/domain/approvals.go` |
| `atlas-tools` (ToolRegistry, GuardedToolExecutor) | `atlas-runtime-go/internal/skills/registry.go` |
| `atlas-logging` (AtlasConfig, KeychainSecretStore) | `atlas-runtime-go/internal/config/`, `internal/creds/` |

## Last active commit

The last commit at which `AtlasRuntimeService` (the Swift daemon) was the active
runtime is tagged in the migration history in `MIGRATION.md` — see Phase 7 completion.

## Current implementation

See `Atlas/atlas-runtime-go/` for the active Go runtime and `Atlas/atlas-web/` for
the web UI. The full migration history is documented in `MIGRATION.md` at the repo root.
