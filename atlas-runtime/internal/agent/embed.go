package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync/atomic"
)

// NomicPrefixDocument is the task prefix for texts being stored as memories.
// NomicPrefixQuery is the task prefix for recall/search queries.
// Both are required by nomic-embed-text-v1.5 and are ignored gracefully by
// OpenAI text-embedding-3-small and Gemini text-embedding-004.
const (
	NomicPrefixDocument = "search_document: "
	NomicPrefixQuery    = "search_query: "
)

// embedSidecarURL holds the URL of the local embedding sidecar when running.
// Set by SetEmbedSidecarURL from main.go; zero value = no sidecar.
var embedSidecarURL atomic.Pointer[string]

// SetEmbedSidecarURL configures the local llama-server embedding endpoint.
// Pass "" to disable the sidecar (fall back to the chat provider).
func SetEmbedSidecarURL(url string) {
	if url == "" {
		embedSidecarURL.Store(nil)
	} else {
		embedSidecarURL.Store(&url)
	}
}

// embedURL returns the active embedding endpoint: sidecar if configured,
// otherwise "" (callers fall back to the chat provider).
func embedURL() string {
	if p := embedSidecarURL.Load(); p != nil {
		return *p
	}
	return ""
}

// Embed calls the active provider's embedding API and returns a float32 vector.
// Returns nil, nil for providers that don't support embeddings (Anthropic, all
// local providers without a sidecar). Callers must treat a nil vector as "no
// embedding available" and fall back to keyword-only retrieval.
//
// When the local embedding sidecar is running (configured via SetEmbedSidecarURL),
// it is always preferred over the chat provider's embedding API.
func Embed(ctx context.Context, p ProviderConfig, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	// Sidecar takes priority: llama-server --embedding mode (nomic-embed-text-v1.5).
	if url := embedURL(); url != "" {
		return embedOAI(ctx, url, "", "", text)
	}
	switch p.Type {
	case ProviderOpenAI:
		return embedOAI(ctx, "https://api.openai.com/v1/embeddings", p.APIKey, "text-embedding-3-small", text)
	case ProviderOpenRouter:
		return embedOAI(ctx, "https://openrouter.ai/api/v1/embeddings", p.APIKey, "text-embedding-3-small", text)
	case ProviderGemini:
		return embedGemini(ctx, p.APIKey, text)
	default:
		// Anthropic has no embedding endpoint. Local providers (LM Studio,
		// Ollama, Atlas Engine, Atlas MLX) are skipped without a sidecar.
		return nil, nil
	}
}

// CosineSimilarity returns the cosine similarity between two float32 vectors.
// Returns 0 if either vector is nil or zero-magnitude.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		magA += ai * ai
		magB += bi * bi
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// ── provider implementations ──────────────────────────────────────────────────

func embedOAI(ctx context.Context, baseURL, apiKey, model, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embed: OAI %d: %s", resp.StatusCode, b)
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("embed: OAI returned no data")
	}
	return out.Data[0].Embedding, nil
}

func embedGemini(ctx context.Context, apiKey, text string) ([]float32, error) {
	url := "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:embedContent?key=" + apiKey
	body, _ := json.Marshal(map[string]any{
		"model":   "models/text-embedding-004",
		"content": map[string]any{"parts": []map[string]any{{"text": text}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embed: Gemini %d: %s", resp.StatusCode, b)
	}

	var out struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embedding.Values, nil
}
