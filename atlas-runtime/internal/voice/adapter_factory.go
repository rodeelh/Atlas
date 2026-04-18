package voice

const ttsLocalDefaultSpeed = 1.08

// newSTTAdapter returns the STT implementation for the given provider config.
func newSTTAdapter(cfg ProviderConfig, mgr *Manager) STTAdapter {
	switch cfg.Type {
	case ProviderOpenAI:
		return &openAISTT{cfg: cfg}
	case ProviderGemini:
		return &geminiSTT{cfg: cfg}
	case ProviderElevenLabs:
		return &elevenLabsSTT{cfg: cfg}
	default:
		return &localSTT{mgr: mgr}
	}
}

// newTTSAdapter returns the TTS implementation for the given provider config.
func newTTSAdapter(cfg ProviderConfig, mgr *Manager) TTSAdapter {
	switch cfg.Type {
	case ProviderOpenAI:
		return &openAITTS{cfg: cfg}
	case ProviderGemini:
		return &geminiTTS{cfg: cfg}
	case ProviderElevenLabs:
		return &elevenLabsTTS{cfg: cfg}
	default:
		return &localTTS{mgr: mgr}
	}
}
