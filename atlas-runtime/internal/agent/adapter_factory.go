package agent

// NewAdapter returns the correct ProviderAdapter for the given ProviderConfig.
//
// In Phase 1 this factory is only called by adapter tests; the agent loop still
// uses streamWithToolDetection directly. Phase 4 will cut the loop over to call
// NewAdapter, at which point the old streaming functions become dead code.
func NewAdapter(p ProviderConfig) ProviderAdapter {
	switch p.Type {
	case ProviderAnthropic:
		return &anthropicAdapter{p: p}
	case ProviderOpenAI:
		return &openAIAdapter{p: p}
	case ProviderAtlasEngine, ProviderLMStudio, ProviderOllama:
		return &localAdapter{p: p}
	case ProviderAtlasMLX:
		return &mlxAdapter{p: p}
	default: // ProviderGemini, ProviderOpenRouter, and any future OAI-compat providers
		return &oaiCompatAdapter{p: p}
	}
}
