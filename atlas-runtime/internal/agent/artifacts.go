// Package agent — artifacts.go provides a short-lived, in-memory token store
// that maps random download tokens to local file paths.
//
// When the agent loop detects that a tool produced a file, it calls
// RegisterArtifact to obtain a token. The token is embedded in the
// file_generated SSE event and later resolved by GET /artifacts/{token}
// to serve the file to the web client.
//
// Tokens are 32-hex-character random strings (128 bits). The store holds
// at most 500 entries; when the limit is exceeded, the oldest batch is pruned
// on a best-effort basis (insertion order, not LRU). Files on the local
// filesystem are authoritative — the token is only a pointer.
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const artifactStoreMax = 500

type artifactEntry struct {
	path string
}

var artifactStore = struct {
	mu     sync.Mutex
	tokens map[string]artifactEntry
	order  []string // insertion order for bounded eviction
}{
	tokens: make(map[string]artifactEntry, 64),
}

// RegisterArtifact stores a local file path and returns a random download token.
// Returns "" if the path does not exist or token generation fails.
func RegisterArtifact(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := hex.EncodeToString(b)

	artifactStore.mu.Lock()
	defer artifactStore.mu.Unlock()

	// Prune oldest entries if we're at capacity.
	if len(artifactStore.tokens) >= artifactStoreMax {
		pruneCount := artifactStoreMax / 5
		for i := 0; i < pruneCount && i < len(artifactStore.order); i++ {
			delete(artifactStore.tokens, artifactStore.order[i])
		}
		artifactStore.order = artifactStore.order[pruneCount:]
	}

	artifactStore.tokens[token] = artifactEntry{path: path}
	artifactStore.order = append(artifactStore.order, token)
	return token
}

// ResolveArtifact returns the file path for a token, and whether it was found.
func ResolveArtifact(token string) (string, bool) {
	artifactStore.mu.Lock()
	e, ok := artifactStore.tokens[token]
	artifactStore.mu.Unlock()
	if !ok {
		return "", false
	}
	return e.path, true
}

// filePathRe matches absolute macOS/Linux file paths with common sendable
// extensions, appearing in free text (model responses, tool summaries).
// Path must start with a known root prefix and end with a recognised extension.
var filePathRe = regexp.MustCompile(
	`(?i)(/(?:Users|tmp|var|Library|private|home)[^\s"'<>()\[\]]+\.` +
		`(?:jpg|jpeg|png|gif|webp|pdf|txt|md|json|csv|xlsx|docx|pptx|` +
		`zip|tar|gz|mp3|mp4|wav|svg|html|xml|log|py|go|js|ts|sh))`,
)

// ExtractPathsFromText returns unique local file paths found in free text
// (model responses, tool summaries) that actually exist on disk.
// At most 10 paths are returned.
func ExtractPathsFromText(text string) []string {
	matches := filePathRe.FindAllString(text, 20)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if seen[m] {
			continue
		}
		seen[m] = true
		if _, err := os.Stat(m); err == nil {
			out = append(out, m)
		}
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// artifactFileRe matches absolute macOS/Linux file paths with common sendable
// extensions. Used to extract file paths from ToolResult.Artifacts values.
// Recognised extensions mirror what skills typically produce.
var artifactExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".pdf": true, ".txt": true, ".md": true, ".json": true, ".csv": true,
	".xlsx": true, ".docx": true, ".pptx": true, ".zip": true, ".tar": true,
	".gz": true, ".mp3": true, ".mp4": true, ".wav": true, ".svg": true,
	".html": true, ".xml": true, ".log": true, ".py": true, ".go": true,
	".js": true, ".ts": true, ".sh": true,
}

// ExtractArtifactPaths scans the string values in a ToolResult.Artifacts map
// and returns each value that looks like an absolute path to an existing file
// with a known extension. At most 10 paths are returned.
func ExtractArtifactPaths(artifacts map[string]any) []string {
	if len(artifacts) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range artifacts {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "/") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(s))
		if !artifactExtensions[ext] {
			continue
		}
		if seen[s] {
			continue
		}
		if _, err := os.Stat(s); err != nil {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// MimeTypeForPath returns a best-effort MIME type string for the given file path.
func MimeTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	// mime.TypeByExtension covers most standard types.
	if t := mime.TypeByExtension(ext); t != "" {
		return strings.Split(t, ";")[0] // strip charset suffix
	}
	// Fallbacks for types not in the stdlib list.
	switch ext {
	case ".md":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".log":
		return "text/plain"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}
