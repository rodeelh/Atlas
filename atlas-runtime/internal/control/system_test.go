package control

import (
	"path/filepath"
	"testing"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/storage"
)

func TestSystemService_UpdateConfigClampsBoundsAndReportsRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	initial := config.Defaults()
	initial.RuntimePort = 1984
	if err := cfgStore.Save(initial); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	next := initial
	next.RuntimePort = 1985
	next.MaxParallelAgents = 99
	next.WorkerMaxIterations = 0

	updated, restartRequired, err := svc.UpdateConfig(next)
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if !restartRequired {
		t.Fatal("expected restartRequired to be true")
	}
	if updated.MaxParallelAgents != 5 {
		t.Fatalf("expected MaxParallelAgents clamp to 5, got %d", updated.MaxParallelAgents)
	}
	if updated.WorkerMaxIterations != 1 {
		t.Fatalf("expected WorkerMaxIterations clamp to 1, got %d", updated.WorkerMaxIterations)
	}
}

func TestSystemService_LogsReturnsArrayWhenEmpty(t *testing.T) {
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

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	logs := svc.Logs(10)
	if logs == nil {
		t.Fatal("expected [] not nil")
	}
}

func TestSystemService_OnboardingRoundTrip(t *testing.T) {
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

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	if svc.OnboardingCompleted() {
		t.Fatal("expected onboarding to start as false")
	}
	if err := svc.UpdateOnboarding(true); err != nil {
		t.Fatalf("UpdateOnboarding: %v", err)
	}
	if !svc.OnboardingCompleted() {
		t.Fatal("expected onboarding to be true after update")
	}
}
