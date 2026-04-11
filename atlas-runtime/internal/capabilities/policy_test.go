package capabilities

import (
	"strings"
	"testing"
)

func TestBuildPolicy_RunExisting(t *testing.T) {
	policy := BuildPolicy(Analysis{Decision: DecisionRunExisting})
	if policy.NextAction == "" {
		t.Fatal("expected next action")
	}
	if !strings.Contains(policy.PromptBlock, "existing skills directly") {
		t.Fatalf("unexpected prompt block: %q", policy.PromptBlock)
	}
}

func TestBuildPolicy_ForgeNewIncludesMissingCapabilities(t *testing.T) {
	policy := BuildPolicy(Analysis{
		Decision:            DecisionForgeNew,
		MissingCapabilities: []string{"delivery.email"},
	})
	if !strings.Contains(policy.PromptBlock, "delivery.email") {
		t.Fatalf("expected missing capability in prompt block: %q", policy.PromptBlock)
	}
}

func TestBuildPolicy_AskPrerequisiteIncludesMissingPrereqs(t *testing.T) {
	policy := BuildPolicy(Analysis{
		Decision:             DecisionAskPrerequisite,
		MissingPrerequisites: []string{"delivery.channel"},
	})
	if !strings.Contains(policy.PromptBlock, "delivery.channel") {
		t.Fatalf("expected missing prerequisite in prompt block: %q", policy.PromptBlock)
	}
}
