package workflowexec

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/features"
)

// PreparedRun contains the runtime state needed to execute a workflow prompt.
type PreparedRun struct {
	RunID          string
	WorkflowID     string
	ConversationID string
	Prompt         string
	Definition     map[string]any
	Record         map[string]any
}

// PrepareRun creates a workflow run record and returns the composed prompt for the agent.
func PrepareRun(supportDir, workflowID, runID, conversationID string, inputValues map[string]string, extraInstruction string) (PreparedRun, error) {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return PreparedRun{}, fmt.Errorf("workflow ID is required")
	}
	raw := features.GetWorkflowDefinition(supportDir, workflowID)
	if raw == nil {
		return PreparedRun{}, fmt.Errorf("workflow not found: %s", workflowID)
	}
	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		return PreparedRun{}, fmt.Errorf("corrupt workflow definition %s: %w", workflowID, err)
	}

	prompt := ComposePrompt(def, inputValues, extraInstruction)
	run := map[string]any{
		"id":             runID,
		"workflowID":     workflowID,
		"status":         "running",
		"startedAt":      time.Now().UTC().Format(time.RFC3339),
		"conversationID": conversationID,
	}
	if len(inputValues) > 0 {
		run["inputValues"] = inputValues
	}
	if err := features.AppendWorkflowRun(supportDir, run); err != nil {
		return PreparedRun{}, fmt.Errorf("create workflow run: %w", err)
	}

	return PreparedRun{
		RunID:          runID,
		WorkflowID:     workflowID,
		ConversationID: conversationID,
		Prompt:         prompt,
		Definition:     def,
		Record:         run,
	}, nil
}

// ComposePrompt resolves the workflow prompt and appends optional inputs/instructions.
func ComposePrompt(def map[string]any, inputValues map[string]string, extraInstruction string) string {
	workflowPrompt, _ := def["prompt"].(string)
	if strings.TrimSpace(workflowPrompt) == "" {
		if desc, _ := def["description"].(string); strings.TrimSpace(desc) != "" {
			workflowPrompt = desc
		} else if name, _ := def["name"].(string); strings.TrimSpace(name) != "" {
			workflowPrompt = "Execute workflow: " + name
		} else {
			workflowPrompt = "Execute this workflow."
		}
	}

	parts := []string{strings.TrimSpace(workflowPrompt)}
	if len(inputValues) > 0 {
		if data, err := json.Marshal(inputValues); err == nil {
			parts = append(parts, "Workflow input values:\n"+string(data))
		}
	}
	if strings.TrimSpace(extraInstruction) != "" {
		parts = append(parts, "Automation instruction:\n"+strings.TrimSpace(extraInstruction))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// CompleteRun updates a workflow run status and returns the updated record.
func CompleteRun(supportDir, runID, status string) (map[string]any, error) {
	return features.UpdateWorkflowRunStatus(supportDir, runID, status)
}
