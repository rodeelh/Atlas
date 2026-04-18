package skills

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"atlas-runtime-go/internal/storage"
)

// registerMemory registers the memory.save and memory.recall skill actions.
// These allow the model to write to and query its own memory store inline,
// without waiting for the post-turn extraction pipeline.
func (r *Registry) registerMemory() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "memory.save",
			Description: "Save an important fact to long-term memory. Use this when the user tells you something worth remembering across sessions — preferences, commitments, key facts about projects or workflow. Do NOT use for ephemeral details.",
			Properties: map[string]ToolParam{
				"category": {
					Type:        "string",
					Description: "Memory category",
					Enum:        []string{"profile", "preference", "project", "workflow", "episodic", "commitment", "tool_learning"},
				},
				"title": {
					Type:        "string",
					Description: "Short descriptive title (max 6 words)",
				},
				"content": {
					Type:        "string",
					Description: "One sentence describing the fact to remember",
				},
				"importance": {
					Type:        "number",
					Description: "Importance 0.0–1.0 (commitment = 0.99, strong preference = 0.85, general fact = 0.70)",
				},
			},
			Required: []string{"category", "title", "content", "importance"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Category   string  `json:"category"`
				Title      string  `json:"title"`
				Content    string  `json:"content"`
				Importance float64 `json:"importance"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("memory.save: invalid args: %w", err)
			}
			if p.Category == "" || p.Title == "" || p.Content == "" {
				return "", fmt.Errorf("memory.save: category, title, and content are required")
			}
			validCategories := map[string]bool{
				"profile": true, "preference": true, "project": true,
				"workflow": true, "episodic": true, "commitment": true, "tool_learning": true,
			}
			if !validCategories[p.Category] {
				return "", fmt.Errorf("memory.save: unknown category %q", p.Category)
			}
			p.Importance = math.Min(math.Max(p.Importance, 0.0), 1.0)

			now := time.Now().UTC().Format(time.RFC3339Nano)

			// Check for duplicate — reinforce confidence if it exists.
			existing, err := r.db.FindDuplicateMemory(p.Category, p.Title)
			if err != nil {
				return "", fmt.Errorf("memory.save: db lookup: %w", err)
			}
			if existing != nil {
				upd := *existing
				upd.Content = p.Content
				upd.Confidence = math.Min(existing.Confidence+0.20, 1.0)
				if p.Importance > upd.Importance {
					upd.Importance = p.Importance
				}
				if p.Category == "commitment" {
					upd.IsUserConfirmed = true
				}
				upd.UpdatedAt = now
				if err := r.db.UpdateMemory(upd); err != nil {
					return "", fmt.Errorf("memory.save: update: %w", err)
				}
				return fmt.Sprintf("Updated memory: [%s] %s", p.Category, p.Title), nil
			}

			isConfirmed := p.Category == "commitment"
			row := storage.MemoryRow{
				ID:              newMemoryID(),
				Category:        p.Category,
				Title:           p.Title,
				Content:         p.Content,
				Source:          "model_explicit",
				Confidence:      0.90, // model explicitly chose to save this
				Importance:      p.Importance,
				CreatedAt:       now,
				UpdatedAt:       now,
				IsUserConfirmed: isConfirmed,
				TagsJSON:        `[]`,
			}
			if err := r.db.SaveMemory(row); err != nil {
				return "", fmt.Errorf("memory.save: insert: %w", err)
			}
			return fmt.Sprintf("Saved memory: [%s] %s", p.Category, p.Title), nil
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "memory.recall",
			Description: "Search long-term memory for information relevant to a query. Use this when you need to recall specific facts the user has told you, or to check what you know before asking them to repeat themselves.",
			Properties: map[string]ToolParam{
				"query": {
					Type:        "string",
					Description: "What to search for — keywords, topic, or a natural-language question",
				},
			},
			Required: []string{"query"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("memory.recall: invalid args: %w", err)
			}
			if strings.TrimSpace(p.Query) == "" {
				return "", fmt.Errorf("memory.recall: query is required")
			}

			mems, err := r.db.RelevantMemories(p.Query, 8, nil)
			if err != nil {
				return "", fmt.Errorf("memory.recall: db query: %w", err)
			}
			if len(mems) == 0 {
				return "No relevant memories found for: " + p.Query, nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n\n", len(mems)))
			for _, m := range mems {
				confirmed := ""
				if m.IsUserConfirmed {
					confirmed = " ✓"
				}
				sb.WriteString(fmt.Sprintf("[%s] %s%s\n%s\n\n", m.Category, m.Title, confirmed, m.Content))
			}
			return strings.TrimSpace(sb.String()), nil
		},
	})
}

// newMemoryID generates a random 16-byte hex ID for model-written memories.
func newMemoryID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// ensure time import is used
var _ = time.RFC3339Nano
