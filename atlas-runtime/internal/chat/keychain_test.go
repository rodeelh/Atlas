package chat

import (
	"testing"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
)

func withResolverTestHooks(t *testing.T, bundle credentialBundle, routerReady func(port int) bool) {
	t.Helper()
	prevBundle := readCredentialBundleFn
	prevRouter := engineRouterReadyFn
	readCredentialBundleFn = func() (credentialBundle, error) { return bundle, nil }
	engineRouterReadyFn = routerReady
	t.Cleanup(func() {
		readCredentialBundleFn = prevBundle
		engineRouterReadyFn = prevRouter
	})
}

func TestResolveProvider_PrimaryAcrossProviders(t *testing.T) {
	withResolverTestHooks(t, credentialBundle{
		OpenAIAPIKey:     "openai",
		AnthropicAPIKey:  "anth",
		GeminiAPIKey:     "gem",
		OpenRouterAPIKey: "or",
		LMStudioAPIKey:   "lm",
		OllamaAPIKey:     "ol",
	}, func(int) bool { return false })

	cases := []struct {
		name     string
		provider string
		wantType agent.ProviderType
	}{
		{name: "openai", provider: "openai", wantType: agent.ProviderOpenAI},
		{name: "anthropic", provider: "anthropic", wantType: agent.ProviderAnthropic},
		{name: "gemini", provider: "gemini", wantType: agent.ProviderGemini},
		{name: "openrouter", provider: "openrouter", wantType: agent.ProviderOpenRouter},
		{name: "lm_studio", provider: "lm_studio", wantType: agent.ProviderLMStudio},
		{name: "ollama", provider: "ollama", wantType: agent.ProviderOllama},
		{name: "atlas_engine", provider: "atlas_engine", wantType: agent.ProviderAtlasEngine},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.ActiveAIProvider = tc.provider
			p, err := ResolveProvider(cfg)
			if err != nil {
				t.Fatalf("ResolveProvider error: %v", err)
			}
			if p.Type != tc.wantType {
				t.Fatalf("provider type mismatch: got=%s want=%s", p.Type, tc.wantType)
			}
		})
	}
}

func TestResolveFastProvider_FallsBackToPrimaryWhenFastEmpty(t *testing.T) {
	withResolverTestHooks(t, credentialBundle{
		OpenAIAPIKey:    "openai",
		AnthropicAPIKey: "anth",
		GeminiAPIKey:    "gem",
	}, func(int) bool { return false })

	cfg := config.Defaults()

	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIPrimaryModel = "gpt-4.1"
	cfg.SelectedOpenAIFastModel = ""
	p, err := ResolveFastProvider(cfg)
	if err != nil {
		t.Fatalf("openai ResolveFastProvider: %v", err)
	}
	if p.Model != "gpt-4.1" {
		t.Fatalf("openai fast fallback mismatch: got=%s want=gpt-4.1", p.Model)
	}

	cfg.ActiveAIProvider = "anthropic"
	cfg.SelectedAnthropicModel = "claude-sonnet-4-6"
	cfg.SelectedAnthropicFastModel = ""
	p, err = ResolveFastProvider(cfg)
	if err != nil {
		t.Fatalf("anthropic ResolveFastProvider: %v", err)
	}
	if p.Model != "claude-sonnet-4-6" {
		t.Fatalf("anthropic fast fallback mismatch: got=%s want=claude-sonnet-4-6", p.Model)
	}

	cfg.ActiveAIProvider = "gemini"
	cfg.SelectedGeminiModel = "gemini-2.5-pro"
	cfg.SelectedGeminiFastModel = ""
	p, err = ResolveFastProvider(cfg)
	if err != nil {
		t.Fatalf("gemini ResolveFastProvider: %v", err)
	}
	if p.Model != "gemini-2.5-pro" {
		t.Fatalf("gemini fast fallback mismatch: got=%s want=gemini-2.5-pro", p.Model)
	}
}

func TestResolveBackgroundProvider_EngineHealthyUsesDefaultPort(t *testing.T) {
	seenPort := 0
	withResolverTestHooks(t, credentialBundle{OpenAIAPIKey: "openai"}, func(port int) bool {
		seenPort = port
		return true
	})

	cfg := config.Defaults()
	cfg.AtlasEngineRouterPort = 0
	p, err := ResolveBackgroundProvider(cfg)
	if err != nil {
		t.Fatalf("ResolveBackgroundProvider: %v", err)
	}
	if p.Type != agent.ProviderAtlasEngine {
		t.Fatalf("expected atlas_engine, got %s", p.Type)
	}
	if seenPort != 11986 {
		t.Fatalf("expected default router port 11986, got %d", seenPort)
	}
}

func TestResolveBackgroundProvider_UsesSelectedSupportiveLocalEngine(t *testing.T) {
	seenPort := 0
	withResolverTestHooks(t, credentialBundle{OpenAIAPIKey: "openai"}, func(port int) bool {
		seenPort = port
		return true
	})

	cfg := config.Defaults()
	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIFastModel = "gpt-4.1-mini"
	cfg.SelectedLocalEngine = "atlas_mlx"
	cfg.AtlasMLXRouterPort = 0
	cfg.AtlasMLXRouterModel = "Qwen2.5-0.5B-Instruct-4bit"

	p, err := ResolveBackgroundProvider(cfg)
	if err != nil {
		t.Fatalf("ResolveBackgroundProvider: %v", err)
	}
	if p.Type != agent.ProviderAtlasMLX {
		t.Fatalf("expected atlas_mlx, got %s", p.Type)
	}
	if seenPort != 11991 {
		t.Fatalf("expected default MLX router port 11991, got %d", seenPort)
	}
	if p.Model == "" || p.Model == "Qwen2.5-0.5B-Instruct-4bit" {
		t.Fatalf("expected full MLX model path for router, got %q", p.Model)
	}
}

func TestResolveBackgroundProvider_EngineUnhealthyFallsBack(t *testing.T) {
	withResolverTestHooks(t, credentialBundle{OpenAIAPIKey: "openai"}, func(int) bool { return false })

	cfg := config.Defaults()
	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIFastModel = "gpt-4.1-mini"
	p, err := ResolveBackgroundProvider(cfg)
	if err != nil {
		t.Fatalf("ResolveBackgroundProvider: %v", err)
	}
	if p.Type != agent.ProviderOpenAI {
		t.Fatalf("expected openai fallback, got %s", p.Type)
	}
}

func TestResolveHeavyBackgroundProvider_HonorsRouterForAll(t *testing.T) {
	withResolverTestHooks(t, credentialBundle{OpenAIAPIKey: "openai"}, func(int) bool { return true })
	cfg := config.Defaults()
	cfg.AtlasEngineRouterForAll = true

	p, err := ResolveHeavyBackgroundProvider(cfg)
	if err != nil {
		t.Fatalf("ResolveHeavyBackgroundProvider: %v", err)
	}
	if p.Type != agent.ProviderAtlasEngine {
		t.Fatalf("expected atlas_engine when router_for_all enabled, got %s", p.Type)
	}
}

func TestResolveHeavyBackgroundProvider_UsesSelectedSupportiveLocalEngine(t *testing.T) {
	withResolverTestHooks(t, credentialBundle{OpenAIAPIKey: "openai"}, func(int) bool { return true })

	cfg := config.Defaults()
	cfg.ActiveAIProvider = "openai"
	cfg.SelectedOpenAIFastModel = "gpt-4.1-mini"
	cfg.SelectedLocalEngine = "atlas_mlx"
	cfg.AtlasMLXRouterForAll = true
	cfg.SelectedAtlasMLXModel = "Llama-3.2-3B-Instruct-4bit"
	cfg.AtlasMLXRouterModel = "Qwen2.5-0.5B-Instruct-4bit"

	p, err := ResolveHeavyBackgroundProvider(cfg)
	if err != nil {
		t.Fatalf("ResolveHeavyBackgroundProvider: %v", err)
	}
	if p.Type != agent.ProviderAtlasMLX {
		t.Fatalf("expected atlas_mlx, got %s", p.Type)
	}
	if p.Model == "" || p.Model == "Qwen2.5-0.5B-Instruct-4bit" {
		t.Fatalf("expected full MLX model path for router, got %q", p.Model)
	}
}
