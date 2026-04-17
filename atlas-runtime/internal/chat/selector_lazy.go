package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/skills"
)

// lazySelector is the ToolSelector for "lazy" / Smart mode.
// It owns the upgrade stage so the agent loop no longer needs to track it.
type lazySelector struct {
	ctx      context.Context
	cfg      config.RuntimeConfigSnapshot
	turn     *turnContext
	msg      string
	registry *skills.Registry
	stage    int // 0 = meta-only, 1 = short list, 2 = broad/category
}

func (s *lazySelector) Initial() []map[string]any {
	return appendRequestToolsMeta(selectToolsWithLLM(s.ctx, s.cfg, s.turn, s.msg, s.registry))
}

func (s *lazySelector) Upgrade(tc agent.OAIToolCall) ([]map[string]any, string) {
	var args agent.RequestToolsArgs
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

	if len(args.Categories) > 0 {
		tools := s.registry.ToolDefsForGroupsForMessage(args.Categories, s.msg)
		s.stage = 2
		return tools, fmt.Sprintf(
			"Tool capabilities are now expanded for categories: %s. "+
				"Proceed using the appropriate tools. "+
				"If these are still insufficient, call request_tools again with broad=true.",
			strings.Join(args.Categories, ", "),
		)
	}
	if args.Broad || s.stage >= 1 {
		s.stage = 2
		return s.registry.ToolDefinitions(),
			"The broad tool surface is now available. " +
				"Proceed using the appropriate tools; " +
				"do not ask the user to paste a spec if a tool can perform the action."
	}
	s.stage = 1
	return s.registry.SelectiveToolDefs(s.msg),
		"A short relevant tool list is now available. " +
			"Proceed using those tools. " +
			"If the short list is not enough, call request_tools again with broad=true or with categories."
}
