package agents

import (
	"strings"
	"testing"

	"atlas-runtime-go/internal/storage"
)

// legacyDef returns a minimal AgentDefinitionRow without any V1 structured fields.
// Simulates agents created before Phase 1 schema migration.
func legacyDef(name, role, mission string, skills []string) storage.AgentDefinitionRow {
	skillsJSON := `["` + strings.Join(skills, `","`) + `"]`
	if len(skills) == 0 {
		skillsJSON = "[]"
	}
	return storage.AgentDefinitionRow{
		ID:                name,
		Name:              name,
		Role:              role,
		Mission:           mission,
		AllowedSkillsJSON: skillsJSON,
	}
}

// legacyTask returns a minimal AgentTaskRow with only the pre-V1 fields set.
func legacyTask(taskID, agentID, goal string) storage.AgentTaskRow {
	return storage.AgentTaskRow{
		TaskID:  taskID,
		AgentID: agentID,
		Goal:    goal,
	}
}

// ── Test 1: Legacy path produces all four sections ────────────────────────────

func TestComposeWorkerPrompt_LegacyPath_FourSections(t *testing.T) {
	def := legacyDef("scout-1", "Research Specialist", "Gather information on demand", []string{"web", "websearch"})
	task := legacyTask("task-1", "scout-1", "Research the current state of Go generics")

	prompt := composeWorkerPrompt(def, task)

	for _, section := range []string{"## Identity", "## Assignment", "## Execution contract"} {
		if !strings.Contains(prompt, section) {
			t.Errorf("prompt missing section %q\nprompt:\n%s", section, prompt)
		}
	}
	// Context section is omitted when no structured context is provided.
	if strings.Contains(prompt, "## Context") {
		t.Error("expected no ## Context section on legacy path with empty InputContextJSON")
	}
}

// ── Test 2: Identity section includes member fields ────────────────────────────

func TestComposeWorkerPrompt_IdentitySection(t *testing.T) {
	def := legacyDef("builder-1", "Engineer", "Build reliable software", []string{"fs", "terminal"})
	def.Style = "concise and pragmatic"
	task := legacyTask("task-2", "builder-1", "Implement feature X")

	prompt := composeWorkerPrompt(def, task)

	checks := []string{
		"builder-1",          // name
		"Mission:",           // mission header
		"Build reliable software", // mission text
		"Style: concise and pragmatic", // style
		"fs, terminal",       // allowed skills
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("Identity section missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

// ── Test 3: Template contracts inject role-specific behavior ───────────────────

func TestComposeWorkerPrompt_TemplateContract_Scout(t *testing.T) {
	def := legacyDef("scout-2", "Scout", "Gather context", []string{"websearch"})
	def.TemplateRole = "scout"
	task := legacyTask("task-3", "scout-2", "Find recent news on AI regulation")

	prompt := composeWorkerPrompt(def, task)

	wantLabel := "Scout"
	if !strings.Contains(prompt, wantLabel) {
		t.Errorf("expected scout label %q in prompt\nprompt:\n%s", wantLabel, prompt)
	}
	wantRule := "Prefer evidence over speculation"
	if !strings.Contains(prompt, wantRule) {
		t.Errorf("expected scout behavior rule %q\nprompt:\n%s", wantRule, prompt)
	}
}

func TestComposeWorkerPrompt_TemplateContract_Builder(t *testing.T) {
	def := legacyDef("builder-2", "Builder", "Produce artifacts", []string{"fs", "terminal"})
	def.TemplateRole = "builder"
	task := legacyTask("task-4", "builder-2", "Write the feature")

	prompt := composeWorkerPrompt(def, task)

	wantRule := "first-pass implementation"
	if !strings.Contains(prompt, wantRule) {
		t.Errorf("expected builder rule %q\nprompt:\n%s", wantRule, prompt)
	}
}

func TestComposeWorkerPrompt_TemplateContract_Reviewer(t *testing.T) {
	def := legacyDef("reviewer-1", "Reviewer", "Review code", []string{"fs"})
	def.TemplateRole = "reviewer"
	task := legacyTask("task-5", "reviewer-1", "Review PR changes")

	prompt := composeWorkerPrompt(def, task)

	wantRule := "regressions"
	if !strings.Contains(prompt, wantRule) {
		t.Errorf("expected reviewer rule %q\nprompt:\n%s", wantRule, prompt)
	}
}

// ── Test 4: Assignment section uses structured fields when present ─────────────

func TestComposeWorkerPrompt_AssignmentSection_StructuredFields(t *testing.T) {
	def := legacyDef("op-1", "Operator", "Run scripts", []string{"terminal"})
	def.TemplateRole = "operator"
	task := storage.AgentTaskRow{
		TaskID:              "task-6",
		AgentID:             "op-1",
		Goal:                "fallback goal",
		Title:               "Deploy staging environment",
		Objective:           "Run the deploy script and confirm all services are healthy.",
		ScopeJSON:           `{"included":["deploy script","health checks"],"excluded":["production"],"boundaries":["only staging"]}`,
		SuccessCriteriaJSON: `{"must":["all services healthy"],"should":["deploy time under 5m"],"failureConditions":["any service crash"]}`,
		ExpectedOutputJSON:  `{"type":"summary","formatNotes":["concise status report"]}`,
		InputContextJSON:    `{"atlasTaskFrame":"User triggered manual deploy","knownConstraints":["weekday only"]}`,
	}

	prompt := composeWorkerPrompt(def, task)

	checks := []string{
		"Deploy staging environment",      // title overrides goal
		"Run the deploy script",           // objective
		"in scope: deploy script",         // scope included
		"out of scope: production",        // scope excluded
		"boundary: only staging",          // scope boundary
		"must: all services healthy",      // success criteria must
		"should: deploy time under 5m",    // success criteria should
		"fail if: any service crash",      // failure condition
		"Expected output: summary",        // expected output type
		"concise status report",           // format notes
		"## Context",                      // context section present
		"User triggered manual deploy",    // atlas task frame
		"weekday only",                    // known constraint
	}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("Assignment/Context section missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

// ── Test 5: Legacy goal used when structured fields are absent ─────────────────

func TestComposeWorkerPrompt_FallsBackToGoal(t *testing.T) {
	def := legacyDef("scout-3", "Scout", "Research things", []string{"websearch"})
	task := legacyTask("task-7", "scout-3", "Research the history of the internet")

	prompt := composeWorkerPrompt(def, task)

	// Both title and objective should fall back to Goal when not set.
	if !strings.Contains(prompt, "Research the history of the internet") {
		t.Errorf("expected goal to appear in Assignment section\nprompt:\n%s", prompt)
	}
}

// ── Test 6: Execution contract contains non-inheritance rules ──────────────────

func TestComposeWorkerPrompt_ExecutionContractNonInheritance(t *testing.T) {
	def := legacyDef("mon-1", "Monitor", "Watch for anomalies", []string{"web"})
	def.TemplateRole = "monitor"
	task := legacyTask("task-8", "mon-1", "Monitor API error rate")

	prompt := composeWorkerPrompt(def, task)

	nonInheritanceRules := []string{
		"Work only within the scope",
		"Use only your allowed skills",
		"Do not broaden",
		"Clearly report any blockers",
		"not Atlas",
	}
	for _, rule := range nonInheritanceRules {
		if !strings.Contains(prompt, rule) {
			t.Errorf("Execution contract missing rule %q\nprompt:\n%s", rule, prompt)
		}
	}
}

// ── Test 7: Prior results and artifacts appear in Context section ──────────────

func TestComposeWorkerPrompt_ContextSection_PriorResultsAndArtifacts(t *testing.T) {
	def := legacyDef("builder-3", "Builder", "Implement features", []string{"fs", "terminal"})
	task := storage.AgentTaskRow{
		TaskID:           "task-9",
		AgentID:          "builder-3",
		Goal:             "implement",
		Title:            "Implement auth module",
		Objective:        "Write the auth middleware",
		InputContextJSON: `{"priorResults":["Scout found OAuth2 library at github.com/example/oauth"],"artifacts":["docs/auth-spec.md","src/middleware/"]}`,
	}

	prompt := composeWorkerPrompt(def, task)

	if !strings.Contains(prompt, "## Context") {
		t.Error("expected ## Context section")
	}
	if !strings.Contains(prompt, "Scout found OAuth2") {
		t.Error("expected prior results in context section")
	}
	if !strings.Contains(prompt, "docs/auth-spec.md") {
		t.Error("expected artifacts in context section")
	}
}

// ── Test 8: Unknown template role uses default contract ────────────────────────

func TestComposeWorkerPrompt_UnknownTemplateRole_UsesDefault(t *testing.T) {
	def := legacyDef("custom-1", "Custom Role", "Do custom things", []string{"web"})
	def.TemplateRole = "some-future-role"
	task := legacyTask("task-10", "custom-1", "Do the thing")

	prompt := composeWorkerPrompt(def, task)

	// Should use default contract rules (empty template contract = default key "")
	// and not crash. The Role field should appear in the Identity line.
	if !strings.Contains(prompt, "## Identity") {
		t.Error("expected ## Identity section")
	}
	// Unknown role: templateContracts["some-future-role"] will return zero value,
	// which uses the fallback label from def.Role.
	if !strings.Contains(prompt, "Custom Role") {
		t.Errorf("expected Role to appear in identity line\nprompt:\n%s", prompt)
	}
}
