package skills

import (
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
