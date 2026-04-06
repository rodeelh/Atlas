package control

import (
	"path/filepath"
	"testing"

	"atlas-runtime-go/internal/config"
)

func TestModelsService_SelectedReflectsCurrentConfig(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	snap := config.Defaults()
	snap.ActiveAIProvider = "gemini"
	snap.SelectedGeminiModel = "gemini-2.5-pro"
	snap.SelectedGeminiFastModel = "gemini-2.5-flash"
	if err := cfgStore.Save(snap); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewModelsService(cfgStore, nil)
	selected := svc.Selected()
	if selected["activeAIProvider"] != "gemini" {
		t.Fatalf("unexpected activeAIProvider: %+v", selected)
	}
	if selected["selectedGeminiModel"] != "gemini-2.5-pro" {
		t.Fatalf("unexpected selectedGeminiModel: %+v", selected)
	}
}

func TestModelsService_AvailableReturnsCuratedOpenAIWithoutKey(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	if err := cfgStore.Save(config.Defaults()); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewModelsService(cfgStore, nil)
	result := svc.Available("openai")
	models, ok := result["availableModels"].([]ModelRecord)
	if !ok {
		t.Fatalf("expected []ModelRecord, got %T", result["availableModels"])
	}
	if len(models) == 0 {
		t.Fatal("expected curated OpenAI models")
	}
	if models[0].ID == "" {
		t.Fatalf("unexpected first model: %+v", models[0])
	}
}

func TestModelsService_AvailableUnknownProviderReturnsEmptyArray(t *testing.T) {
	dir := t.TempDir()
	cfgStore := config.NewStoreAt(filepath.Join(dir, "config.json"), filepath.Join(dir, "legacy.json"))
	if err := cfgStore.Save(config.Defaults()); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	svc := NewModelsService(cfgStore, nil)
	result := svc.Available("nope")
	models, ok := result["availableModels"].([]ModelRecord)
	if !ok {
		t.Fatalf("expected []ModelRecord, got %T", result["availableModels"])
	}
	if models == nil {
		t.Fatal("expected [] not nil")
	}
	if len(models) != 0 {
		t.Fatalf("expected empty models, got %+v", models)
	}
}
