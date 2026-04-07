package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/skills"
)

type workflowRefArgs struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Module) registerAgentActions() {
	if m.skills == nil {
		return
	}
	for _, entry := range []struct {
		name        string
		description string
		properties  map[string]skills.ToolParam
		required    []string
		perm        string
		class       skills.ActionClass
		fn          func(context.Context, json.RawMessage) (skills.ToolResult, error)
	}{
		{
			name:        "workflow.create",
			description: "Create a reusable Atlas workflow.",
			properties: map[string]skills.ToolParam{
				"name":           {Description: "Short workflow name.", Type: "string"},
				"description":    {Description: "Workflow description.", Type: "string"},
				"promptTemplate": {Description: "Reusable prompt or process instructions.", Type: "string"},
				"enabled":        {Description: "Whether the workflow starts enabled. Defaults to true.", Type: "boolean"},
			},
			required: []string{"name", "promptTemplate"}, perm: "execute", class: skills.ActionClassLocalWrite, fn: m.agentCreate,
		},
		{name: "workflow.update", description: "Update an Atlas workflow by ID or exact name.", properties: workflowUpdateProperties(), perm: "execute", class: skills.ActionClassLocalWrite, fn: m.agentUpdate},
		{name: "workflow.delete", description: "Delete an Atlas workflow by ID or exact name.", properties: workflowRefProperties(), perm: "execute", class: skills.ActionClassLocalWrite, fn: m.agentDelete},
		{name: "workflow.list", description: "List Atlas workflows and their current state.", properties: map[string]skills.ToolParam{}, perm: "read", class: skills.ActionClassRead, fn: m.agentList},
		{name: "workflow.get", description: "Get one Atlas workflow by ID or name.", properties: workflowRefProperties(), perm: "read", class: skills.ActionClassRead, fn: m.agentGet},
		{name: "workflow.run", description: "Run an Atlas workflow immediately by ID or exact name.", properties: workflowRunProperties(), perm: "execute", class: skills.ActionClassLocalWrite, fn: m.agentRun},
		{name: "workflow.run_history", description: "Show recent run history for an Atlas workflow.", properties: workflowHistoryProperties(), perm: "read", class: skills.ActionClassRead, fn: m.agentRunHistory},
		{name: "workflow.duplicate", description: "Duplicate an Atlas workflow under a new name.", properties: workflowDuplicateProperties(), required: []string{"newName"}, perm: "execute", class: skills.ActionClassLocalWrite, fn: m.agentDuplicate},
		{name: "workflow.validate", description: "Validate a workflow definition shape.", properties: workflowRefProperties(), perm: "read", class: skills.ActionClassRead, fn: m.agentValidate},
		{name: "workflow.explain", description: "Explain what a workflow does and how it is constrained.", properties: workflowRefProperties(), perm: "read", class: skills.ActionClassRead, fn: m.agentExplain},
	} {
		m.skills.RegisterExternal(skills.SkillEntry{
			Def:       skills.ToolDef{Name: entry.name, Description: entry.description, Properties: entry.properties, Required: entry.required},
			PermLevel: entry.perm, ActionClass: entry.class, FnResult: entry.fn,
		})
	}
}

func workflowRefProperties() map[string]skills.ToolParam {
	return map[string]skills.ToolParam{
		"id":   {Description: "Workflow ID. Preferred for exact targeting.", Type: "string"},
		"name": {Description: "Workflow name when ID is not known.", Type: "string"},
	}
}

func workflowUpdateProperties() map[string]skills.ToolParam {
	props := workflowRefProperties()
	props["newName"] = skills.ToolParam{Description: "New workflow name.", Type: "string"}
	props["description"] = skills.ToolParam{Description: "New workflow description.", Type: "string"}
	props["promptTemplate"] = skills.ToolParam{Description: "New prompt template.", Type: "string"}
	props["enabled"] = skills.ToolParam{Description: "Enable or disable the workflow.", Type: "boolean"}
	return props
}

func workflowRunProperties() map[string]skills.ToolParam {
	props := workflowRefProperties()
	props["inputValuesJSON"] = skills.ToolParam{Description: "Optional JSON object of workflow input values.", Type: "string"}
	return props
}

func workflowHistoryProperties() map[string]skills.ToolParam {
	props := workflowRefProperties()
	props["limit"] = skills.ToolParam{Description: "Maximum run records to return. Defaults to 10.", Type: "integer"}
	return props
}

func workflowDuplicateProperties() map[string]skills.ToolParam {
	props := workflowRefProperties()
	props["newName"] = skills.ToolParam{Description: "Name for the duplicate workflow.", Type: "string"}
	return props
}

func (m *Module) agentCreate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		PromptTemplate string `json:"promptTemplate"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.PromptTemplate) == "" {
		return skills.ToolResult{}, fmt.Errorf("name and promptTemplate are required")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	def, err := m.createDefinition(map[string]any{
		"name":           strings.TrimSpace(p.Name),
		"description":    strings.TrimSpace(p.Description),
		"promptTemplate": strings.TrimSpace(p.PromptTemplate),
		"isEnabled":      enabled,
	})
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to create workflow: %w", err)
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q created.", stringValue(def, "name")), map[string]any{"workflow": def}), nil
}

func (m *Module) agentUpdate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID             string  `json:"id"`
		Name           string  `json:"name"`
		NewName        string  `json:"newName"`
		Description    *string `json:"description"`
		PromptTemplate *string `json:"promptTemplate"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(workflowRefArgs{ID: p.ID, Name: p.Name}, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	patch := map[string]any{}
	if strings.TrimSpace(p.NewName) != "" {
		patch["name"] = strings.TrimSpace(p.NewName)
	}
	if p.Description != nil {
		patch["description"] = strings.TrimSpace(*p.Description)
	}
	if p.PromptTemplate != nil {
		patch["promptTemplate"] = strings.TrimSpace(*p.PromptTemplate)
	}
	if p.Enabled != nil {
		patch["isEnabled"] = *p.Enabled
	}
	updated, _, err := m.updateDefinition(stringValue(item, "id"), patch)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update workflow: %w", err)
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q updated.", stringValue(updated, "name")), map[string]any{"workflow": updated}), nil
}

func (m *Module) agentDelete(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p workflowRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(p, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	id := stringValue(item, "id")
	found, err := m.deleteDefinition(id)
	if err != nil {
		return skills.ToolResult{}, err
	}
	if !found {
		return skills.ToolResult{}, fmt.Errorf("workflow %q not found", id)
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q deleted.", stringValue(item, "name")), map[string]any{"id": id}), nil
}

func (m *Module) agentList(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	items, err := m.listDefinitions()
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Found %d workflows.", len(items)), map[string]any{"workflows": items}), nil
}

func (m *Module) agentGet(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p workflowRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(p, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q loaded.", stringValue(item, "name")), map[string]any{"workflow": item}), nil
}

func (m *Module) agentRun(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		InputValuesJSON string `json:"inputValuesJSON"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(workflowRefArgs{ID: p.ID, Name: p.Name}, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	inputs := map[string]string{}
	if strings.TrimSpace(p.InputValuesJSON) != "" {
		if err := json.Unmarshal([]byte(p.InputValuesJSON), &inputs); err != nil {
			return skills.ToolResult{}, fmt.Errorf("inputValuesJSON must be a JSON object: %w", err)
		}
	}
	record, err := m.runWorkflowSync(ctx, stringValue(item, "id"), inputs, "agent")
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q ran successfully.", stringValue(item, "name")), map[string]any{"run": record}), nil
}

func (m *Module) agentRunHistory(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(workflowRefArgs{ID: p.ID, Name: p.Name}, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}
	runs, err := m.listRuns(stringValue(item, "id"), p.Limit)
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Found %d run records for %q.", len(runs), stringValue(item, "name")), map[string]any{"workflow": item, "runs": runs}), nil
}

func (m *Module) agentDuplicate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		NewName string `json:"newName"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(p.NewName) == "" {
		return skills.ToolResult{}, fmt.Errorf("newName is required")
	}
	item, err := m.resolveWorkflow(workflowRefArgs{ID: p.ID, Name: p.Name}, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	duplicate := map[string]any{}
	for k, v := range item {
		duplicate[k] = v
	}
	delete(duplicate, "id")
	duplicate["name"] = strings.TrimSpace(p.NewName)
	duplicate["isEnabled"] = false
	duplicate["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	created, err := m.createDefinition(duplicate)
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Workflow duplicated as %q and left disabled for review.", stringValue(created, "name")), map[string]any{"workflow": created}), nil
}

func (m *Module) agentValidate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p workflowRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(p, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	warnings := workflowWarnings(item)
	result := skills.OKResult(fmt.Sprintf("Workflow %q is valid with %d warning(s).", stringValue(item, "name"), len(warnings)), map[string]any{"workflow": item, "warnings": warnings})
	result.Warnings = warnings
	return result, nil
}

func (m *Module) agentExplain(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p workflowRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveWorkflow(p, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Workflow %q runs a reusable process with %d configured step(s).", stringValue(item, "name"), len(stepList(item))), map[string]any{"workflow": item, "warnings": workflowWarnings(item)}), nil
}

func (m *Module) resolveWorkflow(ref workflowRefArgs, allowFuzzy bool) (map[string]any, error) {
	id := strings.TrimSpace(ref.ID)
	name := strings.TrimSpace(ref.Name)
	if id == "" && name == "" {
		return nil, fmt.Errorf("id or name is required")
	}
	items, err := m.listDefinitions()
	if err != nil {
		return nil, err
	}
	if id != "" {
		for _, item := range items {
			if stringValue(item, "id") == id {
				return item, nil
			}
		}
		return nil, fmt.Errorf("workflow %q not found", id)
	}
	var exact []map[string]any
	for _, item := range items {
		if strings.EqualFold(stringValue(item, "name"), name) {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return nil, fmt.Errorf("multiple workflows named %q; use the workflow ID", name)
	}
	if allowFuzzy {
		needle := strings.ToLower(name)
		var matches []map[string]any
		for _, item := range items {
			if strings.Contains(strings.ToLower(stringValue(item, "name")), needle) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("multiple workflows match %q; use the workflow ID", name)
		}
	}
	return nil, fmt.Errorf("workflow named %q not found", name)
}

func workflowWarnings(def map[string]any) []string {
	var warnings []string
	if strings.TrimSpace(stringValue(def, "promptTemplate")) == "" && len(stepList(def)) == 0 {
		warnings = append(warnings, "workflow has no prompt template or steps")
	}
	for _, raw := range stepList(def) {
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(stringValue(step, "kind"))
		if kind != "" && kind != "prompt" {
			warnings = append(warnings, fmt.Sprintf("step %q uses unsupported kind %q in the current runner", stringValue(step, "title"), kind))
		}
	}
	if !boolValue(def, "isEnabled", true) {
		warnings = append(warnings, "workflow is disabled")
	}
	return warnings
}
