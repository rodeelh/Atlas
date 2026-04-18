package chat

import "atlas-runtime-go/internal/storage"

// Token budget constants.
// These model each component that consumes context window space so that history
// pruning is proactive rather than reactive (no more waiting for a
// context_length_exceeded error from the provider).
const (
	// tokenEstimateDivisor converts character count to a rough token count.
	// GPT/Claude average ~4 chars per token for English prose.
	tokenEstimateDivisor = 4

	// tokenSafetyFactor adds a 15 % buffer over the raw estimate to account for
	// multi-byte characters, XML tags, and tokenizer irregularities.
	tokenSafetyFactor = 1.15

	// msgOverheadTokens accounts for role/id/metadata wrapper on every message.
	msgOverheadTokens = 10

	// toolsReserveFraction is the share of the context window held back for tool
	// definitions. 15 % covers ~30–50 tools at average schema size.
	toolsReserveFraction = 0.15

	// responseReserveFraction is the share held back for the model's output.
	responseReserveFraction = 0.20

	// historyBudgetFraction is the share of remaining context allocated to history
	// on a normal referential turn ("fix that", "also add X").
	historyBudgetFraction = 0.85

	// compactHistoryBudgetFraction shrinks the history allocation on new-topic
	// turns (no referential pronouns). This lets the model devote more headroom
	// to reasoning without the cost of a full-context replay.
	compactHistoryBudgetFraction = 0.40

	// minHistoryBudget is the floor — always allow at least 2 K tokens of history
	// so very small local models still get useful context.
	minHistoryBudget = 2000
)

// estimateTokens returns a conservative token estimate for a plain-text string.
// It is intentionally fast (no tokenizer) and slightly over-estimates.
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return int(float64(len(s)/tokenEstimateDivisor) * tokenSafetyFactor)
}

// estimateRowTokens estimates the token cost of a stored message row, including
// any structured blocks stored alongside the plain-text content.
func estimateRowTokens(m storage.MessageRow) int {
	chars := len(m.Content)
	if m.BlocksJSON != nil {
		chars += len(*m.BlocksJSON)
	}
	return msgOverheadTokens + int(float64(chars/tokenEstimateDivisor)*tokenSafetyFactor)
}

// historyBudgetTokens computes the token budget available for conversation
// history given:
//   - contextWindow — the model's total context window in tokens
//   - systemTokens  — estimated tokens consumed by the assembled system prompt
//   - compact       — true on new-topic turns; shrinks the history share so the
//     model has more headroom for active reasoning
//
// Budget formula:
//
//	available = contextWindow − systemTokens − toolsReserve − responseReserve
//	budget    = available × historyFraction   (full | compact)
func historyBudgetTokens(contextWindow, systemTokens int, compact bool) int {
	toolsReserve := int(float64(contextWindow) * toolsReserveFraction)
	responseReserve := int(float64(contextWindow) * responseReserveFraction)
	available := contextWindow - systemTokens - toolsReserve - responseReserve
	if available < 0 {
		available = 0
	}
	fraction := historyBudgetFraction
	if compact {
		fraction = compactHistoryBudgetFraction
	}
	budget := int(float64(available) * fraction)
	if budget < minHistoryBudget {
		return minHistoryBudget
	}
	return budget
}

// proactiveKeepFrom returns the index into history from which messages should
// be replayed so that the total estimated token cost stays within budgetTokens.
//
// The algorithm walks backward from the most recent message, accumulating token
// costs, and stops when the budget would be exceeded. It then advances the
// boundary forward to the next user-role message so the replay never starts
// mid-exchange (e.g. an assistant turn with no preceding user context).
//
// The current user message (userMsgID) is excluded from the calculation.
// Returns 0 when all messages fit within the budget.
func proactiveKeepFrom(history []storage.MessageRow, userMsgID string, budgetTokens int) int {
	tokens := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.ID == userMsgID || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		cost := estimateRowTokens(m)
		if tokens+cost > budgetTokens {
			// Advance to the next user-role boundary so replay starts cleanly.
			keepFrom := i + 1
			for keepFrom < len(history) && history[keepFrom].Role != "user" {
				keepFrom++
			}
			return keepFrom
		}
		tokens += cost
	}
	return 0 // all messages fit
}
