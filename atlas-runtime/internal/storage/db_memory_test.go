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
	relevant, err := db.RelevantMemories("active expired memory", 10, nil)
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

	results, err := db.RelevantMemories("metric units", 5, nil)
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

	results, err := db.RelevantMemories("anything at all", 5, nil)
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
	results, err := db.RelevantMemories("a the is", 5, nil)
	if err != nil {
		t.Fatalf("RelevantMemories no-keywords: %v", err)
	}
	// Should still return the memory via fallback.
	if len(results) == 0 {
		t.Error("RelevantMemories no-keywords: expected fallback to return memories")
	}
}

// TestNormalizeBM25 — unit tests for the BM25 normalization helper.
func TestNormalizeBM25(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if normalizeBM25(nil) != nil {
			t.Error("expected nil for nil input")
		}
	})
	t.Run("empty input returns nil", func(t *testing.T) {
		if normalizeBM25(map[string]float64{}) != nil {
			t.Error("expected nil for empty input")
		}
	})
	t.Run("single entry gets 0.5", func(t *testing.T) {
		out := normalizeBM25(map[string]float64{"a": -1.5})
		if out["a"] != 0.5 {
			t.Errorf("single entry: expected 0.5, got %v", out["a"])
		}
	})
	t.Run("best match gets 1.0, worst gets 0.0", func(t *testing.T) {
		// FTS5 rank: more negative = better
		ranks := map[string]float64{"best": -3.0, "mid": -2.0, "worst": -1.0}
		out := normalizeBM25(ranks)
		if out["best"] != 1.0 {
			t.Errorf("best: expected 1.0, got %v", out["best"])
		}
		if out["worst"] != 0.0 {
			t.Errorf("worst: expected 0.0, got %v", out["worst"])
		}
		if out["mid"] <= 0.0 || out["mid"] >= 1.0 {
			t.Errorf("mid: expected (0,1), got %v", out["mid"])
		}
	})
}

// TestRelevantMemoriesBM25Ranking — BM25 ranks higher-relevance memories first.
func TestRelevantMemoriesBM25Ranking(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	// Two memories: one mentions "golang" prominently, one barely.
	prominent := testMemoryRow("bm25-1", "project", "Golang expertise", "Prefers golang for backend services and uses golang daily.", 0.7, 0.7)
	weak := testMemoryRow("bm25-2", "project", "Frontend work", "Occasionally uses golang but mainly writes TypeScript.", 0.7, 0.7)
	for _, r := range []MemoryRow{prominent, weak} {
		if err := db.SaveMemory(r); err != nil {
			t.Fatalf("SaveMemory: %v", err)
		}
	}

	results, err := db.RelevantMemories("golang", 5, nil)
	if err != nil {
		t.Fatalf("RelevantMemories: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != "bm25-1" {
		t.Errorf("expected prominent golang memory first, got %q", results[0].ID)
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

// ── Embedding tests ───────────────────────────────────────────────────────────

// TestUpdateMemoryEmbedding — save a memory, write an embedding, verify it round-trips.
func TestUpdateMemoryEmbedding(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("emb-1", "profile", "Embed Title", "Embed content.", 0.8, 0.9)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	vec := []float32{0.1, 0.2, 0.3, 0.4}
	if err := db.UpdateMemoryEmbedding("emb-1", "test-model", vec); err != nil {
		t.Fatalf("UpdateMemoryEmbedding: %v", err)
	}

	// fetchEmbeddings is internal; verify via RelevantMemories with a matching vector.
	results, err := db.RelevantMemories("Embed content", 5, vec)
	if err != nil {
		t.Fatalf("RelevantMemories: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == "emb-1" {
			found = true
		}
	}
	if !found {
		t.Error("embedded memory emb-1 should appear in hybrid recall results")
	}
}

// TestRelevantMemoriesHybridScoring — the memory with an exact-match embedding
// should rank first over a keyword-only match when queryVec is provided.
func TestRelevantMemoriesHybridScoring(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	// mem-A: has a high-cosine embedding relative to our query vector.
	memA := testMemoryRow("hsc-a", "profile", "Semantic match", "semantic content here", 0.5, 0.5)
	// mem-B: has a low-importance score and no embedding.
	memB := testMemoryRow("hsc-b", "profile", "Keyword match", "semantic content here", 0.5, 0.5)

	if err := db.SaveMemory(memA); err != nil {
		t.Fatalf("SaveMemory A: %v", err)
	}
	if err := db.SaveMemory(memB); err != nil {
		t.Fatalf("SaveMemory B: %v", err)
	}

	// Give mem-A a near-unit embedding along axis 0.
	vecA := []float32{1.0, 0.0, 0.0, 0.0}
	if err := db.UpdateMemoryEmbedding("hsc-a", "test-model", vecA); err != nil {
		t.Fatalf("UpdateMemoryEmbedding: %v", err)
	}

	// Query vector also points along axis 0 → mem-A gets cosine ≈ 1.0.
	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	results, err := db.RelevantMemories("semantic content", 5, queryVec)
	if err != nil {
		t.Fatalf("RelevantMemories: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].ID != "hsc-a" {
		t.Errorf("expected hsc-a (high cosine) to rank first, got %s", results[0].ID)
	}
}

// TestUpdateMemoryClearsEmbeddingAt — UpdateMemory must null embedding_at so stale
// vectors are re-generated on the next extraction cycle.
func TestUpdateMemoryClearsEmbeddingAt(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	row := testMemoryRow("clr-1", "preference", "Old title", "Old content.", 0.7, 0.8)
	if err := db.SaveMemory(row); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if err := db.UpdateMemoryEmbedding("clr-1", "test-model", []float32{0.5, 0.5}); err != nil {
		t.Fatalf("UpdateMemoryEmbedding: %v", err)
	}

	// Now update the content — embedding_at should be cleared.
	row.Title = "New title"
	row.Content = "New content."
	row.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.UpdateMemory(row); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	var embeddingAt *string
	err := db.Conn().QueryRow(`SELECT embedding_at FROM memories WHERE memory_id='clr-1'`).Scan(&embeddingAt)
	if err != nil {
		t.Fatalf("scan embedding_at: %v", err)
	}
	if embeddingAt != nil {
		t.Errorf("embedding_at should be NULL after UpdateMemory, got %q", *embeddingAt)
	}
}

// ── Entity graph tests ────────────────────────────────────────────────────────

func testNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// TestUpsertEntity — first call creates; second call bumps last_seen.
func TestUpsertEntity(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	id1, err := db.UpsertEntity("Alice", "person", now)
	if err != nil {
		t.Fatalf("UpsertEntity (create): %v", err)
	}
	if id1 == "" {
		t.Fatal("expected non-empty entity_id")
	}

	// Second upsert same name+type: should return the same ID and update last_seen.
	later := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	id2, err := db.UpsertEntity("Alice", "person", later)
	if err != nil {
		t.Fatalf("UpsertEntity (bump): %v", err)
	}
	if id2 != id1 {
		t.Errorf("expected same entity_id on re-upsert: want %s, got %s", id1, id2)
	}

	ents, err := db.ListEntities(10)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(ents))
	}
	if ents[0].LastSeen != later {
		t.Errorf("last_seen should be updated: want %s, got %s", later, ents[0].LastSeen)
	}
}

// TestSaveEdgeAndTraverse — save two entities + one edge, traverse from source.
func TestSaveEdgeAndTraverse(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	srcID, _ := db.UpsertEntity("Rami", "person", now)
	tgtID, _ := db.UpsertEntity("RXA Labs", "organization", now)

	edge := EdgeRow{
		EdgeID:       "edge-1",
		SourceEntity: srcID,
		TargetEntity: tgtID,
		Relation:     "works_at",
		ValidFrom:    now,
		Confidence:   1.0,
	}
	if err := db.SaveEdge(edge); err != nil {
		t.Fatalf("SaveEdge: %v", err)
	}

	edges, err := db.TraverseEntityGraph([]string{srcID}, 1)
	if err != nil {
		t.Fatalf("TraverseEntityGraph: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Relation != "works_at" {
		t.Errorf("expected relation works_at, got %s", edges[0].Relation)
	}
	if edges[0].SourceEntity != srcID || edges[0].TargetEntity != tgtID {
		t.Error("edge endpoints do not match")
	}
}

// TestSupersedeEdge — superseding closes the old edge before inserting the new one.
func TestSupersedeEdge(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	srcID, _ := db.UpsertEntity("Bob", "person", now)
	tgtID, _ := db.UpsertEntity("AcmeCorp", "organization", now)

	db.SaveEdge(EdgeRow{ //nolint:errcheck
		EdgeID: "old-edge", SourceEntity: srcID, TargetEntity: tgtID,
		Relation: "works_at", ValidFrom: now, Confidence: 1.0,
	})

	later := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	db.SupersedeEdge(srcID, tgtID, "works_at", later) //nolint:errcheck
	db.SaveEdge(EdgeRow{                               //nolint:errcheck
		EdgeID: "new-edge", SourceEntity: srcID, TargetEntity: tgtID,
		Relation: "works_at", ValidFrom: later, Confidence: 1.0,
	})

	// Only the new edge should be returned (valid_until IS NULL).
	edges, err := db.TraverseEntityGraph([]string{srcID}, 1)
	if err != nil {
		t.Fatalf("TraverseEntityGraph: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 valid edge after supersede, got %d", len(edges))
	}
	if edges[0].EdgeID != "new-edge" {
		t.Errorf("expected new-edge, got %s", edges[0].EdgeID)
	}
}

// TestPruneExpiredEdges — edge whose endpoint is deleted gets closed.
func TestPruneExpiredEdges(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	srcID, _ := db.UpsertEntity("Carol", "person", now)
	tgtID, _ := db.UpsertEntity("GhostOrg", "organization", now)

	db.SaveEdge(EdgeRow{ //nolint:errcheck
		EdgeID: "dangling", SourceEntity: srcID, TargetEntity: tgtID,
		Relation: "member_of", ValidFrom: now, Confidence: 1.0,
	})

	// Delete the target entity to create a dangling edge.
	db.Conn().Exec(`DELETE FROM memory_entities WHERE entity_id=?`, tgtID) //nolint:errcheck

	pruned := db.PruneExpiredEdges()
	if pruned != 1 {
		t.Errorf("expected 1 pruned edge, got %d", pruned)
	}

	// Confirm the edge is now closed.
	edges, err := db.TraverseEntityGraph([]string{srcID}, 1)
	if err != nil {
		t.Fatalf("TraverseEntityGraph after prune: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 valid edges after prune, got %d", len(edges))
	}
}

// TestDeduplicateEntitiesKeepsEmbedding — when duplicates exist, the one with
// an embedding is kept (not necessarily the oldest).
func TestDeduplicateEntitiesKeepsEmbedding(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	// Insert two entities with the same name+type directly (bypassing UpsertEntity
	// so we can control first_seen ordering and embedding presence).
	earlier := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	later := testNow()

	db.Conn().Exec( //nolint:errcheck
		`INSERT INTO memory_entities (entity_id,name,entity_type,first_seen,last_seen,metadata_json)
		 VALUES ('old-id','Dave','person',?,?,'{}')`, earlier, earlier)
	db.Conn().Exec( //nolint:errcheck
		`INSERT INTO memory_entities (entity_id,name,entity_type,first_seen,last_seen,metadata_json)
		 VALUES ('new-id','Dave','person',?,?,'{}')`, later, later)

	// Give the newer (new-id) an embedding.
	vec := []float32{0.9, 0.1}
	if err := db.UpdateEntityEmbedding("new-id", vec); err != nil {
		t.Fatalf("UpdateEntityEmbedding: %v", err)
	}

	removed := db.DeduplicateEntities()
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	ents, err := db.ListEntities(10)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity after dedup, got %d", len(ents))
	}
	// The survivor must be new-id (has embedding), not old-id (oldest).
	if ents[0].EntityID != "new-id" {
		t.Errorf("expected new-id (has embedding) to survive, got %s", ents[0].EntityID)
	}
}

// TestDeduplicateEntitiesRepoinstsEdges — edges to the removed duplicate must
// be repointed to the survivor.
func TestDeduplicateEntitiesRepointsEdges(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	earlier := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)

	// Two duplicate entity nodes.
	db.Conn().Exec(`INSERT INTO memory_entities (entity_id,name,entity_type,first_seen,last_seen,metadata_json) VALUES ('e-old','Eve','person',?,?,'{}')`, earlier, earlier) //nolint:errcheck
	db.Conn().Exec(`INSERT INTO memory_entities (entity_id,name,entity_type,first_seen,last_seen,metadata_json) VALUES ('e-new','Eve','person',?,?,'{}')`, now, now)           //nolint:errcheck

	// A third entity connected to the duplicate (e-old).
	otherID, _ := db.UpsertEntity("SomeOrg", "organization", now)
	db.SaveEdge(EdgeRow{EdgeID: "ep-edge", SourceEntity: "e-old", TargetEntity: otherID, Relation: "member_of", ValidFrom: now, Confidence: 1.0}) //nolint:errcheck

	db.DeduplicateEntities()

	// The edge should now point from the survivor to otherID.
	ents, _ := db.ListEntities(10)
	var survivorID string
	for _, e := range ents {
		if e.Name == "Eve" {
			survivorID = e.EntityID
		}
	}
	if survivorID == "" {
		t.Fatal("survivor entity Eve not found after dedup")
	}
	edges, err := db.TraverseEntityGraph([]string{survivorID}, 1)
	if err != nil {
		t.Fatalf("TraverseEntityGraph: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 repointed edge, got %d", len(edges))
	}
}

// TestFetchEntitiesByIDs — fetches a subset of entities by ID.
func TestFetchEntitiesByIDs(t *testing.T) {
	db, cleanup := openMemTestDB(t)
	defer cleanup()

	now := testNow()
	id1, _ := db.UpsertEntity("Frank", "person", now)
	id2, _ := db.UpsertEntity("Grace", "person", now)
	_, _ = db.UpsertEntity("Henry", "person", now) // not requested

	got, err := db.FetchEntitiesByIDs([]string{id1, id2})
	if err != nil {
		t.Fatalf("FetchEntitiesByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(got))
	}
	names := map[string]bool{}
	for _, e := range got {
		names[e.Name] = true
	}
	if !names["Frank"] || !names["Grace"] {
		t.Errorf("expected Frank and Grace, got %v", names)
	}
}
