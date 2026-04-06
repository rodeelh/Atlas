package usage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_SummaryReturnsArrayShapeOnEmptyDB(t *testing.T) {
	db := openTestDB(t)
	r := newUsageRouter(t, db)

	req := httptest.NewRequest(http.MethodGet, "/usage/summary", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		ByModel     []any `json:"byModel"`
		DailySeries []any `json:"dailySeries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.ByModel == nil || body.DailySeries == nil {
		t.Fatalf("expected [] fields, got %+v", body)
	}
}

func TestModule_SummaryHonorsExplicitDays(t *testing.T) {
	db := openTestDB(t)
	r := newUsageRouter(t, db)

	old := time.Now().UTC().AddDate(0, 0, -100).Format(time.RFC3339)
	recent := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	insertTestEvent(t, db, "e-old", "openai", "gpt-4o", 100, 100, old)
	insertTestEvent(t, db, "e-recent", "openai", "gpt-4o", 100, 100, recent)

	req := httptest.NewRequest(http.MethodGet, "/usage/summary?days=365", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		TurnCount int64 `json:"turnCount"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.TurnCount != 2 {
		t.Fatalf("want 2 turns, got %d", body.TurnCount)
	}
}

func TestModule_DeleteUsageRequiresBefore(t *testing.T) {
	db := openTestDB(t)
	r := newUsageRouter(t, db)

	req := httptest.NewRequest(http.MethodDelete, "/usage", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newUsageRouter(t *testing.T, db *storage.DB) http.Handler {
	t.Helper()
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	return r
}

func insertTestEvent(t *testing.T, db *storage.DB, id, provider, model string, inputT, outputT int, recordedAt string) {
	t.Helper()
	ic, oc, _ := storage.ComputeCost(provider, model, inputT, outputT)
	if err := db.RecordTokenUsage(id, "conv-test", provider, model, inputT, outputT, ic, oc, recordedAt); err != nil {
		t.Fatalf("RecordTokenUsage(%s): %v", id, err)
	}
}
