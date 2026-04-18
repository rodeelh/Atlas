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

// transcribeLocal sends raw audio bytes to the local whisper-server.
// Called by localSTT.Transcribe — not called directly outside the voice package.
func (m *Manager) transcribeLocal(
	ctx context.Context,
	audio []byte,
	mimeType, language string,
	defaultModel string,
	defaultPort int,
) (TranscribeResult, error) {
	if len(audio) == 0 {
		return TranscribeResult{}, fmt.Errorf("audio is empty")
	}

	if !strings.Contains(mimeType, "wav") && !strings.Contains(mimeType, "pcm") {
		return TranscribeResult{}, fmt.Errorf(
			"local whisper-server requires WAV format (got %s); convert first with: ffmpeg -i input.mp3 output.wav",
			mimeType,
		)
	}

	// Reset idle timer at the start so a long transcription doesn't get evicted
	// mid-request by the idle watcher.
	m.RecordActivity()

	// Ensure a session is running. Auto-start with defaults if not.
	if !m.SessionActive() {
		if _, err := m.StartSession(defaultModel, defaultPort); err != nil {
			return TranscribeResult{}, fmt.Errorf("auto-start voice session: %w", err)
		}
		if err := m.WaitWhisperReady(15 * time.Second); err != nil {
			return TranscribeResult{}, err
		}
	}

	m.mu.Lock()
	port := m.whisperPort
	sessionID := m.sessionID
	m.mu.Unlock()
	if port == 0 {
		return TranscribeResult{}, fmt.Errorf("whisper not running")
	}

	// Build multipart body. whisper.cpp's server expects field name "file" and
	// an optional "response_format" field.
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

	if language != "" {
		_ = mw.WriteField("language", language)
	}
	_ = mw.WriteField("response_format", "json")
	mw.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/inference", port)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("whisper POST: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TranscribeResult{}, fmt.Errorf("read whisper response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return TranscribeResult{}, fmt.Errorf("whisper HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// whisper.cpp server returns {"text":"...", ...} when response_format=json.
	var wr struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(body, &wr); err != nil {
		// Fallback: some builds return raw text.
		wr.Text = strings.TrimSpace(string(body))
	}

	m.RecordActivity()
	return TranscribeResult{
		Text:      strings.TrimSpace(wr.Text),
		Language:  wr.Language,
		Duration:  wr.Duration,
		SessionID: sessionID,
	}, nil
}

func filenameForMime(mt string) string {
	mt = strings.ToLower(mt)
	switch {
	case strings.Contains(mt, "webm"):
		return "audio.webm"
	case strings.Contains(mt, "wav"):
		return "audio.wav"
	case strings.Contains(mt, "ogg"):
		return "audio.ogg"
	case strings.Contains(mt, "mp4"), strings.Contains(mt, "m4a"):
		return "audio.m4a"
	case strings.Contains(mt, "mpeg"), strings.Contains(mt, "mp3"):
		return "audio.mp3"
	default:
		return "audio.bin"
	}
}
