package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atlas-runtime-go/internal/engine"
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

func TestCallOpenAIResponsesNonStreaming_ParsesToolCallsAndCachedUsage(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"status": "completed",
			"output": []map[string]any{
				{
					"id":   "msg_123",
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": "Let me check."},
					},
				},
				{
					"id":        "fc_123",
					"type":      "function_call",
					"call_id":   "call_123",
					"name":      "weather__current",
					"arguments": "{\"location\":\"Boston\"}",
				},
			},
			"usage": map[string]any{
				"input_tokens":  120,
				"output_tokens": 30,
				"input_tokens_details": map[string]any{
					"cached_tokens": 80,
				},
			},
		})
	}))
	defer srv.Close()

	p := ProviderConfig{
		Type:    ProviderOpenAI,
		APIKey:  "token",
		Model:   "gpt-5.4-mini",
		BaseURL: srv.URL,
	}
	msg, reason, usage, err := callOpenAIResponsesNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "What's the weather?"}}, []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "weather__current",
				"description": "Weather",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("callOpenAIResponsesNonStreaming: %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if reason != "tool_calls" {
		t.Fatalf("finish reason: got %q want tool_calls", reason)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 30 || usage.CachedInputTokens != 80 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_123" || msg.ToolCalls[0].Function.Name != "weather__current" {
		t.Fatalf("unexpected tool calls: %+v", msg.ToolCalls)
	}
	if text, _ := msg.Content.(string); text != "Let me check." {
		t.Fatalf("unexpected message text: %#v", msg.Content)
	}
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected converted responses tools, got %#v", gotBody["tools"])
	}
}

func TestOpenAIAdapter_StreamsTextAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]}],\"usage\":{\"input_tokens\":44,\"output_tokens\":12,\"input_tokens_details\":{\"cached_tokens\":20}}}}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	adapter := &openAIAdapter{p: ProviderConfig{
		Type:    ProviderOpenAI,
		APIKey:  "token",
		Model:   "gpt-5.4-mini",
		BaseURL: srv.URL,
	}}
	ch, err := adapter.Stream(context.Background(), TurnRequest{
		Messages: []OAIMessage{{Role: "user", Content: "Hi"}},
		ConvID:   "conv1",
	})
	if err != nil {
		t.Fatalf("openAIAdapter.Stream: %v", err)
	}

	var text string
	var usage *TokenUsage
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			text += ev.Text
		case EventDone:
			usage = ev.Usage
		case EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if text != "Hello" {
		t.Fatalf("final text: got %q want Hello", text)
	}
	if usage == nil || usage.CachedInputTokens != 20 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestCallOpenAIResponsesNonStreaming_RetriesTransientStatus(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_retry",
			"status": "completed",
			"output": []map[string]any{
				{
					"id":   "msg_retry",
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{
						{"type": "output_text", "text": "ok"},
					},
				},
			},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	p := ProviderConfig{
		Type:    ProviderOpenAI,
		APIKey:  "token",
		Model:   "gpt-5.4-mini",
		BaseURL: srv.URL,
	}
	msg, _, _, err := callOpenAIResponsesNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "ping"}}, nil)
	if err != nil {
		t.Fatalf("callOpenAIResponsesNonStreaming: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if text, _ := msg.Content.(string); text != "ok" {
		t.Fatalf("unexpected message text: %#v", msg.Content)
	}
}

func TestCallOpenAICompatNonStreaming_AtlasMLXAppliesRequestOptions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
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
		Model:   "/tmp/Qwen3-8B-Instruct-4bit",
		BaseURL: srv.URL,
		MLX: &MLXRequestOptions{
			Temperature:       0.15,
			TopP:              0.92,
			MinP:              0.05,
			RepetitionPenalty: 1.07,
			Capabilities: &engine.MLXModelCapabilities{
				HasThinking:    true,
				HasToolCalling: true,
				ToolParserType: "qwen3_coder",
			},
			ChatTemplateKwargs: map[string]any{
				"foo": "bar",
			},
		},
	}

	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "ping"}},
	}

	_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, tools)
	if err != nil {
		t.Fatalf("callOpenAICompatNonStreaming: %v", err)
	}

	if gotBody["temperature"] != 0.15 {
		t.Fatalf("temperature: got %#v", gotBody["temperature"])
	}
	if gotBody["top_p"] != 0.92 {
		t.Fatalf("top_p: got %#v", gotBody["top_p"])
	}
	if gotBody["min_p"] != 0.05 {
		t.Fatalf("min_p: got %#v", gotBody["min_p"])
	}
	if gotBody["repetition_penalty"] != 1.07 {
		t.Fatalf("repetition_penalty: got %#v", gotBody["repetition_penalty"])
	}
	// No draft model set — num_draft_tokens must not appear in the request.
	if _, present := gotBody["num_draft_tokens"]; present {
		t.Fatalf("num_draft_tokens must not be sent when no draft model configured, got %#v", gotBody["num_draft_tokens"])
	}
	// Only user-configured kwargs are passed through — no auto-injection.
	rawKwargs, ok := gotBody["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", gotBody["chat_template_kwargs"])
	}
	if rawKwargs["foo"] != "bar" {
		t.Fatalf("expected user template arg foo=bar, got %#v", rawKwargs["foo"])
	}
	if _, present := rawKwargs["enable_thinking"]; present {
		t.Fatalf("enable_thinking must not be auto-injected, got %#v", rawKwargs["enable_thinking"])
	}
}

func TestCallOpenAICompatNonStreaming_AtlasMLXPassesThroughUserKwargs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	// User has explicitly set enable_thinking=true — it passes through unchanged.
	p := ProviderConfig{
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/Qwen3-8B-Instruct-4bit",
		BaseURL: srv.URL,
		MLX: &MLXRequestOptions{
			Capabilities: &engine.MLXModelCapabilities{
				HasThinking:    true,
				HasToolCalling: true,
				ToolParserType: "qwen3_coder",
			},
			ChatTemplateKwargs: map[string]any{
				"enable_thinking": true,
			},
		},
	}
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "ping"}},
	}
	_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, tools)
	if err != nil {
		t.Fatalf("callOpenAICompatNonStreaming: %v", err)
	}
	rawKwargs, ok := gotBody["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %#v", gotBody["chat_template_kwargs"])
	}
	if rawKwargs["enable_thinking"] != true {
		t.Fatalf("expected user enable_thinking=true to pass through, got %#v", rawKwargs["enable_thinking"])
	}
}

func TestCallOpenAICompatNonStreaming_AtlasMLXNoKwargsWhenNoneConfigured(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	// No user-configured ChatTemplateKwargs → no chat_template_kwargs in request.
	p := ProviderConfig{
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/Qwen3-8B-Instruct-4bit",
		BaseURL: srv.URL,
		MLX: &MLXRequestOptions{
			Capabilities: &engine.MLXModelCapabilities{
				HasThinking:    true,
				HasToolCalling: true,
				ToolParserType: "qwen3_coder",
			},
		},
	}
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "request_tools"}},
	}
	_, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, tools)
	if err != nil {
		t.Fatalf("callOpenAICompatNonStreaming: %v", err)
	}
	if gotBody["chat_template_kwargs"] != nil {
		t.Fatalf("expected no chat_template_kwargs when none configured, got %#v", gotBody["chat_template_kwargs"])
	}
}

func TestOAICompatBaseURL_OpenRouter(t *testing.T) {
	got := oaiCompatBaseURL(ProviderConfig{Type: ProviderOpenRouter})
	if got != "https://openrouter.ai/api/v1" {
		t.Fatalf("unexpected openrouter base url: %s", got)
	}
}

func TestMLXAdapter_Streams(t *testing.T) {
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

	adapter := &mlxAdapter{p: ProviderConfig{Type: ProviderAtlasMLX, Model: "/tmp/test-model", BaseURL: srv.URL}}
	ch, err := adapter.Stream(context.Background(), TurnRequest{
		Messages: []OAIMessage{{Role: "user", Content: "hello"}},
		ConvID:   "conv-1",
	})
	if err != nil {
		t.Fatalf("mlxAdapter.Stream: %v", err)
	}

	var text string
	var usage *TokenUsage
	var deltaCount int
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			text += ev.Text
			deltaCount++
		case EventDone:
			usage = ev.Usage
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if text != "hello" {
		t.Fatalf("final text: got %q, want hello", text)
	}
	if deltaCount < 1 {
		t.Fatalf("expected at least 1 text delta, got %d", deltaCount)
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestMLXAdapter_FallsBackFromEmptyStream(t *testing.T) {
	var requests int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		switch atomic.AddInt32(&requests, 1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer is not a flusher")
			}
			writer := bufio.NewWriter(w)
			lines := []string{
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: {"usage":{"prompt_tokens":7,"completion_tokens":0}}`,
				`data: [DONE]`,
			}
			for _, line := range lines {
				_, _ = writer.WriteString(line + "\n\n")
				_ = writer.Flush()
				flusher.Flush()
			}
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": "hello",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 1},
			})
		default:
			t.Fatalf("unexpected request count: %d", requests)
		}
	}))
	defer srv.Close()

	adapter := &mlxAdapter{p: ProviderConfig{Type: ProviderAtlasMLX, Model: "/tmp/test-model", BaseURL: srv.URL}}
	ch, err := adapter.Stream(context.Background(), TurnRequest{
		Messages: []OAIMessage{{Role: "user", Content: "hello"}},
		ConvID:   "conv-1",
	})
	if err != nil {
		t.Fatalf("mlxAdapter.Stream: %v", err)
	}

	var text string
	var usage *TokenUsage
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			text += ev.Text
		case EventDone:
			usage = ev.Usage
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("request count: got %d, want 2", got)
	}
	if text != "hello" {
		t.Fatalf("final text: got %q, want hello", text)
	}
	if usage == nil || usage.InputTokens != 7 || usage.OutputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestMLXAdapter_RetriesWithoutToolsAfterEmptyReplies(t *testing.T) {
	var requests int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		switch atomic.AddInt32(&requests, 1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer is not a flusher")
			}
			writer := bufio.NewWriter(w)
			lines := []string{
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: {"usage":{"prompt_tokens":7,"completion_tokens":0}}`,
				`data: [DONE]`,
			}
			for _, line := range lines {
				_, _ = writer.WriteString(line + "\n\n")
				_ = writer.Flush()
				flusher.Flush()
			}
		case 2:
			if _, ok := body["tools"]; !ok {
				t.Fatal("expected tool-bearing retry on second request")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": "",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 0},
			})
		case 3:
			if _, ok := body["tools"]; ok {
				t.Fatal("expected final retry without tools")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": "hello",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1},
			})
		default:
			t.Fatalf("unexpected request count: %d", requests)
		}
	}))
	defer srv.Close()

	adapter := &mlxAdapter{p: ProviderConfig{Type: ProviderAtlasMLX, Model: "/tmp/test-model", BaseURL: srv.URL}}
	ch, err := adapter.Stream(context.Background(), TurnRequest{
		Messages: []OAIMessage{{Role: "user", Content: "hello"}},
		Tools:    []map[string]any{{"type": "function", "function": map[string]any{"name": "request_tools"}}},
		ConvID:   "conv-1",
	})
	if err != nil {
		t.Fatalf("mlxAdapter.Stream: %v", err)
	}

	var text string
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			text += ev.Text
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("request count: got %d, want 3", got)
	}
	if text != "hello" {
		t.Fatalf("final text: got %q, want hello", text)
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

func TestCallOpenAICompatNonStreaming_AtlasMLXRetriesEOF(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer is not a hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
			return
		}

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

	reply, _, _, err := callOpenAICompatNonStreaming(context.Background(), p, []OAIMessage{{Role: "user", Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("callOpenAICompatNonStreaming: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("request attempts: got %d, want 2", got)
	}
	if content, ok := reply.Content.(string); !ok || content != "ok" {
		t.Fatalf("reply content: got %#v, want ok", reply.Content)
	}
}

func TestDoOpenAICompatRequest_AtlasMLXSetsConnectionClose(t *testing.T) {
	var gotConnection string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotConnection = r.Header.Get("Connection")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()

	resp, err := doOpenAICompatRequest(context.Background(), ProviderConfig{
		Type:    ProviderAtlasMLX,
		Model:   "/tmp/test-model",
		BaseURL: srv.URL,
	}, srv.URL+"/v1/chat/completions", []byte(`{"model":"x","messages":[],"stream":false}`))
	if err != nil {
		t.Fatalf("doOpenAICompatRequest: %v", err)
	}
	resp.Body.Close()

	if gotConnection != "close" {
		t.Fatalf("Connection header: got %q, want close", gotConnection)
	}
}
