package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// errWalkLimit is returned by WalkDir callbacks to stop early once the result
// cap is reached. Using a named sentinel avoids fragile string comparisons.
var errWalkLimit = errors.New("walk limit reached")

const (
	noRootsMsg  = "No file system roots approved. Add approved directories in Atlas Settings → Skills → File System."
	maxFileSize = 50 * 1024 // 50KB
)

func (r *Registry) registerFilesystem() {
	supportDir := r.supportDir

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.list_directory",
			Description: "Lists files and directories at the given path (must be within an approved root).",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to the directory to list", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel: "read",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsListDirectory(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.read_file",
			Description: "Reads the content of a file (max 50KB, must be within an approved root).",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to the file to read", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel: "read",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsReadFile(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.get_metadata",
			Description: "Returns size, modification time, and type for a path (must be within an approved root).",
			Properties: map[string]ToolParam{
				"path": {Description: "Absolute path to the file or directory", Type: "string"},
			},
			Required: []string{"path"},
		},
		PermLevel: "read",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsGetMetadata(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.search",
			Description: "Finds files matching a glob pattern under a path (must be within an approved root).",
			Properties: map[string]ToolParam{
				"path":    {Description: "Root directory to search", Type: "string"},
				"pattern": {Description: "Glob pattern (e.g. '*.go', '**/*.txt')", Type: "string"},
			},
			Required: []string{"path", "pattern"},
		},
		PermLevel: "read",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsSearch(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.content_search",
			Description: "Searches file contents for a text query under a path (must be within an approved root).",
			Properties: map[string]ToolParam{
				"path":  {Description: "Root directory to search", Type: "string"},
				"query": {Description: "Text to search for in file contents", Type: "string"},
			},
			Required: []string{"path", "query"},
		},
		PermLevel: "read",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsContentSearch(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.write_file",
			Description: "Writes content to a file (creates or overwrites). Requires an approved root. Returns a unified diff showing what changed.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to write to", Type: "string"},
				"content":        {Description: "Full content to write", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "content"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsWriteFile(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.patch_file",
			Description: "Applies a unified diff patch to an existing file. Use this for targeted edits to large files. Requires an approved root.",
			Properties: map[string]ToolParam{
				"path":  {Description: "Absolute path to the file to patch", Type: "string"},
				"patch": {Description: "Unified diff patch string (--- / +++ / @@ hunks)", Type: "string"},
			},
			Required: []string{"path", "patch"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsPatchFile(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.write_binary_file",
			Description: "Writes a base64-encoded binary file (creates or overwrites). Requires an approved root.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to write to", Type: "string"},
				"content_base64": {Description: "Base64-encoded file content. Data URLs are also accepted.", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "content_base64"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsWriteBinaryFile(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.create_directory",
			Description: "Creates a directory at the given path (must be within an approved root).",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path of the directory to create", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true (like mkdir -p)", Type: "boolean"},
			},
			Required: []string{"path"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsCreateDirectory(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.create_pdf",
			Description: "Creates a PDF file from text content. Use this whenever the user asks to save, export, or create a PDF — never use fs.write_file for PDF output. Requires an approved root.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to the PDF file to create", Type: "string"},
				"title":          {Description: "Optional title shown at the top of the document", Type: "string"},
				"content":        {Description: "Main document text content", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "content"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsCreatePDF(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.create_docx",
			Description: "Creates a DOCX document from text content. Requires an approved root.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to the DOCX file to create", Type: "string"},
				"title":          {Description: "Optional title shown at the top of the document", Type: "string"},
				"content":        {Description: "Main document text content", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "content"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsCreateDOCX(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.create_zip",
			Description: "Creates a ZIP archive from approved files or folders. Requires approved roots for both source paths and the destination path.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to the ZIP file to create", Type: "string"},
				"source_paths":   {Description: "Absolute file or directory paths to include in the archive", Type: "array", Items: &ToolParam{Type: "string"}},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "source_paths"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsCreateZIP(ctx, args, supportDir)
		},
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "fs.save_image",
			Description: "Saves a PNG, JPEG, or GIF image from base64 data. Requires an approved root.",
			Properties: map[string]ToolParam{
				"path":           {Description: "Absolute path to the image file to create", Type: "string"},
				"image_base64":   {Description: "Base64-encoded PNG, JPEG, or GIF data. Data URLs are also accepted.", Type: "string"},
				"create_parents": {Description: "Create missing parent directories if true", Type: "boolean"},
			},
			Required: []string{"path", "image_base64"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return fsSaveImage(ctx, args, supportDir)
		},
	})
}

// ── approved roots ────────────────────────────────────────────────────────────

// FsRoot is a single approved file-system root entry persisted in go-fs-roots.json.
type FsRoot struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// NewFsRootID generates a random 8-byte hex ID for a new root entry.
func NewFsRootID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// LoadFsRoots reads go-fs-roots.json and returns the approved roots.
// Supports both the legacy []string format and the current []FsRoot format.
func LoadFsRoots(supportDir string) ([]FsRoot, error) {
	data, err := os.ReadFile(filepath.Join(supportDir, "go-fs-roots.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Try new []FsRoot format first.
	var roots []FsRoot
	if err := json.Unmarshal(data, &roots); err == nil {
		return roots, nil
	}
	// Fall back to legacy []string format and migrate IDs on the fly.
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil, err
	}
	out := make([]FsRoot, len(paths))
	for i, p := range paths {
		out[i] = FsRoot{ID: NewFsRootID(), Path: p}
	}
	return out, nil
}

// SaveFsRoots writes roots atomically to go-fs-roots.json.
func SaveFsRoots(supportDir string, roots []FsRoot) error {
	data, err := json.MarshalIndent(roots, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(supportDir, "go-fs-roots.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadApprovedRoots returns just the path strings for internal fs enforcement.
func loadApprovedRoots(supportDir string) ([]string, error) {
	roots, err := LoadFsRoots(supportDir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(roots))
	for i, r := range roots {
		paths[i] = r.Path
	}
	return paths, nil
}

func checkApproved(path string, roots []string) error {
	if len(roots) == 0 {
		return fmt.Errorf(noRootsMsg)
	}
	clean := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if strings.HasPrefix(clean, cleanRoot+string(filepath.Separator)) || clean == cleanRoot {
			return nil
		}
	}
	return fmt.Errorf("path %q is not within any approved root. Approved roots: %s", path, strings.Join(roots, ", "))
}

func approvedRootsForPath(supportDir, path string) ([]string, error) {
	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return nil, err
	}
	if err := checkApproved(path, roots); err != nil {
		return nil, err
	}
	return roots, nil
}

func writeBytesAtomically(path string, data []byte, createParents bool) error {
	if createParents {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("could not create parent directories: %w", err)
		}
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atlas-write-*.tmp")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("could not write file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("could not close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("could not rename file: %w", err)
	}
	return nil
}

func decodeBase64Payload(value string) ([]byte, error) {
	payload := strings.TrimSpace(value)
	if payload == "" {
		return nil, fmt.Errorf("base64 content is required")
	}
	if comma := strings.Index(payload, ","); strings.HasPrefix(payload, "data:") && comma >= 0 {
		payload = payload[comma+1:]
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err == nil {
		return data, nil
	}
	data, rawErr := base64.RawStdEncoding.DecodeString(payload)
	if rawErr == nil {
		return data, nil
	}
	return nil, fmt.Errorf("invalid base64 payload: %w", err)
}

// ── fs.list_directory ─────────────────────────────────────────────────────────

func fsListDirectory(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return "", err
	}
	if err := checkApproved(p.Path, roots); err != nil {
		return "", err
	}

	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return "", fmt.Errorf("could not read directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Contents of %s:\n", p.Path))
	for _, e := range entries {
		info, _ := e.Info()
		typeMark := ""
		if e.IsDir() {
			typeMark = "/"
		}
		size := ""
		if info != nil && !e.IsDir() {
			size = fmt.Sprintf(" (%d bytes)", info.Size())
		}
		sb.WriteString(fmt.Sprintf("  %s%s%s\n", e.Name(), typeMark, size))
	}
	if len(entries) == 0 {
		sb.WriteString("  (empty directory)\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── fs.read_file ──────────────────────────────────────────────────────────────

func fsReadFile(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return "", err
	}
	if err := checkApproved(p.Path, roots); err != nil {
		return "", err
	}

	f, err := os.Open(p.Path)
	if err != nil {
		return "", fmt.Errorf("could not open file: %w", err)
	}
	defer f.Close()

	content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	if err != nil {
		return "", fmt.Errorf("could not read file: %w", err)
	}

	result := string(content)
	info, _ := f.Stat()
	if info != nil && info.Size() > maxFileSize {
		result += "\n... [file truncated at 50KB]"
	}
	return result, nil
}

// ── fs.get_metadata ───────────────────────────────────────────────────────────

func fsGetMetadata(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return "", err
	}
	if err := checkApproved(p.Path, roots); err != nil {
		return "", err
	}

	info, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("could not stat path: %w", err)
	}

	fileType := "file"
	if info.IsDir() {
		fileType = "directory"
	}

	return fmt.Sprintf("Path: %s\nType: %s\nSize: %d bytes\nModified: %s\nMode: %s",
		p.Path, fileType, info.Size(),
		info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
		info.Mode().String(),
	), nil
}

// ── fs.search ─────────────────────────────────────────────────────────────────

func fsSearch(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.Pattern == "" {
		return "", fmt.Errorf("path and pattern are required")
	}

	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return "", err
	}
	if err := checkApproved(p.Path, roots); err != nil {
		return "", err
	}

	var matches []string
	err = filepath.WalkDir(p.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable dirs
		}
		matched, matchErr := filepath.Match(p.Pattern, d.Name())
		if matchErr != nil {
			return matchErr
		}
		if matched {
			matches = append(matches, path)
		}
		if len(matches) >= 100 {
			return errWalkLimit
		}
		return nil
	})
	if err != nil && !errors.Is(err, errWalkLimit) {
		return "", fmt.Errorf("search error: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No files matching %q found under %s", p.Pattern, p.Path), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files matching %q under %s:\n", p.Pattern, p.Path))
	for _, m := range matches {
		sb.WriteString("  " + m + "\n")
	}
	if len(matches) == 100 {
		sb.WriteString("  ... (results limited to 100)\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── fs.content_search ─────────────────────────────────────────────────────────

func fsContentSearch(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path  string `json:"path"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.Query == "" {
		return "", fmt.Errorf("path and query are required")
	}

	roots, err := loadApprovedRoots(supportDir)
	if err != nil {
		return "", err
	}
	if err := checkApproved(p.Path, roots); err != nil {
		return "", err
	}

	type match struct {
		File string
		Line int
		Text string
	}
	var matches []match

	err = filepath.WalkDir(p.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		// Skip large files.
		info, _ := d.Info()
		if info != nil && info.Size() > maxFileSize {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, p.Query) {
				matches = append(matches, match{File: path, Line: i + 1, Text: strings.TrimSpace(line)})
				if len(matches) >= 50 {
					return errWalkLimit
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errWalkLimit) {
		return "", fmt.Errorf("content search error: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No files containing %q found under %s", p.Query, p.Path), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files containing %q under %s:\n", p.Query, p.Path))
	for _, m := range matches {
		// Trim long lines.
		text := m.Text
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %s:%d: %s\n", m.File, m.Line, text))
	}
	if len(matches) == 50 {
		sb.WriteString("  ... (results limited to 50)\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── fs.write_file ─────────────────────────────────────────────────────────────

func fsWriteFile(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		Content       string `json:"content"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path and content are required")
	}

	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}

	// Read existing content so we can generate a diff.
	var oldContent string
	isNew := false
	existing, readErr := os.ReadFile(p.Path)
	if os.IsNotExist(readErr) {
		isNew = true
	} else if readErr != nil {
		return "", fmt.Errorf("could not read existing file: %w", readErr)
	} else {
		oldContent = string(existing)
	}

	if err := writeBytesAtomically(p.Path, []byte(p.Content), p.CreateParents); err != nil {
		return "", err
	}

	if isNew {
		return fmt.Sprintf("Created %s (%d bytes)", p.Path, len(p.Content)), nil
	}
	diff := UnifiedDiff(p.Path, p.Path, oldContent, p.Content)
	if diff == "" {
		return fmt.Sprintf("No changes — %s content is identical", p.Path), nil
	}
	return fmt.Sprintf("Updated %s\n\n%s", p.Path, diff), nil
}

// ── fs.patch_file ─────────────────────────────────────────────────────────────

func fsPatchFile(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path  string `json:"path"`
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.Patch == "" {
		return "", fmt.Errorf("path and patch are required")
	}

	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}

	existing, err := os.ReadFile(p.Path)
	if err != nil {
		return "", fmt.Errorf("could not read file: %w", err)
	}
	oldContent := string(existing)

	newContent, err := ApplyPatch(oldContent, p.Patch)
	if err != nil {
		return "", fmt.Errorf("patch failed: %w", err)
	}

	if err := writeBytesAtomically(p.Path, []byte(newContent), false); err != nil {
		return "", err
	}

	diff := UnifiedDiff(p.Path, p.Path, oldContent, newContent)
	return fmt.Sprintf("Patched %s\n\n%s", p.Path, diff), nil
}

// ── fs.create_directory ───────────────────────────────────────────────────────

func fsCreateDirectory(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}

	if p.CreateParents {
		if err := os.MkdirAll(p.Path, 0o755); err != nil {
			return "", fmt.Errorf("could not create directories: %w", err)
		}
		return fmt.Sprintf("Created directory (with parents): %s", p.Path), nil
	}
	if err := os.Mkdir(p.Path, 0o755); err != nil {
		return "", fmt.Errorf("could not create directory: %w", err)
	}
	return fmt.Sprintf("Created directory: %s", p.Path), nil
}

func fsWriteBinaryFile(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		ContentBase64 string `json:"content_base64"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.ContentBase64 == "" {
		return "", fmt.Errorf("path and content_base64 are required")
	}
	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}
	data, err := decodeBase64Payload(p.ContentBase64)
	if err != nil {
		return "", err
	}
	if err := writeBytesAtomically(p.Path, data, p.CreateParents); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(data), p.Path), nil
}

func fsCreatePDF(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		Title         string `json:"title"`
		Content       string `json:"content"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || strings.TrimSpace(p.Content) == "" {
		return "", fmt.Errorf("path and content are required")
	}
	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}

	// Try pandoc + typst for rich Markdown rendering.
	// Falls back to the built-in renderer if pandoc/typst are not installed.
	engine := ""
	pandocPath, pandocErr := exec.LookPath("pandoc")
	if pandocErr == nil {
		for _, e := range []string{"typst", "pdflatex", "xelatex", "lualatex", "weasyprint"} {
			if _, err := exec.LookPath(e); err == nil {
				engine = e
				break
			}
		}
	}

	var data []byte
	var renderer string
	if pandocErr == nil && engine != "" {
		var err error
		data, err = buildPDFWithPandoc(pandocPath, engine, p.Title, p.Content)
		if err == nil {
			renderer = "pandoc+" + engine
		}
	}
	if data == nil {
		var err error
		data, err = buildSimplePDF(p.Title, p.Content)
		if err != nil {
			return "", err
		}
		renderer = "built-in"
	}

	if err := writeBytesAtomically(p.Path, data, p.CreateParents); err != nil {
		return "", err
	}

	msg := fmt.Sprintf("Created PDF %s (%d bytes, renderer: %s)", p.Path, len(data), renderer)
	if pandocErr != nil {
		msg += ". Note: pandoc not found — call terminal.brew_install with packages=[\"pandoc\",\"typst\"] for rich Markdown layout."
	} else if engine == "" {
		msg += ". Note: no PDF engine found — call terminal.brew_install with packages=[\"typst\"] to enable pandoc rendering."
	}
	return msg, nil
}

// buildPDFWithPandoc renders Markdown content to PDF via pandoc + a PDF engine.
func buildPDFWithPandoc(pandocPath, engine, title, content string) ([]byte, error) {
	// Build the markdown source — prepend YAML front matter if a title is set.
	var md strings.Builder
	if t := strings.TrimSpace(title); t != "" {
		fmt.Fprintf(&md, "---\ntitle: %q\n---\n\n", t)
	}
	md.WriteString(content)

	tmpIn, err := os.CreateTemp("", "atlas-pdf-in-*.md")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpIn.Name())
	if _, err := tmpIn.WriteString(md.String()); err != nil {
		tmpIn.Close()
		return nil, err
	}
	tmpIn.Close()

	tmpOut := strings.TrimSuffix(tmpIn.Name(), filepath.Ext(tmpIn.Name())) + ".pdf"
	defer os.Remove(tmpOut)

	cmd := exec.Command(pandocPath, "--pdf-engine="+engine, "-o", tmpOut, tmpIn.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pandoc: %w — %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.ReadFile(tmpOut)
}

func fsCreateDOCX(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		Title         string `json:"title"`
		Content       string `json:"content"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || strings.TrimSpace(p.Content) == "" {
		return "", fmt.Errorf("path and content are required")
	}
	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}
	data, err := buildSimpleDOCX(p.Title, p.Content)
	if err != nil {
		return "", err
	}
	if err := writeBytesAtomically(p.Path, data, p.CreateParents); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created DOCX %s (%d bytes)", p.Path, len(data)), nil
}

func fsCreateZIP(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string   `json:"path"`
		SourcePaths   []string `json:"source_paths"`
		CreateParents bool     `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || len(p.SourcePaths) == 0 {
		return "", fmt.Errorf("path and source_paths are required")
	}
	roots, err := approvedRootsForPath(supportDir, p.Path)
	if err != nil {
		return "", err
	}
	for _, sourcePath := range p.SourcePaths {
		if sourcePath == "" {
			return "", fmt.Errorf("source_paths cannot contain empty values")
		}
		if err := checkApproved(sourcePath, roots); err != nil {
			return "", err
		}
	}
	data, fileCount, err := buildZIPArchive(p.SourcePaths)
	if err != nil {
		return "", err
	}
	if err := writeBytesAtomically(p.Path, data, p.CreateParents); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created ZIP %s with %d entries", p.Path, fileCount), nil
}

func fsSaveImage(_ context.Context, args json.RawMessage, supportDir string) (string, error) {
	var p struct {
		Path          string `json:"path"`
		ImageBase64   string `json:"image_base64"`
		CreateParents bool   `json:"create_parents"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Path == "" || p.ImageBase64 == "" {
		return "", fmt.Errorf("path and image_base64 are required")
	}
	if _, err := approvedRootsForPath(supportDir, p.Path); err != nil {
		return "", err
	}
	data, err := decodeBase64Payload(p.ImageBase64)
	if err != nil {
		return "", err
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("image data must be a valid PNG, JPEG, or GIF: %w", err)
	}
	if err := writeBytesAtomically(p.Path, data, p.CreateParents); err != nil {
		return "", err
	}
	return fmt.Sprintf("Saved %s image to %s (%d bytes)", strings.ToUpper(format), p.Path, len(data)), nil
}

// ── Markdown-aware PDF renderer ────────────────────────────────────────────
//
// Supports block-level: # headings (1–3), - bullet lists, ``` fenced code,
// --- horizontal rules, blank-line paragraph breaks.
// Supports inline: **bold** (font switch within a line).
// Uses PDF 1.4 standard Type1 fonts — no external dependencies.

type pdfRun struct {
	text string
	font string  // F1=Helvetica  F2=Helvetica-Bold  F3=Helvetica-Oblique  F4=Courier
	size float64
}

type pdfLine struct {
	runs        []pdfRun
	x           float64
	spaceBefore float64
	lineHeight  float64
	isRule      bool
}

const (
	pdfPageW  = 612.0
	pdfPageH  = 792.0
	pdfMarL   = 50.0
	pdfMarR   = 50.0
	pdfMarT   = 60.0
	pdfMarB   = 60.0
	pdfStartY = pdfPageH - pdfMarT // 732
	pdfEndY   = pdfMarB            // 60
)

func buildSimplePDF(title, content string) ([]byte, error) {
	lines := parsePDFContent(title, content)
	if len(lines) == 0 {
		return nil, fmt.Errorf("content is required")
	}
	pages := pdfPaginate(lines)
	return pdfAssemble(pages)
}

// parsePDFContent converts title + Markdown content into a flat list of pdfLines.
func parsePDFContent(title, content string) []pdfLine {
	var out []pdfLine

	if t := strings.TrimSpace(title); t != "" {
		for wi, wl := range wrapText(t, 52) {
			sb := 0.0
			if wi > 0 {
				sb = 0
			}
			out = append(out, pdfLine{
				runs:        []pdfRun{{text: wl, font: "F2", size: 20}},
				x:           pdfMarL,
				spaceBefore: sb,
				lineHeight:  26,
			})
		}
		out = append(out, pdfLine{isRule: true, x: pdfMarL, spaceBefore: 8, lineHeight: 10})
	}

	rawLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	inCode := false

	for _, raw := range rawLines {
		stripped := strings.TrimSpace(raw)

		if strings.HasPrefix(stripped, "```") {
			inCode = !inCode
			out = append(out, pdfLine{lineHeight: 4})
			continue
		}

		if inCode {
			cl := raw
			if len(cl) > 80 {
				cl = cl[:80]
			}
			out = append(out, pdfLine{
				runs:       []pdfRun{{text: cl, font: "F4", size: 10}},
				x:          pdfMarL + 20,
				lineHeight: 13,
			})
			continue
		}

		if stripped == "" {
			out = append(out, pdfLine{lineHeight: 8})
			continue
		}

		if stripped == "---" || stripped == "___" || stripped == "***" {
			out = append(out, pdfLine{isRule: true, x: pdfMarL, spaceBefore: 4, lineHeight: 8})
			continue
		}

		// Headings
		if level, text := parsePDFHeading(stripped); level > 0 {
			type hStyle struct {
				size, lh, sb float64
				ww            int
			}
			styles := [3]hStyle{{18, 26, 16, 57}, {15, 22, 12, 68}, {13, 19, 10, 79}}
			s := styles[2]
			if level <= 3 {
				s = styles[level-1]
			}
			for wi, wl := range wrapText(text, s.ww) {
				sb := s.sb
				if wi > 0 {
					sb = 0
				}
				out = append(out, pdfLine{
					runs:        []pdfRun{{text: wl, font: "F2", size: s.size}},
					x:           pdfMarL,
					spaceBefore: sb,
					lineHeight:  s.lh,
				})
			}
			continue
		}

		// Bullet list
		if btext, ok := parsePDFBullet(stripped); ok {
			for wi, wl := range wrapText(btext, 82) {
				var runs []pdfRun
				if wi == 0 {
					runs = append([]pdfRun{{text: "- ", font: "F1", size: 12}}, parsePDFInline(wl, 12)...)
				} else {
					runs = append([]pdfRun{{text: "  ", font: "F1", size: 12}}, parsePDFInline(wl, 12)...)
				}
				sb := 1.0
				if wi == 0 {
					sb = 4
				}
				out = append(out, pdfLine{
					runs:        runs,
					x:           pdfMarL + 18,
					spaceBefore: sb,
					lineHeight:  16,
				})
			}
			continue
		}

		// Regular paragraph
		for wi, wl := range wrapText(stripped, 85) {
			sb := 0.0
			if wi == 0 {
				sb = 1
			}
			out = append(out, pdfLine{
				runs:        parsePDFInline(wl, 12),
				x:           pdfMarL,
				spaceBefore: sb,
				lineHeight:  16,
			})
		}
	}

	return out
}

func parsePDFHeading(s string) (int, string) {
	for level := 3; level >= 1; level-- {
		prefix := strings.Repeat("#", level) + " "
		if strings.HasPrefix(s, prefix) {
			return level, strings.TrimPrefix(s, prefix)
		}
	}
	return 0, ""
}

func parsePDFBullet(s string) (string, bool) {
	for _, pfx := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(s, pfx) {
			return strings.TrimPrefix(s, pfx), true
		}
	}
	return "", false
}

// parsePDFInline splits text into styled runs on **bold** markers.
func parsePDFInline(text string, size float64) []pdfRun {
	var runs []pdfRun
	for len(text) > 0 {
		i := strings.Index(text, "**")
		if i == -1 {
			runs = append(runs, pdfRun{text: text, font: "F1", size: size})
			break
		}
		if i > 0 {
			runs = append(runs, pdfRun{text: text[:i], font: "F1", size: size})
		}
		text = text[i+2:]
		j := strings.Index(text, "**")
		if j == -1 {
			runs = append(runs, pdfRun{text: text, font: "F2", size: size})
			break
		}
		if j > 0 {
			runs = append(runs, pdfRun{text: text[:j], font: "F2", size: size})
		}
		text = text[j+2:]
	}
	return runs
}

// pdfPaginate assigns lines to pages based on Y position tracking.
func pdfPaginate(lines []pdfLine) [][]pdfLine {
	var pages [][]pdfLine
	var cur []pdfLine
	y := pdfStartY
	for _, line := range lines {
		need := line.spaceBefore + line.lineHeight
		if y-need < pdfEndY && len(cur) > 0 {
			pages = append(pages, cur)
			cur = nil
			y = pdfStartY
		}
		cur = append(cur, line)
		y -= need
	}
	if len(cur) > 0 {
		pages = append(pages, cur)
	}
	if len(pages) == 0 {
		pages = [][]pdfLine{nil}
	}
	return pages
}

// pdfAssemble builds the final PDF binary from paginated lines.
// Object layout: 1–4 = fonts, then pairs (content, page) per page, then pages dict, catalog.
func pdfAssemble(pages [][]pdfLine) ([]byte, error) {
	const numFonts = 4
	n := len(pages)
	pagesID := numFonts + n*2 + 1
	catalogID := pagesID + 1
	total := catalogID

	objs := make([]string, total+1)
	objs[1] = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`
	objs[2] = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>`
	objs[3] = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Oblique /Encoding /WinAnsiEncoding >>`
	objs[4] = `<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>`

	pageIDs := make([]int, n)
	for i, page := range pages {
		cid := numFonts + 1 + i*2
		pid := cid + 1
		pageIDs[i] = pid
		stream := pdfRenderPage(page)
		objs[cid] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
		objs[pid] = fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R /F2 2 0 R /F3 3 0 R /F4 4 0 R >> >> /Contents %d 0 R >>`,
			pagesID, cid,
		)
	}

	kids := make([]string, n)
	for i, id := range pageIDs {
		kids[i] = fmt.Sprintf("%d 0 R", id)
	}
	objs[pagesID] = fmt.Sprintf(`<< /Type /Pages /Count %d /Kids [%s] >>`, n, strings.Join(kids, " "))
	objs[catalogID] = fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, pagesID)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xFF\xFF\xFF\xFF\n")
	offsets := make([]int, total+1)
	for id := 1; id <= total; id++ {
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, objs[id])
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", total+1)
	buf.WriteString("0000000000 65535 f \n")
	for id := 1; id <= total; id++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF", total+1, catalogID, xref)
	return buf.Bytes(), nil
}

// pdfRenderPage emits the content stream for a single page.
// Each line is positioned absolutely via Tm so Y tracking is exact.
func pdfRenderPage(lines []pdfLine) string {
	var sb strings.Builder
	y := pdfStartY

	for _, line := range lines {
		y -= line.spaceBefore

		if line.isRule {
			fmt.Fprintf(&sb, "0.7 g\n0.5 w\n%.1f %.1f m %.1f %.1f l S\n0 g\n",
				pdfMarL, y, pdfPageW-pdfMarR, y)
			y -= line.lineHeight
			continue
		}

		if len(line.runs) == 0 {
			y -= line.lineHeight
			continue
		}

		sb.WriteString("BT\n")
		fmt.Fprintf(&sb, "1 0 0 1 %.1f %.1f Tm\n", line.x, y)
		curFont, curSize := "", 0.0
		for _, run := range line.runs {
			if run.text == "" {
				continue
			}
			if run.font != curFont || run.size != curSize {
				fmt.Fprintf(&sb, "/%s %.1f Tf\n", run.font, run.size)
				curFont, curSize = run.font, run.size
			}
			fmt.Fprintf(&sb, "(%s) Tj\n", escapePDFText(run.text))
		}
		sb.WriteString("ET\n")
		y -= line.lineHeight
	}

	return sb.String()
}

func escapePDFText(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return replacer.Replace(value)
}

func buildSimpleDOCX(title, content string) ([]byte, error) {
	lines := renderDocumentLines(title, content, 0)
	if len(lines) == 0 {
		return nil, fmt.Errorf("content is required")
	}
	var document strings.Builder
	document.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	document.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, line := range lines {
		document.WriteString(`<w:p><w:r><w:t xml:space="preserve">`)
		document.WriteString(escapeXMLText(line))
		document.WriteString(`</w:t></w:r></w:p>`)
	}
	document.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	document.WriteString(`</w:body></w:document>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
		"word/document.xml": document.String(),
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("create docx entry %s: %w", name, err)
		}
		if _, err := io.WriteString(w, files[name]); err != nil {
			return nil, fmt.Errorf("write docx entry %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close docx archive: %w", err)
	}
	return buf.Bytes(), nil
}

func buildZIPArchive(sourcePaths []string) ([]byte, int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entryCount := 0

	for _, sourcePath := range sourcePaths {
		info, err := os.Lstat(sourcePath)
		if err != nil {
			zw.Close()
			return nil, 0, fmt.Errorf("stat %s: %w", sourcePath, err)
		}
		baseName := filepath.Base(sourcePath)
		if info.IsDir() {
			err = filepath.WalkDir(sourcePath, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				rel, err := filepath.Rel(sourcePath, path)
				if err != nil {
					return err
				}
				archiveName := filepath.ToSlash(baseName)
				if rel != "." {
					archiveName = filepath.ToSlash(filepath.Join(baseName, rel))
				}
				if d.IsDir() {
					if rel == "." {
						return nil
					}
					_, err := zw.Create(archiveName + "/")
					if err == nil {
						entryCount++
					}
					return err
				}
				if d.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("symlinks are not supported in zip sources: %s", path)
				}
				return addZipFile(zw, path, archiveName, &entryCount)
			})
			if err != nil {
				zw.Close()
				return nil, 0, err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			zw.Close()
			return nil, 0, fmt.Errorf("symlinks are not supported in zip sources: %s", sourcePath)
		}
		if err := addZipFile(zw, sourcePath, filepath.ToSlash(baseName), &entryCount); err != nil {
			zw.Close()
			return nil, 0, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, 0, fmt.Errorf("close zip archive: %w", err)
	}
	return buf.Bytes(), entryCount, nil
}

func addZipFile(zw *zip.Writer, sourcePath, archiveName string, entryCount *int) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sourcePath, err)
	}
	w, err := zw.Create(archiveName)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", archiveName, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write zip entry %s: %w", archiveName, err)
	}
	*entryCount = *entryCount + 1
	return nil
}

func renderDocumentLines(title, content string, wrapWidth int) []string {
	var lines []string
	if trimmedTitle := strings.TrimSpace(title); trimmedTitle != "" {
		lines = append(lines, trimmedTitle, "")
	}
	for _, paragraph := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			lines = append(lines, "")
			continue
		}
		if wrapWidth <= 0 {
			lines = append(lines, trimmed)
			continue
		}
		lines = append(lines, wrapText(trimmed, wrapWidth)...)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func wrapText(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

func escapeXMLText(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
