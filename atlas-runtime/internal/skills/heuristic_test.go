package skills

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSelectiveToolDefsRecurringDeliveryIncludesAutomationAndCommunication(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "automation.create",
			Description: "Create a new Atlas automation.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassLocalWrite,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})
	registry.RegisterExternal(SkillEntry{
		Def: ToolDef{
			Name:        "communication.list_channels",
			Description: "List authorized communication channels.",
			Properties:  map[string]ToolParam{},
		},
		ActionClass: ActionClassRead,
		FnResult: func(context.Context, json.RawMessage) (ToolResult, error) {
			return OKResult("ok", nil), nil
		},
	})

	tools := registry.SelectiveToolDefs("Send me a friendly detailed Orlando weather forecast every day at 8 AM on Telegram.")
	if !hasToolName(tools, "automation__create") {
		t.Fatalf("expected automation.create to be selected, got %v", toolNames(tools))
	}
	if !hasToolName(tools, "communication__list_channels") {
		t.Fatalf("expected communication.list_channels to be selected, got %v", toolNames(tools))
	}
}

func hasToolName(tools []map[string]any, want string) bool {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == want {
			return true
		}
	}
	return false
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}
