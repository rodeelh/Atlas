package chat

import (
	"strings"
	"testing"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/mind"
)

func TestBuildGreetingPrompt_SingleEntry(t *testing.T) {
	queue := []mind.GreetingEntry{
		{
			ThoughtID:  "T-01",
			Body:       "Pull the latest openclaw release notes",
			SkillID:    "openclaw.check",
			Result:     `{"success":true,"summary":"1.2.3 released"}`,
			Provenance: "recurring interest in openclaw",
		},
	}
	system, user, name := buildGreetingPrompt(queue, config.Defaults())

	if !strings.Contains(system, "warm and personal") && !strings.Contains(system, "Warm and personal") {
		t.Error("system prompt missing warmth instruction")
	}
	if !strings.Contains(system, "gift") {
		t.Error("system prompt missing gift framing")
	}
	if !strings.Contains(system, "150 words") {
		t.Error("system prompt missing length cap")
	}

	if !strings.Contains(user, "openclaw.check") {
		t.Error("user prompt missing skill id")
	}
	if !strings.Contains(user, "1.2.3 released") {
		t.Error("user prompt missing result text")
	}
	if !strings.Contains(user, "recurring interest") {
		t.Error("user prompt missing provenance")
	}
	if name == "" {
		t.Error("userName should not be empty")
	}
}

func TestBuildGreetingPrompt_MultipleEntriesAreNumbered(t *testing.T) {
	queue := []mind.GreetingEntry{
		{ThoughtID: "T-01", Body: "a", SkillID: "s.a", Result: "result a"},
		{ThoughtID: "T-02", Body: "b", SkillID: "s.b", Result: "result b"},
		{ThoughtID: "T-03", Body: "c", SkillID: "s.c", Result: "result c"},
	}
	_, user, _ := buildGreetingPrompt(queue, config.Defaults())
	for i := 1; i <= 3; i++ {
		marker := "Thought " + string(rune('0'+i))
		if !strings.Contains(user, marker) {
			t.Errorf("user prompt missing %q", marker)
		}
	}
}

func TestBuildGreetingPrompt_TruncatesLongResults(t *testing.T) {
	longResult := strings.Repeat("x", 5000)
	queue := []mind.GreetingEntry{
		{ThoughtID: "T-01", Body: "b", SkillID: "s", Result: longResult},
	}
	_, user, _ := buildGreetingPrompt(queue, config.Defaults())
	if strings.Contains(user, strings.Repeat("x", 5000)) {
		t.Error("user prompt did not truncate long result")
	}
	if !strings.Contains(user, "truncated") {
		t.Error("user prompt should indicate truncation")
	}
}

func TestTruncateForGreeting(t *testing.T) {
	if got := truncateForGreeting("short", 100); got != "short" {
		t.Errorf("short string should pass through, got %q", got)
	}
	long := strings.Repeat("a", 500)
	got := truncateForGreeting(long, 100)
	if len(got) > 130 {
		t.Errorf("truncated len %d, want ≤130", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncated output should mention truncation, got %q", got)
	}
}

func TestChatGreetingQueuer_Enqueue(t *testing.T) {
	dir := t.TempDir()
	q := NewChatGreetingQueuer(dir)

	entry := mind.GreetingEntry{
		ThoughtID: "T-01",
		Body:      "queued body",
		SkillID:   "s.one",
		Result:    "queued result",
	}
	if err := q.EnqueueGreeting(entry); err != nil {
		t.Fatalf("EnqueueGreeting: %v", err)
	}

	queue, err := mind.LoadPendingGreetings(dir)
	if err != nil {
		t.Fatalf("LoadPendingGreetings: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("got %d entries, want 1", len(queue))
	}
	if queue[0].ThoughtID != "T-01" {
		t.Errorf("thought id: got %q, want T-01", queue[0].ThoughtID)
	}
}

func TestGreetingTelemetryNoop(t *testing.T) {
	// Just verify the noop doesn't panic.
	noop := greetingTelemetryNoop{}
	noop.Emit("test", "T-01", "conv", nil)
}
