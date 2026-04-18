package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// ── ElevenLabs STT ────────────────────────────────────────────────────────────
// Uses ElevenLabs Scribe API for speech-to-text.

type elevenLabsSTT struct{ cfg ProviderConfig }

func (a *elevenLabsSTT) Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error) {
	model := a.cfg.STTModel
	if model == "" {
		model = "scribe_v1"
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := filenameForMime(mimeType)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", mimeType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("multipart part: %w", err)
	}
	if _, err := fw.Write(audio); err != nil {
		return TranscribeResult{}, fmt.Errorf("multipart write: %w", err)
	}
	_ = mw.WriteField("model_id", model)
	if language != "" {
		_ = mw.WriteField("language_code", language)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.elevenlabs.io/v1/speech-to-text", &buf)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("xi-api-key", a.cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("elevenlabs transcribe: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return TranscribeResult{}, fmt.Errorf("elevenlabs HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	var result struct {
		Text           string `json:"text"`
		LanguageCode   string `json:"language_code"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return TranscribeResult{}, fmt.Errorf("parse response: %w", err)
	}
	return TranscribeResult{
		Text:     strings.TrimSpace(result.Text),
		Language: result.LanguageCode,
	}, nil
}

// ── ElevenLabs TTS ────────────────────────────────────────────────────────────
// Streams 24 kHz 16-bit mono PCM from ElevenLabs text-to-speech.

type elevenLabsTTS struct{ cfg ProviderConfig }

func (a *elevenLabsTTS) Synthesize(ctx context.Context, text, voice string, _ float64, emit func(SynthesizeChunk) error) error {
	if voice == "" {
		voice = a.cfg.TTSVoice
	}
	if voice == "" {
		voice = "21m00Tcm4TlvDq8ikWAM" // Rachel — calm default
	}
	model := a.cfg.TTSModel
	if model == "" {
		model = "eleven_turbo_v2_5"
	}

	reqBody, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": model,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s?output_format=pcm_24000", voice)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("xi-api-key", a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("elevenlabs tts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elevenlabs HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	// Stream the raw PCM response as chunks (24 kHz 16-bit mono).
	const sampleRate = 24000
	buf := make([]byte, pcmStreamChunkBytes)
	var pending []byte
	chunkIdx := 0
	firstChunk := true

	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			chunk := append(pending, buf[:n]...)
			pending = nil
			if len(chunk)%2 != 0 {
				pending = []byte{chunk[len(chunk)-1]}
				chunk = chunk[:len(chunk)-1]
			}
			if len(chunk) > 0 {
				out := make([]byte, len(chunk))
				copy(out, chunk)
				c := SynthesizeChunk{
					Index:      chunkIdx,
					PCM:        out,
					SampleRate: sampleRate,
				}
				if firstChunk {
					c.Text = text
					firstChunk = false
				}
				chunkIdx++
				if err := emit(c); err != nil {
					return err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("elevenlabs tts read: %w", rerr)
		}
	}
	if len(pending) > 0 {
		_ = emit(SynthesizeChunk{Index: chunkIdx, PCM: pending, SampleRate: sampleRate})
	}
	return nil
}
