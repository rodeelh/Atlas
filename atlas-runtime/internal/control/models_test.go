package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
	imageModels, ok := result["imageModels"].([]ModelRecord)
	if !ok {
		t.Fatalf("expected []ModelRecord imageModels, got %T", result["imageModels"])
	}
	for _, model := range imageModels {
		if model.ID == "" || model.DisplayName == "" {
			t.Fatalf("expected populated discovered image models, got %+v", imageModels)
		}
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
	imageModels, ok := result["imageModels"].([]ModelRecord)
	if !ok {
		t.Fatalf("expected []ModelRecord imageModels, got %T", result["imageModels"])
	}
	if len(imageModels) != 0 {
		t.Fatalf("expected empty image models, got %+v", imageModels)
	}
}

func TestModelsService_OpenRouterModelCacheTTLAndRefresh(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	legacyPath := filepath.Join(dir, "legacy.json")
	cfgStore := config.NewStoreAt(cfgPath, legacyPath)
	snap := config.Defaults()
	snap.SelectedOpenRouterModel = "openai/gpt-4.1-mini"
	snap.OpenRouterModelCache = config.OpenRouterModelCache{
		FetchedAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		Models: []config.CachedModelRecord{
			{ID: "cached/model", DisplayName: "Cached Model", IsFast: true},
		},
	}
	if err := cfgStore.Save(snap); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openai/gpt-4.1-mini",
					"name":           "GPT-4.1 Mini",
					"context_length": 128000,
					"pricing": map[string]any{
						"prompt": "0.000001",
					},
					"top_provider": map[string]any{"max_completion_tokens": 16384},
				},
			},
		})
	}))
	defer srv.Close()

	svc := NewModelsService(cfgStore, nil)
	svc.httpDo = (&http.Client{Timeout: 5 * time.Second}).Do

	origFetch := fetchOpenRouterModelsFn
	t.Cleanup(func() { fetchOpenRouterModelsFn = origFetch })
	fetchOpenRouterModelsFn = func(apiKey string, do func(*http.Request) (*http.Response, error)) []ModelRecord {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			return []ModelRecord{}
		}
		defer resp.Body.Close()
		return []ModelRecord{{ID: "openai/gpt-4.1-mini", DisplayName: "GPT-4.1 Mini", IsFast: true}}
	}

	// Fresh cache hit: no remote fetch.
	out, total := svc.openRouterModels(false, "key", cfgStore.Load(), 25)
	if len(out) != 1 || out[0].ID != "cached/model" {
		t.Fatalf("expected cached model, got %+v", out)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if callCount != 0 {
		t.Fatalf("expected zero fetch calls on fresh cache, got %d", callCount)
	}

	// TTL miss: should fetch and update.
	older := cfgStore.Load()
	older.OpenRouterModelCache.FetchedAt = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	if err := cfgStore.Save(older); err != nil {
		t.Fatalf("cfgStore.Save older: %v", err)
	}
	out, total = svc.openRouterModels(false, "key", cfgStore.Load(), 25)
	if len(out) != 1 || out[0].ID != "openai/gpt-4.1-mini" {
		t.Fatalf("expected fetched model after ttl miss, got %+v", out)
	}
	if total != 1 {
		t.Fatalf("expected total=1 after ttl miss, got %d", total)
	}

	// Refresh bypasses cache.
	out, total = svc.openRouterModels(true, "key", cfgStore.Load(), 25)
	if len(out) != 1 || out[0].ID != "openai/gpt-4.1-mini" {
		t.Fatalf("expected fetched model on refresh, got %+v", out)
	}
	if total != 1 {
		t.Fatalf("expected total=1 on refresh, got %d", total)
	}
}

func TestSelectOpenAIImageModels_FiltersAndFormats(t *testing.T) {
	items := []struct {
		ID string `json:"id"`
	}{
		{ID: "gpt-image-1.5"},
		{ID: "gpt-image-1-mini"},
		{ID: "gpt-4o-mini-tts"},
		{ID: "text-embedding-3-large"},
	}
	models := selectOpenAIImageModels(items)
	if len(models) != 2 {
		t.Fatalf("expected 2 image models, got %+v", models)
	}
	if models[0].DisplayName != "GPT Image 1.5" {
		t.Fatalf("expected normalized label, got %+v", models[0])
	}
	if models[1].DisplayName != "GPT Image 1 Mini" {
		t.Fatalf("expected normalized mini label, got %+v", models[1])
	}
}

func TestSelectGeminiImageModels_FiltersAndFormats(t *testing.T) {
	items := []struct {
		ID string `json:"id"`
	}{
		{ID: "models/gemini-3.1-flash-image-preview"},
		{ID: "models/gemini-2.5-flash-image"},
		{ID: "models/gemini-2.5-flash-preview-tts"},
		{ID: "models/gemini-embedding-001"},
	}
	models := selectGeminiImageModels(items)
	if len(models) != 2 {
		t.Fatalf("expected 2 image models, got %+v", models)
	}
	if models[0].DisplayName != "Gemini 3.1 Flash Image Preview" {
		t.Fatalf("expected normalized preview label, got %+v", models[0])
	}
	if models[1].DisplayName != "Gemini 2.5 Flash Image" {
		t.Fatalf("expected normalized flash label, got %+v", models[1])
	}
}
