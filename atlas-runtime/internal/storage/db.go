// Package storage provides a SQLite database layer for the Go runtime.
// The schema matches the Swift MemoryStore so both runtimes can share the
// same database file during Phase 5 dual-run.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

// DB wraps a SQLite connection and exposes typed query methods.
type DB struct {
	conn *sql.DB
}

// Open opens the SQLite database at path and runs all schema migrations.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite is single-writer; one connection is correct.

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("storage: migrate: %w", err)
	}
	return db, nil
}

// Close closes the underlying SQLite connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying *sql.DB for callers that need direct query access
// (e.g. the dashboards module resolvers that run arbitrary read-only queries).
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// migrate creates or updates the schema to match the Swift MemoryStore schema.
// Each migration is idempotent (CREATE TABLE IF NOT EXISTS / ALTER TABLE ADD COLUMN).
func (db *DB) migrate() error {
	stmts := []string{
		// conversations — matches Swift MemoryStore conversations table
		`CREATE TABLE IF NOT EXISTS conversations (
			conversation_id  TEXT PRIMARY KEY,
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			platform_context TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_updated_at
			ON conversations(updated_at DESC)`,

		// messages — matches Swift MemoryStore messages table
		`CREATE TABLE IF NOT EXISTS messages (
			message_id      TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id),
			role            TEXT NOT NULL,
			content         TEXT NOT NULL,
			timestamp       TEXT NOT NULL,
			blocks_json     TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id
			ON messages(conversation_id)`,

		// web_sessions — matches Swift MemoryStore web_sessions table exactly.
		// created_at / expires_at / refreshed_at stored as Unix timestamp doubles
		// (REAL) to match the Swift Double column type.
		`CREATE TABLE IF NOT EXISTS web_sessions (
			session_id   TEXT PRIMARY KEY,
			created_at   REAL NOT NULL,
			refreshed_at REAL NOT NULL,
			expires_at   REAL NOT NULL,
			is_remote    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_expires_at
			ON web_sessions(expires_at)`,

		// deferred_executions — matches Swift MemoryStore deferred_executions table.
		// created_at / updated_at stored as ISO8601 TEXT (SQLite.swift Date serialization).
		`CREATE TABLE IF NOT EXISTS deferred_executions (
			deferred_id            TEXT PRIMARY KEY,
			source_type            TEXT NOT NULL,
			skill_id               TEXT,
			tool_id                TEXT,
			action_id              TEXT,
			agent_id               TEXT,
			tool_call_id           TEXT NOT NULL,
			normalized_input_json  TEXT NOT NULL,
			conversation_id        TEXT,
			originating_message_id TEXT,
			approval_id            TEXT NOT NULL,
			summary                TEXT NOT NULL DEFAULT '',
			permission_level       TEXT NOT NULL DEFAULT 'execute',
			risk_level             TEXT NOT NULL DEFAULT 'execute',
			status                 TEXT NOT NULL,
			last_error             TEXT,
			result_json            TEXT,
			created_at             TEXT NOT NULL,
			updated_at             TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_deferred_tool_call_id
			ON deferred_executions(tool_call_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_deferred_approval_id
			ON deferred_executions(approval_id)`,
		`CREATE INDEX IF NOT EXISTS idx_deferred_status
			ON deferred_executions(status)`,

		// telegram_sessions — matches Swift MemoryStore telegram_sessions table.
		// chat_id is INTEGER (Int64 in Swift), timestamps are ISO8601 TEXT.
		`CREATE TABLE IF NOT EXISTS telegram_sessions (
			chat_id                INTEGER PRIMARY KEY,
			user_id                INTEGER,
			active_conversation_id TEXT NOT NULL,
			created_at             TEXT NOT NULL,
			updated_at             TEXT NOT NULL,
			last_message_id        INTEGER
		)`,

		// communication_sessions — matches Swift MemoryStore communication_sessions table.
		// Primary key is composite (platform, channel_id, thread_id).
		`CREATE TABLE IF NOT EXISTS communication_sessions (
			platform               TEXT NOT NULL,
			channel_id             TEXT NOT NULL,
			thread_id              TEXT NOT NULL DEFAULT '',
			channel_name           TEXT,
			user_id                TEXT,
			active_conversation_id TEXT NOT NULL,
			created_at             TEXT NOT NULL,
			updated_at             TEXT NOT NULL,
			last_message_id        TEXT,
			PRIMARY KEY (platform, channel_id, thread_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_comm_sessions_platform
			ON communication_sessions(platform)`,
		`CREATE INDEX IF NOT EXISTS idx_comm_sessions_updated_at
			ON communication_sessions(updated_at DESC)`,

		// memories — matches Swift MemoryStore memories table.
		// created_at / updated_at / last_retrieved_at stored as ISO8601 TEXT
		// (SQLite.swift Expression<Date> serialization).
		`CREATE TABLE IF NOT EXISTS memories (
			memory_id               TEXT PRIMARY KEY,
			category                TEXT NOT NULL,
			title                   TEXT NOT NULL,
			content                 TEXT NOT NULL,
			source                  TEXT NOT NULL,
			confidence              REAL NOT NULL DEFAULT 0.0,
			importance              REAL NOT NULL DEFAULT 0.0,
			created_at              TEXT NOT NULL,
			updated_at              TEXT NOT NULL,
			last_retrieved_at       TEXT,
			is_user_confirmed       INTEGER NOT NULL DEFAULT 0,
			is_sensitive            INTEGER NOT NULL DEFAULT 0,
			tags_json               TEXT NOT NULL DEFAULT '[]',
			related_conversation_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_category
			ON memories(category)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_importance
			ON memories(importance DESC, updated_at DESC)`,

		// memories_fts — FTS5 full-text index for BM25 candidate selection.
		// Standalone table (not content=) so it survives schema migrations cleanly.
		// Triggers below keep it in sync with the memories table.
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			memory_id UNINDEXED,
			title,
			content,
			tags_json
		)`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(memory_id, title, content, tags_json)
				VALUES (new.memory_id, new.title, new.content, new.tags_json);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_au AFTER UPDATE ON memories BEGIN
			DELETE FROM memories_fts WHERE memory_id = old.memory_id;
			INSERT INTO memories_fts(memory_id, title, content, tags_json)
				VALUES (new.memory_id, new.title, new.content, new.tags_json);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ad AFTER DELETE ON memories BEGIN
			DELETE FROM memories_fts WHERE memory_id = old.memory_id;
		END`,

		// automations — canonical automation definitions.
		// GREMLINS.md remains an import/export compatibility surface.
		`CREATE TABLE IF NOT EXISTS automations (
			id                              TEXT PRIMARY KEY,
			name                            TEXT NOT NULL,
			emoji                           TEXT NOT NULL DEFAULT '⚡',
			prompt                          TEXT NOT NULL,
			schedule_raw                    TEXT NOT NULL,
			schedule_json                   TEXT,
			is_enabled                      INTEGER NOT NULL DEFAULT 1,
			source_type                     TEXT NOT NULL DEFAULT 'manual',
			created_at                      TEXT NOT NULL,
			updated_at                      TEXT NOT NULL,
			next_run_at                     TEXT,
			workflow_id                     TEXT,
			workflow_inputs_json            TEXT,
			communication_destination_json  TEXT,
			gremlin_description             TEXT,
			tags_json                       TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_automations_enabled
			ON automations(is_enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_automations_updated_at
			ON automations(updated_at DESC)`,

		// gremlin_runs — stores automation run history.
		// started_at / finished_at stored as Unix timestamp doubles (REAL).
		`CREATE TABLE IF NOT EXISTS gremlin_runs (
			run_id          TEXT PRIMARY KEY,
			gremlin_id      TEXT NOT NULL,
			started_at      REAL NOT NULL,
			finished_at     REAL,
			status          TEXT NOT NULL,
			output          TEXT,
			error_message   TEXT,
			conversation_id TEXT,
			workflow_run_id TEXT,
			trigger_source  TEXT NOT NULL DEFAULT '',
			execution_status TEXT NOT NULL DEFAULT '',
			delivery_status TEXT NOT NULL DEFAULT '',
			delivery_error TEXT,
			destination_json TEXT,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			artifacts_json TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gremlin_runs_gremlin_id
			ON gremlin_runs(gremlin_id)`,
		`CREATE INDEX IF NOT EXISTS idx_gremlin_runs_started_at
			ON gremlin_runs(started_at DESC)`,

		// workflows — canonical workflow definitions.
		// workflow-definitions.json remains an import compatibility surface.
		`CREATE TABLE IF NOT EXISTS workflows (
			id               TEXT PRIMARY KEY,
			name             TEXT NOT NULL,
			definition_json  TEXT NOT NULL,
			is_enabled       INTEGER NOT NULL DEFAULT 1,
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_name
			ON workflows(lower(name), id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflows_updated_at
			ON workflows(updated_at DESC)`,

		// workflow_runs — structured workflow run history.
		`CREATE TABLE IF NOT EXISTS workflow_runs (
			run_id             TEXT PRIMARY KEY,
			workflow_id        TEXT NOT NULL,
			workflow_name      TEXT NOT NULL DEFAULT '',
			status             TEXT NOT NULL,
			outcome            TEXT,
			input_values_json  TEXT NOT NULL DEFAULT '{}',
			step_runs_json     TEXT NOT NULL DEFAULT '[]',
			approval_json      TEXT,
			assistant_summary  TEXT,
			error_message      TEXT,
			started_at         TEXT NOT NULL,
			finished_at        TEXT,
			conversation_id    TEXT,
			trigger_source     TEXT NOT NULL DEFAULT '',
			duration_ms        INTEGER NOT NULL DEFAULT 0,
			artifacts_json     TEXT,
			record_json        TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_workflow_id
			ON workflow_runs(workflow_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_started_at
			ON workflow_runs(started_at DESC)`,

		// agent_definitions — parsed AGENTS.md team member definitions.
		`CREATE TABLE IF NOT EXISTS agent_definitions (
			id                        TEXT PRIMARY KEY,
			name                      TEXT NOT NULL,
			role                      TEXT NOT NULL,
			mission                   TEXT NOT NULL,
			style                     TEXT NOT NULL DEFAULT '',
			allowed_skills_json       TEXT NOT NULL DEFAULT '[]',
			allowed_tool_classes_json TEXT NOT NULL DEFAULT '[]',
			autonomy                  TEXT NOT NULL,
			activation                TEXT NOT NULL DEFAULT '',
			is_enabled                INTEGER NOT NULL DEFAULT 1,
			created_at                TEXT NOT NULL,
			updated_at                TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_definitions_name
			ON agent_definitions(lower(name), id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_definitions_enabled
			ON agent_definitions(is_enabled)`,

		// agent_runtime — live operational state for each configured team member.
		`CREATE TABLE IF NOT EXISTS agent_runtime (
			agent_id         TEXT PRIMARY KEY REFERENCES agent_definitions(id) ON DELETE CASCADE,
			status           TEXT NOT NULL,
			current_task_id  TEXT,
			last_active_at   TEXT,
			last_error       TEXT,
			updated_at       TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_status
			ON agent_runtime(status)`,

		// agent_tasks — delegated sub-agent task records.
		`CREATE TABLE IF NOT EXISTS agent_tasks (
			task_id            TEXT PRIMARY KEY,
			agent_id           TEXT NOT NULL,
			status             TEXT NOT NULL,
			goal               TEXT NOT NULL,
			requested_by       TEXT NOT NULL DEFAULT 'atlas',
			result_summary     TEXT,
			error_message      TEXT,
			conversation_id    TEXT,
			started_at         TEXT NOT NULL,
			finished_at        TEXT,
			created_at         TEXT NOT NULL,
			updated_at         TEXT NOT NULL,
			iterations_used    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_agent_id
			ON agent_tasks(agent_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_status
			ON agent_tasks(status, started_at DESC)`,

		// agent_task_steps — sub-agent execution log for each delegated task.
		`CREATE TABLE IF NOT EXISTS agent_task_steps (
			step_id           TEXT PRIMARY KEY,
			task_id           TEXT NOT NULL REFERENCES agent_tasks(task_id) ON DELETE CASCADE,
			sequence_number   INTEGER NOT NULL,
			role              TEXT NOT NULL,
			step_type         TEXT NOT NULL,
			content           TEXT NOT NULL,
			tool_name         TEXT,
			tool_call_id      TEXT,
			created_at        TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_task_steps_task_id
			ON agent_task_steps(task_id, sequence_number ASC)`,

		// agent_events — agent activity feed and lifecycle log.
		`CREATE TABLE IF NOT EXISTS agent_events (
			event_id       TEXT PRIMARY KEY,
			event_type     TEXT NOT NULL,
			agent_id       TEXT,
			task_id        TEXT,
			title          TEXT NOT NULL,
			detail         TEXT,
			payload_json   TEXT NOT NULL DEFAULT '{}',
			created_at     TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_events_created_at
			ON agent_events(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_events_agent_task
			ON agent_events(agent_id, task_id, created_at DESC)`,

		// browser_sessions — persists login cookies across Atlas restarts.
		// cookies_json holds a JSON array of simplified cookie records.
		// Sessions expire after 7 days of non-use.
		`CREATE TABLE IF NOT EXISTS browser_sessions (
			host         TEXT PRIMARY KEY,
			cookies_json TEXT NOT NULL DEFAULT '[]',
			last_used_at TEXT NOT NULL,
			created_at   TEXT NOT NULL
		)`,

		// token_usage — one row per agent turn; costs pre-computed at write time.
		`CREATE TABLE IF NOT EXISTS token_usage (
			id               TEXT PRIMARY KEY,
			conversation_id  TEXT NOT NULL,
			provider         TEXT NOT NULL,
			model            TEXT NOT NULL,
			input_tokens     INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens    INTEGER NOT NULL DEFAULT 0,
			input_cost_usd   REAL NOT NULL DEFAULT 0.0,
			output_cost_usd  REAL NOT NULL DEFAULT 0.0,
			total_cost_usd   REAL NOT NULL DEFAULT 0.0,
			recorded_at      TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_recorded_at
			ON token_usage(recorded_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_conversation_id
			ON token_usage(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_provider_model
			ON token_usage(provider, model)`,

		// image_usage — one row per image generation call.
		`CREATE TABLE IF NOT EXISTS image_usage (
			id           TEXT PRIMARY KEY,
			provider     TEXT NOT NULL,
			model        TEXT NOT NULL,
			quality      TEXT NOT NULL,
			image_count  INTEGER NOT NULL DEFAULT 1,
			cost_usd     REAL NOT NULL DEFAULT 0.0,
			recorded_at  TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_image_usage_recorded_at
			ON image_usage(recorded_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_image_usage_provider_model
			ON image_usage(provider, model)`,

		// mind_telemetry — event log for the mind-thoughts subsystem.
		// Every interesting event (nap start/complete/fail, thought
		// add/update/reinforce/discard/merge, surfacing, engagement,
		// auto-execute, approval proposal, greeting delivery, sidebar
		// interaction) is one row. Payload is a JSON blob with
		// kind-specific fields. Indexed by (kind, ts) and by thought_id
		// so we can reconstruct the full lifecycle of a thought from
		// the telemetry table even after Atlas has forgotten it.
		//
		// "Atlas's thoughts are fleeting" — the system itself has no
		// access to its own graveyard. But designers need the graveyard
		// for tuning, so the telemetry table preserves the full history.
		// Two separate views of history, intentional.
		`CREATE TABLE IF NOT EXISTS mind_telemetry (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			ts         TEXT    NOT NULL,
			kind       TEXT    NOT NULL,
			thought_id TEXT,
			conv_id    TEXT,
			payload    TEXT    NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mind_telemetry_kind_ts
			ON mind_telemetry(kind, ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mind_telemetry_ts
			ON mind_telemetry(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mind_telemetry_thought
			ON mind_telemetry(thought_id) WHERE thought_id IS NOT NULL`,

		// memory_entities — named entities extracted from memories.
		// The unique index on (name, entity_type) enables upsert-by-identity:
		// INSERT OR IGNORE + UPDATE last_seen keeps one canonical node per entity.
		`CREATE TABLE IF NOT EXISTS memory_entities (
			entity_id     TEXT PRIMARY KEY,
			name          TEXT NOT NULL,
			entity_type   TEXT NOT NULL,
			first_seen    TEXT NOT NULL,
			last_seen     TEXT NOT NULL,
			embedding     BLOB,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		)`,
		// No UNIQUE INDEX on (name, entity_type) — uniqueness is enforced at the
		// application level by UpsertEntity's SELECT-before-INSERT logic.
		// A database-level unique constraint would prevent DeduplicateEntities
		// from ever finding rows to clean up (making it untestable and the safety
		// net useless). UpsertEntity is the sole write path for normal operation.
		`CREATE INDEX IF NOT EXISTS idx_entities_name_type
			ON memory_entities(name, entity_type)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_last_seen
			ON memory_entities(last_seen DESC)`,

		// memory_edges — typed, time-bounded relationships between entities.
		// valid_until IS NULL means the relationship is currently true.
		// When a relationship is superseded (e.g. user moves city), valid_until
		// is stamped and a new edge is inserted — preserving history.
		`CREATE TABLE IF NOT EXISTS memory_edges (
			edge_id          TEXT PRIMARY KEY,
			source_entity    TEXT NOT NULL REFERENCES memory_entities(entity_id),
			target_entity    TEXT NOT NULL REFERENCES memory_entities(entity_id),
			relation         TEXT NOT NULL,
			valid_from       TEXT NOT NULL,
			valid_until      TEXT,
			confidence       REAL NOT NULL DEFAULT 1.0,
			source_memory_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source_valid
			ON memory_edges(source_entity, valid_until)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target
			ON memory_edges(target_entity)`,
	}

	// Idempotent migrations for memories columns added after initial creation.
	// valid_until: ISO8601 timestamp after which a contradicted memory is excluded
	// from retrieval but preserved for history. NULL = still valid.
	// embedding/embedding_model/embedding_at: vector stored as JSON float32 array.
	alterMemories := []string{
		`ALTER TABLE memories ADD COLUMN valid_until TEXT`,
		// Backfill FTS5 index for memories that existed before the FTS5 table was added.
		`INSERT OR IGNORE INTO memories_fts(memory_id, title, content, tags_json)
		    SELECT memory_id, title, content, tags_json FROM memories`,
		`ALTER TABLE memories ADD COLUMN embedding BLOB`,
		`ALTER TABLE memories ADD COLUMN embedding_model TEXT`,
		`ALTER TABLE memories ADD COLUMN embedding_at TEXT`,
	}

	// Idempotent migrations for rows added to deferred_executions after its initial creation.
	// SQLite returns an error when a column already exists; swallow those errors.
	alterDeferred := []string{
		`ALTER TABLE deferred_executions ADD COLUMN skill_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN tool_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN action_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN agent_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN conversation_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN originating_message_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN summary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE deferred_executions ADD COLUMN permission_level TEXT NOT NULL DEFAULT 'execute'`,
		`ALTER TABLE deferred_executions ADD COLUMN risk_level TEXT NOT NULL DEFAULT 'execute'`,
		`ALTER TABLE deferred_executions ADD COLUMN last_error TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN result_json TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN preview_diff TEXT`,
	}

	// Idempotent migrations for conversations columns added after initial creation.
	alterConversations := []string{
		`ALTER TABLE conversations ADD COLUMN platform TEXT NOT NULL DEFAULT 'web'`,
		`ALTER TABLE conversations ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
	}

	// Idempotent migrations for messages columns added after initial creation.
	alterMessages := []string{
		`ALTER TABLE messages ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN blocks_json TEXT`,
	}

	// Idempotent migrations for browser_sessions columns added after initial creation.
	alterBrowserSessions := []string{
		`ALTER TABLE browser_sessions ADD COLUMN session_name TEXT NOT NULL DEFAULT ''`,
	}
	alterGremlinRuns := []string{
		`ALTER TABLE gremlin_runs ADD COLUMN trigger_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE gremlin_runs ADD COLUMN execution_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE gremlin_runs ADD COLUMN delivery_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE gremlin_runs ADD COLUMN delivery_error TEXT`,
		`ALTER TABLE gremlin_runs ADD COLUMN destination_json TEXT`,
		`ALTER TABLE gremlin_runs ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE gremlin_runs ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE gremlin_runs ADD COLUMN artifacts_json TEXT`,
	}
	alterAutomations := []string{
		`ALTER TABLE automations ADD COLUMN schedule_json TEXT`,
		`ALTER TABLE automations ADD COLUMN next_run_at TEXT`,
		`ALTER TABLE automations ADD COLUMN workflow_id TEXT`,
		`ALTER TABLE automations ADD COLUMN workflow_inputs_json TEXT`,
		`ALTER TABLE automations ADD COLUMN communication_destination_json TEXT`,
		`ALTER TABLE automations ADD COLUMN gremlin_description TEXT`,
		`ALTER TABLE automations ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]'`,
	}
	alterWorkflowRuns := []string{
		`ALTER TABLE workflow_runs ADD COLUMN workflow_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE workflow_runs ADD COLUMN outcome TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN input_values_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE workflow_runs ADD COLUMN step_runs_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE workflow_runs ADD COLUMN approval_json TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN assistant_summary TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN error_message TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN finished_at TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN conversation_id TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN trigger_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE workflow_runs ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE workflow_runs ADD COLUMN artifacts_json TEXT`,
		`ALTER TABLE workflow_runs ADD COLUMN record_json TEXT NOT NULL DEFAULT '{}'`,
	}
	alterTokenUsage := []string{
		`ALTER TABLE token_usage ADD COLUMN cached_input_tokens INTEGER NOT NULL DEFAULT 0`,
	}

	for _, stmt := range stmts {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed (%s...): %w", stmt[:min(40, len(stmt))], err)
		}
	}
	// Swallow errors — column already exists is expected on re-open.
	for _, stmt := range alterMemories {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterDeferred {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterConversations {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterMessages {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterBrowserSessions {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterGremlinRuns {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterAutomations {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterWorkflowRuns {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	for _, stmt := range alterTokenUsage {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	alterAgentDefinitions := []string{
		`ALTER TABLE agent_definitions ADD COLUMN provider_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_definitions ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alterAgentDefinitions {
		db.conn.Exec(stmt) //nolint:errcheck
	}
	// agent_metrics table — created idempotently so it can be added to existing DBs.
	db.conn.Exec(`CREATE TABLE IF NOT EXISTS agent_metrics (
		agent_id          TEXT PRIMARY KEY REFERENCES agent_definitions(id) ON DELETE CASCADE,
		tasks_completed   INTEGER NOT NULL DEFAULT 0,
		tasks_failed      INTEGER NOT NULL DEFAULT 0,
		total_tool_calls  INTEGER NOT NULL DEFAULT 0,
		last_active_at    TEXT,
		updated_at        TEXT NOT NULL
	)`) //nolint:errcheck
	// trigger_events — bounded autonomy trigger audit log.
	db.conn.Exec(`CREATE TABLE IF NOT EXISTS trigger_events (
		trigger_id    TEXT PRIMARY KEY,
		trigger_type  TEXT NOT NULL,
		agent_id      TEXT,
		instruction   TEXT NOT NULL DEFAULT '',
		status        TEXT NOT NULL DEFAULT 'pending',
		fired_at      TEXT,
		created_at    TEXT NOT NULL
	)`) //nolint:errcheck
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_trigger_events_created_at
		ON trigger_events(created_at DESC)`) //nolint:errcheck
	// trigger_cooldowns — prevents same (trigger_type, agent_id) firing more than once per window.
	db.conn.Exec(`CREATE TABLE IF NOT EXISTS trigger_cooldowns (
		cooldown_id  TEXT PRIMARY KEY,
		trigger_type TEXT NOT NULL,
		agent_id     TEXT NOT NULL,
		fired_at     TEXT NOT NULL
	)`) //nolint:errcheck
	db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_trigger_cooldowns_lookup
		ON trigger_cooldowns(trigger_type, agent_id, fired_at DESC)`) //nolint:errcheck
	// Rename legacy team_* tables to agent_* tables if the old names still exist.
	for _, rename := range []struct{ old, new string }{
		{"team_tasks", "agent_tasks"},
		{"team_task_steps", "agent_task_steps"},
		{"team_events", "agent_events"},
	} {
		_, _ = db.conn.Exec(`ALTER TABLE ` + rename.old + ` RENAME TO ` + rename.new)
	}
	// Add iterations_used column to agent_tasks if upgrading from an older schema.
	_, _ = db.conn.Exec(`ALTER TABLE agent_tasks ADD COLUMN iterations_used INTEGER NOT NULL DEFAULT 0`)

	// ── Teams V1 Phase 1: additive schema columns ─────────────────────────────
	// All ALTER TABLE statements below are idempotent — SQLite returns an error
	// when a column already exists; we swallow those errors here.

	// agent_definitions: add template_role and persona_style for V1 template contracts.
	for _, stmt := range []string{
		`ALTER TABLE agent_definitions ADD COLUMN template_role TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_definitions ADD COLUMN persona_style TEXT NOT NULL DEFAULT ''`,
	} {
		db.conn.Exec(stmt) //nolint:errcheck
	}

	// agent_tasks: add structured task payload columns required by DelegationTaskSpec.
	for _, stmt := range []string{
		`ALTER TABLE agent_tasks ADD COLUMN title                TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_tasks ADD COLUMN objective            TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_tasks ADD COLUMN scope_json           TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_tasks ADD COLUMN success_criteria_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_tasks ADD COLUMN input_context_json   TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_tasks ADD COLUMN expected_output_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_tasks ADD COLUMN mode                 TEXT NOT NULL DEFAULT 'sync_assist'`,
		`ALTER TABLE agent_tasks ADD COLUMN pattern              TEXT NOT NULL DEFAULT 'single'`,
		`ALTER TABLE agent_tasks ADD COLUMN depends_on_json      TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_tasks ADD COLUMN parent_turn_id       TEXT`,
		`ALTER TABLE agent_tasks ADD COLUMN blocking_kind        TEXT`,
		`ALTER TABLE agent_tasks ADD COLUMN blocking_detail      TEXT`,
		`ALTER TABLE agent_tasks ADD COLUMN resume_token         TEXT`,
	} {
		db.conn.Exec(stmt) //nolint:errcheck
	}

	// delegation_task_results: new table for structured worker output.
	// Keyed by task_id; one row per completed (or partially completed) task.
	db.conn.Exec(`CREATE TABLE IF NOT EXISTS delegation_task_results (
		task_id                   TEXT PRIMARY KEY,
		output_type               TEXT NOT NULL DEFAULT 'summary',
		summary                   TEXT NOT NULL DEFAULT '',
		output_json               TEXT NOT NULL DEFAULT '{}',
		artifacts_json            TEXT NOT NULL DEFAULT '[]',
		risks_json                TEXT NOT NULL DEFAULT '[]',
		blockers_json             TEXT NOT NULL DEFAULT '[]',
		recommended_next_action   TEXT,
		created_at                TEXT NOT NULL,
		updated_at                TEXT NOT NULL
	)`) //nolint:errcheck

	// local_auth_credentials — stores WebAuthn public keys and bcrypt PIN hashes
	// for local machine authentication. Idempotent.
	db.conn.Exec(`CREATE TABLE IF NOT EXISTS local_auth_credentials (
		id           TEXT PRIMARY KEY,
		type         TEXT NOT NULL,
		name         TEXT NOT NULL,
		credential   TEXT,
		pin_hash     TEXT,
		created_at   TEXT NOT NULL,
		last_used_at TEXT
	)`) //nolint:errcheck

	return nil
}

// ── Web sessions ─────────────────────────────────────────────────────────────

// SessionRecord is the raw DB row for a web session.
type SessionRecord struct {
	ID          string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RefreshedAt time.Time
	IsRemote    bool
}

// SaveWebSession inserts or replaces a session record.
func (db *DB) SaveWebSession(id string, createdAt, expiresAt time.Time, isRemote bool) error {
	now := time.Now()
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO web_sessions(session_id, created_at, refreshed_at, expires_at, is_remote)
		 VALUES (?, ?, ?, ?, ?)`,
		id,
		createdAt.Unix(),
		now.Unix(),
		expiresAt.Unix(),
		boolToInt(isRemote),
	)
	return err
}

// FetchWebSession returns the session record for id, or nil if not found / expired.
func (db *DB) FetchWebSession(id string) (*SessionRecord, error) {
	row := db.conn.QueryRow(
		`SELECT session_id, created_at, refreshed_at, expires_at, is_remote
		 FROM web_sessions WHERE session_id = ?`, id)

	var rec SessionRecord
	var createdTS, refreshedTS, expiresTS float64
	var isRemoteInt int
	if err := row.Scan(&rec.ID, &createdTS, &refreshedTS, &expiresTS, &isRemoteInt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.CreatedAt = time.Unix(int64(createdTS), 0)
	rec.RefreshedAt = time.Unix(int64(refreshedTS), 0)
	rec.ExpiresAt = time.Unix(int64(expiresTS), 0)
	rec.IsRemote = isRemoteInt != 0
	return &rec, nil
}

// RefreshWebSession slides the refreshed_at timestamp forward for a session.
func (db *DB) RefreshWebSession(id string) error {
	_, err := db.conn.Exec(
		`UPDATE web_sessions SET refreshed_at = ? WHERE session_id = ?`,
		time.Now().Unix(), id,
	)
	return err
}

// DeleteWebSession removes a single session record.
func (db *DB) DeleteWebSession(id string) error {
	_, err := db.conn.Exec(`DELETE FROM web_sessions WHERE session_id = ?`, id)
	return err
}

// DeleteAllRemoteWebSessions removes all remote sessions (e.g. after API key rotation).
func (db *DB) DeleteAllRemoteWebSessions() error {
	_, err := db.conn.Exec(`DELETE FROM web_sessions WHERE is_remote = 1`)
	return err
}

// DeleteAllConversations removes all conversations and their messages from the database.
func (db *DB) DeleteAllConversations() error {
	_, err := db.conn.Exec(`DELETE FROM messages`)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(`DELETE FROM conversations`)
	return err
}

// ── Conversations ─────────────────────────────────────────────────────────────

// ConversationRow is a lightweight conversation record (no messages).
type ConversationRow struct {
	ID              string
	CreatedAt       string
	UpdatedAt       string
	Platform        string
	PlatformContext *string
}

// ConversationSummaryRow extends ConversationRow with summary fields for the
// web UI list view, matching the contracts.ts ConversationSummary interface.
type ConversationSummaryRow struct {
	ID                   string
	CreatedAt            string
	UpdatedAt            string
	Platform             string
	PlatformContext      *string
	MessageCount         int
	FirstUserMessage     *string
	LastAssistantMessage *string
}

// ListConversationSummaries returns recent conversations with message counts and
// excerpt fields, ordered by updated_at DESC. This is the richer version used
// by the web UI list view (contracts.ts ConversationSummary).
func (db *DB) ListConversationSummaries(limit int) ([]ConversationSummaryRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.Query(`
		SELECT
			c.conversation_id,
			c.created_at,
			c.updated_at,
			c.platform,
			c.platform_context,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.conversation_id) AS message_count,
			(SELECT m2.content FROM messages m2
			 WHERE m2.conversation_id = c.conversation_id AND m2.role = 'user'
			 ORDER BY m2.timestamp ASC LIMIT 1) AS first_user_message,
			(SELECT m3.content FROM messages m3
			 WHERE m3.conversation_id = c.conversation_id AND m3.role = 'assistant'
			 ORDER BY m3.timestamp DESC LIMIT 1) AS last_assistant_message
		FROM conversations c
		ORDER BY c.updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationSummaryRow
	for rows.Next() {
		var r ConversationSummaryRow
		if err := rows.Scan(
			&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.Platform, &r.PlatformContext,
			&r.MessageCount, &r.FirstUserMessage, &r.LastAssistantMessage,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchConversationSummaries returns conversations whose messages contain query,
// ordered by updated_at DESC. Uses the same summary shape as ListConversationSummaries.
func (db *DB) SearchConversationSummaries(query string, limit int) ([]ConversationSummaryRow, error) {
	if limit <= 0 {
		limit = 20
	}
	like := "%" + query + "%"
	rows, err := db.conn.Query(`
		SELECT
			c.conversation_id,
			c.created_at,
			c.updated_at,
			c.platform,
			c.platform_context,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.conversation_id) AS message_count,
			(SELECT m2.content FROM messages m2
			 WHERE m2.conversation_id = c.conversation_id AND m2.role = 'user'
			 ORDER BY m2.timestamp ASC LIMIT 1) AS first_user_message,
			(SELECT m3.content FROM messages m3
			 WHERE m3.conversation_id = c.conversation_id AND m3.role = 'assistant'
			 ORDER BY m3.timestamp DESC LIMIT 1) AS last_assistant_message
		FROM conversations c
		WHERE EXISTS (
			SELECT 1 FROM messages mx
			WHERE mx.conversation_id = c.conversation_id
			AND LOWER(mx.content) LIKE LOWER(?)
		)
		ORDER BY c.updated_at DESC
		LIMIT ?`, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationSummaryRow
	for rows.Next() {
		var r ConversationSummaryRow
		if err := rows.Scan(
			&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.Platform, &r.PlatformContext,
			&r.MessageCount, &r.FirstUserMessage, &r.LastAssistantMessage,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveConversation inserts a new conversation record. No-op if ID already exists.
func (db *DB) SaveConversation(id, createdAt, updatedAt, platform string, platformContext *string) error {
	if platform == "" {
		platform = "web"
	}
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO conversations(conversation_id, created_at, updated_at, platform, platform_context)
		 VALUES (?, ?, ?, ?, ?)`,
		id, createdAt, updatedAt, platform, platformContext,
	)
	return err
}

// TouchConversation updates updated_at for an existing conversation.
func (db *DB) TouchConversation(id, updatedAt string) error {
	_, err := db.conn.Exec(
		`UPDATE conversations SET updated_at = ? WHERE conversation_id = ?`,
		updatedAt, id,
	)
	return err
}

// ListConversations returns recent conversations ordered by updated_at DESC.
func (db *DB) ListConversations(limit int) ([]ConversationRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.conn.Query(
		`SELECT conversation_id, created_at, updated_at, platform, platform_context
		 FROM conversations ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationRow
	for rows.Next() {
		var r ConversationRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.Platform, &r.PlatformContext); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FetchConversation returns a single conversation by ID, or nil if not found.
func (db *DB) FetchConversation(id string) (*ConversationRow, error) {
	row := db.conn.QueryRow(
		`SELECT conversation_id, created_at, updated_at, platform, platform_context
		 FROM conversations WHERE conversation_id = ?`, id)
	var r ConversationRow
	if err := row.Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.Platform, &r.PlatformContext); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ── Messages ──────────────────────────────────────────────────────────────────

// MessageRow is a single message record.
type MessageRow struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	Timestamp      string
	BlocksJSON     *string
}

// SaveMessage inserts a message and updates the conversation's updated_at.
func (db *DB) SaveMessage(id, convID, role, content, timestamp string) error {
	return db.SaveMessageWithBlocks(id, convID, role, content, timestamp, nil)
}

// SaveMessageWithBlocks inserts a message with optional structured block JSON
// and updates the conversation's updated_at.
func (db *DB) SaveMessageWithBlocks(id, convID, role, content, timestamp string, blocksJSON *string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO messages(message_id, conversation_id, role, content, timestamp, blocks_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, convID, role, content, timestamp, blocksJSON,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE conversations SET updated_at = ? WHERE conversation_id = ?`,
		timestamp, convID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ListMessages returns all messages for a conversation ordered by timestamp ASC.
func (db *DB) ListMessages(convID string) ([]MessageRow, error) {
	rows, err := db.conn.Query(
		`SELECT message_id, conversation_id, role, content, timestamp, blocks_json
		 FROM messages WHERE conversation_id = ? ORDER BY timestamp ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var r MessageRow
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.Role, &r.Content, &r.Timestamp, &r.BlocksJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Deferred executions ───────────────────────────────────────────────────────

// DeferredExecRow is a raw deferred_executions row.
type DeferredExecRow struct {
	DeferredID           string
	SourceType           string
	SkillID              *string
	ToolID               *string
	ActionID             *string
	AgentID              *string
	ToolCallID           string
	NormalizedInputJSON  string
	ConversationID       *string
	OriginatingMessageID *string
	ApprovalID           string
	Summary              string
	PermissionLevel      string
	RiskLevel            string
	Status               string
	LastError            *string
	ResultJSON           *string
	CreatedAt            string
	UpdatedAt            string
	PreviewDiff          *string
}

const deferredCols = `deferred_id, source_type, skill_id, tool_id, action_id, agent_id,
	tool_call_id, normalized_input_json, conversation_id, originating_message_id,
	approval_id, summary, permission_level, risk_level, status, last_error,
	result_json, created_at, updated_at, preview_diff`

func scanDeferredRow(row interface{ Scan(...any) error }) (*DeferredExecRow, error) {
	var r DeferredExecRow
	err := row.Scan(
		&r.DeferredID, &r.SourceType, &r.SkillID, &r.ToolID, &r.ActionID, &r.AgentID,
		&r.ToolCallID, &r.NormalizedInputJSON, &r.ConversationID, &r.OriginatingMessageID,
		&r.ApprovalID, &r.Summary, &r.PermissionLevel, &r.RiskLevel, &r.Status, &r.LastError,
		&r.ResultJSON, &r.CreatedAt, &r.UpdatedAt, &r.PreviewDiff,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SaveDeferredExecution inserts a new deferred_executions row.
func (db *DB) SaveDeferredExecution(r DeferredExecRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO deferred_executions(
			deferred_id, source_type, skill_id, tool_id, action_id, agent_id,
			tool_call_id, normalized_input_json, conversation_id, originating_message_id,
			approval_id, summary, permission_level, risk_level, status, last_error,
			result_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.DeferredID, r.SourceType, r.SkillID, r.ToolID, r.ActionID, r.AgentID,
		r.ToolCallID, r.NormalizedInputJSON, r.ConversationID, r.OriginatingMessageID,
		r.ApprovalID, r.Summary, r.PermissionLevel, r.RiskLevel, r.Status, r.LastError,
		r.ResultJSON, r.CreatedAt, r.UpdatedAt,
	)
	return err
}

// FetchDeferredsByAgentTaskID returns all deferred executions for a delegated
// agent task, looking up by the task ID stored as conversation_id for agent
// loop deferrals. The source_type='agent_loop' filter makes the intent explicit.
func (db *DB) FetchDeferredsByAgentTaskID(taskID string, status string) ([]DeferredExecRow, error) {
	rows, err := db.conn.Query(
		`SELECT `+deferredCols+`
		 FROM deferred_executions
		 WHERE conversation_id = ? AND source_type = 'agent_loop' AND status = ?
		 ORDER BY created_at ASC`,
		taskID, status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeferredExecRow
	for rows.Next() {
		r, err := scanDeferredRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// FetchDeferredsByConversationID returns all deferred_executions for a conversation with the given status.
func (db *DB) FetchDeferredsByConversationID(convID, status string) ([]DeferredExecRow, error) {
	rows, err := db.conn.Query(
		`SELECT `+deferredCols+`
		 FROM deferred_executions WHERE conversation_id = ? AND status = ?
		 ORDER BY created_at DESC`, convID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DeferredExecRow
	for rows.Next() {
		r, err := scanDeferredRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountConversations returns the total number of conversations in the DB.
func (db *DB) CountConversations() int {
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n)
	return n
}

// ListPendingApprovals returns up to limit pending deferred_executions rows, oldest first.
func (db *DB) ListPendingApprovals(limit int) ([]DeferredExecRow, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(
		`SELECT `+deferredCols+`
		 FROM deferred_executions WHERE status = 'pending_approval'
		 ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeferredExecRow
	for rows.Next() {
		r, err := scanDeferredRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountPendingApprovals returns the number of deferred_executions with status='pending_approval'.
func (db *DB) CountPendingApprovals() int {
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM deferred_executions WHERE status = 'pending_approval'`).Scan(&n)
	return n
}

// ListAllApprovals returns all deferred_executions rows ordered by created_at DESC.
func (db *DB) ListAllApprovals(limit int) ([]DeferredExecRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.Query(
		`SELECT `+deferredCols+`
		 FROM deferred_executions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DeferredExecRow
	for rows.Next() {
		r, err := scanDeferredRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// FetchDeferredByToolCallID returns the deferred_executions row for a given tool_call_id.
func (db *DB) FetchDeferredByToolCallID(toolCallID string) (*DeferredExecRow, error) {
	row := db.conn.QueryRow(
		`SELECT `+deferredCols+`
		 FROM deferred_executions WHERE tool_call_id = ?`, toolCallID)
	r, err := scanDeferredRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// UpdateDeferredStatus sets the status and updated_at for a deferred_executions row
// identified by tool_call_id.
func (db *DB) UpdateDeferredStatus(toolCallID, status, updatedAt string) error {
	_, err := db.conn.Exec(
		`UPDATE deferred_executions SET status = ?, updated_at = ? WHERE tool_call_id = ?`,
		status, updatedAt, toolCallID,
	)
	return err
}

// SetDeferredLastError stores a last_error string on an existing row without
// changing status. Used by the mind-thoughts approval resolver when a
// thought-sourced skill execution fails after the user approved it.
func (db *DB) SetDeferredLastError(toolCallID, errText, updatedAt string) error {
	_, err := db.conn.Exec(
		`UPDATE deferred_executions SET last_error = ?, updated_at = ? WHERE tool_call_id = ?`,
		errText, updatedAt, toolCallID,
	)
	return err
}

// SetPreviewDiff stores a pre-computed unified diff preview for the approval UI.
// Called after SaveDeferredExecution for write/patch operations.
func (db *DB) SetPreviewDiff(toolCallID, diff string) error {
	_, err := db.conn.Exec(
		`UPDATE deferred_executions SET preview_diff = ? WHERE tool_call_id = ?`,
		diff, toolCallID,
	)
	return err
}

// ── Telegram sessions ─────────────────────────────────────────────────────────

// TelegramSessionRow is a raw telegram_sessions row.
type TelegramSessionRow struct {
	ChatID               int64
	UserID               *int64
	ActiveConversationID string
	CreatedAt            string
	UpdatedAt            string
	LastMessageID        *int64
}

// ListTelegramSessions returns all telegram_sessions rows ordered by updated_at DESC.
func (db *DB) ListTelegramSessions() ([]TelegramSessionRow, error) {
	rows, err := db.conn.Query(
		`SELECT chat_id, user_id, active_conversation_id, created_at, updated_at, last_message_id
		 FROM telegram_sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TelegramSessionRow
	for rows.Next() {
		var r TelegramSessionRow
		if err := rows.Scan(&r.ChatID, &r.UserID, &r.ActiveConversationID,
			&r.CreatedAt, &r.UpdatedAt, &r.LastMessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Communication sessions ────────────────────────────────────────────────────

// CommSessionRow is a raw communication_sessions row.
type CommSessionRow struct {
	Platform             string
	ChannelID            string
	ThreadID             string
	ChannelName          *string
	UserID               *string
	ActiveConversationID string
	CreatedAt            string
	UpdatedAt            string
	LastMessageID        *string
}

// ListCommunicationChannels returns all communication_sessions rows ordered by updated_at DESC.
// Pass a non-empty platform string to filter by platform.
func (db *DB) ListCommunicationChannels(platform string) ([]CommSessionRow, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if platform != "" {
		rows, err = db.conn.Query(
			`SELECT platform, channel_id, thread_id, channel_name, user_id,
			        active_conversation_id, created_at, updated_at, last_message_id
			 FROM communication_sessions WHERE platform = ? ORDER BY updated_at DESC`, platform)
	} else {
		rows, err = db.conn.Query(
			`SELECT platform, channel_id, thread_id, channel_name, user_id,
			        active_conversation_id, created_at, updated_at, last_message_id
			 FROM communication_sessions ORDER BY updated_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CommSessionRow
	for rows.Next() {
		var r CommSessionRow
		if err := rows.Scan(&r.Platform, &r.ChannelID, &r.ThreadID, &r.ChannelName, &r.UserID,
			&r.ActiveConversationID, &r.CreatedAt, &r.UpdatedAt, &r.LastMessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Automation definitions ───────────────────────────────────────────────────

// AutomationRow is a canonical automation definition row.
type AutomationRow struct {
	ID                           string
	Name                         string
	Emoji                        string
	Prompt                       string
	ScheduleRaw                  string
	ScheduleJSON                 *string
	IsEnabled                    bool
	SourceType                   string
	CreatedAt                    string
	UpdatedAt                    string
	NextRunAt                    *string
	WorkflowID                   *string
	WorkflowInputsJSON           *string
	CommunicationDestinationJSON *string
	GremlinDescription           *string
	TagsJSON                     string
}

// ListAutomations returns canonical automation definitions ordered by name.
func (db *DB) ListAutomations() ([]AutomationRow, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, emoji, prompt, schedule_raw, schedule_json, is_enabled, source_type,
		        created_at, updated_at, next_run_at, workflow_id, workflow_inputs_json, communication_destination_json,
		        gremlin_description, tags_json
		 FROM automations ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AutomationRow
	for rows.Next() {
		row, err := scanAutomationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAutomation returns one canonical automation definition by ID.
func (db *DB) GetAutomation(id string) (*AutomationRow, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, emoji, prompt, schedule_raw, schedule_json, is_enabled, source_type,
		        created_at, updated_at, next_run_at, workflow_id, workflow_inputs_json, communication_destination_json,
		        gremlin_description, tags_json
		 FROM automations WHERE id = ?`, id)
	out, err := scanAutomationRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveAutomation upserts one canonical automation definition.
func (db *DB) SaveAutomation(row AutomationRow) error {
	enabled := 0
	if row.IsEnabled {
		enabled = 1
	}
	_, err := db.conn.Exec(
		`INSERT INTO automations
		 (id, name, emoji, prompt, schedule_raw, schedule_json, is_enabled, source_type,
		  created_at, updated_at, next_run_at, workflow_id, workflow_inputs_json, communication_destination_json, gremlin_description, tags_json)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name,
		  emoji=excluded.emoji,
		  prompt=excluded.prompt,
		  schedule_raw=excluded.schedule_raw,
		  schedule_json=excluded.schedule_json,
		  is_enabled=excluded.is_enabled,
		  source_type=excluded.source_type,
		  updated_at=excluded.updated_at,
		  next_run_at=excluded.next_run_at,
		  workflow_id=excluded.workflow_id,
		  workflow_inputs_json=excluded.workflow_inputs_json,
		  communication_destination_json=excluded.communication_destination_json,
		  gremlin_description=excluded.gremlin_description,
		  tags_json=excluded.tags_json`,
		row.ID, row.Name, row.Emoji, row.Prompt, row.ScheduleRaw, row.ScheduleJSON,
		enabled, row.SourceType, row.CreatedAt, row.UpdatedAt, row.NextRunAt, row.WorkflowID,
		row.WorkflowInputsJSON, row.CommunicationDestinationJSON, row.GremlinDescription, row.TagsJSON,
	)
	return err
}

// DeleteAutomation deletes one canonical automation definition.
func (db *DB) DeleteAutomation(id string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanAutomationRow(row interface{ Scan(...any) error }) (AutomationRow, error) {
	var out AutomationRow
	var enabled int
	err := row.Scan(&out.ID, &out.Name, &out.Emoji, &out.Prompt, &out.ScheduleRaw, &out.ScheduleJSON,
		&enabled, &out.SourceType, &out.CreatedAt, &out.UpdatedAt, &out.NextRunAt, &out.WorkflowID,
		&out.WorkflowInputsJSON, &out.CommunicationDestinationJSON, &out.GremlinDescription, &out.TagsJSON)
	out.IsEnabled = enabled != 0
	return out, err
}

// ── Gremlin runs ──────────────────────────────────────────────────────────────

// GremlinRunRow is a raw gremlin_runs row.
type GremlinRunRow struct {
	RunID           string
	GremlinID       string
	StartedAt       float64
	FinishedAt      *float64
	Status          string
	Output          *string
	ErrorMessage    *string
	ConversationID  *string
	WorkflowRunID   *string
	TriggerSource   string
	ExecutionStatus string
	DeliveryStatus  string
	DeliveryError   *string
	DestinationJSON *string
	DurationMs      int64
	RetryCount      int
	ArtifactsJSON   *string
}

// ListGremlinRuns returns runs for a gremlin (or all runs when gremlinID is empty),
// ordered by started_at DESC, limited to limit rows.
func (db *DB) ListGremlinRuns(gremlinID string, limit int) ([]GremlinRunRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if gremlinID != "" {
		rows, err = db.conn.Query(
			`SELECT run_id, gremlin_id, started_at, finished_at, status, output, error_message, conversation_id, workflow_run_id,
			        trigger_source, execution_status, delivery_status, delivery_error, destination_json, duration_ms, retry_count, artifacts_json
			 FROM gremlin_runs WHERE gremlin_id = ? ORDER BY started_at DESC LIMIT ?`, gremlinID, limit)
	} else {
		rows, err = db.conn.Query(
			`SELECT run_id, gremlin_id, started_at, finished_at, status, output, error_message, conversation_id, workflow_run_id,
			        trigger_source, execution_status, delivery_status, delivery_error, destination_json, duration_ms, retry_count, artifacts_json
			 FROM gremlin_runs ORDER BY started_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GremlinRunRow
	for rows.Next() {
		var r GremlinRunRow
		if err := rows.Scan(&r.RunID, &r.GremlinID, &r.StartedAt, &r.FinishedAt,
			&r.Status, &r.Output, &r.ErrorMessage, &r.ConversationID, &r.WorkflowRunID,
			&r.TriggerSource, &r.ExecutionStatus, &r.DeliveryStatus, &r.DeliveryError,
			&r.DestinationJSON, &r.DurationMs, &r.RetryCount, &r.ArtifactsJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveGremlinRun inserts a new gremlin_run row.
func (db *DB) SaveGremlinRun(r GremlinRunRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO gremlin_runs
		 (run_id, gremlin_id, started_at, finished_at, status, output, error_message, conversation_id, workflow_run_id,
		  trigger_source, execution_status, delivery_status, delivery_error, destination_json, duration_ms, retry_count, artifacts_json)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.RunID, r.GremlinID, r.StartedAt, r.FinishedAt, r.Status,
		r.Output, r.ErrorMessage, r.ConversationID, r.WorkflowRunID,
		r.TriggerSource, r.ExecutionStatus, r.DeliveryStatus, r.DeliveryError,
		r.DestinationJSON, r.DurationMs, r.RetryCount, r.ArtifactsJSON,
	)
	return err
}

// UpdateGremlinRun sets finished_at, status, and output on an existing run.
func (db *DB) UpdateGremlinRun(runID, status string, output *string, finishedAt float64) error {
	return db.CompleteGremlinRun(runID, status, output, nil, finishedAt, "", nil, 0, nil)
}

// CompleteGremlinRun stores structured run completion state.
func (db *DB) CompleteGremlinRun(runID, status string, output, errorMessage *string, finishedAt float64, deliveryStatus string, deliveryError *string, durationMs int64, artifactsJSON *string) error {
	_, err := db.conn.Exec(
		`UPDATE gremlin_runs
		 SET finished_at=?, status=?, output=?, error_message=?,
		     execution_status=?, delivery_status=?, delivery_error=?, duration_ms=?, artifacts_json=?
		 WHERE run_id=?`,
		finishedAt, status, output, errorMessage, status, deliveryStatus, deliveryError, durationMs, artifactsJSON, runID,
	)
	return err
}

// UpdateGremlinRunWorkflowRunID links an automation run to a workflow run.
func (db *DB) UpdateGremlinRunWorkflowRunID(runID, workflowRunID string) error {
	_, err := db.conn.Exec(
		`UPDATE gremlin_runs SET workflow_run_id=? WHERE run_id=?`,
		workflowRunID, runID,
	)
	return err
}

// ── Workflow definitions and runs ────────────────────────────────────────────

// WorkflowRow is a canonical workflow definition row.
type WorkflowRow struct {
	ID             string
	Name           string
	DefinitionJSON string
	IsEnabled      bool
	CreatedAt      string
	UpdatedAt      string
}

// WorkflowRunRow is a structured workflow run row.
type WorkflowRunRow struct {
	RunID            string
	WorkflowID       string
	WorkflowName     string
	Status           string
	Outcome          *string
	InputValuesJSON  string
	StepRunsJSON     string
	ApprovalJSON     *string
	AssistantSummary *string
	ErrorMessage     *string
	StartedAt        string
	FinishedAt       *string
	ConversationID   *string
	TriggerSource    string
	DurationMs       int64
	ArtifactsJSON    *string
	RecordJSON       string
}

// ListWorkflows returns canonical workflow definitions ordered by name.
func (db *DB) ListWorkflows() ([]WorkflowRow, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, definition_json, is_enabled, created_at, updated_at
		 FROM workflows ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkflowRow
	for rows.Next() {
		row, err := scanWorkflowRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetWorkflow returns one canonical workflow definition by ID.
func (db *DB) GetWorkflow(id string) (*WorkflowRow, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, definition_json, is_enabled, created_at, updated_at
		 FROM workflows WHERE id = ?`, id)
	out, err := scanWorkflowRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveWorkflow upserts one canonical workflow definition.
func (db *DB) SaveWorkflow(row WorkflowRow) error {
	enabled := 0
	if row.IsEnabled {
		enabled = 1
	}
	_, err := db.conn.Exec(
		`INSERT INTO workflows (id, name, definition_json, is_enabled, created_at, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name,
		  definition_json=excluded.definition_json,
		  is_enabled=excluded.is_enabled,
		  updated_at=excluded.updated_at`,
		row.ID, row.Name, row.DefinitionJSON, enabled, row.CreatedAt, row.UpdatedAt,
	)
	return err
}

// DeleteWorkflow deletes one canonical workflow definition.
func (db *DB) DeleteWorkflow(id string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanWorkflowRow(row interface{ Scan(...any) error }) (WorkflowRow, error) {
	var out WorkflowRow
	var enabled int
	err := row.Scan(&out.ID, &out.Name, &out.DefinitionJSON, &enabled, &out.CreatedAt, &out.UpdatedAt)
	out.IsEnabled = enabled != 0
	return out, err
}

// ListWorkflowRuns returns workflow runs ordered newest first.
func (db *DB) ListWorkflowRuns(workflowID string, limit int) ([]WorkflowRunRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if workflowID != "" {
		rows, err = db.conn.Query(
			`SELECT run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
			        approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
			        trigger_source, duration_ms, artifacts_json, record_json
			 FROM workflow_runs WHERE workflow_id = ? ORDER BY started_at DESC LIMIT ?`, workflowID, limit)
	} else {
		rows, err = db.conn.Query(
			`SELECT run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
			        approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
			        trigger_source, duration_ms, artifacts_json, record_json
			 FROM workflow_runs ORDER BY started_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkflowRunRow
	for rows.Next() {
		row, err := scanWorkflowRunRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetWorkflowRun returns one workflow run by ID.
func (db *DB) GetWorkflowRun(runID string) (*WorkflowRunRow, error) {
	row := db.conn.QueryRow(
		`SELECT run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
		        approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
		        trigger_source, duration_ms, artifacts_json, record_json
		 FROM workflow_runs WHERE run_id = ?`, runID)
	out, err := scanWorkflowRunRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveWorkflowRun inserts one workflow run row.
func (db *DB) SaveWorkflowRun(row WorkflowRunRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO workflow_runs
		 (run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
		  approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
		  trigger_source, duration_ms, artifacts_json, record_json)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(run_id) DO UPDATE SET
		  workflow_id=excluded.workflow_id,
		  workflow_name=excluded.workflow_name,
		  status=excluded.status,
		  outcome=excluded.outcome,
		  input_values_json=excluded.input_values_json,
		  step_runs_json=excluded.step_runs_json,
		  approval_json=excluded.approval_json,
		  assistant_summary=excluded.assistant_summary,
		  error_message=excluded.error_message,
		  started_at=excluded.started_at,
		  finished_at=excluded.finished_at,
		  conversation_id=excluded.conversation_id,
		  trigger_source=excluded.trigger_source,
		  duration_ms=excluded.duration_ms,
		  artifacts_json=excluded.artifacts_json,
		  record_json=excluded.record_json`,
		row.RunID, row.WorkflowID, row.WorkflowName, row.Status, row.Outcome,
		row.InputValuesJSON, row.StepRunsJSON, row.ApprovalJSON, row.AssistantSummary,
		row.ErrorMessage, row.StartedAt, row.FinishedAt, row.ConversationID,
		row.TriggerSource, row.DurationMs, row.ArtifactsJSON, row.RecordJSON,
	)
	return err
}

// CompleteWorkflowRun stores structured workflow completion state.
func (db *DB) CompleteWorkflowRun(runID, status string, outcome, assistantSummary, errorMessage, finishedAt *string, durationMs int64, artifactsJSON *string) error {
	_, err := db.conn.Exec(
		`UPDATE workflow_runs
		 SET status=?, outcome=?, assistant_summary=?, error_message=?, finished_at=?, duration_ms=?, artifacts_json=?
		 WHERE run_id=?`,
		status, outcome, assistantSummary, errorMessage, finishedAt, durationMs, artifactsJSON, runID,
	)
	return err
}

// UpdateWorkflowRunStepRuns stores the latest structured per-step run state.
func (db *DB) UpdateWorkflowRunStepRuns(runID, stepRunsJSON string) error {
	_, err := db.conn.Exec(`UPDATE workflow_runs SET step_runs_json=? WHERE run_id=?`, stepRunsJSON, runID)
	return err
}

// UpdateWorkflowRunStatus updates one workflow run status for approval routes.
func (db *DB) UpdateWorkflowRunStatus(runID, status string) (*WorkflowRunRow, error) {
	_, err := db.conn.Exec(`UPDATE workflow_runs SET status=? WHERE run_id=?`, status, runID)
	if err != nil {
		return nil, err
	}
	row := db.conn.QueryRow(
		`SELECT run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
		        approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
		        trigger_source, duration_ms, artifacts_json, record_json
		 FROM workflow_runs WHERE run_id = ?`, runID)
	out, err := scanWorkflowRunRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func scanWorkflowRunRow(row interface{ Scan(...any) error }) (WorkflowRunRow, error) {
	var out WorkflowRunRow
	err := row.Scan(&out.RunID, &out.WorkflowID, &out.WorkflowName, &out.Status, &out.Outcome,
		&out.InputValuesJSON, &out.StepRunsJSON, &out.ApprovalJSON, &out.AssistantSummary,
		&out.ErrorMessage, &out.StartedAt, &out.FinishedAt, &out.ConversationID,
		&out.TriggerSource, &out.DurationMs, &out.ArtifactsJSON, &out.RecordJSON)
	return out, err
}

// ── Team definitions and runtime ─────────────────────────────────────────────

// AgentDefinitionRow is a canonical team member definition row (agent_definitions table).
// Fields added in Teams V1 Phase 1: TemplateRole, PersonaStyle.
type AgentDefinitionRow struct {
	ID                     string
	Name                   string
	Role                   string // legacy free-text role; TemplateRole is the V1 enum
	Mission                string
	Style                  string // kept for backward compat; PersonaStyle preferred
	AllowedSkillsJSON      string
	AllowedToolClassesJSON string
	Autonomy               string
	Activation             string
	ProviderType           string
	Model                  string
	IsEnabled              bool
	CreatedAt              string
	UpdatedAt              string
	// V1 fields (added Phase 1 schema migration):
	TemplateRole string // "scout" | "builder" | "reviewer" | "operator" | "monitor" | ""
	PersonaStyle string // preferred persona style (richer than Style)
}

// ListEnabledAgentDefinitions returns only enabled team member definitions.
// Used by the DB-backed roster context (Phase 2) to build the system-prompt
// roster block without touching AGENTS.md on every turn.
func (db *DB) ListEnabledAgentDefinitions() ([]AgentDefinitionRow, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, role, mission, style, allowed_skills_json, allowed_tool_classes_json,
		        autonomy, activation, provider_type, model, is_enabled, created_at, updated_at,
		        template_role, persona_style
		 FROM agent_definitions WHERE is_enabled = 1 ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentDefinitionRow
	for rows.Next() {
		row, err := scanAgentDefinitionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// AgentRuntimeRow is the live runtime state row for one team member.
type AgentRuntimeRow struct {
	AgentID       string
	Status        string
	CurrentTaskID *string
	LastActiveAt  *string
	LastError     *string
	UpdatedAt     string
}

// AgentMetricsRow holds cumulative usage statistics for one team member.
type AgentMetricsRow struct {
	AgentID        string
	TasksCompleted int
	TasksFailed    int
	TotalToolCalls int
	LastActiveAt   *string
	UpdatedAt      string
}

// ListAgentDefinitions returns canonical team member definitions ordered by name.
func (db *DB) ListAgentDefinitions() ([]AgentDefinitionRow, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, role, mission, style, allowed_skills_json, allowed_tool_classes_json,
		        autonomy, activation, provider_type, model, is_enabled, created_at, updated_at,
		        template_role, persona_style
		 FROM agent_definitions ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentDefinitionRow
	for rows.Next() {
		row, err := scanAgentDefinitionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAgentDefinition returns one team member definition by ID.
func (db *DB) GetAgentDefinition(id string) (*AgentDefinitionRow, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, role, mission, style, allowed_skills_json, allowed_tool_classes_json,
		        autonomy, activation, provider_type, model, is_enabled, created_at, updated_at,
		        template_role, persona_style
		 FROM agent_definitions WHERE id = ?`, id)
	out, err := scanAgentDefinitionRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveAgentDefinition upserts one team member definition row.
func (db *DB) SaveAgentDefinition(row AgentDefinitionRow) error {
	enabled := 0
	if row.IsEnabled {
		enabled = 1
	}
	_, err := db.conn.Exec(
		`INSERT INTO agent_definitions
		 (id, name, role, mission, style, allowed_skills_json, allowed_tool_classes_json,
		  autonomy, activation, provider_type, model, is_enabled, created_at, updated_at,
		  template_role, persona_style)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name,
		  role=excluded.role,
		  mission=excluded.mission,
		  style=excluded.style,
		  allowed_skills_json=excluded.allowed_skills_json,
		  allowed_tool_classes_json=excluded.allowed_tool_classes_json,
		  autonomy=excluded.autonomy,
		  activation=excluded.activation,
		  provider_type=excluded.provider_type,
		  model=excluded.model,
		  is_enabled=excluded.is_enabled,
		  updated_at=excluded.updated_at,
		  template_role=excluded.template_role,
		  persona_style=excluded.persona_style`,
		row.ID, row.Name, row.Role, row.Mission, row.Style, row.AllowedSkillsJSON,
		row.AllowedToolClassesJSON, row.Autonomy, row.Activation, row.ProviderType, row.Model,
		enabled, row.CreatedAt, row.UpdatedAt, row.TemplateRole, row.PersonaStyle,
	)
	return err
}

// DeleteAgentDefinition deletes one team member definition row.
func (db *DB) DeleteAgentDefinition(id string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM agent_definitions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanAgentDefinitionRow(row interface{ Scan(...any) error }) (AgentDefinitionRow, error) {
	var out AgentDefinitionRow
	var enabled int
	err := row.Scan(
		&out.ID,
		&out.Name,
		&out.Role,
		&out.Mission,
		&out.Style,
		&out.AllowedSkillsJSON,
		&out.AllowedToolClassesJSON,
		&out.Autonomy,
		&out.Activation,
		&out.ProviderType,
		&out.Model,
		&enabled,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.TemplateRole,
		&out.PersonaStyle,
	)
	out.IsEnabled = enabled != 0
	return out, err
}

// ListAgentRuntime returns live runtime state ordered by agent id.
func (db *DB) ListAgentRuntime() ([]AgentRuntimeRow, error) {
	rows, err := db.conn.Query(
		`SELECT agent_id, status, current_task_id, last_active_at, last_error, updated_at
		 FROM agent_runtime ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentRuntimeRow
	for rows.Next() {
		row, err := scanAgentRuntimeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAgentRuntime returns one agent runtime row by agent ID.
func (db *DB) GetAgentRuntime(agentID string) (*AgentRuntimeRow, error) {
	row := db.conn.QueryRow(
		`SELECT agent_id, status, current_task_id, last_active_at, last_error, updated_at
		 FROM agent_runtime WHERE agent_id = ?`, agentID)
	out, err := scanAgentRuntimeRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveAgentRuntime upserts one live runtime state row.
func (db *DB) SaveAgentRuntime(row AgentRuntimeRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_runtime
		 (agent_id, status, current_task_id, last_active_at, last_error, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		  status=excluded.status,
		  current_task_id=excluded.current_task_id,
		  last_active_at=excluded.last_active_at,
		  last_error=excluded.last_error,
		  updated_at=excluded.updated_at`,
		row.AgentID, row.Status, row.CurrentTaskID, row.LastActiveAt, row.LastError, row.UpdatedAt,
	)
	return err
}

// DeleteAgentRuntime deletes one runtime row.
func (db *DB) DeleteAgentRuntime(agentID string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM agent_runtime WHERE agent_id = ?`, agentID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanAgentRuntimeRow(row interface{ Scan(...any) error }) (AgentRuntimeRow, error) {
	var out AgentRuntimeRow
	err := row.Scan(&out.AgentID, &out.Status, &out.CurrentTaskID, &out.LastActiveAt, &out.LastError, &out.UpdatedAt)
	return out, err
}

// GetAgentMetrics returns the metrics row for one team member, or nil if none exists.
func (db *DB) GetAgentMetrics(agentID string) (*AgentMetricsRow, error) {
	row := db.conn.QueryRow(
		`SELECT agent_id, tasks_completed, tasks_failed, total_tool_calls, last_active_at, updated_at
		 FROM agent_metrics WHERE agent_id = ?`, agentID)
	var out AgentMetricsRow
	err := row.Scan(&out.AgentID, &out.TasksCompleted, &out.TasksFailed, &out.TotalToolCalls, &out.LastActiveAt, &out.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// UpsertAgentMetrics inserts or replaces the metrics row for one team member.
func (db *DB) UpsertAgentMetrics(row AgentMetricsRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_metrics (agent_id, tasks_completed, tasks_failed, total_tool_calls, last_active_at, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		  tasks_completed=excluded.tasks_completed,
		  tasks_failed=excluded.tasks_failed,
		  total_tool_calls=excluded.total_tool_calls,
		  last_active_at=excluded.last_active_at,
		  updated_at=excluded.updated_at`,
		row.AgentID, row.TasksCompleted, row.TasksFailed, row.TotalToolCalls, row.LastActiveAt, row.UpdatedAt,
	)
	return err
}

// ListAgentMetrics returns metrics rows for all team members.
func (db *DB) ListAgentMetrics() ([]AgentMetricsRow, error) {
	rows, err := db.conn.Query(
		`SELECT agent_id, tasks_completed, tasks_failed, total_tool_calls, last_active_at, updated_at
		 FROM agent_metrics ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentMetricsRow
	for rows.Next() {
		var r AgentMetricsRow
		if err := rows.Scan(&r.AgentID, &r.TasksCompleted, &r.TasksFailed, &r.TotalToolCalls, &r.LastActiveAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AgentTaskRow is a persisted delegated task record (agent_tasks table).
// Fields added in Teams V1 Phase 1 are marked [new]; all others are [existing].
type AgentTaskRow struct {
	// [existing] core
	TaskID         string
	AgentID        string
	Status         string
	Goal           string
	RequestedBy    string
	ResultSummary  *string
	ErrorMessage   *string
	ConversationID *string
	StartedAt      string
	FinishedAt     *string
	CreatedAt      string
	UpdatedAt      string
	IterationsUsed int
	// [new] structured task payload
	Title               string
	Objective           string
	ScopeJSON           string
	SuccessCriteriaJSON string
	InputContextJSON    string
	ExpectedOutputJSON  string
	// [new] orchestration metadata
	Mode          string
	Pattern       string
	DependsOnJSON string
	ParentTurnID  *string
	// [new] blocking metadata
	BlockingKind   *string
	BlockingDetail *string
	ResumeToken    *string
}

// AgentTaskStepRow is a persisted execution-log row for a delegated task.
type AgentTaskStepRow struct {
	StepID         string
	TaskID         string
	SequenceNumber int
	Role           string
	StepType       string
	Content        string
	ToolName       *string
	ToolCallID     *string
	CreatedAt      string
}

// AgentEventRow is one team activity feed event.
type AgentEventRow struct {
	EventID     string
	EventType   string
	AgentID     *string
	TaskID      *string
	Title       string
	Detail      *string
	PayloadJSON string
	CreatedAt   string
}

// ListAgentTasks returns delegated task records ordered newest first.
func (db *DB) ListAgentTasks(limit int) ([]AgentTaskRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.Query(
		`SELECT task_id, agent_id, status, goal, requested_by, result_summary, error_message,
		        conversation_id, started_at, finished_at, created_at, updated_at, iterations_used,
		        title, objective, scope_json, success_criteria_json, input_context_json,
		        expected_output_json, mode, pattern, depends_on_json, parent_turn_id,
		        blocking_kind, blocking_detail, resume_token
		 FROM agent_tasks ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentTaskRow
	for rows.Next() {
		row, err := scanAgentTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetAgentTask returns one delegated task record by ID.
func (db *DB) GetAgentTask(taskID string) (*AgentTaskRow, error) {
	row := db.conn.QueryRow(
		`SELECT task_id, agent_id, status, goal, requested_by, result_summary, error_message,
		        conversation_id, started_at, finished_at, created_at, updated_at, iterations_used,
		        title, objective, scope_json, success_criteria_json, input_context_json,
		        expected_output_json, mode, pattern, depends_on_json, parent_turn_id,
		        blocking_kind, blocking_detail, resume_token
		 FROM agent_tasks WHERE task_id = ?`, taskID)
	out, err := scanAgentTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// SaveAgentTask upserts one delegated task record.
func (db *DB) SaveAgentTask(row AgentTaskRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_tasks
		 (task_id, agent_id, status, goal, requested_by, result_summary, error_message,
		  conversation_id, started_at, finished_at, created_at, updated_at, iterations_used,
		  title, objective, scope_json, success_criteria_json, input_context_json,
		  expected_output_json, mode, pattern, depends_on_json, parent_turn_id,
		  blocking_kind, blocking_detail, resume_token)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(task_id) DO UPDATE SET
		  agent_id=excluded.agent_id,
		  status=excluded.status,
		  goal=excluded.goal,
		  requested_by=excluded.requested_by,
		  result_summary=excluded.result_summary,
		  error_message=excluded.error_message,
		  conversation_id=excluded.conversation_id,
		  started_at=excluded.started_at,
		  finished_at=excluded.finished_at,
		  updated_at=excluded.updated_at,
		  iterations_used=excluded.iterations_used,
		  title=excluded.title,
		  objective=excluded.objective,
		  scope_json=excluded.scope_json,
		  success_criteria_json=excluded.success_criteria_json,
		  input_context_json=excluded.input_context_json,
		  expected_output_json=excluded.expected_output_json,
		  mode=excluded.mode,
		  pattern=excluded.pattern,
		  depends_on_json=excluded.depends_on_json,
		  parent_turn_id=excluded.parent_turn_id,
		  blocking_kind=excluded.blocking_kind,
		  blocking_detail=excluded.blocking_detail,
		  resume_token=excluded.resume_token`,
		row.TaskID, row.AgentID, row.Status, row.Goal, row.RequestedBy, row.ResultSummary, row.ErrorMessage,
		row.ConversationID, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.IterationsUsed,
		row.Title, row.Objective, row.ScopeJSON, row.SuccessCriteriaJSON, row.InputContextJSON,
		row.ExpectedOutputJSON, row.Mode, row.Pattern, row.DependsOnJSON, row.ParentTurnID,
		row.BlockingKind, row.BlockingDetail, row.ResumeToken,
	)
	return err
}

// AddAgentTaskIterations increments the iterations_used counter for a task.
func (db *DB) AddAgentTaskIterations(taskID string, count int) error {
	_, err := db.conn.Exec(
		`UPDATE agent_tasks SET iterations_used = iterations_used + ?, updated_at = ? WHERE task_id = ?`,
		count, time.Now().UTC().Format(time.RFC3339Nano), taskID,
	)
	return err
}

func scanAgentTaskRow(row interface{ Scan(...any) error }) (AgentTaskRow, error) {
	var out AgentTaskRow
	err := row.Scan(
		&out.TaskID,
		&out.AgentID,
		&out.Status,
		&out.Goal,
		&out.RequestedBy,
		&out.ResultSummary,
		&out.ErrorMessage,
		&out.ConversationID,
		&out.StartedAt,
		&out.FinishedAt,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.IterationsUsed,
		&out.Title,
		&out.Objective,
		&out.ScopeJSON,
		&out.SuccessCriteriaJSON,
		&out.InputContextJSON,
		&out.ExpectedOutputJSON,
		&out.Mode,
		&out.Pattern,
		&out.DependsOnJSON,
		&out.ParentTurnID,
		&out.BlockingKind,
		&out.BlockingDetail,
		&out.ResumeToken,
	)
	return out, err
}

// ListAgentTaskSteps returns one delegated task's execution log in sequence order.
func (db *DB) ListAgentTaskSteps(taskID string) ([]AgentTaskStepRow, error) {
	rows, err := db.conn.Query(
		`SELECT step_id, task_id, sequence_number, role, step_type, content, tool_name, tool_call_id, created_at
		 FROM agent_task_steps WHERE task_id = ? ORDER BY sequence_number ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentTaskStepRow
	for rows.Next() {
		row, err := scanAgentTaskStepRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SaveAgentTaskStep inserts one delegated task step row.
func (db *DB) SaveAgentTaskStep(row AgentTaskStepRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_task_steps
		 (step_id, task_id, sequence_number, role, step_type, content, tool_name, tool_call_id, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		row.StepID, row.TaskID, row.SequenceNumber, row.Role, row.StepType, row.Content,
		row.ToolName, row.ToolCallID, row.CreatedAt,
	)
	return err
}

func scanAgentTaskStepRow(row interface{ Scan(...any) error }) (AgentTaskStepRow, error) {
	var out AgentTaskStepRow
	err := row.Scan(
		&out.StepID,
		&out.TaskID,
		&out.SequenceNumber,
		&out.Role,
		&out.StepType,
		&out.Content,
		&out.ToolName,
		&out.ToolCallID,
		&out.CreatedAt,
	)
	return out, err
}

// ListAgentEvents returns recent team activity ordered newest first.
func (db *DB) ListAgentEvents(limit int) ([]AgentEventRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.Query(
		`SELECT event_id, event_type, agent_id, task_id, title, detail, payload_json, created_at
		 FROM agent_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentEventRow
	for rows.Next() {
		row, err := scanAgentEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ClearAgentTasks deletes all rows from agent_tasks and their steps.
func (db *DB) ClearAgentTasks() error {
	if _, err := db.conn.Exec(`DELETE FROM agent_task_steps`); err != nil {
		return err
	}
	_, err := db.conn.Exec(`DELETE FROM agent_tasks`)
	return err
}

// ClearBlockedAgentTasks deletes agent_tasks rows with failed or needs_review status.
func (db *DB) ClearBlockedAgentTasks() error {
	if _, err := db.conn.Exec(`DELETE FROM agent_task_steps WHERE task_id IN (SELECT task_id FROM agent_tasks WHERE status IN ('failed','error','needs_review','pending_approval'))`); err != nil {
		return err
	}
	_, err := db.conn.Exec(`DELETE FROM agent_tasks WHERE status IN ('failed','error','needs_review','pending_approval')`)
	return err
}

// ClearAgentEvents deletes all rows from agent_events.
func (db *DB) ClearAgentEvents() error {
	_, err := db.conn.Exec(`DELETE FROM agent_events`)
	return err
}

// SaveAgentEvent inserts one team activity event row.
func (db *DB) SaveAgentEvent(row AgentEventRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_events
		 (event_id, event_type, agent_id, task_id, title, detail, payload_json, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		row.EventID, row.EventType, row.AgentID, row.TaskID, row.Title, row.Detail, row.PayloadJSON, row.CreatedAt,
	)
	return err
}

func scanAgentEventRow(row interface{ Scan(...any) error }) (AgentEventRow, error) {
	var out AgentEventRow
	err := row.Scan(
		&out.EventID,
		&out.EventType,
		&out.AgentID,
		&out.TaskID,
		&out.Title,
		&out.Detail,
		&out.PayloadJSON,
		&out.CreatedAt,
	)
	return out, err
}

// ── Delegation task results (Teams V1) ───────────────────────────────────────

// DelegationTaskResultRow is the structured worker-output record for one delegated task.
// Written by the task engine on finalize; read by Team HQ and returned to Agent.
type DelegationTaskResultRow struct {
	TaskID                string
	OutputType            string
	Summary               string
	OutputJSON            string
	ArtifactsJSON         string
	RisksJSON             string
	BlockersJSON          string
	RecommendedNextAction *string
	CreatedAt             string
	UpdatedAt             string
}

// SaveDelegationTaskResult upserts a structured result for a delegated task.
func (db *DB) SaveDelegationTaskResult(row DelegationTaskResultRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO delegation_task_results
		 (task_id, output_type, summary, output_json, artifacts_json, risks_json,
		  blockers_json, recommended_next_action, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(task_id) DO UPDATE SET
		  output_type=excluded.output_type,
		  summary=excluded.summary,
		  output_json=excluded.output_json,
		  artifacts_json=excluded.artifacts_json,
		  risks_json=excluded.risks_json,
		  blockers_json=excluded.blockers_json,
		  recommended_next_action=excluded.recommended_next_action,
		  updated_at=excluded.updated_at`,
		row.TaskID, row.OutputType, row.Summary, row.OutputJSON, row.ArtifactsJSON,
		row.RisksJSON, row.BlockersJSON, row.RecommendedNextAction, row.CreatedAt, row.UpdatedAt,
	)
	return err
}

// GetDelegationTaskResult returns the structured result for one delegated task.
// Returns nil, nil when no result row exists yet (task still running).
func (db *DB) GetDelegationTaskResult(taskID string) (*DelegationTaskResultRow, error) {
	row := db.conn.QueryRow(
		`SELECT task_id, output_type, summary, output_json, artifacts_json, risks_json,
		        blockers_json, recommended_next_action, created_at, updated_at
		 FROM delegation_task_results WHERE task_id = ?`, taskID)
	var out DelegationTaskResultRow
	err := row.Scan(
		&out.TaskID, &out.OutputType, &out.Summary, &out.OutputJSON, &out.ArtifactsJSON,
		&out.RisksJSON, &out.BlockersJSON, &out.RecommendedNextAction, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDelegationTaskResults returns results for a set of task IDs.
// Useful for bulk-loading results when rendering Team HQ.
func (db *DB) ListDelegationTaskResults(taskIDs []string) ([]DelegationTaskResultRow, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(taskIDs))
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT task_id, output_type, summary, output_json, artifacts_json, risks_json,
	                 blockers_json, recommended_next_action, created_at, updated_at
	          FROM delegation_task_results
	          WHERE task_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DelegationTaskResultRow
	for rows.Next() {
		var r DelegationTaskResultRow
		if err := rows.Scan(
			&r.TaskID, &r.OutputType, &r.Summary, &r.OutputJSON, &r.ArtifactsJSON,
			&r.RisksJSON, &r.BlockersJSON, &r.RecommendedNextAction, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountTeamMembers returns the number of rows in agent_definitions.
// Used by the Phase 8 one-time AGENTS.md import guard.
func (db *DB) CountTeamMembers() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM agent_definitions`).Scan(&n)
	return n, err
}

// ── Trigger events ────────────────────────────────────────────────────────────

// TriggerEventRow is one bounded-autonomy trigger record.
type TriggerEventRow struct {
	TriggerID   string
	TriggerType string
	AgentID     *string
	Instruction string
	Status      string // "pending" | "fired" | "suppressed"
	FiredAt     *string
	CreatedAt   string
}

// TriggerCooldownRow records when a (trigger_type, agent_id) pair last fired.
type TriggerCooldownRow struct {
	CooldownID  string
	TriggerType string
	AgentID     string
	FiredAt     string
}

// SaveTriggerEvent inserts one trigger event record.
func (db *DB) SaveTriggerEvent(row TriggerEventRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO trigger_events (trigger_id, trigger_type, agent_id, instruction, status, fired_at, created_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(trigger_id) DO UPDATE SET
		  status=excluded.status, fired_at=excluded.fired_at`,
		row.TriggerID, row.TriggerType, row.AgentID, row.Instruction, row.Status, row.FiredAt, row.CreatedAt,
	)
	return err
}

// ListTriggerEvents returns recent trigger records ordered newest first.
func (db *DB) ListTriggerEvents(limit int) ([]TriggerEventRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(
		`SELECT trigger_id, trigger_type, agent_id, instruction, status, fired_at, created_at
		 FROM trigger_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TriggerEventRow
	for rows.Next() {
		var r TriggerEventRow
		if err := rows.Scan(&r.TriggerID, &r.TriggerType, &r.AgentID, &r.Instruction, &r.Status, &r.FiredAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveTriggerCooldown records a cooldown entry for (trigger_type, agent_id).
func (db *DB) SaveTriggerCooldown(row TriggerCooldownRow) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO trigger_cooldowns (cooldown_id, trigger_type, agent_id, fired_at)
		 VALUES (?,?,?,?)`,
		row.CooldownID, row.TriggerType, row.AgentID, row.FiredAt,
	)
	return err
}

// IsOnCooldown returns true if (triggerType, agentID) fired within the given window.
func (db *DB) IsOnCooldown(triggerType, agentID string, window time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM trigger_cooldowns WHERE trigger_type=? AND agent_id=? AND fired_at > ?`,
		triggerType, agentID, cutoff,
	).Scan(&count)
	return count > 0, err
}

// TryAcquireTriggerCooldown atomically checks cooldown and records a new firing in one
// SQLite operation. Returns true if the cooldown was acquired (i.e. not on cooldown before),
// false if the agent is still within the cooldown window.
// Because SQLite has a single writer, the INSERT … SELECT is atomic — no race possible.
func (db *DB) TryAcquireTriggerCooldown(cooldownID, triggerType, agentID string, window time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.conn.Exec(
		`INSERT OR IGNORE INTO trigger_cooldowns (cooldown_id, trigger_type, agent_id, fired_at)
		 SELECT ?, ?, ?, ?
		 WHERE NOT EXISTS (
		   SELECT 1 FROM trigger_cooldowns
		   WHERE trigger_type=? AND agent_id=? AND fired_at > ?
		 )`,
		cooldownID, triggerType, agentID, now,
		triggerType, agentID, cutoff,
	)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// ── Memories ──────────────────────────────────────────────────────────────────

// MemoryRow is a raw memories row.
type MemoryRow struct {
	ID                    string
	Category              string
	Title                 string
	Content               string
	Source                string
	Confidence            float64
	Importance            float64
	CreatedAt             string
	UpdatedAt             string
	LastRetrievedAt       *string
	IsUserConfirmed       bool
	IsSensitive           bool
	TagsJSON              string
	RelatedConversationID *string
	// ValidUntil, when set, marks a contradicted memory as inactive. NULL = still valid.
	// The memory is preserved for history but excluded from retrieval after this timestamp.
	ValidUntil *string
}

const memoryCols = `memory_id, category, title, content, source, confidence, importance,
	created_at, updated_at, last_retrieved_at, is_user_confirmed, is_sensitive,
	tags_json, related_conversation_id, valid_until`

func scanMemoryRow(row interface{ Scan(...any) error }) (*MemoryRow, error) {
	var r MemoryRow
	var isConfirmedInt, isSensitiveInt int
	err := row.Scan(
		&r.ID, &r.Category, &r.Title, &r.Content, &r.Source,
		&r.Confidence, &r.Importance,
		&r.CreatedAt, &r.UpdatedAt, &r.LastRetrievedAt,
		&isConfirmedInt, &isSensitiveInt,
		&r.TagsJSON, &r.RelatedConversationID, &r.ValidUntil,
	)
	if err != nil {
		return nil, err
	}
	r.IsUserConfirmed = isConfirmedInt != 0
	r.IsSensitive = isSensitiveInt != 0
	return &r, nil
}

// ListMemories returns active memories ordered by importance DESC, updated_at DESC.
// Active means valid_until IS NULL or in the future.
// Pass a non-empty category to filter. limit <= 0 defaults to 100.
func (db *DB) ListMemories(limit int, category string) ([]MemoryRow, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if category != "" {
		rows, err = db.conn.Query(
			`SELECT `+memoryCols+`
			 FROM memories
			 WHERE category = ? AND (valid_until IS NULL OR valid_until > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			 ORDER BY importance DESC, updated_at DESC LIMIT ?`, category, limit)
	} else {
		rows, err = db.conn.Query(
			`SELECT `+memoryCols+`
			 FROM memories
			 WHERE valid_until IS NULL OR valid_until > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
			 ORDER BY importance DESC, updated_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// SearchMemories performs a case-insensitive search on title and content.
func (db *DB) SearchMemories(query string, limit int) ([]MemoryRow, error) {
	if limit <= 0 {
		limit = 50
	}
	pattern := "%" + query + "%"
	rows, err := db.conn.Query(
		`SELECT `+memoryCols+`
		 FROM memories
		 WHERE title LIKE ? OR content LIKE ?
		 ORDER BY importance DESC, updated_at DESC LIMIT ?`,
		pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DeleteMemory removes a memory by its ID. No-op if not found.
func (db *DB) DeleteMemory(id string) error {
	_, err := db.conn.Exec(`DELETE FROM memories WHERE memory_id = ?`, id)
	return err
}

// SaveMemory inserts a new memory row. ID, CreatedAt, and UpdatedAt must be pre-populated.
func (db *DB) SaveMemory(r MemoryRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO memories (`+memoryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Category, r.Title, r.Content, r.Source, r.Confidence, r.Importance,
		r.CreatedAt, r.UpdatedAt, r.LastRetrievedAt,
		boolToInt(r.IsUserConfirmed), boolToInt(r.IsSensitive),
		r.TagsJSON, r.RelatedConversationID, r.ValidUntil,
	)
	return err
}

// UpdateMemory updates the mutable fields of an existing memory row.
func (db *DB) UpdateMemory(r MemoryRow) error {
	// Null out embedding_at so any path that changes title/content triggers a
	// fresh async embed on the next recall cycle. The asyncEmbed goroutines called
	// by extractors will repopulate it; direct UI edits that don't call asyncEmbed
	// will also correctly clear the stale vector marker.
	_, err := db.conn.Exec(
		`UPDATE memories SET title=?, content=?, confidence=?, importance=?, updated_at=?,
		 is_user_confirmed=?, is_sensitive=?, tags_json=?, valid_until=?, embedding_at=NULL
		 WHERE memory_id=?`,
		r.Title, r.Content, r.Confidence, r.Importance, r.UpdatedAt,
		boolToInt(r.IsUserConfirmed), boolToInt(r.IsSensitive),
		r.TagsJSON, r.ValidUntil, r.ID,
	)
	return err
}

// SetValidUntil marks a memory as no longer valid after the given ISO8601 timestamp.
// The memory is excluded from retrieval but preserved for historical record.
// Use time.Now().UTC().Format(time.RFC3339) as until to invalidate immediately.
func (db *DB) SetValidUntil(id, until string) error {
	_, err := db.conn.Exec(
		`UPDATE memories SET valid_until=?, updated_at=? WHERE memory_id=?`,
		until, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// UpdateMemoryEmbedding stores a precomputed embedding vector on an existing
// memory row. vec is serialised as a JSON float32 array in the embedding BLOB
// column. Callers should invoke this from a goroutine so it doesn't block the
// extraction pipeline.
func (db *DB) UpdateMemoryEmbedding(id, model string, vec []float32) error {
	data, err := json.Marshal(vec)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(
		`UPDATE memories SET embedding=?, embedding_model=?, embedding_at=? WHERE memory_id=?`,
		data, model, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// fetchEmbeddings returns a map of memory_id → float32 vector for the given IDs.
// Rows with no embedding are omitted from the map. Used by RelevantMemories to
// cosine re-rank FTS5 candidates when a query vector is available.
func (db *DB) fetchEmbeddings(ids []string) map[string][]float32 {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := db.conn.Query(
		`SELECT memory_id, embedding FROM memories WHERE memory_id IN (`+
			strings.Join(placeholders, ",")+`) AND embedding IS NOT NULL`,
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[string][]float32, len(ids))
	for rows.Next() {
		var memID string
		var blob []byte
		if err := rows.Scan(&memID, &blob); err != nil {
			continue
		}
		var vec []float32
		if err := json.Unmarshal(blob, &vec); err != nil {
			continue
		}
		out[memID] = vec
	}
	return out
}

// FetchMemory returns a single memory by ID, or nil if not found.
func (db *DB) FetchMemory(id string) (*MemoryRow, error) {
	row := db.conn.QueryRow(
		`SELECT `+memoryCols+` FROM memories WHERE memory_id=?`, id)
	r, err := scanMemoryRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ConfirmMemory sets is_user_confirmed=1 on the memory with the given ID.
func (db *DB) ConfirmMemory(id string) error {
	_, err := db.conn.Exec(
		`UPDATE memories SET is_user_confirmed=1, updated_at=? WHERE memory_id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// FindDuplicateMemory returns an existing memory matching the given category and title, or nil.
func (db *DB) FindDuplicateMemory(category, title string) (*MemoryRow, error) {
	row := db.conn.QueryRow(
		`SELECT `+memoryCols+` FROM memories WHERE category=? AND title=? LIMIT 1`,
		category, title,
	)
	r, err := scanMemoryRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// CountMemories returns the total number of memories in the database.
func (db *DB) CountMemories() int {
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n) //nolint:errcheck
	return n
}

// ListAllMemories returns every memory row with no limit, ordered by category
// then importance DESC. Used by the dream cycle for consolidation scans.
func (db *DB) ListAllMemories() ([]MemoryRow, error) {
	rows, err := db.conn.Query(
		`SELECT ` + memoryCols + `
		 FROM memories
		 ORDER BY category, importance DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DeleteStaleMemories removes old, low-value memories. Returns the count deleted.
// Rules:
//   - confidence < minConfidence AND age > maxAge
//   - last_retrieved_at IS NULL AND age > unretrievedMaxAge AND importance < minImportance
func (db *DB) DeleteStaleMemories(maxAgeDays, unretrievedMaxAgeDays int, minConfidence, minImportance float64) int {
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -maxAgeDays).Format(time.RFC3339Nano)
	unretrievedCutoff := now.AddDate(0, 0, -unretrievedMaxAgeDays).Format(time.RFC3339Nano)

	// Low-confidence old memories.
	r1, _ := db.conn.Exec(
		`DELETE FROM memories WHERE confidence < ? AND created_at < ?`,
		minConfidence, cutoff)
	n1, _ := r1.RowsAffected()

	// Never-retrieved old memories with low importance.
	r2, _ := db.conn.Exec(
		`DELETE FROM memories WHERE last_retrieved_at IS NULL AND created_at < ? AND importance < ?`,
		unretrievedCutoff, minImportance)
	n2, _ := r2.RowsAffected()

	return int(n1 + n2)
}

// RelevantMemories returns memories scored by a weighted combination of keyword
// relevance (0.5), static importance (0.3), and time-decayed recency (0.2).
// Commitment memories receive an importance boost (+0.2) so they surface first.
// FTS5 is used for candidate selection when available, falling back to
// importance-ordered pre-filter. Invalidated memories (valid_until in the past)
// are excluded.
// RelevantMemories returns up to limit memories ranked by relevance to query.
//
// When queryVec is non-nil (a precomputed embedding of the query — optionally
// generated via HyDE), scoring uses a hybrid formula:
//
//	cosine×0.45 + keyword×0.25 + importance×0.20 + recency×0.10 − diversity
//
// When queryVec is nil the legacy keyword-dominant formula is used:
//
//	keyword×0.50 + importance×0.30 + recency×0.20 − diversity
//
// In both modes FTS5 provides the candidate set and commitment memories are
// importance-boosted and exempt from the diversity penalty.
func (db *DB) RelevantMemories(query string, limit int, queryVec []float32) ([]MemoryRow, error) {
	if limit <= 0 {
		limit = 4
	}
	keywords := extractKeywords(query)
	if len(keywords) == 0 && queryVec == nil {
		return db.listActiveMemories(limit)
	}

	// FTS5 candidate selection — gives better recall than importance-only pre-filter.
	// Falls back silently if FTS5 is unavailable or the query produces no results.
	var all []MemoryRow
	var rawBM25 map[string]float64 // FTS5 rank values (negative; more negative = better)
	var err error
	if len(keywords) > 0 {
		ftsQuery := strings.Join(keywords, " OR ")
		all, err = db.ftsSearch(ftsQuery, 50)
		if err == nil && len(all) > 0 {
			rawBM25, _ = db.ftsRanks(ftsQuery, 50)
		}
	}
	if err != nil || len(all) == 0 {
		all, err = db.listActiveMemories(50)
		if err != nil {
			return nil, err
		}
	}
	if len(all) == 0 {
		return nil, nil
	}

	// Normalize BM25 ranks to [0, 1] across the candidate set.
	// FTS5 rank is negative; more negative = better match.
	// Best (most negative) → 1.0, worst (least negative) → 0.0.
	bm25Norm := normalizeBM25(rawBM25)

	// Fetch stored embeddings for the candidate set when a query vector is present.
	var embeddings map[string][]float32
	if queryVec != nil {
		ids := make([]string, len(all))
		for i, m := range all {
			ids[i] = m.ID
		}
		embeddings = db.fetchEmbeddings(ids)
	}

	now := time.Now()

	type scored struct {
		row   MemoryRow
		score float64
	}
	var results []scored

	for _, m := range all {
		// BM25 signal: normalized FTS5 rank when available; falls back to keyword
		// overlap fraction for memories surfaced via listActiveMemories fallback.
		keywordScore := bm25Norm[m.ID]
		if len(rawBM25) == 0 && len(keywords) > 0 {
			haystack := strings.ToLower(m.Title + " " + m.Content + " " + m.TagsJSON)
			hits := 0
			for _, kw := range keywords {
				if strings.Contains(haystack, kw) {
					hits++
				}
			}
			keywordScore = float64(hits) / float64(len(keywords))
		}

		// Time-decayed recency: exponential decay with 7-day half-life.
		var hoursAge float64
		if t, err2 := time.Parse(time.RFC3339Nano, m.UpdatedAt); err2 == nil {
			hoursAge = now.Sub(t).Hours()
		}
		recencyScore := math.Exp(-0.693 * hoursAge / (7.0 * 24.0))

		// Commitment memories always surface — boost importance.
		importance := m.Importance
		if m.Category == "commitment" {
			importance = math.Min(importance+0.2, 1.0)
		}

		// Retrieval diversity penalty: fades over 1 hour; commitments exempt.
		diversityPenalty := 0.0
		if m.Category != "commitment" && m.LastRetrievedAt != nil && *m.LastRetrievedAt != "" {
			if lastRetr, err2 := time.Parse(time.RFC3339Nano, *m.LastRetrievedAt); err2 == nil {
				hoursSinceRetrieval := now.Sub(lastRetr).Hours()
				if hoursSinceRetrieval < 1.0 {
					diversityPenalty = 0.15 * (1.0 - hoursSinceRetrieval)
				}
			}
		}

		var combined float64
		if queryVec != nil {
			// Full hybrid: cosine×0.45 + bm25×0.25 + importance×0.20 + recency×0.10
			cosine := 0.0
			if vec, ok := embeddings[m.ID]; ok {
				cosine = cosineSim32(queryVec, vec)
			}
			combined = cosine*0.45 + keywordScore*0.25 + importance*0.20 + recencyScore*0.10 - diversityPenalty
		} else {
			// BM25-dominant: bm25×0.50 + importance×0.30 + recency×0.20
			combined = keywordScore*0.5 + importance*0.3 + recencyScore*0.2 - diversityPenalty
		}

		results = append(results, scored{row: m, score: combined})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]MemoryRow, len(results))
	for i, r := range results {
		out[i] = r.row
	}
	return out, nil
}

// ftsRanks queries the memories_fts virtual table for BM25 rank values.
// Returns a map of memory_id → raw FTS5 rank (negative float; more negative = better).
// Returns nil on any error — callers treat missing ranks as BM25 unavailable.
func (db *DB) ftsRanks(ftsQuery string, limit int) (map[string]float64, error) {
	rows, err := db.conn.Query(
		`SELECT memory_id, rank FROM memories_fts WHERE memories_fts MATCH ? LIMIT ?`,
		ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]float64)
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, err
		}
		out[id] = rank
	}
	return out, rows.Err()
}

// normalizeBM25 converts a map of raw FTS5 rank values (negative; more negative
// = better) into [0,1]-normalized scores. Best match → 1.0, worst → 0.0.
// Returns nil when ranks is empty or nil.
func normalizeBM25(ranks map[string]float64) map[string]float64 {
	if len(ranks) == 0 {
		return nil
	}
	minR, maxR := math.MaxFloat64, -math.MaxFloat64
	for _, r := range ranks {
		if r < minR {
			minR = r
		}
		if r > maxR {
			maxR = r
		}
	}
	out := make(map[string]float64, len(ranks))
	span := minR - maxR // minR < maxR (both negative); span is negative
	for id, r := range ranks {
		if span == 0 {
			out[id] = 0.5
		} else {
			out[id] = (r - maxR) / span
		}
	}
	return out
}

// cosineSim32 computes cosine similarity between two float32 vectors.
// Returns 0 if either vector is nil, empty, or zero-magnitude.
func cosineSim32(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		magA += ai * ai
		magB += bi * bi
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// listActiveMemories returns up to limit active memories (valid_until IS NULL or
// in the future), ordered by importance DESC, updated_at DESC.
func (db *DB) listActiveMemories(limit int) ([]MemoryRow, error) {
	rows, err := db.conn.Query(
		`SELECT `+memoryCols+`
		 FROM memories
		 WHERE valid_until IS NULL OR valid_until > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		 ORDER BY importance DESC, updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ftsSearch uses the memories_fts FTS5 index to find candidate memories matching
// an OR query of keywords. Returns active memories only (valid_until filter applied).
func (db *DB) ftsSearch(ftsQuery string, limit int) ([]MemoryRow, error) {
	rows, err := db.conn.Query(
		`SELECT `+memoryCols+`
		 FROM memories
		 WHERE memory_id IN (SELECT memory_id FROM memories_fts WHERE memories_fts MATCH ?)
		   AND (valid_until IS NULL OR valid_until > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		 ORDER BY importance DESC, updated_at DESC LIMIT ?`,
		ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// UpdateLastRetrieved sets last_retrieved_at = now for a batch of memory IDs.
func (db *DB) UpdateLastRetrieved(ids []string) {
	if len(ids) == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		db.conn.Exec(`UPDATE memories SET last_retrieved_at=? WHERE memory_id=?`, now, id) //nolint:errcheck
	}
}

// extractKeywords splits a query into lowercased words, filtering stop words.
func extractKeywords(query string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "being": true, "have": true,
		"has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true,
		"might": true, "can": true, "shall": true, "to": true, "of": true,
		"in": true, "for": true, "on": true, "with": true, "at": true,
		"by": true, "from": true, "as": true, "into": true, "about": true,
		"it": true, "its": true, "i": true, "me": true, "my": true,
		"we": true, "our": true, "you": true, "your": true, "he": true,
		"she": true, "they": true, "them": true, "this": true, "that": true,
		"and": true, "or": true, "but": true, "not": true, "so": true,
		"if": true, "what": true, "how": true, "when": true, "where": true,
		"who": true, "which": true, "why": true, "just": true, "also": true,
	}
	words := strings.Fields(strings.ToLower(query))
	var out []string
	for _, w := range words {
		// Strip punctuation from edges.
		w = strings.Trim(w, ".,!?;:'\"()[]{}/-")
		if len(w) < 2 || stop[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// ---------------------------------------------------------------------------
// Entity Knowledge Graph — memory_entities + memory_edges
// ---------------------------------------------------------------------------

// EntityRow is a named, typed entity extracted from conversation memories.
type EntityRow struct {
	EntityID     string
	Name         string
	EntityType   string // person, place, organization, concept, technology, event, other
	FirstSeen    string
	LastSeen     string
	Embedding    []float32 // nil when not yet embedded
	MetadataJSON string    // arbitrary JSON for future fields
}

// EdgeRow is a directed, temporal relation between two entities.
type EdgeRow struct {
	EdgeID         string
	SourceEntity   string
	TargetEntity   string
	Relation       string  // e.g. "works_at", "uses", "located_in"
	ValidFrom      string
	ValidUntil     *string // nil = currently true
	Confidence     float64
	SourceMemoryID *string // memory that produced this edge
}

// UpsertEntity inserts a new entity or bumps last_seen if it already exists.
// Returns the entity_id (existing or newly created).
func (db *DB) UpsertEntity(name, entityType, now string) (string, error) {
	var existing string
	err := db.conn.QueryRow(
		`SELECT entity_id FROM memory_entities WHERE name=? AND entity_type=?`,
		name, entityType,
	).Scan(&existing)
	if err == nil {
		// Already exists — update last_seen.
		_, uerr := db.conn.Exec(
			`UPDATE memory_entities SET last_seen=? WHERE entity_id=?`,
			now, existing,
		)
		return existing, uerr
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// New entity.
	id := fmt.Sprintf("ent_%d", time.Now().UnixNano())
	_, err = db.conn.Exec(
		`INSERT INTO memory_entities (entity_id, name, entity_type, first_seen, last_seen, metadata_json)
		 VALUES (?, ?, ?, ?, ?, '{}')`,
		id, name, entityType, now, now,
	)
	return id, err
}

// UpdateEntityEmbedding stores the embedding vector for an entity.
func (db *DB) UpdateEntityEmbedding(entityID string, vec []float32) error {
	blob, err := json.Marshal(vec)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(
		`UPDATE memory_entities SET embedding=? WHERE entity_id=?`, blob, entityID,
	)
	return err
}

// SaveEdge inserts a new entity relation edge.
func (db *DB) SaveEdge(e EdgeRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO memory_edges
		 (edge_id, source_entity, target_entity, relation, valid_from, valid_until, confidence, source_memory_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.EdgeID, e.SourceEntity, e.TargetEntity, e.Relation,
		e.ValidFrom, e.ValidUntil, e.Confidence, e.SourceMemoryID,
	)
	return err
}

// SupersedeEdge closes any currently-valid edges between the same source/target/relation
// pair by setting valid_until = now. Call before SaveEdge when a fact changes.
func (db *DB) SupersedeEdge(sourceEntity, targetEntity, relation, now string) error {
	_, err := db.conn.Exec(
		`UPDATE memory_edges SET valid_until=?
		 WHERE source_entity=? AND target_entity=? AND relation=? AND valid_until IS NULL`,
		now, sourceEntity, targetEntity, relation,
	)
	return err
}

// FindNearestEntities returns up to limit entities ranked by cosine similarity
// to queryVec. Falls back to returning the most-recently-seen entities when
// no embeddings are stored yet.
func (db *DB) FindNearestEntities(queryVec []float32, limit int) ([]EntityRow, error) {
	rows, err := db.conn.Query(
		`SELECT entity_id, name, entity_type, first_seen, last_seen, embedding, metadata_json
		 FROM memory_entities ORDER BY last_seen DESC LIMIT ?`, limit*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		e    EntityRow
		sim  float32
		hasV bool
	}
	var cands []candidate
	for rows.Next() {
		var e EntityRow
		var blob []byte
		if err := rows.Scan(&e.EntityID, &e.Name, &e.EntityType,
			&e.FirstSeen, &e.LastSeen, &blob, &e.MetadataJSON); err != nil {
			continue
		}
		c := candidate{e: e}
		if len(blob) > 0 {
			var v []float32
			if err := json.Unmarshal(blob, &v); err == nil {
				e.Embedding = v
				c.sim = float32(cosineSim32(queryVec, v))
				c.hasV = true
			}
		}
		c.e = e
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].hasV != cands[j].hasV {
			return cands[i].hasV
		}
		if cands[i].sim != cands[j].sim {
			return cands[i].sim > cands[j].sim
		}
		return cands[i].e.LastSeen > cands[j].e.LastSeen
	})

	out := make([]EntityRow, 0, limit)
	for i, c := range cands {
		if i >= limit {
			break
		}
		out = append(out, c.e)
	}
	return out, nil
}

// TraverseEntityGraph performs BFS starting from seedIDs and returns all valid
// edges reachable within maxHops. Duplicate edges are excluded.
func (db *DB) TraverseEntityGraph(seedIDs []string, maxHops int) ([]EdgeRow, error) {
	if len(seedIDs) == 0 || maxHops <= 0 {
		return nil, nil
	}
	visited := make(map[string]bool, len(seedIDs))
	for _, id := range seedIDs {
		visited[id] = true
	}
	frontier := seedIDs
	seenEdges := make(map[string]bool)
	var result []EdgeRow

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string
		for _, entityID := range frontier {
			rows, err := db.conn.Query(
				`SELECT edge_id, source_entity, target_entity, relation,
				        valid_from, valid_until, confidence, source_memory_id
				 FROM memory_edges
				 WHERE (source_entity=? OR target_entity=?) AND valid_until IS NULL`,
				entityID, entityID,
			)
			if err != nil {
				continue
			}
			for rows.Next() {
				var e EdgeRow
				if err := rows.Scan(&e.EdgeID, &e.SourceEntity, &e.TargetEntity, &e.Relation,
					&e.ValidFrom, &e.ValidUntil, &e.Confidence, &e.SourceMemoryID); err != nil {
					continue
				}
				if seenEdges[e.EdgeID] {
					continue
				}
				seenEdges[e.EdgeID] = true
				result = append(result, e)
				// Add the other end to next frontier.
				other := e.TargetEntity
				if other == entityID {
					other = e.SourceEntity
				}
				if !visited[other] {
					visited[other] = true
					nextFrontier = append(nextFrontier, other)
				}
			}
			rows.Close()
		}
		frontier = nextFrontier
	}
	return result, nil
}

// PruneExpiredEdges sets valid_until on edges whose target entity no longer
// exists. Returns the number of edges closed. Used by dream Phase 1.
func (db *DB) PruneExpiredEdges() int {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.conn.Exec(
		`UPDATE memory_edges SET valid_until=?
		 WHERE valid_until IS NULL AND (
		     source_entity NOT IN (SELECT entity_id FROM memory_entities) OR
		     target_entity NOT IN (SELECT entity_id FROM memory_entities)
		 )`, now,
	)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// DeduplicateEntities merges entity nodes that share the same name+type by
// repointing all edges to the oldest entity and deleting the duplicates.
// Returns the number of nodes removed. Used by dream Phase 2.
func (db *DB) DeduplicateEntities() int {
	rows, err := db.conn.Query(
		`SELECT name, entity_type, COUNT(*) AS cnt
		 FROM memory_entities GROUP BY name, entity_type HAVING cnt > 1`,
	)
	if err != nil {
		return 0
	}
	type dup struct{ name, entityType string }
	var dups []dup
	for rows.Next() {
		var d dup
		var cnt int
		if err := rows.Scan(&d.name, &d.entityType, &cnt); err == nil {
			dups = append(dups, d)
		}
	}
	rows.Close()

	removed := 0
	for _, d := range dups {
		// Fetch all duplicates oldest-first, including whether they have an embedding.
		type dupRow struct {
			id          string
			hasEmbedding bool
		}
		var candidates []dupRow
		r2, err := db.conn.Query(
			`SELECT entity_id, (embedding IS NOT NULL) AS has_emb
			 FROM memory_entities WHERE name=? AND entity_type=? ORDER BY first_seen ASC`,
			d.name, d.entityType,
		)
		if err != nil {
			continue
		}
		for r2.Next() {
			var row dupRow
			var hasEmb int
			if err := r2.Scan(&row.id, &hasEmb); err == nil {
				row.hasEmbedding = hasEmb != 0
				candidates = append(candidates, row)
			}
		}
		r2.Close()
		if len(candidates) < 2 {
			continue
		}
		// Prefer the node that already has an embedding; fall back to oldest.
		keep := candidates[0].id
		for _, c := range candidates {
			if c.hasEmbedding {
				keep = c.id
				break
			}
		}
		for _, c := range candidates {
			if c.id == keep {
				continue
			}
			// Repoint edges then delete the duplicate node.
			db.conn.Exec(`UPDATE memory_edges SET source_entity=? WHERE source_entity=?`, keep, c.id) //nolint:errcheck
			db.conn.Exec(`UPDATE memory_edges SET target_entity=? WHERE target_entity=?`, keep, c.id) //nolint:errcheck
			if _, err := db.conn.Exec(`DELETE FROM memory_entities WHERE entity_id=?`, c.id); err == nil {
				removed++
			}
		}
	}
	return removed
}

// FetchEntitiesByIDs returns EntityRows for all requested IDs in one query.
// IDs not found in the table are silently omitted.
func (db *DB) FetchEntitiesByIDs(ids []string) ([]EntityRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := db.conn.Query(
		`SELECT entity_id, name, entity_type, first_seen, last_seen, embedding, metadata_json
		 FROM memory_entities WHERE entity_id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntityRow
	for rows.Next() {
		var e EntityRow
		var blob []byte
		if err := rows.Scan(&e.EntityID, &e.Name, &e.EntityType,
			&e.FirstSeen, &e.LastSeen, &blob, &e.MetadataJSON); err != nil {
			continue
		}
		if len(blob) > 0 {
			var v []float32
			if err := json.Unmarshal(blob, &v); err == nil {
				e.Embedding = v
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEntities returns up to limit entity rows ordered by last_seen DESC.
func (db *DB) ListEntities(limit int) ([]EntityRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.conn.Query(
		`SELECT entity_id, name, entity_type, first_seen, last_seen, embedding, metadata_json
		 FROM memory_entities ORDER BY last_seen DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntityRow
	for rows.Next() {
		var e EntityRow
		var blob []byte
		if err := rows.Scan(&e.EntityID, &e.Name, &e.EntityType,
			&e.FirstSeen, &e.LastSeen, &blob, &e.MetadataJSON); err != nil {
			continue
		}
		if len(blob) > 0 {
			var v []float32
			if err := json.Unmarshal(blob, &v); err == nil {
				e.Embedding = v
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FetchTelegramSession returns the telegram_sessions row for chatID, or nil if not found.
func (db *DB) FetchTelegramSession(chatID int64) (*TelegramSessionRow, error) {
	row := db.conn.QueryRow(
		`SELECT chat_id, user_id, active_conversation_id, created_at, updated_at, last_message_id
		 FROM telegram_sessions WHERE chat_id = ?`, chatID)
	var r TelegramSessionRow
	if err := row.Scan(&r.ChatID, &r.UserID, &r.ActiveConversationID,
		&r.CreatedAt, &r.UpdatedAt, &r.LastMessageID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// UpsertTelegramSession inserts or replaces a telegram_sessions row.
func (db *DB) UpsertTelegramSession(r TelegramSessionRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO telegram_sessions
		     (chat_id, user_id, active_conversation_id, created_at, updated_at, last_message_id)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		     user_id                = excluded.user_id,
		     active_conversation_id = excluded.active_conversation_id,
		     updated_at             = excluded.updated_at,
		     last_message_id        = excluded.last_message_id`,
		r.ChatID, r.UserID, r.ActiveConversationID, r.CreatedAt, r.UpdatedAt, r.LastMessageID,
	)
	return err
}

// FetchCommSession returns the communication_sessions row, or nil if not found.
func (db *DB) FetchCommSession(platform, channelID, threadID string) (*CommSessionRow, error) {
	row := db.conn.QueryRow(
		`SELECT platform, channel_id, thread_id, channel_name, user_id,
		        active_conversation_id, created_at, updated_at, last_message_id
		 FROM communication_sessions
		 WHERE platform = ? AND channel_id = ? AND thread_id = ?`,
		platform, channelID, threadID)
	var r CommSessionRow
	if err := row.Scan(&r.Platform, &r.ChannelID, &r.ThreadID, &r.ChannelName, &r.UserID,
		&r.ActiveConversationID, &r.CreatedAt, &r.UpdatedAt, &r.LastMessageID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// UpsertCommSession inserts or replaces a communication_sessions row.
func (db *DB) UpsertCommSession(r CommSessionRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO communication_sessions
		     (platform, channel_id, thread_id, channel_name, user_id,
		      active_conversation_id, created_at, updated_at, last_message_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(platform, channel_id, thread_id) DO UPDATE SET
		     channel_name           = excluded.channel_name,
		     user_id                = excluded.user_id,
		     active_conversation_id = excluded.active_conversation_id,
		     updated_at             = excluded.updated_at,
		     last_message_id        = excluded.last_message_id`,
		r.Platform, r.ChannelID, r.ThreadID, r.ChannelName, r.UserID,
		r.ActiveConversationID, r.CreatedAt, r.UpdatedAt, r.LastMessageID,
	)
	return err
}

// ── Browser sessions ──────────────────────────────────────────────────────────

// SaveBrowserSession upserts the cookie blob for a host.
func (db *DB) SaveBrowserSession(host, cookiesJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(
		`INSERT INTO browser_sessions (host, cookies_json, last_used_at, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET
		   cookies_json = excluded.cookies_json,
		   last_used_at = excluded.last_used_at`,
		host, cookiesJSON, now, now,
	)
	return err
}

// LoadBrowserSession returns the stored cookie blob for a host.
// Returns ("", false, nil) when no session exists.
func (db *DB) LoadBrowserSession(host string) (cookiesJSON string, found bool, err error) {
	var lastUsed string
	row := db.conn.QueryRow(
		`SELECT cookies_json, last_used_at FROM browser_sessions WHERE host = ?`, host,
	)
	if scanErr := row.Scan(&cookiesJSON, &lastUsed); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, scanErr
	}
	// Expire sessions older than 7 days.
	t, parseErr := time.Parse(time.RFC3339, lastUsed)
	if parseErr != nil || time.Since(t) > 7*24*time.Hour {
		_ = db.DeleteBrowserSession(host)
		return "", false, nil
	}
	return cookiesJSON, true, nil
}

// DeleteBrowserSession removes the stored session for a host.
func (db *DB) DeleteBrowserSession(host string) error {
	_, err := db.conn.Exec(`DELETE FROM browser_sessions WHERE host = ?`, host)
	return err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Token usage ───────────────────────────────────────────────────────────────

// TokenUsageRow is one persisted token usage event.
type TokenUsageRow struct {
	ID                string
	ConversationID    string
	Provider          string
	Model             string
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	InputCostUSD      float64
	OutputCostUSD     float64
	TotalCostUSD      float64
	RecordedAt        string
}

// ModelUsageBreakdown aggregates usage for one provider+model combination.
type ModelUsageBreakdown struct {
	Provider          string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
	TotalCostUSD      float64
	TurnCount         int64
}

// DailyUsage aggregates usage for one calendar day.
type DailyUsage struct {
	Date              string // "2025-04-03"
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
	CostUSD           float64
	TurnCount         int64
}

// TokenUsageSummary is the full aggregated response.
type TokenUsageSummary struct {
	TotalInputTokens       int64
	TotalCachedInputTokens int64
	TotalOutputTokens      int64
	TotalTokens            int64
	TotalCostUSD           float64
	TurnCount              int64
	ByModel                []ModelUsageBreakdown
	DailySeries            []DailyUsage
}

// RecordTokenUsage persists one token usage event.
func (db *DB) RecordTokenUsage(id, convID, provider, model string,
	inputTokens, cachedInputTokens, outputTokens int,
	inputCost, outputCost float64,
	recordedAt string,
) error {
	total := inputCost + outputCost
	_, err := db.conn.Exec(`
		INSERT INTO token_usage
			(id, conversation_id, provider, model,
			 input_tokens, cached_input_tokens, output_tokens,
			 input_cost_usd, output_cost_usd, total_cost_usd, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, convID, provider, model,
		inputTokens, cachedInputTokens, outputTokens,
		inputCost, outputCost, total, recordedAt,
	)
	return err
}

// TokenUsageEvents returns raw events newest-first with optional filters.
func (db *DB) TokenUsageEvents(since, until, provider, model string, limit int) ([]TokenUsageRow, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	q := `SELECT id, conversation_id, provider, model,
		input_tokens, cached_input_tokens, output_tokens,
		input_cost_usd, output_cost_usd, total_cost_usd, recorded_at
		FROM token_usage WHERE 1=1`
	args := []any{}
	if since != "" {
		q += " AND recorded_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		q += " AND recorded_at <= ?"
		args = append(args, until)
	}
	if provider != "" {
		q += " AND provider = ?"
		args = append(args, provider)
	}
	if model != "" {
		q += " AND model = ?"
		args = append(args, model)
	}
	q += " ORDER BY recorded_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenUsageRow
	for rows.Next() {
		var r TokenUsageRow
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.Provider, &r.Model,
			&r.InputTokens, &r.CachedInputTokens, &r.OutputTokens,
			&r.InputCostUSD, &r.OutputCostUSD, &r.TotalCostUSD, &r.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTokenUsageSummary returns aggregated stats with optional date range and daily series.
func (db *DB) GetTokenUsageSummary(since, until string, dailyDays int) (TokenUsageSummary, error) {
	where := "WHERE 1=1"
	args := []any{}
	if since != "" {
		where += " AND recorded_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		where += " AND recorded_at <= ?"
		args = append(args, until)
	}

	var s TokenUsageSummary

	// ── Scalar totals ─────────────────────────────────────────────────────────
	row := db.conn.QueryRow(
		"SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(cached_input_tokens),0), COALESCE(SUM(output_tokens),0), "+
			"COALESCE(SUM(total_cost_usd),0), COUNT(*) FROM token_usage "+where,
		args...)
	if err := row.Scan(&s.TotalInputTokens, &s.TotalCachedInputTokens, &s.TotalOutputTokens, &s.TotalCostUSD, &s.TurnCount); err != nil {
		return s, err
	}
	s.TotalTokens = s.TotalInputTokens + s.TotalOutputTokens

	// ── Per-model breakdown ───────────────────────────────────────────────────
	mrows, err := db.conn.Query(
		"SELECT provider, model, "+
			"COALESCE(SUM(input_tokens),0), COALESCE(SUM(cached_input_tokens),0), COALESCE(SUM(output_tokens),0), "+
			"COALESCE(SUM(total_cost_usd),0), COUNT(*) "+
			"FROM token_usage "+where+
			" GROUP BY provider, model ORDER BY SUM(total_cost_usd) DESC",
		args...)
	if err != nil {
		return s, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var m ModelUsageBreakdown
		if err := mrows.Scan(&m.Provider, &m.Model, &m.InputTokens, &m.CachedInputTokens, &m.OutputTokens, &m.TotalCostUSD, &m.TurnCount); err != nil {
			return s, err
		}
		m.TotalTokens = m.InputTokens + m.OutputTokens
		s.ByModel = append(s.ByModel, m)
	}
	if err := mrows.Err(); err != nil {
		return s, err
	}

	// ── Daily series ─────────────────────────────────────────────────────────
	// dailyDays=0 skips the series entirely; callers receive a nil DailySeries.
	// Pass days=30 (or any positive value) to include the per-day breakdown.
	if dailyDays > 0 {
		dargs := []any{}
		dwhere := "WHERE 1=1"
		if since != "" {
			dwhere += " AND recorded_at >= ?"
			dargs = append(dargs, since)
		}
		if until != "" {
			dwhere += " AND recorded_at <= ?"
			dargs = append(dargs, until)
		}
		drows, err := db.conn.Query(
			"SELECT strftime('%Y-%m-%d', recorded_at) AS day, "+
				"COALESCE(SUM(input_tokens),0), COALESCE(SUM(cached_input_tokens),0), COALESCE(SUM(output_tokens),0), "+
				"COALESCE(SUM(total_cost_usd),0), COUNT(*) "+
				"FROM token_usage "+dwhere+
				" GROUP BY day ORDER BY day ASC LIMIT ?",
			append(dargs, dailyDays)...)
		if err != nil {
			return s, err
		}
		defer drows.Close()
		for drows.Next() {
			var d DailyUsage
			if err := drows.Scan(&d.Date, &d.InputTokens, &d.CachedInputTokens, &d.OutputTokens, &d.CostUSD, &d.TurnCount); err != nil {
				return s, err
			}
			d.TotalTokens = d.InputTokens + d.OutputTokens
			s.DailySeries = append(s.DailySeries, d)
		}
		if err := drows.Err(); err != nil {
			return s, err
		}
	}

	return s, nil
}

// BackfillTokenUsageCosts re-computes and updates cost columns for any existing
// token_usage rows that have total_cost_usd = 0 for non-local providers.
// Returns the number of rows updated. Safe to call at startup; no-ops if all
// rows already have accurate costs.
func (db *DB) BackfillTokenUsageCosts() int {
	rows, err := db.conn.Query(`
		SELECT id, provider, model, input_tokens, output_tokens
		FROM token_usage
		WHERE total_cost_usd = 0
		  AND provider NOT IN ('lm_studio', 'ollama', 'atlas_engine', 'atlas_mlx')
	`)
	if err != nil {
		return 0
	}

	type update struct {
		id                    string
		inputCost, outputCost float64
	}
	var updates []update

	for rows.Next() {
		var id, provider, model string
		var inputTokens, outputTokens int
		if err := rows.Scan(&id, &provider, &model, &inputTokens, &outputTokens); err != nil {
			continue
		}
		ic, oc, known := ComputeCost(provider, model, inputTokens, outputTokens)
		if !known || (ic == 0 && oc == 0) {
			continue
		}
		updates = append(updates, update{id, ic, oc})
	}
	rows.Close()

	count := 0
	for _, u := range updates {
		_, err := db.conn.Exec(
			`UPDATE token_usage SET input_cost_usd=?, output_cost_usd=?, total_cost_usd=? WHERE id=?`,
			u.inputCost, u.outputCost, u.inputCost+u.outputCost, u.id,
		)
		if err == nil {
			count++
		}
	}
	return count
}

// TokenUsageDeleteBefore deletes all events recorded before the given ISO8601 timestamp.
// Returns the number of rows deleted.
func (db *DB) TokenUsageDeleteBefore(before string) (int64, error) {
	res, err := db.conn.Exec("DELETE FROM token_usage WHERE recorded_at < ?", before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Image usage ───────────────────────────────────────────────────────────────

// ImageUsageRow is one recorded image generation event.
type ImageUsageRow struct {
	Provider   string
	Model      string
	Quality    string
	ImageCount int
	CostUSD    float64
	RecordedAt string
}

// ImageUsageSummary holds aggregated image generation stats.
type ImageUsageSummary struct {
	TotalImages  int64
	TotalCostUSD float64
	ByModel      []ImageModelBreakdown
}

// ImageModelBreakdown is per-model image stats.
type ImageModelBreakdown struct {
	Provider    string
	Model       string
	ImageCount  int64
	TotalCostUSD float64
}

// RecordImageUsage persists one image generation event.
func (db *DB) RecordImageUsage(provider, model, quality string, count int, costUSD float64) error {
	id := fmt.Sprintf("img-%d", time.Now().UnixNano())
	recordedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.conn.Exec(
		`INSERT INTO image_usage (id, provider, model, quality, image_count, cost_usd, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, provider, model, quality, count, costUSD, recordedAt,
	)
	return err
}

// GetImageUsageSummary returns aggregated image usage stats for the given date range.
func (db *DB) GetImageUsageSummary(since, until string) (ImageUsageSummary, error) {
	var summary ImageUsageSummary

	args := []any{}
	where := "1=1"
	if since != "" {
		where += " AND recorded_at >= ?"
		args = append(args, since)
	}
	if until != "" {
		where += " AND recorded_at <= ?"
		args = append(args, until)
	}

	row := db.conn.QueryRow(
		"SELECT COALESCE(SUM(image_count),0), COALESCE(SUM(cost_usd),0) FROM image_usage WHERE "+where,
		args...,
	)
	if err := row.Scan(&summary.TotalImages, &summary.TotalCostUSD); err != nil {
		return summary, err
	}

	rows, err := db.conn.Query(
		`SELECT provider, model, SUM(image_count), SUM(cost_usd)
		 FROM image_usage WHERE `+where+`
		 GROUP BY provider, model ORDER BY SUM(cost_usd) DESC`,
		args...,
	)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var b ImageModelBreakdown
		if err := rows.Scan(&b.Provider, &b.Model, &b.ImageCount, &b.TotalCostUSD); err != nil {
			return summary, err
		}
		summary.ByModel = append(summary.ByModel, b)
	}
	return summary, rows.Err()
}

// ── Local auth credentials ────────────────────────────────────────────────────

// LocalCredentialRow is the raw DB row for a local auth credential.
type LocalCredentialRow struct {
	ID         string
	Type       string
	Name       string
	Credential string // JSON-encoded webauthn.Credential, empty for PIN type
	PINHash    string // bcrypt hash, empty for webauthn type
	CreatedAt  string
	LastUsedAt string
}

// SaveLocalCredential inserts or replaces a local auth credential.
func (db *DB) SaveLocalCredential(id, credType, name, credential, pinHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO local_auth_credentials
		 (id, type, name, credential, pin_hash, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, credType, name, credential, pinHash, now, now,
	)
	return err
}

// LoadLocalCredentials returns all stored local auth credentials.
func (db *DB) LoadLocalCredentials() ([]LocalCredentialRow, error) {
	rows, err := db.conn.Query(
		`SELECT id, type, name, COALESCE(credential,''), COALESCE(pin_hash,''), created_at, COALESCE(last_used_at,'')
		 FROM local_auth_credentials ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalCredentialRow
	for rows.Next() {
		var r LocalCredentialRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Name, &r.Credential, &r.PINHash, &r.CreatedAt, &r.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HasLocalCredentials returns true if at least one local credential is stored.
func (db *DB) HasLocalCredentials() bool {
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM local_auth_credentials`).Scan(&n) //nolint:errcheck
	return n > 0
}

// HasLocalCredentialOfType returns true if at least one credential of the given
// type (e.g. "webauthn" or "pin") is stored. Used to initialise the per-type
// atomic flags in LocalAuthService so the status endpoint never needs a live
// DB query for the common case.
func (db *DB) HasLocalCredentialOfType(credType string) bool {
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM local_auth_credentials WHERE type=?`, credType).Scan(&n) //nolint:errcheck
	return n > 0
}

// UpdateLocalCredentialSignCount updates the sign count for a WebAuthn credential.
func (db *DB) UpdateLocalCredentialSignCount(id string, credJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(
		`UPDATE local_auth_credentials SET credential=?, last_used_at=? WHERE id=?`,
		credJSON, now, id,
	)
	return err
}

// TouchLocalCredential updates last_used_at for a credential.
func (db *DB) TouchLocalCredential(id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	db.conn.Exec(`UPDATE local_auth_credentials SET last_used_at=? WHERE id=?`, now, id) //nolint:errcheck
}

// DeleteLocalCredential removes a local credential by ID.
func (db *DB) DeleteLocalCredential(id string) error {
	_, err := db.conn.Exec(`DELETE FROM local_auth_credentials WHERE id=?`, id)
	return err
}
