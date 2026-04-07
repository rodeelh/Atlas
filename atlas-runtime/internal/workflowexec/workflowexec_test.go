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
