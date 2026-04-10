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
)

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

// resolveFastProvider builds a ProviderConfig that targets the fast model for
// the active provider. Falls back to the primary model when no fast model is
// explicitly configured, so callers always get a usable config.
func resolveFastProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	bundle, _ := readCredentialBundleFn()

	providerType := agent.ProviderType(cfg.ActiveAIProvider)
	if providerType == "" {
		providerType = agent.ProviderOpenAI
	}

	switch providerType {
	case agent.ProviderAnthropic:
		if bundle.AnthropicAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Anthropic API key not configured")
		}
		model := cfg.SelectedAnthropicFastModel
		if model == "" {
			model = cfg.SelectedAnthropicModel
		}
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderAnthropic,
			APIKey: bundle.AnthropicAPIKey,
			Model:  model,
		}, nil

	case agent.ProviderGemini:
		if bundle.GeminiAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Gemini API key not configured")
		}
		model := cfg.SelectedGeminiFastModel
		if model == "" {
			model = cfg.SelectedGeminiModel
		}
		if model == "" {
			model = "gemini-2.5-flash"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderGemini,
			APIKey: bundle.GeminiAPIKey,
			Model:  model,
		}, nil

	case agent.ProviderOpenRouter:
		if bundle.OpenRouterAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenRouter API key not configured")
		}
		model := cfg.SelectedOpenRouterFastModel
		if model == "" {
			model = cfg.SelectedOpenRouterModel
		}
		if model == "" {
			model = "openrouter/auto:free"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderOpenRouter,
			APIKey: bundle.OpenRouterAPIKey,
			Model:  model,
			ExtraHeaders: map[string]string{
				"HTTP-Referer": "https://github.com/rodeelh/project-atlas",
				"X-Title":      "Atlas",
			},
		}, nil

	case agent.ProviderLMStudio:
		model := cfg.SelectedLMStudioModelFast
		if model == "" {
			model = cfg.SelectedLMStudioModel
		}
		if model == "" {
			model = "local-model"
		}
		baseURL := cfg.LMStudioBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:1234"
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderLMStudio,
			APIKey:  bundle.LMStudioAPIKey,
			Model:   model,
			BaseURL: baseURL,
		}, nil

	case agent.ProviderOllama:
		model := cfg.SelectedOllamaModelFast
		if model == "" {
			model = cfg.SelectedOllamaModel
		}
		if model == "" {
			model = "llama3.2"
		}
		baseURL := cfg.OllamaBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderOllama,
			APIKey:  bundle.OllamaAPIKey,
			Model:   model,
			BaseURL: baseURL,
		}, nil

	case agent.ProviderAtlasEngine:
		// One-port policy: Engine LM runs a single llama-server process on
		// one port. Primary and fast models share the same BaseURL. The model name
		// sent in inference requests is advisory — llama-server always runs the
		// model that was loaded at startup, so fast/primary names only affect which
		// model the user intends to load, not which one actually responds.
		// Normalize to basename — old config values may store full paths.
		model := filepath.Base(cfg.SelectedAtlasEngineModelFast)
		if model == "" || model == "." {
			model = filepath.Base(cfg.SelectedAtlasEngineModel)
		}
		if model == "" || model == "." {
			model = "atlas-engine-model"
		}
		port := cfg.AtlasEnginePort
		if port == 0 {
			port = 11985
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderAtlasEngine,
			APIKey:  "",
			Model:   model,
			BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		}, nil

	case agent.ProviderAtlasMLX:
		// MLX-LM: one port, one process. Fast and primary share the same BaseURL.
		// mlx_lm.server 0.30+ matches requests by the full model path (the value
		// returned in /v1/models). Sending only the directory name causes it to
		// attempt a HuggingFace lookup and fail with a 401/404.
		modelName := filepath.Base(cfg.SelectedAtlasMLXModel)
		if modelName == "" || modelName == "." {
			modelName = "mlx-model"
		}
		model := filepath.Join(config.MLXModelsDir(), modelName)
		port := cfg.AtlasMLXPort
		if port == 0 {
			port = 11990
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderAtlasMLX,
			APIKey:  "",
			Model:   model,
			BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		}, nil

	default: // openai
		if bundle.OpenAIAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenAI API key not configured")
		}
		model := cfg.SelectedOpenAIFastModel
		if model == "" {
			model = cfg.SelectedOpenAIPrimaryModel
		}
		if model == "" {
			model = cfg.DefaultOpenAIModel
		}
		if model == "" {
			model = "gpt-4.1-mini"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderOpenAI,
			APIKey: bundle.OpenAIAPIKey,
			Model:  model,
		}, nil
	}
}

// resolveProvider builds an agent.ProviderConfig from the current runtime config
// and the Keychain credential bundle. Returns an error when the active provider
// has no API key configured (LM Studio is key-optional).
func resolveProvider(cfg config.RuntimeConfigSnapshot) (agent.ProviderConfig, error) {
	bundle, _ := readCredentialBundleFn()

	providerType := agent.ProviderType(cfg.ActiveAIProvider)
	if providerType == "" {
		providerType = agent.ProviderOpenAI
	}

	switch providerType {
	case agent.ProviderAnthropic:
		if bundle.AnthropicAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Anthropic API key not configured. Add your key in Atlas Settings")
		}
		model := cfg.SelectedAnthropicModel
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderAnthropic,
			APIKey: bundle.AnthropicAPIKey,
			Model:  model,
		}, nil

	case agent.ProviderGemini:
		if bundle.GeminiAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("Gemini API key not configured. Add your key in Atlas Settings")
		}
		model := cfg.SelectedGeminiModel
		if model == "" {
			model = "gemini-2.5-flash"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderGemini,
			APIKey: bundle.GeminiAPIKey,
			Model:  model,
		}, nil

	case agent.ProviderLMStudio:
		model := cfg.SelectedLMStudioModel
		if model == "" {
			model = "local-model"
		}
		baseURL := cfg.LMStudioBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:1234"
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderLMStudio,
			APIKey:  bundle.LMStudioAPIKey, // optional — set when LM Studio auth is enabled
			Model:   model,
			BaseURL: baseURL,
		}, nil

	case agent.ProviderOllama:
		model := cfg.SelectedOllamaModel
		if model == "" {
			model = "llama3.2"
		}
		baseURL := cfg.OllamaBaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderOllama,
			APIKey:  bundle.OllamaAPIKey, // optional — set when Ollama auth is enabled
			Model:   model,
			BaseURL: baseURL,
		}, nil

	case agent.ProviderAtlasEngine:
		// Normalize to basename — old config values may store full paths.
		model := filepath.Base(cfg.SelectedAtlasEngineModel)
		if model == "" || model == "." {
			model = "atlas-engine-model"
		}
		port := cfg.AtlasEnginePort
		if port == 0 {
			port = 11985
		}
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		return agent.ProviderConfig{
			Type:    agent.ProviderAtlasEngine,
			APIKey:  "", // Engine LM is local — no API key
			Model:   model,
			BaseURL: baseURL,
		}, nil

	case agent.ProviderAtlasMLX:
		// MLX-LM is local — no API key. mlx_lm.server 0.30+ matches the request
		// model field against /v1/models IDs which use the full model path.
		// Sending only the directory name causes a HuggingFace lookup and 401/404.
		modelName := filepath.Base(cfg.SelectedAtlasMLXModel)
		if modelName == "" || modelName == "." {
			modelName = "mlx-model"
		}
		model := filepath.Join(config.MLXModelsDir(), modelName)
		port := cfg.AtlasMLXPort
		if port == 0 {
			port = 11990
		}
		return agent.ProviderConfig{
			Type:    agent.ProviderAtlasMLX,
			APIKey:  "",
			Model:   model,
			BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		}, nil

	case agent.ProviderOpenRouter:
		if bundle.OpenRouterAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenRouter API key not configured. Add your key in Atlas Settings")
		}
		model := cfg.SelectedOpenRouterModel
		if model == "" {
			model = "openrouter/auto:free"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderOpenRouter,
			APIKey: bundle.OpenRouterAPIKey,
			Model:  model,
			ExtraHeaders: map[string]string{
				"HTTP-Referer": "https://github.com/rodeelh/project-atlas",
				"X-Title":      "Atlas",
			},
		}, nil

	default: // openai
		if bundle.OpenAIAPIKey == "" {
			return agent.ProviderConfig{}, fmt.Errorf("OpenAI API key not configured. Add your key in Atlas Settings")
		}
		model := cfg.DefaultOpenAIModel
		if cfg.SelectedOpenAIPrimaryModel != "" {
			model = cfg.SelectedOpenAIPrimaryModel
		}
		if model == "" {
			model = "gpt-4.1-mini"
		}
		return agent.ProviderConfig{
			Type:   agent.ProviderOpenAI,
			APIKey: bundle.OpenAIAPIKey,
			Model:  model,
		}, nil
	}
}
