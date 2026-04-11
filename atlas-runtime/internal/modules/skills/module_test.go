package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_ListSkillsReturnsCurrentShape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	req := httptest.NewRequest(http.MethodGet, "/skills", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body == nil {
		t.Fatal("expected [] not null")
	}
	var foundAutomation, foundWorkflow, foundCommunication bool
	for _, rec := range body {
		manifest, _ := rec["manifest"].(map[string]any)
		switch manifest["id"] {
		case "gremlin-management":
			t.Fatal("legacy gremlin-management should not be visible in the skills catalog")
		case "automation-control":
			foundAutomation = true
			assertActionPolicy(t, rec, "automation.run", "auto_approve")
		case "workflow-control":
			foundWorkflow = true
			assertActionPolicy(t, rec, "workflow.run", "auto_approve")
		case "communication-bridge":
			foundCommunication = true
			assertActionPolicy(t, rec, "communication.send_message", "auto_approve")
		}
	}
	if !foundAutomation {
		t.Fatal("expected automation-control skill")
	}
	if !foundWorkflow {
		t.Fatal("expected workflow-control skill")
	}
	if !foundCommunication {
		t.Fatal("expected communication-bridge skill")
	}
}

func TestModule_ListSkillsIncludesWorkflowBackedForgeInstall(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if err := os.WriteFile(filepath.Join(dir, "forge-installed.json"), []byte(`[
		{
			"id": "theme-pdf",
			"manifest": {
				"id": "theme-pdf",
				"name": "Theme PDF",
				"description": "Generate themed text and save it as a PDF.",
				"lifecycleState": "enabled",
				"riskLevel": "low",
				"category": "productivity",
				"source": "forge",
				"tags": ["workflow","pdf"]
			},
			"actions": [
				{
					"id": "theme-pdf.run",
					"name": "Run Theme PDF",
					"description": "Execute the workflow-backed Theme PDF capability.",
					"permissionLevel": "execute",
					"approvalPolicy": "auto_approve",
					"isEnabled": true
				}
			],
			"target": {"type":"workflow","ref":"theme-to-pdf.v1"}
		}
	]`), 0o600); err != nil {
		t.Fatalf("Write forge-installed.json: %v", err)
	}

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	req := httptest.NewRequest(http.MethodGet, "/skills", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	found := false
	for _, rec := range body {
		manifest, _ := rec["manifest"].(map[string]any)
		if manifest["id"] == "theme-pdf" {
			found = true
			if manifest["source"] != "forge" {
				t.Fatalf("expected forge source, got %+v", manifest)
			}
		}
	}
	if !found {
		t.Fatal("expected workflow-backed forge install in /skills catalog")
	}
}

func TestModule_ListCapabilitiesReturnsUnifiedInventory(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	workflowDef := map[string]any{
		"name":          "Theme To PDF",
		"description":   "Generate themed content and save it as a PDF.",
		"artifactTypes": []string{"file.pdf"},
		"tags":          []string{"pdf", "workflow"},
		"steps": []map[string]any{
			{"id": "draft", "type": "llm.generate"},
			{"id": "save", "type": "atlas.tool"},
		},
	}
	workflowJSON, _ := json.Marshal(workflowDef)
	if err := db.SaveWorkflow(storage.WorkflowRow{
		ID:             "theme-to-pdf.v1",
		Name:           "Theme To PDF",
		DefinitionJSON: string(workflowJSON),
		IsEnabled:      true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	workflowID := "theme-to-pdf.v1"
	if err := db.SaveAutomation(storage.AutomationRow{
		ID:          "weekly-theme-pdf",
		Name:        "Weekly Theme PDF",
		Emoji:       "⚡",
		Prompt:      "Generate the weekly theme pdf",
		ScheduleRaw: "every friday at 18:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   now,
		UpdatedAt:   now,
		WorkflowID:  &workflowID,
		TagsJSON:    `["weekly","pdf"]`,
	}); err != nil {
		t.Fatalf("SaveAutomation: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "forge-installed.json"), []byte(`[
		{
			"id": "theme-pdf",
			"manifest": {
				"id": "theme-pdf",
				"name": "Theme PDF",
				"description": "Generate themed text and save it as a PDF.",
				"lifecycleState": "enabled",
				"category": "productivity",
				"source": "forge",
				"tags": ["workflow","pdf"]
			},
			"actions": [
				{
					"id": "theme-pdf.run",
					"name": "Run Theme PDF",
					"description": "Execute the workflow-backed Theme PDF capability.",
					"permissionLevel": "execute",
					"approvalPolicy": "auto_approve",
					"isEnabled": true
				}
			],
			"target": {
				"type": "workflow",
				"ref": "theme-to-pdf.v1"
			}
		}
	]`), 0o600); err != nil {
		t.Fatalf("Write forge-installed.json: %v", err)
	}

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected capabilities inventory")
	}

	var foundSkill, foundWorkflow, foundAutomation, foundForgeWorkflowSkill bool
	for _, rec := range body {
		id, _ := rec["id"].(string)
		kind, _ := rec["kind"].(string)
		target, _ := rec["target"].(map[string]any)
		switch id {
		case "file-system":
			foundSkill = true
			if kind != "skill" {
				t.Fatalf("file-system kind = %q, want skill", kind)
			}
			if target["type"] != "skill" {
				t.Fatalf("file-system target.type = %v, want skill", target["type"])
			}
			artifactTypes, _ := rec["artifactTypes"].([]any)
			if len(artifactTypes) == 0 {
				t.Fatalf("expected file-system artifact types, got %+v", rec)
			}
			requiredRoots, _ := rec["requiredRoots"].([]any)
			if len(requiredRoots) == 0 {
				t.Fatalf("expected file-system required roots, got %+v", rec)
			}
		case "theme-to-pdf.v1":
			foundWorkflow = true
			if kind != "workflow" {
				t.Fatalf("theme-to-pdf.v1 kind = %q, want workflow", kind)
			}
			if target["type"] != "workflow" {
				t.Fatalf("theme-to-pdf.v1 target.type = %v, want workflow", target["type"])
			}
		case "weekly-theme-pdf":
			foundAutomation = true
			if kind != "automation" {
				t.Fatalf("weekly-theme-pdf kind = %q, want automation", kind)
			}
			if target["type"] != "workflow" || target["ref"] != workflowID {
				t.Fatalf("weekly-theme-pdf target = %+v, want workflow %q", target, workflowID)
			}
		case "theme-pdf":
			foundForgeWorkflowSkill = true
			if kind != "skill" {
				t.Fatalf("theme-pdf kind = %q, want skill", kind)
			}
			if target["type"] != "workflow" || target["ref"] != workflowID {
				t.Fatalf("theme-pdf target = %+v, want workflow %q", target, workflowID)
			}
		}
	}
	if !foundSkill {
		t.Fatal("expected built-in skill capability entry")
	}
	if !foundWorkflow {
		t.Fatal("expected workflow capability entry")
	}
	if !foundAutomation {
		t.Fatal("expected automation capability entry")
	}
	if !foundForgeWorkflowSkill {
		t.Fatal("expected workflow-backed forge skill capability entry")
	}
}

func assertActionPolicy(t *testing.T, rec map[string]any, actionID, want string) {
	t.Helper()
	actions, _ := rec["actions"].([]any)
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		if action["id"] == actionID {
			if got, _ := action["approvalPolicy"].(string); got != want {
				t.Fatalf("%s approvalPolicy = %q, want %q", actionID, got, want)
			}
			return
		}
	}
	t.Fatalf("missing action %s", actionID)
}

func TestModule_AddFsRootPersistsRoot(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	req := httptest.NewRequest(http.MethodPost, "/skills/file-system/roots", strings.NewReader(`{"path":"/tmp/example"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "go-fs-roots.json"))
	if err != nil {
		t.Fatalf("ReadFile(go-fs-roots.json): %v", err)
	}
	if !strings.Contains(string(data), "/tmp/example") {
		t.Fatalf("expected saved fs root, got %s", string(data))
	}
}
