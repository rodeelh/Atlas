package automations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

func seedAgentDefinition(t *testing.T, db *storage.DB, id, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                id,
		Name:              name,
		Role:              "Researcher",
		Mission:           "Own scheduled specialist tasks",
		AllowedSkillsJSON: `["web.search"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}
}

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

func TestAgentUpsertCreatesWorkflowBackedAutomation(t *testing.T) {
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

	if err := db.SaveWorkflow(storage.WorkflowRow{
		ID:             "wf-brief",
		Name:           "Briefing Workflow",
		DefinitionJSON: `{"id":"wf-brief","name":"Briefing Workflow","promptTemplate":"Build the briefing","isEnabled":true}`,
		IsEnabled:      true,
		CreatedAt:      "2026-04-05T00:00:00Z",
		UpdatedAt:      "2026-04-05T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"name":                    "Daily Briefing Automation",
		"workflowID":              "Briefing Workflow",
		"workflowInputValuesJSON": `{"city":"Orlando"}`,
		"schedule":                "daily 08:00",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpsert(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpsert(create workflow target): %v", err)
	}
	if got := res.Artifacts["operation"]; got != "created" {
		t.Fatalf("operation = %v, want created", got)
	}
	if res.Summary != `Automation "Daily Briefing Automation" created and linked to workflow "Briefing Workflow".` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["automation"].(map[string]any)
	if !ok {
		t.Fatalf("expected automation artifact map, got %#v", res.Artifacts["automation"])
	}
	if artifact["targetDisplayName"] != "Briefing Workflow" {
		t.Fatalf("unexpected targetDisplayName: %#v", artifact["targetDisplayName"])
	}

	item, err := module.resolveAutomation(automationRefArgs{Name: "Daily Briefing Automation"}, false)
	if err != nil {
		t.Fatalf("resolveAutomation: %v", err)
	}
	if item.ExecutableTarget == nil {
		t.Fatalf("expected workflow target, got nil")
	}
	if item.ExecutableTarget.Type != "workflow" || item.ExecutableTarget.Ref != "wf-brief" {
		t.Fatalf("unexpected target: %+v", item.ExecutableTarget)
	}
	if item.WorkflowInputValues["city"] != "Orlando" {
		t.Fatalf("unexpected workflow inputs: %+v", item.WorkflowInputValues)
	}
}

func TestAgentUpdateCanConvertAutomationToWorkflowTarget(t *testing.T) {
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

	if err := db.SaveWorkflow(storage.WorkflowRow{
		ID:             "wf-brief",
		Name:           "Briefing Workflow",
		DefinitionJSON: `{"id":"wf-brief","name":"Briefing Workflow","promptTemplate":"Build the briefing","isEnabled":true}`,
		IsEnabled:      true,
		CreatedAt:      "2026-04-05T00:00:00Z",
		UpdatedAt:      "2026-04-05T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	created, err := module.createDefinition(agentActionTestItem("Daily Weather", "Send me the Orlando forecast.", "daily 08:00"))
	if err != nil {
		t.Fatalf("createDefinition: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"id":                      created.ID,
		"workflowID":              "wf-brief",
		"workflowInputValuesJSON": `{"city":"Miami"}`,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpdate(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpdate(workflow target): %v", err)
	}
	if res.Summary != `Automation "Daily Weather" updated and linked to workflow "Briefing Workflow".` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["automation"].(map[string]any)
	if !ok {
		t.Fatalf("expected automation artifact map, got %#v", res.Artifacts["automation"])
	}
	if artifact["targetDisplayName"] != "Briefing Workflow" {
		t.Fatalf("unexpected targetDisplayName: %#v", artifact["targetDisplayName"])
	}

	item, err := module.resolveAutomation(automationRefArgs{ID: created.ID}, false)
	if err != nil {
		t.Fatalf("resolveAutomation: %v", err)
	}
	if item.ExecutableTarget == nil {
		t.Fatalf("expected workflow target, got nil")
	}
	if item.ExecutableTarget.Type != "workflow" || item.ExecutableTarget.Ref != "wf-brief" {
		t.Fatalf("unexpected target: %+v", item.ExecutableTarget)
	}
	if item.WorkflowInputValues["city"] != "Miami" {
		t.Fatalf("unexpected workflow inputs: %+v", item.WorkflowInputValues)
	}
}

func TestAgentUpsertCreatesAgentBackedAutomation(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	seedAgentDefinition(t, db, "scout", "Research Scout")

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
		"name":     "Weekly Scout Check-In",
		"agentID":  "Research Scout",
		"task":     "Review the latest market shifts and summarize the top changes.",
		"goal":     "Surface the three most important changes.",
		"schedule": "every Monday at 09:00",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpsert(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpsert(create agent target): %v", err)
	}
	if got := res.Artifacts["operation"]; got != "created" {
		t.Fatalf("operation = %v, want created", got)
	}
	if res.Summary != `Automation "Weekly Scout Check-In" created and linked to team member "Research Scout".` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["automation"].(map[string]any)
	if !ok {
		t.Fatalf("expected automation artifact map, got %#v", res.Artifacts["automation"])
	}
	if artifact["targetDisplayName"] != "Research Scout" {
		t.Fatalf("unexpected targetDisplayName: %#v", artifact["targetDisplayName"])
	}

	item, err := module.resolveAutomation(automationRefArgs{Name: "Weekly Scout Check-In"}, false)
	if err != nil {
		t.Fatalf("resolveAutomation: %v", err)
	}
	if item.ExecutableTarget == nil {
		t.Fatalf("expected agent target, got nil")
	}
	if item.ExecutableTarget.Type != "agent" || item.ExecutableTarget.Ref != "scout" {
		t.Fatalf("unexpected target: %+v", item.ExecutableTarget)
	}
	if item.Prompt != "Review the latest market shifts and summarize the top changes." {
		t.Fatalf("unexpected stored task prompt: %q", item.Prompt)
	}
	if item.WorkflowInputValues["goal"] != "Surface the three most important changes." {
		t.Fatalf("unexpected agent goal inputs: %+v", item.WorkflowInputValues)
	}
}

func TestAgentUpdateCanConvertAutomationToAgentTarget(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	seedAgentDefinition(t, db, "scout", "Research Scout")

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
		"id":      created.ID,
		"agentID": "scout",
		"task":    "Review tomorrow's travel weather and flag anything risky.",
		"goal":    "Keep me aware of weather-related issues.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	res, err := module.agentUpdate(context.Background(), args)
	if err != nil {
		t.Fatalf("agentUpdate(agent target): %v", err)
	}
	if res.Summary != `Automation "Daily Weather" updated and linked to team member "Research Scout".` {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	artifact, ok := res.Artifacts["automation"].(map[string]any)
	if !ok {
		t.Fatalf("expected automation artifact map, got %#v", res.Artifacts["automation"])
	}
	if artifact["targetDisplayName"] != "Research Scout" {
		t.Fatalf("unexpected targetDisplayName: %#v", artifact["targetDisplayName"])
	}

	item, err := module.resolveAutomation(automationRefArgs{ID: created.ID}, false)
	if err != nil {
		t.Fatalf("resolveAutomation: %v", err)
	}
	if item.ExecutableTarget == nil {
		t.Fatalf("expected agent target, got nil")
	}
	if item.ExecutableTarget.Type != "agent" || item.ExecutableTarget.Ref != "scout" {
		t.Fatalf("unexpected target: %+v", item.ExecutableTarget)
	}
	if item.Prompt != "Review tomorrow's travel weather and flag anything risky." {
		t.Fatalf("unexpected task prompt: %q", item.Prompt)
	}
	if item.WorkflowInputValues["goal"] != "Keep me aware of weather-related issues." {
		t.Fatalf("unexpected goal inputs: %+v", item.WorkflowInputValues)
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
