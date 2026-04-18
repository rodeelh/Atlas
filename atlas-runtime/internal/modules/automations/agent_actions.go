package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/skills"
)

type automationRefArgs struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Module) registerAgentActions() {
	if m.skills == nil {
		return
	}

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.create",
			Description: "Create a new Atlas automation. The automation can run a direct prompt or target an existing workflow. Prefer automation.upsert when the request might refer to an existing automation. For Telegram/WhatsApp/Slack/Discord delivery, call communication.list_channels first and pass the returned channel id as destinationID.",
			Properties: map[string]skills.ToolParam{
				"name":        {Description: "Short display name for the automation.", Type: "string"},
				"prompt":      {Description: "Task prompt Atlas should run. Optional when workflowID is provided.", Type: "string"},
				"schedule":    {Description: "Human-readable schedule such as 'daily 08:00' or 'every Monday at 9am'.", Type: "string"},
				"emoji":       {Description: "Optional emoji for display.", Type: "string"},
				"description": {Description: "Optional description.", Type: "string"},
				"enabled":     {Description: "Whether the automation starts enabled. Defaults to true.", Type: "boolean"},
				"workflowID":  {Description: "Optional workflow ID or exact workflow name for a workflow-backed automation.", Type: "string"},
				"workflowInputValuesJSON": {
					Description: "Optional JSON object of workflow input values when workflowID is provided.",
					Type:        "string",
				},
				"destinationID": {
					Description: "Optional authorized communication channel id from communication.list_channels, for example telegram:123: or whatsapp:me@s.whatsapp.net:.",
					Type:        "string",
				},
				"platform":  {Description: "Optional delivery platform when destinationID is not provided: telegram, whatsapp, slack, or discord.", Type: "string"},
				"channelID": {Description: "Optional delivery channel/chat ID when destinationID is not provided.", Type: "string"},
				"threadID":  {Description: "Optional delivery thread ID for Slack or Discord.", Type: "string"},
			},
			Required: []string{"name", "prompt", "schedule"},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.agentCreate,
	})

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.upsert",
			Description: "Create a new Atlas automation or update an existing one by ID or exact name. Supports direct prompt automations and workflow-backed automations. Prefer this when the user might already have a matching automation.",
			Properties: map[string]skills.ToolParam{
				"id":          {Description: "Automation ID when updating a known automation.", Type: "string"},
				"name":        {Description: "Automation name. Required when creating a new automation.", Type: "string"},
				"newName":     {Description: "Optional new display name for updates.", Type: "string"},
				"prompt":      {Description: "Prompt text. Required only when creating a prompt-backed automation.", Type: "string"},
				"schedule":    {Description: "Schedule string. Required when creating a new automation.", Type: "string"},
				"emoji":       {Description: "Optional emoji for display.", Type: "string"},
				"description": {Description: "Optional description.", Type: "string"},
				"enabled":     {Description: "Whether the automation is enabled.", Type: "boolean"},
				"workflowID":  {Description: "Optional workflow ID or exact workflow name for a workflow-backed automation.", Type: "string"},
				"workflowInputValuesJSON": {
					Description: "Optional JSON object of workflow input values for workflow-backed automations.",
					Type:        "string",
				},
				"destinationID": {
					Description: "Authorized communication channel id from communication.list_channels. Use clearDestination=true to clear delivery on update.",
					Type:        "string",
				},
				"platform":         {Description: "Optional delivery platform when destinationID is not provided.", Type: "string"},
				"channelID":        {Description: "Optional delivery channel/chat ID when destinationID is not provided.", Type: "string"},
				"threadID":         {Description: "Optional delivery thread ID for Slack or Discord.", Type: "string"},
				"clearTarget":      {Description: "If true, clear any workflow target and revert to prompt-only execution.", Type: "boolean"},
				"clearDestination": {Description: "If true, clear the delivery destination on update.", Type: "boolean"},
			},
			Required: []string{},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.agentUpsert,
	})

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.update",
			Description: "Update an existing Atlas automation by ID or exact name. Supports switching between prompt-only and workflow-backed execution. For delivery changes, call communication.list_channels first and pass the returned channel id as destinationID.",
			Properties: map[string]skills.ToolParam{
				"id":          {Description: "Automation ID. Preferred for exact targeting.", Type: "string"},
				"name":        {Description: "Automation name to target when ID is not known.", Type: "string"},
				"newName":     {Description: "New display name.", Type: "string"},
				"prompt":      {Description: "New prompt text.", Type: "string"},
				"schedule":    {Description: "New schedule string.", Type: "string"},
				"emoji":       {Description: "New emoji.", Type: "string"},
				"enabled":     {Description: "Enable or disable the automation.", Type: "boolean"},
				"description": {Description: "New description.", Type: "string"},
				"workflowID":  {Description: "Optional workflow ID or exact workflow name for a workflow-backed automation.", Type: "string"},
				"workflowInputValuesJSON": {
					Description: "Optional JSON object of workflow input values for workflow-backed automations.",
					Type:        "string",
				},
				"destinationID": {
					Description: "Authorized communication channel id from communication.list_channels. Use clearDestination=true to clear delivery.",
					Type:        "string",
				},
				"platform":         {Description: "Delivery platform when destinationID is not provided.", Type: "string"},
				"channelID":        {Description: "Delivery channel/chat ID when destinationID is not provided.", Type: "string"},
				"threadID":         {Description: "Optional delivery thread ID.", Type: "string"},
				"clearTarget":      {Description: "If true, clear any workflow target and revert to prompt-only execution.", Type: "boolean"},
				"clearDestination": {Description: "If true, clear the automation delivery destination.", Type: "boolean"},
			},
			Required: []string{},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.agentUpdate,
	})

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.delete",
			Description: "Delete an Atlas automation by ID or exact name.",
			Properties: map[string]skills.ToolParam{
				"id":   {Description: "Automation ID. Preferred for exact targeting.", Type: "string"},
				"name": {Description: "Automation name to target when ID is not known.", Type: "string"},
			},
			Required: []string{},
		},
		PermLevel:   "execute",
		ActionClass: skills.ActionClassLocalWrite,
		FnResult:    m.agentDelete,
	})

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.list",
			Description: "List Atlas automations and their current state.",
			Properties:  map[string]skills.ToolParam{},
			Required:    []string{},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.agentList,
	})

	m.skills.RegisterExternal(skills.SkillEntry{
		Def: skills.ToolDef{
			Name:        "automation.get",
			Description: "Get details for one Atlas automation by ID or name.",
			Properties: map[string]skills.ToolParam{
				"id":   {Description: "Automation ID.", Type: "string"},
				"name": {Description: "Automation name.", Type: "string"},
			},
			Required: []string{},
		},
		PermLevel:   "read",
		ActionClass: skills.ActionClassRead,
		FnResult:    m.agentGet,
	})

	for _, entry := range []struct {
		name        string
		description string
		fn          func(context.Context, json.RawMessage) (skills.ToolResult, error)
	}{
		{"automation.enable", "Enable an Atlas automation by ID or exact name.", m.agentEnable},
		{"automation.disable", "Disable an Atlas automation by ID or exact name.", m.agentDisable},
		{"automation.run", "Run an Atlas automation immediately by ID or exact name.", m.agentRun},
		{"automation.run_history", "Show recent run history for an Atlas automation.", m.agentRunHistory},
		{"automation.next_run", "Estimate the next run time for an Atlas automation.", m.agentNextRun},
		{"automation.duplicate", "Duplicate an Atlas automation under a new name.", m.agentDuplicate},
		{"automation.validate_schedule", "Validate an automation schedule string.", m.agentValidateSchedule},
	} {
		m.skills.RegisterExternal(skills.SkillEntry{
			Def: skills.ToolDef{
				Name:        entry.name,
				Description: entry.description,
				Properties:  automationToolProperties(entry.name),
				Required:    automationToolRequired(entry.name),
			},
			PermLevel:   actionPermLevel(entry.name),
			ActionClass: actionClass(entry.name),
			FnResult:    entry.fn,
		})
	}
}

func automationToolProperties(name string) map[string]skills.ToolParam {
	switch name {
	case "automation.validate_schedule":
		return map[string]skills.ToolParam{
			"schedule": {Description: "Schedule string to validate.", Type: "string"},
		}
	case "automation.duplicate":
		return map[string]skills.ToolParam{
			"id":      {Description: "Source automation ID.", Type: "string"},
			"name":    {Description: "Source automation name when ID is not known.", Type: "string"},
			"newName": {Description: "Name for the duplicate.", Type: "string"},
		}
	case "automation.run_history":
		return map[string]skills.ToolParam{
			"id":    {Description: "Automation ID.", Type: "string"},
			"name":  {Description: "Automation name.", Type: "string"},
			"limit": {Description: "Maximum run records to return. Defaults to 10.", Type: "integer"},
		}
	default:
		return map[string]skills.ToolParam{
			"id":   {Description: "Automation ID.", Type: "string"},
			"name": {Description: "Automation name.", Type: "string"},
		}
	}
}

func automationToolRequired(name string) []string {
	switch name {
	case "automation.validate_schedule":
		return []string{"schedule"}
	case "automation.duplicate":
		return []string{"newName"}
	default:
		return []string{}
	}
}

func actionPermLevel(name string) string {
	switch name {
	case "automation.run_history", "automation.next_run", "automation.validate_schedule":
		return "read"
	default:
		return "execute"
	}
}

func actionClass(name string) skills.ActionClass {
	switch name {
	case "automation.run_history", "automation.next_run", "automation.validate_schedule":
		return skills.ActionClassRead
	default:
		return skills.ActionClassLocalWrite
	}
}

func (m *Module) agentCreate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		Name                    string  `json:"name"`
		Prompt                  string  `json:"prompt"`
		Schedule                string  `json:"schedule"`
		Emoji                   string  `json:"emoji"`
		Description             *string `json:"description"`
		Enabled                 *bool   `json:"enabled"`
		WorkflowID              string  `json:"workflowID"`
		WorkflowInputValuesJSON string  `json:"workflowInputValuesJSON"`
		DestinationID           string  `json:"destinationID"`
		Platform                string  `json:"platform"`
		ChannelID               string  `json:"channelID"`
		ThreadID                string  `json:"threadID"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	target, workflowInputs, err := m.resolveAgentWorkflowTarget(p.WorkflowID, p.WorkflowInputValuesJSON)
	if err != nil {
		return skills.ToolResult{}, err
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Schedule) == "" {
		return skills.ToolResult{}, fmt.Errorf("name and schedule are required")
	}
	if strings.TrimSpace(p.Prompt) == "" && target == nil {
		return skills.ToolResult{}, fmt.Errorf("prompt is required when workflowID is not provided")
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	item := features.GremlinItem{
		Name:                strings.TrimSpace(p.Name),
		Prompt:              strings.TrimSpace(p.Prompt),
		ScheduleRaw:         strings.TrimSpace(p.Schedule),
		Emoji:               strings.TrimSpace(p.Emoji),
		IsEnabled:           enabled,
		SourceType:          "agent",
		CreatedAt:           time.Now().Format("2006-01-02"),
		ExecutableTarget:    target,
		WorkflowInputValues: workflowInputs,
		GremlinDescription:  p.Description,
		Tags:                []string{},
	}
	if hasAgentDestinationArgs(p.DestinationID, p.Platform, p.ChannelID, p.ThreadID) {
		dest, err := m.resolveAgentDestination(p.DestinationID, p.Platform, p.ChannelID, p.ThreadID)
		if err != nil {
			return skills.ToolResult{}, err
		}
		item.CommunicationDestination = dest
	}
	if _, err := m.createDefinition(item); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to create automation: %w", err)
	}
	created, _ := m.resolveAutomation(automationRefArgs{Name: item.Name}, false)
	return skills.OKResult(m.automationCreatedSummary(created), map[string]any{"automation": m.automationArtifact(created)}), nil
}

func (m *Module) agentUpsert(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID                      string  `json:"id"`
		Name                    string  `json:"name"`
		NewName                 string  `json:"newName"`
		Prompt                  *string `json:"prompt"`
		Schedule                *string `json:"schedule"`
		Emoji                   *string `json:"emoji"`
		Enabled                 *bool   `json:"enabled"`
		Description             *string `json:"description"`
		WorkflowID              string  `json:"workflowID"`
		WorkflowInputValuesJSON string  `json:"workflowInputValuesJSON"`
		DestinationID           string  `json:"destinationID"`
		Platform                string  `json:"platform"`
		ChannelID               string  `json:"channelID"`
		ThreadID                string  `json:"threadID"`
		ClearTarget             *bool   `json:"clearTarget"`
		ClearDestination        *bool   `json:"clearDestination"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}

	ref := automationRefArgs{ID: p.ID, Name: p.Name}
	if existing, err := m.resolveAutomation(ref, false); err == nil {
		updateArgs, _ := json.Marshal(map[string]any{
			"id":                      existing.ID,
			"name":                    existing.Name,
			"newName":                 p.NewName,
			"prompt":                  p.Prompt,
			"schedule":                p.Schedule,
			"emoji":                   p.Emoji,
			"enabled":                 p.Enabled,
			"description":             p.Description,
			"workflowID":              p.WorkflowID,
			"workflowInputValuesJSON": p.WorkflowInputValuesJSON,
			"destinationID":           p.DestinationID,
			"platform":                p.Platform,
			"channelID":               p.ChannelID,
			"threadID":                p.ThreadID,
			"clearTarget":             p.ClearTarget,
			"clearDestination":        p.ClearDestination,
		})
		res, err := m.agentUpdate(ctx, updateArgs)
		if err != nil {
			return res, err
		}
		if res.Artifacts == nil {
			res.Artifacts = map[string]any{}
		}
		res.Artifacts["operation"] = "updated"
		return res, nil
	} else if !strings.Contains(err.Error(), "not found") {
		return skills.ToolResult{}, err
	}

	name := strings.TrimSpace(p.Name)
	if name == "" {
		return skills.ToolResult{}, fmt.Errorf("name is required when creating a new automation")
	}
	if p.Schedule == nil || strings.TrimSpace(*p.Schedule) == "" {
		return skills.ToolResult{}, fmt.Errorf("schedule is required when creating a new automation")
	}
	if (p.Prompt == nil || strings.TrimSpace(*p.Prompt) == "") && strings.TrimSpace(p.WorkflowID) == "" {
		return skills.ToolResult{}, fmt.Errorf("prompt is required when creating a new automation unless workflowID is provided")
	}
	createArgs, _ := json.Marshal(map[string]any{
		"name":                    name,
		"prompt":                  stringPtrValue(p.Prompt),
		"schedule":                strings.TrimSpace(*p.Schedule),
		"emoji":                   p.Emoji,
		"description":             p.Description,
		"enabled":                 p.Enabled,
		"workflowID":              p.WorkflowID,
		"workflowInputValuesJSON": p.WorkflowInputValuesJSON,
		"destinationID":           p.DestinationID,
		"platform":                p.Platform,
		"channelID":               p.ChannelID,
		"threadID":                p.ThreadID,
	})
	res, err := m.agentCreate(ctx, createArgs)
	if err != nil {
		return res, err
	}
	if res.Artifacts == nil {
		res.Artifacts = map[string]any{}
	}
	res.Artifacts["operation"] = "created"
	return res, nil
}

func (m *Module) agentUpdate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		ID                      string  `json:"id"`
		Name                    string  `json:"name"`
		NewName                 string  `json:"newName"`
		Prompt                  *string `json:"prompt"`
		Schedule                *string `json:"schedule"`
		Emoji                   *string `json:"emoji"`
		Enabled                 *bool   `json:"enabled"`
		Description             *string `json:"description"`
		WorkflowID              string  `json:"workflowID"`
		WorkflowInputValuesJSON string  `json:"workflowInputValuesJSON"`
		DestinationID           string  `json:"destinationID"`
		Platform                string  `json:"platform"`
		ChannelID               string  `json:"channelID"`
		ThreadID                string  `json:"threadID"`
		ClearTarget             *bool   `json:"clearTarget"`
		ClearDestination        *bool   `json:"clearDestination"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(automationRefArgs{ID: p.ID, Name: p.Name}, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	updates := item
	if strings.TrimSpace(p.NewName) != "" {
		updates.Name = strings.TrimSpace(p.NewName)
	}
	if p.Prompt != nil {
		updates.Prompt = strings.TrimSpace(*p.Prompt)
	}
	if p.Schedule != nil {
		updates.ScheduleRaw = strings.TrimSpace(*p.Schedule)
	}
	if p.Emoji != nil {
		updates.Emoji = strings.TrimSpace(*p.Emoji)
	}
	if p.Enabled != nil {
		updates.IsEnabled = *p.Enabled
	}
	if p.Description != nil {
		updates.GremlinDescription = p.Description
	}
	if p.ClearTarget != nil && *p.ClearTarget {
		updates.ExecutableTarget = nil
		updates.WorkflowID = nil
		updates.WorkflowInputValues = nil
	} else if strings.TrimSpace(p.WorkflowID) != "" {
		target, workflowInputs, err := m.resolveAgentWorkflowTarget(p.WorkflowID, p.WorkflowInputValuesJSON)
		if err != nil {
			return skills.ToolResult{}, err
		}
		updates.ExecutableTarget = target
		updates.WorkflowID = nil
		updates.WorkflowInputValues = workflowInputs
	} else if strings.TrimSpace(p.WorkflowInputValuesJSON) != "" {
		if updates.ExecutableTarget == nil || strings.TrimSpace(updates.ExecutableTarget.Type) != "workflow" {
			return skills.ToolResult{}, fmt.Errorf("workflowInputValuesJSON requires an existing workflow target or workflowID")
		}
		workflowInputs, err := parseWorkflowInputValuesJSON(p.WorkflowInputValuesJSON)
		if err != nil {
			return skills.ToolResult{}, err
		}
		updates.WorkflowInputValues = workflowInputs
	}
	if p.ClearDestination != nil && *p.ClearDestination {
		updates.CommunicationDestination = nil
	} else if hasAgentDestinationArgs(p.DestinationID, p.Platform, p.ChannelID, p.ThreadID) {
		dest, err := m.resolveAgentDestination(p.DestinationID, p.Platform, p.ChannelID, p.ThreadID)
		if err != nil {
			return skills.ToolResult{}, err
		}
		updates.CommunicationDestination = dest
	}
	updates.LastModifiedAt = strPtr(time.Now().UTC().Format(time.RFC3339))
	updated, err := m.saveDefinition(updates)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update automation: %w", err)
	}
	return skills.OKResult(m.automationUpdatedSummary(updated), map[string]any{"automation": m.automationArtifact(updated)}), nil
}

func (m *Module) resolveAgentWorkflowTarget(workflowRef, inputValuesJSON string) (*features.ExecutableTarget, map[string]string, error) {
	ref := strings.TrimSpace(workflowRef)
	if ref == "" {
		if strings.TrimSpace(inputValuesJSON) != "" {
			return nil, nil, fmt.Errorf("workflowInputValuesJSON requires workflowID")
		}
		return nil, nil, nil
	}
	if m.workflows == nil {
		return nil, nil, fmt.Errorf("workflow targets are not available")
	}
	canonicalID, err := m.resolveWorkflowTargetRef(ref)
	if err != nil {
		return nil, nil, err
	}
	inputs, err := parseWorkflowInputValuesJSON(inputValuesJSON)
	if err != nil {
		return nil, nil, err
	}
	return &features.ExecutableTarget{Type: "workflow", Ref: canonicalID}, inputs, nil
}

func (m *Module) resolveWorkflowTargetRef(ref string) (string, error) {
	if row, err := m.workflows.GetWorkflow(ref); err != nil {
		return "", fmt.Errorf("load workflow %q: %w", ref, err)
	} else if row != nil {
		return row.ID, nil
	}
	rows, err := m.workflows.ListWorkflows()
	if err != nil {
		return "", fmt.Errorf("list workflows: %w", err)
	}
	var matchID string
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Name), ref) {
			if matchID != "" {
				return "", fmt.Errorf("multiple workflows named %q; use the workflow ID", ref)
			}
			matchID = row.ID
		}
	}
	if matchID == "" {
		return "", fmt.Errorf("workflow %q not found", ref)
	}
	return matchID, nil
}

func parseWorkflowInputValuesJSON(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("workflowInputValuesJSON must be a JSON object: %w", err)
	}
	return values, nil
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (m *Module) automationArtifact(item features.GremlinItem) map[string]any {
	item = normalizeAutomationItem(item)
	data, err := json.Marshal(item)
	if err != nil {
		return map[string]any{
			"id":   item.ID,
			"name": item.Name,
		}
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{
			"id":   item.ID,
			"name": item.Name,
		}
	}
	out["displayName"] = item.Name
	if target := m.automationTargetDisplay(item); len(target) > 0 {
		out["targetDisplay"] = target
		if name, ok := target["name"].(string); ok && strings.TrimSpace(name) != "" {
			out["targetDisplayName"] = name
		}
	}
	return out
}

func (m *Module) automationArtifacts(items []features.GremlinItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, m.automationArtifact(item))
	}
	return out
}

func (m *Module) automationTargetDisplay(item features.GremlinItem) map[string]any {
	item = normalizeAutomationItem(item)
	if item.ExecutableTarget == nil {
		return nil
	}
	targetType := strings.TrimSpace(item.ExecutableTarget.Type)
	targetRef := strings.TrimSpace(item.ExecutableTarget.Ref)
	if targetType == "" || targetRef == "" {
		return nil
	}
	name := targetRef
	if targetType == "workflow" {
		name = m.workflowDisplayName(targetRef)
	}
	if strings.TrimSpace(name) == "" {
		name = targetRef
	}
	return map[string]any{
		"type": targetType,
		"ref":  targetRef,
		"name": name,
	}
}

func (m *Module) workflowDisplayName(id string) string {
	if m.workflows == nil {
		return id
	}
	row, err := m.workflows.GetWorkflow(id)
	if err != nil || row == nil {
		return id
	}
	if strings.TrimSpace(row.Name) == "" {
		return id
	}
	return strings.TrimSpace(row.Name)
}

func (m *Module) automationCreatedSummary(item features.GremlinItem) string {
	return fmt.Sprintf("Automation %q created%s.", item.Name, m.automationTargetSummarySuffix(item))
}

func (m *Module) automationUpdatedSummary(item features.GremlinItem) string {
	return fmt.Sprintf("Automation %q updated%s.", item.Name, m.automationTargetSummarySuffix(item))
}

func (m *Module) automationLoadedSummary(item features.GremlinItem) string {
	return fmt.Sprintf("Automation %q loaded%s.", item.Name, m.automationTargetSummarySuffix(item))
}

func (m *Module) automationDuplicatedSummary(item features.GremlinItem) string {
	return fmt.Sprintf("Automation duplicated as %q and left disabled for review%s.", item.Name, m.automationTargetSummarySuffix(item))
}

func (m *Module) automationTargetSummarySuffix(item features.GremlinItem) string {
	target := m.automationTargetDisplay(item)
	if len(target) == 0 {
		return ""
	}
	targetType, _ := target["type"].(string)
	targetName, _ := target["name"].(string)
	if strings.TrimSpace(targetName) == "" {
		return ""
	}
	switch targetType {
	case "workflow":
		return fmt.Sprintf(" and linked to workflow %q", targetName)
	case "skill":
		return fmt.Sprintf(" and targeting skill %q", targetName)
	case "command":
		return fmt.Sprintf(" and targeting command %q", targetName)
	default:
		return fmt.Sprintf(" and targeting %s %q", targetType, targetName)
	}
}

func (m *Module) agentDelete(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p automationRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(p, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	found, err := m.deleteDefinition(item.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to delete automation: %w", err)
	}
	if !found {
		return skills.ToolResult{}, fmt.Errorf("automation %q not found", item.ID)
	}
	return skills.OKResult(fmt.Sprintf("Automation %q deleted.", item.Name), map[string]any{"id": item.ID}), nil
}

func (m *Module) agentList(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	items, err := m.listDefinitions()
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(fmt.Sprintf("Found %d automations.", len(items)), map[string]any{"automations": m.automationArtifacts(items)}), nil
}

func (m *Module) agentGet(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p automationRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(p, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	return skills.OKResult(m.automationLoadedSummary(item), map[string]any{"automation": m.automationArtifact(item)}), nil
}

func (m *Module) agentEnable(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.agentSetEnabled(args, true)
}

func (m *Module) agentDisable(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.agentSetEnabled(args, false)
}

func (m *Module) agentSetEnabled(args json.RawMessage, enabled bool) (skills.ToolResult, error) {
	var p automationRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(p, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	item.IsEnabled = enabled
	item.LastModifiedAt = strPtr(time.Now().UTC().Format(time.RFC3339))
	if _, err := m.saveDefinition(item); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update automation state: %w", err)
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	return skills.OKResult(fmt.Sprintf("Automation %q %s.", item.Name, state), map[string]any{"id": item.ID, "enabled": enabled}), nil
}

func (m *Module) agentRun(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p automationRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(p, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	result, err := m.runAutomationSync(ctx, item.ID, "agent")
	if err != nil {
		return skills.ToolResult{}, err
	}
	artifacts := map[string]any{
		"runID":          result.RunID,
		"automationID":   result.GremlinID,
		"conversationID": result.ConversationID,
		"status":         result.Status,
		"output":         result.Output,
	}
	if result.Status != "completed" {
		return skills.ToolResult{}, fmt.Errorf("automation %q failed: %s", result.AutomationName, result.Output)
	}
	return skills.OKResult(fmt.Sprintf("Automation %q ran successfully.", result.AutomationName), artifacts), nil
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
	item, err := m.resolveAutomation(automationRefArgs{ID: p.ID, Name: p.Name}, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}
	rows, err := m.store.ListGremlinRuns(item.ID, p.Limit)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to read run history: %w", err)
	}
	return skills.OKResult(fmt.Sprintf("Found %d run records for %q.", len(rows), item.Name), map[string]any{
		"automation": m.automationArtifact(item),
		"runs":       toGremlinRunRecords(rows),
	}), nil
}

func (m *Module) agentNextRun(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p automationRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	item, err := m.resolveAutomation(p, true)
	if err != nil {
		return skills.ToolResult{}, err
	}
	next := "unknown"
	if !item.IsEnabled {
		next = "disabled"
	} else if runAt, ok := nextRunForAutomation(item.ScheduleRaw, item.NextRunAt, time.Now()); ok {
		next = runAt.UTC().Format(time.RFC3339)
	}
	return skills.OKResult(fmt.Sprintf("Automation %q next run: %s.", item.Name, next), map[string]any{
		"automation": m.automationArtifact(item),
		"nextRun":    next,
	}), nil
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
	item, err := m.resolveAutomation(automationRefArgs{ID: p.ID, Name: p.Name}, false)
	if err != nil {
		return skills.ToolResult{}, err
	}
	desc := fmt.Sprintf("Copy of %s", item.Name)
	duplicate := item
	duplicate.ID = ""
	duplicate.Name = strings.TrimSpace(p.NewName)
	duplicate.IsEnabled = false
	duplicate.SourceType = "agent"
	duplicate.CreatedAt = time.Now().Format("2006-01-02")
	duplicate.GremlinDescription = &desc
	duplicate.LastModifiedAt = nil
	if _, err := m.createDefinition(duplicate); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to duplicate automation: %w", err)
	}
	created, _ := m.resolveAutomation(automationRefArgs{Name: duplicate.Name}, false)
	return skills.OKResult(m.automationDuplicatedSummary(created), map[string]any{"automation": m.automationArtifact(created)}), nil
}

func (m *Module) agentValidateSchedule(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p struct {
		Schedule string `json:"schedule"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	schedule := strings.TrimSpace(p.Schedule)
	if schedule == "" {
		return skills.ToolResult{}, fmt.Errorf("schedule is required")
	}
	summary := validateScheduleSummary(schedule)
	return skills.OKResult(summary, map[string]any{"schedule": schedule, "summary": summary}), nil
}

func hasAgentDestinationArgs(destinationID, platform, channelID, threadID string) bool {
	return strings.TrimSpace(destinationID) != "" ||
		strings.TrimSpace(platform) != "" ||
		strings.TrimSpace(channelID) != "" ||
		strings.TrimSpace(threadID) != ""
}

func (m *Module) resolveAgentDestination(destinationID, platform, channelID, threadID string) (*features.CommunicationDestination, error) {
	if m.commsStore == nil {
		return nil, fmt.Errorf("communication destinations are not available")
	}
	targetID := strings.TrimSpace(destinationID)
	targetPlatform := strings.ToLower(strings.TrimSpace(platform))
	targetChannelID := strings.TrimSpace(channelID)
	targetThreadID := strings.TrimSpace(threadID)

	rows, err := m.commsStore.ListCommunicationChannels(targetPlatform)
	if err != nil {
		return nil, fmt.Errorf("list communication channels: %w", err)
	}
	for _, row := range rows {
		rowID := strings.Join([]string{row.Platform, row.ChannelID, row.ThreadID}, ":")
		idMatches := targetID != "" && rowID == targetID
		tupleMatches := targetID == "" &&
			strings.ToLower(row.Platform) == targetPlatform &&
			row.ChannelID == targetChannelID &&
			row.ThreadID == targetThreadID
		if !idMatches && !tupleMatches {
			continue
		}
		return &features.CommunicationDestination{
			ID:          rowID,
			Platform:    row.Platform,
			ChannelID:   row.ChannelID,
			ChannelName: row.ChannelName,
			ThreadID:    strPtrIfNotEmpty(row.ThreadID),
		}, nil
	}
	if targetID != "" {
		return nil, fmt.Errorf("destination %q is not an authorized communication channel; call communication.list_channels and use one of its returned ids", targetID)
	}
	return nil, fmt.Errorf("destination %s:%s is not an authorized communication channel; call communication.list_channels and use one of its returned ids", targetPlatform, targetChannelID)
}

func (m *Module) resolveAutomation(ref automationRefArgs, allowFuzzy bool) (features.GremlinItem, error) {
	id := strings.TrimSpace(ref.ID)
	name := strings.TrimSpace(ref.Name)
	if id == "" && name == "" {
		return features.GremlinItem{}, fmt.Errorf("id or name is required")
	}
	items, err := m.listDefinitions()
	if err != nil {
		return features.GremlinItem{}, err
	}
	if id != "" {
		for _, item := range items {
			if item.ID == id {
				return item, nil
			}
		}
		return features.GremlinItem{}, fmt.Errorf("automation %q not found", id)
	}
	var exact []features.GremlinItem
	for _, item := range items {
		if strings.EqualFold(item.Name, name) {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return features.GremlinItem{}, fmt.Errorf("multiple automations named %q; use the automation ID", name)
	}
	if allowFuzzy {
		needle := strings.ToLower(name)
		var matches []features.GremlinItem
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Name), needle) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return features.GremlinItem{}, fmt.Errorf("multiple automations match %q; use the automation ID", name)
		}
	}
	return features.GremlinItem{}, fmt.Errorf("automation named %q not found", name)
}

func validateScheduleSummary(schedule string) string {
	lower := strings.ToLower(strings.TrimSpace(schedule))
	spec, ok := parseSchedule(schedule)
	if ok {
		return fmt.Sprintf("Valid %s schedule.", spec.kind)
	}
	switch {
	case strings.HasPrefix(lower, "cron "):
		if len(strings.Fields(schedule)) != 6 {
			return "Invalid cron schedule: expected 5 cron fields after 'cron'."
		}
		return "Unsupported schedule: cron schedules are not executed by the automation scheduler yet."
	case strings.HasPrefix(lower, "once "):
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(strings.TrimPrefix(lower, "once "))); err != nil {
			return "Invalid one-time schedule: expected 'once YYYY-MM-DD'."
		}
		return "Unsupported schedule: one-time schedules are not executed by the automation scheduler yet."
	default:
		return "Unrecognized schedule format."
	}
}

func strPtr(v string) *string {
	return &v
}

func strPtrIfNotEmpty(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}
