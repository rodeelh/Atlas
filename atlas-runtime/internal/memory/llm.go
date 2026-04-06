package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"
)

// llmCandidate is the JSON shape returned by the LLM extraction prompt.
type llmCandidate struct {
	Category      string  `json:"category"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	Importance    float64 `json:"importance"`
	Reinforcement string  `json:"reinforcement"` // "reinforce", "weaken", "contradict"
}

// reinforcementAlpha is the confidence step applied per reinforcement operation.
const reinforcementAlpha = 0.20

// extractWithLLM sends both user and assistant messages to the fast model to
// extract memories that the regex pipeline cannot catch: novel preferences,
// implicit signals, facts discovered through tool results, and relationship
// changes. Results are deduplicated against existing memories before insert,
// with opinion reinforcement applied on collision (Reinforce/Weaken/Contradict).
func extractWithLLM(
	ctx context.Context,
	provider agent.ProviderConfig,
	userMsg, assistantMsg string,
	toolSummaries []string,
	toolResultSummaries []string,
	convID string,
	db *storage.DB,
) {
	system := `You extract factual memories from an Atlas conversation turn.

Return a JSON array of objects. Each object has:
- "category": one of "profile", "preference", "project", "workflow", "episodic", "tool_learning"
- "title": short descriptive title (max 6 words)
- "content": one sentence describing the fact
- "importance": 0.0-1.0 (how important is this to remember long-term?)
- "reinforcement": "reinforce", "weaken", or "contradict"
  (use "reinforce" for confirmations or new facts; "weaken" for partial contradictions;
   "contradict" for direct reversals of prior beliefs)

Categories:
- profile: name, location, role, expertise, tools they use
- preference: communication style, response format, approval preferences
- project: active projects, goals, deadlines, tech stack
- workflow: how they work, recurring patterns, habits, schedules
- episodic: significant events, milestones, breakthroughs, frustrations
- tool_learning: what worked or failed when using a skill — include skill name in title

Rules:
- For tool_learning: only extract when a tool failed, produced suboptimal results, or the
  user corrected Atlas's tool choice. Tag with skill name (e.g. ["weather.current", "airports"]).
- Skip greetings, routine questions, and small talk
- Skip facts only relevant to the current turn (ephemeral)
- Return [] if nothing worth remembering
- Max 4 items per turn
- Be conservative — false positives are worse than missed extractions`

	tools := ""
	if len(toolSummaries) > 0 {
		tools = strings.Join(toolSummaries, ", ")
	}
	toolResults := compressToolResults(toolResultSummaries)
	userContent := fmt.Sprintf("User: %s\nAtlas: %s\nTools used: %s\nTool outcomes: %s",
		truncateRunes(userMsg, 400),
		truncateRunes(assistantMsg, 400),
		tools,
		toolResults,
	)

	messages := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: userContent},
	}

	reply, _, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil {
		logstore.Write("debug", "LLM memory extraction failed: "+err.Error(),
			map[string]string{"conv": convID[:min(8, len(convID))]})
		return
	}

	replyStr, ok := reply.Content.(string)
	if !ok {
		return
	}
	replyStr = strings.TrimSpace(replyStr)

	// Strip markdown code fences if the model wrapped its response.
	replyStr = stripCodeFence(replyStr)

	var candidates []llmCandidate
	if err := json.Unmarshal([]byte(replyStr), &candidates); err != nil {
		logstore.Write("debug", "LLM memory extraction: invalid JSON: "+err.Error(),
			map[string]string{"conv": convID[:min(8, len(convID))]})
		return
	}

	if len(candidates) == 0 {
		return
	}
	// Cap at 3 to prevent runaway extraction.
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	validCategories := map[string]bool{
		"profile": true, "preference": true, "project": true,
		"workflow": true, "episodic": true, "tool_learning": true,
	}

	saved := 0
	for _, c := range candidates {
		if !validCategories[c.Category] {
			continue
		}
		if c.Title == "" || c.Content == "" {
			continue
		}
		if c.Importance < 0.3 || c.Importance > 1.0 {
			c.Importance = 0.7 // normalize out-of-range values
		}
		if c.Reinforcement == "" {
			c.Reinforcement = "reinforce"
		}

		// Deduplicate against existing memories.
		existing, err := db.FindDuplicateMemory(c.Category, c.Title)
		if err != nil {
			continue
		}

		if existing != nil {
			// Opinion reinforcement: update confidence based on whether new evidence
			// agrees with, weakens, or contradicts the existing memory.
			updated := *existing
			updated.UpdatedAt = now

			switch c.Reinforcement {
			case "contradict":
				// Direct contradiction: reduce confidence sharply and mark invalid.
				updated.Confidence = math.Max(existing.Confidence-2*reinforcementAlpha, 0.0)
				updated.Content = c.Content // use new (corrected) content
				updated.ValidUntil = &now   // exclude from active retrieval
			case "weaken":
				// Partial contradiction: reduce confidence gently.
				updated.Confidence = math.Max(existing.Confidence-reinforcementAlpha, 0.0)
			default: // "reinforce"
				// Confirmation: raise confidence, prefer longer/newer content.
				updated.Confidence = math.Min(existing.Confidence+reinforcementAlpha, 1.0)
				if len(c.Content) > len(existing.Content) {
					updated.Content = c.Content
				}
			}
			if c.Importance > updated.Importance {
				updated.Importance = c.Importance
			}
			db.UpdateMemory(updated) //nolint:errcheck
		} else {
			// New memory — start with moderate confidence.
			row := storage.MemoryRow{
				ID:                    newMemoryID(),
				Category:              c.Category,
				Title:                 c.Title,
				Content:               c.Content,
				Source:                "llm_extraction",
				Confidence:            0.60, // starts moderate; reinforcement raises it
				Importance:            c.Importance,
				CreatedAt:             now,
				UpdatedAt:             now,
				TagsJSON:              "[]",
				RelatedConversationID: &convID,
			}
			db.SaveMemory(row) //nolint:errcheck
			saved++
		}
	}

	if saved > 0 {
		logstore.Write("debug", fmt.Sprintf("LLM extraction: %d new memories saved", saved),
			map[string]string{"conv": convID[:min(8, len(convID))]})
	}
}

// truncateRunes returns the first n runes of s.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// compressToolResults compresses tool result summaries for the LLM extraction
// prompt. Each result is stripped of excess whitespace and truncated to 150
// runes; total output is capped at 400 runes. Targets 10-20x compression on
// verbose tool outputs.
func compressToolResults(summaries []string) string {
	if len(summaries) == 0 {
		return ""
	}
	n := len(summaries)
	if n > 3 {
		n = 3
	}
	const perResultCap = 150
	const totalCap = 400

	var parts []string
	for _, s := range summaries[:n] {
		// Collapse whitespace runs (JSON formatting, newlines).
		compressed := strings.Join(strings.Fields(s), " ")
		parts = append(parts, truncateRunes(compressed, perResultCap))
	}
	combined := strings.Join(parts, "; ")
	return truncateRunes(combined, totalCap)
}

// stripCodeFence removes ```json ... ``` wrapping if present.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence line.
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		// Remove closing fence.
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}
