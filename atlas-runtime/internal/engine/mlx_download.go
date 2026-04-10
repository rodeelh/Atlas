package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DownloadModel downloads an MLX-LM model from HuggingFace using mlx_lm.
//
// repo is a HuggingFace repository ID in the form "org/model-name"
// (e.g. "mlx-community/Llama-3.2-3B-Instruct-4bit"). Atlas derives the
// destination directory name from the last segment of the repo ID.
//
// The download is delegated to mlx_lm as a subprocess:
//
//	python3 -m mlx_lm.convert --hf-path <repo> --mlx-path <dest>
//
// Progress is reported via the progress callback with lines of text as they
// arrive from the subprocess stdout/stderr. Total bytes are not available
// from mlx_lm; Percent is set to -1 to indicate indeterminate progress.
//
// The models directory is created if it does not exist.
// Returns an error if the venv is missing, the repo ID is invalid, or
// the subprocess exits non-zero.
func (m *MLXManager) DownloadModel(ctx context.Context, repo string, progress func(line string)) error {
	if !strings.Contains(repo, "/") {
		return fmt.Errorf("invalid repo ID %q — expected format: org/model-name", repo)
	}
	modelName := filepath.Base(repo) // "Llama-3.2-3B-Instruct-4bit"
	// filepath.Base("/") == "/" and filepath.Base(".") == "." — both are invalid model names.
	// The "" case is defensive only (filepath.Base never returns "").
	if modelName == "" || modelName == "." || modelName == "/" {
		return fmt.Errorf("could not derive model name from repo %q", repo)
	}

	if !m.VenvReady() {
		return fmt.Errorf("MLX-LM Python venv not found — install it from the Engine settings tab")
	}

	if err := os.MkdirAll(m.modelsDir, 0o700); err != nil {
		return fmt.Errorf("create mlx-models directory: %w", err)
	}

	destPath := filepath.Join(m.modelsDir, modelName)

	// Mark download as active.
	m.setDownloadProgress(repo, modelName, 0, -1, true, "")

	// mlx_lm.convert downloads from HuggingFace and quantizes into MLX format.
	// It handles authentication via HF_TOKEN env variable if set.
	args := []string{
		"-m", "mlx_lm.convert",
		"--hf-path", repo,
		"--mlx-path", destPath,
	}

	cmd := exec.CommandContext(ctx, m.pythonBin(), args...)

	// Merge stdout + stderr so all progress lines come through one pipe.
	out, err := cmd.StdoutPipe()
	if err != nil {
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("mlx_lm.convert failed to start: %w", err)
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := out.Read(buf)
		if n > 0 {
			line := strings.TrimSpace(string(buf[:n]))
			if line != "" && progress != nil {
				progress(line)
			}
		}
		if readErr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		// Clean up partial download on failure.
		_ = os.RemoveAll(destPath)
		// Set error explicitly — no defer so this is not overwritten.
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("mlx_lm.convert: %w", err)
	}

	// Success — mark inactive with no error.
	m.setDownloadProgress(repo, modelName, 0, -1, false, "")
	return nil
}

// DownloadModelFromSnapshot downloads a pre-built MLX snapshot directly from
// HuggingFace using the snapshot_download approach (no conversion step).
// This is faster for models already in MLX format (mlx-community repos).
//
// Unlike DownloadModel (which uses mlx_lm.convert), this calls:
//
//	python3 -c "from huggingface_hub import snapshot_download; snapshot_download('<repo>', local_dir='<dest>')"
//
// It requires huggingface_hub to be installed in the venv (mlx-lm pulls it in
// as a dependency, so it is always available after InstallOrUpgrade).
func (m *MLXManager) DownloadModelFromSnapshot(ctx context.Context, repo string, progress func(line string)) error {
	if !strings.Contains(repo, "/") {
		return fmt.Errorf("invalid repo ID %q — expected format: org/model-name", repo)
	}
	modelName := filepath.Base(repo)
	// filepath.Base("/") == "/" and filepath.Base(".") == "." — both are invalid model names.
	// The "" case is defensive only (filepath.Base never returns "").
	if modelName == "" || modelName == "." || modelName == "/" {
		return fmt.Errorf("could not derive model name from repo %q", repo)
	}

	if !m.VenvReady() {
		return fmt.Errorf("MLX-LM Python venv not found — install it from the Engine settings tab")
	}

	if err := os.MkdirAll(m.modelsDir, 0o700); err != nil {
		return fmt.Errorf("create mlx-models directory: %w", err)
	}

	destPath := filepath.Join(m.modelsDir, modelName)

	// Mark download as active — no defer so error state is never overwritten on failure.
	m.setDownloadProgress(repo, modelName, 0, -1, true, "")

	// Python one-liner: snapshot_download writes to local_dir, showing tqdm
	// progress on stderr. We capture both streams.
	// %q escapes " to \" (both Go and Python agree on this escape) — injection-safe.
	script := fmt.Sprintf(
		`from huggingface_hub import snapshot_download; snapshot_download(%q, local_dir=%q)`,
		repo, destPath,
	)

	cmd := exec.CommandContext(ctx, m.pythonBin(), "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("snapshot_download failed to start: %w", err)
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := out.Read(buf)
		if n > 0 {
			line := strings.TrimSpace(string(buf[:n]))
			if line != "" && progress != nil {
				progress(line)
			}
		}
		if readErr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		_ = os.RemoveAll(destPath)
		// Set error explicitly — not overwritten since there is no defer.
		m.setDownloadProgress(repo, modelName, 0, -1, false, err.Error())
		return fmt.Errorf("snapshot_download: %w", err)
	}

	// Success — mark inactive with no error.
	m.setDownloadProgress(repo, modelName, 0, -1, false, "")
	return nil
}
