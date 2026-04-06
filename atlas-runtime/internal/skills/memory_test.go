package skills

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"atlas-runtime-go/internal/storage"
)

// openTestDBForSkills opens a fresh SQLite DB in a temp directory.
func openTestDBForSkills(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "skills_test.sqlite3"))
	if err != nil {
		t.Fatalf("openTestDBForSkills: %v", err)
	}
	return db, func() { db.Close() }
}

// newMemoryRegistry creates a Registry with only the memory skill registered.
func newMemoryRegistry(t *testing.T, db *storage.DB) *Registry {
	t.Helper()
	r := &Registry{
		entries: make(map[string]SkillEntry),
		db:      db,
	}
	r.registerMemory()
	return r
}

// TestMemorySaveAction — call memory.save with valid args, verify row in DB.
func TestMemorySaveAction(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{
		"category":   "preference",
		"title":      "Preferred language",
		"content":    "User prefers Go for all backend work.",
		"importance": 0.85
	}`)

	result, err := r.Execute(context.Background(), "memory.save", args)
	if err != nil {
		t.Fatalf("Execute memory.save: %v", err)
	}
	if !result.Success {
		t.Fatalf("memory.save returned failure: %s", result.Summary)
	}

	// Verify row in DB.
	mems, err := db.ListMemories(100, "preference")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	found := false
	for _, m := range mems {
		if m.Title == "Preferred language" {
			found = true
			if m.Content != "User prefers Go for all backend work." {
				t.Errorf("Content: want %q, got %q", "User prefers Go for all backend work.", m.Content)
			}
			if math.Abs(m.Confidence-0.90) > 1e-6 {
				t.Errorf("Confidence: want 0.90 (model_explicit default), got %f", m.Confidence)
			}
			if math.Abs(m.Importance-0.85) > 1e-6 {
				t.Errorf("Importance: want 0.85, got %f", m.Importance)
			}
			if m.Source != "model_explicit" {
				t.Errorf("Source: want model_explicit, got %q", m.Source)
			}
		}
	}
	if !found {
		t.Error("memory.save: memory not found in DB after save")
	}
}

// TestMemorySaveReinforcement — save same title twice, verify confidence increases.
func TestMemorySaveReinforcement(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{
		"category":   "preference",
		"title":      "Preferred editor",
		"content":    "User prefers VSCode.",
		"importance": 0.75
	}`)

	// First save.
	if _, err := r.Execute(context.Background(), "memory.save", args); err != nil {
		t.Fatalf("first Execute memory.save: %v", err)
	}

	// Check initial confidence.
	initial, err := db.FindDuplicateMemory("preference", "Preferred editor")
	if err != nil || initial == nil {
		t.Fatalf("FindDuplicateMemory after first save: %v", err)
	}
	if math.Abs(initial.Confidence-0.90) > 1e-6 {
		t.Errorf("Initial confidence: want 0.90, got %f", initial.Confidence)
	}

	// Second save with same title — should reinforce.
	args2 := json.RawMessage(`{
		"category":   "preference",
		"title":      "Preferred editor",
		"content":    "User strongly prefers VSCode with Go extension.",
		"importance": 0.80
	}`)
	if _, err := r.Execute(context.Background(), "memory.save", args2); err != nil {
		t.Fatalf("second Execute memory.save: %v", err)
	}

	after, err := db.FindDuplicateMemory("preference", "Preferred editor")
	if err != nil || after == nil {
		t.Fatalf("FindDuplicateMemory after second save: %v", err)
	}
	// Expected: min(0.90 + 0.20, 1.0) = 1.0
	expectedConf := math.Min(0.90+0.20, 1.0)
	if math.Abs(after.Confidence-expectedConf) > 1e-6 {
		t.Errorf("Confidence after reinforce: want %.2f, got %f", expectedConf, after.Confidence)
	}
	// Content should be updated.
	if after.Content != "User strongly prefers VSCode with Go extension." {
		t.Errorf("Content not updated on reinforce: %q", after.Content)
	}
}

// TestMemoryRecall — save a memory, recall it by query.
func TestMemoryRecall(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	// Save a memory directly in DB.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := storage.MemoryRow{
		ID:         "recall-test-1",
		Category:   "project",
		Title:      "Atlas project",
		Content:    "Atlas is a macOS AI operator project.",
		Source:     "test",
		Confidence: 0.90,
		Importance: 0.85,
		CreatedAt:  now,
		UpdatedAt:  now,
		TagsJSON:   `["atlas","macos"]`,
	}
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	args := json.RawMessage(`{"query": "Atlas macOS project"}`)
	result, err := r.Execute(context.Background(), "memory.recall", args)
	if err != nil {
		t.Fatalf("Execute memory.recall: %v", err)
	}
	if !result.Success {
		t.Fatalf("memory.recall failed: %s", result.Summary)
	}
	if result.Summary == "" {
		t.Error("memory.recall: expected non-empty summary")
	}
	// Result should mention the memory content.
	if result.Summary == "No relevant memories found for: Atlas macOS project" {
		t.Error("memory.recall: expected to find memory, got 'no relevant memories'")
	}
}

// TestMemoryRecallEmpty — recall on empty DB returns "no relevant memories" message.
func TestMemoryRecallEmpty(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{"query": "something that does not exist"}`)
	result, err := r.Execute(context.Background(), "memory.recall", args)
	if err != nil {
		t.Fatalf("Execute memory.recall empty: %v", err)
	}
	if !result.Success {
		t.Errorf("memory.recall on empty DB should succeed, got: %s", result.Summary)
	}
}

// TestMemorySaveInvalidCategory — pass unknown category, expect error.
func TestMemorySaveInvalidCategory(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{
		"category":   "forbidden_category",
		"title":      "Some Title",
		"content":    "Some content.",
		"importance": 0.70
	}`)

	result, err := r.Execute(context.Background(), "memory.save", args)
	// Should either return an error or a failure result.
	if err == nil && result.Success {
		t.Error("memory.save: expected failure for invalid category, but succeeded")
	}
}

// TestMemorySaveEmptyFields — missing required fields, expect error.
func TestMemorySaveEmptyFields(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	cases := []struct {
		name string
		args string
	}{
		{
			name: "missing category",
			args: `{"title": "Title", "content": "Content", "importance": 0.7}`,
		},
		{
			name: "missing title",
			args: `{"category": "preference", "content": "Content", "importance": 0.7}`,
		},
		{
			name: "missing content",
			args: `{"category": "preference", "title": "Title", "importance": 0.7}`,
		},
		{
			name: "empty category",
			args: `{"category": "", "title": "Title", "content": "Content", "importance": 0.7}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := r.Execute(context.Background(), "memory.save", json.RawMessage(tc.args))
			if err == nil && result.Success {
				t.Errorf("memory.save %s: expected failure for empty required field, but succeeded", tc.name)
			}
		})
	}
}

// TestMemorySaveCommitmentSetsUserConfirmed — commitment category sets IsUserConfirmed.
func TestMemorySaveCommitmentSetsUserConfirmed(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{
		"category":   "commitment",
		"title":      "Always ask before deleting",
		"content":    "Always ask user before deleting any files.",
		"importance": 0.99
	}`)

	if _, err := r.Execute(context.Background(), "memory.save", args); err != nil {
		t.Fatalf("Execute memory.save commitment: %v", err)
	}

	found, err := db.FindDuplicateMemory("commitment", "Always ask before deleting")
	if err != nil || found == nil {
		t.Fatalf("FindDuplicateMemory: %v, %v", err, found)
	}
	if !found.IsUserConfirmed {
		t.Error("commitment memory should have IsUserConfirmed=true")
	}
}

// TestMemorySaveImportanceClamp — importance values outside [0,1] are clamped.
func TestMemorySaveImportanceClamp(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	// Importance > 1.0 should be clamped to 1.0.
	args := json.RawMessage(`{
		"category":   "preference",
		"title":      "Clamp test",
		"content":    "Should be clamped.",
		"importance": 5.0
	}`)

	if _, err := r.Execute(context.Background(), "memory.save", args); err != nil {
		t.Fatalf("Execute memory.save: %v", err)
	}

	found, err := db.FindDuplicateMemory("preference", "Clamp test")
	if err != nil || found == nil {
		t.Fatalf("FindDuplicateMemory: %v", err)
	}
	if found.Importance > 1.0 {
		t.Errorf("Importance not clamped: got %f (should be <= 1.0)", found.Importance)
	}
}

// TestMemoryRecallEmptyQuery — empty query should return error.
func TestMemoryRecallEmptyQuery(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	args := json.RawMessage(`{"query": ""}`)
	result, err := r.Execute(context.Background(), "memory.recall", args)
	if err == nil && result.Success {
		t.Error("memory.recall: expected failure for empty query, but succeeded")
	}
}

// TestMemorySaveBothSaveAndRecallRegistered — verify both actions exist in registry.
func TestMemorySaveBothActionsRegistered(t *testing.T) {
	db, cleanup := openTestDBForSkills(t)
	defer cleanup()
	r := newMemoryRegistry(t, db)

	_, hasSave := r.entries["memory.save"]
	_, hasRecall := r.entries["memory.recall"]
	if !hasSave {
		t.Error("memory.save not registered")
	}
	if !hasRecall {
		t.Error("memory.recall not registered")
	}
}
