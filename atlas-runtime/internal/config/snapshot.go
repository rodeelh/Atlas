// Package config owns runtime configuration: loading, saving, and the
// canonical RuntimeConfigSnapshot type. JSON field names mirror the Swift
// CodingKeys exactly so the Go and Swift runtimes share the same config file.
package config

// RuntimeConfigSnapshot is the portable config contract shared between the
// Swift and Go runtimes. All JSON keys are identical to the Swift CodingKeys.
type RuntimeConfigSnapshot struct {
	RuntimePort                     int     `json:"runtimePort"`
	OnboardingCompleted             bool    `json:"onboardingCompleted"`
	TelegramEnabled                 bool    `json:"telegramEnabled"`
	DiscordEnabled                  bool    `json:"discordEnabled"`
	DiscordClientID                 string  `json:"discordClientID"`
	SlackEnabled                    bool    `json:"slackEnabled"`
	TelegramPollingTimeoutSeconds   int     `json:"telegramPollingTimeoutSeconds"`
	TelegramPollingRetryBaseSeconds int     `json:"telegramPollingRetryBaseSeconds"`
	TelegramCommandPrefix           string  `json:"telegramCommandPrefix"`
	TelegramAllowedUserIDs          []int64 `json:"telegramAllowedUserIDs"`
	TelegramAllowedChatIDs          []int64 `json:"telegramAllowedChatIDs"`
	DefaultOpenAIModel              string  `json:"defaultOpenAIModel"`
	BaseSystemPrompt                string  `json:"baseSystemPrompt"`
	MaxAgentIterations              int     `json:"maxAgentIterations"`
	ConversationWindowLimit         int     `json:"conversationWindowLimit"`
	MemoryEnabled                   bool    `json:"memoryEnabled"`
	MaxRetrievedMemoriesPerTurn     int     `json:"maxRetrievedMemoriesPerTurn"`
	MemoryAutoSaveThreshold         float64 `json:"memoryAutoSaveThreshold"`
	PersonaName                     string  `json:"personaName"`
	UserName                        string  `json:"userName"`
	ActionSafetyMode                string  `json:"actionSafetyMode"`
	ActiveImageProvider             string  `json:"activeImageProvider"`
	ActiveAIProvider                string  `json:"activeAIProvider"`
	LMStudioBaseURL                 string  `json:"lmStudioBaseURL"`
	SelectedAnthropicModel          string  `json:"selectedAnthropicModel"`
	SelectedGeminiModel             string  `json:"selectedGeminiModel"`
	SelectedOpenAIPrimaryModel      string  `json:"selectedOpenAIPrimaryModel"`
	SelectedOpenAIFastModel         string  `json:"selectedOpenAIFastModel"`
	SelectedAnthropicFastModel      string  `json:"selectedAnthropicFastModel"`
	SelectedGeminiFastModel         string  `json:"selectedGeminiFastModel"`
	SelectedLMStudioModel           string  `json:"selectedLMStudioModel"`
	SelectedLMStudioModelFast       string  `json:"selectedLMStudioModelFast"`
	LMStudioContextWindowLimit      int     `json:"lmStudioContextWindowLimit"`
	LMStudioMaxAgentIterations      int     `json:"lmStudioMaxAgentIterations"`
	OllamaBaseURL                   string  `json:"ollamaBaseURL"`
	SelectedOllamaModel             string  `json:"selectedOllamaModel"`
	SelectedOllamaModelFast         string  `json:"selectedOllamaModelFast"`
	OllamaContextWindowLimit        int     `json:"ollamaContextWindowLimit"`
	OllamaMaxAgentIterations        int     `json:"ollamaMaxAgentIterations"`
	AtlasEnginePort                 int     `json:"atlasEnginePort"`
	SelectedAtlasEngineModel        string  `json:"selectedAtlasEngineModel"`
	SelectedAtlasEngineModelFast    string  `json:"selectedAtlasEngineModelFast"`
	AtlasEngineContextWindowLimit   int     `json:"atlasEngineContextWindowLimit"`
	AtlasEngineMaxAgentIterations   int     `json:"atlasEngineMaxAgentIterations"`
	AtlasEngineCtxSize              int     `json:"atlasEngineCtxSize"`       // llama-server --ctx-size (KV-cache token limit)
	AtlasEngineKVCacheQuant         string  `json:"atlasEngineKVCacheQuant"`  // llama-server -ctk/-ctv quant level (for example: f32, f16, bf16, q8_0, q5_1, q5_0, q4_1, q4_0, iq4_nl)
	AtlasEngineMlock                bool    `json:"atlasEngineMlock"`         // llama-server --mlock — pin model in physical RAM
	AtlasEngineRouterPort           int     `json:"atlasEngineRouterPort"`    // port for the dedicated tool-router llama-server
	AtlasEngineRouterModel          string  `json:"atlasEngineRouterModel"`   // GGUF filename for the tool router (e.g. gemma-4-2b-it-Q4_K_M.gguf)
	AtlasEngineRouterForAll         bool    `json:"atlasEngineRouterForAll"`  // use router for heavy background tasks too (memory, reflection, dream)
	AtlasEngineDraftModel           string  `json:"atlasEngineDraftModel"`    // GGUF filename for speculative decoding draft model (same family as primary)
	EnableSmartToolSelection        bool    `json:"enableSmartToolSelection"` // legacy — superseded by ToolSelectionMode
	ToolSelectionMode               string  `json:"toolSelectionMode"`        // "off" | "lazy" | "heuristic" | "llm"
	WebResearchUseJinaReader        bool    `json:"webResearchUseJinaReader"`
	EnableMultiAgentOrchestration   bool    `json:"enableMultiAgentOrchestration"`
	MaxParallelAgents               int     `json:"maxParallelAgents"`
	WorkerMaxIterations             int     `json:"workerMaxIterations"`
	RemoteAccessEnabled             bool    `json:"remoteAccessEnabled"`
	TailscaleEnabled                bool    `json:"tailscaleEnabled"`
	ModelContextWindow              int     `json:"modelContextWindow"` // effective context window in tokens; 0 = auto-detect from provider
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
		DiscordClientID:                 "",
		SlackEnabled:                    false,
		TelegramPollingTimeoutSeconds:   30,
		TelegramPollingRetryBaseSeconds: 2,
		TelegramCommandPrefix:           "/",
		TelegramAllowedUserIDs:          []int64{},
		TelegramAllowedChatIDs:          []int64{},
		DefaultOpenAIModel:              "gpt-4.1-mini",
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
		SelectedOpenAIPrimaryModel:      "",
		SelectedOpenAIFastModel:         "",
		SelectedAnthropicFastModel:      "",
		SelectedGeminiFastModel:         "",
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
		EnableSmartToolSelection:        true,
		ToolSelectionMode:               "lazy",
		WebResearchUseJinaReader:        false,
		EnableMultiAgentOrchestration:   false,
		MaxParallelAgents:               3,
		WorkerMaxIterations:             4,
		RemoteAccessEnabled:             false,
		TailscaleEnabled:                false,
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
