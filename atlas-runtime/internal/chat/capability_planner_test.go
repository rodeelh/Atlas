package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"atlas-runtime-go/internal/capabilities"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
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

func TestTaskContextFromHistoryCarriesPriorUserGoal(t *testing.T) {
	history := []storage.MessageRow{
		{Role: "user", Content: "send a message to 646-425-7838 via iMessage", Timestamp: time.Now().Format(time.RFC3339)},
		{Role: "assistant", Content: "I tried the direct path.", Timestamp: time.Now().Format(time.RFC3339)},
	}

	context := taskContextFromHistory(history, "figure it out please")
	if context == "figure it out please" {
		t.Fatalf("expected prior user goal to be preserved, got %q", context)
	}
	if !strings.Contains(context, "via iMessage") {
		t.Fatalf("expected prior iMessage request in planner context, got %q", context)
	}
	if !strings.Contains(context, "figure it out please") {
		t.Fatalf("expected current turn in planner context, got %q", context)
	}
}
