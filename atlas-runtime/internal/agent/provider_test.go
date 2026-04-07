package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallOpenAICompatNonStreaming_AppliesExtraHeaders(t *testing.T) {
	var gotReferer, gotTitle, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	p := ProviderConfig{
		Type:    ProviderLMStudio,
		APIKey:  "token",
		Model:   "test-model",
		BaseURL: srv.URL,
		ExtraHeaders: map[string]string{
			"HTTP-Referer": "https://github.com/rodeelh/project-atlas",
			"X-Title":      "Atlas",
		},
	}

	_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("callOpenAICompatNonStreaming: %v", err)
	}
	if gotReferer != "https://github.com/rodeelh/project-atlas" {
		t.Fatalf("missing referer header: %q", gotReferer)
	}
	if gotTitle != "Atlas" {
		t.Fatalf("missing title header: %q", gotTitle)
	}
	if gotAuth != "Bearer token" {
		t.Fatalf("missing auth header: %q", gotAuth)
	}
}

func TestOAICompatBaseURL_OpenRouter(t *testing.T) {
	got := oaiCompatBaseURL(ProviderConfig{Type: ProviderOpenRouter})
	if got != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected openrouter base url: %s", got)
	}
}
