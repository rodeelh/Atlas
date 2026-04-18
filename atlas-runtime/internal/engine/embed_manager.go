package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
)

// EmbedManager controls a llama-server subprocess running in embedding mode.
// It wraps a Manager but starts with embedding-specific flags:
//   - --embedding (enable /v1/embeddings endpoint)
//   - --rope-scaling yarn + --rope-freq-scale 0.75 (unlock 8192 token context for Nomic v1.5)
//   - No chat flags (--jinja, --parallel, --flash-attn, --defrag-thold, --metrics)
//
// Default model: nomic-embed-text-v1.5.Q4_K_M.gguf
// Default port:  11987
// Embedding dims: 768 (Nomic v1.5)
type EmbedManager struct {
	installDir string
	modelsDir  string

	mu          sync.Mutex
	cmd         *exec.Cmd
	port        int
	loadedModel string
	lastError   string
}

// NewEmbedManager creates an EmbedManager.
//   - installDir: Atlas install dir (binary at installDir/engine/llama-server)
//   - modelsDir:  directory for GGUF model files (shared with the primary Manager)
func NewEmbedManager(installDir, modelsDir string) *EmbedManager {
	return &EmbedManager{installDir: installDir, modelsDir: modelsDir}
}

// BinaryReady reports whether the llama-server binary exists.
func (m *EmbedManager) BinaryReady() bool {
	info, err := os.Stat(m.binaryPath())
	return err == nil && !info.IsDir()
}

func (m *EmbedManager) binaryPath() string {
	return filepath.Join(m.installDir, "engine", "llama-server")
}

// IsRunning reports whether the embedding server process is alive.
func (m *EmbedManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isRunningLocked()
}

func (m *EmbedManager) isRunningLocked() bool {
	return m.cmd != nil && m.cmd.ProcessState == nil
}

// LoadedModel returns the GGUF filename of the currently running model, or "".
func (m *EmbedManager) LoadedModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadedModel
}

// LastError returns the last process exit reason (empty when running cleanly).
func (m *EmbedManager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// Port returns the port the embedding server is listening on (0 if stopped).
func (m *EmbedManager) Port() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.port
}

// Start launches llama-server in embedding mode with the given model and port.
// Embedding-specific flags:
//   - --embedding — expose /v1/embeddings
//   - --rope-scaling yarn + --rope-freq-scale 0.75 — full 8192-token context (Nomic v1.5)
//   - --ctx-size 8192, -b 4096 — match Nomic v1.5 context + efficient batching
func (m *EmbedManager) Start(modelName string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.BinaryReady() {
		return fmt.Errorf("llama-server binary not found — run 'make install' or 'make download-engine'")
	}

	modelPath := filepath.Join(m.modelsDir, modelName)
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("embed model %q not found in models directory", modelName)
	}

	if port <= 0 {
		port = 11988
	}

	m.stopLocked()

	args := []string{
		"--model", modelPath,
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"--ctx-size", "8192",
		"--n-gpu-layers", "99",
		"-b", "4096",
		"--rope-scaling", "yarn",
		"--rope-freq-scale", "0.75",
		"--embedding",
		"--log-disable",
	}

	var stderrBuf bytes.Buffer
	cmd := exec.Command(m.binaryPath(), args...)
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start embed server: %w", err)
	}

	m.cmd = cmd
	m.port = port
	m.loadedModel = modelName
	m.lastError = ""

	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			m.port = 0
			m.loadedModel = ""
			if err != nil {
				m.lastError = err.Error()
				logstore.Write("warn", "embed server exited", map[string]string{"error": err.Error()})
			}
		}
		m.mu.Unlock()
	}()

	logstore.Write("info", "embed server started",
		map[string]string{"model": modelName, "port": fmt.Sprintf("%d", port)})
	return nil
}

// Stop terminates the running embedding server process.
func (m *EmbedManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

func (m *EmbedManager) stopLocked() error {
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	if err := m.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("embed server kill: %w", err)
	}
	m.cmd = nil
	m.loadedModel = ""
	m.port = 0
	return nil
}

// WaitUntilReady polls /health until the server responds 200 or times out.
func (m *EmbedManager) WaitUntilReady(port int, timeout time.Duration) error {
	// Reuse the Manager's health-poll logic via a temporary Manager pointed at
	// the same binary. This avoids duplicating the HTTP health-check loop.
	tmp := NewManager(m.installDir, m.modelsDir)
	return tmp.WaitUntilReady(port, timeout)
}

// BaseURL returns the /v1/embeddings endpoint URL for the running server.
func (m *EmbedManager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/v1/embeddings", m.port)
}

