package chat

import (
	"strings"
	"testing"
)

func TestShouldCompactHistory(t *testing.T) {
	if !shouldCompactHistory("What time is it in Tokyo right now?") {
		t.Fatal("expected standalone question to compact history")
	}
	if shouldCompactHistory("Can you update that automation instead?") {
		t.Fatal("expected referential follow-up to keep fuller history")
	}
}

func TestTokenizeTrimmedHistory(t *testing.T) {
	tokens := tokenizeTrimmedHistory("Please update the Orlando weather automation and Telegram delivery settings.")
	if len(tokens) == 0 {
		t.Fatal("expected topical tokens from trimmed history")
	}
}

func TestSelectiveMindContentCurrentHeadings(t *testing.T) {
	content := `# Mind of Atlas

## Who I Am
core

## Active Theories
theory

## Our Story
story

## Today's Read
today`
	got := selectiveMindContent(content, "What time is it in Tokyo?")
	if !strings.Contains(got, "## Who I Am") || !strings.Contains(got, "## Today's Read") {
		t.Fatalf("expected always sections in filtered MIND content: %q", got)
	}
	if strings.Contains(got, "## Active Theories") || strings.Contains(got, "## Our Story") {
		t.Fatalf("expected contextual sections to stay out for objective query: %q", got)
	}
}

func TestPromptInjectionGates(t *testing.T) {
	if shouldInjectMemories("What time is it in Tokyo right now?") {
		t.Fatal("should not inject memories for objective time query")
	}
	if !shouldInjectMemories("Update my Telegram automation to use a friendlier tone.") {
		t.Fatal("should inject memories for personalized automation query")
	}
	if shouldInjectDiary("What is the weather in Paris?") {
		t.Fatal("should not inject diary for weather query")
	}
	if !shouldInjectDiary("Can you help me plan today and recap my diary?") {
		t.Fatal("should inject diary for planning/diary query")
	}
}
