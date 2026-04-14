package voice

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ConvertToWAV converts raw audio bytes to 16kHz mono PCM WAV, which is the
// only format guaranteed to be accepted by the bundled whisper-server binary
// (compiled without --ffmpeg-converter). Conversion is skipped if the input is
// already WAV. Requires ffmpeg in PATH; returns an error if it is absent.
//
// The output is suitable for direct use with Manager.Transcribe("audio/wav").
func ConvertToWAV(ctx context.Context, data []byte, inputMimeType string) ([]byte, error) {
	if isWAVMime(inputMimeType) {
		return data, nil
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH — cannot convert %s to WAV: install ffmpeg (e.g. brew install ffmpeg)", inputMimeType)
	}

	// Write input to a temp file so ffmpeg can detect the container format.
	ext := extForMime(inputMimeType)
	inFile, err := os.CreateTemp("", "atlas-audio-in*"+ext)
	if err != nil {
		return nil, fmt.Errorf("create temp input: %w", err)
	}
	defer os.Remove(inFile.Name())
	if _, err := inFile.Write(data); err != nil {
		inFile.Close()
		return nil, fmt.Errorf("write temp input: %w", err)
	}
	inFile.Close()

	outPath := filepath.Join(os.TempDir(), filepath.Base(inFile.Name())+".wav")
	defer os.Remove(outPath)

	// -ar 16000  : 16 kHz sample rate (optimal for whisper)
	// -ac 1      : mono
	// -c:a pcm_s16le : signed 16-bit little-endian PCM (standard WAV)
	// -y         : overwrite without prompt
	// -loglevel error : suppress verbose output
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-i", inFile.Name(),
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		"-y",
		"-loglevel", "error",
		outPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg convert: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	wav, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read converted WAV: %w", err)
	}
	return wav, nil
}

func isWAVMime(mime string) bool {
	return strings.Contains(strings.ToLower(mime), "wav")
}

func extForMime(mime string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.Contains(m, "ogg"):
		return ".ogg"
	case strings.Contains(m, "webm"):
		return ".webm"
	case strings.Contains(m, "mp4"), strings.Contains(m, "m4a"):
		return ".m4a"
	case strings.Contains(m, "mpeg"), strings.Contains(m, "mp3"):
		return ".mp3"
	case strings.Contains(m, "flac"):
		return ".flac"
	default:
		return ".bin"
	}
}
