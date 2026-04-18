package memory

import (
	"context"
	"testing"
	"time"

	"atlas-runtime-go/internal/agent"
)

// TestHyDEVector_EmptyQuery — empty query returns nil immediately without any LLM call.
func TestHyDEVector_EmptyQuery(t *testing.T) {
	provider := agent.ProviderConfig{Type: "openai", Model: "gpt-4o-mini"}
	vec := HyDEVector(context.Background(), provider, "")
	if vec != nil {
		t.Errorf("expected nil for empty query, got %v", vec)
	}
}

// TestHyDEVector_NoProvider — missing provider type returns nil immediately.
func TestHyDEVector_NoProvider(t *testing.T) {
	vec := HyDEVector(context.Background(), agent.ProviderConfig{}, "what is my favourite colour?")
	if vec != nil {
		t.Errorf("expected nil for empty provider, got %v", vec)
	}
}

// TestHyDEVector_TimeoutFallback — when the LLM call exceeds hydeTimeout the
// function returns nil (BM25 fallback path) within a reasonable deadline.
// Uses a cancelled context to simulate a slow/hung provider without a real API call.
func TestHyDEVector_TimeoutFallback(t *testing.T) {
	// Pre-cancel the context so CallAINonStreamingExported returns immediately
	// with a context error — same code path as a timeout expiry.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := agent.ProviderConfig{Type: "openai", Model: "gpt-4o-mini"}

	start := time.Now()
	vec := HyDEVector(ctx, provider, "what is my favourite food?")
	elapsed := time.Since(start)

	if vec != nil {
		t.Errorf("expected nil on cancelled context, got %v", vec)
	}
	// Should return near-instantly — not hang waiting for the LLM.
	if elapsed > 500*time.Millisecond {
		t.Errorf("HyDEVector took %v on cancelled context — expected <500ms", elapsed)
	}
}

// TestHyDEVector_TimeoutBound — hydeTimeout is defined and ≤ 5 seconds so it
// never stalls a turn. This pins the constant against accidental inflation.
func TestHyDEVector_TimeoutBound(t *testing.T) {
	if hydeTimeout <= 0 {
		t.Fatal("hydeTimeout must be positive")
	}
	if hydeTimeout > 5*time.Second {
		t.Errorf("hydeTimeout is %v — must be ≤ 5s to avoid stalling turns", hydeTimeout)
	}
}
