package automations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

func TestAgentUpsertCreatesWhenMissing(t *testing.T) {
	module := New(t.TempDir())
	args, err := json.Marshal(map[string]any{
		"name":     "Daily Weather",
		"prompt":   "Send me the Orlando forecast.",
		"schedule": "daily 08:00",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpsert(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpsert(create): %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success result: %+v", res)
	}
	if got := res.Artifacts["operation"]; got != "created" {
		t.Fatalf("operation = %v, want created", got)
	}
	items, err := module.listDefinitions()
	if err != nil {
		t.Fatalf("listDefinitions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 automation, got %d", len(items))
	}
}

func TestAgentUpsertUpdatesExistingByName(t *testing.T) {
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
	created, err := module.createDefinition(agentActionTestItem("Daily Weather", "Send me the Orlando forecast.", "daily 08:00"))
	if err != nil {
		t.Fatalf("createDefinition: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"id":       created.ID,
		"name":     created.Name,
		"prompt":   "Send me the Orlando forecast in a friendlier tone.",
		"enabled":  true,
		"schedule": "daily 08:00",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpsert(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpsert(update): %v", err)
	}
	if got := res.Artifacts["operation"]; got != "updated" {
		t.Fatalf("operation = %v, want updated", got)
	}
	item, err := module.resolveAutomation(automationRefArgs{Name: "Daily Weather"}, false)
	if err != nil {
		t.Fatalf("resolveAutomation: %v", err)
	}
	if item.Prompt != "Send me the Orlando forecast in a friendlier tone." {
		t.Fatalf("prompt = %q", item.Prompt)
	}
}

func agentActionTestItem(name, prompt, schedule string) features.GremlinItem {
	return features.GremlinItem{
		Name:        name,
		Prompt:      prompt,
		ScheduleRaw: schedule,
		IsEnabled:   true,
		SourceType:  "agent-test",
	}
}
