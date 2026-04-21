package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ── Gemini STT ────────────────────────────────────────────────────────────────
// Uses Gemini's multimodal API: audio is sent as an inline base64 part with a
// transcription prompt. Not a dedicated STT endpoint — accuracy is good but
// slightly lower than gpt-4o-transcribe for short utterances.

type geminiSTT struct{ cfg ProviderConfig }

func (a *geminiSTT) Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error) {
	model := a.cfg.STTModel
	if model == "" {
		model = defaultGeminiSTTModel
	}

	prompt := "Transcribe the speech in this audio accurately. Return only the transcript text, nothing else."
	if language != "" {
		prompt = fmt.Sprintf("Transcribe the speech in this audio accurately. The language is %s. Return only the transcript text, nothing else.", language)
	}

	reqBody, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{
						"inlineData": map[string]any{
							"mimeType": mimeType,
							"data":     base64.StdEncoding.EncodeToString(audio),
						},
					},
					{"text": prompt},
				},
			},
		},
	})
	if err != nil {
		return TranscribeResult{}, err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, a.cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("gemini transcribe: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return TranscribeResult{}, fmt.Errorf("gemini HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	// Extract text from Gemini response: candidates[0].content.parts[0].text
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return TranscribeResult{}, fmt.Errorf("parse gemini response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return TranscribeResult{}, fmt.Errorf("gemini returned no transcript")
	}
	text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
	return TranscribeResult{Text: text}, nil
}

// ── Gemini TTS ────────────────────────────────────────────────────────────────
// Gemini TTS generates the full audio file before returning (no streaming).
// The complete PCM response is buffered then emitted as chunks so voicePlayback.ts
// receives the same SynthesizeChunk shape it uses for Kokoro and OpenAI.

type geminiTTS struct{ cfg ProviderConfig }

func (a *geminiTTS) Synthesize(ctx context.Context, text, voice string, _ float64, emit func(SynthesizeChunk) error) error {
	model := a.cfg.TTSModel
	if model == "" {
		model = defaultGeminiTTSModel
	}
	if voice == "" {
		voice = a.cfg.TTSVoice
	}
	if voice == "" {
		voice = "Aoede"
	}

	// Prepend style directive when configured.
	input := text
	if a.cfg.StylePrompt != "" {
		input = a.cfg.StylePrompt + "\n\n" + text
	}

	reqBody, err := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": input}}},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{
					"prebuiltVoiceConfig": map[string]any{
						"voiceName": voice,
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, a.cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gemini tts: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gemini HTTP %d: %s", resp.StatusCode, parseProviderErrorBody(body))
	}

	// Extract base64-encoded PCM from candidates[0].content.parts[0].inlineData.data
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("parse gemini tts response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return fmt.Errorf("gemini tts returned no audio")
	}
	pcmB64 := parsed.Candidates[0].Content.Parts[0].InlineData.Data
	if pcmB64 == "" {
		return fmt.Errorf("gemini tts returned empty audio data")
	}
	pcm, err := base64.StdEncoding.DecodeString(pcmB64)
	if err != nil {
		return fmt.Errorf("decode gemini pcm: %w", err)
	}

	// Emit the full PCM in streaming-sized chunks for the browser player.
	const sampleRate = 24000
	for i, chunkIdx := 0, 0; i < len(pcm); i, chunkIdx = i+pcmStreamChunkBytes, chunkIdx+1 {
		end := i + pcmStreamChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		slice := make([]byte, end-i)
		copy(slice, pcm[i:end])
		c := SynthesizeChunk{
			Index:      chunkIdx,
			PCM:        slice,
			SampleRate: sampleRate,
		}
		if chunkIdx == 0 {
			c.Text = text
		}
		if err := emit(c); err != nil {
			return err
		}
	}
	return nil
}
