package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferFromCountryOnlyInitializesOnce(t *testing.T) {
	resetForTest(t)

	InferFromCountry("United States")
	got := Get()
	if got.TemperatureUnit != "fahrenheit" || got.UnitSystem != "imperial" || got.Currency != "USD" {
		t.Fatalf("unexpected inferred prefs: %+v", got)
	}
	if !got.Initialized {
		t.Fatalf("expected inferred prefs to be marked initialized")
	}

	InferFromCountry("United Kingdom")
	got = Get()
	if got.TemperatureUnit != "fahrenheit" || got.UnitSystem != "imperial" || got.Currency != "USD" {
		t.Fatalf("expected later inference to be ignored once initialized, got %+v", got)
	}
}

func TestSetMarksPreferencesInitializedAndBlocksReinfer(t *testing.T) {
	resetForTest(t)

	Set(Prefs{
		TemperatureUnit: "celsius",
		Currency:        "EUR",
		UnitSystem:      "metric",
	})
	InferFromCountry("United States")

	got := Get()
	if got.TemperatureUnit != "celsius" || got.UnitSystem != "metric" || got.Currency != "EUR" {
		t.Fatalf("expected manual prefs to win, got %+v", got)
	}
	if !got.Initialized {
		t.Fatalf("expected manual prefs to be marked initialized")
	}
}

func TestLoadFromConfigRestoresFromKeychainFallback(t *testing.T) {
	tmpHome := resetForTest(t)

	keychainPrefs := Prefs{
		TemperatureUnit: "fahrenheit",
		Currency:        "USD",
		UnitSystem:      "imperial",
		Initialized:     true,
	}
	execSecurity = func(args ...string) (string, error) {
		switch {
		case len(args) >= 1 && args[0] == "find-generic-password":
			data, _ := json.Marshal(keychainPrefs)
			return string(data), nil
		case len(args) >= 1 && args[0] == "add-generic-password":
			return "", nil
		default:
			return "", nil
		}
	}

	goConfigPath := filepath.Join(tmpHome, "Library", "Application Support", "ProjectAtlas", "go-runtime-config.json")
	if _, err := os.Stat(goConfigPath); !os.IsNotExist(err) {
		t.Fatalf("expected no config file before load, stat err=%v", err)
	}

	LoadFromConfig()
	got := Get()
	if got.TemperatureUnit != "fahrenheit" || got.UnitSystem != "imperial" || got.Currency != "USD" {
		t.Fatalf("expected keychain prefs to be restored, got %+v", got)
	}
	if !got.Initialized {
		t.Fatalf("expected restored prefs to be marked initialized")
	}
}

func TestResolvedReloadsPersistedPreferencesWhenMemoryIsEmpty(t *testing.T) {
	tmpHome := resetForTest(t)

	goConfigPath := filepath.Join(tmpHome, "Library", "Application Support", "ProjectAtlas", "go-runtime-config.json")
	if err := os.WriteFile(goConfigPath, []byte(`{
  "userTemperatureUnit": "fahrenheit",
  "userCurrency": "USD",
  "userUnitSystem": "imperial",
  "userPreferencesInitialized": true
}`), 0o600); err != nil {
		t.Fatalf("write go config: %v", err)
	}

	current = Prefs{}
	got := Resolved()
	if got.TemperatureUnit != "fahrenheit" || got.UnitSystem != "imperial" || got.Currency != "USD" {
		t.Fatalf("expected persisted prefs to be reloaded, got %+v", got)
	}
	if !got.Initialized {
		t.Fatalf("expected reloaded prefs to be marked initialized")
	}
}

func resetForTest(t *testing.T) string {
	t.Helper()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	oldExec := execSecurity
	execSecurity = func(args ...string) (string, error) {
		switch {
		case len(args) >= 1 && args[0] == "find-generic-password":
			return "", os.ErrNotExist
		case len(args) >= 1 && args[0] == "add-generic-password":
			return "", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		execSecurity = oldExec
	})

	current = Prefs{}
	supportDir := filepath.Join(tmpHome, "Library", "Application Support", "ProjectAtlas")
	if err := os.MkdirAll(supportDir, 0o700); err != nil {
		t.Fatalf("mkdir support dir: %v", err)
	}
	entries, err := os.ReadDir(supportDir)
	if err != nil {
		t.Fatalf("read support dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("expected empty support dir, found %s", strings.Join(names, ", "))
	}
	return tmpHome
}
