package mind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDefaultMindContent — verify all 8 section headers present.
func TestDefaultMindContent(t *testing.T) {
	content := defaultMindContent()

	requiredSections := []string{
		"## Identity",
		"## Current Frame",
		"## You",
		"## How You Work",
		"## Commitments",
		"## What's Active",
		"## What I've Learned",
		"## Today's Read",
	}

	for _, section := range requiredSections {
		if !strings.Contains(content, section) {
			t.Errorf("defaultMindContent: missing section %q", section)
		}
	}

	// Must start with the correct title.
	if !strings.HasPrefix(strings.TrimSpace(content), "# Mind of Atlas") {
		t.Error("defaultMindContent: must start with '# Mind of Atlas'")
	}

	// Must contain today's date.
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(content, today) {
		t.Errorf("defaultMindContent: expected today's date %q in content", today)
	}

	// Must have _Last updated: date_ metadata.
	if !strings.Contains(content, "_Last updated:") {
		t.Error("defaultMindContent: missing '_Last updated:' metadata line")
	}
}

// TestDefaultSkillsContent — verify required sections present including
// "## Synthesized Tool Notes" (not "## Things That Don't Work").
func TestDefaultSkillsContent(t *testing.T) {
	content := defaultSkillsContent()

	// Must start with correct title.
	if !strings.HasPrefix(strings.TrimSpace(content), "# Skill Memory") {
		t.Error("defaultSkillsContent: must start with '# Skill Memory'")
	}

	// Required sections.
	requiredSections := []string{
		"## Orchestration Principles",
		"## Learned Routines",
		"## Synthesized Tool Notes",
	}
	for _, section := range requiredSections {
		if !strings.Contains(content, section) {
			t.Errorf("defaultSkillsContent: missing section %q", section)
		}
	}

	// Must NOT contain the old section name.
	if strings.Contains(content, "## Things That Don't Work") {
		t.Error("defaultSkillsContent: should not contain old section '## Things That Don't Work'")
	}

	// Must contain today's date.
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(content, today) {
		t.Errorf("defaultSkillsContent: expected today's date %q in content", today)
	}
}

// TestDefaultMindContentExactSectionCount — exactly 8 sections.
func TestDefaultMindContentExactSectionCount(t *testing.T) {
	content := defaultMindContent()
	sections := parseSections(content)
	if len(sections) != 8 {
		t.Errorf("defaultMindContent: expected 8 sections, got %d: %v",
			len(sections), func() []string {
				var keys []string
				for k := range sections {
					keys = append(keys, k)
				}
				return keys
			}())
	}
}

// TestDefaultSkillsContentSectionCount — exactly 3 sections.
func TestDefaultSkillsContentSectionCount(t *testing.T) {
	content := defaultSkillsContent()
	sections := parseSections(content)
	if len(sections) != 3 {
		t.Errorf("defaultSkillsContent: expected 3 sections, got %d: %v",
			len(sections), func() []string {
				var keys []string
				for k := range sections {
					keys = append(keys, k)
				}
				return keys
			}())
	}
}

// TestInitMindIfNeeded — creates MIND.md on first call, no-ops on second.
func TestInitMindIfNeeded(t *testing.T) {
	dir := t.TempDir()
	mindPath := filepath.Join(dir, "MIND.md")

	// File should not exist yet.
	if _, err := os.Stat(mindPath); !os.IsNotExist(err) {
		t.Fatal("MIND.md should not exist before InitMindIfNeeded")
	}

	// First call: should create file.
	if err := InitMindIfNeeded(dir); err != nil {
		t.Fatalf("InitMindIfNeeded (first): %v", err)
	}
	data, err := os.ReadFile(mindPath)
	if err != nil {
		t.Fatalf("ReadFile after init: %v", err)
	}
	if len(data) == 0 {
		t.Error("InitMindIfNeeded: created empty file")
	}

	// Write a sentinel value to the file.
	sentinel := "# Mind of Atlas\n\nSentinel content.\n"
	if err := os.WriteFile(mindPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}

	// Second call: should be a no-op (file already exists).
	if err := InitMindIfNeeded(dir); err != nil {
		t.Fatalf("InitMindIfNeeded (second): %v", err)
	}
	data2, err := os.ReadFile(mindPath)
	if err != nil {
		t.Fatalf("ReadFile after second init: %v", err)
	}
	if string(data2) != sentinel {
		t.Error("InitMindIfNeeded: second call overwrote existing file")
	}
}

// TestInitSkillsIfNeeded — creates SKILLS.md on first call, no-ops on second.
func TestInitSkillsIfNeeded(t *testing.T) {
	dir := t.TempDir()
	skillsPath := filepath.Join(dir, "SKILLS.md")

	if err := InitSkillsIfNeeded(dir); err != nil {
		t.Fatalf("InitSkillsIfNeeded (first): %v", err)
	}
	data, err := os.ReadFile(skillsPath)
	if err != nil {
		t.Fatalf("ReadFile after init: %v", err)
	}
	if !strings.Contains(string(data), "## Synthesized Tool Notes") {
		t.Error("InitSkillsIfNeeded: created file without '## Synthesized Tool Notes'")
	}

	// Second call should be no-op.
	sentinel := "# Skill Memory\n\nSentinel.\n"
	os.WriteFile(skillsPath, []byte(sentinel), 0o600) //nolint:errcheck
	if err := InitSkillsIfNeeded(dir); err != nil {
		t.Fatalf("InitSkillsIfNeeded (second): %v", err)
	}
	data2, err := os.ReadFile(skillsPath)
	if err != nil {
		t.Fatalf("ReadFile after second init: %v", err)
	}
	if string(data2) != sentinel {
		t.Error("InitSkillsIfNeeded: second call overwrote existing file")
	}
}

// TestReplaceSKILLSSection — generic splice of a SKILLS.md section.
func TestReplaceSKILLSSection(t *testing.T) {
	content := `# Skill Memory

_Last updated: 2026-01-01_

---

## Orchestration Principles

Always complete requests.

---

## Learned Routines

_(None yet.)_

---

## Synthesized Tool Notes

_(None yet.)_
`

	got := replaceSKILLSSection(content, "## Synthesized Tool Notes", "- [weather.current] Use IATA codes, not ICAO (confidence: high)")

	if !strings.Contains(got, "IATA codes") {
		t.Error("replaceSKILLSSection: new content not present")
	}
	if strings.Contains(got, "_(None yet.)_") && strings.Contains(got, "## Synthesized Tool Notes") {
		// Both might appear if there was a bug — verify the old placeholder was replaced.
		// Find the section body.
		body := extractSection(got, "## Synthesized Tool Notes")
		if strings.Contains(body, "_(None yet.)_") {
			t.Error("replaceSKILLSSection: old placeholder not replaced")
		}
	}

	// Orchestration Principles must be preserved.
	if !strings.Contains(got, "Always complete requests.") {
		t.Error("replaceSKILLSSection: Orchestration Principles should be preserved")
	}
}

// TestReplaceSKILLSSectionMissing — appends section if not found.
func TestReplaceSKILLSSectionMissing(t *testing.T) {
	content := "# Skill Memory\n\n## Orchestration Principles\n\nRules.\n"
	got := replaceSKILLSSection(content, "## New Section", "New notes here.")

	if !strings.Contains(got, "## New Section") {
		t.Error("replaceSKILLSSection: section not appended")
	}
	if !strings.Contains(got, "New notes here.") {
		t.Error("replaceSKILLSSection: content not appended")
	}
}
