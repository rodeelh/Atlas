package agent

import (
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
