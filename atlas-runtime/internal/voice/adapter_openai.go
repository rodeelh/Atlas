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

// ── OpenAI STT ────────────────────────────────────────────────────────────────

type openAISTT struct{ cfg ProviderConfig }

func (a *openAISTT) Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error) {
	model := a.cfg.STTModel
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	lang := language
	if lang == "" {
		lang = a.cfg.Language
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// OpenAI accepts the raw browser blob (WebM/Opus, MP4, etc.) directly —
	// no in-process format conversion needed unlike whisper.cpp.
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
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", "json")
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("openai transcriptions: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return TranscribeResult{}, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	var result struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return TranscribeResult{}, fmt.Errorf("parse response: %w", err)
	}
	return TranscribeResult{
		Text:     strings.TrimSpace(result.Text),
		Language: result.Language,
	}, nil
}

// ── OpenAI TTS ────────────────────────────────────────────────────────────────

type openAITTS struct{ cfg ProviderConfig }

func (a *openAITTS) Synthesize(ctx context.Context, text, voice string, speed float64, emit func(SynthesizeChunk) error) error {
	model := a.cfg.TTSModel
	if model == "" {
		model = "tts-1"
	}
	if voice == "" {
		voice = a.cfg.TTSVoice
	}
	if voice == "" {
		voice = "alloy"
	}
	if speed <= 0 {
		speed = 1.0
	}

	// Prepend style prompt when using gpt-4o-mini-tts and a prompt is configured.
	input := text
	if model == "gpt-4o-mini-tts" && a.cfg.StylePrompt != "" {
		input = a.cfg.StylePrompt + "\n\n" + text
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":           model,
		"input":           input,
		"voice":           voice,
		"speed":           speed,
		"response_format": "pcm", // 24 kHz 16-bit mono — same as Kokoro; no conversion needed
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/audio/speech", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("openai speech: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	// Stream the PCM response body as chunks — identical shape to Kokoro output.
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
			// Keep chunks on 2-byte (16-bit sample) boundaries.
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
			return fmt.Errorf("openai speech read: %w", rerr)
		}
	}
	if len(pending) > 0 {
		_ = emit(SynthesizeChunk{Index: chunkIdx, PCM: pending, SampleRate: sampleRate})
	}
	return nil
}
