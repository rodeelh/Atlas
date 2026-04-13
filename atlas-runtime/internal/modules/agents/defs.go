package agents

import (
	"fmt"
	"strings"
)

// agentDefinition is the in-memory representation of an agent used by the
// merge and validation pipeline. All DB CRUD handlers convert to/from this
// struct via rowToFileDef / fileDefToRow in module.go.
type agentDefinition struct {
	Name               string   `json:"name"`
	ID                 string   `json:"id"`
	Role               string   `json:"role"`
	Mission            string   `json:"mission"`
	Style              string   `json:"style,omitempty"`
	AllowedSkills      []string `json:"allowedSkills"`
	AllowedToolClasses []string `json:"allowedToolClasses,omitempty"`
	Autonomy           string   `json:"autonomy"`
	Activation         string   `json:"activation,omitempty"`
	ProviderType       string   `json:"providerType,omitempty"`
	Model              string   `json:"model,omitempty"`
	Enabled            bool     `json:"enabled"`
}

func normalizeDefinition(def agentDefinition) agentDefinition {
	def.Name = strings.TrimSpace(def.Name)
	def.ID = strings.TrimSpace(def.ID)
	def.Role = strings.TrimSpace(def.Role)
	def.Mission = strings.TrimSpace(def.Mission)
	def.Style = strings.TrimSpace(def.Style)
	def.Autonomy = strings.TrimSpace(def.Autonomy)
	def.Activation = strings.TrimSpace(def.Activation)
	def.ProviderType = strings.TrimSpace(def.ProviderType)
	def.Model = strings.TrimSpace(def.Model)
	def.AllowedSkills = normalizeSkillPatterns(splitList(strings.Join(def.AllowedSkills, ",")))
	def.AllowedToolClasses = splitList(strings.Join(def.AllowedToolClasses, ","))
	return def
}

// normalizeSkillPattern converts any accepted pattern form to the canonical
// bare-prefix form. The canonical form is a plain namespace name with no suffix:
//
//	"fs.*"  → "fs"   (wildcard stripped)
//	"fs."   → "fs"   (trailing dot stripped)
//	"fs"    → "fs"   (already canonical)
//	"fs.read_file" → "fs.read_file"  (exact action preserved as-is)
//
// Exact action IDs are never collapsed to their namespace — they intentionally
// restrict an agent to a specific action, not an entire skill group.
func normalizeSkillPattern(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasSuffix(p, ".*") {
		return strings.TrimSuffix(p, ".*")
	}
	if strings.HasSuffix(p, ".") {
		return strings.TrimSuffix(p, ".")
	}
	return p
}

func normalizeSkillPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	seen := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		n := normalizeSkillPattern(p)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func validateDefinition(def agentDefinition) error {
	def = normalizeDefinition(def)
	switch {
	case def.Name == "":
		return fmt.Errorf("agent name is required")
	case def.ID == "":
		return fmt.Errorf("agent %q is missing required field ID", def.Name)
	case def.Role == "":
		return fmt.Errorf("agent %q is missing required field Role", def.Name)
	case def.Mission == "":
		return fmt.Errorf("agent %q is missing required field Mission", def.Name)
	case len(def.AllowedSkills) == 0:
		return fmt.Errorf("agent %q is missing required field Allowed Skills", def.Name)
	case def.Autonomy == "":
		return fmt.Errorf("agent %q is missing required field Autonomy", def.Name)
	}
	return nil
}

func slugID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// splitList splits a comma-separated string into a deduplicated slice.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}
