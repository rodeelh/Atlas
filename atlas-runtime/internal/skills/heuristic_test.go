package skills

import (
	"context"
	"encoding/json"
	"strings"
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

func TestToolDefsForGroupsForMessageNarrowsWeatherGroup(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)
	tools := registry.ToolDefsForGroupsForMessage([]string{"weather"}, "What will the weather be in Orlando tomorrow?")
	names := toolNames(tools)
	if len(names) >= 9 {
		t.Fatalf("expected narrowed weather tool set, got %d tools: %v", len(names), names)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "weather__forecast") {
		t.Fatalf("expected forecast-style weather tool in narrowed set, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesFilesystemForCreateAndSaveFiles(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("I want Atlas to create and save files for me.")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__write_file") {
		t.Fatalf("expected fs.write_file to be selected, got %v", names)
	}
	if !hasToolName(tools, "fs__create_directory") {
		t.Fatalf("expected fs.create_directory to be selected, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesFilesystemForCommonTypoFileds(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("Time to super charge atlas productivity... I want atlas to be able to create and save fileds")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__write_file") {
		t.Fatalf("expected fs.write_file to be selected for typo input, got %v", names)
	}
}

func TestSelectiveToolDefsIncludesPDFToolForCreatePDFRequest(t *testing.T) {
	registry := NewRegistry(t.TempDir(), nil, nil)

	tools := registry.SelectiveToolDefs("Create a PDF with today's project summary.")
	names := toolNames(tools)

	if !hasToolName(tools, "fs__create_pdf") {
		t.Fatalf("expected fs.create_pdf to be selected, got %v", names)
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
