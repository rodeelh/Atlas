package workflowexec

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/storage"
)

// Store is the workflow persistence surface used by HTTP, agent actions, and automations.
type Store interface {
	GetWorkflow(id string) (*storage.WorkflowRow, error)
	SaveWorkflowRun(row storage.WorkflowRunRow) error
	CompleteWorkflowRun(runID, status string, outcome, assistantSummary, errorMessage, finishedAt *string, durationMs int64, artifactsJSON *string) error
	UpdateWorkflowRunStepRuns(runID, stepRunsJSON string) error
}

// PreparedRun contains the runtime state needed to execute a workflow prompt.
type PreparedRun struct {
	RunID          string
	WorkflowID     string
	ConversationID string
	Prompt         string
	InputValues    map[string]string
	Definition     map[string]any
	Record         map[string]any
	StartedAt      time.Time
}

// PrepareRun creates a workflow run record and returns the composed prompt for the agent.
func PrepareRun(store Store, workflowID, runID, conversationID, triggerSource string, inputValues map[string]string, extraInstruction string) (PreparedRun, error) {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return PreparedRun{}, fmt.Errorf("workflow ID is required")
	}
	if store == nil {
		return PreparedRun{}, fmt.Errorf("workflow store is required")
	}
	row, err := store.GetWorkflow(workflowID)
	if err != nil {
		return PreparedRun{}, fmt.Errorf("load workflow %s: %w", workflowID, err)
	}
	if row == nil {
		return PreparedRun{}, fmt.Errorf("workflow not found: %s", workflowID)
	}
	var def map[string]any
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &def); err != nil {
		return PreparedRun{}, fmt.Errorf("corrupt workflow definition %s: %w", workflowID, err)
	}

	prompt := ComposePrompt(def, inputValues, extraInstruction)
	started := time.Now().UTC()
	workflowName := stringField(def, "name")
	if workflowName == "" {
		workflowName = row.Name
	}
	stepRuns := InitialStepRuns(def)
	run := map[string]any{
		"id":             runID,
		"workflowID":     workflowID,
		"workflowName":   workflowName,
		"status":         "running",
		"startedAt":      started.Format(time.RFC3339),
		"conversationID": conversationID,
		"triggerSource":  triggerSource,
		"inputValues":    map[string]string{},
		"stepRuns":       stepRuns,
	}
	if len(inputValues) > 0 {
		run["inputValues"] = inputValues
	}
	inputsJSON := "{}"
	if len(inputValues) > 0 {
		if data, err := json.Marshal(inputValues); err == nil {
			inputsJSON = string(data)
		}
	}
	recordJSON := "{}"
	if data, err := json.Marshal(run); err == nil {
		recordJSON = string(data)
	}
	stepRunsJSON := "[]"
	if data, err := json.Marshal(stepRuns); err == nil {
		stepRunsJSON = string(data)
	}
	if err := store.SaveWorkflowRun(storage.WorkflowRunRow{
		RunID:           runID,
		WorkflowID:      workflowID,
		WorkflowName:    workflowName,
		Status:          "running",
		InputValuesJSON: inputsJSON,
		StepRunsJSON:    stepRunsJSON,
		StartedAt:       started.Format(time.RFC3339),
		ConversationID:  strPtr(conversationID),
		TriggerSource:   triggerSource,
		RecordJSON:      recordJSON,
	}); err != nil {
		return PreparedRun{}, fmt.Errorf("create workflow run: %w", err)
	}

	return PreparedRun{
		RunID:          runID,
		WorkflowID:     workflowID,
		ConversationID: conversationID,
		Prompt:         prompt,
		InputValues:    inputValues,
		Definition:     def,
		Record:         run,
		StartedAt:      started,
	}, nil
}

// ComposePrompt resolves the workflow prompt and appends optional inputs/instructions.
func ComposePrompt(def map[string]any, inputValues map[string]string, extraInstruction string) string {
	workflowPrompt := stringField(def, "promptTemplate")
	if strings.TrimSpace(workflowPrompt) == "" {
		workflowPrompt = stringField(def, "prompt")
	}
	if strings.TrimSpace(workflowPrompt) == "" {
		if desc := stringField(def, "description"); strings.TrimSpace(desc) != "" {
			workflowPrompt = desc
		} else if name := stringField(def, "name"); strings.TrimSpace(name) != "" {
			workflowPrompt = "Execute workflow: " + name
		} else {
			workflowPrompt = "Execute this workflow."
		}
	}

	parts := []string{strings.TrimSpace(workflowPrompt)}
	if steps := promptSteps(def); len(steps) > 0 {
		parts = append(parts, "Workflow steps:\n"+strings.Join(steps, "\n"))
	}
	if scope := trustScopeInstructions(def); scope != "" {
		parts = append(parts, "Workflow trust scope:\n"+scope)
	}
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
func CompleteRun(store Store, runID, status, assistantSummary, errorMessage string, startedAt time.Time) error {
	if store == nil {
		return fmt.Errorf("workflow store is required")
	}
	finished := time.Now().UTC()
	finishedStr := finished.Format(time.RFC3339)
	durationMs := int64(0)
	if !startedAt.IsZero() {
		durationMs = finished.Sub(startedAt).Milliseconds()
	}
	var outcome *string
	if status == "completed" {
		outcome = strPtr("success")
	} else if status != "" {
		outcome = strPtr(status)
	}
	var summary *string
	if strings.TrimSpace(assistantSummary) != "" {
		summary = strPtr(strings.TrimSpace(assistantSummary))
	}
	var errMsg *string
	if strings.TrimSpace(errorMessage) != "" {
		errMsg = strPtr(strings.TrimSpace(errorMessage))
	}
	return store.CompleteWorkflowRun(runID, status, outcome, summary, errMsg, &finishedStr, durationMs, nil)
}

func stringField(def map[string]any, key string) string {
	value, _ := def[key].(string)
	return strings.TrimSpace(value)
}

func promptSteps(def map[string]any) []string {
	raw := workflowStepItems(def["steps"])
	if len(raw) == 0 {
		return nil
	}
	var out []string
	for idx, item := range raw {
		step, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title := stringField(step, "title")
		if title == "" {
			title = fmt.Sprintf("Step %d", idx+1)
		}
		kind := stringField(step, "kind")
		prompt := stringField(step, "prompt")
		if prompt == "" {
			continue
		}
		if kind == "" {
			kind = "prompt"
		}
		out = append(out, fmt.Sprintf("%d. %s (%s): %s", idx+1, title, kind, prompt))
	}
	return out
}

// InitialStepRuns builds the persisted step-run placeholders for a workflow definition.
func InitialStepRuns(def map[string]any) []map[string]any {
	raw := workflowStepItems(def["steps"])
	if len(raw) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(raw))
	for idx, item := range raw {
		step, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(step, "id")
		if id == "" {
			id = fmt.Sprintf("step-%d", idx+1)
		}
		title := stringField(step, "title")
		if title == "" {
			title = fmt.Sprintf("Step %d", idx+1)
		}
		status := "pending"
		stepType := strings.ToLower(strings.TrimSpace(stringField(step, "type")))
		if stepType == "" {
			stepType = strings.ToLower(strings.TrimSpace(stringField(step, "kind")))
		}
		if stepType == "" {
			stepType = "prompt"
		}
		out = append(out, map[string]any{
			"id":     id + "-run",
			"stepID": id,
			"title":  title,
			"type":   stepType,
			"status": status,
		})
	}
	return out
}

func workflowStepItems(raw any) []any {
	switch items := raw.(type) {
	case []any:
		return items
	case []map[string]any:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func trustScopeInstructions(def map[string]any) string {
	scope, ok := def["trustScope"].(map[string]any)
	if !ok {
		return ""
	}
	var parts []string
	if paths := stringSlice(scope["approvedRootPaths"]); len(paths) > 0 {
		parts = append(parts, "Approved root paths: "+strings.Join(paths, ", "))
	}
	if apps := stringSlice(scope["allowedApps"]); len(apps) > 0 {
		parts = append(parts, "Allowed apps/tools: "+strings.Join(apps, ", "))
	}
	if sensitive, ok := scope["allowsSensitiveRead"].(bool); ok && !sensitive {
		parts = append(parts, "Do not read sensitive data unless the user explicitly provides it in this run.")
	}
	if liveWrite, ok := scope["allowsLiveWrite"].(bool); ok && !liveWrite {
		parts = append(parts, "Do not perform live writes or external side effects unless the runtime action-safety policy separately allows them.")
	}
	return strings.Join(parts, "\n")
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func strPtr(v string) *string {
	return &v
}
