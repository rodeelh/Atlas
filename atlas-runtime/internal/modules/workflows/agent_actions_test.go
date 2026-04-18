package workflows

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

func TestAgentCreateReturnsFriendlyWorkflowArtifact(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"name":           "Briefing Workflow",
		"promptTemplate": "Build the briefing",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentCreate(context.Background(), args)
	if err != nil {
		t.Fatalf("agentCreate: %v", err)
	}
	if res.Summary != `Workflow "Briefing Workflow" created.` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("expected workflow artifact map, got %#v", res.Artifacts["workflow"])
	}
	if artifact["displayName"] != "Briefing Workflow" {
		t.Fatalf("unexpected displayName: %#v", artifact["displayName"])
	}
	if artifact["id"] == "" {
		t.Fatalf("expected canonical workflow id, got %#v", artifact["id"])
	}
}

func TestAgentGetReturnsFriendlyWorkflowArtifact(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	def, err := module.createDefinition(map[string]any{
		"name":           "Briefing Workflow",
		"promptTemplate": "Build the briefing",
		"isEnabled":      true,
	})
	if err != nil {
		t.Fatalf("createDefinition: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"id": def["id"],
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentGet(context.Background(), args)
	if err != nil {
		t.Fatalf("agentGet: %v", err)
	}
	if res.Summary != `Workflow "Briefing Workflow" loaded.` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("expected workflow artifact map, got %#v", res.Artifacts["workflow"])
	}
	if artifact["displayName"] != "Briefing Workflow" {
		t.Fatalf("unexpected displayName: %#v", artifact["displayName"])
	}
	if artifact["id"] != def["id"] {
		t.Fatalf("expected canonical workflow id %v, got %#v", def["id"], artifact["id"])
	}
}
