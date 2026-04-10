package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
)

// MLXManager controls the mlx_lm.server subprocess lifecycle.
// It mirrors the Manager (llama.cpp) design: one process per port,
// idle auto-eject, and the same EngineAutoStarter interface consumed
// by the chat service.
//
// MLX-LM is Apple Silicon only. Call IsAppleSilicon() before enabling
// this subsystem — it will return an error from Start() on x86_64.
//
// Atlas owns the Python venv at venvDir (default ~/.atlas-mlx).
// Models are stored as directories under modelsDir (default
// ~/...ProjectAtlas/mlx-models/) — one subdirectory per model,
// containing safetensors shards and a config.json.
//
// The router pattern is first-class here: create a second MLXManager
// on AtlasMLXRouterPort (11991) and set a lighter model for tool routing.
type MLXManager struct {
	venvDir   string // Python venv owned by Atlas (e.g. ~/.atlas-mlx)
	modelsDir string // ~/...ProjectAtlas/mlx-models/ — each model is a directory

	mu          sync.Mutex
	cmd         *exec.Cmd
	port        int
	loadedModel string // directory name of the currently running model
	lastError   string

	// Idle auto-eject — mirrors Manager behaviour.
	idleTimeout  time.Duration
	lastActivity time.Time
	watcherStop  chan struct{}

	// Download progress — persisted across client reconnects.
	dlMu       sync.Mutex
	dlProgress MLXDownloadProgress

	// Cached values for fields that require subprocess calls (pip show, sysctl)
	// or network fetches (PyPI latest version).
	// Refreshed by RefreshCache() and on InstallOrUpgrade completion.
	// Avoids running subprocesses while holding mu in Status().
	cacheMu         sync.RWMutex
	cachedPkgVer    string
	cachedAppleSi   bool
	cachedLatestVer string // latest mlx-lm version from PyPI

	// Per-turn inference stats — updated by RecordInference after each agent turn.
	inferMu       sync.Mutex
	lastInference *MLXInferenceStats

	warmMu          sync.Mutex
	warmedPromptKey string
	warmupInFlight  string
}

// NewMLXManager creates an MLXManager.
//   - venvDir:   Python venv path (e.g. config.MLXVenvDir())
//   - modelsDir: directory for MLX model directories (e.g. config.MLXModelsDir())
func NewMLXManager(venvDir, modelsDir string) *MLXManager {
	m := &MLXManager{
		venvDir:   venvDir,
		modelsDir: modelsDir,
	}
	// Populate cheap cache synchronously at startup.
	m.cachedAppleSi = IsAppleSilicon()
	m.cachedPkgVer = m.packageVersionUncached()
	// Fetch latest version from PyPI asynchronously so startup is not blocked.
	go func() {
		ver := fetchLatestPyPIVersion()
		m.cacheMu.Lock()
		m.cachedLatestVer = ver
		m.cacheMu.Unlock()
	}()
	return m
}

// RefreshCache re-runs the subprocess-backed queries (pip show, sysctl) and
// the PyPI latest-version fetch, then updates the in-memory cache.
// Safe to call from any goroutine. Called automatically after InstallOrUpgrade
// so the version badge reflects the new package immediately.
func (m *MLXManager) RefreshCache() {
	ver := m.packageVersionUncached()
	si := IsAppleSilicon()
	latest := fetchLatestPyPIVersion()
	m.cacheMu.Lock()
	m.cachedPkgVer = ver
	m.cachedAppleSi = si
	if latest != "" {
		m.cachedLatestVer = latest
	}
	m.cacheMu.Unlock()
}

// fetchLatestPyPIVersion queries the PyPI JSON API for the latest mlx-lm release.
// Returns an empty string on any error (network unavailable, rate-limited, etc.).
// Source of truth: https://pypi.org/project/mlx-lm/
func fetchLatestPyPIVersion() string {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("https://pypi.org/pypi/mlx-lm/json")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return ""
	}
	var result struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.Info.Version)
}

// findBestPython returns the path to the highest Python interpreter available
// that is >= 3.10 (required for mlx-lm 0.30+).
//
// The daemon runs under launchd with a minimal PATH that may not include
// framework or Homebrew directories. We therefore probe known absolute
// installation paths in addition to PATH-based lookup.
func findBestPython() string {
	// Ordered from highest to lowest preference. Absolute paths are checked
	// by stat (works regardless of launchd PATH); bare names fall back to LookPath.
	candidates := []string{
		// Framework installs (python.org macOS pkg)
		"/Library/Frameworks/Python.framework/Versions/3.13/bin/python3.13",
		"/Library/Frameworks/Python.framework/Versions/3.12/bin/python3.12",
		"/Library/Frameworks/Python.framework/Versions/3.11/bin/python3.11",
		"/Library/Frameworks/Python.framework/Versions/3.10/bin/python3.10",
		// Apple Silicon Homebrew
		"/opt/homebrew/bin/python3.13",
		"/opt/homebrew/bin/python3.12",
		"/opt/homebrew/bin/python3.11",
		"/opt/homebrew/bin/python3.10",
		// Intel Homebrew
		"/usr/local/bin/python3.13",
		"/usr/local/bin/python3.12",
		"/usr/local/bin/python3.11",
		"/usr/local/bin/python3.10",
		// PATH-based (works when launchd PATH includes the right directories)
		"python3.13",
		"python3.12",
		"python3.11",
		"python3.10",
	}

	for _, candidate := range candidates {
		var path string
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			path = candidate
		} else {
			var err error
			path, err = exec.LookPath(candidate)
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
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major > 3 || (major == 3 && minor >= 10) {
			logstore.Write("info", fmt.Sprintf("MLX-LM: selected Python %d.%d at %s", major, minor, path), nil)
			return path
		}
	}
	logstore.Write("warn", "MLX-LM: no Python >= 3.10 found — falling back to python3 (mlx-lm upgrade may be limited)", nil)
	return "python3"
}

// venvPythonVersion returns the major and minor version of the Python
// interpreter inside the managed venv. Returns (0, 0) on error.
func (m *MLXManager) venvPythonVersion() (major, minor int) {
	out, err := exec.Command(m.pythonBin(), "-c", "import sys; print('%d %d' % sys.version_info[:2])").Output()
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ = strconv.Atoi(parts[0])
	minor, _ = strconv.Atoi(parts[1])
	return
}

// IsAppleSilicon reports whether the host is running on Apple Silicon.
// MLX-LM only works on M-series chips. Returns false on Intel Macs.
func IsAppleSilicon() bool {
	out, err := exec.Command("sysctl", "-n", "hw.optional.arm64").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// systemRAMGB returns total physical RAM in whole gigabytes via sysctl hw.memsize.
// Returns 0 on error so callers can apply a safe fallback.
func systemRAMGB() int {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return int(bytes / (1024 * 1024 * 1024))
}

// mlxPromptCacheBytes returns the KV cache memory cap in bytes for
// --prompt-cache-bytes. We use 60 % of total RAM, leaving headroom for
// the OS and other apps. Floor is 4 GB.
func mlxPromptCacheBytes() int64 {
	total := systemRAMGB()
	if total <= 0 {
		return 8 * 1024 * 1024 * 1024 // 8 GB safe fallback
	}
	limitGB := int(float64(total) * 0.6)
	if limitGB < 4 {
		limitGB = 4
	}
	return int64(limitGB) * 1024 * 1024 * 1024
}

// pythonBin returns the path to the Python executable inside the managed venv.
func (m *MLXManager) pythonBin() string {
	return filepath.Join(m.venvDir, "bin", "python3")
}

// VenvReady reports whether the managed Python venv exists and contains
// a python3 executable. Does not verify that mlx-lm is installed.
func (m *MLXManager) VenvReady() bool {
	info, err := os.Stat(m.pythonBin())
	return err == nil && !info.IsDir()
}

// PackageVersion returns the cached mlx-lm version string (e.g. "0.21.3").
// The cache is populated at construction and refreshed after InstallOrUpgrade.
// Does NOT run a subprocess — safe to call while holding any lock.
func (m *MLXManager) PackageVersion() string {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	return m.cachedPkgVer
}

// packageVersionUncached runs `pip show mlx-lm` and returns the version string.
// Only called from NewMLXManager and RefreshCache — never while holding mu.
func (m *MLXManager) packageVersionUncached() string {
	if !m.VenvReady() {
		return ""
	}
	pip := filepath.Join(m.venvDir, "bin", "pip")
	out, err := exec.Command(pip, "show", "mlx-lm").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(line, "Version: "); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// SetIdleTimeout configures automatic ejection after d of inactivity.
// Must be called before Start. Zero disables idle ejection.
func (m *MLXManager) SetIdleTimeout(d time.Duration) {
	m.mu.Lock()
	m.idleTimeout = d
	m.mu.Unlock()
}

// SetMlock is a no-op for MLX — MLX manages its own memory layout via the
// Apple MLX framework and does not expose a mlock equivalent. Present to
// satisfy the same configuration interface as the llama.cpp Manager.
func (m *MLXManager) SetMlock(_ bool) {}

// RecordActivity resets the idle timer. Call after each completed inference turn.
func (m *MLXManager) RecordActivity() {
	m.mu.Lock()
	m.lastActivity = time.Now()
	m.mu.Unlock()
}

// RecordInference stores per-turn performance stats after a completed agent turn.
// promptTokens and completionTokens come from the provider usage response;
// elapsed is the wall-clock duration of the full turn (including prompt eval).
// Decode TPS is computed as completionTokens / elapsed. This is a conservative
// lower bound — it includes scheduling overhead — but it's the only timing
// available without patching mlx_lm.server itself.
func (m *MLXManager) RecordInference(promptTokens, completionTokens int, elapsed time.Duration, firstToken time.Duration, streamChunks int, streamChars int) {
	if elapsed <= 0 || completionTokens <= 0 {
		return
	}
	tps := float64(completionTokens) / elapsed.Seconds()
	avgChunkChars := 0.0
	if streamChunks > 0 && streamChars > 0 {
		avgChunkChars = float64(streamChars) / float64(streamChunks)
	}
	stats := &MLXInferenceStats{
		DecodeTPS:        tps,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		GenerationSec:    elapsed.Seconds(),
		FirstTokenSec:    firstToken.Seconds(),
		StreamChunks:     streamChunks,
		StreamChars:      streamChars,
		AvgChunkChars:    avgChunkChars,
	}
	m.inferMu.Lock()
	m.lastInference = stats
	m.inferMu.Unlock()
}

// startIdleWatcher launches a background goroutine that stops the model after
// idleTimeout of inactivity. Mirrors Manager.startIdleWatcher exactly.
func (m *MLXManager) startIdleWatcher() {
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
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				m.mu.Lock()
				idle := time.Since(m.lastActivity)
				running := m.isRunningLocked()
				m.mu.Unlock()
				if running && idle >= timeout {
					logstore.Write("info",
						fmt.Sprintf("MLX Engine idle for %s — auto-ejecting model", idle.Round(time.Minute)),
						map[string]string{"idle": idle.String()})
					_ = m.Stop()
					return
				}
			}
		}
	}()
}

// ModelsDir returns the directory where MLX model directories are stored.
func (m *MLXManager) ModelsDir() string {
	return m.modelsDir
}

// IsRunning reports whether the mlx_lm.server process is currently alive.
func (m *MLXManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isRunningLocked()
}

func (m *MLXManager) isRunningLocked() bool {
	return m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil
}

// LoadedModel returns the model directory name currently loaded, empty if not running.
func (m *MLXManager) LoadedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadedModel
}

// LastError returns the last process exit reason, or empty string if healthy.
func (m *MLXManager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// PrefillPrompt warms the mlx_lm.server KV cache by sending a one-token
// completion request containing only the system prompt. The response is
// discarded — the goal is to pre-compute KV states so the first real user
// turn hits the cache instead of paying full prefill cost.
//
// Call this in a goroutine immediately after WaitUntilReady returns. The
// request queues on the single-threaded MLX server ahead of the first user
// turn, which typically arrives a few seconds later (model still warming up
// in the UI). On a 2 000-token system prompt at --prefill-step-size 4096 the
// warm-up completes in under one second on M-series hardware.
func (m *MLXManager) PrefillPrompt(ctx context.Context, port int, model, systemPrompt string) {
	if systemPrompt == "" {
		return
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	if stats := MLXSchedulerSnapshot(baseURL); stats.ActiveRequests > 0 || stats.QueueDepth > 0 {
		logstore.Write("info", "MLX KV warm-up skipped because interactive traffic is active", map[string]string{
			"model": model,
		})
		return
	}
	promptKey := model + "\x00" + systemPrompt
	m.warmMu.Lock()
	if m.warmedPromptKey == promptKey || m.warmupInFlight == promptKey {
		m.warmMu.Unlock()
		return
	}
	m.warmupInFlight = promptKey
	m.warmMu.Unlock()
	defer func() {
		m.warmMu.Lock()
		if m.warmupInFlight == promptKey {
			m.warmupInFlight = ""
		}
		m.warmMu.Unlock()
	}()

	release, _, _, err := AcquireMLXRequest(ctx, baseURL)
	if err != nil {
		return
	}
	defer release()

	url := fmt.Sprintf("%s/chat/completions", baseURL)
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
		},
		"max_tokens": 1,
		"stream":     false,
	})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		logstore.Write("warn", "MLX KV cache warm-up failed", map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		logstore.Write("warn", "MLX KV cache warm-up returned non-200", map[string]string{
			"model":  model,
			"status": strconv.Itoa(resp.StatusCode),
		})
		return
	}
	m.warmMu.Lock()
	m.warmedPromptKey = promptKey
	m.warmMu.Unlock()
	logstore.Write("info", "MLX KV cache warmed — first-turn prefill cost eliminated", map[string]string{"model": model})
}

// Status returns a snapshot of the current MLX engine state.
// PackageVersion, LatestVersion, and IsAppleSilicon are read from the
// in-memory cache — no subprocesses are launched while holding mu.
func (m *MLXManager) Status(cfgPort int) MLXStatus {
	m.cacheMu.RLock()
	pkgVer := m.cachedPkgVer
	latestVer := m.cachedLatestVer
	appleSi := m.cachedAppleSi
	m.cacheMu.RUnlock()

	m.inferMu.Lock()
	lastInf := m.lastInference
	m.inferMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	port := m.port
	if port == 0 {
		port = cfgPort
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
	return MLXStatus{
		Running:        m.isRunningLocked(),
		LoadedModel:    m.loadedModel,
		Port:           port,
		VenvReady:      m.VenvReady(),
		PackageVersion: pkgVer,
		LatestVersion:  latestVer,
		LastError:      m.lastError,
		IsAppleSilicon: appleSi,
		LastInference:  lastInf,
		Scheduler:      MLXSchedulerSnapshot(baseURL),
	}
}

func (m *MLXManager) ConfigureScheduler(port, maxConcurrency, batchWindowMs int) MLXSchedulerStats {
	if port == 0 {
		port = 11990
	}
	window := time.Duration(batchWindowMs) * time.Millisecond
	return ConfigureMLXScheduler(fmt.Sprintf("http://127.0.0.1:%d/v1", port), maxConcurrency, window)
}

func (m *MLXManager) SchedulerSnapshot(port int) MLXSchedulerStats {
	if port == 0 {
		port = 11990
	}
	return MLXSchedulerSnapshot(fmt.Sprintf("http://127.0.0.1:%d/v1", port))
}

// Start launches mlx_lm.server with the given model directory, port, and max-tokens.
//
// modelName is the directory name under modelsDir (e.g. "Llama-3.2-3B-Instruct-4bit").
// ctxSize maps to --max-tokens; defaults to 4096 if <= 0.
// kvCacheQuant and draftModel are reserved for future use — they are
// accepted to satisfy the same EngineAutoStarter interface as the llama.cpp Manager.
//
// If a server is already running it is stopped first.
func (m *MLXManager) Start(modelName string, port int, ctxSize int, _ string, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !IsAppleSilicon() {
		return fmt.Errorf("MLX-LM requires Apple Silicon — this machine is not supported")
	}
	if !m.VenvReady() {
		return fmt.Errorf("MLX-LM Python venv not found at %s — install it from the Engine settings tab", m.venvDir)
	}

	modelPath := filepath.Join(m.modelsDir, modelName)
	if _, err := os.Stat(filepath.Join(modelPath, "config.json")); err != nil {
		return fmt.Errorf("MLX model %q not found — expected directory with config.json at %s", modelName, modelPath)
	}

	if ctxSize <= 0 {
		ctxSize = 4096
	}

	modelBase := filepath.Base(modelName)
	if m.isRunningLocked() && m.loadedModel == modelBase && m.port == port {
		m.lastActivity = time.Now()
		return nil
	}

	// Stop any existing process first.
	m.stopLocked()
	m.warmMu.Lock()
	m.warmedPromptKey = ""
	m.warmupInFlight = ""
	m.warmMu.Unlock()

	args := []string{
		"-m", "mlx_lm.server",
		"--model", modelPath,
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"--max-tokens", fmt.Sprintf("%d", ctxSize),
		"--trust-remote-code",    // required for some model families (Phi, Qwen, etc.)
		"--log-level", "WARNING", // suppress verbose mlx-lm output
		"--prompt-cache-bytes", fmt.Sprintf("%d", mlxPromptCacheBytes()), // cap KV cache at 60 % of total RAM
		"--prompt-cache-size", "1", // single-user: one KV cache is sufficient
		"--prefill-step-size", "4096", // 2× default (2048) for faster prompt prefill
	}

	var stderrBuf bytes.Buffer
	cmd := exec.Command(m.pythonBin(), args...)
	cmd.Stdout = nil
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mlx_lm.server: %w", err)
	}

	m.cmd = cmd
	m.port = port
	m.loadedModel = modelBase
	m.lastError = ""
	m.lastActivity = time.Now()
	m.startIdleWatcher()

	// Reap process in background so ProcessState is set on crash/exit.
	go func() {
		cmd.Wait() //nolint:errcheck
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			m.loadedModel = ""
			if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
				lines := strings.Split(msg, "\n")
				m.lastError = lines[len(lines)-1]
			}
		}
		m.mu.Unlock()
	}()

	return nil
}

// WaitUntilReady polls the mlx_lm.server /health endpoint until the server
// reports 200 OK (model fully loaded) or the timeout elapses.
// mlx_lm.server follows the same /health convention as llama-server.
func (m *MLXManager) WaitUntilReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("mlx engine: mlx_lm.server did not become ready within %s", timeout)
}

// IsLoading checks the /health endpoint to determine if the model is still loading.
func (m *MLXManager) IsLoading(port int) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusServiceUnavailable
}

// FetchMetrics is a stub for MLX-LM — mlx_lm.server does not expose a
// Prometheus /metrics endpoint. Returns a zeroed snapshot so callers
// that expect MetricsSnapshot can degrade gracefully.
func (m *MLXManager) FetchMetrics(_ int) MetricsSnapshot {
	return MetricsSnapshot{}
}

// Stop terminates the running mlx_lm.server process.
func (m *MLXManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watcherStop != nil {
		close(m.watcherStop)
		m.watcherStop = nil
	}
	return m.stopLocked()
}

// stopLocked must be called with m.mu held.
func (m *MLXManager) stopLocked() error {
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	if err := m.cmd.Process.Kill(); err != nil {
		return err
	}
	m.cmd = nil
	m.loadedModel = ""
	m.warmMu.Lock()
	m.warmedPromptKey = ""
	m.warmupInFlight = ""
	m.warmMu.Unlock()
	return nil
}

// ListModels returns all MLX model directories in modelsDir.
// Each entry is a subdirectory that contains a config.json file.
func (m *MLXManager) ListModels() ([]MLXModelInfo, error) {
	entries, err := os.ReadDir(m.modelsDir)
	if os.IsNotExist(err) {
		return []MLXModelInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var models []MLXModelInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only include directories that look like MLX models (contain config.json).
		cfgPath := filepath.Join(m.modelsDir, e.Name(), "config.json")
		if _, statErr := os.Stat(cfgPath); statErr != nil {
			continue
		}
		sizeBytes := dirSizeBytes(filepath.Join(m.modelsDir, e.Name()))
		models = append(models, MLXModelInfo{
			Name:      e.Name(),
			SizeBytes: sizeBytes,
			SizeHuman: humanBytes(sizeBytes),
		})
	}
	return models, nil
}

// DeleteModel removes a model directory from modelsDir.
// Returns an error if the model is currently loaded.
func (m *MLXManager) DeleteModel(name string) error {
	// filepath.Base("/") == "/" so the base-equality check would pass for "/",
	// but filepath.Join(modelsDir, "/") resolves to modelsDir itself — deleting
	// the entire models directory. Guard explicitly.
	if name == "" || name == "/" || name == "." || filepath.Base(name) != name {
		return fmt.Errorf("invalid model name")
	}
	m.mu.Lock()
	loaded := m.loadedModel
	m.mu.Unlock()
	if loaded == name {
		return fmt.Errorf("cannot delete the currently loaded model — stop MLX Engine first")
	}
	return os.RemoveAll(filepath.Join(m.modelsDir, name))
}

// ActiveDownload returns the progress of the current or most recently
// interrupted model download. Repo is empty if no download has started.
func (m *MLXManager) ActiveDownload() MLXDownloadProgress {
	m.dlMu.Lock()
	defer m.dlMu.Unlock()
	return m.dlProgress
}

// ClearDownload resets the stored download progress.
func (m *MLXManager) ClearDownload() {
	m.dlMu.Lock()
	m.dlProgress = MLXDownloadProgress{}
	m.dlMu.Unlock()
}

func (m *MLXManager) setDownloadProgress(repo, modelName string, downloaded, total int64, active bool, errMsg string) {
	pct := 0.0
	if total > 0 {
		pct = float64(downloaded) / float64(total) * 100
	}
	m.dlMu.Lock()
	m.dlProgress = MLXDownloadProgress{
		Active:     active,
		Repo:       repo,
		ModelName:  modelName,
		Downloaded: downloaded,
		Total:      total,
		Percent:    pct,
		Error:      errMsg,
	}
	m.dlMu.Unlock()
}

// InstallOrUpgrade creates the Python venv (if absent) and installs or
// upgrades the mlx-lm package. Progress lines are streamed to the
// progress callback as they arrive from pip's stdout.
//
// If the existing venv uses Python < 3.10 (mlx-lm 0.30+ requires ≥ 3.10),
// the old venv is removed and recreated with the best Python available.
//
// This is a blocking call — run in a goroutine when serving HTTP.
func (m *MLXManager) InstallOrUpgrade(progress func(line string)) error {
	bestPython := findBestPython()

	// Step 1: rebuild venv if it exists but uses Python < 3.10.
	if m.VenvReady() {
		major, minor := m.venvPythonVersion()
		if major > 0 && (major < 3 || (major == 3 && minor < 10)) {
			msg := fmt.Sprintf("Rebuilding venv: upgrading from Python %d.%d → newer version (mlx-lm 0.30+ requires Python 3.10+)…", major, minor)
			logstore.Write("info", "MLX-LM: rebuilding venv for Python version upgrade",
				map[string]string{"old": fmt.Sprintf("%d.%d", major, minor), "new": bestPython})
			if progress != nil {
				progress(msg)
			}
			if err := os.RemoveAll(m.venvDir); err != nil {
				return fmt.Errorf("failed to remove old venv for rebuild: %w", err)
			}
		}
	}

	// Step 2: create venv if it doesn't exist.
	if !m.VenvReady() {
		logstore.Write("info", "Creating MLX-LM Python venv", map[string]string{"venv": m.venvDir, "python": bestPython})
		out, err := exec.Command(bestPython, "-m", "venv", m.venvDir).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to create venv: %s: %w", strings.TrimSpace(string(out)), err)
		}
		if progress != nil {
			progress(fmt.Sprintf("Created Python venv at %s (%s)", m.venvDir, bestPython))
		}
	}

	// Step 3: install or upgrade mlx-lm.
	pip := filepath.Join(m.venvDir, "bin", "pip")
	args := []string{"install", "--upgrade", "mlx-lm"}
	cmd := exec.Command(pip, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr so progress lines are visible

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("pip install failed to start: %w", err)
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 && progress != nil {
			progress(strings.TrimSpace(string(buf[:n])))
		}
		if readErr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("pip install mlx-lm failed: %w", err)
	}
	// Refresh the cached package version so Status() and the UI reflect the new version.
	m.RefreshCache()
	logstore.Write("info", "MLX-LM installed/upgraded", map[string]string{"version": m.PackageVersion()})
	return nil
}

// dirSizeBytes returns the total size of all files in dir, recursively.
// Returns 0 on any error.
func dirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
