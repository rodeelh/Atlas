// Package config owns runtime configuration: loading, saving, and the
// canonical RuntimeConfigSnapshot type. JSON field names mirror the Swift
// CodingKeys exactly so the Go and Swift runtimes share the same config file.
package config

type CachedModelRecord struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	IsFast      bool   `json:"isFast"`
}

type OpenRouterModelCache struct {
	FetchedAt string              `json:"fetchedAt"`
	Models    []CachedModelRecord `json:"models"`
}

// RuntimeConfigSnapshot is the portable config contract shared between the
// Swift and Go runtimes. All JSON keys are identical to the Swift CodingKeys.
type RuntimeConfigSnapshot struct {
	RuntimePort                     int                  `json:"runtimePort"`
	OnboardingCompleted             bool                 `json:"onboardingCompleted"`
	TelegramEnabled                 bool                 `json:"telegramEnabled"`
	DiscordEnabled                  bool                 `json:"discordEnabled"`
	WhatsAppEnabled                 bool                 `json:"whatsAppEnabled"`
	DiscordClientID                 string               `json:"discordClientID"`
	SlackEnabled                    bool                 `json:"slackEnabled"`
	TelegramPollingTimeoutSeconds   int                  `json:"telegramPollingTimeoutSeconds"`
	TelegramPollingRetryBaseSeconds int                  `json:"telegramPollingRetryBaseSeconds"`
	TelegramCommandPrefix           string               `json:"telegramCommandPrefix"`
	TelegramAllowedUserIDs          []int64              `json:"telegramAllowedUserIDs"`
	TelegramAllowedChatIDs          []int64              `json:"telegramAllowedChatIDs"`
	TelegramWebhookURL              string               `json:"telegramWebhookURL"`
	TelegramWebhookSecret           string               `json:"telegramWebhookSecret"`
	DefaultOpenAIModel              string               `json:"defaultOpenAIModel"`
	BaseSystemPrompt                string               `json:"baseSystemPrompt"`
	MaxAgentIterations              int                  `json:"maxAgentIterations"`
	ConversationWindowLimit         int                  `json:"conversationWindowLimit"`
	MemoryEnabled                   bool                 `json:"memoryEnabled"`
	MaxRetrievedMemoriesPerTurn     int                  `json:"maxRetrievedMemoriesPerTurn"`
	MemoryAutoSaveThreshold         float64              `json:"memoryAutoSaveThreshold"`
	PersonaName                     string               `json:"personaName"`
	UserName                        string               `json:"userName"`
	ActionSafetyMode                string               `json:"actionSafetyMode"`
	ActiveImageProvider             string               `json:"activeImageProvider"`
	ActiveAIProvider                string               `json:"activeAIProvider"`
	LMStudioBaseURL                 string               `json:"lmStudioBaseURL"`
	SelectedAnthropicModel          string               `json:"selectedAnthropicModel"`
	SelectedGeminiModel             string               `json:"selectedGeminiModel"`
	SelectedOpenRouterModel         string               `json:"selectedOpenRouterModel"`
	SelectedOpenAIPrimaryModel      string               `json:"selectedOpenAIPrimaryModel"`
	SelectedOpenAIFastModel         string               `json:"selectedOpenAIFastModel"`
	SelectedAnthropicFastModel      string               `json:"selectedAnthropicFastModel"`
	SelectedGeminiFastModel         string               `json:"selectedGeminiFastModel"`
	SelectedOpenRouterFastModel     string               `json:"selectedOpenRouterFastModel"`
	OpenRouterModelCache            OpenRouterModelCache `json:"openRouterModelCache"`
	SelectedLMStudioModel           string               `json:"selectedLMStudioModel"`
	SelectedLMStudioModelFast       string               `json:"selectedLMStudioModelFast"`
	LMStudioContextWindowLimit      int                  `json:"lmStudioContextWindowLimit"`
	LMStudioMaxAgentIterations      int                  `json:"lmStudioMaxAgentIterations"`
	OllamaBaseURL                   string               `json:"ollamaBaseURL"`
	SelectedOllamaModel             string               `json:"selectedOllamaModel"`
	SelectedOllamaModelFast         string               `json:"selectedOllamaModelFast"`
	OllamaContextWindowLimit        int                  `json:"ollamaContextWindowLimit"`
	OllamaMaxAgentIterations        int                  `json:"ollamaMaxAgentIterations"`
	AtlasEnginePort                 int                  `json:"atlasEnginePort"`
	SelectedAtlasEngineModel        string               `json:"selectedAtlasEngineModel"`
	SelectedAtlasEngineModelFast    string               `json:"selectedAtlasEngineModelFast"`
	AtlasEngineContextWindowLimit   int                  `json:"atlasEngineContextWindowLimit"`
	AtlasEngineMaxAgentIterations   int                  `json:"atlasEngineMaxAgentIterations"`
	AtlasEngineCtxSize              int                  `json:"atlasEngineCtxSize"`      // llama-server --ctx-size (KV-cache token limit)
	AtlasEngineKVCacheQuant         string               `json:"atlasEngineKVCacheQuant"` // llama-server -ctk/-ctv quant level (for example: f32, f16, bf16, q8_0, q5_1, q5_0, q4_1, q4_0, iq4_nl)
	AtlasEngineMlock                bool                 `json:"atlasEngineMlock"`        // llama-server --mlock — pin model in physical RAM
	AtlasEngineRouterPort           int                  `json:"atlasEngineRouterPort"`   // port for the dedicated tool-router llama-server
	AtlasEngineRouterModel          string               `json:"atlasEngineRouterModel"`  // GGUF filename for the tool router (e.g. gemma-4-2b-it-Q4_K_M.gguf)
	AtlasEngineRouterForAll         bool                 `json:"atlasEngineRouterForAll"` // use router for heavy background tasks too (memory, reflection, dream)
	AtlasEngineDraftModel           string               `json:"atlasEngineDraftModel"`   // GGUF filename for speculative decoding draft model (same family as primary)

	// ── MLX-LM subsystem (Apple Silicon only) ────────────────────────────────
	//
	// MLX-LM is a Python-based local inference server that uses Apple's MLX
	// framework. It is mutually exclusive with llama.cpp (atlas_engine): only
	// one local engine runs at a time. Active provider switches between
	// "atlas_engine" (llama.cpp) and "atlas_mlx" (MLX-LM).
	//
	// AtlasMLXPort is the primary inference port; AtlasMLXRouterPort is the
	// dedicated router port (MLX-exclusive — replaces the llama.cpp router
	// for MLX users). Atlas owns the Python venv at ~/.atlas-mlx.
	AtlasMLXPort              int     `json:"atlasMLXPort"`              // default 11990
	SelectedAtlasMLXModel     string  `json:"selectedAtlasMLXModel"`     // directory name under mlx-models/
	AtlasMLXCtxSize           int     `json:"atlasMLXCtxSize"`           // --max-tokens for mlx_lm.server: max output tokens per response (default 4096)
	AtlasMLXRouterPort        int     `json:"atlasMLXRouterPort"`        // default 11991 — MLX-exclusive router
	AtlasMLXRouterModel       string  `json:"atlasMLXRouterModel"`       // directory name for the MLX router model
	AtlasMLXRouterForAll      bool    `json:"atlasMLXRouterForAll"`      // use MLX router for heavy background tasks too
	AtlasMLXTemperature       float64 `json:"atlasMLXTemperature"`       // default sampling temperature for mlx_lm.server requests
	AtlasMLXTopP              float64 `json:"atlasMLXTopP"`              // default nucleus sampling parameter
	AtlasMLXMinP              float64 `json:"atlasMLXMinP"`              // default min-p sampling parameter
	AtlasMLXRepetitionPenalty float64 `json:"atlasMLXRepetitionPenalty"` // optional repetition penalty
	AtlasMLXThinkingEnabled   bool    `json:"atlasMLXThinkingEnabled"`   // send enable_thinking=true in chat_template_kwargs for supported models
	AtlasMLXChatTemplateArgs  string  `json:"atlasMLXChatTemplateArgs"`  // raw JSON object passed as chat_template_kwargs (overrides AtlasMLXThinkingEnabled)

	// SelectedLocalEngine is the user-configured local backend.
	// "atlas_engine" (llama.cpp) or "atlas_mlx" (MLX-LM).
	// Used by the chat composer's "Local LM" option to determine which
	// engine to activate. Defaults to "atlas_engine".
	SelectedLocalEngine string `json:"selectedLocalEngine"`

	EnableSmartToolSelection      bool   `json:"enableSmartToolSelection"` // legacy — superseded by ToolSelectionMode
	ToolSelectionMode             string `json:"toolSelectionMode"`        // "off" | "lazy" | "heuristic" | "llm"
	WebResearchUseJinaReader      bool   `json:"webResearchUseJinaReader"`
	EnableMultiAgentOrchestration bool   `json:"enableMultiAgentOrchestration"`
	MaxParallelAgents             int    `json:"maxParallelAgents"`
	WorkerMaxIterations           int    `json:"workerMaxIterations"`
	RemoteAccessEnabled           bool   `json:"remoteAccessEnabled"`
	TailscaleEnabled              bool   `json:"tailscaleEnabled"`
	ModelContextWindow            int    `json:"modelContextWindow"` // effective context window in tokens; 0 = auto-detect from provider

	// Voice — Whisper STT + Kokoro TTS.
	VoiceSTTEnabled      bool   `json:"voiceSTTEnabled"`
	VoiceTTSEnabled      bool   `json:"voiceTTSEnabled"`
	VoiceContinuousMode  bool   `json:"voiceContinuousMode"`
	VoiceWhisperPort     int    `json:"voiceWhisperPort"`
	VoiceWhisperModel    string `json:"voiceWhisperModel"`
	VoiceWhisperLanguage string `json:"voiceWhisperLanguage"`
	VoiceTTSAutoPlay     bool   `json:"voiceTTSAutoPlay"`
	VoiceSessionIdleSec  int    `json:"voiceSessionIdleSec"`

	// Kokoro TTS (OHF-Voice, ONNX). Voice is hardcoded to am_onyx in
	// internal/voice/kokoro.go (KokoroVoiceDefault); only the port survives
	// in config so multiple instances can use different ports if ever needed.
	VoiceKokoroPort int `json:"voiceKokoroPort"`

	// ── Mind thoughts / nap scheduler ─────────────────────────────────────
	//
	// Tunables for the mind-thoughts subsystem. All thresholds are exposed
	// here so they can be rebalanced from the web config screen during the
	// few-day review without rebuilding the binary.
	//
	// ThoughtsEnabled is the MASTER feature flag — a single switch that
	// gates the entire mind-thoughts subsystem: presence line, sidebar
	// dot, greeting flow, surfacing detection, classifier, system prompt
	// THOUGHTS injection, dispatcher, approval routing, and the nap
	// scheduler. When false, Atlas behaves as if the feature does not
	// exist. Ships false by default — users who don't want their agent
	// having inner life shouldn't have to explain themselves.
	ThoughtsEnabled bool `json:"thoughtsEnabled"`

	// NapsEnabled is a SUB-FLAG of ThoughtsEnabled. When both are true,
	// the scheduler fires naps on idle/floor triggers. When NapsEnabled
	// is false but ThoughtsEnabled is true, thoughts can still exist
	// (seeded manually, added through the dream cycle) but no automatic
	// curation happens. Ships false so the scheduler is plumbed but
	// dormant until explicitly opted in. A manual POST /mind/nap works
	// regardless of this flag, as long as ThoughtsEnabled is true.
	NapsEnabled bool `json:"napsEnabled"`

	// NapIdleMinutes is how many minutes of chat idleness trigger a nap.
	// 60 default — naps fire after an hour of conversational silence, not
	// mid-coffee-break.
	NapIdleMinutes int `json:"napIdleMinutes"`

	// NapFloorHours is the maximum time between naps regardless of idleness.
	// 6 default — ensures naps fire even on quiet days.
	NapFloorHours int `json:"napFloorHours"`

	// NapMaxOpsPerCycle caps how many thought ops a single nap may apply.
	// 3 default — more is suspicious per the spec.
	NapMaxOpsPerCycle int `json:"napMaxOpsPerCycle"`

	// Thought scoring thresholds. See internal/mind/thoughts/score.go for
	// how these are used and why their defaults are what they are. Changing
	// the auto-execute threshold below the max non-read class score breaks
	// the structural safety ceiling.
	ThoughtAutoExecuteThreshold int `json:"thoughtAutoExecuteThreshold"` // default 95
	ThoughtProposeThreshold     int `json:"thoughtProposeThreshold"`     // default 80

	// Engagement-driven discard thresholds.
	ThoughtDiscardOnNegatives int `json:"thoughtDiscardOnNegatives"` // default 2
	ThoughtDiscardOnIgnores   int `json:"thoughtDiscardOnIgnores"`   // default 3

	// Auto-execute rate limits. Hard caps on how often the dispatcher is
	// allowed to run a skill without user approval. The structural safety
	// ceiling (read-class only) is the primary defense; these are belt-and-
	// braces to prevent runaway behavior if a nap produces many eligible
	// thoughts in quick succession.
	ThoughtMaxAutoExecPerNap int `json:"thoughtMaxAutoExecPerNap"` // default 1
	ThoughtMaxAutoExecPerDay int `json:"thoughtMaxAutoExecPerDay"` // default 3
}

// EffectiveContextWindow returns the model's context window in tokens for the
// active provider. Uses explicit ModelContextWindow if set, otherwise derives
// from provider-specific config or falls back to sensible defaults.
func (c RuntimeConfigSnapshot) EffectiveContextWindow() int {
	if c.ModelContextWindow > 0 {
		return c.ModelContextWindow
	}
	switch c.ActiveAIProvider {
	case "lm_studio":
		// LM Studio: use AtlasEngineCtxSize as a proxy if available, else 8K.
		return 8192
	case "ollama":
		return 8192
	case "atlas_engine":
		if c.AtlasEngineCtxSize > 0 {
			return c.AtlasEngineCtxSize
		}
		return 16384
	case "atlas_mlx":
		if c.AtlasMLXCtxSize > 0 {
			return c.AtlasMLXCtxSize
		}
		return 4096
	case "anthropic":
		return 200000
	case "gemini":
		return 1000000
	default: // openai
		return 128000
	}
}

// SystemPromptRuneBudget returns the rune budget for the assembled system
// prompt based on the model's context window. Allocates 15% of the context
// window (in tokens, converted to runes at ~4 runes/token), clamped between
// a floor and ceiling.
func (c RuntimeConfigSnapshot) SystemPromptRuneBudget() int {
	ctxTokens := c.EffectiveContextWindow()
	// 15% of context window in tokens, converted to runes (~4 runes/token).
	budget := int(float64(ctxTokens) * 0.15 * 4)
	const floor = 4000
	const ceiling = 20000
	if budget < floor {
		return floor
	}
	if budget > ceiling {
		return ceiling
	}
	return budget
}

// Defaults returns a snapshot with the same default values as Swift's
// RuntimeConfigSnapshot.init() so cold-start behaviour is identical.
func Defaults() RuntimeConfigSnapshot {
	return RuntimeConfigSnapshot{
		RuntimePort:                     1984,
		OnboardingCompleted:             false,
		TelegramEnabled:                 false,
		DiscordEnabled:                  false,
		WhatsAppEnabled:                 false,
		DiscordClientID:                 "",
		SlackEnabled:                    false,
		TelegramPollingTimeoutSeconds:   30,
		TelegramPollingRetryBaseSeconds: 2,
		TelegramCommandPrefix:           "/",
		TelegramAllowedUserIDs:          []int64{},
		TelegramAllowedChatIDs:          []int64{},
		DefaultOpenAIModel:              "gpt-5.4",
		BaseSystemPrompt:                fallbackSystemPrompt,
		MaxAgentIterations:              3,
		ConversationWindowLimit:         15,
		MemoryEnabled:                   true,
		MaxRetrievedMemoriesPerTurn:     4,
		MemoryAutoSaveThreshold:         0.75,
		PersonaName:                     "Atlas",
		UserName:                        "",
		ActionSafetyMode:                "ask_only_for_risky_actions",
		ActiveImageProvider:             "openai",
		ActiveAIProvider:                "openai",
		LMStudioBaseURL:                 "http://localhost:1234",
		SelectedAnthropicModel:          "",
		SelectedGeminiModel:             "",
		SelectedOpenRouterModel:         "",
		SelectedOpenAIPrimaryModel:      "",
		SelectedOpenAIFastModel:         "",
		SelectedAnthropicFastModel:      "",
		SelectedGeminiFastModel:         "",
		SelectedOpenRouterFastModel:     "",
		OpenRouterModelCache:            OpenRouterModelCache{FetchedAt: "", Models: []CachedModelRecord{}},
		SelectedLMStudioModel:           "",
		SelectedLMStudioModelFast:       "",
		LMStudioContextWindowLimit:      10,
		LMStudioMaxAgentIterations:      2,
		OllamaBaseURL:                   "http://localhost:11434",
		SelectedOllamaModel:             "",
		SelectedOllamaModelFast:         "",
		OllamaContextWindowLimit:        10,
		OllamaMaxAgentIterations:        2,
		AtlasEnginePort:                 11985,
		SelectedAtlasEngineModel:        "",
		SelectedAtlasEngineModelFast:    "",
		AtlasEngineContextWindowLimit:   10,
		AtlasEngineMaxAgentIterations:   2,
		AtlasEngineCtxSize:              16384,
		AtlasEngineKVCacheQuant:         "q4_0",
		AtlasEngineMlock:                true,
		AtlasEngineRouterPort:           11986,
		AtlasEngineRouterModel:          "",
		AtlasEngineRouterForAll:         false,
		AtlasEngineDraftModel:           "",
		AtlasMLXPort:                    11990,
		SelectedAtlasMLXModel:           "",
		AtlasMLXCtxSize:                 4096,
		AtlasMLXRouterPort:              11991,
		AtlasMLXRouterModel:             "",
		AtlasMLXRouterForAll:            false,
		AtlasMLXTemperature:             0,
		AtlasMLXTopP:                    1,
		AtlasMLXMinP:                    0,
		AtlasMLXRepetitionPenalty:       0,
		AtlasMLXThinkingEnabled:         false,
		AtlasMLXChatTemplateArgs:        "",
		EnableSmartToolSelection:        true,
		ToolSelectionMode:               "lazy",
		WebResearchUseJinaReader:        false,
		EnableMultiAgentOrchestration:   false,
		MaxParallelAgents:               3,
		WorkerMaxIterations:             4,
		RemoteAccessEnabled:             false,
		TailscaleEnabled:                false,
		VoiceSTTEnabled:                 false,
		VoiceTTSEnabled:                 false,
		VoiceContinuousMode:             false,
		VoiceWhisperPort:                11987,
		VoiceWhisperModel:               "ggml-base.en.bin",
		VoiceWhisperLanguage:            "en",
		VoiceTTSAutoPlay:                false,
		VoiceSessionIdleSec:             300,
		VoiceKokoroPort:                 11989,

		// Mind thoughts / nap scheduler — defaults match the spec.
		// The whole subsystem ships disabled. Users flip ThoughtsEnabled
		// first to see the feature at all; NapsEnabled is the second
		// toggle that turns on autonomous curation.
		ThoughtsEnabled:             false,
		NapsEnabled:                 false,
		NapIdleMinutes:              60,
		NapFloorHours:               6,
		NapMaxOpsPerCycle:           3,
		ThoughtAutoExecuteThreshold: 95,
		ThoughtProposeThreshold:     80,
		ThoughtDiscardOnNegatives:   2,
		ThoughtDiscardOnIgnores:     3,
		ThoughtMaxAutoExecPerNap:    1,
		ThoughtMaxAutoExecPerDay:    3,
	}
}

const fallbackSystemPrompt = `You are Atlas, a local macOS AI operator.
Follow the active persona and relevant memory blocks supplied with each request.
Use remembered information only when it appears in the provided memory context.
Never claim that a tool ran unless you received its result.
Never pretend to remember things you do not actually know or store.
Only call registered Atlas tools when they are needed.
Respect approval boundaries:
- read tools may run automatically only within the allowed local scope
- draft tools may require approval depending on policy
- execute tools always require explicit approval
If approval is needed, request the tool through a structured tool call instead of pretending the action completed.`
