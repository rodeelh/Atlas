package mind

import (
	"strings"
	"testing"
)

func TestBaseSkillsContentCompact(t *testing.T) {
	content := `# Skill Memory

## Orchestration Principles
Always do the right thing.

## Learned Routines
Routine body

## Things That Don't Work
Noisy section

## Synthesized Tool Notes
Duplicated notes`

	got := baseSkillsContent(content)
	if !strings.Contains(got, "## Orchestration Principles") {
		t.Fatalf("expected orchestration principles to remain, got %q", got)
	}
	if strings.Contains(got, "## Learned Routines") || strings.Contains(got, "## Things That Don't Work") || strings.Contains(got, "## Synthesized Tool Notes") {
		t.Fatalf("expected non-core sections to be omitted, got %q", got)
	}
}
