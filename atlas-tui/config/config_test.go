package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config, got nil")
	}
	if cfg.BaseURL == "" {
		t.Error("expected default BaseURL to be set")
	}
	if cfg.OnboardingDone {
		t.Error("expected fresh config to have OnboardingDone=false")
	}
}

func TestSaveAndReload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.BaseURL = "http://localhost:9999"
	cfg.Token = "abc123"
	cfg.OnboardingDone = true
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File should exist under HOME/.config/atlas-tui/config.json
	p := filepath.Join(tmp, ".config", "atlas-tui", "config.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected config file at %s: %v", p, err)
	}

	cfg2, err := Load()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if cfg2.BaseURL != "http://localhost:9999" || cfg2.Token != "abc123" || !cfg2.OnboardingDone {
		t.Errorf("reloaded config mismatch: %+v", cfg2)
	}
}
