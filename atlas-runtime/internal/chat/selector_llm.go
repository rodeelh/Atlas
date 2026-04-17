package chat

import (
	"context"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/skills"
)

// llmSelector uses selectToolsWithLLM for initial selection.
// The router engine must be running before Initial() is called — the caller
// (selectTurnTools) ensures that via ensureRouterRunning.
type llmSelector struct {
	ctx      context.Context
	cfg      config.RuntimeConfigSnapshot
	turn     *turnContext
	msg      string
	registry *skills.Registry
}

func (s *llmSelector) Initial() []map[string]any {
	return selectToolsWithLLM(s.ctx, s.cfg, s.turn, s.msg, s.registry)
}

func (s *llmSelector) Upgrade(_ agent.OAIToolCall) ([]map[string]any, string) {
	return s.Initial(), ""
}
