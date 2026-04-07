package voice

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/logstore"
)

// kokoroServerScript is the Python HTTP server that loads the Kokoro-ONNX
// model and serves POST /synthesize. Embedded at compile time so it ships
// with the Atlas binary; extracted to disk on first use.
//
//go:embed kokoro_server.py
var kokoroServerScript []byte

// KokoroVoiceDefault is the only voice Atlas exposes. Hardcoded — the UI no
// longer offers a picker; if you want to change voices edit this constant
// and rebuild.
const KokoroVoiceDefault = "am_onyx"

// pcmStreamChunkBytes is the target size in bytes for each streamed PCM chunk
// read from the Kokoro subprocess stdout. Each chunk is emitted as a single
// SSE voice_audio event to the browser. 4 KB at 24 kHz 16-bit mono ≈ 85 ms.
const pcmStreamChunkBytes = 4096

// KokoroBinaryPath returns the bundled Python wrapper script path. The script
// is extracted on first use from the embedded copy.
func (m *Manager) KokoroScriptPath() string {
	return filepath.Join(m.VoiceBinDir(), "kokoro_server.py")
}

// KokoroModelPath returns the expected location of the Kokoro ONNX model.
func (m *Manager) KokoroModelPath() string {
	return filepath.Join(m.kokoroModelsDir(), "kokoro-v1.0.onnx")
}

// KokoroVoicesPath returns the expected location of the Kokoro voices.bin.
func (m *Manager) KokoroVoicesPath() string {
	return filepath.Join(m.kokoroModelsDir(), "voices-v1.0.bin")
}

func (m *Manager) kokoroModelsDir() string {
	return filepath.Join(m.modelsDir, "kokoro")
}

// PythonBinaryPath returns the path to the Python interpreter in the shared
// voice venv (same venv piper-tts was installed into).
func (m *Manager) PythonBinaryPath() string {
	return filepath.Join(m.VoiceBinDir(), "venv", "bin", "python")
}

// KokoroReady reports whether all files needed to start Kokoro are on disk:
// the Python interpreter, the kokoro-onnx package (checked indirectly via a
// successful import at start time), the ONNX model, and the voices.bin.
func (m *Manager) KokoroReady() bool {
	if _, err := os.Stat(m.PythonBinaryPath()); err != nil {
		return false
	}
	if _, err := os.Stat(m.KokoroModelPath()); err != nil {
		return false
	}
	if _, err := os.Stat(m.KokoroVoicesPath()); err != nil {
		return false
	}
	return true
}

// extractKokoroScript writes the embedded kokoro_server.py to the voice bin
// directory if it's missing or stale. Safe to call repeatedly.
func (m *Manager) extractKokoroScript() error {
	path := m.KokoroScriptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, kokoroServerScript) {
		return nil
	}
	return os.WriteFile(path, kokoroServerScript, 0o644)
}

// StartKokoro launches the Kokoro HTTP server subprocess on the given port.
// Idempotent: if Kokoro is already running on the same port, it's reused.
// Called lazily from SynthesizeKokoro on first TTS call in a session.
func (m *Manager) StartKokoro(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isKokoroRunningLocked() && m.kokoroPort == port {
		return nil
	}
	if !m.KokoroReady() {
		return fmt.Errorf("kokoro not installed — run 'make download-kokoro'")
	}
	if err := m.extractKokoroScript(); err != nil {
		return fmt.Errorf("extract kokoro script: %w", err)
	}

	// Stop any stale instance first.
	m.stopKokoroLocked()

	if port <= 0 {
		port = 11989
	}

	args := []string{
		m.KokoroScriptPath(),
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--model", m.KokoroModelPath(),
		"--voices", m.KokoroVoicesPath(),
	}
	var stderrBuf bytes.Buffer
	cmd := exec.Command(m.PythonBinaryPath(), args...)
	cmd.Stdout = nil
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kokoro: %w", err)
	}
	m.kokoroCmd = cmd
	m.kokoroPort = port

	// Reap the process in the background and capture the exit reason.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.kokoroCmd == cmd {
			m.kokoroCmd = nil
			m.kokoroPort = 0
			if msg := firstLine(stderrBuf.String()); msg != "" {
				m.lastError = "kokoro: " + msg
			}
		}
	}()

	logstore.Write("info", fmt.Sprintf("voice: kokoro subprocess started on :%d", port),
		map[string]string{"component": "kokoro", "port": fmt.Sprintf("%d", port)})
	return nil
}

func (m *Manager) isKokoroRunningLocked() bool {
	return m.kokoroCmd != nil && m.kokoroCmd.Process != nil && m.kokoroCmd.ProcessState == nil
}

// WaitKokoroReady polls the Kokoro /health endpoint until it responds 200 or
// the timeout elapses. Kokoro's cold-load takes ~600 ms on Apple Silicon.
func (m *Manager) WaitKokoroReady(timeout time.Duration) error {
	m.mu.Lock()
	port := m.kokoroPort
	m.mu.Unlock()
	if port == 0 {
		return fmt.Errorf("kokoro not started")
	}
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("voice: kokoro did not become ready within %s", timeout)
}

func (m *Manager) stopKokoroLocked() {
	if m.kokoroCmd == nil || m.kokoroCmd.Process == nil {
		return
	}
	_ = m.kokoroCmd.Process.Kill()
	m.kokoroCmd = nil
	m.kokoroPort = 0
}

// KokoroVoices fetches the list of installed Kokoro voices by calling the
// running Kokoro server's /health endpoint. Auto-starts the subprocess if
// needed. Returns a deterministic sorted list.
func (m *Manager) KokoroVoices(ctx context.Context, defaultPort int) ([]string, error) {
	if !m.KokoroReady() {
		return nil, fmt.Errorf("kokoro not installed")
	}
	if !m.isKokoroRunning() {
		if err := m.StartKokoro(defaultPort); err != nil {
			return nil, err
		}
		if err := m.WaitKokoroReady(10 * time.Second); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	port := m.kokoroPort
	m.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Voices []string `json:"voices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Voices, nil
}

func (m *Manager) isKokoroRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isKokoroRunningLocked()
}

// SynthesizeKokoroWav runs text through Kokoro, buffers all PCM output in
// memory, and returns it wrapped as a single standalone WAV file. Used by
// the voice.synthesize skill which writes to disk.
func (m *Manager) SynthesizeKokoroWav(
	ctx context.Context,
	text, voice string,
	speed float64,
	lang string,
	defaultPort int,
) (SynthesizeResult, error) {
	var pcm bytes.Buffer
	var sampleRate int
	err := m.SynthesizeKokoroStream(ctx, text, voice, speed, lang, defaultPort, func(c SynthesizeChunk) error {
		pcm.Write(c.PCM)
		if c.SampleRate > 0 {
			sampleRate = c.SampleRate
		}
		return nil
	})
	if err != nil {
		return SynthesizeResult{}, err
	}
	if pcm.Len() == 0 {
		return SynthesizeResult{}, fmt.Errorf("kokoro produced empty output")
	}
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	wav := wrapPCMAsWav(pcm.Bytes(), sampleRate)
	return SynthesizeResult{WAV: wav, SizeBytes: len(wav)}, nil
}

// wrapPCMAsWav writes a minimal RIFF/WAVE header for 16-bit mono PCM and
// concatenates the raw sample bytes. Used by SynthesizeKokoroWav (skill path).
func wrapPCMAsWav(pcm []byte, sampleRate int) []byte {
	const bitsPerSample = 16
	const numChannels = 1
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataSize := uint32(len(pcm))

	var buf bytes.Buffer
	buf.Grow(44 + len(pcm))
	buf.WriteString("RIFF")
	_ = binaryWriteU32LE(&buf, 36+dataSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binaryWriteU32LE(&buf, 16)
	_ = binaryWriteU16LE(&buf, 1) // PCM
	_ = binaryWriteU16LE(&buf, uint16(numChannels))
	_ = binaryWriteU32LE(&buf, uint32(sampleRate))
	_ = binaryWriteU32LE(&buf, uint32(byteRate))
	_ = binaryWriteU16LE(&buf, uint16(blockAlign))
	_ = binaryWriteU16LE(&buf, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binaryWriteU32LE(&buf, dataSize)
	buf.Write(pcm)
	return buf.Bytes()
}

func binaryWriteU32LE(buf *bytes.Buffer, v uint32) error {
	var b [4]byte
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	_, err := buf.Write(b[:])
	return err
}

func binaryWriteU16LE(buf *bytes.Buffer, v uint16) error {
	var b [2]byte
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	_, err := buf.Write(b[:])
	return err
}

// SynthesizeKokoroStream runs text through the Kokoro HTTP server and streams
// the raw int16 PCM bytes from its response to the emit callback in ~4 KB
// chunks. Auto-starts the subprocess if not already running.
//
// Kokoro always emits 24000 Hz mono; that's what gets reported to the caller
// so the Web Audio resampler can upscale to the browser's native rate.
func (m *Manager) SynthesizeKokoroStream(
	ctx context.Context,
	text, voice string,
	speed float64,
	lang string,
	defaultPort int,
	emit func(c SynthesizeChunk) error,
) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text is empty")
	}
	if !m.KokoroReady() {
		return fmt.Errorf("kokoro not installed — run 'make download-kokoro'")
	}
	if voice == "" {
		voice = KokoroVoiceDefault
	}
	if speed <= 0 {
		speed = 1.0
	}
	if lang == "" {
		lang = "en-us"
	}

	if !m.isKokoroRunning() {
		if err := m.StartKokoro(defaultPort); err != nil {
			return err
		}
		if err := m.WaitKokoroReady(15 * time.Second); err != nil {
			return err
		}
	}

	m.mu.Lock()
	port := m.kokoroPort
	m.mu.Unlock()
	if port == 0 {
		return fmt.Errorf("kokoro not running")
	}

	reqBody, _ := json.Marshal(map[string]any{
		"text":  text,
		"voice": voice,
		"speed": speed,
		"lang":  lang,
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("http://127.0.0.1:%d/synthesize", port),
		bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("kokoro POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kokoro HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Kokoro's own sample rate is 24000; always report it so the browser
	// knows how to resample. (kokoro-v1.0 is fixed at 24 kHz.)
	sampleRate := 24000

	chunkIdx := 0
	firstChunk := true
	buf := make([]byte, pcmStreamChunkBytes)
	var pending []byte
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
			return fmt.Errorf("kokoro read: %w", rerr)
		}
	}
	if len(pending) > 0 {
		_ = emit(SynthesizeChunk{Index: chunkIdx, PCM: pending, SampleRate: sampleRate})
	}
	m.RecordActivity()
	return nil
}
