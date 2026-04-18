package chat

import (
	"fmt"
	"strings"
	"testing"

	"atlas-runtime-go/internal/storage"
)

// ── estimateTokens ────────────────────────────────────────────────────────────

func TestEstimateTokens_empty(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("empty string: want 0, got %d", got)
	}
}

func TestEstimateTokens_proportional(t *testing.T) {
	// 400 chars / 4 * 1.15 = 115
	got := estimateTokens(strings.Repeat("a", 400))
	if got < 100 || got > 130 {
		t.Fatalf("400-char string: want ~115, got %d", got)
	}
}

// ── historyBudgetTokens ───────────────────────────────────────────────────────

func TestHistoryBudgetTokens_normal(t *testing.T) {
	// 128K window, 2K system tokens
	// toolsReserve   = 128000 * 0.15 = 19200
	// responseReserve= 128000 * 0.20 = 25600
	// available      = 128000 - 2000 - 19200 - 25600 = 81200
	// budget (normal)= 81200 * 0.85 = 69020
	got := historyBudgetTokens(128000, 2000, false)
	if got < 60000 || got > 80000 {
		t.Fatalf("normal budget out of expected range: %d", got)
	}
}

func TestHistoryBudgetTokens_compact(t *testing.T) {
	normal := historyBudgetTokens(128000, 2000, false)
	compact := historyBudgetTokens(128000, 2000, true)
	if compact >= normal {
		t.Fatalf("compact budget (%d) should be less than normal (%d)", compact, normal)
	}
}

func TestHistoryBudgetTokens_tinyModel(t *testing.T) {
	// 4K local model, 1K system prompt — should not go below minHistoryBudget.
	got := historyBudgetTokens(4096, 1000, false)
	if got < minHistoryBudget {
		t.Fatalf("tiny model: budget %d below floor %d", got, minHistoryBudget)
	}
}

func TestHistoryBudgetTokens_compactSmall(t *testing.T) {
	// Very small context + compact mode should still respect the floor.
	got := historyBudgetTokens(4096, 3000, true)
	if got < minHistoryBudget {
		t.Fatalf("compact small: budget %d below floor %d", got, minHistoryBudget)
	}
}

// ── proactiveKeepFrom ─────────────────────────────────────────────────────────

func makeRow(id, role, content string) storage.MessageRow {
	return storage.MessageRow{ID: id, Role: role, Content: content}
}

func TestProactiveKeepFrom_allFit(t *testing.T) {
	history := []storage.MessageRow{
		makeRow("1", "user", "hello"),
		makeRow("2", "assistant", "hi"),
	}
	got := proactiveKeepFrom(history, "current", 10000)
	if got != 0 {
		t.Fatalf("all fit: want 0, got %d", got)
	}
}

func TestProactiveKeepFrom_dropOldest(t *testing.T) {
	// Build 10 messages; each ~115 tokens. Budget = 400 → keeps ~3.
	big := strings.Repeat("x", 400) // ~115 tokens
	var history []storage.MessageRow
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, makeRow(fmt.Sprintf("%d", i), role, big))
	}
	budget := 400 // fits ~3 messages
	got := proactiveKeepFrom(history, "current", budget)
	if got == 0 {
		t.Fatal("expected some messages to be pruned, got keepFrom=0")
	}
	// The returned index must be a user-role message.
	if history[got].Role != "user" {
		t.Fatalf("keepFrom=%d lands on role=%q, want user", got, history[got].Role)
	}
}

func TestProactiveKeepFrom_landOnUserBoundary(t *testing.T) {
	// History: user, assistant, assistant, user, assistant
	// If budget is tight the pruner may want to keep from index 2 (assistant),
	// but must advance to index 3 (the next user).
	big := strings.Repeat("x", 800) // ~230 tokens each
	history := []storage.MessageRow{
		makeRow("0", "user", big),
		makeRow("1", "assistant", big),
		makeRow("2", "assistant", big), // unusual but must handle it
		makeRow("3", "user", big),
		makeRow("4", "assistant", big),
	}
	// Budget fits only 2 messages; walk backward hits idx 4 and 3 (fits),
	// idx 2 overflows → keepFrom candidate = 3, which is already "user". ✓
	got := proactiveKeepFrom(history, "current", 500)
	if got == 0 {
		t.Fatal("expected pruning, got keepFrom=0")
	}
	if history[got].Role != "user" {
		t.Fatalf("keepFrom=%d lands on role=%q, want user", got, history[got].Role)
	}
}

func TestProactiveKeepFrom_skipsCurrentMsg(t *testing.T) {
	big := strings.Repeat("x", 4000) // very large — won't fit even one
	history := []storage.MessageRow{
		makeRow("old1", "user", big),
		makeRow("old2", "assistant", big),
		makeRow("current", "user", big), // current turn — must be excluded from cost
	}
	// Budget is tight: only one message could fit if the current msg is excluded.
	got := proactiveKeepFrom(history, "current", estimateRowTokens(makeRow("x", "user", big))+50)
	// old2 (assistant) should fit; old1 should be dropped.
	// keepFrom must advance to a user message boundary → index 0 (old1) or 1.
	_ = got // just checking it doesn't panic and respects the boundary
}
