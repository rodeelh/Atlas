package skills

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"atlas-runtime-go/internal/forge/forgetypes"
)

func TestForgeValidateLocalPlansRejectsThirdPartyPythonDependency(t *testing.T) {
	plans := []forgetypes.ForgeActionPlan{
		{
			ActionID: "make-pdf",
			Type:     "local",
			LocalPlan: &forgetypes.LocalPlan{
				Interpreter: "python3",
				Script:      "from reportlab.pdfgen import canvas\nprint('hi')",
			},
		},
	}

	msg := forgeValidateLocalPlans(plans)
	if !strings.Contains(msg, "standard library only") {
		t.Fatalf("expected stdlib rejection, got %q", msg)
	}
}

func TestForgeOrchestrationProposeTracksResearchingState(t *testing.T) {
	reg := NewRegistry(t.TempDir(), nil, nil)
	started := 0
	stopped := 0
	var trackedTitle string
	reg.SetForgeResearchTracker(func(title, _ string) func() {
		started++
		trackedTitle = title
		return func() { stopped++ }
	})
	reg.SetForgePersistFn(func(_, _, _, _, _ string) (
		id, name, skillID, riskLevel string,
		actionNames, domains []string,
		err error,
	) {
		if started != 1 || stopped != 0 {
			t.Fatalf("expected active tracker during persistence, started=%d stopped=%d", started, stopped)
		}
		return "proposal-1", "macOS Version", "macos-version", "low", []string{"Get Version"}, nil, nil
	})

	spec := map[string]any{
		"id":          "macos-version",
		"name":        "macOS Version",
		"description": "Gets the current macOS version.",
		"category":    "utility",
		"riskLevel":   "low",
		"tags":        []string{"macos"},
		"actions": []map[string]any{{
			"id":              "get-version",
			"name":            "Get Version",
			"description":     "Returns the current macOS version.",
			"permissionLevel": "read",
			"testCases": []map[string]any{{
				"args":          map[string]any{},
				"expectSuccess": true,
			}},
		}},
	}
	plans := []map[string]any{{
		"actionID": "get-version",
		"type":     "local",
		"localPlan": map[string]any{
			"interpreter": "bash",
			"script":      "sw_vers -productVersion",
		},
	}}
	specJSON, _ := json.Marshal(spec)
	plansJSON, _ := json.Marshal(plans)
	args, _ := json.Marshal(map[string]any{
		"kind":       "local",
		"spec_json":  string(specJSON),
		"plans_json": string(plansJSON),
		"summary":    "Gets the current macOS version.",
	})

	out, err := reg.Execute(context.Background(), "forge.orchestration.propose", args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out.Success || !strings.Contains(out.Summary, "Forge proposal created") {
		t.Fatalf("unexpected result: %+v", out)
	}
	if started != 1 || stopped != 1 {
		t.Fatalf("expected tracker start/stop once, got started=%d stopped=%d", started, stopped)
	}
	if trackedTitle != "macOS Version" {
		t.Fatalf("expected tracked title, got %q", trackedTitle)
	}
}

func TestForgeOrchestrationProposeValidationFailureIsRepairableToolFailure(t *testing.T) {
	reg := NewRegistry(t.TempDir(), nil, nil)
	spec := map[string]any{
		"id":          "macos-version",
		"name":        "macOS Version",
		"description": "Gets the current macOS version.",
		"category":    "utility",
		"riskLevel":   "low",
		"actions": []map[string]any{{
			"id":              "get-version",
			"name":            "Get Version",
			"description":     "Returns the current macOS version.",
			"permissionLevel": "read",
		}},
	}
	plans := []map[string]any{{
		"actionID": "get-version",
		"type":     "local",
		"localPlan": map[string]any{
			"interpreter": "bash",
			"script":      "sw_vers -productVersion",
		},
	}}
	specJSON, _ := json.Marshal(spec)
	plansJSON, _ := json.Marshal(plans)
	args, _ := json.Marshal(map[string]any{
		"kind":       "local",
		"spec_json":  string(specJSON),
		"plans_json": string(plansJSON),
		"summary":    "Gets the current macOS version.",
	})

	out, err := reg.Execute(context.Background(), "forge.orchestration.propose", args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Success {
		t.Fatalf("expected validation failure to be success=false, got %+v", out)
	}
	if out.Artifacts["status"] != "needs_revision" || out.Artifacts["next_step"] != "revise_and_recall_forge_orchestration_propose" {
		t.Fatalf("expected repair artifacts, got %+v", out.Artifacts)
	}
	if !strings.Contains(out.Summary, "do not ask the user to try again") {
		t.Fatalf("expected internal repair instruction, got %q", out.Summary)
	}
}

func TestForgeValidateLocalPlansRejectsBuiltInFileGenerationTasks(t *testing.T) {
	plans := []forgetypes.ForgeActionPlan{
		{
			ActionID: "make-pdf",
			Type:     "local",
			LocalPlan: &forgetypes.LocalPlan{
				Interpreter: "bash",
				Script:      "echo 'create pdf report.pdf'",
			},
		},
	}

	msg := forgeValidateLocalPlans(plans)
	if !strings.Contains(msg, "fs.create_pdf") {
		t.Fatalf("expected built-in file generation rejection, got %q", msg)
	}
}
