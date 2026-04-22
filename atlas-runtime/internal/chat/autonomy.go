package chat

import (
	"strings"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
)

var sandboxDeniedToolPatterns = []string{
	"forge.",
	"atlas.update_operator_prompt",
}

func autonomyPromptBlock(cfg config.RuntimeConfigSnapshot) string {
	if cfg.IsUnleashed() {
		return "Autonomy mode: unleashed\nMission:\n- You are in Atlas unleashed mode. Assume you are now expected to make the user's request happen by any legitimate available means.\n- Operate as a capable local operator, builder, researcher, and integrator, not just an advisor.\n- Default to execution, adaptation, and persistence. Do the work, close gaps, and keep moving until the request is complete or a real hard blocker remains.\nCapability doctrine:\n- When the user asks Atlas to do something new, treat capability-building as part of the job.\n- First inspect the real environment with atlas.session_capabilities, atlas.diagnose_blocker, system.app_capabilities, terminal.check_command, and fs.workspace_roots when they are relevant.\n- Then expand the tool surface when possible, research the gap online when needed, compose any viable path with current tools, and use Forge to create reusable extensions when the capability is genuinely missing.\n- Prefer forge.orchestration.propose_and_install when Atlas needs the new capability to become usable in the same run.\n- Do not stop at \"I lack the tool surface\" if a real next step exists.\n- If the first route fails, try another viable route before surfacing a blocker unless a real prerequisite is missing.\n- If the capability cannot be fully installed in this turn, still produce the most concrete executable outcome available: a live Forge install, proposal, relay spec, install plan, exact operator change, or partially working path.\nExecution doctrine:\n- In unleashed mode, normal local execution and external side-effect tools can run directly; do not describe them as approval-gated unless the action is truly privileged or explicitly in a send/publish class.\n- Preserve the user's underlying goal across short follow-ups like \"try again\", \"figure it out\", or \"do it another way\".\n- Prefer doing over describing, testing over theorizing, and shipping over postponing.\n- Make durable improvements when they help Atlas serve the user better on future turns.\nSafety boundary:\n- Treat self-improvement as bounded engineering work: prefer reversible changes, explain what changed, and keep the immutable safety layer intact.\n- Never claim success without results, never fabricate tool outcomes, and never override the hard safety contract."
	}
	return "Autonomy mode: sandboxed\n- You are running in a restricted mode by default.\n- Do not create new Atlas capabilities, Forge skills, or operator-prompt rewrites from inside the agent loop.\n- If the user asks for a missing capability, you may research the gap and explain the path forward, but self-modification remains locked until the user explicitly switches Atlas to unleashed mode."
}

func autonomyToolPolicy(cfg config.RuntimeConfigSnapshot) *agent.ToolPolicy {
	if cfg.EffectiveAutonomyMode() != config.AutonomyModeSandboxed {
		return nil
	}
	return &agent.ToolPolicy{
		Enabled:             true,
		AllowsSensitiveRead: true,
		AllowsLiveWrite:     true,
		DeniedToolPrefixes:  append([]string(nil), sandboxDeniedToolPatterns...),
	}
}

func mergeToolPolicies(base, extra *agent.ToolPolicy) *agent.ToolPolicy {
	if base == nil && extra == nil {
		return nil
	}
	if base == nil {
		dup := *extra
		dup.DeniedToolPrefixes = append([]string(nil), extra.DeniedToolPrefixes...)
		dup.AllowedToolPrefixes = append([]string(nil), extra.AllowedToolPrefixes...)
		dup.ApprovedRootPaths = append([]string(nil), extra.ApprovedRootPaths...)
		return &dup
	}
	if extra == nil {
		return base
	}

	merged := *base
	merged.Enabled = base.Enabled || extra.Enabled
	merged.AllowsSensitiveRead = base.AllowsSensitiveRead || extra.AllowsSensitiveRead
	merged.AllowsLiveWrite = base.AllowsLiveWrite || extra.AllowsLiveWrite
	merged.ApprovedRootPaths = dedupeStrings(append(append([]string(nil), base.ApprovedRootPaths...), extra.ApprovedRootPaths...))
	merged.AllowedToolPrefixes = dedupeStrings(append(append([]string(nil), base.AllowedToolPrefixes...), extra.AllowedToolPrefixes...))
	merged.DeniedToolPrefixes = dedupeStrings(append(append([]string(nil), base.DeniedToolPrefixes...), extra.DeniedToolPrefixes...))
	return &merged
}

func filterToolDefsByDeniedPatterns(tools []map[string]any, denied []string) []map[string]any {
	if len(denied) == 0 {
		return tools
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		actionID := strings.ReplaceAll(name, "__", ".")
		if matchesDeniedPattern(actionID, denied) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func matchesDeniedPattern(actionID string, denied []string) bool {
	for _, pattern := range denied {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		switch {
		case strings.HasSuffix(pattern, "."):
			if strings.HasPrefix(actionID, pattern) {
				return true
			}
		case strings.HasSuffix(pattern, "*"):
			if strings.HasPrefix(actionID, strings.TrimSuffix(pattern, "*")) {
				return true
			}
		default:
			if actionID == pattern {
				return true
			}
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
