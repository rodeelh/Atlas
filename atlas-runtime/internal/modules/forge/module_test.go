package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/config"
	forgesvc "atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func validForgeProposal() forgesvc.ForgeProposal {
	return forgesvc.ForgeProposal{
		ID:          "proposal-1",
		SkillID:     "weather-helper",
		Name:        "Weather Helper",
		Description: "Helpful weather queries",
		Summary:     "Weather integration",
		Status:      "pending",
		SpecJSON: `{
			"id":"weather-helper",
			"name":"Weather Helper",
			"description":"Helpful weather queries",
			"category":"utility",
			"riskLevel":"low",
			"tags":["weather"],
			"actions":[
				{
					"id":"forecast",
					"name":"Forecast",
					"description":"Get the forecast for a place",
					"permissionLevel":"read"
				}
			]
		}`,
		PlansJSON: `[
			{
				"actionID":"forecast",
				"type":"http",
				"httpRequest":{
					"method":"GET",
					"url":"https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
					"authType":"none"
				}
			}
		]`,
		CreatedAt: "2026-04-05T00:00:00Z",
		UpdatedAt: "2026-04-05T00:00:00Z",
	}
}

func TestModule_ListInstalledReturnsArrayShape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/forge/installed", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body == nil {
		t.Fatal("expected [] not null")
	}
}

func TestModule_InstallEnableMovesProposalToInstalled(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proposal := validForgeProposal()
	if err := forgesvc.SaveProposal(dir, proposal); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/forge/proposals/proposal-1/install-enable", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	installed := forgesvc.ListInstalled(dir)
	if len(installed) != 1 {
		t.Fatalf("expected one installed record, got %d", len(installed))
	}

	states := featuresState(t, dir)
	if states["weather-helper"] != "enabled" {
		t.Fatalf("expected enabled state, got %#v", states)
	}

	reloaded := forgesvc.GetProposal(dir, "proposal-1")
	if reloaded == nil || reloaded.Status != "enabled" {
		t.Fatalf("expected proposal status to be enabled after successful install, got %+v", reloaded)
	}
}

func TestModule_ListInstalledReflectsSavedForgeInstalledRecords(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proposal := validForgeProposal()
	if err := forgesvc.SaveProposal(dir, proposal); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	installReq := httptest.NewRequest(http.MethodPost, "/forge/proposals/proposal-1/install", nil)
	installRR := httptest.NewRecorder()
	r.ServeHTTP(installRR, installReq)
	if installRR.Code != http.StatusOK {
		t.Fatalf("install want 200, got %d body=%s", installRR.Code, installRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/forge/installed", nil)
	listRR := httptest.NewRecorder()
	r.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list want 200, got %d body=%s", listRR.Code, listRR.Body.String())
	}

	var installed []map[string]any
	if err := json.NewDecoder(listRR.Body).Decode(&installed); err != nil {
		t.Fatalf("decode installed body: %v", err)
	}
	if len(installed) != 1 {
		t.Fatalf("expected one forge installed record, got %d", len(installed))
	}
	manifest, _ := installed[0]["manifest"].(map[string]any)
	if manifest["source"] != "forge" {
		t.Fatalf("expected forge source, got %+v", manifest)
	}
	if manifest["lifecycleState"] != "installed" {
		t.Fatalf("expected lifecycleState installed, got %v", manifest["lifecycleState"])
	}
	actions, _ := installed[0]["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("expected one installed action, got %+v", installed[0])
	}
	action, _ := actions[0].(map[string]any)
	if enabled, _ := action["isEnabled"].(bool); enabled {
		t.Fatalf("expected installed skill actions to be disabled until enabled, got %+v", action)
	}
}

func TestModule_InstallEnableWorkflowProposalCreatesWorkflowTarget(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proposal := forgesvc.ForgeProposal{
		ID:          "proposal-workflow-1",
		SkillID:     "theme-pdf",
		Name:        "Theme PDF",
		Description: "Generate themed text and save it as a PDF.",
		Summary:     "Workflow-backed PDF generation",
		Status:      "pending",
		SpecJSON: `{
			"id":"theme-pdf",
			"name":"Theme PDF",
			"description":"Generate themed text and save it as a PDF.",
			"category":"productivity",
			"riskLevel":"low",
			"tags":["workflow","pdf"],
			"actions":[
				{"id":"draft","name":"Draft","description":"Draft themed content","permissionLevel":"read"},
				{"id":"save-pdf","name":"Save PDF","description":"Save the PDF","permissionLevel":"draft"},
				{"id":"done","name":"Done","description":"Return completion summary","permissionLevel":"read"}
			]
		}`,
		PlansJSON: `[
			{"actionID":"draft","type":"llm.generate","workflowStep":{"prompt":"Write a concise brief about {input.theme}."}},
			{"actionID":"save-pdf","type":"atlas.tool","workflowStep":{"action":"fs.create_pdf","args":{"path":"{input.path}","title":"{input.theme}","content":"{steps.draft.output}"}}},
			{"actionID":"done","type":"return","workflowStep":{"value":"Saved themed PDF to {input.path}"}}
		]`,
		CreatedAt: "2026-04-05T00:00:00Z",
		UpdatedAt: "2026-04-05T00:00:00Z",
	}
	if err := forgesvc.SaveProposal(dir, proposal); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/forge/proposals/proposal-workflow-1/install-enable", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	installed := forgesvc.ListInstalled(dir)
	if len(installed) != 1 {
		t.Fatalf("expected one installed record, got %+v", installed)
	}
	target, _ := installed[0]["target"].(map[string]any)
	if target["type"] != "workflow" || target["ref"] != "theme-pdf.v1" {
		t.Fatalf("expected workflow target, got %+v", target)
	}
}

func TestModule_WorkflowBackedInstallRegistersCallableAction(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	workflowsManifest := map[string]any{
		"id":          "theme-pdf.v1",
		"name":        "Theme PDF",
		"description": "Workflow-backed Forge skill",
		"isEnabled":   true,
	}
	if err := db.SaveWorkflow(storage.WorkflowRow{
		ID:             "theme-pdf.v1",
		Name:           "Theme PDF",
		DefinitionJSON: mustJSON(workflowsManifest),
		IsEnabled:      true,
		CreatedAt:      "2026-04-05T00:00:00Z",
		UpdatedAt:      "2026-04-05T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}
	registry.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "workflow.run",
			Description: "Run workflow",
			Properties: map[string]skills.ToolParam{
				"id":              {Description: "Workflow id", Type: "string"},
				"inputValuesJSON": {Description: "Inputs", Type: "string"},
			},
		},
		PermLevel: "execute",
		FnResult: func(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
			var payload map[string]any
			if err := json.Unmarshal(args, &payload); err != nil {
				return skills.ToolResult{}, err
			}
			return skills.OKResult("workflow invoked", map[string]any{
				"id":              payload["id"],
				"inputValuesJSON": payload["inputValuesJSON"],
			}), nil
		},
	})

	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	record := map[string]any{
		"id": "theme-pdf",
		"manifest": map[string]any{
			"id":             "theme-pdf",
			"name":           "Theme PDF",
			"description":    "Generate themed text and save it as a PDF.",
			"lifecycleState": "enabled",
			"source":         "forge",
		},
		"actions": []map[string]any{
			{
				"id":              "theme-pdf.run",
				"name":            "Run Theme PDF",
				"description":     "Run the workflow-backed skill.",
				"permissionLevel": "execute",
				"approvalPolicy":  "auto_approve",
				"isEnabled":       true,
			},
		},
		"target": map[string]any{
			"type": "workflow",
			"ref":  "theme-pdf.v1",
		},
	}
	if err := forgesvc.SaveInstalled(dir, record); err != nil {
		t.Fatalf("SaveInstalled: %v", err)
	}

	module.registerInstalledWorkflowActions()

	result, err := registry.Execute(context.Background(), "theme-pdf.run", json.RawMessage(`{"inputValuesJSON":"{\"theme\":\"Atlas\"}"}`))
	if err != nil {
		t.Fatalf("registry.Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if result.Artifacts["id"] != "theme-pdf.v1" {
		t.Fatalf("expected workflow target ref, got %+v", result.Artifacts)
	}
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestModule_RejectReturns500WhenStatusPersistenceFails(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proposal := validForgeProposal()
	if err := forgesvc.SaveProposal(dir, proposal); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod dir: %v", err)
	}
	defer os.Chmod(dir, 0o700)

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/forge/proposals/proposal-1/reject", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d body=%s", rr.Code, rr.Body.String())
	}

	reloaded := forgesvc.GetProposal(dir, "proposal-1")
	if reloaded == nil || reloaded.Status != "pending" {
		t.Fatalf("proposal status should remain pending after failed reject write, got %+v", reloaded)
	}
}

func TestModule_InstallEnableReturns500WhenCodegenFails(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		nil,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	registry := skills.NewRegistry(dir, db, (*browser.Manager)(nil))
	module := New(dir, forgesvc.NewService(dir), nil, registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proposal := validForgeProposal()
	proposal.SpecJSON = `{`
	if err := forgesvc.SaveProposal(dir, proposal); err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/forge/proposals/proposal-1/install-enable", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d body=%s", rr.Code, rr.Body.String())
	}

	if installed := forgesvc.ListInstalled(dir); len(installed) != 0 {
		t.Fatalf("expected no installed records after failed codegen, got %d", len(installed))
	}

	reloaded := forgesvc.GetProposal(dir, "proposal-1")
	if reloaded == nil || reloaded.Status != "pending" {
		t.Fatalf("proposal status should remain pending after failed install, got %+v", reloaded)
	}

	if _, err := os.Stat(filepath.Join(dir, "go-skill-states.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no lifecycle state file after failed install, stat err=%v", err)
	}
}

func featuresState(t *testing.T, dir string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "go-skill-states.json"))
	if err != nil {
		t.Fatalf("ReadFile(go-skill-states.json): %v", err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal(go-skill-states.json): %v", err)
	}
	return out
}
