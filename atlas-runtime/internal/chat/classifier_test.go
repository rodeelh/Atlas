package chat

import (
	"strings"
	"testing"

	"atlas-runtime-go/internal/mind/thoughts"
)

func TestParseClassifierEnvelope_Plain(t *testing.T) {
	raw := `{"signal":"positive","confidence":88,"reasoning":"user asked to hear more"}`
	r, err := parseClassifierEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Signal != "positive" || r.Confidence != 88 {
		t.Errorf("got %+v", r)
	}
}

func TestParseClassifierEnvelope_CodeFence(t *testing.T) {
	raw := "```json\n" + `{"signal":"ignored","confidence":60,"reasoning":"unrelated reply"}` + "\n```"
	r, err := parseClassifierEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Signal != "ignored" {
		t.Errorf("signal: got %q", r.Signal)
	}
}

func TestParseClassifierEnvelope_LeadingProse(t *testing.T) {
	raw := "Sure, here's my classification:\n\n" +
		`{"signal":"negative","confidence":92,"reasoning":"user said drop it"}` +
		"\nDone."
	r, err := parseClassifierEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Signal != "negative" {
		t.Errorf("signal: %q", r.Signal)
	}
}

func TestParseClassifierEnvelope_NoJSON(t *testing.T) {
	_, err := parseClassifierEnvelope("I couldn't classify this.")
	if err == nil {
		t.Error("expected error for no-json output")
	}
}

func TestNormalizeClassifierSignal(t *testing.T) {
	cases := map[string]thoughts.Signal{
		"positive":   thoughts.SignalPositive,
		"POSITIVE":   thoughts.SignalPositive,
		" positive ": thoughts.SignalPositive,
		"pos":        thoughts.SignalPositive,
		"negative":   thoughts.SignalNegative,
		"neg":        thoughts.SignalNegative,
		"ignored":    thoughts.SignalIgnored,
		"ignore":     thoughts.SignalIgnored,
		"garbage":    thoughts.Signal("garbage"), // rejected via IsTerminal
	}
	for in, want := range cases {
		got := normalizeClassifierSignal(in)
		if got != want {
			t.Errorf("normalize %q: got %q, want %q", in, got, want)
		}
	}
}

func TestBuildClassifierPrompt_Content(t *testing.T) {
	system, user := buildClassifierPrompt(
		"Rami keeps coming back to the openclaw release rhythm.",
		"yes tell me more please",
	)
	if !strings.Contains(system, "positive") || !strings.Contains(system, "negative") || !strings.Contains(system, "ignored") {
		t.Error("system prompt missing signal labels")
	}
	if !strings.Contains(system, "JSON") {
		t.Error("system prompt doesn't specify JSON")
	}
	if !strings.Contains(user, "openclaw release rhythm") {
		t.Error("user prompt missing thought body")
	}
	if !strings.Contains(user, "yes tell me more") {
		t.Error("user prompt missing user reply")
	}
}

func TestTruncateForClassifier(t *testing.T) {
	if got := truncateForClassifier("short", 100); got != "short" {
		t.Errorf("short: %q", got)
	}
	long := strings.Repeat("x", 600)
	got := truncateForClassifier(long, 500)
	if len(got) > 520 {
		t.Errorf("truncated len %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncation marker missing")
	}
}
