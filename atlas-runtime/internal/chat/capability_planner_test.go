package chat

import (
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
