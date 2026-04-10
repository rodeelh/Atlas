package chat

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/skills"
)

func TestParseToolRouterGroups_FiltersInvalidAndDedupes(t *testing.T) {
	manifest := []skills.ToolCapabilityGroupManifest{
		{Name: "weather"},
		{Name: "web"},
		{Name: "automation"},
	}

	groups, err := parseToolRouterGroups(`["weather","core","WEATHER","invalid","web"]`, manifest)
	if err != nil {
		t.Fatalf("parseToolRouterGroups error: %v", err)
	}
	if got, want := strings.Join(groups, ","), "weather,web"; got != want {
		t.Fatalf("groups = %q, want %q", got, want)
	}
}

func TestSelectToolsWithLLM_UsesCapabilityGroupsAndCaches(t *testing.T) {
	prevCall := callAINonStreamingFn
	prevBundle := readCredentialBundleFn
	prevRouter := engineRouterReadyFn
	defer func() {
		callAINonStreamingFn = prevCall
		readCredentialBundleFn = prevBundle
		engineRouterReadyFn = prevRouter
	}()

	toolRouterCache.mu.Lock()
	toolRouterCache.items = make(map[string]toolRouterCacheEntry)
	toolRouterCache.mu.Unlock()

	readCredentialBundleFn = func() (credentialBundle, error) {
		return credentialBundle{OpenAIAPIKey: "openai"}, nil
	}
	engineRouterReadyFn = func(int) bool { return false }

	callCount := 0
	capturedPrompt := ""
	callAINonStreamingFn = func(_ context.Context, p agent.ProviderConfig, messages []agent.OAIMessage, _ []map[string]any) (agent.OAIMessage, string, agent.TokenUsage, error) {
		callCount++
		if p.Type != agent.ProviderOpenAI {
			return agent.OAIMessage{}, "", agent.TokenUsage{}, errors.New("unexpected provider")
		}
		if len(messages) > 0 {
			if prompt, _ := messages[0].Content.(string); prompt != "" {
				capturedPrompt = prompt
			}
		}
		return agent.OAIMessage{Role: "assistant", Content: `["weather","web"]`}, "stop", agent.TokenUsage{}, nil
	}

	cfg := config.Defaults()
	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIFastModel = "gpt-4.1-mini"
	reg := skills.NewRegistry(t.TempDir(), nil, nil)

	first := selectToolsWithLLM(context.Background(), cfg, &turnContext{}, "what's the weather in Paris and look up museums nearby", reg)
	second := selectToolsWithLLM(context.Background(), cfg, &turnContext{}, "what's the weather in Paris and look up museums nearby", reg)

	if callCount != 1 {
		t.Fatalf("expected one router model call after cache hit, got %d", callCount)
	}
	if !strings.Contains(capturedPrompt, "Available capability groups:") {
		t.Fatalf("expected capability-group prompt, got %q", capturedPrompt)
	}
	if !hasToolPrefix(first, "weather__") {
		t.Fatalf("expected weather tools in first selection, got %v", toolNames(first))
	}
	if !hasToolPrefix(first, "web__") && !hasToolPrefix(first, "websearch__") {
		t.Fatalf("expected web tools in first selection, got %v", toolNames(first))
	}
	if hasToolPrefix(first, "browser__") {
		t.Fatalf("did not expect browser tools in first selection, got %v", toolNames(first))
	}
	firstNames := toolNames(first)
	secondNames := toolNames(second)
	sort.Strings(firstNames)
	sort.Strings(secondNames)
	if strings.Join(firstNames, ",") != strings.Join(secondNames, ",") {
		t.Fatalf("cached selection mismatch: %v vs %v", firstNames, secondNames)
	}
}

func hasToolPrefix(tools []map[string]any, prefix string) bool {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}
