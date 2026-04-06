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
	forgesvc "atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

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

	proposal := forgesvc.ForgeProposal{
		ID:          "proposal-1",
		SkillID:     "weather-helper",
		Name:        "Weather Helper",
		Description: "Helpful weather queries",
		Summary:     "Weather integration",
		Status:      "pending",
		SpecJSON:    `{"name":"Weather Helper"}`,
		PlansJSON:   `[]`,
		CreatedAt:   "2026-04-05T00:00:00Z",
		UpdatedAt:   "2026-04-05T00:00:00Z",
	}
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
