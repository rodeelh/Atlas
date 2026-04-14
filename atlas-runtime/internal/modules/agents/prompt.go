package agents

import (
	"encoding/json"
	"strings"

	"atlas-runtime-go/internal/storage"
)

// composeWorkerPrompt builds the system prompt for a delegated worker.
//
// Spec §8 — Teams owns worker prompt assembly. Agent must not build worker
// prompts directly. Workers must not inherit Atlas's full system prompt.
//
// Three-layer composition with task > member > template precedence:
//  1. Template layer  — role-specific default behavior from templateContracts
//  2. Member layer    — name, mission, style, skills from AgentDefinitionRow
//  3. Task layer      — title, objective, scope, criteria, context from AgentTaskRow
//
// Four required sections: Identity, Assignment, Context, Execution contract.
func composeWorkerPrompt(def storage.AgentDefinitionRow, task storage.AgentTaskRow) string {
	contract := templateContracts[strings.ToLower(strings.TrimSpace(def.TemplateRole))]

	var b strings.Builder

	// ── Section 1: Identity ───────────────────────────────────────────────────
	b.WriteString("## Identity\n\n")
	roleLine := def.Name
	if contract.label != "" {
		roleLine += " (" + contract.label + ")"
	} else if strings.TrimSpace(def.Role) != "" {
		roleLine += " (" + strings.TrimSpace(def.Role) + ")"
	}
	b.WriteString("You are " + roleLine + ", a specialist team member delegated by Atlas.\n")
	b.WriteString("Mission: " + strings.TrimSpace(def.Mission) + "\n")
	if strings.TrimSpace(def.Style) != "" {
		b.WriteString("Style: " + strings.TrimSpace(def.Style) + "\n")
	}

	var allowedSkills []string
	_ = json.Unmarshal([]byte(def.AllowedSkillsJSON), &allowedSkills)
	if len(allowedSkills) > 0 {
		b.WriteString("Allowed skills: " + strings.Join(allowedSkills, ", ") + "\n")
	}

	if def.AllowedToolClassesJSON != "" && def.AllowedToolClassesJSON != "null" {
		var toolClasses []string
		_ = json.Unmarshal([]byte(def.AllowedToolClassesJSON), &toolClasses)
		if len(toolClasses) > 0 {
			b.WriteString("Allowed tool classes: " + strings.Join(toolClasses, ", ") + "\n")
		}
	}

	// ── Section 2: Assignment ─────────────────────────────────────────────────
	b.WriteString("\n## Assignment\n\n")

	// Title / objective — prefer structured fields (V1 path), fall back to Goal
	// (legacy path where no DelegationPlan was supplied).
	title := strings.TrimSpace(task.Title)
	objective := strings.TrimSpace(task.Objective)
	if title == "" {
		title = strings.TrimSpace(task.Goal)
	}
	if objective == "" {
		objective = strings.TrimSpace(task.Goal)
	}
	b.WriteString("Title: " + title + "\n")
	b.WriteString("Objective: " + objective + "\n")

	// Scope (from structured JSON; omitted on legacy path where it defaults to {})
	if s := strings.TrimSpace(task.ScopeJSON); s != "" && s != "{}" && s != "null" {
		var scope DelegationScope
		if err := json.Unmarshal([]byte(s), &scope); err == nil {
			if len(scope.Included) > 0 || len(scope.Excluded) > 0 || len(scope.Boundaries) > 0 {
				b.WriteString("Scope:\n")
				for _, a := range scope.Included {
					b.WriteString("  in scope: " + a + "\n")
				}
				for _, a := range scope.Excluded {
					b.WriteString("  out of scope: " + a + "\n")
				}
				for _, bnd := range scope.Boundaries {
					b.WriteString("  boundary: " + bnd + "\n")
				}
			}
		}
	}

	// Success criteria
	if s := strings.TrimSpace(task.SuccessCriteriaJSON); s != "" && s != "{}" && s != "null" {
		var sc DelegationSuccessCriteria
		if err := json.Unmarshal([]byte(s), &sc); err == nil {
			if len(sc.Must) > 0 || len(sc.Should) > 0 || len(sc.FailureConditions) > 0 {
				b.WriteString("Success criteria:\n")
				for _, m := range sc.Must {
					b.WriteString("  must: " + m + "\n")
				}
				for _, sh := range sc.Should {
					b.WriteString("  should: " + sh + "\n")
				}
				for _, fc := range sc.FailureConditions {
					b.WriteString("  fail if: " + fc + "\n")
				}
			}
		}
	}

	// Expected output
	if s := strings.TrimSpace(task.ExpectedOutputJSON); s != "" && s != "{}" && s != "null" {
		var eo DelegationExpectedOutput
		if err := json.Unmarshal([]byte(s), &eo); err == nil && eo.Type != "" {
			line := "Expected output: " + eo.Type
			if len(eo.FormatNotes) > 0 {
				line += " — " + strings.Join(eo.FormatNotes, "; ")
			}
			b.WriteString(line + "\n")
		}
	}

	// ── Section 3: Context ────────────────────────────────────────────────────
	var ic DelegationInputContext
	hasContext := false
	if s := strings.TrimSpace(task.InputContextJSON); s != "" && s != "{}" && s != "null" {
		if err := json.Unmarshal([]byte(s), &ic); err == nil {
			hasContext = ic.AtlasTaskFrame != "" || len(ic.PriorResults) > 0 ||
				len(ic.KnownConstraints) > 0 || len(ic.Artifacts) > 0
		}
	}
	if hasContext {
		b.WriteString("\n## Context\n\n")
		if ic.AtlasTaskFrame != "" {
			b.WriteString(strings.TrimSpace(ic.AtlasTaskFrame) + "\n")
		}
		if len(ic.PriorResults) > 0 {
			b.WriteString("\nPrior results from earlier steps:\n")
			for _, pr := range ic.PriorResults {
				b.WriteString("- " + pr + "\n")
			}
		}
		if len(ic.KnownConstraints) > 0 {
			b.WriteString("\nKnown constraints:\n")
			for _, c := range ic.KnownConstraints {
				b.WriteString("- " + c + "\n")
			}
		}
		if len(ic.Artifacts) > 0 {
			b.WriteString("\nArtifacts:\n")
			for _, a := range ic.Artifacts {
				b.WriteString("- " + a + "\n")
			}
		}
	}

	// ── Section 4: Execution contract ─────────────────────────────────────────
	b.WriteString("\n## Execution contract\n\n")

	// Role-specific behavior from template (template layer — lowest precedence,
	// establishes defaults; member and task data above already override specifics).
	if contract.behaviorRules != "" {
		b.WriteString(contract.behaviorRules + "\n\n")
	}

	// Universal contract rules (spec §8.6).
	b.WriteString("- Work only within the scope defined in the Assignment section.\n")
	b.WriteString("- Use only your allowed skills listed in the Identity section.\n")
	b.WriteString("- Do not broaden or reinterpret the task beyond what is specified.\n")
	b.WriteString("- Clearly report any blockers or missing prerequisites before stopping.\n")
	if s := strings.TrimSpace(task.ExpectedOutputJSON); s != "" && s != "{}" && s != "null" {
		var eo DelegationExpectedOutput
		if err := json.Unmarshal([]byte(s), &eo); err == nil && eo.Type != "" {
			b.WriteString("- Return your output in the expected format: " + eo.Type + ".\n")
		}
	}
	b.WriteString("- You are a specialist team member, not Atlas. Do not present your output as if you are the primary assistant.\n")

	return b.String()
}

// templateContract defines role-specific defaults injected at the template layer.
type templateContract struct {
	label         string // short label shown in Identity section
	behaviorRules string // added to Execution contract section
}

// templateContracts maps template_role values (lowercase) to their contracts.
// An empty key ("") is the fallback used when no template_role is set.
var templateContracts = map[string]templateContract{
	"scout": {
		label: "Scout",
		behaviorRules: "Gather facts, references, comparisons, and external context.\n" +
			"Prefer evidence over speculation — surface uncertainty clearly.\n" +
			"Avoid implementation work unless explicitly required by the objective.",
	},
	"builder": {
		label: "Builder",
		behaviorRules: "Produce a first-pass implementation or artifact result.\n" +
			"Optimize for useful forward progress — make reasonable assumptions within scope.\n" +
			"Avoid turning the task into a critique or review pass.",
	},
	"reviewer": {
		label: "Reviewer",
		behaviorRules: "Inspect for correctness, risk, regressions, gaps, and edge cases.\n" +
			"Prioritize issues by severity — stay within the defined review scope.\n" +
			"Avoid rewriting work unless explicitly requested.",
	},
	"operator": {
		label: "Operator",
		behaviorRules: "Execute bounded operational or tool-driven tasks reliably.\n" +
			"Log meaningful state changes and blockers.\n" +
			"Prefer deterministic completion over open-ended reasoning.\n" +
			"Stop cleanly when blocked on an approval or unmet prerequisite.",
	},
	"monitor": {
		label: "Monitor",
		behaviorRules: "Watch for trigger conditions, failures, stale states, or anomalies.\n" +
			"Report only meaningful change — remain quiet when no action is required.\n" +
			"Escalate with concise, actionable recommendations.",
	},
	"": {
		label: "",
		behaviorRules: "Work narrowly and complete the assigned goal using only your allowed tools.\n" +
			"Be concise and execution-focused — return a useful final result.",
	},
}
