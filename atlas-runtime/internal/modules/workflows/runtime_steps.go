package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/storage"
	"atlas-runtime-go/internal/workflowexec"
)

type workflowPromptStep struct {
	ID     string
	Title  string
	Kind   string
	Prompt string
}

type workflowApprovalState struct {
	ID            string         `json:"id"`
	WorkflowID    string         `json:"workflowID"`
	WorkflowRunID string         `json:"workflowRunID"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	RequestedAt   string         `json:"requestedAt"`
	ResolvedAt    string         `json:"resolvedAt,omitempty"`
	TrustScope    map[string]any `json:"trustScope"`
	NextStepIndex int            `json:"nextStepIndex"`
	PendingStepID string         `json:"pendingStepID,omitempty"`
	PendingTitle  string         `json:"pendingTitle,omitempty"`
}

func (m *Module) executePreparedWorkflow(ctx context.Context, prepared workflowexec.PreparedRun) (status, summary, errorMessage string, stepRuns []map[string]any) {
	steps := promptStepDefinitions(prepared.Definition)
	if requiresStepByStepApproval(prepared.Definition, steps) {
		record, err := m.pauseWorkflowForApproval(prepared, 0, workflowexec.InitialStepRuns(prepared.Definition), "")
		if err != nil {
			return "failed", "", err.Error(), workflowexec.InitialStepRuns(prepared.Definition)
		}
		if persisted, ok := record["stepRuns"].([]map[string]any); ok {
			stepRuns = persisted
		} else {
			stepRuns = workflowexec.InitialStepRuns(prepared.Definition)
		}
		return "waiting_for_approval", summaryFromRecord(record), "", stepRuns
	}
	if len(steps) == 0 {
		req := chat.MessageRequest{
			Message:        prepared.Prompt,
			ConversationID: prepared.ConversationID,
			ToolPolicy:     toolPolicyForDefinition(prepared.Definition),
		}
		resp, execErr := m.agent.HandleMessage(ctx, req)
		if execErr != nil {
			return "failed", "", execErr.Error(), workflowexec.InitialStepRuns(prepared.Definition)
		}
		if resp.Response.Status == "error" {
			return "failed", "", strings.TrimSpace(resp.Response.AssistantMessage), workflowexec.InitialStepRuns(prepared.Definition)
		}
		return "completed", resp.Response.AssistantMessage, "", workflowexec.InitialStepRuns(prepared.Definition)
	}

	stepRuns = workflowexec.InitialStepRuns(prepared.Definition)
	summaries := make([]string, 0, len(steps))
	policy := toolPolicyForDefinition(prepared.Definition)
	objective := strings.TrimSpace(workflowexec.ComposePrompt(prepared.Definition, nil, ""))
	for idx, step := range steps {
		stepIndex := stepRunIndex(stepRuns, step.ID)
		if stepIndex < 0 {
			continue
		}
		markStepRun(stepRuns[stepIndex], "running", "", "")
		_ = m.persistStepRuns(prepared.RunID, stepRuns)

		message := composeStepPrompt(objective, step, idx+1, len(steps))
		req := chat.MessageRequest{Message: message, ConversationID: prepared.ConversationID, ToolPolicy: policy}
		resp, execErr := m.agent.HandleMessage(ctx, req)
		if execErr != nil {
			markStepRun(stepRuns[stepIndex], "failed", "", execErr.Error())
			_ = m.persistStepRuns(prepared.RunID, stepRuns)
			return "failed", strings.Join(summaries, "\n\n"), execErr.Error(), stepRuns
		}
		if resp.Response.Status == "error" {
			errMsg := strings.TrimSpace(resp.Response.AssistantMessage)
			if errMsg == "" {
				errMsg = strings.TrimSpace(resp.Response.ErrorMessage)
			}
			markStepRun(stepRuns[stepIndex], "failed", "", errMsg)
			_ = m.persistStepRuns(prepared.RunID, stepRuns)
			return "failed", strings.Join(summaries, "\n\n"), errMsg, stepRuns
		}
		out := strings.TrimSpace(resp.Response.AssistantMessage)
		markStepRun(stepRuns[stepIndex], "completed", out, "")
		if out != "" {
			summaries = append(summaries, fmt.Sprintf("%s: %s", step.Title, out))
		}
		_ = m.persistStepRuns(prepared.RunID, stepRuns)
	}
	return "completed", strings.Join(summaries, "\n\n"), "", stepRuns
}

func (m *Module) pauseWorkflowForApproval(prepared workflowexec.PreparedRun, stepIndex int, stepRuns []map[string]any, summary string) (map[string]any, error) {
	row, err := m.loadWorkflowRun(prepared.RunID)
	if err != nil {
		return nil, err
	}
	steps := promptStepDefinitions(prepared.Definition)
	approval, err := approvalStateForStep(prepared.Definition, prepared.RunID, prepared.WorkflowID, steps, stepIndex)
	if err != nil {
		return nil, err
	}
	if runIndex := stepRunIndex(stepRuns, approval.PendingStepID); runIndex >= 0 {
		markStepRun(stepRuns[runIndex], "waiting_for_approval", "", "")
	}
	row.Status = "waiting_for_approval"
	row.Outcome = strPtrOrNil("waiting_for_approval")
	row.ErrorMessage = nil
	row.FinishedAt = nil
	row.DurationMs = 0
	row.ApprovalJSON = mustJSONStringPtr(approval)
	row.StepRunsJSON = mustJSONString(stepRuns, "[]")
	row.AssistantSummary = strPtrOrNil(summary)
	if err := m.store.SaveWorkflowRun(row); err != nil {
		return nil, err
	}
	return runRecordFromRow(row), nil
}

func (m *Module) resumeWorkflowAfterApproval(ctx context.Context, runID string) (map[string]any, error) {
	row, err := m.loadWorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	if row.Status != "waiting_for_approval" {
		return nil, fmt.Errorf("workflow run not waiting for approval: %s", runID)
	}
	def, ok, err := m.getDefinition(row.WorkflowID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", row.WorkflowID)
	}
	steps := promptStepDefinitions(def)
	approval, err := parseWorkflowApproval(row.ApprovalJSON)
	if err != nil {
		return nil, err
	}
	if approval.NextStepIndex < 0 || approval.NextStepIndex >= len(steps) {
		return nil, fmt.Errorf("workflow run %s has invalid approval checkpoint", runID)
	}
	stepRuns := parseWorkflowStepRuns(row.StepRunsJSON, def)
	stepIndex := approval.NextStepIndex
	runIndex := stepRunIndex(stepRuns, steps[stepIndex].ID)
	if runIndex < 0 {
		return nil, fmt.Errorf("workflow run %s is missing step state for %s", runID, steps[stepIndex].ID)
	}

	row.Status = "running"
	row.Outcome = nil
	row.ErrorMessage = nil
	row.ApprovalJSON = nil
	markStepRun(stepRuns[runIndex], "running", "", "")
	row.StepRunsJSON = mustJSONString(stepRuns, "[]")
	row.AssistantSummary = strPtrOrNil(summaryFromRow(row))
	if err := m.store.SaveWorkflowRun(row); err != nil {
		return nil, err
	}

	objective := strings.TrimSpace(workflowexec.ComposePrompt(def, parseWorkflowInputValues(row.InputValuesJSON), ""))
	message := composeStepPrompt(objective, steps[stepIndex], stepIndex+1, len(steps))
	req := chat.MessageRequest{
		Message:        message,
		ConversationID: stringPtrValue(row.ConversationID),
		ToolPolicy:     toolPolicyForDefinition(def),
	}
	resp, execErr := m.agent.HandleMessage(ctx, req)
	if execErr != nil {
		return m.failWorkflowRun(row, stepRuns, runIndex, execErr.Error())
	}
	if resp.Response.Status == "error" {
		errMsg := strings.TrimSpace(resp.Response.AssistantMessage)
		if errMsg == "" {
			errMsg = strings.TrimSpace(resp.Response.ErrorMessage)
		}
		return m.failWorkflowRun(row, stepRuns, runIndex, errMsg)
	}

	out := strings.TrimSpace(resp.Response.AssistantMessage)
	markStepRun(stepRuns[runIndex], "completed", out, "")
	summary := appendWorkflowSummary(summaryFromRow(row), steps[stepIndex].Title, out)
	row.StepRunsJSON = mustJSONString(stepRuns, "[]")
	row.AssistantSummary = strPtrOrNil(summary)

	if stepIndex+1 < len(steps) {
		prepared := workflowexec.PreparedRun{
			RunID:          row.RunID,
			WorkflowID:     row.WorkflowID,
			ConversationID: stringPtrValue(row.ConversationID),
			Definition:     def,
		}
		return m.pauseWorkflowForApproval(prepared, stepIndex+1, stepRuns, summary)
	}

	return m.completeWorkflowRun(row, stepRuns, "completed", "success", summary, "")
}

func (m *Module) denyWorkflowRunRecord(runID string) (map[string]any, error) {
	row, err := m.loadWorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	if row.Status != "waiting_for_approval" {
		return nil, fmt.Errorf("workflow run not waiting for approval: %s", runID)
	}
	def, ok, err := m.getDefinition(row.WorkflowID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", row.WorkflowID)
	}
	stepRuns := parseWorkflowStepRuns(row.StepRunsJSON, def)
	if approval, err := parseWorkflowApproval(row.ApprovalJSON); err == nil {
		if runIndex := stepRunIndex(stepRuns, approval.PendingStepID); runIndex >= 0 {
			markStepRun(stepRuns[runIndex], "skipped", "", "Denied before execution.")
		}
		row.ApprovalJSON = mustJSONStringPtr(workflowApprovalState{
			ID:            approval.ID,
			WorkflowID:    approval.WorkflowID,
			WorkflowRunID: approval.WorkflowRunID,
			Status:        "denied",
			Reason:        approval.Reason,
			RequestedAt:   approval.RequestedAt,
			ResolvedAt:    time.Now().UTC().Format(time.RFC3339),
			TrustScope:    approval.TrustScope,
			NextStepIndex: approval.NextStepIndex,
			PendingStepID: approval.PendingStepID,
			PendingTitle:  approval.PendingTitle,
		})
	} else {
		row.ApprovalJSON = nil
	}
	return m.completeWorkflowRun(row, stepRuns, "denied", "denied", summaryFromRow(row), "Workflow run denied.")
}

func (m *Module) failWorkflowRun(row storage.WorkflowRunRow, stepRuns []map[string]any, runIndex int, errMsg string) (map[string]any, error) {
	markStepRun(stepRuns[runIndex], "failed", "", errMsg)
	return m.completeWorkflowRun(row, stepRuns, "failed", "failed", summaryFromRow(row), errMsg)
}

func (m *Module) completeWorkflowRun(row storage.WorkflowRunRow, stepRuns []map[string]any, status, outcome, summary, errMsg string) (map[string]any, error) {
	finishedAt := time.Now().UTC()
	row.Status = status
	row.Outcome = strPtrOrNil(outcome)
	row.StepRunsJSON = mustJSONString(stepRuns, "[]")
	row.AssistantSummary = strPtrOrNil(summary)
	row.ErrorMessage = strPtrOrNil(errMsg)
	row.FinishedAt = strPtrOrNil(finishedAt.Format(time.RFC3339))
	row.DurationMs = workflowDurationMs(row.StartedAt, finishedAt)
	if status != "denied" {
		row.ApprovalJSON = nil
	}
	if err := m.store.SaveWorkflowRun(row); err != nil {
		return nil, err
	}
	return runRecordFromRow(row), nil
}

func (m *Module) loadWorkflowRun(runID string) (storage.WorkflowRunRow, error) {
	row, err := m.store.GetWorkflowRun(runID)
	if err != nil {
		return storage.WorkflowRunRow{}, err
	}
	if row == nil {
		return storage.WorkflowRunRow{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	return *row, nil
}

func requiresStepByStepApproval(def map[string]any, steps []workflowPromptStep) bool {
	return approvalModeForDefinition(def) == "step_by_step" && len(steps) > 0
}

func approvalModeForDefinition(def map[string]any) string {
	mode := strings.ToLower(strings.TrimSpace(stringValue(def, "approvalMode")))
	if mode == "" {
		return "workflow_boundary"
	}
	return mode
}

func approvalStateForStep(def map[string]any, runID, workflowID string, steps []workflowPromptStep, stepIndex int) (workflowApprovalState, error) {
	if stepIndex < 0 || stepIndex >= len(steps) {
		return workflowApprovalState{}, fmt.Errorf("invalid step index %d", stepIndex)
	}
	requestedAt := time.Now().UTC().Format(time.RFC3339)
	trustScope, _ := def["trustScope"].(map[string]any)
	step := steps[stepIndex]
	return workflowApprovalState{
		ID:            runID + "-approval-" + step.ID,
		WorkflowID:    workflowID,
		WorkflowRunID: runID,
		Status:        "pending",
		Reason:        fmt.Sprintf("Approve step %d of %d: %s", stepIndex+1, len(steps), step.Title),
		RequestedAt:   requestedAt,
		TrustScope:    trustScope,
		NextStepIndex: stepIndex,
		PendingStepID: step.ID,
		PendingTitle:  step.Title,
	}, nil
}

func parseWorkflowApproval(raw *string) (workflowApprovalState, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return workflowApprovalState{}, fmt.Errorf("workflow approval state missing")
	}
	var approval workflowApprovalState
	if err := json.Unmarshal([]byte(*raw), &approval); err != nil {
		return workflowApprovalState{}, fmt.Errorf("decode workflow approval state: %w", err)
	}
	return approval, nil
}

func parseWorkflowStepRuns(raw string, def map[string]any) []map[string]any {
	var stepRuns []map[string]any
	if json.Unmarshal([]byte(raw), &stepRuns) == nil && stepRuns != nil {
		return stepRuns
	}
	return workflowexec.InitialStepRuns(def)
}

func parseWorkflowInputValues(raw string) map[string]string {
	var inputValues map[string]string
	if json.Unmarshal([]byte(raw), &inputValues) == nil && inputValues != nil {
		return inputValues
	}
	return nil
}

func summaryFromRow(row storage.WorkflowRunRow) string {
	if row.AssistantSummary == nil {
		return ""
	}
	return strings.TrimSpace(*row.AssistantSummary)
}

func summaryFromRecord(record map[string]any) string {
	value, _ := record["assistantSummary"].(string)
	return strings.TrimSpace(value)
}

func appendWorkflowSummary(existing, title, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return existing
	}
	entry := fmt.Sprintf("%s: %s", strings.TrimSpace(title), output)
	if strings.TrimSpace(existing) == "" {
		return entry
	}
	return existing + "\n\n" + entry
}

func workflowDurationMs(startedAt string, finishedAt time.Time) int64 {
	started, err := time.Parse(time.RFC3339, strings.TrimSpace(startedAt))
	if err != nil {
		return 0
	}
	return finishedAt.Sub(started).Milliseconds()
}

func mustJSONString(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
}

func mustJSONStringPtr(value any) *string {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	text := string(data)
	return &text
}

func strPtrOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func promptStepDefinitions(def map[string]any) []workflowPromptStep {
	raw, ok := def["steps"].([]any)
	if !ok {
		return nil
	}
	steps := make([]workflowPromptStep, 0, len(raw))
	for idx, item := range raw {
		step, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind := stringValue(step, "kind")
		if kind == "" {
			kind = "prompt"
		}
		if kind != "prompt" {
			continue
		}
		prompt := strings.TrimSpace(stringValue(step, "prompt"))
		if prompt == "" {
			continue
		}
		id := stringValue(step, "id")
		if id == "" {
			id = fmt.Sprintf("step-%d", idx+1)
		}
		title := stringValue(step, "title")
		if title == "" {
			title = fmt.Sprintf("Step %d", idx+1)
		}
		steps = append(steps, workflowPromptStep{ID: id, Title: title, Kind: kind, Prompt: prompt})
	}
	return steps
}

func composeStepPrompt(objective string, step workflowPromptStep, index, total int) string {
	parts := []string{
		fmt.Sprintf("Workflow step %d of %d: %s", index, total, step.Title),
		step.Prompt,
	}
	if strings.TrimSpace(objective) != "" {
		parts = append(parts, "Workflow context:\n"+strings.TrimSpace(objective))
	}
	return strings.Join(parts, "\n\n")
}

func stepRunIndex(stepRuns []map[string]any, stepID string) int {
	for i, run := range stepRuns {
		if stringValue(run, "stepID") == stepID {
			return i
		}
	}
	return -1
}

func markStepRun(run map[string]any, status, output, errorMessage string) {
	now := time.Now().UTC().Format(time.RFC3339)
	run["status"] = status
	switch status {
	case "running":
		run["startedAt"] = now
	case "completed", "failed", "skipped":
		run["finishedAt"] = now
	}
	if strings.TrimSpace(output) != "" {
		run["output"] = strings.TrimSpace(output)
	}
	if strings.TrimSpace(errorMessage) != "" {
		run["errorMessage"] = strings.TrimSpace(errorMessage)
	}
}

func (m *Module) persistStepRuns(runID string, stepRuns []map[string]any) error {
	if m.store == nil {
		return nil
	}
	data, err := json.Marshal(stepRuns)
	if err != nil {
		return err
	}
	return m.store.UpdateWorkflowRunStepRuns(runID, string(data))
}

func toolPolicyForDefinition(def map[string]any) *agent.ToolPolicy {
	scope, _ := def["trustScope"].(map[string]any)
	policy := &agent.ToolPolicy{
		Enabled:             true,
		AllowsSensitiveRead: boolValue(scope, "allowsSensitiveRead", false),
		AllowsLiveWrite:     boolValue(scope, "allowsLiveWrite", false),
		ApprovedRootPaths:   stringListValue(scope, "approvedRootPaths"),
		AllowedToolPrefixes: allowedToolPrefixes(stringListValue(scope, "allowedApps")),
	}
	return policy
}

func stringListValue(obj map[string]any, key string) []string {
	if obj == nil {
		return nil
	}
	switch raw := obj[key].(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func allowedToolPrefixes(apps []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(prefix string) {
		if prefix == "" || seen[prefix] {
			return
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	for _, app := range apps {
		switch strings.ToLower(strings.TrimSpace(app)) {
		case "filesystem", "files", "file system", "fs":
			add("fs.")
		case "calendar":
			add("applescript.calendar_")
		case "reminders":
			add("applescript.reminders_")
		case "mail":
			add("applescript.mail_")
		case "contacts":
			add("applescript.contacts_")
		case "notes":
			add("applescript.notes_")
		case "safari":
			add("applescript.safari_")
		case "music":
			add("applescript.music_")
		case "web", "websearch", "web search":
			add("web.")
			add("websearch.")
		case "browser":
			add("browser.")
		case "weather":
			add("weather.")
		case "finance":
			add("finance.")
		case "memory":
			add("memory.")
		case "terminal", "shell":
			add("terminal.")
		case "vault":
			add("vault.")
		case "image", "creative":
			add("image.")
		case "voice":
			add("voice.")
		case "communications", "communication", "chat", "chat bridge", "bridge":
			add("communication.")
		}
	}
	return out
}
