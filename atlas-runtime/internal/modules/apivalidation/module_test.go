package apivalidation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_HistoryReturnsArrayShapeWhenFileMissing(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api-validation/history", nil)
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

func TestModule_HistoryHonorsLimit(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	history := []map[string]any{
		{"id": "first"},
		{"id": "second"},
	}
	data, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api-validation-history.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/api-validation/history?limit=1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("want 1 record, got %d", len(body))
	}
	if body[0]["id"] != "first" {
		t.Fatalf("unexpected first record: %+v", body[0])
	}
}
