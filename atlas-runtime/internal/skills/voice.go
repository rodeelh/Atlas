package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"atlas-runtime-go/internal/voice"
)

// SetVoiceManager injects the voice.Manager so voice.* skills can drive the
// whisper and kokoro servers. Must be called after the skills registry is
// constructed. Mirrors SetVisionFn / SetForgePersistFn.
func (r *Registry) SetVoiceManager(m *voice.Manager) {
	r.voiceMgr = m
}

func (r *Registry) registerVoice() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name: "voice.transcribe",
			Description: "Transcribes a local audio file to text using the bundled Whisper server. " +
				"Accepts WAV, WebM/Opus, MP3, M4A, or OGG. Reads the file, auto-starts a voice session if " +
				"not already running, and returns the transcript.",
			Properties: map[string]ToolParam{
				"file_path": {
					Description: "Absolute or home-relative path to an audio file on the local filesystem.",
					Type:        "string",
				},
				"language": {
					Description: "Optional ISO-639-1 language code (e.g. 'en', 'fr'). Leave empty for auto-detection.",
					Type:        "string",
				},
			},
			Required: []string{"file_path"},
		},
		PermLevel:   "read",
		ActionClass: ActionClassRead,
		Fn:          r.voiceTranscribe,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name: "voice.synthesize",
			Description: "Synthesizes speech from text using the bundled Kokoro TTS model and writes " +
				"a WAV file to output_path. Use this to turn an assistant response, a quote, or " +
				"any text into an audio file the user can play locally.",
			Properties: map[string]ToolParam{
				"text": {
					Description: "Text to speak aloud.",
					Type:        "string",
				},
				"output_path": {
					Description: "Absolute or home-relative destination path for the WAV file.",
					Type:        "string",
				},
				"voice": {
					Description: "Optional Kokoro voice name (e.g. 'af_heart', 'am_michael', 'bf_emma'). Leave empty to use the configured default.",
					Type:        "string",
				},
			},
			Required: []string{"text", "output_path"},
		},
		PermLevel:   "draft",
		ActionClass: ActionClassLocalWrite,
		Fn:          r.voiceSynthesize,
	})
}

func (r *Registry) voiceTranscribe(ctx context.Context, args json.RawMessage) (string, error) {
	if r.voiceMgr == nil {
		return "", fmt.Errorf("voice manager not configured")
	}
	var p struct {
		FilePath string `json:"file_path"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	path := expandHome(p.FilePath)
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read audio file: %w", err)
	}
	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".webm":
			mime = "audio/webm"
		case ".wav":
			mime = "audio/wav"
		case ".ogg":
			mime = "audio/ogg"
		case ".mp3":
			mime = "audio/mpeg"
		case ".m4a", ".mp4":
			mime = "audio/mp4"
		}
	}

	result, err := r.voiceMgr.Transcribe(ctx, data, mime, p.Language, "ggml-base.en.bin", 11987)
	if err != nil {
		return "", err
	}
	if result.Text == "" {
		return "(no speech detected)", nil
	}
	return result.Text, nil
}

func (r *Registry) voiceSynthesize(ctx context.Context, args json.RawMessage) (string, error) {
	if r.voiceMgr == nil {
		return "", fmt.Errorf("voice manager not configured")
	}
	var p struct {
		Text       string `json:"text"`
		OutputPath string `json:"output_path"`
		Voice      string `json:"voice"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if p.OutputPath == "" {
		return "", fmt.Errorf("output_path is required")
	}
	out := expandHome(p.OutputPath)
	if !filepath.IsAbs(out) {
		abs, err := filepath.Abs(out)
		if err == nil {
			out = abs
		}
	}
	if !strings.HasSuffix(strings.ToLower(out), ".wav") {
		out += ".wav"
	}

	voiceName := p.Voice
	if voiceName == "" {
		voiceName = voice.KokoroVoiceDefault
	}
	result, err := r.voiceMgr.SynthesizeKokoroWav(ctx, p.Text, voiceName, 1.0, "en-us", 11989)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(out, result.WAV, 0o644); err != nil {
		return "", fmt.Errorf("write wav: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", result.SizeBytes, out), nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
