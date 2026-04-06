package communications

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_RegistersRoutes(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	if err := cfgStore.Save(config.Defaults()); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	service := comms.New(cfgStore, db)
	module := New(service)
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), nil, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/communications/channels", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/telegram/chats", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestModule_LifecycleStartStopWithoutHandlerIsSafe(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	if err := cfgStore.Save(config.Defaults()); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	module := New(comms.New(cfgStore, db))
	if err := module.Start(nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := module.Stop(nil); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	_ = auth.SessionCookieName
}
