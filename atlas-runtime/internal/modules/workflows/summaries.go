package workflows

import "net/http"

type WorkflowSummary struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	IsEnabled     bool    `json:"isEnabled"`
	StepCount     int     `json:"stepCount"`
	Health        string  `json:"health"`
	LastRunAt     *string `json:"lastRunAt,omitempty"`
	LastRunStatus *string `json:"lastRunStatus,omitempty"`
	LastRunError  *string `json:"lastRunError,omitempty"`
}

func (m *Module) listWorkflowSummaries(w http.ResponseWriter, _ *http.Request) {
	summaries, err := m.workflowSummaries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build workflow summaries: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (m *Module) workflowSummaries() ([]WorkflowSummary, error) {
	defs, err := m.listDefinitions()
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowSummary, 0, len(defs))
	for _, def := range defs {
		id := stringValue(def, "id")
		runs, err := m.listRuns(id, 1)
		if err != nil {
			return nil, err
		}
		health := "never_run"
		var lastRunAt, lastRunStatus, lastRunError *string
		if len(runs) > 0 {
			if started, _ := runs[0]["startedAt"].(string); started != "" {
				lastRunAt = &started
			}
			if status, _ := runs[0]["status"].(string); status != "" {
				lastRunStatus = &status
				health = status
			}
			if errMsg, _ := runs[0]["errorMessage"].(string); errMsg != "" {
				lastRunError = &errMsg
				health = "failed"
			}
		}
		out = append(out, WorkflowSummary{
			ID:            id,
			Name:          stringValue(def, "name"),
			Description:   stringValue(def, "description"),
			IsEnabled:     boolValue(def, "isEnabled", true),
			StepCount:     len(stepList(def)),
			Health:        health,
			LastRunAt:     lastRunAt,
			LastRunStatus: lastRunStatus,
			LastRunError:  lastRunError,
		})
	}
	return out, nil
}

func stepList(def map[string]any) []any {
	if steps, ok := def["steps"].([]any); ok {
		return steps
	}
	return nil
}
