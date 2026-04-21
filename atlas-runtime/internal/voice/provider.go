package voice

// ProviderType identifies the audio backend.
type ProviderType string

const (
	ProviderLocal      ProviderType = "local"
	ProviderOpenAI     ProviderType = "openai"
	ProviderGemini     ProviderType = "gemini"
	ProviderElevenLabs ProviderType = "elevenlabs"
)

// ProviderConfig holds resolved settings for the active audio provider.
// Built by resolveAudioProvider() from RuntimeConfigSnapshot + Keychain.
type ProviderConfig struct {
	Type        ProviderType
	APIKey      string
	STTModel    string
	TTSModel    string
	TTSVoice    string
	Language    string  // BCP-47 hint; "" = auto-detect
	Speed       float64 // TTS speed; provider default when 0
	StylePrompt string  // natural-language delivery directive (Gemini / gpt-4o-mini-tts)
}

// VoiceOption describes a single TTS voice for GET /voice/voices.
type VoiceOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Featured    bool   `json:"featured"`
	ModelGate   string `json:"modelGate,omitempty"` // non-empty = only show when this TTS model is selected
}

type ProviderModelOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type ProviderModelSet struct {
	STT []ProviderModelOption `json:"stt"`
	TTS []ProviderModelOption `json:"tts"`
}
