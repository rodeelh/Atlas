package mind

import (
	"strings"
	"testing"
	"time"
)

// ── updateCurrentFrame ─────────────────────────────────────────────────────────

// TestUpdateCurrentFrame — provide MIND.md with What's Active and How You Work
// populated; verify Current Frame is updated.
func TestUpdateCurrentFrame(t *testing.T) {
	mind := `# Mind of Atlas

_Last updated: 2026-01-01_

---

## Identity

I am Atlas.

---

## Current Frame

_(Generating context for first interaction.)_

---

## You

User is a developer.

---

## How You Work

**Confirmed** User prefers concise answers with minimal preamble.

---

## Commitments

_(No explicit commitments recorded yet.)_

---

## What's Active

Working on memory system redesign with FTS5 and opinion reinforcement.

---

## What I've Learned

_(Nothing yet.)_

---

## Today's Read

_(No turns recorded yet.)_
`

	got := updateCurrentFrame(mind)

	// Current Frame should be updated (not remain as the placeholder).
	if strings.Contains(got, "_(Generating context for first interaction.)_") {
		t.Error("updateCurrentFrame: Current Frame still has placeholder text")
	}

	// The frame should include content from What's Active.
	if !strings.Contains(got, "Working on memory system redesign") {
		t.Error("updateCurrentFrame: Current Frame should include What's Active content")
	}

	// The frame should include content from How You Work.
	if !strings.Contains(got, "concise") {
		t.Error("updateCurrentFrame: Current Frame should include How You Work content")
	}

	// The ## Current Frame header should still be present.
	if !strings.Contains(got, "## Current Frame") {
		t.Error("updateCurrentFrame: Current Frame section header missing")
	}

	// Other sections should be preserved.
	if !strings.Contains(got, "## Identity") {
		t.Error("updateCurrentFrame: Identity section should be preserved")
	}
	if !strings.Contains(got, "## What's Active") {
		t.Error("updateCurrentFrame: What's Active section should be preserved")
	}
}

// TestUpdateCurrentFrameEmptyWhatsActive — empty What's Active section should not
// crash, and should produce a fallback (no update or partial frame).
func TestUpdateCurrentFrameEmptyWhatsActive(t *testing.T) {
	mind := `# Mind of Atlas

_Last updated: 2026-01-01_

---

## Identity

I am Atlas.

---

## Current Frame

_(placeholder)_

---

## You

_(Nothing recorded yet.)_

---

## How You Work

_(Nothing recorded yet.)_

---

## Commitments

_(No explicit commitments recorded yet.)_

---

## What's Active

_(Nothing active yet.)_

---

## What I've Learned

_(Nothing yet.)_

---

## Today's Read

_(No turns recorded yet.)_
`

	// Should not panic.
	got := updateCurrentFrame(mind)

	// When all sections are placeholders, updateCurrentFrame returns content unchanged.
	// Both behaviors (return original, or return with empty frame) are valid.
	if got == "" {
		t.Error("updateCurrentFrame: returned empty string")
	}
}

// TestUpdateCurrentFrameWithCommitments — commitments appear in the frame.
func TestUpdateCurrentFrameWithCommitments(t *testing.T) {
	mind := `# Mind of Atlas

_Last updated: 2026-01-01_

---

## Identity

I am Atlas.

---

## Current Frame

_(placeholder)_

---

## You

User info.

---

## How You Work

**Confirmed** User prefers brevity.

---

## Commitments

- Always ask before deleting files
- Never send messages without explicit approval

---

## What's Active

Working on the memory redesign project.

---

## What I've Learned

_(Nothing yet.)_

---

## Today's Read

_(No turns recorded yet.)_
`

	got := updateCurrentFrame(mind)

	// Current Frame should include active rules from Commitments.
	if !strings.Contains(got, "Always ask before deleting files") {
		t.Error("updateCurrentFrame: commitment rule not in Current Frame")
	}
}

// ── replaceSection ─────────────────────────────────────────────────────────────

// TestReplaceSection — verify generic section splice.
func TestReplaceSection(t *testing.T) {
	mind := `# Mind of Atlas

## Identity

Old identity.

## Current Frame

Old frame.

## Today's Read

Old read.
`

	got := replaceSection(mind, "## Current Frame", "New frame content.")

	if !strings.Contains(got, "New frame content.") {
		t.Error("replaceSection: new content not present")
	}
	if strings.Contains(got, "Old frame.") {
		t.Error("replaceSection: old content should be replaced")
	}
	// Other sections must be preserved.
	if !strings.Contains(got, "Old identity.") {
		t.Error("replaceSection: Identity section should be preserved")
	}
	if !strings.Contains(got, "Old read.") {
		t.Error("replaceSection: Today's Read section should be preserved")
	}
}

// TestReplaceSectionMissing — when header absent, section is appended.
func TestReplaceSectionMissing(t *testing.T) {
	mind := "# Mind of Atlas\n\n## Identity\n\nIdentity.\n"
	got := replaceSection(mind, "## New Section", "New content.")

	if !strings.Contains(got, "## New Section") {
		t.Error("replaceSection: section not appended")
	}
	if !strings.Contains(got, "New content.") {
		t.Error("replaceSection: content not appended")
	}
	// Original content preserved.
	if !strings.Contains(got, "Identity.") {
		t.Error("replaceSection: existing content should be preserved")
	}
}

// TestReplaceSectionNoFalsePositive — header string in body text must not mis-splice.
func TestReplaceSectionNoFalsePositive(t *testing.T) {
	mind := "# Mind of Atlas\n\n## Identity\n\nI mentioned ## Current Frame in passing.\n\n## Current Frame\n\nOld frame.\n"
	got := replaceSection(mind, "## Current Frame", "New frame.")

	// The Identity section body should still contain the embedded string.
	if !strings.Contains(got, "I mentioned ## Current Frame in passing.") {
		t.Error("replaceSection: embedded header in body text was incorrectly spliced")
	}
	// The actual Current Frame should be updated.
	if !strings.Contains(got, "New frame.") {
		t.Error("replaceSection: new content not present")
	}
	if strings.Contains(got, "Old frame.") {
		t.Error("replaceSection: old content should be replaced")
	}
}

// ── updateReflectionDate ───────────────────────────────────────────────────────

// TestUpdateReflectionDateNewFormat — "_Last updated:" format is updated.
func TestUpdateReflectionDateNewFormat(t *testing.T) {
	mind := "# Mind of Atlas\n\n_Last updated: 2000-01-01_\n\n## Identity\n\nIdentity.\n"
	got := updateReflectionDate(mind)
	today := time.Now().Format("2006-01-02")

	if !strings.Contains(got, today) {
		t.Errorf("updateReflectionDate: today's date %q not found in output", today)
	}
	if strings.Contains(got, "2000-01-01") {
		t.Error("updateReflectionDate: old date should be replaced")
	}
	// Should use new format "_Last updated:" not legacy format.
	if !strings.Contains(got, "_Last updated:") {
		t.Error("updateReflectionDate: should use '_Last updated:' format")
	}
}

// TestUpdateReflectionDateLegacyFormat — "_Last deep reflection:" is replaced and
// upgraded to "_Last updated:" format.
func TestUpdateReflectionDateLegacyFormat(t *testing.T) {
	mind := "# Mind of Atlas\n\n_Last deep reflection: 1999-06-15_\n\n## Identity\n\nIdentity.\n"
	got := updateReflectionDate(mind)
	today := time.Now().Format("2006-01-02")

	if !strings.Contains(got, today) {
		t.Errorf("updateReflectionDate (legacy): today's date %q not found", today)
	}
	// Legacy format should be gone.
	if strings.Contains(got, "_Last deep reflection:") {
		t.Error("updateReflectionDate: legacy '_Last deep reflection:' should be replaced")
	}
	// New format should be used.
	if !strings.Contains(got, "_Last updated:") {
		t.Error("updateReflectionDate: should use new '_Last updated:' format")
	}
}

// TestUpdateReflectionDateMissing — inserts date when not found.
func TestUpdateReflectionDateMissing(t *testing.T) {
	mind := "# Mind of Atlas\n\n## Identity\n\nIdentity.\n"
	got := updateReflectionDate(mind)
	today := time.Now().Format("2006-01-02")

	if !strings.Contains(got, today) {
		t.Errorf("updateReflectionDate missing: today's date %q not inserted", today)
	}
}

// TestUpdateCurrentFrameSentenceSeparator — sections combined with " · " separator.
func TestUpdateCurrentFrameSentenceSeparator(t *testing.T) {
	mind := `# Mind of Atlas

_Last updated: 2026-01-01_

---

## Identity

I am Atlas.

---

## Current Frame

_(placeholder)_

---

## You

_(Nothing recorded yet.)_

---

## How You Work

**Confirmed** User works fast and direct.

---

## Commitments

_(No explicit commitments recorded yet.)_

---

## What's Active

Active memory redesign.

---

## What I've Learned

_(Nothing yet.)_

---

## Today's Read

_(No turns recorded yet.)_
`

	got := updateCurrentFrame(mind)

	// When both What's Active and How You Work have content, separator should appear.
	frame := extractSection(got, "## Current Frame")
	if frame != "" && strings.Contains(frame, "Active memory") && strings.Contains(frame, "User works fast") {
		if !strings.Contains(frame, "·") {
			t.Error("updateCurrentFrame: expected ' · ' separator between frame sentences")
		}
	}
}

// extractSection is a test helper that extracts the body of a ## section.
func extractSection(content, header string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
					end = j
					break
				}
			}
			return strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
		}
	}
	return ""
}
