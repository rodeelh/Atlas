package domain

import (
	"path/filepath"
	"strings"
	"testing"

	"atlas-runtime-go/internal/storage"
)

// openTestDB opens a fresh SQLite DB in a temp directory.
func openTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "domain_test.sqlite3"))
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	return db, func() { db.Close() }
}

// TestWriteRejectionMemory — call writeRejectionMemory, verify tool_learning row saved.
func TestWriteRejectionMemory(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	writeRejectionMemory(db, "browser.navigate", `{"url":"https://example.com"}`)

	mems, err := db.ListMemories(100, "tool_learning")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("writeRejectionMemory: expected at least 1 tool_learning memory, got 0")
	}

	// Find the rejection memory.
	found := false
	for _, m := range mems {
		if strings.Contains(m.Title, "browser.navigate") || strings.Contains(m.Title, "browser") {
			found = true
			if m.Category != "tool_learning" {
				t.Errorf("Category: want tool_learning, got %q", m.Category)
			}
			if m.Source != "approval_rejection" {
				t.Errorf("Source: want approval_rejection, got %q", m.Source)
			}
			// Confidence should be 0.60 per design.
			if m.Confidence != 0.60 {
				t.Errorf("Confidence: want 0.60, got %f", m.Confidence)
			}
			// Content should mention the tool name.
			if !strings.Contains(m.Content, "browser.navigate") {
				t.Errorf("Content should mention browser.navigate: %q", m.Content)
			}
			// Tags should include "browser" (skill base), "browser.navigate", and "rejection".
			if !strings.Contains(m.TagsJSON, "rejection") {
				t.Errorf("TagsJSON should include 'rejection': %q", m.TagsJSON)
			}
			if !strings.Contains(m.TagsJSON, "browser") {
				t.Errorf("TagsJSON should include skill base 'browser': %q", m.TagsJSON)
			}
		}
	}
	if !found {
		t.Error("writeRejectionMemory: could not find rejection memory by tool name")
	}
}

// TestTruncateApprovalArgs — strings at/above/below 150 chars.
func TestTruncateApprovalArgs(t *testing.T) {
	// Below limit: returned unchanged.
	short := "abc"
	if got := truncateApprovalArgs(short, 150); got != short {
		t.Errorf("short string: want %q, got %q", short, got)
	}

	// Exactly at limit: returned unchanged.
	exact := strings.Repeat("x", 150)
	if got := truncateApprovalArgs(exact, 150); got != exact {
		t.Errorf("exact-length string: want unchanged, got %q (len=%d)", got, len(got))
	}

	// Above limit: truncated with ellipsis.
	long := strings.Repeat("y", 151)
	got := truncateApprovalArgs(long, 150)
	// The result should be the first 150 bytes + "…"
	// "…" is a multi-byte UTF-8 character (3 bytes).
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated string should end with '…', got %q", got)
	}
	// The truncated portion should be exactly 150 bytes (before the ellipsis).
	truncatedPart := strings.TrimSuffix(got, "…")
	if len(truncatedPart) != 150 {
		t.Errorf("truncated part: want 150 bytes, got %d bytes", len(truncatedPart))
	}
}

// TestTruncateApprovalArgsEmpty — empty string returns empty string.
func TestTruncateApprovalArgsEmpty(t *testing.T) {
	if got := truncateApprovalArgs("", 150); got != "" {
		t.Errorf("empty string: want empty, got %q", got)
	}
}

// TestWriteRejectionMemoryToolNameParsing — "browser.navigate" → skillBase = "browser".
func TestWriteRejectionMemoryToolNameParsing(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	writeRejectionMemory(db, "browser.navigate", `{}`)

	mems, err := db.ListMemories(100, "tool_learning")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("expected at least 1 memory")
	}

	m := mems[0]
	// TagsJSON should contain "browser" (the skill base), "browser.navigate" (full name), "rejection".
	if !strings.Contains(m.TagsJSON, `"browser"`) {
		t.Errorf("skillBase 'browser' should be in tags: %q", m.TagsJSON)
	}
	if !strings.Contains(m.TagsJSON, `"browser.navigate"`) {
		t.Errorf("full tool name 'browser.navigate' should be in tags: %q", m.TagsJSON)
	}
	if !strings.Contains(m.TagsJSON, `"rejection"`) {
		t.Errorf("'rejection' should be in tags: %q", m.TagsJSON)
	}
}

// TestWriteRejectionMemoryEmptyToolName — empty toolName produces title "User rejected: ".
func TestWriteRejectionMemoryEmptyToolName(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	writeRejectionMemory(db, "", `{}`)

	mems, err := db.ListMemories(100, "tool_learning")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	// Should still save the memory without panicking.
	if len(mems) == 0 {
		t.Fatal("writeRejectionMemory with empty toolName: expected row saved, got 0")
	}

	m := mems[0]
	// Title is "User rejected: " (with trailing space from empty toolName).
	if !strings.HasPrefix(m.Title, "User rejected:") {
		t.Errorf("Title should start with 'User rejected:', got %q", m.Title)
	}
}

// TestWriteRejectionMemoryTitleTruncation — title is truncated at 48 chars.
func TestWriteRejectionMemoryTitleTruncation(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	// A very long tool name.
	longToolName := strings.Repeat("x", 100)
	writeRejectionMemory(db, longToolName, `{}`)

	mems, err := db.ListMemories(100, "tool_learning")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("expected memory saved")
	}
	if len(mems[0].Title) > 48 {
		t.Errorf("Title should be <= 48 chars, got %d: %q", len(mems[0].Title), mems[0].Title)
	}
}

// TestWriteRejectionMemoryArgsInContent — argsJSON appears in content (truncated).
func TestWriteRejectionMemoryArgsInContent(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	writeRejectionMemory(db, "fs.delete", `{"path": "/important/file.txt"}`)

	mems, err := db.ListMemories(100, "tool_learning")
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("expected memory saved")
	}
	// Content should include args (truncated to 150 chars).
	if !strings.Contains(mems[0].Content, "/important/file.txt") {
		t.Errorf("Content should include args: %q", mems[0].Content)
	}
}
