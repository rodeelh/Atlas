package storage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// openMemTestDB opens a fresh SQLite DB in a temp directory and returns it with
// a cleanup function. Never touches production data.
// Named differently from usage_test.go's openTestDB to avoid redeclaration.
func openMemTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "mem_test.sqlite3"))
	if err != nil {
		t.Fatalf("openMemTestDB: %v", err)
	}
	return db, func() { db.Close() }
}

func testMemoryRow(id, category, title, content string, importance, confidence float64) MemoryRow {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return MemoryRow{
		ID:         id,
		Category:   category,
		Title:      title,
		Content:    content,
		Source:     "test",
		Confidence: confidence,
		Importance: importance,
		CreatedAt:  now,
		UpdatedAt:  now,
		TagsJSON:   `["test"]`,
	}
}

// TestSaveAndRetrieveMemory — save a memory, retrieve it by ID, verify all fields.
func TestSaveAndRetrieveMemory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("mem-001", "profile", "Test Title", "Test content.", 0.80, 0.90)
	row.IsUserConfirmed = true

	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	got, err := db.FetchMemory("mem-001")
	if err != nil {
		t.Fatalf("FetchMemory: %v", err)
	}
	if got == nil {
		t.Fatal("FetchMemory: returned nil, expected row")
	}
	if got.Category != "profile" {
		t.Errorf("Category: want %q, got %q", "profile", got.Category)
	}
	if got.Title != "Test Title" {
		t.Errorf("Title: want %q, got %q", "Test Title", got.Title)
	}
	if got.Content != "Test content." {
		t.Errorf("Content: want %q, got %q", "Test content.", got.Content)
	}
	if math.Abs(got.Importance-0.80) > 1e-6 {
		t.Errorf("Importance: want 0.80, got %f", got.Importance)
	}
	if math.Abs(got.Confidence-0.90) > 1e-6 {
		t.Errorf("Confidence: want 0.90, got %f", got.Confidence)
	}
	if !got.IsUserConfirmed {
		t.Error("IsUserConfirmed: expected true")
	}
}

// TestValidUntilFiltering — set valid_until in the past, confirm excluded from
// ListMemories and RelevantMemories.
func TestValidUntilFiltering(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	// Save an active memory.
	active := testMemoryRow("active-1", "profile", "Active Memory", "I am active.", 0.80, 0.85)
	if err := db.SaveMemory(active); err != nil {
		t.Fatalf("SaveMemory active: %v", err)
	}

	// Save a memory that we will expire.
	expired := testMemoryRow("expired-1", "profile", "Expired Memory", "I am expired.", 0.80, 0.85)
	if err := db.SaveMemory(expired); err != nil {
		t.Fatalf("SaveMemory expired: %v", err)
	}

	// Set valid_until to one second ago.
	past := time.Now().UTC().Add(-1 * time.Second).Format(time.RFC3339Nano)
	if err := db.SetValidUntil("expired-1", past); err != nil {
		t.Fatalf("SetValidUntil: %v", err)
	}

	// ListMemories should only return the active one.
	mems, err := db.ListMemories(100, "")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	for _, m := range mems {
		if m.ID == "expired-1" {
			t.Error("ListMemories: returned expired memory (should be excluded)")
		}
	}
	found := false
	for _, m := range mems {
		if m.ID == "active-1" {
			found = true
		}
	}
	if !found {
		t.Error("ListMemories: did not return active memory")
	}

	// RelevantMemories should also exclude it.
	relevant, err := db.RelevantMemories("active expired memory", 10)
	if err != nil {
		t.Fatalf("RelevantMemories: %v", err)
	}
	for _, m := range relevant {
		if m.ID == "expired-1" {
			t.Error("RelevantMemories: returned expired memory (should be excluded)")
		}
	}
}

// TestFTS5Search — save memories with searchable content, verify ftsSearch returns them.
func TestFTS5Search(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	rows := []MemoryRow{
		testMemoryRow("fts-1", "project", "Telescope project", "User is building a telescope control app.", 0.80, 0.80),
		testMemoryRow("fts-2", "preference", "Response style", "User prefers concise answers.", 0.75, 0.90),
		testMemoryRow("fts-3", "profile", "Location", "User is based in London.", 0.70, 0.95),
	}
	for _, r := range rows {
		if err := db.SaveMemory(r); err != nil {
			t.Fatalf("SaveMemory %s: %v", r.ID, err)
		}
	}

	// FTS search for "telescope".
	results, err := db.ftsSearch("telescope", 10)
	if err != nil {
		t.Fatalf("ftsSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("ftsSearch: expected at least 1 result for 'telescope', got 0")
	}
	found := false
	for _, m := range results {
		if m.ID == "fts-1" {
			found = true
		}
	}
	if !found {
		t.Error("ftsSearch: did not return expected memory fts-1")
	}

	// FTS search for "concise".
	results2, err := db.ftsSearch("concise", 10)
	if err != nil {
		t.Fatalf("ftsSearch concise: %v", err)
	}
	found2 := false
	for _, m := range results2 {
		if m.ID == "fts-2" {
			found2 = true
		}
	}
	if !found2 {
		t.Error("ftsSearch concise: did not return expected memory fts-2")
	}
}

// TestRelevantMemoriesCommitmentBoost — commitment category gets boosted in scoring
// so it surfaces ahead of equal-importance non-commitment memories.
func TestRelevantMemoriesCommitmentBoost(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	// Same importance, but one is commitment.
	commitment := testMemoryRow("c-1", "commitment", "Always use metric", "Always use metric units.", 0.80, 0.99)
	commitment.IsUserConfirmed = true
	preference := testMemoryRow("p-1", "preference", "Response length", "User prefers short responses.", 0.80, 0.90)

	if err := db.SaveMemory(commitment); err != nil {
		t.Fatalf("SaveMemory commitment: %v", err)
	}
	if err := db.SaveMemory(preference); err != nil {
		t.Fatalf("SaveMemory preference: %v", err)
	}

	results, err := db.RelevantMemories("metric units", 5)
	if err != nil {
		t.Fatalf("RelevantMemories: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("RelevantMemories: expected results, got 0")
	}
	// Commitment should rank first or at least be present.
	found := false
	for _, m := range results {
		if m.ID == "c-1" {
			found = true
		}
	}
	if !found {
		t.Error("RelevantMemories: commitment memory not included in results")
	}
	// First result should be the commitment (boosted).
	if results[0].ID != "c-1" {
		t.Errorf("RelevantMemories: expected commitment first, got %q (category=%q)", results[0].ID, results[0].Category)
	}
}

// TestSetValidUntil — mark memory invalid, verify excluded.
func TestSetValidUntil(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("sv-1", "project", "Old project", "This project is done.", 0.70, 0.80)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Verify it's present first.
	mems, err := db.ListMemories(100, "")
	if err != nil {
		t.Fatalf("ListMemories before: %v", err)
	}
	foundBefore := false
	for _, m := range mems {
		if m.ID == "sv-1" {
			foundBefore = true
		}
	}
	if !foundBefore {
		t.Fatal("memory should be present before invalidation")
	}

	// Invalidate it.
	until := time.Now().UTC().Add(-1 * time.Second).Format(time.RFC3339)
	if err := db.SetValidUntil("sv-1", until); err != nil {
		t.Fatalf("SetValidUntil: %v", err)
	}

	// Should no longer appear in ListMemories.
	mems2, err := db.ListMemories(100, "")
	if err != nil {
		t.Fatalf("ListMemories after: %v", err)
	}
	for _, m := range mems2 {
		if m.ID == "sv-1" {
			t.Error("SetValidUntil: memory still present after invalidation")
		}
	}

	// FetchMemory still returns it (for historical record).
	fetched, err := db.FetchMemory("sv-1")
	if err != nil {
		t.Fatalf("FetchMemory after: %v", err)
	}
	if fetched == nil {
		t.Error("FetchMemory: memory should still be fetchable after invalidation (historical record)")
	}
}

// TestFindDuplicateMemory — save memory, find duplicate by category+title.
func TestFindDuplicateMemory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("dup-1", "preference", "Preferred language", "User prefers Go.", 0.80, 0.90)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Should find it.
	found, err := db.FindDuplicateMemory("preference", "Preferred language")
	if err != nil {
		t.Fatalf("FindDuplicateMemory: %v", err)
	}
	if found == nil {
		t.Fatal("FindDuplicateMemory: expected to find duplicate, got nil")
	}
	if found.ID != "dup-1" {
		t.Errorf("FindDuplicateMemory: expected ID dup-1, got %q", found.ID)
	}

	// Different category — should NOT find it.
	notFound, err := db.FindDuplicateMemory("project", "Preferred language")
	if err != nil {
		t.Fatalf("FindDuplicateMemory (wrong category): %v", err)
	}
	if notFound != nil {
		t.Error("FindDuplicateMemory: should NOT find by mismatched category")
	}

	// Different title — should NOT find it.
	notFound2, err := db.FindDuplicateMemory("preference", "Unrelated Title")
	if err != nil {
		t.Fatalf("FindDuplicateMemory (wrong title): %v", err)
	}
	if notFound2 != nil {
		t.Error("FindDuplicateMemory: should NOT find by mismatched title")
	}
}

// TestOpinionReinforcement — confidence starts at 0.90, reinforce to min(0.90+0.20, 1.0).
func TestOpinionReinforcement(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("op-1", "preference", "Preferred framework", "User prefers React.", 0.80, 0.90)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Simulate reinforcement: fetch, add alpha, update.
	existing, err := db.FindDuplicateMemory("preference", "Preferred framework")
	if err != nil || existing == nil {
		t.Fatalf("FindDuplicateMemory: %v, %v", err, existing)
	}

	const reinforcementAlpha = 0.20
	newConfidence := math.Min(existing.Confidence+reinforcementAlpha, 1.0)
	upd := *existing
	upd.Confidence = newConfidence
	now := time.Now().UTC().Format(time.RFC3339Nano)
	upd.UpdatedAt = now

	if err := db.UpdateMemory(upd); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	after, err := db.FetchMemory("op-1")
	if err != nil || after == nil {
		t.Fatalf("FetchMemory after update: %v", err)
	}
	// 0.90 + 0.20 = 1.0 (capped).
	expected := math.Min(0.90+reinforcementAlpha, 1.0)
	if math.Abs(after.Confidence-expected) > 1e-6 {
		t.Errorf("Confidence after reinforce: want %.2f, got %.6f", expected, after.Confidence)
	}
}

// TestOpinionReinforcementCap — reinforce a memory already at 0.90, result is 1.0, not 1.10.
func TestOpinionReinforcementCap(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("op-cap-1", "commitment", "Never delete", "Never delete user files.", 0.99, 0.90)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	existing, err := db.FindDuplicateMemory("commitment", "Never delete")
	if err != nil || existing == nil {
		t.Fatalf("FindDuplicateMemory: %v", err)
	}

	newConf := math.Min(existing.Confidence+0.20, 1.0)
	upd := *existing
	upd.Confidence = newConf
	upd.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.UpdateMemory(upd); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	after, err := db.FetchMemory("op-cap-1")
	if err != nil || after == nil {
		t.Fatalf("FetchMemory: %v", err)
	}
	if after.Confidence > 1.0 {
		t.Errorf("Confidence should not exceed 1.0, got %f", after.Confidence)
	}
}

// TestRelevantMemoriesEmptyDB — empty DB should return nil/empty, not error.
func TestRelevantMemoriesEmptyDB(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	results, err := db.RelevantMemories("anything at all", 5)
	if err != nil {
		t.Fatalf("RelevantMemories on empty DB: unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on empty DB, got %d", len(results))
	}
}

// TestConcurrentSaveMemory — race test: concurrent SaveMemory calls should not panic or corrupt.
func TestConcurrentSaveMemory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			now := time.Now().UTC().Format(time.RFC3339Nano)
			row := MemoryRow{
				ID:         fmt.Sprintf("race-%03d", idx),
				Category:   "project",
				Title:      fmt.Sprintf("Race test memory %d", idx),
				Content:    fmt.Sprintf("Content for memory %d.", idx),
				Source:     "test",
				Confidence: 0.80,
				Importance: 0.70,
				CreatedAt:  now,
				UpdatedAt:  now,
				TagsJSON:   `[]`,
			}
			if err := db.SaveMemory(row); err != nil {
				errs <- fmt.Errorf("worker %d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	count := db.CountMemories()
	if count != workers {
		t.Errorf("expected %d memories after concurrent saves, got %d", workers, count)
	}
}

// TestListMemoriesActiveFilter — verify category filter passes through AND valid_until is respected.
func TestListMemoriesActiveFilter(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339Nano)

	rows := []MemoryRow{
		{ID: "lm-1", Category: "preference", Title: "Pref A", Content: "A", Source: "test", Confidence: 0.8, Importance: 0.7, CreatedAt: now, UpdatedAt: now, TagsJSON: "[]"},
		{ID: "lm-2", Category: "preference", Title: "Pref B", Content: "B", Source: "test", Confidence: 0.8, Importance: 0.7, CreatedAt: now, UpdatedAt: now, TagsJSON: "[]"},
		{ID: "lm-3", Category: "profile", Title: "Prof A", Content: "C", Source: "test", Confidence: 0.8, Importance: 0.7, CreatedAt: now, UpdatedAt: now, TagsJSON: "[]"},
	}
	for _, r := range rows {
		if err := db.SaveMemory(r); err != nil {
			t.Fatalf("SaveMemory %s: %v", r.ID, err)
		}
	}

	// Expire lm-2.
	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if err := db.SetValidUntil("lm-2", past); err != nil {
		t.Fatalf("SetValidUntil: %v", err)
	}

	// Filter by category "preference" — should only get lm-1.
	prefs, err := db.ListMemories(100, "preference")
	if err != nil {
		t.Fatalf("ListMemories preference: %v", err)
	}
	for _, m := range prefs {
		if m.ID == "lm-2" {
			t.Error("ListMemories: returned expired memory lm-2")
		}
		if m.ID == "lm-3" {
			t.Error("ListMemories: returned wrong-category memory lm-3")
		}
	}
	if len(prefs) != 1 || prefs[0].ID != "lm-1" {
		t.Errorf("ListMemories: expected [lm-1], got IDs: %v", func() []string {
			var ids []string
			for _, m := range prefs {
				ids = append(ids, m.ID)
			}
			return ids
		}())
	}
}

// TestFTS5SearchExcludesExpired — expired memories must NOT appear in ftsSearch results.
func TestFTS5SearchExcludesExpired(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	active := MemoryRow{ID: "fts-active", Category: "project", Title: "Active telescope", Content: "Telescope project ongoing.", Source: "test", Confidence: 0.8, Importance: 0.8, CreatedAt: now, UpdatedAt: now, TagsJSON: "[]"}
	expired := MemoryRow{ID: "fts-expired", Category: "project", Title: "Expired telescope", Content: "Old telescope notes.", Source: "test", Confidence: 0.8, Importance: 0.8, CreatedAt: now, UpdatedAt: now, TagsJSON: "[]"}

	for _, r := range []MemoryRow{active, expired} {
		if err := db.SaveMemory(r); err != nil {
			t.Fatalf("SaveMemory %s: %v", r.ID, err)
		}
	}

	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if err := db.SetValidUntil("fts-expired", past); err != nil {
		t.Fatalf("SetValidUntil: %v", err)
	}

	results, err := db.ftsSearch("telescope", 10)
	if err != nil {
		t.Fatalf("ftsSearch: %v", err)
	}
	for _, m := range results {
		if m.ID == "fts-expired" {
			t.Error("ftsSearch: returned expired memory")
		}
	}
}

// TestFetchMemoryNotFound — FetchMemory returns nil for unknown ID.
func TestFetchMemoryNotFound(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	got, err := db.FetchMemory("nonexistent-id")
	if err != nil {
		t.Fatalf("FetchMemory nonexistent: %v", err)
	}
	if got != nil {
		t.Errorf("FetchMemory: expected nil for nonexistent ID, got %+v", got)
	}
}

// TestUpdateMemory — update mutable fields, verify persisted.
func TestUpdateMemory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("upd-1", "workflow", "Dev workflow", "User uses VSCode.", 0.70, 0.75)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	existing, err := db.FetchMemory("upd-1")
	if err != nil || existing == nil {
		t.Fatalf("FetchMemory: %v", err)
	}

	upd := *existing
	upd.Content = "User uses Neovim."
	upd.Confidence = 0.95
	upd.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	if err := db.UpdateMemory(upd); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	after, err := db.FetchMemory("upd-1")
	if err != nil || after == nil {
		t.Fatalf("FetchMemory after update: %v", err)
	}
	if after.Content != "User uses Neovim." {
		t.Errorf("Content not updated: got %q", after.Content)
	}
	if math.Abs(after.Confidence-0.95) > 1e-6 {
		t.Errorf("Confidence not updated: got %f", after.Confidence)
	}
}

// TestDeleteMemory — delete a memory, verify removed.
func TestDeleteMemory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("del-1", "episodic", "Old event", "This happened.", 0.50, 0.60)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	if err := db.DeleteMemory("del-1"); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	got, err := db.FetchMemory("del-1")
	if err != nil {
		t.Fatalf("FetchMemory after delete: %v", err)
	}
	if got != nil {
		t.Error("DeleteMemory: memory still present after deletion")
	}
}

// TestExtractKeywords — stop words are stripped, short words filtered.
func TestExtractKeywords(t *testing.T) {
	keywords := extractKeywords("what is the weather today in London")
	for _, kw := range keywords {
		if len(kw) < 2 {
			t.Errorf("keyword %q is too short (< 2 chars)", kw)
		}
	}
	// "London" and "weather" should survive.
	found := map[string]bool{}
	for _, kw := range keywords {
		found[kw] = true
	}
	if !found["london"] {
		t.Error("expected 'london' in keywords")
	}
	if !found["weather"] {
		t.Error("expected 'weather' in keywords")
	}
	// Stop words should not be present.
	for _, sw := range []string{"what", "is", "the", "in"} {
		if found[sw] {
			t.Errorf("stop word %q should not appear in keywords", sw)
		}
	}
}

// TestRelevantMemoriesNoKeywords — when query has only stop words, falls back to
// importance-ordered active memories.
func TestRelevantMemoriesNoKeywords(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("nk-1", "preference", "Stop word test", "Some preference.", 0.80, 0.90)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// "a the is" — all stop words → extractKeywords returns []
	results, err := db.RelevantMemories("a the is", 5)
	if err != nil {
		t.Fatalf("RelevantMemories no-keywords: %v", err)
	}
	// Should still return the memory via fallback.
	if len(results) == 0 {
		t.Error("RelevantMemories no-keywords: expected fallback to return memories")
	}
}

// TestFTS5TriggerDeleteSync — deleting a memory removes it from FTS index.
func TestFTS5TriggerDeleteSync(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("td-1", "project", "FTS delete test", "Unique word xylophone here.", 0.80, 0.85)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// Verify FTS finds it before delete.
	before, err := db.ftsSearch("xylophone", 5)
	if err != nil {
		t.Fatalf("ftsSearch before delete: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("ftsSearch: expected to find memory before delete")
	}

	if err := db.DeleteMemory("td-1"); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}

	// FTS should no longer find it.
	after, err := db.ftsSearch("xylophone", 5)
	if err != nil {
		t.Fatalf("ftsSearch after delete: %v", err)
	}
	for _, m := range after {
		if m.ID == "td-1" {
			t.Error("FTS5 trigger did not remove deleted memory from index")
		}
	}
}

// TestOpenInvalidPath — Open on an unwritable path returns an error, not a panic.
func TestOpenInvalidPath(t *testing.T) {
	_, err := Open("/nonexistent-dir/atlas-test.sqlite3")
	if err == nil {
		t.Error("expected error opening DB in nonexistent directory, got nil")
	}
}

// TestCountMemories — CountMemories reflects actual row count.
func TestCountMemories(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	if n := db.CountMemories(); n != 0 {
		t.Errorf("expected 0 initially, got %d", n)
	}

	for i := 0; i < 5; i++ {
		row := testMemoryRow(fmt.Sprintf("cnt-%d", i), "episodic", fmt.Sprintf("Event %d", i), "Content.", 0.70, 0.60)
		if err := db.SaveMemory(row); err != nil {
			t.Fatalf("SaveMemory: %v", err)
		}
	}

	if n := db.CountMemories(); n != 5 {
		t.Errorf("expected 5 after 5 inserts, got %d", n)
	}
}

// TestSetValidUntilPreservesHistory — after invalidation, FetchMemory still works
// and ValidUntil field is non-nil.
func TestSetValidUntilPreservesHistory(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("hist-1", "project", "Historical", "Old fact.", 0.70, 0.75)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	until := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if err := db.SetValidUntil("hist-1", until); err != nil {
		t.Fatalf("SetValidUntil: %v", err)
	}

	got, err := db.FetchMemory("hist-1")
	if err != nil {
		t.Fatalf("FetchMemory: %v", err)
	}
	if got == nil {
		t.Fatal("FetchMemory should return row for historical record")
	}
	if got.ValidUntil == nil {
		t.Error("ValidUntil should be non-nil after SetValidUntil")
	}
	if *got.ValidUntil != until {
		t.Errorf("ValidUntil: want %q, got %q", until, *got.ValidUntil)
	}
}

// Ensure the test file passes vet even if os is used only for temp dir.
var _ = os.TempDir
