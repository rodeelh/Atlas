package storage

// usage_test.go — tests for the token_usage storage layer.
//
// Coverage:
//   - RecordTokenUsage: insert, duplicate ID rejected
//   - TokenUsageEvents: empty, filters (since/until/provider/model), limit enforcement
//   - GetTokenUsageSummary: scalar totals, per-model breakdown, daily series, date range filtering
//   - BackfillTokenUsageCosts: updates zero-cost rows for cloud models, skips local providers
//   - TokenUsageDeleteBefore: deletes correct rows, returns count, zero when nothing matches
//   - Concurrent writes: race detector must pass

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// openTestDB opens a temporary SQLite database for testing.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertEvent is a helper that inserts one row and fails the test on error.
func insertEvent(t *testing.T, db *DB, id, convID, provider, model string,
	inputTokens, outputTokens int,
	inputCost, outputCost float64,
	recordedAt string,
) {
	t.Helper()
	if err := db.RecordTokenUsage(id, convID, provider, model, inputTokens, outputTokens, inputCost, outputCost, recordedAt); err != nil {
		t.Fatalf("RecordTokenUsage(%s): %v", id, err)
	}
}

// ── RecordTokenUsage ──────────────────────────────────────────────────────────

func TestRecordTokenUsage_Insert_Succeeds(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "conv1", "anthropic", "claude-sonnet-4-6", 100, 200, 0.0003, 0.003, "2026-04-01T10:00:00Z")
	events, err := db.TokenUsageEvents("", "", "", "", 10)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	e := events[0]
	if e.ID != "e1" || e.Provider != "anthropic" || e.Model != "claude-sonnet-4-6" {
		t.Errorf("wrong event: %+v", e)
	}
	if e.InputTokens != 100 || e.OutputTokens != 200 {
		t.Errorf("token counts: want 100/200, got %d/%d", e.InputTokens, e.OutputTokens)
	}
	// total_cost_usd must equal inputCost + outputCost
	const wantTotal = 0.0003 + 0.003
	if math.Abs(e.TotalCostUSD-wantTotal) > 1e-9 {
		t.Errorf("total cost: want %.6f, got %.6f", wantTotal, e.TotalCostUSD)
	}
}

func TestRecordTokenUsage_DuplicateID_Rejected(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "dup", "conv1", "openai", "gpt-4o", 10, 20, 0.001, 0.002, "2026-04-01T10:00:00Z")
	err := db.RecordTokenUsage("dup", "conv2", "openai", "gpt-4o", 10, 20, 0.001, 0.002, "2026-04-01T11:00:00Z")
	if err == nil {
		t.Error("duplicate primary key should return an error")
	}
}

// ── TokenUsageEvents ──────────────────────────────────────────────────────────

func TestTokenUsageEvents_EmptyDB_ReturnsEmptySlice(t *testing.T) {
	db := openTestDB(t)
	events, err := db.TokenUsageEvents("", "", "", "", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("want 0 events in empty DB, got %d", len(events))
	}
}

func TestTokenUsageEvents_FilterBySince(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "old", "c1", "anthropic", "model-a", 10, 10, 0, 0, "2026-03-01T00:00:00Z")
	insertEvent(t, db, "new", "c1", "anthropic", "model-a", 20, 20, 0, 0, "2026-04-01T00:00:00Z")

	events, err := db.TokenUsageEvents("2026-04-01T00:00:00Z", "", "", "", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 || events[0].ID != "new" {
		t.Errorf("since filter: want [new], got %v", evIDs(events))
	}
}

func TestTokenUsageEvents_FilterByUntil(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "anthropic", "model-a", 10, 10, 0, 0, "2026-03-01T00:00:00Z")
	insertEvent(t, db, "e2", "c1", "anthropic", "model-a", 20, 20, 0, 0, "2026-04-01T00:00:00Z")

	events, err := db.TokenUsageEvents("", "2026-03-31T23:59:59Z", "", "", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 || events[0].ID != "e1" {
		t.Errorf("until filter: want [e1], got %v", evIDs(events))
	}
}

func TestTokenUsageEvents_FilterByProvider(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "oai", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")
	insertEvent(t, db, "ant", "c1", "anthropic", "claude-sonnet-4-6", 10, 10, 0, 0, "2026-04-01T00:00:00Z")

	events, err := db.TokenUsageEvents("", "", "openai", "", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 || events[0].ID != "oai" {
		t.Errorf("provider filter: want [oai], got %v", evIDs(events))
	}
}

func TestTokenUsageEvents_FilterByModel(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "m1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")
	insertEvent(t, db, "m2", "c1", "openai", "gpt-4o-mini", 10, 10, 0, 0, "2026-04-01T01:00:00Z")

	events, err := db.TokenUsageEvents("", "", "", "gpt-4o-mini", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 || events[0].ID != "m2" {
		t.Errorf("model filter: want [m2], got %v", evIDs(events))
	}
}

func TestTokenUsageEvents_LimitEnforced(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 10; i++ {
		insertEvent(t, db, fmt.Sprintf("e%d", i), "c1", "openai", "gpt-4o",
			10, 10, 0, 0, fmt.Sprintf("2026-04-01T%02d:00:00Z", i))
	}

	events, err := db.TokenUsageEvents("", "", "", "", 3)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("limit=3: want 3 events, got %d", len(events))
	}
}

func TestTokenUsageEvents_LimitZero_DefaultsTo200(t *testing.T) {
	// limit ≤ 0 should be treated as default (200) not unlimited.
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")

	events, err := db.TokenUsageEvents("", "", "", "", 0)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	// Just verify it doesn't panic and returns the event (limit defaults to 200).
	if len(events) != 1 {
		t.Errorf("limit=0: want 1 event via default limit, got %d", len(events))
	}
}

func TestTokenUsageEvents_LimitOver1000_ClampedTo1000(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")

	// limit=5000 exceeds the 1000 hard cap — should clamp to 1000 (not 200).
	events, err := db.TokenUsageEvents("", "", "", "", 5000)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("limit=5000: want 1 event after 1000 clamp, got %d", len(events))
	}
}

func TestTokenUsageEvents_OrderedNewestFirst(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T08:00:00Z")
	insertEvent(t, db, "e2", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T10:00:00Z")
	insertEvent(t, db, "e3", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T09:00:00Z")

	events, err := db.TokenUsageEvents("", "", "", "", 100)
	if err != nil {
		t.Fatalf("TokenUsageEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	// Should be newest first: e2, e3, e1
	if events[0].ID != "e2" || events[1].ID != "e3" || events[2].ID != "e1" {
		t.Errorf("order wrong: got %v", evIDs(events))
	}
}

// ── GetTokenUsageSummary ──────────────────────────────────────────────────────

func TestGetTokenUsageSummary_EmptyDB_ReturnsZeros(t *testing.T) {
	db := openTestDB(t)
	s, err := db.GetTokenUsageSummary("", "", 30)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if s.TotalInputTokens != 0 || s.TotalOutputTokens != 0 || s.TotalTokens != 0 {
		t.Errorf("want zero token counts, got %+v", s)
	}
	if s.TurnCount != 0 {
		t.Errorf("want zero turn count, got %d", s.TurnCount)
	}
	if s.TotalCostUSD != 0 {
		t.Errorf("want zero cost, got %.6f", s.TotalCostUSD)
	}
	if s.ByModel != nil {
		t.Errorf("want nil ByModel for empty DB, got %v", s.ByModel)
	}
}

func TestGetTokenUsageSummary_ScalarTotals(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 100, 200, 0.0025, 0.002, "2026-04-01T00:00:00Z")
	insertEvent(t, db, "e2", "c1", "anthropic", "claude-sonnet-4-6", 50, 100, 0.00015, 0.0015, "2026-04-01T01:00:00Z")

	s, err := db.GetTokenUsageSummary("", "", 30)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if s.TotalInputTokens != 150 {
		t.Errorf("TotalInputTokens: want 150, got %d", s.TotalInputTokens)
	}
	if s.TotalOutputTokens != 300 {
		t.Errorf("TotalOutputTokens: want 300, got %d", s.TotalOutputTokens)
	}
	if s.TotalTokens != 450 {
		t.Errorf("TotalTokens: want 450, got %d", s.TotalTokens)
	}
	if s.TurnCount != 2 {
		t.Errorf("TurnCount: want 2, got %d", s.TurnCount)
	}
}

func TestGetTokenUsageSummary_ByModelBreakdown(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 100, 200, 0.01, 0.02, "2026-04-01T00:00:00Z")
	insertEvent(t, db, "e2", "c1", "openai", "gpt-4o", 50, 50, 0.005, 0.005, "2026-04-01T01:00:00Z")
	insertEvent(t, db, "e3", "c1", "anthropic", "claude-sonnet-4-6", 200, 100, 0.001, 0.001, "2026-04-01T02:00:00Z")

	s, err := db.GetTokenUsageSummary("", "", 30)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if len(s.ByModel) != 2 {
		t.Fatalf("want 2 model breakdowns, got %d", len(s.ByModel))
	}
	// ByModel ordered by cost DESC — gpt-4o should be first
	if s.ByModel[0].Model != "gpt-4o" {
		t.Errorf("first model should be gpt-4o (highest cost), got %s", s.ByModel[0].Model)
	}
	if s.ByModel[0].TurnCount != 2 {
		t.Errorf("gpt-4o turn count: want 2, got %d", s.ByModel[0].TurnCount)
	}
	if s.ByModel[0].TotalTokens != (100 + 200 + 50 + 50) {
		t.Errorf("gpt-4o TotalTokens: want %d, got %d", 100+200+50+50, s.ByModel[0].TotalTokens)
	}
}

func TestGetTokenUsageSummary_DateRangeFiltering(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "old", "c1", "openai", "gpt-4o", 999, 999, 100, 100, "2026-01-01T00:00:00Z")
	insertEvent(t, db, "mid", "c1", "openai", "gpt-4o", 100, 200, 0.01, 0.02, "2026-04-01T00:00:00Z")

	s, err := db.GetTokenUsageSummary("2026-03-01T00:00:00Z", "", 30)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if s.TurnCount != 1 {
		t.Errorf("since filter: want 1 turn, got %d", s.TurnCount)
	}
	if s.TotalInputTokens != 100 {
		t.Errorf("since filter: want 100 input tokens, got %d", s.TotalInputTokens)
	}
}

func TestGetTokenUsageSummary_DailySeries_PopulatesCorrectly(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "d1-a", "c1", "openai", "gpt-4o", 100, 100, 0.01, 0.01, "2026-04-01T08:00:00Z")
	insertEvent(t, db, "d1-b", "c1", "openai", "gpt-4o", 50, 50, 0.005, 0.005, "2026-04-01T09:00:00Z")
	insertEvent(t, db, "d2-a", "c1", "openai", "gpt-4o", 200, 200, 0.02, 0.02, "2026-04-02T08:00:00Z")

	s, err := db.GetTokenUsageSummary("", "", 30)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if len(s.DailySeries) != 2 {
		t.Fatalf("want 2 daily entries, got %d", len(s.DailySeries))
	}
	// Daily series ordered ASC
	if s.DailySeries[0].Date != "2026-04-01" {
		t.Errorf("first daily date: want 2026-04-01, got %s", s.DailySeries[0].Date)
	}
	if s.DailySeries[0].TurnCount != 2 {
		t.Errorf("day 1 turn count: want 2, got %d", s.DailySeries[0].TurnCount)
	}
	if s.DailySeries[0].TotalTokens != (100 + 100 + 50 + 50) {
		t.Errorf("day 1 total tokens: want %d, got %d", 100+100+50+50, s.DailySeries[0].TotalTokens)
	}
}

func TestGetTokenUsageSummary_DailySeries_ZeroDays_Skipped(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 100, 100, 0.01, 0.01, "2026-04-01T00:00:00Z")

	s, err := db.GetTokenUsageSummary("", "", 0)
	if err != nil {
		t.Fatalf("GetTokenUsageSummary: %v", err)
	}
	if s.DailySeries != nil {
		t.Errorf("dailyDays=0 should skip series, got %v", s.DailySeries)
	}
}

// ── BackfillTokenUsageCosts ───────────────────────────────────────────────────

func TestBackfillTokenUsageCosts_UpdatesZeroCostRows(t *testing.T) {
	db := openTestDB(t)
	// Insert with zero costs (simulates rows recorded before pricing was implemented).
	insertEvent(t, db, "e1", "c1", "anthropic", "claude-sonnet-4-6", 1000, 500, 0, 0, "2026-04-01T00:00:00Z")

	count := db.BackfillTokenUsageCosts()
	if count != 1 {
		t.Errorf("BackfillTokenUsageCosts: want 1 updated row, got %d", count)
	}

	events, _ := db.TokenUsageEvents("", "", "", "", 10)
	if len(events) != 1 {
		t.Fatalf("want 1 event after backfill, got %d", len(events))
	}
	if events[0].TotalCostUSD == 0 {
		t.Error("TotalCostUSD should be non-zero after backfill for known claude model")
	}
}

func TestBackfillTokenUsageCosts_SkipsLocalProviders(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "lm_studio", "llama-3.1-8b", 1000, 500, 0, 0, "2026-04-01T00:00:00Z")
	insertEvent(t, db, "e2", "c1", "ollama", "mistral", 1000, 500, 0, 0, "2026-04-01T01:00:00Z")

	count := db.BackfillTokenUsageCosts()
	if count != 0 {
		t.Errorf("BackfillTokenUsageCosts: local providers must not be backfilled, got %d updated", count)
	}
}

func TestBackfillTokenUsageCosts_NoOpWhenAllHaveCosts(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 100, 200, 0.0025, 0.002, "2026-04-01T00:00:00Z")

	count := db.BackfillTokenUsageCosts()
	if count != 0 {
		t.Errorf("BackfillTokenUsageCosts: row already has cost, should no-op, got %d updated", count)
	}
}

func TestBackfillTokenUsageCosts_UnknownModel_NotBackfilled(t *testing.T) {
	db := openTestDB(t)
	// A totally unknown cloud model with 0 cost — pricing unknown, so skip.
	insertEvent(t, db, "e1", "c1", "unknown-provider", "totally-unknown-model-xyz", 100, 200, 0, 0, "2026-04-01T00:00:00Z")

	count := db.BackfillTokenUsageCosts()
	// unknown-provider is not a local provider (not lm_studio/ollama/atlas_engine)
	// so it passes the WHERE clause, but ComputeCost returns known=false → skip.
	if count != 0 {
		t.Errorf("unknown model: want 0 updates, got %d", count)
	}
}

// ── TokenUsageDeleteBefore ────────────────────────────────────────────────────

func TestTokenUsageDeleteBefore_DeletesCorrectRows(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "old1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-01-01T00:00:00Z")
	insertEvent(t, db, "old2", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-02-01T00:00:00Z")
	insertEvent(t, db, "keep", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")

	deleted, err := db.TokenUsageDeleteBefore("2026-03-01T00:00:00Z")
	if err != nil {
		t.Fatalf("TokenUsageDeleteBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("want 2 deleted, got %d", deleted)
	}

	events, _ := db.TokenUsageEvents("", "", "", "", 100)
	if len(events) != 1 || events[0].ID != "keep" {
		t.Errorf("want [keep] remaining, got %v", evIDs(events))
	}
}

func TestTokenUsageDeleteBefore_NothingToDelete_ReturnsZero(t *testing.T) {
	db := openTestDB(t)
	insertEvent(t, db, "e1", "c1", "openai", "gpt-4o", 10, 10, 0, 0, "2026-04-01T00:00:00Z")

	deleted, err := db.TokenUsageDeleteBefore("2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("TokenUsageDeleteBefore: %v", err)
	}
	if deleted != 0 {
		t.Errorf("want 0 deleted, got %d", deleted)
	}
}

func TestTokenUsageDeleteBefore_EmptyDB_ReturnsZero(t *testing.T) {
	db := openTestDB(t)
	deleted, err := db.TokenUsageDeleteBefore("2026-04-01T00:00:00Z")
	if err != nil {
		t.Fatalf("TokenUsageDeleteBefore: %v", err)
	}
	if deleted != 0 {
		t.Errorf("want 0 deleted from empty DB, got %d", deleted)
	}
}

// ── Concurrent writes ─────────────────────────────────────────────────────────

func TestRecordTokenUsage_ConcurrentWrites_NoRace(t *testing.T) {
	db := openTestDB(t)
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := db.RecordTokenUsage(
				fmt.Sprintf("concurrent-%d", idx),
				"conv-concurrent",
				"openai", "gpt-4o",
				100, 200,
				0.0025, 0.002,
				fmt.Sprintf("2026-04-01T%02d:00:00Z", idx%24),
			)
			if err != nil {
				t.Errorf("concurrent RecordTokenUsage %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	events, err := db.TokenUsageEvents("", "", "", "", 1000)
	if err != nil {
		t.Fatalf("TokenUsageEvents after concurrent writes: %v", err)
	}
	if len(events) != n {
		t.Errorf("want %d events after concurrent writes, got %d", n, len(events))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func evIDs(events []TokenUsageRow) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	return ids
}

// Ensure the test file compiles even if os/filepath are only used by openTestDB.
var _ = os.DevNull
var _ = filepath.Join
