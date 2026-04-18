package voice

import (
	"context"
	"encoding/json"
)

// parseProviderErrorBody extracts a clean error message from an HTTP error
// response body, understanding OAI-style and Gemini-style error envelopes.
func parseProviderErrorBody(body []byte) string {
	// OAI-style: {"error":{"message":"...","metadata":{"raw":"..."}}}
	var envelope struct {
		Error *struct {
			Message  string          `json:"message"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != nil {
		if len(envelope.Error.Metadata) > 0 {
			var meta struct{ Raw string `json:"raw"` }
			if err := json.Unmarshal(envelope.Error.Metadata, &meta); err == nil && meta.Raw != "" {
				return meta.Raw
			}
		}
		if envelope.Error.Message != "" {
			return envelope.Error.Message
		}
	}
	// ElevenLabs-style: {"detail":{"message":"..."}} or {"detail":"plain string"}
	var elEnvelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &elEnvelope); err == nil && len(elEnvelope.Detail) > 0 {
		var detail struct{ Message string `json:"message"` }
		if err := json.Unmarshal(elEnvelope.Detail, &detail); err == nil && detail.Message != "" {
			return detail.Message
		}
		// plain string detail
		var s string
		if err := json.Unmarshal(elEnvelope.Detail, &s); err == nil && s != "" {
			return s
		}
	}
	return string(body)
}

// STTAdapter transcribes raw audio bytes to text.
type STTAdapter interface {
	Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error)
}

// TTSAdapter synthesises text to PCM audio, calling emit for each chunk.
// The chunk shape (16-bit mono PCM at SampleRate Hz) is identical across all
// providers so voicePlayback.ts in the browser needs no changes.
type TTSAdapter interface {
	Synthesize(ctx context.Context, text, voice string, speed float64, emit func(SynthesizeChunk) error) error
}
