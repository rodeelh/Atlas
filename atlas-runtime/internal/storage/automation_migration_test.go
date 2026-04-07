package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAutomationMigrationAddsScheduleJSONToExistingTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.sqlite3")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = legacy.Exec(`CREATE TABLE automations (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		emoji TEXT NOT NULL DEFAULT '⚡',
		prompt TEXT NOT NULL,
		schedule_raw TEXT NOT NULL,
		is_enabled INTEGER NOT NULL DEFAULT 1,
		source_type TEXT NOT NULL DEFAULT 'manual',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		legacy.Close()
		t.Fatalf("create legacy automations table: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer db.Close()

	if err := db.SaveAutomation(AutomationRow{
		ID:          "migrated",
		Name:        "Migrated",
		Emoji:       "⚡",
		Prompt:      "Prompt",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-07",
		UpdatedAt:   "2026-04-07T09:00:00Z",
		TagsJSON:    "[]",
	}); err != nil {
		t.Fatalf("SaveAutomation after migration: %v", err)
	}
	rows, err := db.ListAutomations()
	if err != nil {
		t.Fatalf("ListAutomations after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "migrated" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
