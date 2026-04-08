// Package storage provides a SQLite database layer for the Go runtime.
// The schema matches the Swift MemoryStore so both runtimes can share the
// same database file during Phase 5 dual-run.
package storage

import (
	"database/sql"
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
			timestamp       TEXT NOT NULL
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
	}

	// Idempotent migrations for memories columns added after initial creation.
	// valid_until: ISO8601 timestamp after which a contradicted memory is excluded
	// from retrieval but preserved for history. NULL = still valid.
	alterMemories := []string{
		`ALTER TABLE memories ADD COLUMN valid_until TEXT`,
		// Backfill FTS5 index for memories that existed before the FTS5 table was added.
		`INSERT OR IGNORE INTO memories_fts(memory_id, title, content, tags_json)
		    SELECT memory_id, title, content, tags_json FROM memories`,
	}

	// Idempotent migrations for rows added to deferred_executions after its initial creation.
	// SQLite returns an error when a column already exists; swallow those errors.
	alterDeferred := []string{
		`ALTER TABLE deferred_executions ADD COLUMN skill_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN tool_id TEXT`,
		`ALTER TABLE deferred_executions ADD COLUMN action_id TEXT`,
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
}

// SaveMessage inserts a message and updates the conversation's updated_at.
func (db *DB) SaveMessage(id, convID, role, content, timestamp string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO messages(message_id, conversation_id, role, content, timestamp)
		 VALUES (?, ?, ?, ?, ?)`,
		id, convID, role, content, timestamp,
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
		`SELECT message_id, conversation_id, role, content, timestamp
		 FROM messages WHERE conversation_id = ? ORDER BY timestamp ASC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var r MessageRow
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.Role, &r.Content, &r.Timestamp); err != nil {
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

const deferredCols = `deferred_id, source_type, skill_id, tool_id, action_id,
	tool_call_id, normalized_input_json, conversation_id, originating_message_id,
	approval_id, summary, permission_level, risk_level, status, last_error,
	result_json, created_at, updated_at, preview_diff`

func scanDeferredRow(row interface{ Scan(...any) error }) (*DeferredExecRow, error) {
	var r DeferredExecRow
	err := row.Scan(
		&r.DeferredID, &r.SourceType, &r.SkillID, &r.ToolID, &r.ActionID,
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
			deferred_id, source_type, skill_id, tool_id, action_id,
			tool_call_id, normalized_input_json, conversation_id, originating_message_id,
			approval_id, summary, permission_level, risk_level, status, last_error,
			result_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.DeferredID, r.SourceType, r.SkillID, r.ToolID, r.ActionID,
		r.ToolCallID, r.NormalizedInputJSON, r.ConversationID, r.OriginatingMessageID,
		r.ApprovalID, r.Summary, r.PermissionLevel, r.RiskLevel, r.Status, r.LastError,
		r.ResultJSON, r.CreatedAt, r.UpdatedAt,
	)
	return err
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

// SaveWorkflowRun inserts one workflow run row.
func (db *DB) SaveWorkflowRun(row WorkflowRunRow) error {
	_, err := db.conn.Exec(
		`INSERT INTO workflow_runs
		 (run_id, workflow_id, workflow_name, status, outcome, input_values_json, step_runs_json,
		  approval_json, assistant_summary, error_message, started_at, finished_at, conversation_id,
		  trigger_source, duration_ms, artifacts_json, record_json)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
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
	_, err := db.conn.Exec(
		`UPDATE memories SET title=?, content=?, confidence=?, importance=?, updated_at=?,
		 is_user_confirmed=?, is_sensitive=?, tags_json=?, valid_until=? WHERE memory_id=?`,
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
func (db *DB) RelevantMemories(query string, limit int) ([]MemoryRow, error) {
	if limit <= 0 {
		limit = 4
	}
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return db.listActiveMemories(limit)
	}

	// Try FTS5 candidate selection — gives better recall than importance-only pre-filter.
	// Falls back silently if the FTS5 table is unavailable or the query fails.
	ftsQuery := strings.Join(keywords, " OR ")
	all, err := db.ftsSearch(ftsQuery, 50)
	if err != nil || len(all) == 0 {
		// Fall back to importance-ordered active memories.
		all, err = db.listActiveMemories(50)
		if err != nil {
			return nil, err
		}
	}
	if len(all) == 0 {
		return nil, nil
	}

	now := time.Now()

	type scored struct {
		row   MemoryRow
		score float64
	}
	var results []scored

	for _, m := range all {
		// Keyword relevance: fraction of query keywords found in title+content+tags.
		haystack := strings.ToLower(m.Title + " " + m.Content + " " + m.TagsJSON)
		hits := 0
		for _, kw := range keywords {
			if strings.Contains(haystack, kw) {
				hits++
			}
		}
		keywordScore := float64(hits) / float64(len(keywords))

		// Time-decayed recency: exponential decay with 7-day half-life.
		var hoursAge float64
		if t, err := time.Parse(time.RFC3339Nano, m.UpdatedAt); err == nil {
			hoursAge = now.Sub(t).Hours()
		}
		recencyScore := math.Exp(-0.693 * hoursAge / (7.0 * 24.0))

		// Commitment memories always surface — boost importance.
		importance := m.Importance
		if m.Category == "commitment" {
			importance = math.Min(importance+0.2, 1.0)
		}

		// Retrieval diversity penalty: if this memory was retrieved very recently
		// (within the last hour — roughly the last few turns), reduce its score
		// so fresh, unseen memories can surface. Commitments are exempt.
		diversityPenalty := 0.0
		if m.Category != "commitment" && m.LastRetrievedAt != nil && *m.LastRetrievedAt != "" {
			if lastRetr, err := time.Parse(time.RFC3339Nano, *m.LastRetrievedAt); err == nil {
				hoursSinceRetrieval := now.Sub(lastRetr).Hours()
				if hoursSinceRetrieval < 1.0 {
					diversityPenalty = 0.15 * (1.0 - hoursSinceRetrieval) // fades over 1 hour
				}
			}
		}

		combined := keywordScore*0.5 + importance*0.3 + recencyScore*0.2 - diversityPenalty
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
	ID             string
	ConversationID string
	Provider       string
	Model          string
	InputTokens    int
	OutputTokens   int
	InputCostUSD   float64
	OutputCostUSD  float64
	TotalCostUSD   float64
	RecordedAt     string
}

// ModelUsageBreakdown aggregates usage for one provider+model combination.
type ModelUsageBreakdown struct {
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	TotalCostUSD float64
	TurnCount    int64
}

// DailyUsage aggregates usage for one calendar day.
type DailyUsage struct {
	Date         string // "2025-04-03"
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	TurnCount    int64
}

// TokenUsageSummary is the full aggregated response.
type TokenUsageSummary struct {
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalTokens       int64
	TotalCostUSD      float64
	TurnCount         int64
	ByModel           []ModelUsageBreakdown
	DailySeries       []DailyUsage
}

// RecordTokenUsage persists one token usage event.
func (db *DB) RecordTokenUsage(id, convID, provider, model string,
	inputTokens, outputTokens int,
	inputCost, outputCost float64,
	recordedAt string,
) error {
	total := inputCost + outputCost
	_, err := db.conn.Exec(`
		INSERT INTO token_usage
			(id, conversation_id, provider, model,
			 input_tokens, output_tokens,
			 input_cost_usd, output_cost_usd, total_cost_usd, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, convID, provider, model,
		inputTokens, outputTokens,
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
		input_tokens, output_tokens,
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
			&r.InputTokens, &r.OutputTokens,
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
		"SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), "+
			"COALESCE(SUM(total_cost_usd),0), COUNT(*) FROM token_usage "+where,
		args...)
	if err := row.Scan(&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCostUSD, &s.TurnCount); err != nil {
		return s, err
	}
	s.TotalTokens = s.TotalInputTokens + s.TotalOutputTokens

	// ── Per-model breakdown ───────────────────────────────────────────────────
	mrows, err := db.conn.Query(
		"SELECT provider, model, "+
			"COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), "+
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
		if err := mrows.Scan(&m.Provider, &m.Model, &m.InputTokens, &m.OutputTokens, &m.TotalCostUSD, &m.TurnCount); err != nil {
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
				"COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), "+
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
			if err := drows.Scan(&d.Date, &d.InputTokens, &d.OutputTokens, &d.CostUSD, &d.TurnCount); err != nil {
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
		  AND provider NOT IN ('lm_studio', 'ollama', 'atlas_engine')
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
