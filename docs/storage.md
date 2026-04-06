# Atlas Storage Design

This document defines the Atlas storage architecture for the current Go runtime and its cross-platform direction.

It is intentionally practical. The goal is to keep storage portable, secure, and easy to evolve as Atlas expands beyond macOS.

## Goals

- Make runtime configuration portable across macOS, Windows, and Linux.
- Keep secrets in OS-backed secure storage rather than app-managed files.
- Preserve a clean separation between human-authored guidance and machine-owned state.
- Let the runtime own the storage contracts.
- Support incremental migration from the current Swift storage layer.

## Storage Rules

- Use Markdown for human-authored guidance and intent only.
- Use structured storage for real runtime configuration.
- Keep secrets out of Markdown entirely.
- Do not use platform preference systems like `UserDefaults` as the long-term source of truth for portable runtime config.

## Canonical Split

Atlas should use four storage classes:

1. Human guidance
   - Example: `MIND.md`
   - Purpose: user-authored instructions, intent, goals, notes
   - Format: Markdown

2. Runtime config
   - Example: `config.json`
   - Purpose: machine-validated configuration that controls runtime behavior
   - Format: TOML

3. Operational state
   - Example: SQLite databases
   - Purpose: logs, sessions, approvals, memory, workflow state, caches, and other runtime-owned records
   - Format: SQLite or other structured machine storage

4. Secrets
   - Example: provider API keys, remote access secrets, channel tokens
   - Purpose: credential material that must not be stored in normal config or docs
   - Format: OS-native secret backend behind a `SecretStore` interface

## ConfigStore

### Decision

The canonical Atlas runtime config store should be a file-backed JSON document.

Recommended file name:

- `config.json`

Recommended ownership:

- The runtime owns the schema.
- The web UI and native companion shells read and write config through runtime APIs, not by writing the file directly.

### Why JSON

- Already matches the live runtime implementation
- Easy for the web UI and runtime to round-trip without translation layers
- Stable for typed machine-owned configuration
- Supported everywhere without extra parsing ambiguity

### What belongs in `config.json`

- runtime port
- onboarding completion state
- enabled providers and channels
- model selections
- feature flags
- local mode / remote mode toggles
- non-secret user preferences
- paths and other runtime options

### What does not belong in `config.json`

- API keys
- tokens
- session secrets
- remote access credentials
- encrypted blobs managed by the app itself

### ConfigStore requirements

- One canonical config file per Atlas runtime instance
- Schema version field in the file
- Strong typed validation on load
- Atomic writes using temp-file-then-rename semantics
- Restricted file permissions where the OS supports them
- Default values applied in code, not by tolerating malformed files
- Clear migration support for renamed or deprecated fields

### Path strategy

Use the standard app-data/config location for the host platform rather than app-only preference APIs.

Suggested targets:

- macOS: `~/Library/Application Support/ProjectAtlas/config.json`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/project-atlas/config.json`
- Windows: `%AppData%\\ProjectAtlas\\config.json`

The exact path should be provided by a `PathProvider` or equivalent platform abstraction.

## SecretStore

### Decision

Atlas secrets should live behind a portable `SecretStore` interface, with each platform using the native OS secret system underneath.

### Primary backend by platform

- macOS: Keychain
- Windows: Credential Manager or a DPAPI-backed implementation
- Linux desktop: Secret Service / libsecret
- Linux headless/server: explicit alternative backend, environment injection, or managed secret provider depending on deployment mode

### SecretStore design requirements

- The runtime depends on an interface, not a specific OS API
- Secrets are stored as individual named entries rather than one large application blob where possible
- Read, write, delete, and existence checks should be explicit
- Backends must avoid logging secret values
- Secret identifiers should be stable across runtimes and shells
- Secret access failures should produce actionable errors without exposing secret content

### Recommended interface shape

At a minimum:

- `Get(name)`
- `Set(name, value)`
- `Delete(name)`
- `Has(name)`

Optional if needed later:

- `ListNames()`
- `GetMany(names)`

### Why not store secrets in files

- weaker OS-level protection
- harder rotation and auditing
- greater accidental exposure risk through backups, logs, and support artifacts
- encourages config/secrets sprawl

If Atlas ever needs a non-native fallback for unsupported environments, it should be treated as an explicit secondary backend with strong warnings and a separate threat-model review.

## Recommended Runtime Ownership

The runtime should own the storage contracts:

- `ConfigStore`
- `SecretStore`
- `PathProvider`

The native companion app should not own the canonical storage model. It may call runtime APIs or use a shared backend adapter during migration, but it should not define storage behavior.

## Current Runtime State

### Current state

- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/config/snapshot.go`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/config/snapshot.go) defines the live `RuntimeConfigSnapshot`
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/config/store.go`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/config/store.go) implements atomic JSON-backed config persistence
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/config/paths.go`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/config/paths.go) defines the current support-dir layout
- [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/creds`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/creds) and [`/Users/ralhassan/Desktop/CODING/Project Atlas/Atlas/atlas-runtime/internal/comms/keychain.go`](/Users/ralhassan/Desktop/CODING/Project%20Atlas/Atlas/atlas-runtime/internal/comms/keychain.go) use macOS Keychain-backed storage for secrets today

### Direction

1. Keep `RuntimeConfigSnapshot` as the conceptual config schema baseline.
2. Keep JSON as the canonical runtime config format unless a later compatibility reason justifies a change.
3. Introduce a real `SecretStore` abstraction above the current keychain implementation.
4. Keep Keychain as the first concrete backend while defining the interface for Windows and Linux.
5. Gradually move secret storage away from a single bundle entry toward stable per-secret records if the migration cost is acceptable.

## Near-Term Implementation Plan

1. Define storage interfaces in a runtime-owned package boundary.
2. Keep the current JSON-backed config implementation and isolate it behind runtime-owned interfaces.
3. Add a `SecretStore` protocol and wrap the current macOS keychain behavior behind it.
4. Move all callers to the interfaces rather than direct `UserDefaults` or Keychain assumptions.
5. Add migration logic only when introducing a second secret backend or a cross-platform path strategy.
6. Keep macOS as the first validated platform while ensuring path names, secret identifiers, and interfaces are cross-platform from the start.

## Memories Table

The `memories` table is the primary long-term memory store. It is queried before every agent turn via BM25 FTS5 search and updated non-blocking after each turn.

```sql
CREATE TABLE memories (
  memory_id              TEXT PRIMARY KEY,
  category               TEXT NOT NULL,          -- commitment|profile|preference|project|workflow|episodic|tool_learning
  title                  TEXT NOT NULL,
  content                TEXT NOT NULL,
  source                 TEXT,                   -- user_explicit|conversation_inference|assistant_observation|dream
  confidence             REAL NOT NULL DEFAULT 0.8,
  importance             REAL NOT NULL DEFAULT 0.5,
  created_at             TEXT NOT NULL,          -- ISO8601
  updated_at             TEXT NOT NULL,          -- ISO8601
  is_user_confirmed      INTEGER NOT NULL DEFAULT 0,
  is_sensitive           INTEGER NOT NULL DEFAULT 0,
  tags_json              TEXT NOT NULL DEFAULT '[]',
  related_conversation_id TEXT,
  last_retrieved_at      TEXT,                   -- ISO8601, updated on each recall
  retrieval_count        INTEGER NOT NULL DEFAULT 0,
  valid_until            TEXT                    -- ISO8601; NULL = active, past = invalidated (contradicted)
);

-- FTS5 virtual table kept in sync via INSERT/UPDATE/DELETE triggers
CREATE VIRTUAL TABLE memories_fts USING fts5(
  memory_id UNINDEXED,
  title, content, tags_json,
  content='memories', content_rowid='rowid'
);
```

**Key behaviors:**
- `valid_until` — set to `now` when a memory is contradicted by the dream cycle (opinion: `contradict`). Excluded from all recall queries.
- `last_retrieved_at` + `retrieval_count` — updated each time `RelevantMemories()` returns the memory. Used by the dream cycle to identify never-retrieved memories for pruning.
- `memories_fts` — BM25 full-text index on `title`, `content`, `tags_json`. Recall query: OR of all keywords extracted from the user message, filtered to active memories (`valid_until IS NULL OR valid_until > now`).
- Commitment memories (`category = 'commitment'`) receive +0.20 importance boost in `RelevantMemories()` so they always surface first in the system prompt.

## Non-Goals

- Storing secrets in Markdown
- Making the native menu bar app the owner of storage contracts
- Replacing secure OS secret systems with custom encryption by default
- Collapsing config, state, and secrets into one storage backend
