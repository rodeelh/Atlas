package capabilities

import (
	"encoding/json"
	"sort"
	"strings"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/forge"
	runtimeskills "atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type Kind string

const (
	KindSkill      Kind = "skill"
	KindWorkflow   Kind = "workflow"
	KindAutomation Kind = "automation"
)

type TargetType string

const (
	TargetSkill    TargetType = "skill"
	TargetWorkflow TargetType = "workflow"
	TargetCommand  TargetType = "command"
)

type ExecutableTarget struct {
	Type TargetType `json:"type"`
	Ref  string     `json:"ref"`
}

type Record struct {
	ID                   string           `json:"id"`
	Kind                 Kind             `json:"kind"`
	Name                 string           `json:"name"`
	Description          string           `json:"description"`
	Source               string           `json:"source"`
	Category             string           `json:"category"`
	Target               ExecutableTarget `json:"target"`
	IsEnabled            bool             `json:"isEnabled"`
	Tags                 []string         `json:"tags"`
	InputSchema          map[string]any   `json:"inputSchema"`
	OutputSchema         map[string]any   `json:"outputSchema"`
	ArtifactTypes        []string         `json:"artifactTypes"`
	RequiredCapabilities []string         `json:"requiredCapabilities"`
	RequiredSecrets      []string         `json:"requiredSecrets"`
	RequiredRoots        []string         `json:"requiredRoots"`
	Metadata             map[string]any   `json:"metadata"`
}

type WorkflowLister interface {
	ListWorkflows() ([]storage.WorkflowRow, error)
}

type AutomationLister interface {
	ListAutomations() ([]storage.AutomationRow, error)
}

func List(supportDir string, workflows WorkflowLister, automations AutomationLister) ([]Record, error) {
	records := make([]Record, 0, 32)
	existingSkillIDs := make(map[string]bool)

	for _, skill := range features.ListSkills(supportDir) {
		records = append(records, skillToRecord(supportDir, skill))
		existingSkillIDs[skill.Manifest.ID] = true
	}

	for _, installed := range forge.ListInstalled(supportDir) {
		record, ok := installedForgeToRecord(installed)
		if !ok || existingSkillIDs[record.ID] {
			continue
		}
		records = append(records, record)
		existingSkillIDs[record.ID] = true
	}

	if workflows != nil {
		rows, err := workflows.ListWorkflows()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			records = append(records, workflowToRecord(row))
		}
	}

	if automations != nil {
		rows, err := automations.ListAutomations()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			records = append(records, automationToRecord(row))
		}
	}

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Kind == records[j].Kind {
			if strings.EqualFold(records[i].Name, records[j].Name) {
				return records[i].ID < records[j].ID
			}
			return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name)
		}
		return records[i].Kind < records[j].Kind
	})
	return records, nil
}

func skillToRecord(supportDir string, skill features.SkillRecord) Record {
	source := strings.TrimSpace(skill.Manifest.Source)
	if source == "" {
		source = "builtin"
	}
	artifactTypes := inferSkillArtifactTypes(skill)
	requiredRoots := inferSkillRequiredRoots(skill)
	metadata := map[string]any{
		"actionCount": len(skill.Actions),
		"actions":     skill.Actions,
	}
	if skill.Manifest.ID == "file-system" {
		hasRoots := false
		rootCount := 0
		if roots, err := runtimeskills.LoadFsRoots(supportDir); err == nil {
			rootCount = len(roots)
			hasRoots = len(roots) > 0
		}
		metadata["hasApprovedRoots"] = hasRoots
		metadata["approvedRootCount"] = rootCount
	}
	return Record{
		ID:                   skill.Manifest.ID,
		Kind:                 KindSkill,
		Name:                 skill.Manifest.Name,
		Description:          skill.Manifest.Description,
		Source:               source,
		Category:             defaultString(skill.Manifest.Category, "skill"),
		Target:               ExecutableTarget{Type: TargetSkill, Ref: skill.Manifest.ID},
		IsEnabled:            strings.ToLower(skill.Manifest.LifecycleState) != "disabled",
		Tags:                 cloneStrings(skill.Manifest.Tags),
		InputSchema:          map[string]any{},
		OutputSchema:         map[string]any{},
		ArtifactTypes:        artifactTypes,
		RequiredCapabilities: cloneStrings(skill.Manifest.Capabilities),
		RequiredSecrets:      []string{},
		RequiredRoots:        requiredRoots,
		Metadata:             metadata,
	}
}

func workflowToRecord(row storage.WorkflowRow) Record {
	def := decodeObject(row.DefinitionJSON)
	stepCount := 0
	if raw, ok := def["steps"].([]any); ok {
		stepCount = len(raw)
	}
	return Record{
		ID:                   row.ID,
		Kind:                 KindWorkflow,
		Name:                 defaultString(stringValue(def, "name"), row.Name),
		Description:          defaultString(stringValue(def, "description"), stringValue(def, "promptTemplate")),
		Source:               "runtime",
		Category:             "workflow",
		Target:               ExecutableTarget{Type: TargetWorkflow, Ref: row.ID},
		IsEnabled:            row.IsEnabled,
		Tags:                 stringSlice(def["tags"]),
		InputSchema:          emptyObject(def["inputSchema"]),
		OutputSchema:         emptyObject(def["outputSchema"]),
		ArtifactTypes:        stringSlice(def["artifactTypes"]),
		RequiredCapabilities: []string{},
		RequiredSecrets:      []string{},
		RequiredRoots:        []string{},
		Metadata: map[string]any{
			"stepCount":        stepCount,
			"trustScope":       emptyObject(def["trustScope"]),
			"promptTemplate":   stringValue(def, "promptTemplate"),
			"createdAt":        row.CreatedAt,
			"updatedAt":        row.UpdatedAt,
			"definitionSource": "sqlite",
		},
	}
}

func automationToRecord(row storage.AutomationRow) Record {
	target := ExecutableTarget{Type: TargetCommand, Ref: row.ID}
	requiredCapabilities := []string{}
	if row.WorkflowID != nil && strings.TrimSpace(*row.WorkflowID) != "" {
		rawTarget := strings.TrimSpace(*row.WorkflowID)
		switch {
		case strings.HasPrefix(rawTarget, "skill:"):
			target = ExecutableTarget{Type: TargetSkill, Ref: strings.TrimSpace(strings.TrimPrefix(rawTarget, "skill:"))}
			requiredCapabilities = append(requiredCapabilities, "skill:"+target.Ref)
		case strings.HasPrefix(rawTarget, "command:"):
			target = ExecutableTarget{Type: TargetCommand, Ref: strings.TrimSpace(strings.TrimPrefix(rawTarget, "command:"))}
		default:
			target = ExecutableTarget{Type: TargetWorkflow, Ref: rawTarget}
			requiredCapabilities = append(requiredCapabilities, "workflow:"+rawTarget)
		}
	}
	description := strings.TrimSpace(stringPtrValue(row.GremlinDescription))
	if description == "" {
		description = strings.TrimSpace(row.Prompt)
	}
	if len(description) > 180 {
		description = description[:177] + "..."
	}
	deliveryConfigured := row.CommunicationDestinationJSON != nil && strings.TrimSpace(*row.CommunicationDestinationJSON) != ""
	return Record{
		ID:                   row.ID,
		Kind:                 KindAutomation,
		Name:                 row.Name,
		Description:          description,
		Source:               defaultString(row.SourceType, "runtime"),
		Category:             "automation",
		Target:               target,
		IsEnabled:            row.IsEnabled,
		Tags:                 decodeStringArray(row.TagsJSON),
		InputSchema:          map[string]any{},
		OutputSchema:         map[string]any{},
		ArtifactTypes:        []string{"automation.run_result"},
		RequiredCapabilities: requiredCapabilities,
		RequiredSecrets:      []string{},
		RequiredRoots:        []string{},
		Metadata: map[string]any{
			"scheduleRaw":        row.ScheduleRaw,
			"scheduleJSON":       stringPtrValue(row.ScheduleJSON),
			"workflowID":         stringPtrValue(row.WorkflowID),
			"targetType":         string(target.Type),
			"targetRef":          target.Ref,
			"deliveryConfigured": deliveryConfigured,
			"nextRunAt":          stringPtrValue(row.NextRunAt),
			"updatedAt":          row.UpdatedAt,
			"createdAt":          row.CreatedAt,
		},
	}
}

func installedForgeToRecord(installed map[string]any) (Record, bool) {
	if installed == nil {
		return Record{}, false
	}
	skillID := strings.TrimSpace(stringFromAnyValue(installed["id"]))
	if skillID == "" {
		return Record{}, false
	}

	manifest, _ := installed["manifest"].(map[string]any)
	actions := skillActions(installed["actions"])
	target := executableTarget(installed["target"])
	if target.Type == "" {
		target = ExecutableTarget{Type: TargetSkill, Ref: skillID}
	}
	metadata := map[string]any{
		"actionCount": len(actions),
		"actions":     actions,
	}

	requiredSecrets := stringSlice(installed["requiredSecrets"])
	record := Record{
		ID:                   skillID,
		Kind:                 KindSkill,
		Name:                 defaultString(stringFromAnyValue(manifest["name"]), skillID),
		Description:          defaultString(stringFromAnyValue(manifest["description"]), strings.TrimSpace(stringFromAnyValue(installed["description"]))),
		Source:               defaultString(stringFromAnyValue(manifest["source"]), "forge"),
		Category:             defaultString(stringFromAnyValue(manifest["category"]), "skill"),
		Target:               target,
		IsEnabled:            strings.ToLower(stringFromAnyValue(manifest["lifecycleState"])) != "disabled" && strings.ToLower(stringFromAnyValue(manifest["lifecycleState"])) != "uninstalled",
		Tags:                 stringSlice(manifest["tags"]),
		InputSchema:          map[string]any{},
		OutputSchema:         map[string]any{},
		ArtifactTypes:        []string{},
		RequiredCapabilities: []string{},
		RequiredSecrets:      requiredSecrets,
		RequiredRoots:        []string{},
		Metadata:             metadata,
	}
	if target.Type == TargetWorkflow {
		record.RequiredCapabilities = append(record.RequiredCapabilities, "workflow:"+target.Ref)
	}
	return record, true
}

func inferSkillArtifactTypes(skill features.SkillRecord) []string {
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
	}
	for _, action := range skill.Actions {
		switch action.ID {
		case "fs.write_file":
			add("file.text")
		case "fs.write_binary_file":
			add("file.binary")
		case "fs.create_pdf":
			add("file.pdf")
		case "fs.create_docx":
			add("file.docx")
		case "fs.create_zip":
			add("file.zip")
		case "fs.save_image":
			add("file.image")
		case "communication.send_message":
			add("message.chat")
		case "workflow.run":
			add("workflow.run_result")
		case "automation.run":
			add("automation.run_result")
		}
	}
	return sortedKeys(seen)
}

func inferSkillRequiredRoots(skill features.SkillRecord) []string {
	for _, action := range skill.Actions {
		if strings.HasPrefix(action.ID, "fs.") {
			return []string{"approved_fs_root"}
		}
	}
	return []string{}
}

func executableTarget(raw any) ExecutableTarget {
	target, _ := raw.(map[string]any)
	if target == nil {
		return ExecutableTarget{}
	}
	targetType := strings.TrimSpace(stringFromAnyValue(target["type"]))
	ref := strings.TrimSpace(stringFromAnyValue(target["ref"]))
	switch targetType {
	case string(TargetWorkflow):
		return ExecutableTarget{Type: TargetWorkflow, Ref: ref}
	case string(TargetCommand):
		return ExecutableTarget{Type: TargetCommand, Ref: ref}
	case "custom_skill", string(TargetSkill):
		return ExecutableTarget{Type: TargetSkill, Ref: defaultString(ref, ref)}
	default:
		return ExecutableTarget{}
	}
}

func skillActions(raw any) []features.SkillAction {
	switch actions := raw.(type) {
	case []features.SkillAction:
		return append([]features.SkillAction(nil), actions...)
	case []any:
		out := make([]features.SkillAction, 0, len(actions))
		for _, item := range actions {
			action, _ := item.(map[string]any)
			if action == nil {
				continue
			}
			out = append(out, features.SkillAction{
				ID:              strings.TrimSpace(stringFromAnyValue(action["id"])),
				Name:            strings.TrimSpace(stringFromAnyValue(action["name"])),
				Description:     strings.TrimSpace(stringFromAnyValue(action["description"])),
				PermissionLevel: strings.TrimSpace(stringFromAnyValue(action["permissionLevel"])),
				ApprovalPolicy:  strings.TrimSpace(stringFromAnyValue(action["approvalPolicy"])),
				IsEnabled:       boolValue(action["isEnabled"]),
			})
		}
		return out
	default:
		return []features.SkillAction{}
	}
}

func stringFromAnyValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func decodeObject(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func decodeStringArray(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func stringValue(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}

func stringSlice(v any) []string {
	switch raw := v.(type) {
	case []string:
		return cloneStrings(raw)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if out == nil {
			return []string{}
		}
		return out
	default:
		return []string{}
	}
}

func emptyObject(v any) map[string]any {
	raw, _ := v.(map[string]any)
	if raw == nil {
		return map[string]any{}
	}
	return raw
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}
