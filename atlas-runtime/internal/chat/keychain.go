package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/engine"
)

func atlasMLXRequestOptions(cfg config.RuntimeConfigSnapshot) *agent.MLXRequestOptions {
	opts := &agent.MLXRequestOptions{
		Temperature:       cfg.AtlasMLXTemperature,
		TopP:              cfg.AtlasMLXTopP,
		MinP:              cfg.AtlasMLXMinP,
		RepetitionPenalty: cfg.AtlasMLXRepetitionPenalty,
	}
	if opts.TopP == 0 {
		opts.TopP = 1
	}
	// Build chat_template_kwargs: start with the thinking toggle, then merge any
	// explicit user-configured kwargs (which take precedence over the toggle).
	kwargs := make(map[string]any)
	if cfg.AtlasMLXThinkingEnabled {
		kwargs["enable_thinking"] = true
	}
	if raw := strings.TrimSpace(cfg.AtlasMLXChatTemplateArgs); raw != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			for k, v := range parsed {
				kwargs[k] = v // explicit user kwargs override the thinking toggle
			}
		}
	}
	if len(kwargs) > 0 {
		opts.ChatTemplateKwargs = kwargs
	}
	return opts
}

func newAtlasMLXProviderConfig(cfg config.RuntimeConfigSnapshot, model string, baseURL string) agent.ProviderConfig {
	opts := atlasMLXRequestOptions(cfg)
	modelName := filepath.Base(model)
	if modelName != "" && modelName != "." {
		opts.Capabilities = engine.InspectMLXModelCapabilities(filepath.Join(config.MLXModelsDir(), modelName))
	}
	return agent.ProviderConfig{
		Type:    agent.ProviderAtlasMLX,
		APIKey:  "",
		Model:   model,
		BaseURL: baseURL,
		MLX:     opts,
	}
}

// execSecurity runs the macOS `security` CLI with the given arguments and
// returns stdout. Used to read Keychain items without CGO.
func execSecurity(args ...string) (string, error) {
	cmd := exec.Command("security", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("security %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// randomID returns a hex-encoded random ID of n bytes.
func randomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// credentialBundle is the JSON bundle stored in the Keychain under
// com.projectatlas.credentials / bundle (same struct as AtlasCredentialBundle).
type credentialBundle struct {
	OpenAIAPIKey     string `json:"openAIAPIKey"`
	AnthropicAPIKey  string `json:"anthropicAPIKey"`
	GeminiAPIKey     string `json:"geminiAPIKey"`
	OpenRouterAPIKey string `json:"openRouterAPIKey"`
	LMStudioAPIKey   string `json:"lmStudioAPIKey"`
	OllamaAPIKey     string `json:"ollamaAPIKey"`
}

// readCredentialBundle reads the full credential bundle from the Keychain.
func readCredentialBundle() (credentialBundle, error) {
	out, err := execSecurity("find-generic-password",
		"-s", "com.projectatlas.credentials",
		"-a", "bundle",
		"-w",
	)
	if err != nil {
		return credentialBundle{}, nil // key absent — not an error at this level
	}
	var bundle credentialBundle
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &bundle); err != nil {
		return credentialBundle{}, nil
	}
	return bundle, nil
}

// ResolveProvider resolves the primary slot for the active provider.
//
// Contract:
//   - Uses ActiveAIProvider from runtime config.
//   - Returns a concrete ProviderConfig or an API-key error.
//   - Call sites: main message loop, vision calls, and primary-turn inference.
func ResolveProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	return resolveProvider(cfg)
}

// ResolveFastProvider resolves the fast slot for the active provider.
//
// Contract:
//   - Uses Selected*FastModel when configured.
//   - Falls back to provider primary model, then provider default.
//   - Call sites: fallback background tasks and heavy background default path.
func ResolveFastProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	return resolveFastProvider(cfg)
}

// ResolveBackgroundProvider resolves the router slot for light background work.
//
// Contract:
//   - Prefers Engine router when /health is 200.
//   - Health check uses AtlasEngineRouterPort, defaulting to 11986.
//   - Falls back to ResolveFastProvider on non-200, timeout, or sticky-turn fallback.
//   - Call sites: tool routing and other latency-sensitive background lookups.
func ResolveBackgroundProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	p, _, err := resolveBackgroundProvider(cfg, nil)
	return p, err
}

// ResolveHeavyBackgroundProvider resolves the reflection slot for heavy background work.
//
// Contract:
//   - Uses Engine router only when AtlasEngineRouterForAll is true and healthy.
//   - Otherwise falls back to ResolveFastProvider.
//   - Call sites: memory extraction, reflection, dream cycle.
func ResolveHeavyBackgroundProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	return resolveHeavyBackgroundProvider(cfg)
}

// engineRouterReady pings the Engine LM router's /health endpoint.
// Returns true only when the server is up and the model is fully loaded.
// Uses a short timeout so background tasks don't stall waiting for a cold router.
var (
	readCredentialBundleFn = readCredentialBundle
	engineRouterReadyFn    = engineRouterReady
)

func engineRouterReady(port int) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// resolveBackgroundProvider returns the best provider for LIGHT background tasks
// (tool routing, classification).
//
// Router selection:
//   - atlas_mlx active or selected as the supportive local engine → prefer the
//     MLX router (port AtlasMLXRouterPort / 11991).
//   - atlas_engine active or selected as the supportive local engine → prefer
//     the llama.cpp router (port AtlasEngineRouterPort / 11986).
//   - Any other provider → fall through to cloud fast model.
//
// Falls back to ResolveFastProvider when the preferred router is unavailable.
func resolveBackgroundProvider(cfg config.RuntimeConfigSnapshot, turn *turnContext) (agent.ProviderConfig, bool, error) {
	if turn != nil && turn.routerFallbackSticky {
		p, err := resolveFastProvider(cfg)
		return p, true, err
	}

	switch supportiveLocalProviderType(cfg) {
	case agent.ProviderAtlasMLX:
		port := cfg.AtlasMLXRouterPort
		if port == 0 {
			port = 11991
		}
		if engineRouterReadyFn(port) {
			routerModelName := filepath.Base(cfg.AtlasMLXRouterModel)
			if routerModelName == "" || routerModelName == "." {
				routerModelName = "router"
			}
			return agent.ProviderConfig{
				Type:    agent.ProviderAtlasMLX,
				APIKey:  "",
				Model:   filepath.Join(config.MLXModelsDir(), routerModelName),
				BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
				MLX:     atlasMLXRequestOptions(cfg),
			}, false, nil
		}
	case agent.ProviderAtlasEngine:
		port := cfg.AtlasEngineRouterPort
		if port == 0 {
			port = 11986
		}
		if engineRouterReadyFn(port) {
			return agent.ProviderConfig{
				Type:    agent.ProviderAtlasEngine,
				APIKey:  "",
				Model:   "router",
				BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
			}, false, nil
		}
	}
	p, err := resolveFastProvider(cfg)
	return p, true, err
}

// resolveHeavyBackgroundProvider returns the best provider for HEAVY background
// tasks (memory extraction, mind reflection, dream cycle). These tasks are
// quality-sensitive, so the local router is only used when the user has
// explicitly opted in via the router-for-all toggle.
//
//   - atlas_mlx active OR selected as the supportive local engine, with
//     AtlasMLXRouterForAll → MLX router when healthy.
//   - atlas_engine active OR selected as the supportive local engine, with
//     AtlasEngineRouterForAll → llama.cpp router when healthy.
//   - Otherwise → cloud fast model (better quality for consolidation tasks).
func resolveHeavyBackgroundProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	switch supportiveLocalProviderType(cfg) {
	case agent.ProviderAtlasMLX:
		if cfg.AtlasMLXRouterForAll {
			port := cfg.AtlasMLXRouterPort
			if port == 0 {
				port = 11991
			}
			if engineRouterReadyFn(port) {
				routerModelName := filepath.Base(cfg.AtlasMLXRouterModel)
				if routerModelName == "" || routerModelName == "." {
					routerModelName = "router"
				}
				return agent.ProviderConfig{
					Type:    agent.ProviderAtlasMLX,
					APIKey:  "",
					Model:   filepath.Join(config.MLXModelsDir(), routerModelName),
					BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
					MLX:     atlasMLXRequestOptions(cfg),
				}, nil
			}
		}
	case agent.ProviderAtlasEngine:
		if cfg.AtlasEngineRouterForAll {
			port := cfg.AtlasEngineRouterPort
			if port == 0 {
				port = 11986
			}
			if engineRouterReadyFn(port) {
				return agent.ProviderConfig{
					Type:    agent.ProviderAtlasEngine,
					APIKey:  "",
					Model:   "router",
					BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
				}, nil
			}
		}
	}
	return resolveFastProvider(cfg)
}

func supportiveLocalProviderType(cfg config.RuntimeConfigSnapshot) agent.ProviderType {
	switch active := agent.ProviderType(cfg.ActiveAIProvider); active {
	case agent.ProviderAtlasEngine, agent.ProviderAtlasMLX:
		return active
	}

	local := agent.ProviderType(cfg.SelectedLocalEngine)
	if local == "" {
		local = agent.ProviderAtlasEngine
	}
	switch local {
	case agent.ProviderAtlasEngine, agent.ProviderAtlasMLX:
		return local
	default:
		return ""
	}
}

// providerResolver builds a ProviderConfig for one provider kind.
// fast=true selects the fast-model slot; fast=false selects the primary slot.
// Adding a new provider: add exactly one entry to providerResolvers.
type providerResolver func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error)

// firstNonEmpty returns the first non-empty string from s.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

var openRouterHeaders = map[string]string{
	"HTTP-Referer": "https://github.com/rodeelh/project-atlas",
	"X-Title":      "Atlas",
}

var providerResolvers = map[agent.ProviderType]providerResolver{
	agent.ProviderAnthropic: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		if bundle.AnthropicAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Anthropic API key not configured. Add your key in Atlas Settings")
		}
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedAnthropicFastModel, cfg.SelectedAnthropicModel, "claude-haiku-4-5-20251001")
		} else {
			model = firstNonEmpty(cfg.SelectedAnthropicModel, "claude-haiku-4-5-20251001")
		}
		return agent.ProviderConfig{Type: agent.ProviderAnthropic, APIKey: bundle.AnthropicAPIKey, Model: model}, nil
	},

	agent.ProviderGemini: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		if bundle.GeminiAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Gemini API key not configured. Add your key in Atlas Settings")
		}
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedGeminiFastModel, cfg.SelectedGeminiModel, "gemini-2.5-flash")
		} else {
			model = firstNonEmpty(cfg.SelectedGeminiModel, "gemini-2.5-flash")
		}
		return agent.ProviderConfig{Type: agent.ProviderGemini, APIKey: bundle.GeminiAPIKey, Model: model}, nil
	},

	agent.ProviderOpenRouter: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		if bundle.OpenRouterAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenRouter API key not configured. Add your key in Atlas Settings")
		}
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedOpenRouterFastModel, cfg.SelectedOpenRouterModel, "openrouter/auto:free")
		} else {
			model = firstNonEmpty(cfg.SelectedOpenRouterModel, "openrouter/auto:free")
		}
		return agent.ProviderConfig{
			Type: agent.ProviderOpenRouter, APIKey: bundle.OpenRouterAPIKey, Model: model,
			ExtraHeaders: openRouterHeaders,
		}, nil
	},

	agent.ProviderLMStudio: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedLMStudioModelFast, cfg.SelectedLMStudioModel, "local-model")
		} else {
			model = firstNonEmpty(cfg.SelectedLMStudioModel, "local-model")
		}
		baseURL := firstNonEmpty(cfg.LMStudioBaseURL, "http://localhost:1234")
		return agent.ProviderConfig{
			Type: agent.ProviderLMStudio, APIKey: bundle.LMStudioAPIKey, // optional
			Model: model, BaseURL: baseURL,
		}, nil
	},

	agent.ProviderOllama: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedOllamaModelFast, cfg.SelectedOllamaModel, "llama3.2")
		} else {
			model = firstNonEmpty(cfg.SelectedOllamaModel, "llama3.2")
		}
		baseURL := firstNonEmpty(cfg.OllamaBaseURL, "http://localhost:11434")
		return agent.ProviderConfig{
			Type: agent.ProviderOllama, APIKey: bundle.OllamaAPIKey, // optional
			Model: model, BaseURL: baseURL,
		}, nil
	},

	agent.ProviderAtlasEngine: func(cfg config.RuntimeConfigSnapshot, _ credentialBundle, fast bool) (agent.ProviderConfig, error) {
		// One-port policy: llama-server runs the model loaded at startup.
		// Fast/primary model names are advisory; only affect which model the
		// user intends to load, not which one actually responds.
		var raw string
		if fast {
			raw = firstNonEmpty(cfg.SelectedAtlasEngineModelFast, cfg.SelectedAtlasEngineModel)
		} else {
			raw = cfg.SelectedAtlasEngineModel
		}
		model := filepath.Base(raw)
		if model == "" || model == "." {
			return agent.ProviderConfig{}, fmt.Errorf("no model configured for Engine LM — select a model in Settings → Engine")
		}
		port := cfg.AtlasEnginePort
		if port == 0 {
			port = 11985
		}
		return agent.ProviderConfig{
			Type: agent.ProviderAtlasEngine, Model: model,
			BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		}, nil
	},

	agent.ProviderAtlasMLX: func(cfg config.RuntimeConfigSnapshot, _ credentialBundle, _ bool) (agent.ProviderConfig, error) {
		// MLX-LM: one port, one process. Fast and primary share the same BaseURL
		// and model — mlx_lm.server serves whatever was loaded at startup.
		modelName := filepath.Base(cfg.SelectedAtlasMLXModel)
		if modelName == "" || modelName == "." {
			modelName = "mlx-model"
		}
		model := filepath.Join(config.MLXModelsDir(), modelName)
		port := cfg.AtlasMLXPort
		if port == 0 {
			port = 11990
		}
		return newAtlasMLXProviderConfig(cfg, model, fmt.Sprintf("http://127.0.0.1:%d", port)), nil
	},

	agent.ProviderOpenAI: func(cfg config.RuntimeConfigSnapshot, bundle credentialBundle, fast bool) (agent.ProviderConfig, error) {
		if bundle.OpenAIAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenAI API key not configured. Add your key in Atlas Settings")
		}
		var model string
		if fast {
			model = firstNonEmpty(cfg.SelectedOpenAIFastModel, cfg.SelectedOpenAIPrimaryModel, cfg.DefaultOpenAIModel, "gpt-5.4-mini")
		} else {
			model = firstNonEmpty(cfg.SelectedOpenAIPrimaryModel, cfg.DefaultOpenAIModel, "gpt-5.4")
		}
		return agent.ProviderConfig{Type: agent.ProviderOpenAI, APIKey: bundle.OpenAIAPIKey, Model: model}, nil
	},
}

// resolveProviderSlot is the single table-driven entry point. fast=true picks
// the fast-model slot. Unknown providers fall through to OpenAI (legacy default).
func resolveProviderSlot(cfg config.RuntimeConfigSnapshot, fast bool) (agent.ProviderConfig, error) {
	bundle, _ := readCredentialBundleFn()
	t := agent.ProviderType(cfg.ActiveAIProvider)
	if t == "" {
		t = agent.ProviderOpenAI
	}
	r, ok := providerResolvers[t]
	if !ok {
		r = providerResolvers[agent.ProviderOpenAI]
	}
	return r(cfg, bundle, fast)
}

// resolveProvider — primary slot.
func resolveProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	return resolveProviderSlot(cfg, false)
}

// resolveFastProvider — fast slot with primary-model fallback per provider.
func resolveFastProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	return resolveProviderSlot(cfg, true)
}
