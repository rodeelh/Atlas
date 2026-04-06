package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	runtimeengine "atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_StatusReturnsCurrentShape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(
		runtimeengine.NewManager(dir, filepath.Join(dir, "models")),
		runtimeengine.NewManager(dir, filepath.Join(dir, "models-router")),
		config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json")),
	)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/engine/status", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["running"]; !ok {
		t.Fatalf("unexpected payload: %+v", body)
	}
}

func TestModule_ModelsReturnsArrayShape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(
		runtimeengine.NewManager(dir, filepath.Join(dir, "models")),
		runtimeengine.NewManager(dir, filepath.Join(dir, "models-router")),
		config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json")),
	)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/engine/models", nil)
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

func TestModule_StartRejectsMissingModel(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(
		runtimeengine.NewManager(dir, filepath.Join(dir, "models")),
		runtimeengine.NewManager(dir, filepath.Join(dir, "models-router")),
		config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json")),
	)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/engine/start", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}
