package voice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
)

// Manager owns the voice session lifecycle. whisper-server (STT) starts when
// a session begins; kokoro-onnx (TTS) starts lazily on first synthesize call.
// Both are killed together when the session ends or idles out. Safe for
// concurrent use.
type Manager struct {
	installDir string // ~/Library/Application Support/Atlas — binaries under voice/
	modelsDir  string // ~/Library/Application Support/ProjectAtlas/voice-models
	cfgStore   *config.Store

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

	// Active provider adapters — rebuilt when ActiveAudioProvider changes.
	adapterMu       sync.Mutex
	activeProvider  ProviderType
	sttAdapter      STTAdapter
	ttsAdapter      TTSAdapter
}

// NewManager creates a Manager.
//   - installDir: Atlas install dir (binaries live under installDir/voice/)
//   - modelsDir:  parent dir for whisper/ and kokoro/ model subdirs
//   - cfgStore:   runtime config store used to resolve the active audio provider
func NewManager(installDir, modelsDir string, cfgStore *config.Store) *Manager {
	return &Manager{
		installDir: installDir,
		modelsDir:  modelsDir,
		cfgStore:   cfgStore,
	}
}

// resolveAdapters returns the STT and TTS adapters for the current config,
// rebuilding them when the active provider has changed.
func (m *Manager) resolveAdapters() (STTAdapter, TTSAdapter) {
	cfg := m.cfgStore.Load()
	providerCfg := resolveAudioProvider(cfg)

	m.adapterMu.Lock()
	defer m.adapterMu.Unlock()

	if m.sttAdapter == nil || m.ttsAdapter == nil || m.activeProvider != providerCfg.Type {
		m.sttAdapter = newSTTAdapter(providerCfg, m)
		m.ttsAdapter = newTTSAdapter(providerCfg, m)
		m.activeProvider = providerCfg.Type
		logstore.Write("info", "voice: adapters resolved for provider "+string(providerCfg.Type), nil)
	}
	return m.sttAdapter, m.ttsAdapter
}

// Transcribe routes audio to the active STT provider.
// For the local provider this auto-starts whisper-server if needed.
func (m *Manager) Transcribe(ctx context.Context, audio []byte, mimeType, language string) (TranscribeResult, error) {
	stt, _ := m.resolveAdapters()
	return stt.Transcribe(ctx, audio, mimeType, language)
}

// Synthesize routes text to the active TTS provider, streaming PCM chunks via emit.
func (m *Manager) Synthesize(ctx context.Context, text, voice string, emit func(SynthesizeChunk) error) error {
	cfg := m.cfgStore.Load()
	speed := floatOr(cfg.AudioTTSSpeed, 1.0)
	_, tts := m.resolveAdapters()
	return tts.Synthesize(ctx, text, voice, speed, emit)
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

// DefaultKokoroPort returns the configured Kokoro port, falling back to 11989.
func (m *Manager) DefaultKokoroPort() int {
	if cfg := m.cfgStore.Load(); cfg.VoiceKokoroPort > 0 {
		return cfg.VoiceKokoroPort
	}
	return 11989
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
		SessionActive:   m.sessionActive,
		SessionID:       m.sessionID,
		WhisperRunning:  m.isWhisperRunningLocked(),
		WhisperReady:    m.WhisperBinaryReady(),
		WhisperPort:     m.whisperPort,
		WhisperModel:    m.whisperModel,
		WhisperBuildTag: m.WhisperVersion(),
		KokoroRunning:   m.isKokoroRunningLocked(),
		KokoroReady:     m.kokoroReadyLocked(),
		KokoroPort:      m.kokoroPort,
		KokoroVersion:   m.KokoroVersion(),
		LastError:        m.lastError,
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
	cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+m.VoiceBinDir())

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

// WhisperVersionFilePath is the file written by `make download-whisper` that
// records the whisper.cpp tag used to build the installed whisper-server binary.
func (m *Manager) WhisperVersionFilePath() string {
	return filepath.Join(m.VoiceBinDir(), "whisper-server.version")
}

// WhisperVersion reads the installed whisper-server build tag (e.g. "v1.8.4").
// Returns "" if the binary is not installed or no version file is present.
func (m *Manager) WhisperVersion() string {
	data, err := os.ReadFile(m.WhisperVersionFilePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// PipBinaryPath returns the path to pip inside the voice Python venv.
func (m *Manager) PipBinaryPath() string {
	return filepath.Join(m.VoiceBinDir(), "venv", "bin", "pip")
}

// KokoroVersion returns the installed kokoro-onnx pip package version by
// running `pip show kokoro-onnx` inside the voice venv.
func (m *Manager) KokoroVersion() string {
	out, err := exec.Command(m.PipBinaryPath(), "show", "kokoro-onnx").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
	}
	return ""
}

// progressWriter wraps a progress callback as an io.Writer so it can be used
// for both cmd.Stdout and cmd.Stderr simultaneously.
type progressWriter struct{ fn func(string) }

func (pw *progressWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			pw.fn(line)
		}
	}
	return len(p), nil
}

// RebuildWhisper clones whisper.cpp at the given tag and rebuilds whisper-server,
// replacing the installed binary. Progress lines are streamed to the progress func.
func (m *Manager) RebuildWhisper(version string, progress func(string)) error {
	src := "/tmp/atlas-whisper-src"
	pw := &progressWriter{fn: progress}

	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout = pw
		cmd.Stderr = pw
		return cmd.Run()
	}

	progress("Removing old sources…")
	_ = os.RemoveAll(src)

	progress("Cloning whisper.cpp " + version + "…")
	if err := run("git", "clone", "--depth", "1", "--branch", version,
		"https://github.com/ggml-org/whisper.cpp.git", src); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	progress("Configuring cmake…")
	if err := run("cmake", "-S", src, "-B", src+"/build",
		"-DCMAKE_BUILD_TYPE=Release", "-DWHISPER_BUILD_EXAMPLES=ON"); err != nil {
		return fmt.Errorf("cmake configure: %w", err)
	}

	progress("Building whisper-server…")
	err := run("cmake", "--build", src+"/build", "--target", "whisper-server", "-j", "--config", "Release")
	if err != nil {
		err = run("cmake", "--build", src+"/build", "--target", "server", "-j", "--config", "Release")
	}
	if err != nil {
		return fmt.Errorf("cmake build: %w", err)
	}

	// Find the built binary.
	var binPath string
	_ = filepath.Walk(src+"/build", func(p string, fi os.FileInfo, err error) error {
		if err != nil || binPath != "" {
			return nil
		}
		name := fi.Name()
		if !fi.IsDir() && (name == "whisper-server" || name == "server") {
			if fi.Mode()&0o111 != 0 {
				binPath = p
			}
		}
		return nil
	})
	if binPath == "" {
		return fmt.Errorf("whisper-server binary not found after build")
	}

	dest := m.WhisperBinaryPath()
	progress("Installing whisper-server…")
	data, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	if err := os.WriteFile(dest, data, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	_ = exec.Command("codesign", "--force", "--sign", "-", dest).Run()

	// Persist version tag.
	_ = os.WriteFile(m.WhisperVersionFilePath(), []byte(version+"\n"), 0o644)

	_ = os.RemoveAll(src)
	progress("Done.")
	return nil
}

// findBestVoicePython returns the best Python interpreter that satisfies
// kokoro-onnx's requirement of >=3.10,<3.14. Probes known absolute paths
// first so it works under launchd's minimal PATH.
func findBestVoicePython() string {
	candidates := []string{
		"/Library/Frameworks/Python.framework/Versions/3.13/bin/python3.13",
		"/Library/Frameworks/Python.framework/Versions/3.12/bin/python3.12",
		"/Library/Frameworks/Python.framework/Versions/3.11/bin/python3.11",
		"/Library/Frameworks/Python.framework/Versions/3.10/bin/python3.10",
		"/opt/homebrew/bin/python3.13",
		"/opt/homebrew/bin/python3.12",
		"/opt/homebrew/bin/python3.11",
		"/opt/homebrew/bin/python3.10",
		"/usr/local/bin/python3.13",
		"/usr/local/bin/python3.12",
		"/usr/local/bin/python3.11",
		"/usr/local/bin/python3.10",
		"python3.13", "python3.12", "python3.11", "python3.10",
	}
	for _, c := range candidates {
		var path string
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err != nil {
				continue
			}
			path = c
		} else {
			var err error
			path, err = exec.LookPath(c)
			if err != nil {
				continue
			}
		}
		out, err := exec.Command(path, "-c", "import sys; print('%d %d' % sys.version_info[:2])").Output()
		if err != nil {
			continue
		}
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) < 2 {
			continue
		}
		var major, minor int
		fmt.Sscanf(parts[0], "%d", &major)
		fmt.Sscanf(parts[1], "%d", &minor)
		if major == 3 && minor >= 10 && minor <= 13 {
			logstore.Write("info", fmt.Sprintf("voice: selected Python %d.%d at %s", major, minor, path), nil)
			return path
		}
	}
	return ""
}

// venvPythonMinor returns the minor version of the Python interpreter inside
// the voice venv (e.g. 14 for 3.14). Returns 0 on error.
func (m *Manager) venvPythonMinor() int {
	py := filepath.Join(m.VoiceBinDir(), "venv", "bin", "python")
	out, err := exec.Command(py, "-c", "import sys; print(sys.version_info.minor)").Output()
	if err != nil {
		return 0
	}
	var minor int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &minor)
	return minor
}

// UpgradeKokoro upgrades the kokoro-onnx pip package. If the current venv
// runs Python 3.14+ (incompatible with kokoro-onnx >=0.4.8), it recreates
// the venv with a <=3.13 interpreter before running pip.
func (m *Manager) UpgradeKokoro(version string, progress func(string)) error {
	venvDir := filepath.Join(m.VoiceBinDir(), "venv")

	// Recreate venv if it's on an incompatible Python version.
	if minor := m.venvPythonMinor(); minor >= 14 {
		bestPy := findBestVoicePython()
		if bestPy == "" {
			return fmt.Errorf("no Python 3.10–3.13 found; kokoro-onnx 0.5+ requires Python <3.14")
		}
		progress(fmt.Sprintf("Recreating voice venv with %s (Python 3.14 is incompatible with kokoro-onnx 0.5+)…", bestPy))
		if err := os.RemoveAll(venvDir); err != nil {
			return fmt.Errorf("remove old venv: %w", err)
		}
		pw := &progressWriter{fn: progress}
		cmd := exec.Command(bestPy, "-m", "venv", venvDir)
		cmd.Stdout = pw
		cmd.Stderr = pw
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("create venv: %w", err)
		}
		// Reinstall kokoro-onnx dependencies after venv rebuild.
		progress("Reinstalling kokoro-onnx in new venv…")
	}

	pip := m.PipBinaryPath()
	if _, err := os.Stat(pip); err != nil {
		return fmt.Errorf("pip not found at %s", pip)
	}

	spec := "kokoro-onnx"
	if version != "" && version != "latest" {
		spec = "kokoro-onnx==" + version
	}

	pw := &progressWriter{fn: progress}
	cmd := exec.Command(pip, "install", "--upgrade", spec)
	cmd.Stdout = pw
	cmd.Stderr = pw
	return cmd.Run()
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
