package voice

import "testing"

func TestSanitizeProviderConfig_GeminiReplacesDeadSTTDefault(t *testing.T) {
	cfg := sanitizeProviderConfig(ProviderConfig{
		Type:     ProviderGemini,
		STTModel: "gemini-2.0-flash",
		TTSModel: defaultGeminiTTSModel,
	})
	if cfg.STTModel != defaultGeminiSTTModel {
		t.Fatalf("expected dead Gemini STT model to fall back to %q, got %q", defaultGeminiSTTModel, cfg.STTModel)
	}
}

func TestSanitizeProviderConfig_GeminiRejectsNonTTSInTTSSlot(t *testing.T) {
	cfg := sanitizeProviderConfig(ProviderConfig{
		Type:     ProviderGemini,
		STTModel: defaultGeminiSTTModel,
		TTSModel: "gemini-2.5-flash",
	})
	if cfg.TTSModel != defaultGeminiTTSModel {
		t.Fatalf("expected non-TTS Gemini model to fall back to %q, got %q", defaultGeminiTTSModel, cfg.TTSModel)
	}
}

func TestSanitizeProviderConfig_OpenAIRetainsKnownModels(t *testing.T) {
	cfg := sanitizeProviderConfig(ProviderConfig{
		Type:     ProviderOpenAI,
		STTModel: "whisper-1",
		TTSModel: "gpt-4o-mini-tts",
	})
	if cfg.STTModel != "whisper-1" {
		t.Fatalf("expected whisper-1 to remain valid, got %q", cfg.STTModel)
	}
	if cfg.TTSModel != "gpt-4o-mini-tts" {
		t.Fatalf("expected gpt-4o-mini-tts to remain valid, got %q", cfg.TTSModel)
	}
}

func TestSelectProviderAudioOptions_NormalizesAndLimits(t *testing.T) {
	curated := []ProviderModelOption{
		{ID: "gpt-4o-transcribe", Label: "GPT-4o Transcribe"},
		{ID: "gpt-4o-mini-transcribe", Label: "GPT-4o Mini Transcribe"},
	}
	out := selectOpenAIAudioOptions([]string{
		"gpt-4o-mini-transcribe-2026-04-01",
		"gpt-4o-mini-transcribe-2026-05-01",
		"gpt-4o-transcribe-2026-04-01",
		"some-other-audio-model",
	}, curated)
	if len(out) != 2 {
		t.Fatalf("expected 2 normalized options, got %d", len(out))
	}
	if out[0].Label != "GPT-4o Transcribe" || out[1].Label != "GPT-4o Mini Transcribe" {
		t.Fatalf("unexpected labels: %+v", out)
	}
	if out[1].ID != "gpt-4o-mini-transcribe-2026-04-01" {
		t.Fatalf("expected shortest discovered variant to be chosen, got %q", out[1].ID)
	}
}

func TestSelectGeminiAudioOptions_NormalizesFamilies(t *testing.T) {
	curated := []ProviderModelOption{
		{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash"},
		{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
	}
	out := selectGeminiAudioOptions([]string{
		"gemini-2.5-flash-exp-0827",
		"gemini-2.5-pro-preview-05-06",
		"gemini-3.0-pro",
	}, curated)
	if len(out) != 2 {
		t.Fatalf("expected 2 normalized Gemini options, got %d", len(out))
	}
	if out[0].Label != "Gemini 2.5 Flash" || out[1].Label != "Gemini 2.5 Pro" {
		t.Fatalf("unexpected labels: %+v", out)
	}
}

func TestSanitizeProviderConfig_OpenAIAllowsLegacySavedModels(t *testing.T) {
	cfg := sanitizeProviderConfig(ProviderConfig{
		Type:     ProviderOpenAI,
		STTModel: "whisper-1",
		TTSModel: "tts-1-hd",
	})
	if cfg.STTModel != "whisper-1" {
		t.Fatalf("expected legacy whisper-1 to remain valid, got %q", cfg.STTModel)
	}
	if cfg.TTSModel != "tts-1-hd" {
		t.Fatalf("expected legacy tts-1-hd to remain valid, got %q", cfg.TTSModel)
	}
}
