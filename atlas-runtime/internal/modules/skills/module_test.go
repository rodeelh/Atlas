package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
