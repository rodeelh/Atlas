package skills

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemBinaryAndFormatCreators(t *testing.T) {
	supportDir := t.TempDir()
	rootDir := filepath.Join(supportDir, "approved")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(rootDir): %v", err)
	}
	if err := SaveFsRoots(supportDir, []FsRoot{{ID: "root1", Path: rootDir}}); err != nil {
		t.Fatalf("SaveFsRoots: %v", err)
	}

	t.Run("write binary file", func(t *testing.T) {
		target := filepath.Join(rootDir, "payload.bin")
		args, _ := json.Marshal(map[string]any{
			"path":           target,
			"content_base64": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
		})
		if _, err := fsWriteBinaryFile(nil, args, supportDir); err != nil {
			t.Fatalf("fsWriteBinaryFile: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(binary): %v", err)
		}
		if !bytes.Equal(data, []byte{0x01, 0x02, 0x03, 0x04}) {
			t.Fatalf("unexpected binary data: %v", data)
		}
	})

	t.Run("create pdf", func(t *testing.T) {
		target := filepath.Join(rootDir, "report.pdf")
		args, _ := json.Marshal(map[string]any{
			"path":    target,
			"title":   "Atlas Report",
			"content": "Line one\nLine two",
		})
		if _, err := fsCreatePDF(nil, args, supportDir); err != nil {
			t.Fatalf("fsCreatePDF: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(pdf): %v", err)
		}
		if !bytes.HasPrefix(data, []byte("%PDF-1.4")) {
			t.Fatalf("expected PDF header, got %q", string(data[:min(len(data), 8)]))
		}
		if !bytes.Contains(data, []byte("/Type /Catalog")) {
			t.Fatalf("expected catalog object in pdf")
		}
	})

	t.Run("create docx", func(t *testing.T) {
		target := filepath.Join(rootDir, "notes.docx")
		args, _ := json.Marshal(map[string]any{
			"path":    target,
			"title":   "Atlas Notes",
			"content": "Hello DOCX",
		})
		if _, err := fsCreateDOCX(nil, args, supportDir); err != nil {
			t.Fatalf("fsCreateDOCX: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(docx): %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("zip.NewReader(docx): %v", err)
		}
		var foundDocument bool
		for _, file := range zr.File {
			if file.Name == "word/document.xml" {
				foundDocument = true
				rc, err := file.Open()
				if err != nil {
					t.Fatalf("Open(document.xml): %v", err)
				}
				body, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					t.Fatalf("ReadAll(document.xml): %v", err)
				}
				if !strings.Contains(string(body), "Hello DOCX") {
					t.Fatalf("expected docx body text, got %s", string(body))
				}
			}
		}
		if !foundDocument {
			t.Fatal("expected word/document.xml in docx")
		}
	})

	t.Run("create zip", func(t *testing.T) {
		sourceA := filepath.Join(rootDir, "alpha.txt")
		sourceDir := filepath.Join(rootDir, "nested")
		sourceB := filepath.Join(sourceDir, "beta.txt")
		if err := os.WriteFile(sourceA, []byte("alpha"), 0o644); err != nil {
			t.Fatalf("WriteFile(alpha): %v", err)
		}
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(nested): %v", err)
		}
		if err := os.WriteFile(sourceB, []byte("beta"), 0o644); err != nil {
			t.Fatalf("WriteFile(beta): %v", err)
		}

		target := filepath.Join(rootDir, "bundle.zip")
		args, _ := json.Marshal(map[string]any{
			"path":         target,
			"source_paths": []string{sourceA, sourceDir},
		})
		if _, err := fsCreateZIP(nil, args, supportDir); err != nil {
			t.Fatalf("fsCreateZIP: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(zip): %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("zip.NewReader(zip): %v", err)
		}
		var names []string
		for _, file := range zr.File {
			names = append(names, file.Name)
		}
		joined := strings.Join(names, ",")
		if !strings.Contains(joined, "alpha.txt") || !strings.Contains(joined, "nested/beta.txt") {
			t.Fatalf("unexpected zip contents: %v", names)
		}
	})

	t.Run("save image", func(t *testing.T) {
		target := filepath.Join(rootDir, "pixel.png")
		args, _ := json.Marshal(map[string]any{
			"path":         target,
			"image_base64": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aP1cAAAAASUVORK5CYII=",
		})
		if _, err := fsSaveImage(nil, args, supportDir); err != nil {
			t.Fatalf("fsSaveImage: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile(image): %v", err)
		}
		if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
			t.Fatalf("expected PNG signature")
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
