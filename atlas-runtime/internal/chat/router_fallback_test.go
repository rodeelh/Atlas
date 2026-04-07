package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/skills"
)

func TestRouterFallback_IsPerTurnStickyAndWarnsOnce(t *testing.T) {
	prevCall := callAINonStreamingFn
	prevBundle := readCredentialBundleFn
	prevRouter := engineRouterReadyFn
	defer func() {
		callAINonStreamingFn = prevCall
		readCredentialBundleFn = prevBundle
		engineRouterReadyFn = prevRouter
	}()

	readCredentialBundleFn = func() (credentialBundle, error) {
		return credentialBundle{OpenAIAPIKey: "openai"}, nil
	}
	routerHealthChecks := 0
	engineRouterReadyFn = func(int) bool {
		routerHealthChecks++
		return true
	}

	engineCalls := 0
	openAICalls := 0
	callAINonStreamingFn = func(_ context.Context, p agent.ProviderConfig, _ []agent.OAIMessage, _ []map[string]any) (agent.OAIMessage, string, agent.TokenUsage, error) {
		if p.Type == agent.ProviderAtlasEngine {
			engineCalls++
			return agent.OAIMessage{}, "", agent.TokenUsage{}, errors.New("router unavailable")
		}
		if p.Type == agent.ProviderOpenAI {
			openAICalls++
			return agent.OAIMessage{Role: "assistant", Content: `[]`}, "stop", agent.TokenUsage{}, nil
		}
		return agent.OAIMessage{}, "", agent.TokenUsage{}, errors.New("unexpected provider")
	}

	cfg := config.Defaults()
	cfg.ToolSelectionMode = "llm"
	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIFastModel = "gpt-4.1-mini"
	reg := skills.NewRegistry(t.TempDir(), nil, nil)
	turn := &turnContext{}

	before := len(logstore.Global().Entries(500))

	_ = selectToolsWithLLM(context.Background(), cfg, turn, "what time is it", reg)
	_ = selectToolsWithLLM(context.Background(), cfg, turn, "and now weather", reg)

	if engineCalls != 1 {
		t.Fatalf("expected one engine call before sticky fallback, got %d", engineCalls)
	}
	if openAICalls == 0 {
		t.Fatalf("expected openai fallback call")
	}
	if routerHealthChecks != 1 {
		t.Fatalf("expected one router health check after sticky fallback, got %d", routerHealthChecks)
	}
	if !turn.routerFallbackSticky {
		t.Fatalf("expected sticky fallback to be set")
	}

	afterEntries := logstore.Global().Entries(500)
	if len(afterEntries) < before {
		t.Fatalf("log entries unexpectedly shrank")
	}
	newEntries := afterEntries[before:]
	warnCount := 0
	for _, e := range newEntries {
		if e.Level == "warn" && strings.Contains(e.Message, "router fallback: engine offline") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("expected exactly one fallback warn log, got %d", warnCount)
	}

	// New turn should reset sticky state and allow router probe again.
	nextTurn := &turnContext{}
	_ = selectToolsWithLLM(context.Background(), cfg, nextTurn, "third turn", reg)
	if routerHealthChecks != 2 {
		t.Fatalf("expected fresh turn to re-check router health, got %d checks", routerHealthChecks)
	}
}
