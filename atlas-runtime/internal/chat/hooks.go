package chat

import (
	"context"
	"sync"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
)

// TurnRecord is the immutable snapshot passed to post-turn hooks.
// It contains enough context for memory extraction, reflection, and any other
// background work that needs to happen after a completed turn.
type TurnRecord struct {
	ConvID              string
	UserMessage         string
	AssistantResponse   string
	Provider            agent.ProviderConfig
	HeavyBgProvider     agent.ProviderConfig
	ToolCallSummaries   []string
	ToolResultSummaries []string
	Cfg                 config.RuntimeConfigSnapshot
}

// TurnHook is called after a turn completes. It runs in its own goroutine.
// Implementations must be non-blocking — they should start background work
// and return immediately.
type TurnHook func(ctx context.Context, record TurnRecord)

// HookRegistry holds registered post-turn hooks.
// Phase 3: registry exists but no hooks are registered (direct goroutine calls
// still live in Pipeline.postTurn). Phase 5 registers hooks here and removes
// the direct calls from postTurn.
type HookRegistry struct {
	mu    sync.RWMutex
	hooks []TurnHook
}

// NewHookRegistry creates an empty HookRegistry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// Register appends a hook. Safe to call concurrently.
func (r *HookRegistry) Register(h TurnHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// Run fires all registered hooks in separate goroutines.
// ctx should be a detached context (context.WithoutCancel) so hooks outlive
// the HTTP request.
func (r *HookRegistry) Run(ctx context.Context, record TurnRecord) {
	r.mu.RLock()
	hooks := r.hooks
	r.mu.RUnlock()
	for _, h := range hooks {
		h := h
		go h(ctx, record)
	}
}
