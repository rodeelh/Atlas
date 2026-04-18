package voice

import (
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/creds"
)

// resolveAudioProvider builds an AudioProviderConfig from config + Keychain.
// Falls back to ProviderLocal if the configured cloud provider has no API key.
func resolveAudioProvider(cfg config.RuntimeConfigSnapshot) ProviderConfig {
	bundle, _ := creds.Read()

	switch ProviderType(cfg.ActiveAudioProvider) {
	case ProviderOpenAI:
		if bundle.OpenAIAPIKey == "" {
			return ProviderConfig{Type: ProviderLocal}
		}
		return ProviderConfig{
			Type:        ProviderOpenAI,
			APIKey:      bundle.OpenAIAPIKey,
			STTModel:    strOr(cfg.AudioSTTModel, "gpt-4o-mini-transcribe"),
			TTSModel:    strOr(cfg.AudioTTSModel, "tts-1"),
			TTSVoice:    strOr(cfg.AudioTTSVoice, "alloy"),
			Language:    cfg.AudioSTTLanguage,
			Speed:       floatOr(cfg.AudioTTSSpeed, 1.0),
			StylePrompt: cfg.AudioTTSStylePrompt,
		}

	case ProviderGemini:
		if bundle.GeminiAPIKey == "" {
			return ProviderConfig{Type: ProviderLocal}
		}
		return ProviderConfig{
			Type:        ProviderGemini,
			APIKey:      bundle.GeminiAPIKey,
			STTModel:    strOr(cfg.AudioSTTModel, "gemini-2.0-flash"),
			TTSModel:    strOr(cfg.AudioTTSModel, "gemini-2.5-flash-preview-tts"),
			TTSVoice:    strOr(cfg.AudioTTSVoice, "Aoede"),
			Language:    cfg.AudioSTTLanguage,
			StylePrompt: cfg.AudioTTSStylePrompt,
		}

	case ProviderElevenLabs:
		if bundle.ElevenLabsAPIKey == "" {
			return ProviderConfig{Type: ProviderLocal}
		}
		return ProviderConfig{
			Type:     ProviderElevenLabs,
			APIKey:   bundle.ElevenLabsAPIKey,
			STTModel: strOr(cfg.AudioSTTModel, "scribe_v1"),
			TTSModel: strOr(cfg.AudioTTSModel, "eleven_turbo_v2_5"),
			TTSVoice: strOr(cfg.AudioTTSVoice, "21m00Tcm4TlvDq8ikWAM"),
			Language: cfg.AudioSTTLanguage,
		}

	default:
		return ProviderConfig{Type: ProviderLocal}
	}
}

func strOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func floatOr(f, def float64) float64 {
	if f <= 0 {
		return def
	}
	return f
}
