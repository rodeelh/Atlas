package chat

import (
	"context"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/skills"
)

// NewSelector creates the ToolSelector for the given mode.
// policy scopes the available tools to AllowedToolPrefixes when set.
func NewSelector(
	mode string,
	policy *agent.ToolPolicy,
	ctx context.Context,
	cfg config.RuntimeConfigSnapshot,
	turn *turnContext,
	message string,
	registry *skills.Registry,
) agent.ToolSelector {
	// Pre-scope the registry to the policy's allowed prefixes so every
	// selector (initial + upgrade) automatically respects the constraint.
	if policy != nil && len(policy.AllowedToolPrefixes) > 0 {
		registry = registry.FilteredByPatterns(policy.AllowedToolPrefixes)
	}

	switch mode {
	case "lazy":
		return &lazySelector{ctx: ctx, cfg: cfg, turn: turn, msg: message, registry: registry}
	case "llm":
		return &llmSelector{ctx: ctx, cfg: cfg, turn: turn, msg: message, registry: registry}
	case "heuristic":
		return &heuristicSelector{msg: message, registry: registry}
	default: // "off" or unrecognised
		if policy != nil && len(policy.AllowedToolPrefixes) > 0 {
			// IdentitySelector returns nil (full loop tools) which would bypass
			// the pre-filtered registry, so use scopedSelector instead.
			return &scopedSelector{registry: registry}
		}
		return agent.IdentitySelector{}
	}
}

// scopedSelector is used for "off" mode when a ToolPolicy narrows the tool set.
// It exposes all tools in the pre-filtered registry rather than the full one.
type scopedSelector struct{ registry *skills.Registry }

func (s *scopedSelector) Initial() []map[string]any { return s.registry.ToolDefinitions() }
func (s *scopedSelector) Upgrade(_ agent.OAIToolCall) ([]map[string]any, string) {
	return s.registry.ToolDefinitions(), ""
}
