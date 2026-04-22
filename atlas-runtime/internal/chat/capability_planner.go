package chat

import (
	"fmt"
	"strings"

	"atlas-runtime-go/internal/capabilities"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

const capabilityPlannerHistoryWindow = 4

func analyzeCapabilityPlan(message, supportDir string, workflows capabilities.WorkflowLister, automations capabilities.AutomationLister) capabilities.Analysis {
	inventory, err := capabilities.List(supportDir, workflows, automations)
	if err != nil {
		logstore.Write("warn", "Capability planner: inventory unavailable", map[string]string{"error": err.Error()})
		return capabilities.Analysis{Goal: strings.TrimSpace(message), Decision: capabilities.DecisionRunExisting}
	}
	analysis := capabilities.Analyze(message, inventory)
	meta := map[string]string{
		"decision":              string(analysis.Decision),
		"suggested_groups":      strings.Join(analysis.SuggestedGroups, ","),
		"missing_capabilities":  strings.Join(analysis.MissingCapabilities, ","),
		"missing_prerequisites": strings.Join(analysis.MissingPrerequisites, ","),
	}
	logstore.Write("debug",
		fmt.Sprintf("Capability planner: decision=%s requirements=%d missingCaps=%d missingPrereqs=%d",
			analysis.Decision, len(analysis.Requirements), len(analysis.MissingCapabilities), len(analysis.MissingPrerequisites)),
		meta)
	return analysis
}

func capabilityPolicy(message, supportDir string, workflows capabilities.WorkflowLister, automations capabilities.AutomationLister) (capabilities.Analysis, capabilities.Policy) {
	analysis := analyzeCapabilityPlan(message, supportDir, workflows, automations)
	policy := capabilities.BuildPolicy(analysis)
	logstore.Write("debug",
		fmt.Sprintf("Capability policy: decision=%s next=%s", policy.Decision, policy.NextAction),
		map[string]string{"decision": string(policy.Decision), "next_action": policy.NextAction})
	return analysis, policy
}

func taskContextFromHistory(history []storage.MessageRow, current string) string {
	current = strings.TrimSpace(current)
	if len(history) == 0 {
		return current
	}

	userTurns := make([]string, 0, capabilityPlannerHistoryWindow+1)
	seen := map[string]bool{}
	appendTurn := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" || seen[text] {
			return
		}
		seen[text] = true
		userTurns = append(userTurns, text)
	}

	start := len(history) - capabilityPlannerHistoryWindow*2
	if start < 0 {
		start = 0
	}
	for _, msg := range history[start:] {
		if msg.Role != "user" {
			continue
		}
		appendTurn(msg.Content)
	}
	appendTurn(current)
	if len(userTurns) == 0 {
		return current
	}
	return strings.Join(userTurns, "\n")
}

func mergeToolDefs(existing []map[string]any, additions []map[string]any) []map[string]any {
	if len(existing) == 0 {
		return additions
	}
	if len(additions) == 0 {
		return existing
	}
	merged := make([]map[string]any, 0, len(existing)+len(additions))
	seen := make(map[string]bool, len(existing)+len(additions))
	for _, tool := range existing {
		name := toolFunctionName(tool)
		if name != "" {
			seen[name] = true
		}
		merged = append(merged, tool)
	}
	for _, tool := range additions {
		name := toolFunctionName(tool)
		if name != "" && seen[name] {
			continue
		}
		if name != "" {
			seen[name] = true
		}
		merged = append(merged, tool)
	}
	return merged
}

func applyCapabilityPlanToolHints(registry *skills.Registry, selected []map[string]any, message string, analysis capabilities.Analysis) []map[string]any {
	if registry == nil || len(selected) == 0 || len(analysis.SuggestedGroups) == 0 {
		return selected
	}
	return mergeToolDefs(selected, registry.ToolDefsForGroupsForMessage(analysis.SuggestedGroups, message))
}

func toolFunctionName(tool map[string]any) string {
	fn, _ := tool["function"].(map[string]any)
	name, _ := fn["name"].(string)
	return strings.TrimSpace(name)
}
