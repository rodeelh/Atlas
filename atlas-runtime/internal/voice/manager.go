package voice

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
)

// Manager owns the voice session lifecycle. whisper-server (STT) starts when
// a session begins; kokoro-onnx (TTS) starts lazily on first synthesize call.
// Both are killed together when the session ends or idles out. Safe for
// concurrent use.
type Manager struct {
	installDir string // ~/Library/Application Support/Atlas — binaries under voice/
	modelsDir  string // ~/Library/Application Support/ProjectAtlas/voice-models

	mu             sync.Mutex
	sessionActive  bool
	sessionID      string
	sessionStarted time.Time

	whisperCmd   *exec.Cmd
	whisperPort  int
	whisperModel string

	// Kokoro TTS server subprocess — started lazily on first synthesize call,
	// killed on session end.
	kokoroCmd  *exec.Cmd
	kokoroPort int

	lastError string

	// Idle auto-end: after idleTimeout of no transcribe/synthesize calls,
	// EndSession runs automatically. Zero disables.
	idleTimeout  time.Duration
	lastActivity time.Time
	watcherStop  chan struct{}

	dlMu       sync.Mutex
	dlProgress DownloadProgress
}

// NewManager creates a Manager.
//   - installDir: Atlas install dir (binaries live under installDir/voice/)
//   - modelsDir:  parent dir for whisper/ and piper/ model subdirs
func NewManager(installDir, modelsDir string) *Manager {
	return &Manager{
		installDir: installDir,
		modelsDir:  modelsDir,
	}
}

// SetIdleTimeout configures automatic session end after d of inactivity.
// Must be called before StartSession. Zero disables idle ejection.
func (m *Manager) SetIdleTimeout(d time.Duration) {
	m.mu.Lock()
	m.idleTimeout = d
	m.mu.Unlock()
}

// RecordActivity resets the idle timer. Call after each completed transcribe
// or synthesize operation.
func (m *Manager) RecordActivity() {
	m.mu.Lock()
	m.lastActivity = time.Now()
	m.mu.Unlock()
}

// VoiceBinDir returns the directory holding voice binaries + the Python venv.
func (m *Manager) VoiceBinDir() string {
	return filepath.Join(m.installDir, "voice")
}

// WhisperBinaryPath returns the full path to the whisper-server binary.
func (m *Manager) WhisperBinaryPath() string {
	return filepath.Join(m.VoiceBinDir(), "whisper-server")
}

// WhisperBinaryReady reports whether whisper-server exists on disk.
func (m *Manager) WhisperBinaryReady() bool {
	info, err := os.Stat(m.WhisperBinaryPath())
	return err == nil && !info.IsDir()
}

// WhisperModelsDir returns the directory where Whisper models are stored.
func (m *Manager) WhisperModelsDir() string {
	return filepath.Join(m.modelsDir, "whisper")
}

// SessionActive reports whether a voice session is currently running.
func (m *Manager) SessionActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionActive
}

// Status returns a snapshot of the current voice state.
func (m *Manager) Status() VoiceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := VoiceStatus{
		SessionActive:  m.sessionActive,
		SessionID:      m.sessionID,
		WhisperRunning: m.isWhisperRunningLocked(),
		WhisperReady:   m.WhisperBinaryReady(),
		WhisperPort:    m.whisperPort,
		WhisperModel:   m.whisperModel,
		KokoroRunning:  m.isKokoroRunningLocked(),
		KokoroReady:    m.kokoroReadyLocked(),
		KokoroPort:     m.kokoroPort,
		LastError:      m.lastError,
	}
	if !m.sessionStarted.IsZero() {
		s.SessionStarted = m.sessionStarted.Unix()
	}
	return s
}

func (m *Manager) isWhisperRunningLocked() bool {
	return m.whisperCmd != nil && m.whisperCmd.Process != nil && m.whisperCmd.ProcessState == nil
}

// kokoroReadyLocked is a lock-aware version of KokoroReady for use inside
// methods that already hold m.mu.
func (m *Manager) kokoroReadyLocked() bool {
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

// StartSession starts the voice session. In Phase 1 this launches whisper-server
// only. Phase 2 will also launch piper-server. If a session is already active it
// is reused (no-op). whisperModel / whisperPort come from runtime config.
func (m *Manager) StartSession(whisperModel string, whisperPort int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessionActive && m.isWhisperRunningLocked() {
		return m.sessionID, nil
	}

	if !m.WhisperBinaryReady() {
		return "", fmt.Errorf("whisper-server binary not found at %s — run 'make download-whisper'", m.WhisperBinaryPath())
	}
	if whisperModel == "" {
		return "", fmt.Errorf("whisper model is required")
	}
	modelPath := filepath.Join(m.WhisperModelsDir(), whisperModel)
	if _, err := os.Stat(modelPath); err != nil {
		return "", fmt.Errorf("whisper model %q not found in %s", whisperModel, m.WhisperModelsDir())
	}
	if whisperPort <= 0 {
		whisperPort = 11987
	}

	// Kill any orphan from a prior process.
	m.stopWhisperLocked()

	args := []string{
		"-m", modelPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", whisperPort),
		"--threads", "4",
	}
	var stderrBuf bytes.Buffer
	cmd := exec.Command(m.WhisperBinaryPath(), args...)
	cmd.Stdout = nil
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start whisper-server: %w", err)
	}

	m.whisperCmd = cmd
	m.whisperPort = whisperPort
	m.whisperModel = whisperModel
	m.lastError = ""
	m.sessionID = newSessionID()
	m.sessionStarted = time.Now()
	m.sessionActive = true
	m.lastActivity = time.Now()
	m.startIdleWatcherLocked()

	// Reap the process in background so ProcessState is set on crash/exit.
	go func() {
		cmd.Wait() //nolint:errcheck
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.whisperCmd == cmd {
			m.whisperCmd = nil
			m.whisperModel = ""
			if msg := firstLine(stderrBuf.String()); msg != "" {
				m.lastError = msg
			}
			// A dying whisper-server implicitly ends the session.
			m.sessionActive = false
		}
	}()

	logstore.Write("info", fmt.Sprintf("voice: session %s started (whisper %s on :%d)", m.sessionID, whisperModel, whisperPort),
		map[string]string{"sessionID": m.sessionID, "whisperModel": whisperModel})
	return m.sessionID, nil
}

// WaitWhisperReady polls whisper-server's health endpoint until it accepts
// connections or the timeout elapses. whisper.cpp's server does not expose a
// strict /health route in all builds — instead we probe the root path and
// consider any HTTP response a success signal.
func (m *Manager) WaitWhisperReady(timeout time.Duration) error {
	m.mu.Lock()
	port := m.whisperPort
	m.mu.Unlock()
	if port == 0 {
		return fmt.Errorf("whisper not started")
	}
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("voice: whisper-server did not become ready within %s", timeout)
}

// EndSession kills both subprocess servers and clears session state.
func (m *Manager) EndSession() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.endSessionLocked()
}

func (m *Manager) endSessionLocked() error {
	if m.watcherStop != nil {
		close(m.watcherStop)
		m.watcherStop = nil
	}
	m.stopWhisperLocked()
	m.stopKokoroLocked()
	if m.sessionActive {
		logstore.Write("info", fmt.Sprintf("voice: session %s ended", m.sessionID),
			map[string]string{"sessionID": m.sessionID})
	}
	m.sessionActive = false
	m.sessionID = ""
	m.sessionStarted = time.Time{}
	return nil
}

func (m *Manager) stopWhisperLocked() {
	if m.whisperCmd == nil || m.whisperCmd.Process == nil {
		return
	}
	_ = m.whisperCmd.Process.Kill()
	m.whisperCmd = nil
	m.whisperModel = ""
	m.whisperPort = 0
}

// Close is called on runtime shutdown to ensure no orphan subprocesses.
func (m *Manager) Close() {
	_ = m.EndSession()
}

func (m *Manager) startIdleWatcherLocked() {
	if m.idleTimeout <= 0 {
		return
	}
	if m.watcherStop != nil {
		close(m.watcherStop)
	}
	m.watcherStop = make(chan struct{})
	stop := m.watcherStop
	timeout := m.idleTimeout

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.mu.Lock()
				idle := time.Since(m.lastActivity)
				active := m.sessionActive
				m.mu.Unlock()
				if active && idle >= timeout {
					logstore.Write("info",
						fmt.Sprintf("voice: idle %s — ending session", idle.Round(time.Second)),
						map[string]string{"idle": idle.String()})
					_ = m.EndSession()
					return
				}
			}
		}
	}()
}

// ActiveDownload returns the last download progress snapshot.
func (m *Manager) ActiveDownload() DownloadProgress {
	m.dlMu.Lock()
	defer m.dlMu.Unlock()
	return m.dlProgress
}

// ClearDownload wipes the stored download progress.
func (m *Manager) ClearDownload() {
	m.dlMu.Lock()
	m.dlProgress = DownloadProgress{}
	m.dlMu.Unlock()
}

func (m *Manager) setDownloadProgress(component, filename, url string, downloaded, total int64, active bool) {
	pct := 0.0
	if total > 0 {
		pct = float64(downloaded) / float64(total) * 100
	}
	m.dlMu.Lock()
	m.dlProgress = DownloadProgress{
		Active:     active,
		Component:  component,
		Filename:   filename,
		URL:        url,
		Downloaded: downloaded,
		Total:      total,
		Percent:    pct,
	}
	m.dlMu.Unlock()
}

// ListModels returns all model files for the requested component.
// Supported: "whisper".
func (m *Manager) ListModels(component string) ([]VoiceModelInfo, error) {
	var dir, ext string
	switch component {
	case "whisper":
		dir = m.WhisperModelsDir()
		ext = ".bin"
	default:
		return nil, fmt.Errorf("unknown voice component: %q", component)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []VoiceModelInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []VoiceModelInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ext {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, VoiceModelInfo{
			Name:      e.Name(),
			Component: component,
			SizeBytes: info.Size(),
			SizeHuman: humanBytes(info.Size()),
		})
	}
	if out == nil {
		out = []VoiceModelInfo{}
	}
	return out, nil
}

// DeleteModel removes a model file. Refuses to delete the currently-loaded
// whisper model while a session is active.
func (m *Manager) DeleteModel(component, name string) error {
	if filepath.Base(name) != name || name == "" {
		return fmt.Errorf("invalid model name")
	}
	switch component {
	case "whisper":
		m.mu.Lock()
		loaded := m.whisperModel
		active := m.sessionActive
		m.mu.Unlock()
		if active && loaded == name {
			return fmt.Errorf("cannot delete the loaded whisper model — end the voice session first")
		}
		return os.Remove(filepath.Join(m.WhisperModelsDir(), name))
	default:
		return fmt.Errorf("unknown voice component: %q", component)
	}
}

func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "vs_" + hex.EncodeToString(b[:])
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return trimSpace(s[:i])
		}
	}
	return trimSpace(s)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
