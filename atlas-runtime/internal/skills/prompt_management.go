package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (r *Registry) registerPromptManagement() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "atlas.get_operator_prompt",
			Description: "Read Atlas's editable operator prompt layer (MIND.md). Use this before proposing or applying prompt changes.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		FnResult: func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
			path := filepath.Join(r.supportDir, "MIND.md")
			data, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return ToolResult{}, fmt.Errorf("read operator prompt: %w", err)
			}
			content := strings.TrimSpace(string(data))
			if content == "" {
				content = "(empty)"
			}
			return OKResult("Loaded Atlas operator prompt layer.", map[string]any{
				"path":    path,
				"content": content,
			}), nil
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "atlas.update_operator_prompt",
			Description: "Replace Atlas's editable operator prompt layer (MIND.md). This updates the adaptive prompt layer without modifying the built-in safety prompt.",
			Properties: map[string]ToolParam{
				"content": {Description: "Full replacement content for MIND.md", Type: "string"},
				"reason":  {Description: "Optional short reason for the prompt update", Type: "string"},
			},
			Required: []string{"content"},
		},
		PermLevel:   "draft",
		ActionClass: ActionClassLocalWrite,
		FnResult: func(_ context.Context, args json.RawMessage) (ToolResult, error) {
			var req struct {
				Content string `json:"content"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(args, &req); err != nil {
				return ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
			}
			content := strings.TrimSpace(req.Content)
			if content == "" {
				return ToolResult{}, fmt.Errorf("content is required")
			}
			if err := os.MkdirAll(r.supportDir, 0o700); err != nil {
				return ToolResult{}, fmt.Errorf("prepare support dir: %w", err)
			}

			path := filepath.Join(r.supportDir, "MIND.md")
			if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
				_ = os.WriteFile(filepath.Join(r.supportDir, "MIND.md.bak"), existing, 0o600)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return ToolResult{}, fmt.Errorf("write operator prompt: %w", err)
			}

			summary := "Updated Atlas operator prompt layer."
			if reason := strings.TrimSpace(req.Reason); reason != "" {
				summary += " Reason: " + reason
			}
			return OKResult(summary, map[string]any{
				"path":         path,
				"content_size": len(content),
			}), nil
		},
	})
}
