package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/storage"
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

func TestShouldComputeHyDEUsesMemoryInjectionGate(t *testing.T) {
	cfg := storageTestDefaults()
	cfg.MaxRetrievedMemoriesPerTurn = 4

	if shouldComputeHyDE(cfg, "What time is it in Tokyo right now?") {
		t.Fatal("should not compute HyDE for objective turns that do not inject memories")
	}
	if !shouldComputeHyDE(cfg, "What do you remember about my Telegram automation?") {
		t.Fatal("should compute HyDE when recalled memories may be injected")
	}

	cfg.MaxRetrievedMemoriesPerTurn = 0
	if shouldComputeHyDE(cfg, "What do you remember about my Telegram automation?") {
		t.Fatal("should not compute HyDE when memory retrieval is disabled by limit")
	}
}

func TestDetectTurnMode(t *testing.T) {
	cases := []struct {
		message string
		want    turnMode
	}{
		{"Hi Atlas, how are you?", turnModeChat},
		{"What time is it in Tokyo right now?", turnModeFactual},
		{"Research the current OpenAI CEO and verify from the official website.", turnModeResearch},
		{"Update this file to use the new endpoint.", turnModeExecution},
		{"Send me a daily Orlando weather forecast on Telegram at 8 AM.", turnModeAutomation},
	}
	for _, tc := range cases {
		if got := detectTurnMode(tc.message); got != tc.want {
			t.Fatalf("detectTurnMode(%q) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

func TestBuildSystemPromptAddsResponseContract(t *testing.T) {
	cfg := storageTestDefaults()
	prompt := buildSystemPrompt(cfg, nil, t.TempDir(), "Verify the current OpenAI CEO from the official website.", "", nil)
	if !strings.Contains(prompt, "<response_contract>") {
		t.Fatal("expected response contract block in prompt")
	}
	if !strings.Contains(prompt, "Mode: research") {
		t.Fatalf("expected research response contract, got: %q", prompt)
	}
}

func TestBuildSystemPromptKeepsBasePromptAndEditableMindLayer(t *testing.T) {
	cfg := storageTestDefaults()
	supportDir := t.TempDir()
	mind := `# Mind of Atlas

## Who I Am
Adaptive operator layer`
	if err := os.WriteFile(filepath.Join(supportDir, "MIND.md"), []byte(mind), 0o600); err != nil {
		t.Fatalf("write MIND.md: %v", err)
	}

	prompt := buildSystemPrompt(cfg, nil, supportDir, "Help me adjust how you behave.", "", nil)
	if !strings.Contains(prompt, "Base prompt for Atlas") {
		t.Fatalf("expected base prompt to remain in system prompt, got: %q", prompt)
	}
	if !strings.Contains(prompt, "<editable_operator_prompt>") || !strings.Contains(prompt, "Adaptive operator layer") {
		t.Fatalf("expected editable operator layer in system prompt, got: %q", prompt)
	}
}

func TestBuildSystemPromptAddsUnleashedMissionPrompt(t *testing.T) {
	cfg := storageTestDefaults()
	cfg.AutonomyMode = config.AutonomyModeUnleashed

	prompt := buildSystemPrompt(cfg, nil, t.TempDir(), "Figure out how to connect Atlas to iMessage and make it work.", "", nil)
	if !strings.Contains(prompt, "You are in Atlas unleashed mode") {
		t.Fatalf("expected unleashed mission prompt, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Default to execution, adaptation, and persistence") {
		t.Fatalf("expected unleashed execution doctrine, got: %q", prompt)
	}
	if !strings.Contains(prompt, "treat capability-building as part of the job") {
		t.Fatalf("expected unleashed capability doctrine, got: %q", prompt)
	}
	if !strings.Contains(prompt, "system.app_capabilities") || !strings.Contains(prompt, "terminal.check_command") || !strings.Contains(prompt, "fs.workspace_roots") {
		t.Fatalf("expected unleashed prompt to mention operator introspection tools, got: %q", prompt)
	}
}

func TestAutomationResponseContractDistinguishesAutomationFromAgent(t *testing.T) {
	cfg := storageTestDefaults()
	db, err := storage.Open(filepath.Join(t.TempDir(), "prompt-test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	prompt := buildSystemPrompt(cfg, db, t.TempDir(), "Create an agent that checks my email every 3 hours and sends useful items to Telegram.", "", nil)
	if !strings.Contains(prompt, "Mode: automation") {
		t.Fatalf("expected automation response contract, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Use exact outcome language") {
		t.Fatalf("expected automation contract to require exact outcome language, got: %q", prompt)
	}
	if !strings.Contains(prompt, "only say agent/team member when you actually used agent.create") {
		t.Fatalf("expected automation contract to distinguish automation from AGENTS team members, got: %q", prompt)
	}
	if !strings.Contains(prompt, "Never fulfill an agent request as an automation") {
		t.Fatalf("expected automation contract to prohibit fulfilling agent requests as automations, got: %q", prompt)
	}
}

func TestExecutionResponseContractDistinguishesWorkflowAutomationAndAgent(t *testing.T) {
	cfg := storageTestDefaults()
	db, err := storage.Open(filepath.Join(t.TempDir(), "prompt-test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	prompt := buildSystemPrompt(cfg, db, t.TempDir(), "Create an agent for triaging requests using the existing workflow tools.", "", nil)
	if !strings.Contains(prompt, "Mode: execution") {
		t.Fatalf("expected execution response contract, got: %q", prompt)
	}
	if !strings.Contains(prompt, "call workflows workflows, automations automations, and AGENTS team members agents") {
		t.Fatalf("expected execution contract to distinguish control surfaces, got: %q", prompt)
	}
}

func TestBuildTrimmedHistoryNoteIncludesProgress(t *testing.T) {
	svc := &Service{summaryCache: make(map[string]conversationSummary)}
	trimmed := []storage.MessageRow{
		{ID: "u1", Role: "user", Content: "Please create a daily Orlando forecast automation for Telegram."},
		{ID: "a1", Role: "assistant", Content: "I created the automation and set delivery to Telegram at 8 AM."},
		{ID: "u2", Role: "user", Content: "Now make the tone friendlier."},
	}
	note := svc.buildTrimmedHistoryNote("conv-test", len(trimmed), trimmed, "current")
	if !strings.Contains(note, "Recent asks:") {
		t.Fatalf("expected recent asks in note: %q", note)
	}
	if !strings.Contains(note, "Latest progress:") {
		t.Fatalf("expected latest progress in note: %q", note)
	}
}

func storageTestDefaults() config.RuntimeConfigSnapshot {
	cfg := config.Defaults()
	cfg.PersonaName = "Atlas"
	cfg.UserName = "Rami"
	cfg.BaseSystemPrompt = fmt.Sprintf("Base prompt for %s", cfg.PersonaName)
	return cfg
}
