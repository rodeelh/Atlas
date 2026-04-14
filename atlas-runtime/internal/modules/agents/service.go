package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"atlas-runtime-go/internal/storage"
)

// TeamsService is the internal API boundary between Agent and Teams.
//
// Agent must not:
//   - parse team-member files directly
//   - build worker prompts directly
//   - manage worker runtime state directly
//   - inspect Teams storage internals directly
//
// Agent interacts with Teams only through this interface.
// Delegate() is added in Phase 4 once DelegationPlan is wired into the skill.
type TeamsService interface {
	// ListTeamMembers returns all enabled team member definitions.
	ListTeamMembers() ([]TeamMember, error)

	// GetTask returns the current state of a delegated task by ID.
	// Returns nil, nil when the task does not exist.
	GetTask(taskID string) (*DelegationTask, error)

	// ResumeTask resumes a delegated task that is paused at an approval gate.
	// approved=true → the pending tool call is executed; approved=false → denied.
	// This is a thin wrapper over the module's resumeDelegatedTask logic; the full
	// engine path is wired in Phase 5.
	ResumeTask(taskID string, toolCallID string, approved bool) error
}

// ── Converters ────────────────────────────────────────────────────────────────

// toTeamMember converts an AgentDefinitionRow to the canonical TeamMember type.
// New fields (TemplateRole, PersonaStyle) fall back to empty string when the
// underlying columns are not yet populated (existing rows from before Phase 1).
func toTeamMember(row storage.AgentDefinitionRow) TeamMember {
	var skills []string
	_ = json.Unmarshal([]byte(row.AllowedSkillsJSON), &skills)
	var toolClasses []string
	if row.AllowedToolClassesJSON != "" && row.AllowedToolClassesJSON != "null" {
		_ = json.Unmarshal([]byte(row.AllowedToolClassesJSON), &toolClasses)
	}
	return TeamMember{
		ID:               row.ID,
		Name:             row.Name,
		TemplateRole:     row.TemplateRole,
		Mission:          row.Mission,
		PersonaStyle:     row.Style,
		AllowedSkills:    skills,
		AllowedToolClasses: toolClasses,
		AutonomyMode:     row.Autonomy,
		ActivationRules:  row.Activation,
		ProviderOverride: row.ProviderType,
		ModelOverride:    row.Model,
		IsEnabled:        row.IsEnabled,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

// toDelegationTask converts an AgentTaskRow to the canonical DelegationTask type.
// New columns added in Phase 1 have zero-value defaults when not yet set.
func toDelegationTask(row storage.AgentTaskRow) DelegationTask {
	return DelegationTask{
		TaskID:              row.TaskID,
		AgentID:             row.AgentID,
		Status:              row.Status,
		Goal:                row.Goal,
		RequestedBy:         row.RequestedBy,
		ResultSummary:       row.ResultSummary,
		ErrorMessage:        row.ErrorMessage,
		ConversationID:      row.ConversationID,
		StartedAt:           row.StartedAt,
		FinishedAt:          row.FinishedAt,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		IterationsUsed:      row.IterationsUsed,
		Title:               row.Title,
		Objective:           row.Objective,
		ScopeJSON:           row.ScopeJSON,
		SuccessCriteriaJSON: row.SuccessCriteriaJSON,
		InputContextJSON:    row.InputContextJSON,
		ExpectedOutputJSON:  row.ExpectedOutputJSON,
		Mode:                row.Mode,
		Pattern:             row.Pattern,
		DependsOnJSON:       row.DependsOnJSON,
		ParentTurnID:        row.ParentTurnID,
		BlockingKind:        row.BlockingKind,
		BlockingDetail:      row.BlockingDetail,
		ResumeToken:         row.ResumeToken,
	}
}

// ── TeamsService implementation ───────────────────────────────────────────────

// ListTeamMembers returns all enabled team member definitions from the DB.
// This is the single method Agent uses to read the roster — no file I/O.
func (m *Module) ListTeamMembers() ([]TeamMember, error) {
	rows, err := m.store.ListEnabledAgentDefinitions()
	if err != nil {
		return nil, fmt.Errorf("teams: list members: %w", err)
	}
	out := make([]TeamMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTeamMember(row))
	}
	return out, nil
}

// GetTask returns a delegated task by ID, or nil if not found.
func (m *Module) GetTask(taskID string) (*DelegationTask, error) {
	row, err := m.store.GetAgentTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("teams: get task: %w", err)
	}
	if row == nil {
		return nil, nil
	}
	t := toDelegationTask(*row)
	return &t, nil
}

// ResumeTask resumes a paused delegated task via the approval flow.
// Delegates to the module's existing resumeDelegatedTask logic (unchanged).
// Full engine integration happens in Phase 5.
func (m *Module) ResumeTask(taskID string, toolCallID string, approved bool) error {
	task, err := m.store.GetAgentTask(taskID)
	if err != nil {
		return fmt.Errorf("teams: resume task: load task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("teams: resume task: task %q not found", taskID)
	}
	def, err := m.store.GetAgentDefinition(task.AgentID)
	if err != nil || def == nil {
		return fmt.Errorf("teams: resume task: agent %q not found", task.AgentID)
	}
	// Run in background so callers are not blocked (matches existing approval route behavior).
	go func() {
		_, _ = m.resumeDelegatedTask(context.Background(), *def, *task, toolCallID, approved)
	}()
	return nil
}
