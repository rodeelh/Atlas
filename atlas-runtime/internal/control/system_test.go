package control

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	next.AutonomyMode = "UNLEASHED"
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
	if updated.AutonomyMode != config.AutonomyModeUnleashed {
		t.Fatalf("expected autonomy mode to normalize, got %q", updated.AutonomyMode)
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

func TestSystemService_ScheduleRestartRunsRestartFn(t *testing.T) {
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
	done := make(chan struct{}, 1)
	svc.restartDelay = 0
	svc.restartFn = func() error {
		done <- struct{}{}
		return nil
	}

	if err := svc.ScheduleRestart(); err != nil {
		t.Fatalf("ScheduleRestart: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restart function")
	}
}

func TestSystemService_ScheduleRestartPreventsDuplicateRequests(t *testing.T) {
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
	block := make(chan struct{})
	svc.restartDelay = 0
	svc.restartFn = func() error {
		<-block
		return nil
	}

	if err := svc.ScheduleRestart(); err != nil {
		t.Fatalf("first ScheduleRestart: %v", err)
	}
	if err := svc.ScheduleRestart(); err == nil {
		t.Fatal("expected duplicate restart to be rejected")
	}
	close(block)
}

func TestSystemService_UpdateConfigNormalizesLocalBaseURLs(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	initial := config.Defaults()
	if err := cfgStore.Save(initial); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	next := initial
	next.LMStudioBaseURL = " http://127.0.0.1:1234/ "
	next.OllamaBaseURL = ""

	updated, _, err := svc.UpdateConfig(next)
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if updated.LMStudioBaseURL != "http://127.0.0.1:1234" {
		t.Fatalf("expected LM Studio URL to normalize, got %q", updated.LMStudioBaseURL)
	}
	if updated.OllamaBaseURL != "http://localhost:11434" {
		t.Fatalf("expected Ollama fallback URL, got %q", updated.OllamaBaseURL)
	}
}

func TestSystemService_UpdateConfigRejectsNonLoopbackLocalProviderURLs(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	initial := config.Defaults()
	if err := cfgStore.Save(initial); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	next := initial
	next.LMStudioBaseURL = "https://example.com:1234"

	_, _, err = svc.UpdateConfig(next)
	if err == nil {
		t.Fatal("expected invalid LM Studio URL to be rejected")
	}
	if !strings.Contains(err.Error(), "lmStudioBaseURL") {
		t.Fatalf("expected lmStudioBaseURL error, got %v", err)
	}
}

func TestSystemService_UpdateConfigRejectsConfigSecretMutation(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	initial := config.Defaults()
	initial.TelegramWebhookSecret = "existing-secret"
	if err := cfgStore.Save(initial); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	next := initial
	next.TelegramWebhookSecret = "new-secret"

	_, _, err = svc.UpdateConfig(next)
	if err == nil {
		t.Fatal("expected webhook secret mutation to be rejected")
	}
	if !strings.Contains(err.Error(), "telegramWebhookSecret") {
		t.Fatalf("expected telegramWebhookSecret error, got %v", err)
	}
}

func TestSystemService_UpdateConfigRejectsInvalidDiscordClientID(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	initial := config.Defaults()
	if err := cfgStore.Save(initial); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewSystemService(cfgStore, runtime.NewService(1984), db)
	next := initial
	next.DiscordClientID = "abc123"

	_, _, err = svc.UpdateConfig(next)
	if err == nil {
		t.Fatal("expected invalid Discord client ID to be rejected")
	}
	if !strings.Contains(err.Error(), "discordClientID") {
		t.Fatalf("expected discordClientID error, got %v", err)
	}
}
