package chat

import (
	"context"
	"encoding/json"
	"testing"

	"atlas-runtime-go/internal/capabilities"
	"atlas-runtime-go/internal/skills"
)

func TestApplyCapabilityPlanToolHintsAddsSuggestedGroups(t *testing.T) {
	reg := skills.NewRegistry(t.TempDir(), nil, nil)
	selected := reg.ToolDefsForGroupsForMessage([]string{"files"}, "create a pdf")
	analysis := capabilities.Analysis{
		Decision:        capabilities.DecisionComposeExisting,
		SuggestedGroups: []string{"files", "automation"},
	}

	out := applyCapabilityPlanToolHints(reg, selected, "create a pdf every friday", analysis)
	if len(out) <= len(selected) {
		t.Fatalf("expected planner hints to expand tool set: before=%d after=%d", len(selected), len(out))
	}
	if !hasToolPrefix(out, "gremlin__") {
		t.Fatalf("expected automation tools after planner hints, got %v", toolNames(out))
	}
}

func TestApplyCapabilityPlanToolHintsAddsTeamTools(t *testing.T) {
	reg := skills.NewRegistry(t.TempDir(), nil, nil)
	reg.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "team.list",
			Description: "List Atlas agents.",
			Properties:  map[string]skills.ToolParam{},
		},
		ActionClass: skills.ActionClassRead,
		FnResult: func(context.Context, json.RawMessage) (skills.ToolResult, error) {
			return skills.OKResult("ok", nil), nil
		},
	})
	selected := reg.ToolDefsForGroupsForMessage([]string{"files"}, "delete all agents")
	analysis := capabilities.Analysis{
		Decision:        capabilities.DecisionRunExisting,
		SuggestedGroups: []string{"team"},
	}

	out := applyCapabilityPlanToolHints(reg, selected, "delete all agents", analysis)
	if !hasToolPrefix(out, "agent__") {
		t.Fatalf("expected agent tools after planner hints, got %v", toolNames(out))
	}
}
