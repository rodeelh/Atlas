package workflowexec

import (
	"strings"
	"testing"
)

func TestComposePromptFallsBackAndAppendsInputsAndInstruction(t *testing.T) {
	out := ComposePrompt(map[string]any{"name": "Review"}, map[string]string{"city": "Orlando"}, "Summarize results")
	if !strings.Contains(out, "Execute workflow: Review") ||
		!strings.Contains(out, `"city":"Orlando"`) ||
		!strings.Contains(out, "Automation instruction:\nSummarize results") {
		t.Fatalf("unexpected composed prompt: %q", out)
	}
}

func TestInitialStepRunsIncludesTypedWorkflowSteps(t *testing.T) {
	runs := InitialStepRuns(map[string]any{
		"steps": []map[string]any{
			{"id": "draft", "type": "llm.generate", "title": "Draft"},
			{"id": "save", "type": "atlas.tool", "title": "Save"},
			{"id": "done", "type": "return", "title": "Done"},
		},
	})
	if len(runs) != 3 {
		t.Fatalf("expected 3 step runs, got %+v", runs)
	}
	for _, run := range runs {
		if run["status"] != "pending" {
			t.Fatalf("expected pending status for typed step, got %+v", runs)
		}
	}
}
