package capabilities

import (
	"fmt"
	"strings"

	"atlas-runtime-go/internal/features"
)

type Decision string

const (
	DecisionRunExisting     Decision = "run_existing"
	DecisionComposeExisting Decision = "compose_existing"
	DecisionForgeNew        Decision = "forge_new"
	DecisionAskPrerequisite Decision = "ask_for_prerequisite"
)

type RequirementStatus string

const (
	StatusAvailable           RequirementStatus = "available"
	StatusMissingCapability   RequirementStatus = "missing_capability"
	StatusMissingPrerequisite RequirementStatus = "missing_prerequisite"
)

type Requirement struct {
	Type          string            `json:"type"`
	Status        RequirementStatus `json:"status"`
	SatisfiedBy   []string          `json:"satisfiedBy"`
	MissingReason string            `json:"missingReason,omitempty"`
}

type Analysis struct {
	Goal                 string        `json:"goal"`
	Decision             Decision      `json:"decision"`
	Requirements         []Requirement `json:"requirements"`
	MissingCapabilities  []string      `json:"missingCapabilities"`
	MissingPrerequisites []string      `json:"missingPrerequisites"`
	SuggestedGroups      []string      `json:"suggestedGroups"`
}

func Analyze(message string, inventory []Record) Analysis {
	analysis := Analysis{
		Goal:            strings.TrimSpace(message),
		Decision:        DecisionRunExisting,
		Requirements:    []Requirement{},
		SuggestedGroups: []string{},
	}

	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return analysis
	}

	explicitWorkflowRequest := isExplicitWorkflowRequest(normalized)
	explicitAutomationRequest := isExplicitAutomationRequest(normalized)

	index := buildInventoryIndex(inventory)
	reqs := inferRequirements(normalized)
	if len(reqs) == 0 {
		if index.hasSkill("forge") && wantsForge(normalized) {
			reqs = append(reqs, "forge.build")
		} else {
			return analysis
		}
	}

	suggestedGroups := map[string]bool{}
	uniqueProviders := map[string]bool{}
	hasMultiStepIntent := false

	for _, reqType := range reqs {
		req := Requirement{Type: reqType, SatisfiedBy: []string{}}
		for _, group := range groupsForRequirement(reqType) {
			suggestedGroups[group] = true
		}
		if (reqType == "workflow.compose" && !explicitWorkflowRequest) || (reqType == "automation.schedule" && !explicitAutomationRequest) {
			hasMultiStepIntent = true
		}

		switch reqType {
		case "file.write":
			req = evaluateActionRequirement(req, index, "fs.write_file", "")
		case "file.create_pdf":
			req = evaluateActionRequirement(req, index, "fs.create_pdf", "")
		case "file.create_docx":
			req = evaluateActionRequirement(req, index, "fs.create_docx", "")
		case "file.create_zip":
			req = evaluateActionRequirement(req, index, "fs.create_zip", "")
		case "file.save_image":
			req = evaluateActionRequirement(req, index, "fs.save_image", "")
		case "workflow.compose":
			req = evaluateSkillRequirement(req, index, "workflow-control", "")
		case "automation.schedule":
			req = evaluateSkillRequirement(req, index, "automation-control", "")
		case "team.manage":
			req = evaluateSkillRequirement(req, index, "team-control", "")
		case "forge.build":
			req = evaluateSkillRequirement(req, index, "forge", "")
		case "delivery.chat":
			req = evaluateActionRequirement(req, index, "communication.send_message", "")
		case "delivery.channel":
			req = evaluateActionRequirement(req, index, "communication.send_message", "authorized destination required")
		case "delivery.email":
			req = evaluateCustomRequirement(req, nil, "no email delivery capability is currently installed")
		}

		for _, provider := range req.SatisfiedBy {
			uniqueProviders[provider] = true
		}
		analysis.Requirements = append(analysis.Requirements, req)
		switch req.Status {
		case StatusMissingCapability:
			analysis.MissingCapabilities = append(analysis.MissingCapabilities, req.Type)
		case StatusMissingPrerequisite:
			analysis.MissingPrerequisites = append(analysis.MissingPrerequisites, req.Type)
		}
	}

	analysis.MissingCapabilities = dedupeStrings(analysis.MissingCapabilities)
	analysis.MissingPrerequisites = dedupeStrings(analysis.MissingPrerequisites)
	if len(analysis.MissingCapabilities) > 0 && index.hasSkill("forge") {
		suggestedGroups["forge"] = true
	}
	analysis.SuggestedGroups = sortedKeys(suggestedGroups)

	switch {
	case len(analysis.MissingPrerequisites) > 0:
		analysis.Decision = DecisionAskPrerequisite
	case len(analysis.MissingCapabilities) > 0:
		if index.hasSkill("forge") {
			analysis.Decision = DecisionForgeNew
		} else {
			analysis.Decision = DecisionAskPrerequisite
		}
	case hasMultiStepIntent || len(uniqueProviders) > 1:
		analysis.Decision = DecisionComposeExisting
	default:
		analysis.Decision = DecisionRunExisting
	}

	return analysis
}

type inventoryIndex struct {
	skillByID     map[string]Record
	actionToSkill map[string]string
}

func buildInventoryIndex(inventory []Record) inventoryIndex {
	out := inventoryIndex{
		skillByID:     make(map[string]Record),
		actionToSkill: make(map[string]string),
	}
	for _, record := range inventory {
		if record.Kind != KindSkill {
			continue
		}
		out.skillByID[record.ID] = record
		for _, action := range recordActions(record) {
			out.actionToSkill[action.ID] = record.ID
		}
	}
	return out
}

func (i inventoryIndex) hasSkill(skillID string) bool {
	_, ok := i.skillByID[skillID]
	return ok
}

func inferRequirements(message string) []string {
	reqs := []string{}
	add := func(req string) {
		for _, existing := range reqs {
			if existing == req {
				return
			}
		}
		reqs = append(reqs, req)
	}

	if containsAny(message, "pdf") {
		add("file.create_pdf")
	}
	if containsAny(message, "docx", "word document", "word doc") {
		add("file.create_docx")
	}
	if containsAny(message, "zip", "archive") {
		add("file.create_zip")
	}
	if containsAny(message, "image", "png", "jpg", "jpeg", "gif") {
		add("file.save_image")
	}
	if containsAny(message, "save file", "save files", "create file", "create files", "write file", "write files") {
		add("file.write")
	}
	explicitWorkflowRequest := isExplicitWorkflowRequest(message)
	if containsAny(message, "workflow", "multi-step", "multistep", "chain", "pipeline", "orchestrate") || explicitWorkflowRequest {
		add("workflow.compose")
	}
	explicitTeamRequest := containsAny(message,
		"create an agent", "make an agent", "new agent", "add an agent",
		"create a team member", "add a team member", "new team member",
		"create teammate", "add teammate", "new teammate",
		"create worker agent", "create a worker",
		"delete agent", "delete an agent", "delete all agents",
		"remove agent", "remove an agent", "remove all agents",
		"list agents", "show agents", "show my agents", "what agents do i have",
		"pause agent", "resume agent", "disable agent", "enable agent",
		"pause all agents", "resume all agents", "disable all agents", "enable all agents",
		"delete team member", "remove team member", "list team members",
		"pause team member", "resume team member", "disable team member", "enable team member",
		"delete teammate", "remove teammate", "list teammates",
	)
	if explicitTeamRequest {
		add("team.manage")
	}
	explicitAutomationRequest := isExplicitAutomationRequest(message)
	if containsAny(message, "every ", "daily", "weekly", "monthly", "schedule", "automation", "remind me", "at 8", "at 9") || explicitAutomationRequest {
		add("automation.schedule")
	}
	if explicitTeamRequest || explicitWorkflowRequest || explicitAutomationRequest {
		return reqs
	}
	if containsAny(message, "email", "mail ") {
		add("delivery.email")
	} else if containsAny(message, "telegram", "slack", "discord", "channel") {
		add("delivery.channel")
	} else if containsAny(message, "send me", "message me", "chat", "send a message") {
		add("delivery.chat")
	}
	if wantsForge(message) {
		add("forge.build")
	}
	return reqs
}

func wantsForge(message string) bool {
	return containsAny(message,
		"forge ",
		"create a skill",
		"build a skill",
		"new skill",
		"teach atlas",
		"make atlas able",
		"add capability",
		"new capability",
	)
}

func isExplicitWorkflowRequest(message string) bool {
	return containsAny(message,
		"list workflows", "show workflows", "show my workflows", "what workflows do i have",
		"create workflow", "new workflow", "add workflow", "make workflow",
		"update workflow", "edit workflow", "change workflow",
		"delete workflow", "delete a workflow", "delete all workflows",
		"remove workflow", "remove a workflow", "remove all workflows",
		"run workflow", "run a workflow", "run all workflows",
		"enable workflow", "disable workflow",
		"enable all workflows", "disable all workflows",
		"activate workflow", "activate all workflows",
	)
}

func isExplicitAutomationRequest(message string) bool {
	return containsAny(message,
		"list automations", "show automations", "show my automations", "what automations do i have",
		"create automation", "new automation", "add automation", "make automation",
		"update automation", "edit automation", "change automation",
		"delete automation", "delete an automation", "delete all automations",
		"remove automation", "remove an automation", "remove all automations",
		"run automation", "run an automation", "run all automations",
		"enable automation", "disable automation",
		"enable all automations", "disable all automations",
		"activate automation", "activate all automations",
		"pause automation", "pause all automations", "resume automation", "resume all automations",
	)
}

func groupsForRequirement(reqType string) []string {
	switch reqType {
	case "file.write", "file.create_pdf", "file.create_docx", "file.create_zip", "file.save_image":
		return []string{"files"}
	case "workflow.compose":
		return []string{"workflow"}
	case "automation.schedule":
		return []string{"automation"}
	case "team.manage":
		return []string{"team"}
	case "delivery.chat", "delivery.channel", "delivery.email":
		return []string{"communication"}
	case "forge.build":
		return []string{"forge"}
	default:
		return []string{}
	}
}

func evaluateActionRequirement(req Requirement, index inventoryIndex, actionID string, prerequisite string) Requirement {
	if skillID, ok := index.actionToSkill[actionID]; ok {
		req.SatisfiedBy = []string{skillID}
		if prerequisite == "" {
			prerequisite = detectActionPrerequisite(index, skillID, actionID)
		}
		if prerequisite != "" {
			req.Status = StatusMissingPrerequisite
			req.MissingReason = prerequisite
		} else {
			req.Status = StatusAvailable
		}
		return req
	}
	return evaluateCustomRequirement(req, nil, fmt.Sprintf("missing action %s", actionID))
}

func evaluateSkillRequirement(req Requirement, index inventoryIndex, skillID string, prerequisite string) Requirement {
	if index.hasSkill(skillID) {
		req.SatisfiedBy = []string{skillID}
		if prerequisite != "" {
			req.Status = StatusMissingPrerequisite
			req.MissingReason = prerequisite
		} else {
			req.Status = StatusAvailable
		}
		return req
	}
	return evaluateCustomRequirement(req, nil, fmt.Sprintf("missing skill %s", skillID))
}

func detectActionPrerequisite(index inventoryIndex, skillID, actionID string) string {
	record, ok := index.skillByID[skillID]
	if !ok {
		return ""
	}
	if strings.HasPrefix(actionID, "fs.") && !boolMetadata(record.Metadata, "hasApprovedRoots") {
		return "approved file root required"
	}
	return ""
}

func boolMetadata(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func evaluateCustomRequirement(req Requirement, providers []string, reason string) Requirement {
	req.SatisfiedBy = dedupeStrings(providers)
	if req.SatisfiedBy == nil {
		req.SatisfiedBy = []string{}
	}
	req.MissingReason = reason
	if strings.Contains(reason, "required") {
		req.Status = StatusMissingPrerequisite
	} else {
		req.Status = StatusMissingCapability
	}
	return req
}

func recordActions(record Record) []features.SkillAction {
	raw, ok := record.Metadata["actions"]
	if !ok || raw == nil {
		return []features.SkillAction{}
	}
	switch actions := raw.(type) {
	case []features.SkillAction:
		return append([]features.SkillAction(nil), actions...)
	case []any:
		out := make([]features.SkillAction, 0, len(actions))
		for _, item := range actions {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, features.SkillAction{
				ID:              stringFromAny(m["id"]),
				Name:            stringFromAny(m["name"]),
				Description:     stringFromAny(m["description"]),
				PermissionLevel: stringFromAny(m["permissionLevel"]),
				ApprovalPolicy:  stringFromAny(m["approvalPolicy"]),
				IsEnabled:       boolFromAny(m["isEnabled"]),
			})
		}
		return out
	default:
		return []features.SkillAction{}
	}
}

func containsAny(message string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key, enabled := range m {
		if enabled {
			out = append(out, key)
		}
	}
	if len(out) <= 1 {
		return out
	}
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
