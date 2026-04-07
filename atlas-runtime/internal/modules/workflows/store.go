package workflows

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/storage"
)

func (m *Module) importLegacyDefinitions() error {
	if m.store == nil {
		return nil
	}
	existing, err := m.store.ListWorkflows()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	for _, raw := range features.ListWorkflowDefinitions(m.supportDir) {
		var def map[string]any
		if err := json.Unmarshal(raw, &def); err != nil {
			return fmt.Errorf("parse legacy workflow: %w", err)
		}
		if _, err := m.saveDefinition(def, false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) listDefinitions() ([]map[string]any, error) {
	if m.store == nil {
		return rawMessagesToMaps(features.ListWorkflowDefinitions(m.supportDir))
	}
	rows, err := m.store.ListWorkflows()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		if err := m.importLegacyDefinitions(); err != nil {
			return nil, err
		}
		rows, err = m.store.ListWorkflows()
		if err != nil {
			return nil, err
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		def, err := definitionFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}

func (m *Module) getDefinition(id string) (map[string]any, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}
	if m.store == nil {
		raw := features.GetWorkflowDefinition(m.supportDir, id)
		if raw == nil {
			return nil, false, nil
		}
		var def map[string]any
		if err := json.Unmarshal(raw, &def); err != nil {
			return nil, false, err
		}
		return normalizeDefinition(def, false), true, nil
	}
	row, err := m.store.GetWorkflow(id)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		raw := features.GetWorkflowDefinition(m.supportDir, id)
		if raw != nil {
			var def map[string]any
			if err := json.Unmarshal(raw, &def); err != nil {
				return nil, false, err
			}
			saved, err := m.saveDefinition(def, false)
			if err != nil {
				return nil, false, err
			}
			return saved, true, nil
		}
		return nil, false, nil
	}
	def, err := definitionFromRow(*row)
	if err != nil {
		return nil, false, err
	}
	return def, true, nil
}

func (m *Module) createDefinition(def map[string]any) (map[string]any, error) {
	def = normalizeDefinition(def, true)
	id, _ := def["id"].(string)
	if existing, ok, err := m.getDefinition(id); err != nil {
		return nil, err
	} else if ok && existing != nil {
		return nil, fmt.Errorf("workflow id already exists: %s", id)
	}
	return m.saveDefinition(def, false)
}

func (m *Module) updateDefinition(id string, def map[string]any) (map[string]any, bool, error) {
	existing, ok, err := m.getDefinition(id)
	if err != nil || !ok {
		return nil, ok, err
	}
	updated := mergeDefinitionPatch(existing, def)
	updated["id"] = id
	updated["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	out, err := m.saveDefinition(updated, true)
	return out, true, err
}

func (m *Module) deleteDefinition(id string) (bool, error) {
	if m.store == nil {
		return features.DeleteWorkflowDefinition(m.supportDir, id)
	}
	return m.store.DeleteWorkflow(id)
}

func (m *Module) saveDefinition(def map[string]any, preserveCreatedAt bool) (map[string]any, error) {
	def = normalizeDefinition(def, !preserveCreatedAt)
	id, _ := def["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("workflow id is required")
	}
	if m.store == nil {
		if preserveCreatedAt {
			return features.UpdateWorkflowDefinition(m.supportDir, id, def)
		}
		return features.AppendWorkflowDefinition(m.supportDir, def)
	}
	row, err := rowFromDefinition(def)
	if err != nil {
		return nil, err
	}
	if preserveCreatedAt {
		if existing, ok, err := m.getDefinition(id); err != nil {
			return nil, err
		} else if ok {
			if created, _ := existing["createdAt"].(string); strings.TrimSpace(created) != "" {
				def["createdAt"] = created
				row.CreatedAt = created
			}
		}
	}
	if err := m.store.SaveWorkflow(row); err != nil {
		return nil, err
	}
	return def, nil
}

func (m *Module) listRuns(workflowID string, limit int) ([]map[string]any, error) {
	if m.store == nil {
		return rawMessagesToMaps(features.ListWorkflowRuns(m.supportDir, workflowID))
	}
	rows, err := m.store.ListWorkflowRuns(workflowID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, runRecordFromRow(row))
	}
	return out, nil
}

func (m *Module) updateRunStatus(runID, status string) (map[string]any, error) {
	if m.store == nil {
		return features.UpdateWorkflowRunStatus(m.supportDir, runID, status)
	}
	row, err := m.store.UpdateWorkflowRunStatus(runID, status)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("workflow run not found: %s", runID)
	}
	return runRecordFromRow(*row), nil
}

func rawMessagesToMaps(records []json.RawMessage) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(records))
	for _, raw := range records {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

func definitionFromRow(row storage.WorkflowRow) (map[string]any, error) {
	var def map[string]any
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &def); err != nil {
		return nil, err
	}
	def = normalizeDefinition(def, false)
	def["id"] = row.ID
	def["name"] = row.Name
	def["isEnabled"] = row.IsEnabled
	if strings.TrimSpace(row.CreatedAt) != "" {
		def["createdAt"] = row.CreatedAt
	}
	if strings.TrimSpace(row.UpdatedAt) != "" {
		def["updatedAt"] = row.UpdatedAt
	}
	return def, nil
}

func rowFromDefinition(def map[string]any) (storage.WorkflowRow, error) {
	def = normalizeDefinition(def, false)
	data, err := json.Marshal(def)
	if err != nil {
		return storage.WorkflowRow{}, err
	}
	return storage.WorkflowRow{
		ID:             stringValue(def, "id"),
		Name:           stringValue(def, "name"),
		DefinitionJSON: string(data),
		IsEnabled:      boolValue(def, "isEnabled", true),
		CreatedAt:      stringValue(def, "createdAt"),
		UpdatedAt:      stringValue(def, "updatedAt"),
	}, nil
}

func normalizeDefinition(def map[string]any, assignID bool) map[string]any {
	if def == nil {
		def = map[string]any{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := strings.TrimSpace(stringValue(def, "id"))
	if id == "" && assignID {
		id = newID()
	}
	if id != "" {
		def["id"] = id
	}
	if strings.TrimSpace(stringValue(def, "name")) == "" {
		def["name"] = "Untitled workflow"
	}
	if _, ok := def["description"]; !ok {
		def["description"] = ""
	}
	if _, ok := def["promptTemplate"]; !ok {
		if prompt := stringValue(def, "prompt"); prompt != "" {
			def["promptTemplate"] = prompt
		} else {
			def["promptTemplate"] = ""
		}
	}
	if _, ok := def["tags"]; !ok {
		def["tags"] = []string{}
	}
	if _, ok := def["steps"]; !ok {
		def["steps"] = []map[string]any{}
	}
	if _, ok := def["trustScope"]; !ok {
		def["trustScope"] = map[string]any{
			"approvedRootPaths":   []string{},
			"allowedApps":         []string{},
			"allowsSensitiveRead": false,
			"allowsLiveWrite":     false,
		}
	}
	if _, ok := def["approvalMode"]; !ok {
		def["approvalMode"] = "workflow_boundary"
	}
	if _, ok := def["isEnabled"]; !ok {
		def["isEnabled"] = true
	}
	if strings.TrimSpace(stringValue(def, "createdAt")) == "" {
		def["createdAt"] = now
	}
	def["updatedAt"] = now
	return def
}

func mergeDefinitionPatch(existing, updates map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range updates {
		if k == "id" || k == "createdAt" {
			continue
		}
		out[k] = v
	}
	return normalizeDefinition(out, false)
}

func runRecordFromRow(row storage.WorkflowRunRow) map[string]any {
	record := map[string]any{}
	_ = json.Unmarshal([]byte(row.RecordJSON), &record)
	record["id"] = row.RunID
	record["workflowID"] = row.WorkflowID
	record["workflowName"] = row.WorkflowName
	record["status"] = row.Status
	record["startedAt"] = row.StartedAt
	record["triggerSource"] = row.TriggerSource
	record["durationMs"] = row.DurationMs
	if row.Outcome != nil {
		record["outcome"] = *row.Outcome
	}
	if row.AssistantSummary != nil {
		record["assistantSummary"] = *row.AssistantSummary
	}
	if row.ErrorMessage != nil {
		record["errorMessage"] = *row.ErrorMessage
	}
	if row.FinishedAt != nil {
		record["finishedAt"] = *row.FinishedAt
	}
	if row.ConversationID != nil {
		record["conversationID"] = *row.ConversationID
	}
	var inputValues map[string]string
	if json.Unmarshal([]byte(row.InputValuesJSON), &inputValues) == nil && inputValues != nil {
		record["inputValues"] = inputValues
	} else if _, ok := record["inputValues"]; !ok {
		record["inputValues"] = map[string]string{}
	}
	var stepRuns []map[string]any
	if json.Unmarshal([]byte(row.StepRunsJSON), &stepRuns) == nil && stepRuns != nil {
		record["stepRuns"] = stepRuns
	} else if _, ok := record["stepRuns"]; !ok {
		record["stepRuns"] = []map[string]any{}
	}
	return record
}

func stringValue(obj map[string]any, key string) string {
	value, _ := obj[key].(string)
	return strings.TrimSpace(value)
}

func boolValue(obj map[string]any, key string, fallback bool) bool {
	value, ok := obj[key].(bool)
	if !ok {
		return fallback
	}
	return value
}
