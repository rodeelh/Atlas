package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

func TestToolPolicyBlocksLiveWriteAndPathEscape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	registry := skills.NewRegistry(dir, db, nil)
	loop := &Loop{Skills: registry}

	policy := &ToolPolicy{
		Enabled:             true,
		ApprovedRootPaths:   []string{"/tmp/allowed"},
		AllowedToolPrefixes: []string{"fs."},
		AllowsSensitiveRead: false,
		AllowsLiveWrite:     false,
	}

	if block, ok := loop.blockedByToolPolicy(policy, OAIToolCall{ID: "tc1", Function: OAIFunctionCall{Name: "fs.write_file", Arguments: `{"path":"/tmp/allowed/out.txt","content":"x"}`}}); !ok || !strings.Contains(block.Reason, "live-write") {
		t.Fatalf("expected live-write block, got ok=%v block=%+v", ok, block)
	}
	if block, ok := loop.blockedByToolPolicy(policy, OAIToolCall{ID: "tc2", Function: OAIFunctionCall{Name: "fs.read_file", Arguments: `{"path":"/tmp/other/secret.txt"}`}}); !ok || !strings.Contains(block.Reason, "outside approved") {
		t.Fatalf("expected path block, got ok=%v block=%+v", ok, block)
	}
	if block, ok := loop.blockedByToolPolicy(policy, OAIToolCall{ID: "tc3", Function: OAIFunctionCall{Name: "fs.read_file", Arguments: `{"path":"/tmp/allowed/in.txt"}`}}); ok {
		t.Fatalf("expected allowed read, got block=%+v", block)
	}
}

func TestResolveToolUpgradeShortThenBroad(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	registry := skills.NewRegistry(dir, db, nil)
	registerToolUpgradeTestActions(registry)
	loop := &Loop{Skills: registry}

	short, stage, summary := loop.resolveToolUpgrade(LoopConfig{UserMessage: "Send me a daily weather forecast on Telegram."}, OAIToolCall{Function: OAIFunctionCall{Arguments: `{}`}}, 0)
	if stage != 1 || !strings.Contains(summary, "short relevant") {
		t.Fatalf("expected stage 1 short upgrade, got stage=%d summary=%q", stage, summary)
	}
	if !toolListContains(short, "weather__brief") || !toolListContains(short, "automation__create") {
		t.Fatalf("expected short list to include weather and automation tools, got %v", toolListNames(short))
	}

	broadArgs, _ := json.Marshal(requestToolsArgs{Broad: true})
	broad, stage, summary := loop.resolveToolUpgrade(LoopConfig{UserMessage: "same request"}, OAIToolCall{Function: OAIFunctionCall{Arguments: string(broadArgs)}}, 1)
	if stage != 2 || !strings.Contains(summary, "broad tool surface") {
		t.Fatalf("expected stage 2 broad upgrade, got stage=%d summary=%q", stage, summary)
	}
	if len(broad) <= len(short) {
		t.Fatalf("expected broad list to be larger than short list, got broad=%d short=%d", len(broad), len(short))
	}

	catArgs, _ := json.Marshal(requestToolsArgs{Categories: []string{"automation", "communication"}})
	catTools, stage, _ := loop.resolveToolUpgrade(LoopConfig{UserMessage: "same request"}, OAIToolCall{Function: OAIFunctionCall{Arguments: string(catArgs)}}, 1)
	if stage != 2 || !toolListContains(catTools, "automation__create") || !toolListContains(catTools, "communication__list_channels") {
		t.Fatalf("expected category tools to include automation and communication")
	}
}

func registerToolUpgradeTestActions(registry *skills.Registry) {
	registry.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.create",
			Description: "Create a new Atlas automation.",
			Properties:  map[string]skills.ToolParam{},
		},
		ActionClass: skills.ActionClassLocalWrite,
		FnResult: func(context.Context, json.RawMessage) (skills.ToolResult, error) {
			return skills.OKResult("ok", nil), nil
		},
	})
	registry.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "communication.list_channels",
			Description: "List authorized communication channels.",
			Properties:  map[string]skills.ToolParam{},
		},
		ActionClass: skills.ActionClassRead,
		FnResult: func(context.Context, json.RawMessage) (skills.ToolResult, error) {
			return skills.OKResult("ok", nil), nil
		},
	})
}

func toolListContains(tools []map[string]any, want string) bool {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == want {
			return true
		}
	}
	return false
}

func toolListNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func TestToolPolicyBlocksUnlistedAndSensitiveReadTools(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	registry := skills.NewRegistry(dir, db, nil)
	loop := &Loop{Skills: registry}

	policy := &ToolPolicy{Enabled: true, AllowedToolPrefixes: []string{"weather."}, AllowsSensitiveRead: false, AllowsLiveWrite: true}
	if block, ok := loop.blockedByToolPolicy(policy, OAIToolCall{ID: "tc1", Function: OAIFunctionCall{Name: "finance.quote", Arguments: `{}`}}); !ok || !strings.Contains(block.Reason, "does not allow") {
		t.Fatalf("expected unlisted tool block, got ok=%v block=%+v", ok, block)
	}
	policy.AllowedToolPrefixes = nil
	if block, ok := loop.blockedByToolPolicy(policy, OAIToolCall{ID: "tc2", Function: OAIFunctionCall{Name: "vault.list", Arguments: `{}`}}); !ok || !strings.Contains(block.Reason, "sensitive-read") {
		t.Fatalf("expected sensitive read block, got ok=%v block=%+v", ok, block)
	}
}
