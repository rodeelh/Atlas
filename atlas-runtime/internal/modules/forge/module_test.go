package forge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
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

func TestModule_ListInstalledReflectsLiveForgeSkillState(t *testing.T) {
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

	var installed []features.SkillRecord
	if err := json.NewDecoder(listRR.Body).Decode(&installed); err != nil {
		t.Fatalf("decode installed body: %v", err)
	}
	if len(installed) != 1 {
		t.Fatalf("expected one live forge skill, got %d", len(installed))
	}
	if installed[0].Manifest.Source != "forge" {
		t.Fatalf("expected forge source, got %+v", installed[0].Manifest)
	}
	if installed[0].Manifest.LifecycleState != "installed" {
		t.Fatalf("expected lifecycleState installed, got %q", installed[0].Manifest.LifecycleState)
	}
	if len(installed[0].Actions) != 1 || installed[0].Actions[0].IsEnabled {
		t.Fatalf("expected installed skill actions to be disabled until enabled, got %+v", installed[0].Actions)
	}
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
