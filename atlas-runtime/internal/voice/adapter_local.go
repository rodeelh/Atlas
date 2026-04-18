package voice

import "context"

// localSTT wraps the Manager's existing whisper-server transcription path.
type localSTT struct{ mgr *Manager }

func (a *localSTT) Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error) {
	cfg := a.mgr.cfgStore.Load()
	model := cfg.VoiceWhisperModel
	if model == "" {
		model = "ggml-base.en.bin"
	}
	port := cfg.VoiceWhisperPort
	if port == 0 {
		port = 11987
	}
	return a.mgr.transcribeLocal(ctx, audio, mimeType, language, model, port)
}

// localTTS wraps the Manager's existing Kokoro streaming synthesiser.
type localTTS struct{ mgr *Manager }

func (a *localTTS) Synthesize(ctx context.Context, text, voice string, speed float64, emit func(SynthesizeChunk) error) error {
	cfg := a.mgr.cfgStore.Load()
	port := cfg.VoiceKokoroPort
	if port == 0 {
		port = 11989
	}
	if voice == "" {
		voice = cfg.VoiceKokoroVoice
	}
	if voice == "" {
		voice = KokoroVoiceDefault
	}
	if speed <= 0 {
		speed = ttsLocalDefaultSpeed
	}
	return a.mgr.SynthesizeKokoroStream(ctx, text, voice, speed, "en-us", port, emit)
}
