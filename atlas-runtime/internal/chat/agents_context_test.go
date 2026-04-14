package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setTestRosterReader wires a simple file-reading roster builder into RosterReader
// for the duration of the test. It parses AGENTS.md using minimal logic that
// mirrors what the agents module's rosterContext does — used only in unit tests
// where the full agents module cannot be registered without an import cycle.
func setTestRosterReader(t *testing.T) {
	t.Helper()
	prev := RosterReader
	RosterReader = func(supportDir string) string {
		data, err := os.ReadFile(filepath.Join(supportDir, "AGENTS.md"))
		if err != nil {
			return ""
		}
		type entry struct {
			id, name, role, activation, autonomy string
			enabled                              bool
		}
		var entries []entry
		var cur entry
		inTeamMembers := false
		hasCur := false
		curEnabled := true

		flush := func() {
			if hasCur && cur.id != "" && cur.name != "" && curEnabled {
				cur.enabled = true
				entries = append(entries, cur)
			}
			cur = entry{}
			hasCur = false
			curEnabled = true
		}

		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			switch {
			case line == "":
				continue
			case strings.EqualFold(line, "## Team Members"):
				inTeamMembers = true
			case strings.HasPrefix(line, "## "):
				if inTeamMembers {
					flush()
				}
				inTeamMembers = false
			case inTeamMembers && strings.HasPrefix(line, "### "):
				flush()
				cur.name = strings.TrimSpace(strings.TrimPrefix(line, "### "))
				hasCur = true
			case inTeamMembers && strings.HasPrefix(line, "- "):
				parts := strings.SplitN(strings.TrimPrefix(line, "- "), ":", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.ToLower(strings.TrimSpace(parts[0]))
				val := strings.TrimSpace(parts[1])
				switch key {
				case "id":
					cur.id = val
				case "role":
					cur.role = val
				case "activation":
					cur.activation = val
				case "autonomy":
					cur.autonomy = val
				case "enabled":
					switch strings.ToLower(val) {
					case "yes", "true", "1", "enabled":
						curEnabled = true
					default:
						curEnabled = false
					}
				}
			}
		}
		flush()

		if len(entries) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString("You have a team of agents. Always use the exact agent ID shown below.\n")
		sb.WriteString("Key operations: agent.list (list), agent.get (inspect), agent.create (create), agent.update (update), agent.delete (delete), agent.delegate (run a task).\n")
		sb.WriteString("To delete an agent: call agent.delete with its exact id. To delete all: call agent.list first, then agent.delete for each id.\n\n")
		for _, e := range entries {
			line := "- id:" + e.id + " | " + e.name + " | " + e.role
			if e.activation != "" {
				line += " | activate: " + e.activation
			}
			line += " | autonomy: " + e.autonomy
			sb.WriteString(line + "\n")
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	t.Cleanup(func() { RosterReader = prev })
}

// TestAgentRosterContext_PromptInjection verifies that agentRosterContext:
// - returns a non-empty block when AGENTS.md has enabled agents
// - skips disabled agents
// - returns "" when the file is missing or all agents are disabled
func TestAgentRosterContext_PromptInjection(t *testing.T) {
	setTestRosterReader(t)

	agentsContent := `# Atlas Team

## Atlas
(Atlas's own station — always present)

## Team Members

### Scout
- ID: scout
- Role: Research Specialist
- Mission: Gather facts
- Allowed Skills: web.search
- Autonomy: assistive
- Activation: when the user needs research
- Enabled: yes

### Phantom
- ID: phantom
- Role: Ghost Operator
- Mission: Do nothing visible
- Allowed Skills: web.*
- Autonomy: autonomous
- Enabled: no
`

	t.Run("enabled agents appear, disabled excluded", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		block := agentRosterContext(dir)
		if block == "" {
			t.Fatal("expected non-empty roster block")
		}
		if !strings.Contains(block, "id:scout") {
			t.Errorf("expected scout in roster, got:\n%s", block)
		}
		if strings.Contains(block, "phantom") {
			t.Errorf("disabled agent phantom should be excluded, got:\n%s", block)
		}
		if !strings.Contains(block, "Research Specialist") {
			t.Errorf("expected role in roster, got:\n%s", block)
		}
		if !strings.Contains(block, "activate: when the user needs research") {
			t.Errorf("expected activation hint in roster, got:\n%s", block)
		}
		if !strings.Contains(block, "autonomy: assistive") {
			t.Errorf("expected autonomy in roster, got:\n%s", block)
		}
		// Should include the delegation instruction
		if !strings.Contains(block, "agent.delegate") {
			t.Errorf("expected agent.delegate instruction in roster, got:\n%s", block)
		}
	})

	t.Run("missing file returns empty string", func(t *testing.T) {
		dir := t.TempDir()
		// No AGENTS.md written
		block := agentRosterContext(dir)
		if block != "" {
			t.Fatalf("expected empty string for missing file, got: %q", block)
		}
	})

	t.Run("all agents disabled returns empty string", func(t *testing.T) {
		allDisabled := `# Atlas Team

## Team Members

### Ghost
- ID: ghost
- Role: Ghost
- Mission: Nothing
- Allowed Skills: web.*
- Autonomy: assistive
- Enabled: no
`
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(allDisabled), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		block := agentRosterContext(dir)
		if block != "" {
			t.Fatalf("expected empty string when all agents disabled, got: %q", block)
		}
	})

	t.Run("multiple enabled agents all appear", func(t *testing.T) {
		multi := `# Atlas Team

## Team Members

### Alpha
- ID: alpha
- Role: Alpha Specialist
- Mission: Do alpha things
- Allowed Skills: web.*
- Autonomy: assistive
- Enabled: yes

### Beta
- ID: beta
- Role: Beta Specialist
- Mission: Do beta things
- Allowed Skills: weather.*
- Autonomy: supervised
- Enabled: yes
`
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(multi), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		block := agentRosterContext(dir)
		if !strings.Contains(block, "id:alpha") || !strings.Contains(block, "id:beta") {
			t.Errorf("expected both agents in roster, got:\n%s", block)
		}
	})
}

// TestRosterReader_EdgeCases verifies the roster context builder handles edge
// cases correctly. These tests replace the former TestParseRosterMarkdown_EdgeCases
// which tested a now-deleted second parser — the canonical parser lives in the
// agents module (internal/modules/agents/agents_file.go).
func TestRosterReader_EdgeCases(t *testing.T) {
	setTestRosterReader(t)

	t.Run("empty markdown returns empty string", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(""), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		block := agentRosterContext(dir)
		if block != "" {
			t.Fatalf("expected empty string for empty file, got: %q", block)
		}
	})

	t.Run("agent without ID excluded", func(t *testing.T) {
		md := `## Team Members

### NoID
- Role: Something
- Mission: Something
- Autonomy: assistive
- Enabled: yes
`
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(md), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		block := agentRosterContext(dir)
		if block != "" {
			t.Fatalf("agent with no ID should be excluded, got: %q", block)
		}
	})

	t.Run("activation field is optional", func(t *testing.T) {
		md := `## Team Members

### Minimal
- ID: minimal
- Role: Minimalist
- Autonomy: assistive
- Enabled: yes
`
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(md), 0o644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}
		block := agentRosterContext(dir)
		if !strings.Contains(block, "id:minimal") {
			t.Fatalf("expected minimal agent in roster, got: %q", block)
		}
		if strings.Contains(block, "activate:") {
			t.Fatalf("activation should be absent when not set, got: %q", block)
		}
	})
}
