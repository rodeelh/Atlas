package capabilities

import (
	"testing"

	runtimeskills "atlas-runtime-go/internal/skills"
)

func TestAnalyze_AskForPrerequisiteWhenPDFNeedsApprovedRoot(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Create a PDF report and save the file for me.", inventory)
	if analysis.Decision != DecisionAskPrerequisite {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionAskPrerequisite)
	}
	assertRequirementStatus(t, analysis, "file.create_pdf", StatusMissingPrerequisite)
	assertContains(t, analysis.MissingPrerequisites, "file.create_pdf")
	assertContains(t, analysis.SuggestedGroups, "files")
}

func TestAnalyze_RunExistingForPDFRequestWithApprovedRoot(t *testing.T) {
	dir := t.TempDir()
	if err := runtimeskills.SaveFsRoots(dir, []runtimeskills.FsRoot{{ID: "root-1", Path: dir}}); err != nil {
		t.Fatalf("SaveFsRoots: %v", err)
	}
	inventory, err := List(dir, nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Create a PDF report and save the file for me.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "file.create_pdf", StatusAvailable)
}

func TestAnalyze_ComposeExistingForScheduledChatDelivery(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Every Friday at 8 AM, send me a chat message with the weekly status.", inventory)
	if analysis.Decision != DecisionComposeExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionComposeExisting)
	}
	assertRequirementStatus(t, analysis, "automation.schedule", StatusAvailable)
	assertRequirementStatus(t, analysis, "delivery.chat", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "automation")
	assertContains(t, analysis.SuggestedGroups, "communication")
}

func TestAnalyze_ComposeExistingForExplicitTeamMemberCreation(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Create an agent that triages my inbox and sends useful items to Telegram.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "team.manage", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "team")
}

func TestAnalyze_RunExistingForAgentDeletionRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Delete all agents.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "team.manage", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "team")
}

func TestAnalyze_RunExistingForAutomationDeletionRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Delete all automations.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "automation.schedule", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "automation")
}

func TestAnalyze_RunExistingForWorkflowDeletionRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Delete all workflows.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "workflow.compose", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "workflow")
}

func TestAnalyze_RunExistingForAutomationActivationRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Activate all automations.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "automation.schedule", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "automation")
}

func TestAnalyze_AskForPrerequisiteForAuthorizedChannel(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Send this to my Telegram channel every morning.", inventory)
	if analysis.Decision != DecisionAskPrerequisite {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionAskPrerequisite)
	}
	assertRequirementStatus(t, analysis, "delivery.channel", StatusMissingPrerequisite)
	assertContains(t, analysis.MissingPrerequisites, "delivery.channel")
}

func TestAnalyze_RunExistingWhenEmailDeliveryCapabilityIsPresent(t *testing.T) {
	dir := t.TempDir()
	if err := runtimeskills.SaveFsRoots(dir, []runtimeskills.FsRoot{{ID: "root-1", Path: dir}}); err != nil {
		t.Fatalf("SaveFsRoots: %v", err)
	}
	inventory, err := List(dir, nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Email me a PDF report every Friday.", inventory)
	if analysis.Decision != DecisionComposeExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionComposeExisting)
	}
	assertRequirementStatus(t, analysis, "file.create_pdf", StatusAvailable)
	assertRequirementStatus(t, analysis, "delivery.email", StatusAvailable)
	if len(analysis.MissingCapabilities) != 0 {
		t.Fatalf("missing capabilities = %v, want none", analysis.MissingCapabilities)
	}
}

func TestAnalyze_ForgeNewForCapabilityExpansionRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Can you figure out a way to connect to iMessage so Atlas can send and receive messages?", inventory)
	if analysis.Decision != DecisionForgeNew {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionForgeNew)
	}
	assertRequirementStatus(t, analysis, "forge.build", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "forge")
}

func TestAnalyze_RunExistingForIMessageSendRequest(t *testing.T) {
	inventory, err := List(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	analysis := Analyze("Send a message to 646-425-7838 via iMessage.", inventory)
	if analysis.Decision != DecisionRunExisting {
		t.Fatalf("decision = %q, want %q", analysis.Decision, DecisionRunExisting)
	}
	assertRequirementStatus(t, analysis, "delivery.imessage", StatusAvailable)
	assertContains(t, analysis.SuggestedGroups, "communication")
}

func assertRequirementStatus(t *testing.T, analysis Analysis, reqType string, want RequirementStatus) {
	t.Helper()
	for _, req := range analysis.Requirements {
		if req.Type == reqType {
			if req.Status != want {
				t.Fatalf("requirement %s status = %q, want %q", reqType, req.Status, want)
			}
			return
		}
	}
	t.Fatalf("missing requirement %s", reqType)
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, values)
}
