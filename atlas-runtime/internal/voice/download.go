package voice

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadModel downloads a model file into the appropriate models directory.
// Currently only Whisper models are downloaded this way; Kokoro model + voices
// are single static artifacts fetched via the Makefile since they're a fixed
// pair that doesn't need a per-voice download flow.
func (m *Manager) DownloadModel(ctx context.Context, url, filename, component string, progress func(int64, int64)) error {
	if filepath.Base(filename) != filename || filename == "" {
		return fmt.Errorf("invalid filename")
	}
	var dir, ext string
	switch component {
	case "whisper":
		dir = m.WhisperModelsDir()
		ext = ".bin"
	default:
		return fmt.Errorf("unknown voice component: %q", component)
	}
	if !strings.HasSuffix(strings.ToLower(filename), ext) {
		return fmt.Errorf("filename must end with %s", ext)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create models dir: %w", err)
	}

	destPath := filepath.Join(dir, filename)
	tmpPath := destPath + ".download"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	var startOffset int64
	if info, statErr := os.Stat(tmpPath); statErr == nil {
		startOffset = info.Size()
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	if total > 0 && startOffset > 0 && resp.StatusCode == http.StatusPartialContent {
		total += startOffset
	}

	fileFlag := os.O_CREATE | os.O_WRONLY
	if startOffset > 0 && resp.StatusCode == http.StatusPartialContent {
		fileFlag |= os.O_APPEND
	} else {
		fileFlag |= os.O_TRUNC
		startOffset = 0
	}
	f, err := os.OpenFile(tmpPath, fileFlag, 0o600)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}

	downloaded := startOffset
	modelComp := component + "-model"
	m.setDownloadProgress(modelComp, filename, url, downloaded, total, true)
	defer func() {
		m.setDownloadProgress(modelComp, filename, url, downloaded, total, false)
	}()

	lastReport := time.Now()
	buf := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			f.Close()
			return ctx.Err()
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("write: %w", werr)
			}
			downloaded += int64(n)
			if time.Since(lastReport) >= 250*time.Millisecond {
				m.setDownloadProgress(modelComp, filename, url, downloaded, total, true)
				if progress != nil {
					progress(downloaded, total)
				}
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return fmt.Errorf("read: %w", readErr)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	m.setDownloadProgress(modelComp, filename, url, downloaded, downloaded, true)
	if progress != nil {
		progress(downloaded, downloaded)
	}
	return os.Rename(tmpPath, destPath)
}
