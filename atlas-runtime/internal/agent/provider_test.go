package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atlas-runtime-go/internal/engine"
)

type testEmitter struct {
	mu     sync.Mutex
	events []EmitEvent
}

func (e *testEmitter) Emit(_ string, event EmitEvent) {
	e.mu.Lock()
	e.events = append(e.events, event)
	e.mu.Unlock()
}

func (e *testEmitter) Finish(_ string) {}

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

func TestStreamWithToolDetection_AtlasMLXStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		writer := bufio.NewWriter(w)
		lines := []string{
			`data: {"choices":[{"delta":{"content":"hel"},"finish_reason":""}]}`,
			`data: {"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
			`data: {"usage":{"prompt_tokens":12,"completion_tokens":2}}`,
			`data: [DONE]`,
		}
		for _, line := range lines {
			_, _ = writer.WriteString(line + "\n\n")
			_ = writer.Flush()
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer srv.Close()

	var bc testEmitter
	result, err := streamWithToolDetection(context.Background(), ProviderConfig{
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/test-model",
		BaseURL: srv.URL,
	}, []OAIMessage{{Role: "user", Content: "hello"}}, nil, "conv-1", &bc)
	if err != nil {
		t.Fatalf("streamWithToolDetection: %v", err)
	}
	if result.FinalText != "hello" {
		t.Fatalf("final text: got %q, want hello", result.FinalText)
	}
	if result.ChunkCount != 2 {
		t.Fatalf("chunk count: got %d, want 2", result.ChunkCount)
	}
	if result.FirstTokenAt <= 0 {
		t.Fatalf("expected first token latency to be recorded, got %s", result.FirstTokenAt)
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()
	if len(bc.events) != 3 {
		t.Fatalf("event count: got %d, want 3", len(bc.events))
	}
	if bc.events[0].Type != "assistant_started" {
		t.Fatalf("first event: got %s, want assistant_started", bc.events[0].Type)
	}
	if bc.events[1].Type != "assistant_delta" || bc.events[1].Content != "hel" {
		t.Fatalf("second event: %+v", bc.events[1])
	}
	if bc.events[2].Type != "assistant_delta" || bc.events[2].Content != "lo" {
		t.Fatalf("third event: %+v", bc.events[2])
	}
}

func TestCallOpenAICompatNonStreaming_AtlasMLXSerializesConcurrentRequests(t *testing.T) {
	var active int32
	var maxActive int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&active, 1)
		for {
			prev := atomic.LoadInt32(&maxActive)
			if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&active, -1)

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
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/test-model",
		BaseURL: srv.URL,
	}
	engine.ConfigureMLXScheduler(srv.URL+"/v1", 1, 0)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("callOpenAICompatNonStreaming: %v", err)
		}
	}
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent requests: got %d, want 1", got)
	}
}

func TestCallOpenAICompatNonStreaming_AtlasMLXAllowsConfiguredBatchConcurrency(t *testing.T) {
	var active int32
	var maxActive int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&active, 1)
		for {
			prev := atomic.LoadInt32(&maxActive)
			if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&active, -1)

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
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/test-model",
		BaseURL: srv.URL,
	}
	engine.ConfigureMLXScheduler(srv.URL+"/v1", 2, 10*time.Millisecond)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("callOpenAICompatNonStreaming: %v", err)
		}
	}
	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("max concurrent requests: got %d, want at least 2", got)
	}
}
