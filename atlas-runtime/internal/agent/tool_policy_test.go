package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

// TestRequestToolsCategoriesIncludesTeam verifies that the "team" category is
// present in the request_tools enum so the model can self-request team tools
// in lazy/smart-mode turns (Phase A1).
func TestRequestToolsCategoriesIncludesTeam(t *testing.T) {
	outer := RequestToolsDef()

	// RequestToolsDef returns {"type":"function","function":{...}}
	fn, ok := outer["function"].(map[string]any)
	if !ok {
		t.Fatal("RequestToolsDef: missing function map")
	}

	// Drill into function.parameters.properties.categories.items.enum
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("requestToolsDef: missing parameters map")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("requestToolsDef: missing properties map")
	}
	cats, ok := props["categories"].(map[string]any)
	if !ok {
		t.Fatal("requestToolsDef: missing categories entry")
	}
	items, ok := cats["items"].(map[string]any)
	if !ok {
		t.Fatal("requestToolsDef: missing items in categories")
	}
	enum, ok := items["enum"].([]string)
	if !ok {
		t.Fatal("requestToolsDef: enum is not []string")
	}

	found := false
	for _, v := range enum {
		if v == "team" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("request_tools categories enum is missing \"team\"; got: %v", enum)
	}
}

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

// TestToolPolicyBarePrefix verifies that bare-prefix patterns in AllowedToolPrefixes
// use the same canonical matching as team allowedSkills: "fs" matches "fs.read_file"
// but must NOT match "filesystem.check" (false-positive prevention).
func TestToolPolicyBarePrefix(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	registry := skills.NewRegistry(dir, db, nil)
	loop := &Loop{Skills: registry}

	// Use weather (no path check) and a fictional "filesystem" namespace to
	// test the core pattern semantics without triggering the fs-root guard.
	weatherPolicy := &ToolPolicy{Enabled: true, AllowedToolPrefixes: []string{"weather"}, AllowsLiveWrite: true, AllowsSensitiveRead: true}

	// Bare "weather" must allow "weather.current"
	if block, ok := loop.blockedByToolPolicy(weatherPolicy, OAIToolCall{ID: "tc1", Function: OAIFunctionCall{Name: "weather.current", Arguments: `{}`}}); ok {
		t.Fatalf("expected weather.current to be allowed by bare prefix 'weather', got blocked: %+v", block)
	}

	// Bare "weather" must NOT match "weatherman.forecast" (different namespace)
	if _, ok := loop.blockedByToolPolicy(weatherPolicy, OAIToolCall{ID: "tc2", Function: OAIFunctionCall{Name: "weatherman.forecast", Arguments: `{}`}}); !ok {
		t.Fatalf("expected weatherman.forecast to be blocked by bare prefix 'weather'")
	}

	// fs tools need approved root paths — verify bare "fs" allows fs.read_file once a root is set
	fsPolicy := &ToolPolicy{
		Enabled:             true,
		AllowedToolPrefixes: []string{"fs"},
		ApprovedRootPaths:   []string{"/tmp"},
		AllowsLiveWrite:     true,
		AllowsSensitiveRead: true,
	}
	if block, ok := loop.blockedByToolPolicy(fsPolicy, OAIToolCall{ID: "tc3", Function: OAIFunctionCall{Name: "fs.read_file", Arguments: `{"path":"/tmp/x"}`}}); ok {
		t.Fatalf("expected fs.read_file to be allowed by bare prefix 'fs' with approved root, got blocked: %+v", block)
	}

	// All four equivalent pattern forms produce the same result for weather.current
	for _, pattern := range []string{"weather", "weather.", "weather.*", "weather.current"} {
		p := &ToolPolicy{Enabled: true, AllowedToolPrefixes: []string{pattern}, AllowsLiveWrite: true, AllowsSensitiveRead: true}
		if block, blocked := loop.blockedByToolPolicy(p, OAIToolCall{ID: "tc4", Function: OAIFunctionCall{Name: "weather.current", Arguments: `{}`}}); blocked {
			t.Errorf("pattern %q should allow weather.current but it was blocked: %+v", pattern, block)
		}
	}
}
