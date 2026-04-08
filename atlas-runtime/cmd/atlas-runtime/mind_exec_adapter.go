package main

// mind_exec_adapter.go adapts *skills.Registry to the narrow
// mind.SkillExecutor interface the dispatcher needs. Keeps internal/mind
// free of a direct dependency on internal/skills — the adapter lives here
// at the wiring site in main.go.

import (
	"context"
	"encoding/json"

	"atlas-runtime-go/internal/skills"
)

// mindSkillExecutor wraps the skills registry to expose just Execute and
// NeedsApproval as plain string-returning methods.
type mindSkillExecutor struct {
	reg *skills.Registry
}

func newMindSkillExecutor(reg *skills.Registry) *mindSkillExecutor {
	return &mindSkillExecutor{reg: reg}
}

// Execute runs the given skill and returns the tool result as JSON text.
// ToolResult.FormatForModel is the same serialisation the agent loop uses
// when feeding results back to the model, so downstream consumers
// (greeting prompt, approvals, etc.) see the same shape.
func (m *mindSkillExecutor) Execute(ctx context.Context, actionID string, args json.RawMessage) (string, error) {
	res, err := m.reg.Execute(ctx, actionID, args)
	if err != nil {
		return "", err
	}
	return res.FormatForModel(), nil
}

// NeedsApproval delegates to the skills registry. The mind dispatcher uses
// this as a second-layer guard alongside the structural score ceiling.
func (m *mindSkillExecutor) NeedsApproval(actionID string) bool {
	return m.reg.NeedsApproval(actionID)
}
