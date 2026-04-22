package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPathsFromTextSkipsMindPromptFiles(t *testing.T) {
	dir := t.TempDir()
	mindPath := filepath.Join(dir, "MIND.md")
	if err := os.WriteFile(mindPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write MIND.md: %v", err)
	}

	paths := ExtractPathsFromText("Here is the path: " + mindPath)
	if len(paths) != 0 {
		t.Fatalf("expected MIND.md to be filtered, got %v", paths)
	}
}

func TestExtractArtifactPathsSkipsMindPromptFiles(t *testing.T) {
	dir := t.TempDir()
	mindPath := filepath.Join(dir, "MIND.md")
	if err := os.WriteFile(mindPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write MIND.md: %v", err)
	}

	paths := ExtractArtifactPaths(map[string]any{"path": mindPath})
	if len(paths) != 0 {
		t.Fatalf("expected MIND.md to be filtered, got %v", paths)
	}
}

func TestRegisterArtifactRejectsMindPromptFiles(t *testing.T) {
	dir := t.TempDir()
	mindPath := filepath.Join(dir, "MIND.md")
	if err := os.WriteFile(mindPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write MIND.md: %v", err)
	}

	token := RegisterArtifact(mindPath)
	if token != "" {
		t.Fatalf("expected no token for MIND.md, got %q", token)
	}
}
