package chat

import (
	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/skills"
)

// heuristicSelector uses registry.SelectiveToolDefs for initial selection.
// Upgrade returns the same heuristic set (no lazy expansion for this mode).
type heuristicSelector struct {
	msg      string
	registry *skills.Registry
}

func (s *heuristicSelector) Initial() []map[string]any {
	return s.registry.SelectiveToolDefs(s.msg)
}

func (s *heuristicSelector) Upgrade(_ agent.OAIToolCall) ([]map[string]any, string) {
	return s.Initial(), ""
}
