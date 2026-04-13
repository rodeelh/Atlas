package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// agentsFileMu serialises all reads and writes to AGENTS.md so that
// concurrent skill calls (e.g. agent.create + agent.delete) never race.
var agentsFileMu sync.RWMutex

func agentsFilePath(supportDir string) string {
	return filepath.Join(supportDir, "AGENTS.md")
}


// readAgentsFileNoLock reads and parses AGENTS.md without acquiring the mutex.
// Callers must hold agentsFileMu (at least RLock) before calling this.
func readAgentsFileNoLock(path string) ([]agentDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseAgentsMarkdown(string(data))
}

func readAgentsFile(path string) ([]agentDefinition, error) {
	agentsFileMu.RLock()
	defer agentsFileMu.RUnlock()
	return readAgentsFileNoLock(path)
}

func writeAgentsFile(path string, defs []agentDefinition) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data := []byte(renderAgentsMarkdown(defs))
	// Write to a temp file in the same directory, then rename atomically.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agents-tmp-*")
	if err != nil {
		return fmt.Errorf("agents: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("agents: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("agents: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("agents: rename to target: %w", err)
	}
	return nil
}

func parseAgentsMarkdown(markdown string) ([]agentDefinition, error) {
	lines := strings.Split(markdown, "\n")
	inTeamMembers := false
	current := agentDefinition{}
	var defs []agentDefinition
	var fieldCount int

	flush := func() error {
		if strings.TrimSpace(current.Name) == "" && fieldCount == 0 {
			return nil
		}
		if err := validateDefinition(current); err != nil {
			return err
		}
		defs = append(defs, normalizeDefinition(current))
		current = agentDefinition{}
		fieldCount = 0
		return nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case strings.EqualFold(line, "## Team Members"):
			inTeamMembers = true
		case strings.HasPrefix(line, "## "):
			if inTeamMembers {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			inTeamMembers = false
		case inTeamMembers && strings.HasPrefix(line, "### "):
			if err := flush(); err != nil {
				return nil, err
			}
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "### "))
		case inTeamMembers && strings.HasPrefix(line, "- "):
			key, value, ok := splitField(strings.TrimPrefix(line, "- "))
			if !ok {
				continue
			}
			fieldCount++
			switch strings.ToLower(key) {
			case "id":
				current.ID = value
			case "role":
				current.Role = value
			case "mission":
				current.Mission = value
			case "style":
				current.Style = value
			case "allowed skills":
				current.AllowedSkills = splitList(value)
			case "allowed tool classes":
				current.AllowedToolClasses = splitList(value)
			case "autonomy":
				current.Autonomy = value
			case "activation":
				current.Activation = value
			case "provider", "provider type":
				current.ProviderType = value
			case "model":
				current.Model = value
			case "enabled":
				current.Enabled = parseEnabled(value)
			}
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}
	return defs, nil
}

func renderAgentsMarkdown(defs []agentDefinition) string {
	normalized := make([]agentDefinition, 0, len(defs))
	for _, def := range defs {
		normalized = append(normalized, normalizeDefinition(def))
	}
	sort.Slice(normalized, func(i, j int) bool {
		left := strings.ToLower(normalized[i].Name)
		right := strings.ToLower(normalized[j].Name)
		if left == right {
			return normalized[i].ID < normalized[j].ID
		}
		return left < right
	})

	var b strings.Builder
	b.WriteString("# Atlas Agents\n\n")
	b.WriteString("## Atlas\n")
	b.WriteString("(Atlas's own station — always present)\n\n")
	b.WriteString("## Team Members\n\n")
	for i, def := range normalized {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### ")
		b.WriteString(def.Name)
		b.WriteString("\n")
		writeField(&b, "ID", def.ID)
		writeField(&b, "Role", def.Role)
		writeField(&b, "Mission", def.Mission)
		writeField(&b, "Style", def.Style)
		writeField(&b, "Allowed Skills", strings.Join(def.AllowedSkills, ", "))
		writeField(&b, "Allowed Tool Classes", strings.Join(def.AllowedToolClasses, ", "))
		writeField(&b, "Autonomy", def.Autonomy)
		writeField(&b, "Activation", def.Activation)
		writeField(&b, "Provider", def.ProviderType)
		writeField(&b, "Model", def.Model)
		enabled := "no"
		if def.Enabled {
			enabled = "yes"
		}
		writeField(&b, "Enabled", enabled)
	}
	return b.String()
}

func writeField(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

func splitField(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func parseEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "1", "enabled":
		return true
	default:
		return false
	}
}

