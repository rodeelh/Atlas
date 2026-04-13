package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type delegateArgs struct {
	AgentID     string `json:"agentID"`
	Task        string `json:"task"`
	Goal        string `json:"goal"`
	RequestedBy string `json:"requestedBy"` // "atlas" | "user" | "auto" — defaults to "atlas"

	// V1 structured task payload (Phase 4).
	// Populated when team.delegate is called with a DelegationPlan.
	// Left empty when agent.delegate is called with flat args (backward compat).
	Title               string
	Objective           string
	ScopeJSON           string
	SuccessCriteriaJSON string
	InputContextJSON    string
	ExpectedOutputJSON  string
	Mode                string // "sync_assist" | "async_assignment"
	Pattern             string // "single" | "sequence" | "parallel"
	DependsOnJSON       string

	// TaskID allows callers to pre-generate the task ID before spawning a
	// goroutine (async_assignment). When non-empty, delegateTask uses this ID
	// instead of generating a new one. This lets the HTTP caller return the ID
	// in a 202 response before the agent loop runs. (Phase 5)
	TaskID string
}

type agentRefArgs struct {
	ID string `json:"id"`
}

type agentCreateArgs struct {
	Name               string   `json:"name"`
	ID                 string   `json:"id"`
	Role               string   `json:"role"`
	Mission            string   `json:"mission"`
	Style              string   `json:"style"`
	AllowedSkills      []string `json:"allowedSkills"`
	AllowedToolClasses []string `json:"allowedToolClasses"`
	Autonomy           string   `json:"autonomy"`
	Activation         string   `json:"activation"`
	ProviderType       string   `json:"providerType"`
	Model              string   `json:"model"`
	Enabled            *bool    `json:"enabled"`
}

type agentUpdateArgs struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Role               string   `json:"role"`
	Mission            string   `json:"mission"`
	Style              string   `json:"style"`
	AllowedSkills      []string `json:"allowedSkills"`
	AllowedToolClasses []string `json:"allowedToolClasses"`
	Autonomy           string   `json:"autonomy"`
	Activation         string   `json:"activation"`
	ProviderType       string   `json:"providerType"`
	Model              string   `json:"model"`
	Enabled            *bool    `json:"enabled"`
}

type sequenceStep struct {
	AgentID string `json:"agentID"`
	Task    string `json:"task"`
}

type sequenceArgs struct {
	Agents []sequenceStep `json:"agents"`
	Goal   string         `json:"goal"`
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
			name:        "agent.create",
			description: "Create a new Atlas team member. Use this when the user explicitly wants a new agent or teammate, not just a workflow or automation.",
			properties:  agentDefinitionProperties(),
			required:    []string{"name", "role", "mission", "allowedSkills", "autonomy"},
			perm:        "execute",
			class:       skills.ActionClassLocalWrite,
			fn:          m.agentCreate,
		},
		{
			name:        "agent.update",
			description: "Update an existing Atlas team member by exact ID.",
			properties:  agentUpdateProperties(),
			required:    []string{"id"},
			perm:        "execute",
			class:       skills.ActionClassLocalWrite,
			fn:          m.agentUpdate,
		},
		{
			name:        "agent.delete",
			description: "Delete an Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact agent ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentDelete,
		},
		{
			name:        "agent.enable",
			description: "Enable a disabled Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact agent ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentEnable,
		},
		{
			name:        "agent.disable",
			description: "Disable an Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact agent ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentDisable,
		},
		{
			name:        "agent.pause",
			description: "Pause an Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact agent ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentPause,
		},
		{
			name:        "agent.resume",
			description: "Resume a paused Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact agent ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentResume,
		},
		{
			// Deprecated: use team.delegate(pattern="sequence", tasks=[...]) instead.
			// agent.sequence is kept for backward compatibility with existing callers and
			// automation definitions. It is hidden from model-facing descriptions to avoid
			// confusing the model with two parallel sequence surfaces. The handler remains
			// fully functional; only the description signals non-preference.
			name:        "agent.sequence",
			description: "Deprecated — use team.delegate with pattern=sequence instead. Kept for backward compatibility only.",
			properties: map[string]skills.ToolParam{
				"agents": {Type: "array", Description: "Ordered list of delegation steps, each with agentID and task.", Items: &skills.ToolParam{Type: "string"}},
				"goal":   {Type: "string", Description: "Overall goal for the sequence."},
			},
			required: []string{"agents"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentSequence,
		},
		{
			name:        "agent.assign",
			description: "Directly assign a task to an Atlas team member on behalf of the user (not mediated through a chat turn).",
			properties: map[string]skills.ToolParam{
				"agentID": {Type: "string", Description: "Exact agent ID from the team roster."},
				"task":    {Type: "string", Description: "Concrete task for the agent to complete."},
				"goal":    {Type: "string", Description: "Optional outcome framing."},
			},
			required: []string{"agentID", "task"},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.agentAssign,
		},

		// ── Teams V1 canonical skill namespace ────────────────────────────────
		{
			name:        "team.list",
			description: "List all Atlas team members and their current runtime state.",
			properties:  map[string]skills.ToolParam{},
			required:    []string{},
			perm:        "read",
			class:       skills.ActionClassRead,
			fn:          m.agentList, // same handler — alias
		},
		{
			name:        "team.get",
			description: "Get one Atlas team member by exact ID.",
			properties: map[string]skills.ToolParam{
				"id": {Type: "string", Description: "Exact team member ID from the team roster."},
			},
			required: []string{"id"},
			perm:     "read",
			class:    skills.ActionClassRead,
			fn:       m.agentGet, // same handler — alias
		},
		{
			// Canonical delegation skill. Supports three invocation styles:
			//   1. Flat single:   agentID + task  (simplest — preferred for quick delegation)
			//   2. Simple sequence: pattern=sequence, tasks=[{agentId,task},{agentId,task}]
			//   3. Structured plan: tasks with full DelegationTaskSpec fields
			// agent.sequence is deprecated; always use this skill for delegation.
			name: "team.delegate",
			description: "Delegate work to one or more Atlas team specialists.\n\n" +
				"SINGLE STEP: team.delegate(agentID=\"scout\", task=\"Research X\")\n\n" +
				"MULTI-STEP (preferred when steps are dependent): When the task requires more than one specialist " +
				"and step 2 depends on step 1's output, submit ALL steps in a single call using pattern=\"sequence\". " +
				"Do not make multiple separate team.delegate calls in the same turn when the steps are known in advance.\n\n" +
				"Example sequence:\n" +
				"team.delegate(pattern=\"sequence\", tasks=[{agentId:\"scout\", task:\"Research top Go HTTP frameworks\"}, " +
				"{agentId:\"builder\", task:\"Build a comparison table from the research\"}])\n\n" +
				"Avoid:\n" +
				"- Calling team.delegate twice in the same turn for dependent steps\n" +
				"- Performing step 1, then deciding step 2 separately when both are already known\n\n" +
				"executionMode: sync_assist (default, result returned this turn) or async_assignment (background, returns taskID).",
			properties: map[string]skills.ToolParam{
				"pattern": {Type: "string", Description: "single (default) or sequence. Use sequence when step 2 depends on step 1's output."},
				"executionMode": {Type: "string", Description: "sync_assist (default, wait for result) or async_assignment (fire and forget, returns taskID)."},
				"mode":    {Type: "string", Description: "specialist_assist (default) or team_lead."},
				"tasks": {
					Type: "array",
					Description: "For sequence: ordered steps as [{agentId,task},{agentId,task}]. " +
						"Each step needs agentId and task at minimum; objective, title, scope, successCriteria, inputContext, expectedOutput are optional.",
					Items: &skills.ToolParam{Type: "object"},
				},
				// Flat single delegation shortcut:
				"agentID": {Type: "string", Description: "For simple single delegation: exact team member ID (use instead of tasks[])."},
				"task":    {Type: "string", Description: "For simple single delegation: the task description."},
				"goal":    {Type: "string", Description: "For simple single delegation: optional framing or outcome goal."},
			},
			required: []string{},
			perm:     "execute",
			class:    skills.ActionClassLocalWrite,
			fn:       m.teamDelegate,
		},
	} {
		m.skills.RegisterExternal(skills.SkillEntry{
			Def: skills.ToolDef{
				Name:        entry.name,
				Description: entry.description,
				Properties:  entry.properties,
				Required:    entry.required,
			},
			PermLevel:   entry.perm,
			ActionClass: entry.class,
			FnResult:    entry.fn,
		})
	}
}

func agentDefinitionProperties() map[string]skills.ToolParam {
	return map[string]skills.ToolParam{
		"name":               {Type: "string", Description: "Display name for the team member."},
		"id":                 {Type: "string", Description: "Optional explicit stable ID. Defaults to a slug from the name."},
		"role":               {Type: "string", Description: "Short role, such as 'Inbox Triage Specialist'."},
		"mission":            {Type: "string", Description: "Clear mission statement describing what this team member owns."},
		"style":              {Type: "string", Description: "Optional working style or voice guidance."},
		"allowedSkills":      {Type: "array", Description: "Skill namespaces this agent may use. Use bare namespace names as the canonical form: ['fs', 'terminal', 'websearch']. To restrict to a single action use the full action ID: 'fs.read_file'. Wildcard ('fs.*') and dot-suffix ('fs.') forms are accepted and normalized to bare namespace on save.", Items: &skills.ToolParam{Type: "string"}},
		"allowedToolClasses": {Type: "array", Description: "Optional list of allowed tool classes such as ['read','local_write'].", Items: &skills.ToolParam{Type: "string"}},
		"autonomy":           {Type: "string", Description: "Autonomy mode, for example assistive, autonomous, or supervised."},
		"activation":         {Type: "string", Description: "Optional activation hint describing when Atlas should reach for this team member."},
		"providerType":       {Type: "string", Description: "Optional AI provider override for this agent, e.g. 'anthropic', 'openai', 'gemini'. Defaults to Atlas's active provider."},
		"model":              {Type: "string", Description: "Optional model override for this agent. Only used when providerType is also set."},
		"enabled":            {Type: "boolean", Description: "Whether the team member starts enabled. Defaults to true."},
	}
}

func agentUpdateProperties() map[string]skills.ToolParam {
	props := agentDefinitionProperties()
	props["id"] = skills.ToolParam{Type: "string", Description: "Exact agent ID to update."}
	return props
}

func (m *Module) agentList(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	agents, err := m.listJoinedAgents()
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to list team members: %w", err)
	}
	return skills.OKResult(fmt.Sprintf("Found %d team member(s).", len(agents)), map[string]any{"agents": agents}), nil
}

func (m *Module) agentGet(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p agentRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return skills.ToolResult{}, fmt.Errorf("id is required")
	}
	agent, ok, err := m.getJoinedAgent(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load team member: %w", err)
	}
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", p.ID)
	}
	return skills.OKResult(fmt.Sprintf("Team member %q loaded.", agent.Name), map[string]any{"agent": agent}), nil
}

func (m *Module) agentCreate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p agentCreateArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	def := agentDefinition{
		Name:               strings.TrimSpace(p.Name),
		ID:                 strings.TrimSpace(p.ID),
		Role:               strings.TrimSpace(p.Role),
		Mission:            strings.TrimSpace(p.Mission),
		Style:              strings.TrimSpace(p.Style),
		AllowedSkills:      p.AllowedSkills,
		AllowedToolClasses: p.AllowedToolClasses,
		Autonomy:           strings.TrimSpace(p.Autonomy),
		Activation:         strings.TrimSpace(p.Activation),
		ProviderType:       strings.TrimSpace(p.ProviderType),
		Model:              strings.TrimSpace(p.Model),
		Enabled:            true,
	}
	if p.Enabled != nil {
		def.Enabled = *p.Enabled
	}
	def = normalizeDefinition(def)
	if def.ID == "" {
		def.ID = slugID(def.Name)
	}
	if err := validateDefinition(def); err != nil {
		return skills.ToolResult{}, err
	}
	if m.skills != nil {
		if m.skills.FilteredByPatterns(def.AllowedSkills).ToolCount() == 0 {
			return skills.ToolResult{}, fmt.Errorf("allowedSkills %v match no registered skills — check the patterns", def.AllowedSkills)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	existing, _ := m.store.GetAgentDefinition(def.ID)
	if existing != nil {
		return skills.ToolResult{}, fmt.Errorf("team member id already exists: %s", def.ID)
	}
	row := fileDefToRow(def, now, now)
	if err := m.store.SaveAgentDefinition(row); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to create team member: %w", err)
	}
	_ = m.store.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: def.ID, Status: "idle", UpdatedAt: now})
	agent, ok, err := m.getJoinedAgent(def.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load created team member: %w", err)
	}
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("created team member missing after save")
	}
	return skills.OKResult(fmt.Sprintf("Team member %q created.", agent.Name), map[string]any{"agent": agent}), nil
}

func (m *Module) agentUpdate(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p agentUpdateArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return skills.ToolResult{}, fmt.Errorf("id is required")
	}
	existingRow, err := m.store.GetAgentDefinition(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load team member: %w", err)
	}
	if existingRow == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", p.ID)
	}
	current := rowToFileDef(*existingRow)
	patch := agentDefinition{
		Name:               strings.TrimSpace(p.Name),
		Role:               strings.TrimSpace(p.Role),
		Mission:            strings.TrimSpace(p.Mission),
		Style:              strings.TrimSpace(p.Style),
		AllowedSkills:      p.AllowedSkills,
		AllowedToolClasses: p.AllowedToolClasses,
		Autonomy:           strings.TrimSpace(p.Autonomy),
		Activation:         strings.TrimSpace(p.Activation),
		ProviderType:       strings.TrimSpace(p.ProviderType),
		Model:              strings.TrimSpace(p.Model),
		Enabled:            current.Enabled,
	}
	if p.Enabled != nil {
		patch.Enabled = *p.Enabled
	}
	updated := mergeDefinition(current, patch)
	updated.ID = p.ID
	if err := validateDefinition(updated); err != nil {
		return skills.ToolResult{}, err
	}
	if m.skills != nil {
		if m.skills.FilteredByPatterns(updated.AllowedSkills).ToolCount() == 0 {
			return skills.ToolResult{}, fmt.Errorf("allowedSkills %v match no registered skills — check the patterns", updated.AllowedSkills)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := fileDefToRow(updated, existingRow.CreatedAt, now)
	if err := m.store.SaveAgentDefinition(row); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update team member: %w", err)
	}
	agent, ok, err := m.getJoinedAgent(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load updated team member: %w", err)
	}
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("updated team member missing after save")
	}
	return skills.OKResult(fmt.Sprintf("Team member %q updated.", agent.Name), map[string]any{"agent": agent}), nil
}

func (m *Module) agentDelete(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var p agentRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return skills.ToolResult{}, fmt.Errorf("id is required")
	}
	existingRow, err := m.store.GetAgentDefinition(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load team member: %w", err)
	}
	if existingRow == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", p.ID)
	}
	name := existingRow.Name
	if _, err := m.store.DeleteAgentDefinition(p.ID); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to delete team member: %w", err)
	}
	_, _ = m.store.DeleteAgentRuntime(p.ID)
	return skills.OKResult(fmt.Sprintf("Team member %q deleted.", name), map[string]any{"id": p.ID, "name": name, "deleted": true}), nil
}

func (m *Module) agentEnable(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.setAgentEnabled(args, true)
}

func (m *Module) agentDisable(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.setAgentEnabled(args, false)
}

func (m *Module) setAgentEnabled(args json.RawMessage, enabled bool) (skills.ToolResult, error) {
	var p agentRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return skills.ToolResult{}, fmt.Errorf("id is required")
	}
	// Phase 8: DB-first — load from DB, flip enabled flag, save back.
	existingRow, err := m.store.GetAgentDefinition(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load team member: %w", err)
	}
	if existingRow == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", p.ID)
	}
	existingRow.IsEnabled = enabled
	existingRow.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.store.SaveAgentDefinition(*existingRow); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update team member state: %w", err)
	}
	agent, ok, err := m.getJoinedAgent(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load updated team member: %w", err)
	}
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("updated team member missing after save")
	}
	word := "disabled"
	if enabled {
		word = "enabled"
	}
	return skills.OKResult(fmt.Sprintf("Team member %q %s.", agent.Name, word), map[string]any{"agent": agent}), nil
}

func (m *Module) agentPause(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.setAgentRuntimeState(args, "paused")
}

func (m *Module) agentResume(_ context.Context, args json.RawMessage) (skills.ToolResult, error) {
	return m.setAgentRuntimeState(args, "idle")
}

func (m *Module) setAgentRuntimeState(args json.RawMessage, status string) (skills.ToolResult, error) {
	var p agentRefArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return skills.ToolResult{}, fmt.Errorf("id is required")
	}
	def, err := m.store.GetAgentDefinition(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load team member: %w", err)
	}
	if def == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", p.ID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	runtimeRow, err := m.store.GetAgentRuntime(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load runtime state: %w", err)
	}
	if runtimeRow == nil {
		runtimeRow = &storage.AgentRuntimeRow{AgentID: p.ID}
	}
	runtimeRow.Status = status
	runtimeRow.UpdatedAt = now
	if status != "paused" {
		runtimeRow.LastError = nil
	}
	if err := m.store.SaveAgentRuntime(*runtimeRow); err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to update runtime state: %w", err)
	}
	agent, ok, err := m.getJoinedAgent(p.ID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load updated team member: %w", err)
	}
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("updated team member missing after runtime update")
	}
	word := "resumed"
	if status == "paused" {
		word = "paused"
	}
	return skills.OKResult(fmt.Sprintf("Team member %q %s.", agent.Name, word), map[string]any{"agent": agent}), nil
}

// ── DelegationPlan input (Phase 4) ───────────────────────────────────────────

// teamDelegatePlanArgs is the wire-level input for team.delegate.
// It accepts both the new structured DelegationPlan format and the legacy flat
// format for backward compatibility.
type teamDelegatePlanArgs struct {
	// Structured plan fields (Phase 4+)
	Mode          string               `json:"mode"`
	Pattern       string               `json:"pattern"`
	ExecutionMode string               `json:"executionMode"`
	Tasks         []DelegationTaskSpec `json:"tasks"`

	// Flat args backward-compat shim (Phase 3 format)
	// Detected when Tasks is empty and AgentID is non-empty.
	AgentID string `json:"agentID"`
	Task    string `json:"task"`
	Goal    string `json:"goal"`
}

// validateDelegationPlan checks basic plan-level invariants per spec §11.
// It does not redesign or mutate the plan.
func validateDelegationPlan(plan DelegationPlan) error {
	if plan.Mode == "" {
		return fmt.Errorf("plan.mode is required")
	}
	validModes := map[string]bool{"solo": true, "specialist_assist": true, "team_lead": true}
	if !validModes[plan.Mode] {
		return fmt.Errorf("plan.mode %q is not valid (must be solo, specialist_assist, or team_lead)", plan.Mode)
	}
	if plan.Pattern == "" {
		return fmt.Errorf("plan.pattern is required")
	}
	// "parallel" is defined in the type system but not yet implemented.
	// Reject it here (before any execution) so the caller gets a clear error
	// rather than passing validation and failing silently during dispatch.
	if plan.Pattern == "parallel" {
		return fmt.Errorf("plan.pattern \"parallel\" is not yet implemented — use single or sequence")
	}
	validPatterns := map[string]bool{"single": true, "sequence": true}
	if !validPatterns[plan.Pattern] {
		return fmt.Errorf("plan.pattern %q is not valid (must be single or sequence)", plan.Pattern)
	}
	if len(plan.Tasks) == 0 {
		return fmt.Errorf("plan.tasks must not be empty")
	}
	if plan.Pattern == "single" && len(plan.Tasks) > 1 {
		return fmt.Errorf("plan.pattern is single but %d tasks were provided (must be exactly 1)", len(plan.Tasks))
	}
	for i, spec := range plan.Tasks {
		if strings.TrimSpace(spec.AgentID) == "" {
			return fmt.Errorf("task[%d]: agentId is required", i)
		}
		if strings.TrimSpace(spec.Objective) == "" {
			return fmt.Errorf("task[%d]: objective is required", i)
		}
		validOutputTypes := map[string]bool{"summary": true, "findings_list": true, "structured_brief": true, "artifact_update": true}
		if spec.ExpectedOutput.Type != "" && !validOutputTypes[spec.ExpectedOutput.Type] {
			return fmt.Errorf("task[%d]: expectedOutput.type %q is not valid", i, spec.ExpectedOutput.Type)
		}
	}
	return nil
}

// specToDelegateArgs converts one DelegationTaskSpec into delegateArgs for delegateTask().
// JSON payload fields are marshalled to strings for storage; errors are non-fatal
// (defaults to empty JSON).
func specToDelegateArgs(spec DelegationTaskSpec, plan DelegationPlan) delegateArgs {
	scopeJSON, _ := json.Marshal(spec.Scope)
	successJSON, _ := json.Marshal(spec.SuccessCriteria)
	inputJSON, _ := json.Marshal(spec.InputContext)
	outputJSON, _ := json.Marshal(spec.ExpectedOutput)
	dependsJSON, _ := json.Marshal(spec.DependsOn)

	requestedBy := spec.RequestedBy
	if requestedBy == "" {
		requestedBy = "atlas"
	}
	// Use objective as the primary task instruction; fall back to title.
	task := spec.Objective
	if task == "" {
		task = spec.Title
	}
	goal := task
	if spec.InputContext.AtlasTaskFrame != "" {
		goal = spec.InputContext.AtlasTaskFrame
	}

	return delegateArgs{
		AgentID:             spec.AgentID,
		Task:                task,
		Goal:                goal,
		RequestedBy:         requestedBy,
		Title:               spec.Title,
		Objective:           spec.Objective,
		ScopeJSON:           string(scopeJSON),
		SuccessCriteriaJSON: string(successJSON),
		InputContextJSON:    string(inputJSON),
		ExpectedOutputJSON:  string(outputJSON),
		Mode:                orDefault(plan.ExecutionMode, "sync_assist"),
		Pattern:             plan.Pattern,
		DependsOnJSON:       string(dependsJSON),
	}
}

// teamDelegate is the Phase 4 handler for the team.delegate skill.
// It accepts a structured DelegationPlan and routes it through validate →
// materialize → execute. Falls back to flat args (agentID + task) for
// backward compatibility with the Phase 3 shim format.
func (m *Module) teamDelegate(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var input teamDelegatePlanArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}

	// ── Backward compat detection ─────────────────────────────────────────────
	// If tasks is empty but agentID is present, the model sent the old flat format.
	// Wrap it in a minimal DelegationPlan so the rest of the path is uniform.
	if len(input.Tasks) == 0 && strings.TrimSpace(input.AgentID) != "" {
		input.Tasks = []DelegationTaskSpec{{
			AgentID:      strings.TrimSpace(input.AgentID),
			Title:        strings.TrimSpace(input.Task),
			Objective:    strings.TrimSpace(input.Task),
			InputContext: DelegationInputContext{AtlasTaskFrame: strings.TrimSpace(input.Goal)},
			ExpectedOutput: DelegationExpectedOutput{Type: "summary"},
		}}
		if input.Pattern == "" {
			input.Pattern = "single"
		}
		if input.Mode == "" {
			input.Mode = "specialist_assist"
		}
		if input.ExecutionMode == "" {
			input.ExecutionMode = "sync_assist"
		}
	}

	if len(input.Tasks) == 0 {
		return skills.ToolResult{}, fmt.Errorf("team.delegate requires either tasks[] (structured plan) or agentID+task (flat format)")
	}

	// Build the canonical DelegationPlan for validation.
	plan := DelegationPlan{
		Mode:          orDefault(input.Mode, "specialist_assist"),
		Pattern:       orDefault(input.Pattern, "single"),
		ExecutionMode: orDefault(input.ExecutionMode, "sync_assist"),
		Tasks:         input.Tasks,
	}

	// ── C1: Normalize simple sequence steps ──────────────────────────────────
	// When the model sends {agentId, task} in a sequence tasks array (simple form),
	// the "task" field is a wire alias. Promote it to Objective (and Title when
	// Title is also empty) so validateDelegationPlan does not reject the spec.
	for i := range plan.Tasks {
		t := strings.TrimSpace(plan.Tasks[i].Task)
		if t != "" {
			if strings.TrimSpace(plan.Tasks[i].Objective) == "" {
				plan.Tasks[i].Objective = t
			}
			if strings.TrimSpace(plan.Tasks[i].Title) == "" {
				plan.Tasks[i].Title = t
			}
		}
	}

	// ── Validate ──────────────────────────────────────────────────────────────
	if err := validateDelegationPlan(plan); err != nil {
		return skills.ToolResult{}, fmt.Errorf("plan validation failed: %w", err)
	}

	// ── Check guards before any execution ────────────────────────────────────
	if m.skills == nil {
		return skills.ToolResult{}, fmt.Errorf("team delegation is unavailable: skill registry not configured")
	}
	if m.db == nil {
		return skills.ToolResult{}, fmt.Errorf("team delegation is unavailable: database not configured")
	}

	delegate := m.delegateFn
	if delegate == nil {
		delegate = m.delegateTask
	}

	// ── D5: sequence opportunity logging (debug only, no behavior change) ────
	// When two single-pattern calls occur within 30 seconds, the model is likely
	// making back-to-back delegations that could have been one sequence call.
	if plan.Pattern == "single" {
		now := time.Now()
		const window = 30 * time.Second
		if prev, ok := m.lastSingleDelegateAt.Load("single"); ok {
			if now.Sub(prev.(time.Time)) < window {
				logstore.Write("debug", "sequence opportunity detected: second single-pattern team.delegate within 30s — consider pattern=sequence if step 2 depends on step 1", map[string]string{
					"pattern": "single",
					"agent":   plan.Tasks[0].AgentID,
				})
			}
		}
		m.lastSingleDelegateAt.Store("single", now)
	} else {
		// Non-single call resets the window.
		m.lastSingleDelegateAt.Delete("single")
	}

	// ── Route by pattern ──────────────────────────────────────────────────────
	switch plan.Pattern {
	case "single":
		spec := plan.Tasks[0]
		def, err := m.store.GetAgentDefinition(spec.AgentID)
		if err != nil {
			return skills.ToolResult{}, fmt.Errorf("failed to load team member %q: %w", spec.AgentID, err)
		}
		if def == nil {
			return skills.ToolResult{}, fmt.Errorf("team member %q not found", spec.AgentID)
		}
		if !def.IsEnabled {
			return skills.ToolResult{}, fmt.Errorf("team member %q is disabled", spec.AgentID)
		}

		dArgs := specToDelegateArgs(spec, plan)

		// ── Async assignment (Phase 5) ────────────────────────────────────────
		// When executionMode is async_assignment, pre-generate the task ID so
		// it can be returned immediately. The goroutine uses context.Background()
		// so it outlives the skill call context.
		if plan.ExecutionMode == "async_assignment" {
			taskID := newID("teamtask")
			dArgs.TaskID = taskID
			defCopy := *def
			// Capture the originating Atlas conversation ID before the goroutine
			// so async completion can push a follow-up to the right conversation.
			originConvID := chat.OriginConvIDFromCtx(ctx)
			go func() {
				run, err := delegate(context.Background(), defCopy, dArgs)
				if originConvID != "" && chat.AsyncFollowUpSender != nil {
					chat.AsyncFollowUpSender(originConvID, asyncFollowUpText(defCopy.Name, taskID, run, err))
				}
			}()
			return skills.OKResult(
				fmt.Sprintf("Task assigned to %s asynchronously. Poll GET /agents/tasks/%s for status.", def.Name, taskID),
				map[string]any{
					"taskID":  taskID,
					"agentID": spec.AgentID,
					"status":  "queued",
				},
			), nil
		}

		run, err := delegate(ctx, *def, dArgs)
		if err != nil {
			return skills.ToolResult{}, err
		}
		artifacts := map[string]any{
			"taskID":  run.Task.TaskID,
			"agentID": run.Task.AgentID,
			"status":  run.Task.Status,
			"goal":    run.Task.Goal,
			"steps":   len(run.Steps),
			"result":  safeString(run.Task.ResultSummary),
			"error":   safeString(run.Task.ErrorMessage),
		}
		summary := fmt.Sprintf("Delegated to %s. Task status: %s.", def.Name, run.Task.Status)
		if run.Task.ResultSummary != nil && strings.TrimSpace(*run.Task.ResultSummary) != "" {
			summary = fmt.Sprintf("Delegated to %s. %s", def.Name, strings.TrimSpace(*run.Task.ResultSummary))
		}
		return skills.OKResult(summary, artifacts), nil

	case "sequence":
		// Execute each DelegationTaskSpec in order, preserving full V1 metadata
		// (scope, success criteria, expected output, input context) per step.
		// Previous step output is appended to each subsequent step's task instruction.
		type seqStepResult struct {
			Step    int    `json:"step"`
			AgentID string `json:"agentID"`
			TaskID  string `json:"taskID"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
			Error   string `json:"error,omitempty"`
		}
		seqResults := make([]seqStepResult, 0, len(plan.Tasks))
		prevSummary := ""

		for i, spec := range plan.Tasks {
			def, err := m.store.GetAgentDefinition(spec.AgentID)
			if err != nil {
				return skills.ToolResult{}, fmt.Errorf("sequence step %d: failed to load agent %q: %w", i+1, spec.AgentID, err)
			}
			if def == nil {
				return skills.ToolResult{}, fmt.Errorf("sequence step %d: agent %q not found", i+1, spec.AgentID)
			}
			if !def.IsEnabled {
				return skills.ToolResult{}, fmt.Errorf("sequence step %d: agent %q is disabled", i+1, spec.AgentID)
			}

			dArgs := specToDelegateArgs(spec, plan)
			// Append prior-step output so each agent has full context from the chain.
			if prevSummary != "" {
				dArgs.Task += "\n\nOutput from previous step:\n" + prevSummary
			}

			run, err := delegate(ctx, *def, dArgs)
			sr := seqStepResult{Step: i + 1, AgentID: spec.AgentID}
			if err != nil {
				sr.Status = "error"
				sr.Error = err.Error()
				seqResults = append(seqResults, sr)
				return skills.OKResult(
					fmt.Sprintf("Sequence stopped at step %d (%s): %s", i+1, spec.AgentID, err.Error()),
					map[string]any{"stepsCompleted": i, "stepsTotal": len(plan.Tasks), "results": seqResults, "status": "partial"},
				), nil
			}

			sr.TaskID = run.Task.TaskID
			sr.Status = run.Task.Status
			sr.Summary = safeString(run.Task.ResultSummary)
			if run.Task.ErrorMessage != nil {
				sr.Error = *run.Task.ErrorMessage
			}
			seqResults = append(seqResults, sr)

			if run.Task.Status == "error" || run.Task.Status == "cancelled" {
				return skills.OKResult(
					fmt.Sprintf("Sequence stopped at step %d (%s): task %s", i+1, spec.AgentID, run.Task.Status),
					map[string]any{"stepsCompleted": i, "stepsTotal": len(plan.Tasks), "results": seqResults, "status": "partial"},
				), nil
			}

			prevSummary = sr.Summary
		}

		finalSummary := fmt.Sprintf("Sequential task completed (%d steps).", len(seqResults))
		if prevSummary != "" {
			finalSummary = prevSummary
		}
		return skills.OKResult(finalSummary, map[string]any{
			"stepsCompleted": len(seqResults),
			"stepsTotal":     len(plan.Tasks),
			"results":        seqResults,
			"status":         "completed",
		}), nil

	default:
		// Should not be reached — validateDelegationPlan rejects unknown patterns.
		return skills.ToolResult{}, fmt.Errorf("plan.pattern %q is not supported", plan.Pattern)
	}
}

// orDefault returns val if non-empty, otherwise returns fallback.
func orDefault(val, fallback string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return fallback
}

// orJSONDefault returns val if it is non-empty JSON, otherwise returns fallback.
func orJSONDefault(val, fallback string) string {
	if strings.TrimSpace(val) != "" && val != "null" {
		return val
	}
	return fallback
}

func (m *Module) agentDelegate(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var params delegateArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	params.AgentID = strings.TrimSpace(params.AgentID)
	params.Task = strings.TrimSpace(params.Task)
	params.Goal = strings.TrimSpace(params.Goal)
	if params.AgentID == "" || params.Task == "" {
		return skills.ToolResult{}, fmt.Errorf("agentID and task are required")
	}

	def, err := m.store.GetAgentDefinition(params.AgentID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load agent definition: %w", err)
	}
	if def == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", params.AgentID)
	}
	if !def.IsEnabled {
		return skills.ToolResult{}, fmt.Errorf("agent %q is disabled", params.AgentID)
	}
	if m.skills == nil {
		return skills.ToolResult{}, fmt.Errorf("agent delegation is unavailable: skill registry not configured")
	}
	if m.db == nil {
		return skills.ToolResult{}, fmt.Errorf("agent delegation is unavailable: database not configured")
	}

	delegate := m.delegateFn
	if delegate == nil {
		delegate = m.delegateTask
	}
	run, err := delegate(ctx, *def, params)
	if err != nil {
		return skills.ToolResult{}, err
	}
	artifacts := map[string]any{
		"taskID":  run.Task.TaskID,
		"agentID": run.Task.AgentID,
		"status":  run.Task.Status,
		"goal":    run.Task.Goal,
		"steps":   len(run.Steps),
		"result":  safeString(run.Task.ResultSummary),
		"error":   safeString(run.Task.ErrorMessage),
	}
	summary := fmt.Sprintf("Delegated to %s. Task status: %s.", def.Name, run.Task.Status)
	if run.Task.ResultSummary != nil && strings.TrimSpace(*run.Task.ResultSummary) != "" {
		summary = fmt.Sprintf("Delegated to %s. %s", def.Name, strings.TrimSpace(*run.Task.ResultSummary))
	}
	return skills.OKResult(summary, artifacts), nil
}

type delegatedRun struct {
	Task  storage.AgentTaskRow
	Steps []storage.AgentTaskStepRow
}

func (m *Module) delegateTask(ctx context.Context, def storage.AgentDefinitionRow, params delegateArgs) (delegatedRun, error) {
	// Guard: refuse to start a new task if the agent is already busy.
	if rt, err := m.store.GetAgentRuntime(def.ID); err == nil && rt != nil {
		if rt.Status == "busy" {
			return delegatedRun{}, fmt.Errorf("agent %s is already busy (task %s)", def.ID,
				safeString(rt.CurrentTaskID))
		}
	}

	provider, err := m.resolveProviderFor(def)
	if err != nil {
		return delegatedRun{}, fmt.Errorf("failed to resolve provider for delegated task: %w", err)
	}

	subRegistry, err := m.subRegistryFor(def)
	if err != nil {
		return delegatedRun{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Use caller-supplied task ID when pre-generated (async_assignment path).
	taskID := params.TaskID
	if taskID == "" {
		taskID = newID("teamtask")
	}
	convID := taskID
	goal := params.Task
	if params.Goal != "" {
		goal = params.Goal
	}
	requestedBy := params.RequestedBy
	if requestedBy == "" {
		requestedBy = "atlas"
	}
	taskRow := storage.AgentTaskRow{
		TaskID:         taskID,
		AgentID:        def.ID,
		Status:         "running",
		Goal:           goal,
		RequestedBy:    requestedBy,
		ConversationID: &convID,
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
		// V1 structured payload (Phase 4): populated when team.delegate carries
		// a DelegationPlan; zero-value defaults are fine for the legacy path.
		Title:               params.Title,
		Objective:           params.Objective,
		ScopeJSON:           orJSONDefault(params.ScopeJSON, "{}"),
		SuccessCriteriaJSON: orJSONDefault(params.SuccessCriteriaJSON, "{}"),
		InputContextJSON:    orJSONDefault(params.InputContextJSON, "{}"),
		ExpectedOutputJSON:  orJSONDefault(params.ExpectedOutputJSON, "{}"),
		Mode:                orDefault(params.Mode, "sync_assist"),
		Pattern:             orDefault(params.Pattern, "single"),
		DependsOnJSON:       orJSONDefault(params.DependsOnJSON, "[]"),
	}
	if err := m.store.SaveAgentTask(taskRow); err != nil {
		return delegatedRun{}, fmt.Errorf("failed to persist team task: %w", err)
	}
	if err := m.recordEvent("team.task.started", &def.ID, &taskID, fmt.Sprintf("Delegated task started for %s", def.Name), strPtr(goal), map[string]any{
		"taskID":  taskID,
		"agentID": def.ID,
		"goal":    goal,
	}); err != nil {
		logstore.Write("warn", fmt.Sprintf("team: failed to record task.started event for agent %q task %s: %v", def.ID, taskID, err), nil)
	}

	if runtimeRow, err := m.store.GetAgentRuntime(def.ID); err != nil {
		logstore.Write("warn", fmt.Sprintf("team: failed to load runtime state for agent %q before task %s: %v", def.ID, taskID, err), nil)
	} else {
		if runtimeRow == nil {
			runtimeRow = &storage.AgentRuntimeRow{AgentID: def.ID}
		}
		runtimeRow.Status = "busy"
		runtimeRow.CurrentTaskID = &taskID
		runtimeRow.LastActiveAt = &now
		runtimeRow.UpdatedAt = now
		if saveErr := m.store.SaveAgentRuntime(*runtimeRow); saveErr != nil {
			logstore.Write("warn", fmt.Sprintf("team: failed to mark agent %q as busy for task %s: %v", def.ID, taskID, saveErr), nil)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.taskCancels.Store(taskID, cancel)
	defer func() {
		cancel()
		m.taskCancels.Delete(taskID)
	}()

	recorder := &taskStepRecorder{
		store:       m.store,
		recordEvent: m.recordEvent,
		taskID:      taskID,
		agentID:     def.ID,
	}
	systemPrompt := composeWorkerPrompt(def, taskRow)
	_ = recorder.append("system", "system", systemPrompt, nil, nil)
	_ = recorder.append("user", "user", params.Task, nil, nil)

	loop := agent.Loop{
		Skills: subRegistry,
		BC:     recorder,
		DB:     m.db,
	}
	tools := subRegistry.ToolDefinitions()
	result := loop.Run(runCtx, agent.LoopConfig{
		Provider:      provider,
		MaxIterations: 8,
		SupportDir:    m.supportDir,
		ConvID:        convID,
		AgentID:       def.ID,
		Tools:         tools,
		UserMessage:   params.Task,
	}, []agent.OAIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: params.Task},
	}, convID)

	// Track how many iterations the loop consumed for budget accounting.
	if result.IterationsUsed > 0 {
		_ = m.store.AddAgentTaskIterations(taskID, result.IterationsUsed)
	}

	return m.finishDelegatedRun(ctx, def, taskRow, recorder, result, runCtx.Err())
}

func (m *Module) subRegistryFor(def storage.AgentDefinitionRow) (*skills.Registry, error) {
	var allowedSkills []string
	if err := json.Unmarshal([]byte(def.AllowedSkillsJSON), &allowedSkills); err != nil {
		return nil, fmt.Errorf("failed to parse allowedSkills for %q: %w", def.ID, err)
	}
	var allowedToolClasses []string
	if def.AllowedToolClassesJSON != "" && def.AllowedToolClassesJSON != "null" {
		_ = json.Unmarshal([]byte(def.AllowedToolClassesJSON), &allowedToolClasses)
	}
	subRegistry := m.skills.FilteredByPatterns(allowedSkills).FilteredByActionClasses(allowedToolClasses)
	if subRegistry.ToolCount() == 0 {
		return nil, fmt.Errorf("agent %q has no runnable skills after applying allowedSkills/allowedToolClasses", def.ID)
	}
	return subRegistry, nil
}

func (m *Module) finishDelegatedRun(ctx context.Context, def storage.AgentDefinitionRow, taskRow storage.AgentTaskRow, recorder *taskStepRecorder, result agent.RunResult, runErr error) (delegatedRun, error) {
	finishedAt := time.Now().UTC().Format(time.RFC3339Nano)
	taskID := taskRow.TaskID
	taskRow.Status = result.Status
	if taskRow.Status == "complete" {
		taskRow.Status = "completed"
	}
	taskRow.UpdatedAt = finishedAt
	taskRow.FinishedAt = &finishedAt
	if strings.TrimSpace(result.FinalText) != "" {
		taskRow.ResultSummary = strPtr(strings.TrimSpace(result.FinalText))
		_ = recorder.append("assistant", "assistant", strings.TrimSpace(result.FinalText), nil, nil)
	}
	if result.Error != nil {
		taskRow.Status = "error"
		taskRow.ErrorMessage = strPtr(result.Error.Error())
		if runErr == context.Canceled {
			taskRow.Status = "cancelled"
			taskRow.ResultSummary = strPtr("Task cancelled.")
			taskRow.ErrorMessage = nil
		}
	}
	if result.Status == "pendingApproval" {
		taskRow.Status = "pending_approval"
		taskRow.FinishedAt = nil
		summary := fmt.Sprintf("Delegated task is waiting for approval on %d tool call(s).", len(result.PendingApprovals))
		taskRow.ResultSummary = &summary
	}
	if err := m.store.SaveAgentTask(taskRow); err != nil {
		return delegatedRun{}, fmt.Errorf("failed to update team task: %w", err)
	}

	if runtimeRow, err := m.store.GetAgentRuntime(def.ID); err != nil {
		logstore.Write("error", fmt.Sprintf("team: failed to load runtime state for agent %q after task %s — agent may appear stuck as busy: %v", def.ID, taskID, err), nil)
	} else {
		if runtimeRow == nil {
			runtimeRow = &storage.AgentRuntimeRow{AgentID: def.ID}
		}
		runtimeRow.Status = "idle"
		if taskRow.Status == "pending_approval" {
			runtimeRow.Status = "approval_needed"
		}
		runtimeRow.CurrentTaskID = nil
		runtimeRow.LastActiveAt = &finishedAt
		runtimeRow.UpdatedAt = finishedAt
		if result.Error != nil {
			runtimeRow.LastError = strPtr(result.Error.Error())
		} else {
			runtimeRow.LastError = nil
		}
		if saveErr := m.store.SaveAgentRuntime(*runtimeRow); saveErr != nil {
			logstore.Write("error", fmt.Sprintf("team: failed to update runtime state for agent %q after task %s — agent may appear stuck as busy: %v", def.ID, taskID, saveErr), nil)
		}
	}
	busPayload := map[string]any{
		"taskID":   taskID,
		"agentID":  def.ID,
		"agent_id": def.ID,
		"status":   taskRow.Status,
	}
	switch taskRow.Status {
	case "completed":
		if err := m.recordEvent("team.task.completed", &def.ID, &taskID, fmt.Sprintf("Delegated task completed by %s", def.Name), taskRow.ResultSummary, busPayload); err != nil {
			logstore.Write("warn", fmt.Sprintf("team: failed to record task.completed event for agent %q task %s: %v", def.ID, taskID, err), nil)
		}
		if m.bus != nil {
			_ = m.bus.Publish(ctx, "agent.task.completed", busPayload)
		}
	case "pending_approval":
		if err := m.recordEvent("team.task.pending_approval", &def.ID, &taskID, fmt.Sprintf("Delegated task paused for approval: %s", def.Name), taskRow.ResultSummary, busPayload); err != nil {
			logstore.Write("warn", fmt.Sprintf("team: failed to record task.pending_approval event for agent %q task %s: %v", def.ID, taskID, err), nil)
		}
	case "cancelled":
		if err := m.recordEvent("team.task.cancelled", &def.ID, &taskID, fmt.Sprintf("Delegated task cancelled for %s", def.Name), taskRow.ResultSummary, busPayload); err != nil {
			logstore.Write("warn", fmt.Sprintf("team: failed to record task.cancelled event for agent %q task %s: %v", def.ID, taskID, err), nil)
		}
	case "error":
		if err := m.recordEvent("team.task.failed", &def.ID, &taskID, fmt.Sprintf("Delegated task failed for %s", def.Name), taskRow.ErrorMessage, busPayload); err != nil {
			logstore.Write("warn", fmt.Sprintf("team: failed to record task.failed event for agent %q task %s: %v", def.ID, taskID, err), nil)
		}
		if m.bus != nil {
			_ = m.bus.Publish(ctx, "agent.task.failed", busPayload)
		}
	}

	steps, _ := m.store.ListAgentTaskSteps(taskID)
	m.updateMetrics(ctx, def.ID, taskRow.Status, steps, finishedAt)

	return delegatedRun{Task: taskRow, Steps: steps}, nil
}

func (m *Module) resumeDelegatedTask(ctx context.Context, def storage.AgentDefinitionRow, taskRow storage.AgentTaskRow, toolCallID string, approved bool) (delegatedRun, error) {
	if m.db == nil {
		return delegatedRun{}, fmt.Errorf("agent task resume is unavailable: database not configured")
	}
	subRegistry, err := m.subRegistryFor(def)
	if err != nil {
		return delegatedRun{}, err
	}
	provider, err := m.resolveProviderFor(def)
	if err != nil {
		return delegatedRun{}, fmt.Errorf("failed to resolve provider for delegated task resume: %w", err)
	}
	row, err := m.db.FetchDeferredByToolCallID(toolCallID)
	if err != nil {
		return delegatedRun{}, fmt.Errorf("failed to load deferred tool call: %w", err)
	}
	if row == nil {
		return delegatedRun{}, fmt.Errorf("approval not found for delegated task tool call %q", toolCallID)
	}
	var state struct {
		Messages  []agent.OAIMessage  `json:"messages"`
		ToolCalls []agent.OAIToolCall `json:"tool_calls"`
		ConvID    string              `json:"conv_id"`
	}
	if err := json.Unmarshal([]byte(row.NormalizedInputJSON), &state); err != nil {
		return delegatedRun{}, fmt.Errorf("failed to parse deferred state: %w", err)
	}
	convID := state.ConvID
	if convID == "" {
		if taskRow.ConversationID != nil && strings.TrimSpace(*taskRow.ConversationID) != "" {
			convID = *taskRow.ConversationID
		} else {
			convID = taskRow.TaskID
		}
	}
	var targetTC *agent.OAIToolCall
	for i := range state.ToolCalls {
		if state.ToolCalls[i].ID == toolCallID {
			targetTC = &state.ToolCalls[i]
			break
		}
	}
	if targetTC == nil {
		return delegatedRun{}, fmt.Errorf("tool call %q not found in deferred state", toolCallID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	newStatus := "denied"
	if approved {
		newStatus = "approved"
	}
	if err := m.db.UpdateDeferredStatus(toolCallID, newStatus, now); err != nil {
		return delegatedRun{}, fmt.Errorf("failed to update deferred tool call status: %w", err)
	}
	if m.bus != nil {
		_ = m.bus.Publish(ctx, "approval.resolved.v1", map[string]any{
			"toolCallID": toolCallID,
			"status":     newStatus,
		})
	}

	var toolResult string
	if approved {
		toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, execErr := subRegistry.Execute(toolCtx, targetTC.Function.Name, json.RawMessage(targetTC.Function.Arguments))
		cancel()
		if execErr != nil {
			toolResult = fmt.Sprintf("Tool execution error: %v", execErr)
		} else {
			toolResult = res.FormatForModel()
		}
	} else {
		toolResult = "Action denied by user."
	}
	messages := append(state.Messages, agent.OAIMessage{
		Role:       "tool",
		Content:    toolResult,
		ToolCallID: toolCallID,
		Name:       targetTC.Function.Name,
	})

	// When multiple tool calls were deferred simultaneously (parallel tool use),
	// only toolCallID is being approved here. The others need a synthetic tool
	// result to keep the conversation valid, and their DB status must be updated
	// to reflect that they were resolved as part of this resume — not left stuck
	// in pending_approval indefinitely.
	pending, _ := m.store.FetchDeferredsByAgentTaskID(taskRow.TaskID, "pending_approval")
	for _, p := range pending {
		if p.ToolCallID == toolCallID {
			continue
		}
		actionID := ""
		if p.ActionID != nil {
			actionID = *p.ActionID
		}
		// Inject a synthetic result so the conversation stays coherent.
		messages = append(messages, agent.OAIMessage{
			Role:       "tool",
			Content:    "Action deferred (separate approval required).",
			ToolCallID: p.ToolCallID,
			Name:       actionID,
		})
		// Mark the DB row so it is not left stuck in pending_approval.
		if m.db != nil {
			_ = m.db.UpdateDeferredStatus(p.ToolCallID, "auto_denied", now)
		}
	}

	if runtimeRow, err := m.store.GetAgentRuntime(def.ID); err == nil {
		if runtimeRow == nil {
			runtimeRow = &storage.AgentRuntimeRow{AgentID: def.ID}
		}
		runtimeRow.Status = "busy"
		runtimeRow.CurrentTaskID = &taskRow.TaskID
		runtimeRow.LastActiveAt = &now
		runtimeRow.UpdatedAt = now
		runtimeRow.LastError = nil
		_ = m.store.SaveAgentRuntime(*runtimeRow)
	}

	existingSteps, _ := m.store.ListAgentTaskSteps(taskRow.TaskID)
	recorder := &taskStepRecorder{
		store:       m.store,
		recordEvent: m.recordEvent,
		taskID:      taskRow.TaskID,
		agentID:     def.ID,
		seq:         len(existingSteps),
	}
	_ = recorder.append("tool", "tool_resume_result", toolResult, &targetTC.Function.Name, &toolCallID)

	loop := agent.Loop{
		Skills: subRegistry,
		BC:     recorder,
		DB:     m.db,
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.taskCancels.Store(taskRow.TaskID, cancel)
	defer func() {
		cancel()
		m.taskCancels.Delete(taskRow.TaskID)
	}()
	taskRow.Status = "running"
	taskRow.UpdatedAt = now
	taskRow.FinishedAt = nil
	if err := m.store.SaveAgentTask(taskRow); err != nil {
		return delegatedRun{}, fmt.Errorf("failed to mark task running before resume: %w", err)
	}

	// Cap iterations so approvals don't reset the budget.
	const maxTaskIterations = 8
	remaining := maxTaskIterations - taskRow.IterationsUsed
	if remaining <= 0 {
		remaining = 1 // always allow at least one iteration to produce a final answer
	}
	result := loop.Run(runCtx, agent.LoopConfig{
		Provider:      provider,
		MaxIterations: remaining,
		SupportDir:    m.supportDir,
		ConvID:        convID,
		AgentID:       def.ID,
		Tools:         subRegistry.ToolDefinitions(),
		UserMessage:   taskRow.Goal,
	}, messages, convID)

	// Track iterations consumed during the resume run.
	if result.IterationsUsed > 0 {
		_ = m.store.AddAgentTaskIterations(taskRow.TaskID, result.IterationsUsed)
	}

	return m.finishDelegatedRun(ctx, def, taskRow, recorder, result, runCtx.Err())
}

// updateMetrics increments the agent_metrics row after a task finishes.
func (m *Module) updateMetrics(ctx context.Context, agentID, taskStatus string, steps []storage.AgentTaskStepRow, lastActiveAt string) {
	existing, _ := m.store.GetAgentMetrics(agentID)
	row := storage.AgentMetricsRow{
		AgentID:      agentID,
		LastActiveAt: &lastActiveAt,
		UpdatedAt:    lastActiveAt,
	}
	if existing != nil {
		row.TasksCompleted = existing.TasksCompleted
		row.TasksFailed = existing.TasksFailed
		row.TotalToolCalls = existing.TotalToolCalls
	}
	switch taskStatus {
	case "completed":
		row.TasksCompleted++
	case "error":
		row.TasksFailed++
	}
	for _, s := range steps {
		if s.StepType == "tool_started" {
			row.TotalToolCalls++
		}
	}
	if err := m.store.UpsertAgentMetrics(row); err != nil {
		logstore.Write("warn", fmt.Sprintf("team: failed to upsert metrics for agent %q: %v", agentID, err), nil)
	}
}

// agentSequence executes a sequential chain of delegated tasks, passing each
// result as context to the next agent in the chain.
// On step failure the chain stops immediately; completed steps are included in the response.
func (m *Module) agentSequence(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var params sequenceArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.Agents) == 0 {
		return skills.ToolResult{}, fmt.Errorf("agents list is required and must not be empty")
	}

	// Validate all steps up-front so we fail fast before starting any work.
	for i, step := range params.Agents {
		if strings.TrimSpace(step.AgentID) == "" || strings.TrimSpace(step.Task) == "" {
			return skills.ToolResult{}, fmt.Errorf("step %d: agentID and task are required", i+1)
		}
	}

	type stepResult struct {
		Step    int    `json:"step"`
		AgentID string `json:"agentID"`
		TaskID  string `json:"taskID"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
		Error   string `json:"error,omitempty"`
	}
	results := make([]stepResult, 0, len(params.Agents))
	prevSummary := ""
	overallGoal := params.Goal

	delegate := m.delegateFn
	if delegate == nil {
		delegate = m.delegateTask
	}

	for i, step := range params.Agents {
		step.AgentID = strings.TrimSpace(step.AgentID)
		step.Task = strings.TrimSpace(step.Task)

		def, err := m.store.GetAgentDefinition(step.AgentID)
		if err != nil {
			return skills.ToolResult{}, fmt.Errorf("step %d: failed to load agent %q: %w", i+1, step.AgentID, err)
		}
		if def == nil {
			return skills.ToolResult{}, fmt.Errorf("step %d: agent %q not found", i+1, step.AgentID)
		}
		if !def.IsEnabled {
			return skills.ToolResult{}, fmt.Errorf("step %d: agent %q is disabled", i+1, step.AgentID)
		}

		// Build task with context from previous step and the overall goal for orientation.
		task := step.Task
		if overallGoal != "" {
			task = fmt.Sprintf("Overall goal: %s\n\nYour step: %s", overallGoal, step.Task)
		}
		if prevSummary != "" {
			task += "\n\nOutput from previous step:\n" + prevSummary
		}

		run, err := delegate(ctx, *def, delegateArgs{
			AgentID:     step.AgentID,
			Task:        task,
			Goal:        overallGoal,
			RequestedBy: "atlas",
		})

		sr := stepResult{Step: i + 1, AgentID: step.AgentID}
		if err != nil {
			sr.Status = "error"
			sr.Error = err.Error()
			results = append(results, sr)
			// Return partial results — Atlas can decide what to do next.
			return skills.OKResult(
				fmt.Sprintf("Sequence stopped at step %d (%s): %s", i+1, step.AgentID, err.Error()),
				map[string]any{
					"stepsCompleted": i,
					"stepsTotal":     len(params.Agents),
					"results":        results,
					"status":         "partial",
				},
			), nil
		}

		sr.TaskID = run.Task.TaskID
		sr.Status = run.Task.Status
		sr.Summary = safeString(run.Task.ResultSummary)
		if run.Task.ErrorMessage != nil {
			sr.Error = *run.Task.ErrorMessage
		}
		results = append(results, sr)

		// Stop chain on task-level failure; return what was completed.
		if run.Task.Status == "error" || run.Task.Status == "cancelled" {
			return skills.OKResult(
				fmt.Sprintf("Sequence stopped at step %d (%s): task %s", i+1, step.AgentID, run.Task.Status),
				map[string]any{
					"stepsCompleted": i,
					"stepsTotal":     len(params.Agents),
					"results":        results,
					"status":         "partial",
				},
			), nil
		}

		prevSummary = sr.Summary
	}

	// All steps completed.
	finalSummary := fmt.Sprintf("Sequential task completed (%d steps).", len(results))
	if prevSummary != "" {
		finalSummary = prevSummary
	}
	return skills.OKResult(finalSummary, map[string]any{
		"stepsCompleted": len(results),
		"stepsTotal":     len(params.Agents),
		"results":        results,
		"status":         "completed",
	}), nil
}

// agentAssign is the skill handler for team.assign — identical to team.delegate
// but marks the task as requested by "user" rather than "atlas".
func (m *Module) agentAssign(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
	var params delegateArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return skills.ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
	}
	params.AgentID = strings.TrimSpace(params.AgentID)
	params.Task = strings.TrimSpace(params.Task)
	if params.AgentID == "" || params.Task == "" {
		return skills.ToolResult{}, fmt.Errorf("agentID and task are required")
	}
	def, err := m.store.GetAgentDefinition(params.AgentID)
	if err != nil {
		return skills.ToolResult{}, fmt.Errorf("failed to load agent definition: %w", err)
	}
	if def == nil {
		return skills.ToolResult{}, fmt.Errorf("agent %q not found", params.AgentID)
	}
	if !def.IsEnabled {
		return skills.ToolResult{}, fmt.Errorf("agent %q is disabled", params.AgentID)
	}
	params.RequestedBy = "user"
	delegate := m.delegateFn
	if delegate == nil {
		delegate = m.delegateTask
	}
	run, err := delegate(ctx, *def, params)
	if err != nil {
		return skills.ToolResult{}, err
	}
	summary := fmt.Sprintf("Assigned to %s. Task status: %s.", def.Name, run.Task.Status)
	if run.Task.ResultSummary != nil && strings.TrimSpace(*run.Task.ResultSummary) != "" {
		summary = fmt.Sprintf("Assigned to %s. %s", def.Name, strings.TrimSpace(*run.Task.ResultSummary))
	}
	return skills.OKResult(summary, map[string]any{
		"taskID":  run.Task.TaskID,
		"agentID": run.Task.AgentID,
		"status":  run.Task.Status,
		"steps":   len(run.Steps),
		"result":  safeString(run.Task.ResultSummary),
	}), nil
}


type taskStepRecorder struct {
	store       platform.AgentStore
	recordEvent func(eventType string, agentID, taskID *string, title string, detail *string, payload map[string]any) error
	taskID      string
	agentID     string
	mu          sync.Mutex
	seq         int
}

func (r *taskStepRecorder) Emit(_ string, event agent.EmitEvent) {
	record := r.recordEvent
	switch event.Type {
	case "tool_started":
		_ = r.append("tool", "tool_started", event.ToolName, &event.ToolName, &event.ToolCallID)
		if record != nil {
			_ = record("team.tool.started", &r.agentID, &r.taskID, fmt.Sprintf("Tool started: %s", event.ToolName), nil, map[string]any{"toolName": event.ToolName, "toolCallID": event.ToolCallID})
		}
	case "tool_finished":
		content := event.Result
		if strings.TrimSpace(content) == "" {
			content = event.ToolName
		}
		_ = r.append("tool", "tool_finished", content, &event.ToolName, &event.ToolCallID)
		if record != nil {
			_ = record("team.tool.finished", &r.agentID, &r.taskID, fmt.Sprintf("Tool finished: %s", event.ToolName), nil, map[string]any{"toolName": event.ToolName, "toolCallID": event.ToolCallID})
		}
	case "tool_failed":
		_ = r.append("tool", "tool_failed", event.Error, &event.ToolName, &event.ToolCallID)
		if record != nil {
			_ = record("team.tool.failed", &r.agentID, &r.taskID, fmt.Sprintf("Tool failed: %s", event.ToolName), strPtr(event.Error), map[string]any{"toolName": event.ToolName, "toolCallID": event.ToolCallID})
		}
	case "approval_required":
		_ = r.append("tool", "approval_required", event.Arguments, &event.ToolName, &event.ToolCallID)
		if record != nil {
			_ = record("team.tool.approval_required", &r.agentID, &r.taskID, fmt.Sprintf("Approval required: %s", event.ToolName), nil, map[string]any{"toolName": event.ToolName, "toolCallID": event.ToolCallID})
		}
	}
}

func (r *taskStepRecorder) Finish(string) {}

func (r *taskStepRecorder) append(role, stepType, content string, toolName, toolCallID *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.store.SaveAgentTaskStep(storage.AgentTaskStepRow{
		StepID:         newID("teamstep"),
		TaskID:         r.taskID,
		SequenceNumber: r.seq,
		Role:           role,
		StepType:       stepType,
		Content:        content,
		ToolName:       toolName,
		ToolCallID:     toolCallID,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func newID(prefix string) string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}

func strPtr(v string) *string {
	return &v
}

func safeString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// asyncFollowUpText builds the follow-up message delivered to the originating
// Atlas conversation when an async_assignment task completes.
func asyncFollowUpText(agentName, taskID string, run delegatedRun, err error) string {
	if err != nil {
		return fmt.Sprintf("%s finished (task %s) but encountered an error: %s", agentName, taskID, err.Error())
	}
	status := run.Task.Status
	if summary := safeString(run.Task.ResultSummary); strings.TrimSpace(summary) != "" {
		return fmt.Sprintf("%s finished: %s", agentName, strings.TrimSpace(summary))
	}
	return fmt.Sprintf("%s finished (task %s, status: %s).", agentName, taskID, status)
}
