package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

const syncedEventName = "agent.synced.v1"

type syncEvent struct {
	Count int `json:"count"`
}

type Module struct {
	supportDir       string
	store            platform.AgentStore
	bus              platform.EventBus
	cfg              platform.ConfigReader
	skills           *skills.Registry
	db               *storage.DB
	delegateFn       func(context.Context, storage.AgentDefinitionRow, delegateArgs) (delegatedRun, error)
	resumeDelegateFn func(context.Context, storage.AgentDefinitionRow, storage.AgentTaskRow, string, bool) (delegatedRun, error)
	taskCancels          sync.Map
	coordinator          *triggerCoordinator
	// lastSingleDelegateAt tracks the most recent single-pattern team.delegate call
	// time. Used only for debug logging — logs a "sequence opportunity" when two
	// single-pattern calls occur within the same turn window (≤30s). No behavior change.
	lastSingleDelegateAt sync.Map // key: "single", value: time.Time
}

func New(supportDir string) *Module {
	return &Module{supportDir: supportDir}
}

func (m *Module) ID() string { return "agents" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{
		Version:    "v1",
		Publishes:  []string{syncedEventName, "agent.task.completed", "agent.task.failed", "agent.task.step"},
		Subscribes: []string{"automation.failed", "agent.task.failed"},
	}
}

func (m *Module) Register(host platform.Host) error {
	m.store = host.Storage().Agents()
	m.bus = host.Bus()
	m.cfg = host.Config()
	host.MountProtected(m.registerRoutes)
	// Wire the DB-backed roster into the chat system prompt.
	chat.RosterReader = func(_ string) string {
		return m.rosterContextFromDB()
	}
	return nil
}

// rosterContextFromDB builds the team roster block for system-prompt injection
// by querying the DB directly. Called on every Atlas turn via chat.RosterReader.
// Errors are silently swallowed — a missing roster is non-fatal.
//
// Design: the block frames the team as an execution surface, not admin
// documentation. It tells Atlas when and how to delegate, not just which CRUD
// verbs exist. Kept compact to protect the system-prompt budget.
func (m *Module) rosterContextFromDB() string {
	defs, err := m.store.ListEnabledAgentDefinitions()
	if err != nil || len(defs) == 0 {
		return ""
	}
	var sb strings.Builder

	// ── Delegation policy header ──────────────────────────────────────────────
	// Tells Atlas that the team is an execution resource and gives it clear rules
	// for when to delegate vs stay solo. Kept to a tight paragraph so it does not
	// dominate the budget.
	sb.WriteString("You have a team of specialists available for delegated work.\n")
	sb.WriteString("DELEGATION RULES:\n")
	sb.WriteString("- Delegate when a request maps clearly to a specialist's activation hints and they have assistive or bounded_autonomous autonomy.\n")
	sb.WriteString("- Stay solo for: quick factual answers, casual conversation, simple single-tool tasks, automations, workflows, or anything the user did not imply needs specialist depth.\n")
	sb.WriteString("- Do NOT proactively activate on_demand members — only use them when the user explicitly requests it.\n")
	sb.WriteString("- Default execution mode for proactive delegation: sync_assist (wait for the result and integrate it into your answer).\n")
	sb.WriteString("- Use async_assignment only when the user wants background work with its own lifecycle.\n")
	sb.WriteString("- Use pattern=sequence when step B depends on step A's output (e.g. research → draft, draft → review). If you already know multiple steps are required, prefer a single team.delegate call with pattern=\"sequence\" over making two separate calls.\n")
	sb.WriteString("- You remain the primary agent and final narrator. Integrate specialist results into your own answer.\n")
	sb.WriteString("- Never claim a specialist did work unless team.delegate was called and returned a result this turn.\n")
	sb.WriteString("\n")

	// ── Delegation invocation guide ───────────────────────────────────────────
	sb.WriteString("To delegate: team.delegate(agentID=\"<id>\", task=\"<what to do>\")  — simplest form.\n")
	sb.WriteString("Sequence:    team.delegate(pattern=\"sequence\", tasks=[{agentId,task},{agentId,task}])\n")
	sb.WriteString("\n")

	// ── Team members ──────────────────────────────────────────────────────────
	sb.WriteString("Team members (use exact id):\n")
	for _, d := range defs {
		line := fmt.Sprintf("- id:%s | %s | %s", d.ID, d.Name, d.Role)
		if d.Activation != "" {
			line += fmt.Sprintf(" | activate when: %s", d.Activation)
		}
		line += fmt.Sprintf(" | autonomy: %s", d.Autonomy)
		sb.WriteString(line + "\n")
	}

	// ── Management verbs (secondary) ─────────────────────────────────────────
	sb.WriteString("Manage: agent.create, agent.update, agent.delete, agent.enable, agent.disable.")

	return sb.String()
}

func (m *Module) Start(ctx context.Context) error {
	m.resetStaleRuntimeStates()

	// M6: start bounded-autonomy trigger coordinator.
	m.coordinator = &triggerCoordinator{
		store:      m.store,
		bus:        m.bus,
		delegateFn: m.delegateTask,
	}
	_ = m.coordinator.Start(ctx)
	return nil
}

// resetStaleRuntimeStates cleans up any state left over from a previous
// daemon run that was killed mid-execution.
//
//   - Runtime rows stuck in "busy" or "working" are reset to "idle" so agents
//     aren't permanently marked as occupied after a restart.
//   - Tasks still in "running" or "working" state are marked "failed" — they were
//     interrupted and can never be resumed from their original loop.
//     Legacy rows in "running"/"error" are also handled for backward compat.
func (m *Module) resetStaleRuntimeStates() {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	interruptedMsg := "Interrupted by daemon restart."

	if rows, err := m.store.ListAgentRuntime(); err == nil {
		for _, row := range rows {
			// Reset both legacy "busy" and V1 "working" status (Phase 7).
			if row.Status == "busy" || row.Status == "working" {
				row.Status = "idle"
				row.CurrentTaskID = nil
				row.LastError = &interruptedMsg
				row.UpdatedAt = now
				_ = m.store.SaveAgentRuntime(row)
			}
		}
	}

	if tasks, err := m.store.ListAgentTasks(500); err == nil {
		for _, task := range tasks {
			// Reset both legacy "running" and V1 "working" in-progress states (Phase 7).
			if task.Status == "running" || task.Status == "working" {
				task.Status = "failed" // V1 canonical terminal status
				task.ErrorMessage = &interruptedMsg
				task.UpdatedAt = now
				finishedAt := now
				task.FinishedAt = &finishedAt
				_ = m.store.SaveAgentTask(task)
			}
		}
	}
}

func (m *Module) Stop(context.Context) error {
	if m.coordinator != nil {
		m.coordinator.Stop()
	}
	return nil
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/agents/hq", m.getTeam)
	r.Get("/agents", m.listAgents)
	r.Get("/agents/{id}", m.getAgent)
	r.Get("/agents/events", m.listEvents)
	r.Delete("/agents/events", m.clearEvents)
	r.Get("/agents/tasks", m.listTasks)
	r.Delete("/agents/tasks", m.clearTasks)
	r.Delete("/agents/blocked", m.clearBlocked)
	r.Get("/agents/tasks/{id}", m.getTask)
	r.Post("/agents/tasks", m.assignTask)
	r.Post("/agents/tasks/{id}/cancel", m.cancelTask)
	r.Post("/agents/tasks/{id}/approve", m.approveTask)
	r.Post("/agents/tasks/{id}/reject", m.rejectTask)
	r.Post("/agents", m.createAgent)
	r.Put("/agents/{id}", m.updateAgent)
	r.Delete("/agents/{id}", m.deleteAgent)
	r.Post("/agents/{id}/enable", m.enableAgent)
	r.Post("/agents/{id}/disable", m.disableAgent)
	r.Post("/agents/{id}/pause", m.pauseAgent)
	r.Post("/agents/{id}/resume", m.resumeAgent)
	r.Get("/agents/triggers", m.listTriggers)
	r.Post("/agents/triggers/evaluate", m.evaluateTriggerHTTP)
}

func (m *Module) SetSkillRegistry(registry *skills.Registry) {
	m.skills = registry
	m.registerAgentActions()
}

func (m *Module) SetDatabase(db *storage.DB) {
	m.db = db
}

type agentSnapshot struct {
	Atlas            atlasStation          `json:"atlas"`
	Agents           []agentJSON           `json:"agents"`
	Activity         []agentEventJSON      `json:"activity"`
	BlockedItems     []blockedItemJSON     `json:"blockedItems"`
	SuggestedActions []suggestedActionJSON `json:"suggestedActions"`
	KPIs             agentKPIs             `json:"kpis"`
}

type agentKPIs struct {
	TotalTasksCompleted int `json:"totalTasksCompleted"`
	TotalTasksFailed    int `json:"totalTasksFailed"`
	TotalToolCalls      int `json:"totalToolCalls"`
}

type atlasStation struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type agentJSON struct {
	Name               string       `json:"name"`
	ID                 string       `json:"id"`
	Role               string       `json:"role"`
	TemplateRole       string       `json:"templateRole,omitempty"` // Phase 7: V1 template role
	Mission            string       `json:"mission"`
	Style              string       `json:"style,omitempty"`
	AllowedSkills      []string     `json:"allowedSkills"`
	AllowedToolClasses []string     `json:"allowedToolClasses,omitempty"`
	Autonomy           string       `json:"autonomy"`
	Activation         string       `json:"activation,omitempty"`
	ProviderType       string       `json:"providerType,omitempty"`
	Model              string       `json:"model,omitempty"`
	Enabled            bool         `json:"enabled"`
	Runtime            runtimeJSON  `json:"runtime"`
	Metrics            *metricsJSON `json:"metrics,omitempty"`
}

type metricsJSON struct {
	TasksCompleted int      `json:"tasksCompleted"`
	TasksFailed    int      `json:"tasksFailed"`
	TotalToolCalls int      `json:"totalToolCalls"`
	LastActiveAt   *string  `json:"lastActiveAt,omitempty"`
	SuccessRate    *float64 `json:"successRate,omitempty"`
}

type triggerEventJSON struct {
	TriggerID   string  `json:"triggerID"`
	TriggerType string  `json:"triggerType"`
	AgentID     *string `json:"agentID,omitempty"`
	Instruction string  `json:"instruction"`
	Status      string  `json:"status"`
	FiredAt     *string `json:"firedAt,omitempty"`
	CreatedAt   string  `json:"createdAt"`
}

type runtimeJSON struct {
	Status        string  `json:"status"`
	CurrentTaskID *string `json:"currentTaskID,omitempty"`
	LastActiveAt  *string `json:"lastActiveAt,omitempty"`
	LastError     *string `json:"lastError,omitempty"`
	UpdatedAt     string  `json:"updatedAt"`
}

type syncResponse struct {
	Count   int         `json:"count"`
	Agents  []agentJSON `json:"agents"`
	Source  string      `json:"source"`
	Updated string      `json:"updated"`
}

type agentEventJSON struct {
	EventID   string         `json:"eventID"`
	EventType string         `json:"eventType"`
	AgentID   *string        `json:"agentID,omitempty"`
	TaskID    *string        `json:"taskID,omitempty"`
	Title     string         `json:"title"`
	Detail    *string        `json:"detail,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt string         `json:"createdAt"`
}

type blockedItemJSON struct {
	Kind           string  `json:"kind"`
	ID             string  `json:"id"`
	AgentID        *string `json:"agentID,omitempty"`
	Title          string  `json:"title"`
	Status         string  `json:"status"`
	BlockingKind   string  `json:"blockingKind,omitempty"`   // Phase 7: from blocking_kind column
	BlockingDetail string  `json:"blockingDetail,omitempty"` // Phase 7: from blocking_detail column
}

type suggestedActionJSON struct {
	Kind    string  `json:"kind"`
	ID      string  `json:"id"`
	AgentID *string `json:"agentID,omitempty"`
	Title   string  `json:"title"`
}

type taskJSON struct {
	TaskID         string         `json:"taskID"`
	AgentID        string         `json:"agentID"`
	Status         string         `json:"status"`
	Goal           string         `json:"goal"`
	RequestedBy    string         `json:"requestedBy"`
	ResultSummary  *string        `json:"resultSummary,omitempty"`
	ErrorMessage   *string        `json:"errorMessage,omitempty"`
	ConversationID *string        `json:"conversationID,omitempty"`
	StartedAt      string         `json:"startedAt"`
	FinishedAt     *string        `json:"finishedAt,omitempty"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
	Steps          []taskStepJSON `json:"steps,omitempty"`
	// Phase 7: V1 structured fields — populated when task was created via DelegationPlan
	Title          string  `json:"title,omitempty"`
	Objective      string  `json:"objective,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Pattern        string  `json:"pattern,omitempty"`
	BlockingKind   *string `json:"blockingKind,omitempty"`
	BlockingDetail *string `json:"blockingDetail,omitempty"`
}

type taskStepJSON struct {
	StepID         string  `json:"stepID"`
	SequenceNumber int     `json:"sequenceNumber"`
	Role           string  `json:"role"`
	StepType       string  `json:"stepType"`
	Content        string  `json:"content"`
	ToolName       *string `json:"toolName,omitempty"`
	ToolCallID     *string `json:"toolCallID,omitempty"`
	CreatedAt      string  `json:"createdAt"`
}

func (m *Module) getTeam(w http.ResponseWriter, _ *http.Request) {
	agents, err := m.listJoinedAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read team: "+err.Error())
		return
	}
	events, err := m.listEventJSON(20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read team activity: "+err.Error())
		return
	}
	blocked, suggested, atlasStatus, err := m.teamDerivedState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to assemble team state: "+err.Error())
		return
	}
	// Aggregate team-level KPIs from agent metrics
	kpis := agentKPIs{}
	for _, a := range agents {
		if a.Metrics != nil {
			kpis.TotalTasksCompleted += a.Metrics.TasksCompleted
			kpis.TotalTasksFailed += a.Metrics.TasksFailed
			kpis.TotalToolCalls += a.Metrics.TotalToolCalls
		}
	}
	cfg := m.cfg.Load()
	personaName := cfg.PersonaName
	if personaName == "" {
		personaName = "Atlas"
	}
	writeJSON(w, http.StatusOK, agentSnapshot{
		Atlas: atlasStation{
			ID:     "atlas",
			Name:   personaName,
			Role:   "Coordinator",
			Status: atlasStatus,
		},
		Agents:           agents,
		Activity:         events,
		BlockedItems:     blocked,
		SuggestedActions: suggested,
		KPIs:             kpis,
	})
}

func (m *Module) listAgents(w http.ResponseWriter, _ *http.Request) {
	agents, err := m.listJoinedAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (m *Module) getAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok, err := m.getJoinedAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read agent: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (m *Module) listEvents(w http.ResponseWriter, _ *http.Request) {
	events, err := m.listEventJSON(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list team events: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (m *Module) clearEvents(w http.ResponseWriter, _ *http.Request) {
	if err := m.store.ClearAgentEvents(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear events: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) clearTasks(w http.ResponseWriter, _ *http.Request) {
	if err := m.store.ClearAgentTasks(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear tasks: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) clearBlocked(w http.ResponseWriter, _ *http.Request) {
	if err := m.store.ClearBlockedAgentTasks(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear blocked items: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) listTriggers(w http.ResponseWriter, _ *http.Request) {
	rows, err := m.store.ListTriggerEvents(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list trigger events: "+err.Error())
		return
	}
	out := make([]triggerEventJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, triggerEventJSON{
			TriggerID:   row.TriggerID,
			TriggerType: row.TriggerType,
			AgentID:     row.AgentID,
			Instruction: row.Instruction,
			Status:      row.Status,
			FiredAt:     row.FiredAt,
			CreatedAt:   row.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) evaluateTriggerHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TriggerType string `json:"triggerType"`
		Instruction string `json:"instruction"`
	}
	if err := readJSON(r, &body); err != nil || body.TriggerType == "" {
		writeError(w, http.StatusBadRequest, "triggerType is required")
		return
	}
	instruction := body.Instruction
	if instruction == "" {
		instruction = "Manual trigger: " + body.TriggerType
	}
	if m.coordinator == nil {
		writeError(w, http.StatusServiceUnavailable, "trigger coordinator not running")
		return
	}
	_ = m.coordinator.Evaluate(r.Context(), body.TriggerType, instruction, nil, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "evaluated", "triggerType": body.TriggerType})
}

func (m *Module) listTasks(w http.ResponseWriter, _ *http.Request) {
	rows, err := m.store.ListAgentTasks(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list team tasks: "+err.Error())
		return
	}
	out := make([]taskJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskFromRow(row, nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := m.store.GetAgentTask(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load team task: "+err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "team task not found: "+id)
		return
	}
	steps, err := m.store.ListAgentTaskSteps(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load team task steps: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskFromRow(*row, steps))
}

func (m *Module) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := m.store.GetAgentTask(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load team task: "+err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "team task not found: "+id)
		return
	}
	if row.Status != "running" {
		writeError(w, http.StatusConflict, "team task is not running")
		return
	}
	if v, ok := m.taskCancels.Load(id); ok {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row.Status = "cancelled"
	row.UpdatedAt = now
	row.FinishedAt = &now
	if row.ResultSummary == nil {
		row.ResultSummary = strPtr("Task cancelled.")
	}
	if err := m.store.SaveAgentTask(*row); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel team task: "+err.Error())
		return
	}
	if runtimeRow, err := m.store.GetAgentRuntime(row.AgentID); err == nil && runtimeRow != nil {
		runtimeRow.Status = "idle"
		runtimeRow.CurrentTaskID = nil
		runtimeRow.LastActiveAt = &now
		runtimeRow.UpdatedAt = now
		runtimeRow.LastError = nil
		_ = m.store.SaveAgentRuntime(*runtimeRow)
	}
	_ = m.recordEvent("team.task.cancelled", &row.AgentID, &row.TaskID, "Task cancelled", strPtr("Delegated task was cancelled."), map[string]any{
		"taskID":  row.TaskID,
		"agentID": row.AgentID,
	})
	steps, _ := m.store.ListAgentTaskSteps(id)
	writeJSON(w, http.StatusOK, taskFromRow(*row, steps))
}

func (m *Module) approveTask(w http.ResponseWriter, r *http.Request) {
	m.resolveTaskApproval(w, r, true)
}

func (m *Module) rejectTask(w http.ResponseWriter, r *http.Request) {
	m.resolveTaskApproval(w, r, false)
}

// resolveTaskApproval advances or cancels a delegated task that paused at an
// approval gate. Approving resumes the delegated loop from its saved deferral
// state; rejecting denies the pending tool call(s) and cancels the task.
//
// Multiple pending deferred calls (rare — happens when parallel tool use
// produces several deferred calls in a single loop iteration):
// - Approve: resumeDelegatedTask() resumes on pending[0] and marks the
//   remaining calls "auto_denied" in the DB, injecting synthetic tool results
//   so the agent loop can continue without stalling.
// - Reject: all pending deferred calls are denied and the task is cancelled.
func (m *Module) resolveTaskApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	id := chi.URLParam(r, "id")
	row, err := m.store.GetAgentTask(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load team task: "+err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "team task not found: "+id)
		return
	}
	if row.Status != "pending_approval" {
		writeError(w, http.StatusConflict, fmt.Sprintf("task is %q, not pending_approval", row.Status))
		return
	}

	pending, err := m.store.FetchDeferredsByAgentTaskID(row.TaskID, "pending_approval")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pending approvals for task: "+err.Error())
		return
	}
	if len(pending) == 0 {
		writeError(w, http.StatusConflict, "task is pending approval but no deferred tool calls were found")
		return
	}

	def, err := m.store.GetAgentDefinition(row.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent definition: "+err.Error())
		return
	}
	if def == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+row.AgentID)
		return
	}

	if approved {
		resume := m.resumeDelegateFn
		if resume == nil {
			resume = m.resumeDelegatedTask
		}
		run, err := resume(r.Context(), *def, *row, pending[0].ToolCallID, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resume delegated task: "+err.Error())
			return
		}
		_ = m.recordEvent("team.task.approved", &row.AgentID, &row.TaskID, "Task approved", strPtr("Delegated task approval was recorded and the task resumed."), map[string]any{
			"taskID":     row.TaskID,
			"agentID":    row.AgentID,
			"toolCallID": pending[0].ToolCallID,
		})
		writeJSON(w, http.StatusOK, taskFromRow(run.Task, run.Steps))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, deferred := range pending {
		if err := m.db.UpdateDeferredStatus(deferred.ToolCallID, "denied", now); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to deny deferred tool call: "+err.Error())
			return
		}
		if m.bus != nil {
			_ = m.bus.Publish(r.Context(), "approval.resolved.v1", map[string]any{
				"toolCallID": deferred.ToolCallID,
				"status":     "denied",
			})
		}
	}
	row.Status = "cancelled"
	row.ResultSummary = strPtr("Rejected by user.")
	row.ErrorMessage = nil
	row.UpdatedAt = now
	row.FinishedAt = &now
	if err := m.store.SaveAgentTask(*row); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update team task: "+err.Error())
		return
	}
	if runtimeRow, err := m.store.GetAgentRuntime(row.AgentID); err == nil && runtimeRow != nil {
		runtimeRow.Status = "idle"
		runtimeRow.CurrentTaskID = nil
		runtimeRow.LastActiveAt = &now
		runtimeRow.UpdatedAt = now
		runtimeRow.LastError = nil
		_ = m.store.SaveAgentRuntime(*runtimeRow)
	}
	_ = m.recordEvent("team.task.rejected", &row.AgentID, &row.TaskID, "Task rejected", strPtr("Delegated task was rejected by user."), map[string]any{
		"taskID":  row.TaskID,
		"agentID": row.AgentID,
	})
	steps, _ := m.store.ListAgentTaskSteps(id)
	writeJSON(w, http.StatusOK, taskFromRow(*row, steps))
}

func (m *Module) createAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	def := agentDefinition{
		Name:               body.Name,
		ID:                 body.ID,
		Role:               body.Role,
		Mission:            body.Mission,
		Style:              body.Style,
		AllowedSkills:      body.AllowedSkills,
		AllowedToolClasses: body.AllowedToolClasses,
		Autonomy:           body.Autonomy,
		Activation:         body.Activation,
		ProviderType:       body.ProviderType,
		Model:              body.Model,
		Enabled:            true,
	}
	if body.Enabled != nil {
		def.Enabled = *body.Enabled
	}
	def = normalizeDefinition(def)
	if def.ID == "" {
		def.ID = slugID(def.Name)
	}
	if def.Name == "" {
		def.Name = strings.TrimSpace(def.ID)
	}
	if err := validateDefinition(def); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if existingDef, _ := m.store.GetAgentDefinition(def.ID); existingDef != nil {
		writeError(w, http.StatusConflict, "agent id already exists: "+def.ID)
		return
	}
	row := fileDefToRow(def, now, now)
	if err := m.store.SaveAgentDefinition(row); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent: "+err.Error())
		return
	}
	_ = m.store.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: def.ID, Status: "idle", UpdatedAt: now})
	agent, ok, err := m.getJoinedAgent(def.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load created agent: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "created agent missing after save")
		return
	}
	_ = m.recordEvent("team.agent.created", &agent.ID, nil, fmt.Sprintf("Agent created: %s", agent.Name), strPtr(agent.Role), map[string]any{"agentID": agent.ID})
	writeJSON(w, http.StatusCreated, agent)
}

func (m *Module) updateAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existingRow, err := m.store.GetAgentDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if existingRow == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+id)
		return
	}
	current := rowToFileDef(*existingRow)
	var patch agentDefinition
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	updated := mergeDefinition(current, patch)
	updated.ID = id
	if err := validateDefinition(updated); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := fileDefToRow(updated, existingRow.CreatedAt, now)
	if err := m.store.SaveAgentDefinition(row); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agent, ok, err := m.getJoinedAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated agent: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "updated agent missing after save")
		return
	}
	_ = m.recordEvent("team.agent.updated", &agent.ID, nil, fmt.Sprintf("Agent updated: %s", agent.Name), strPtr(agent.Role), map[string]any{"agentID": agent.ID})
	writeJSON(w, http.StatusOK, agent)
}

func (m *Module) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existingRow, err := m.store.GetAgentDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if existingRow == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+id)
		return
	}
	name := existingRow.Name
	role := existingRow.Role
	if _, err := m.store.DeleteAgentDefinition(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete agent: "+err.Error())
		return
	}
	_, _ = m.store.DeleteAgentRuntime(id)
	_ = m.recordEvent("team.agent.deleted", &id, nil, fmt.Sprintf("Agent deleted: %s", name), strPtr(role), map[string]any{"agentID": id})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"name":    name,
		"deleted": true,
	})
}

func (m *Module) enableAgent(w http.ResponseWriter, r *http.Request) {
	m.setEnabled(w, r, true)
}

func (m *Module) disableAgent(w http.ResponseWriter, r *http.Request) {
	m.setEnabled(w, r, false)
}

func (m *Module) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := chi.URLParam(r, "id")
	existingRow, err := m.store.GetAgentDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if existingRow == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+id)
		return
	}
	existingRow.IsEnabled = enabled
	existingRow.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.store.SaveAgentDefinition(*existingRow); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agent, ok, err := m.getJoinedAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "agent missing after save")
		return
	}
	eventType := "team.agent.disabled"
	title := fmt.Sprintf("Agent disabled: %s", agent.Name)
	if enabled {
		eventType = "team.agent.enabled"
		title = fmt.Sprintf("Agent enabled: %s", agent.Name)
	}
	_ = m.recordEvent(eventType, &agent.ID, nil, title, strPtr(agent.Role), map[string]any{"agentID": agent.ID, "enabled": enabled})
	writeJSON(w, http.StatusOK, agent)
}

func (m *Module) pauseAgent(w http.ResponseWriter, r *http.Request) {
	m.setRuntimeStatus(w, r, "paused")
}

func (m *Module) resumeAgent(w http.ResponseWriter, r *http.Request) {
	m.setRuntimeStatus(w, r, "idle")
}

func (m *Module) setRuntimeStatus(w http.ResponseWriter, r *http.Request, status string) {
	id := chi.URLParam(r, "id")
	def, err := m.store.GetAgentDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if def == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+id)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	runtimeRow, err := m.store.GetAgentRuntime(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent runtime: "+err.Error())
		return
	}
	if runtimeRow == nil {
		runtimeRow = &storage.AgentRuntimeRow{AgentID: id}
	}
	runtimeRow.Status = status
	runtimeRow.UpdatedAt = now
	if status != "paused" {
		runtimeRow.LastError = nil
	}
	if err := m.store.SaveAgentRuntime(*runtimeRow); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent runtime: "+err.Error())
		return
	}
	agent, ok, err := m.getJoinedAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusInternalServerError, "agent missing after runtime update")
		return
	}
	eventType := "team.agent.resumed"
	title := fmt.Sprintf("Agent resumed: %s", agent.Name)
	if status == "paused" {
		eventType = "team.agent.paused"
		title = fmt.Sprintf("Agent paused: %s", agent.Name)
	}
	_ = m.recordEvent(eventType, &agent.ID, nil, title, strPtr(status), map[string]any{"agentID": agent.ID, "status": status})
	writeJSON(w, http.StatusOK, agent)
}

func (m *Module) syncAgents(w http.ResponseWriter, r *http.Request) {
	resp, err := m.syncFromFile(r.Context())
	if err != nil {
		switch {
		case os.IsNotExist(err):
			writeJSON(w, http.StatusOK, syncResponse{
				Count:   0,
				Agents:  []agentJSON{},
				Source:  agentsFilePath(m.supportDir),
				Updated: time.Now().UTC().Format(time.RFC3339Nano),
			})
		default:
			writeError(w, http.StatusInternalServerError, "failed to sync agents: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// exportAgents renders the current DB state as AGENTS.md content and returns
// it as plain text. This is the export path for users who want to back up or
// inspect their team configuration in the legacy AGENTS.md format.
// AGENTS.md is no longer read on startup (Phase 8); this route gives it back.
func (m *Module) exportAgents(w http.ResponseWriter, _ *http.Request) {
	defs, err := m.store.ListAgentDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents: "+err.Error())
		return
	}
	fileDefs := make([]agentDefinition, 0, len(defs))
	for _, row := range defs {
		fileDefs = append(fileDefs, rowToFileDef(row))
	}
	markdown := renderAgentsMarkdown(fileDefs)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(markdown))
}

func (m *Module) syncFromFile(ctx context.Context) (syncResponse, error) {
	path := agentsFilePath(m.supportDir)
	defs, err := readAgentsFile(path)
	if err != nil {
		return syncResponse{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	existingDefs, err := m.store.ListAgentDefinitions()
	if err != nil {
		return syncResponse{}, err
	}
	existingRuntime, err := m.store.ListAgentRuntime()
	if err != nil {
		return syncResponse{}, err
	}
	defMap := make(map[string]storage.AgentDefinitionRow, len(existingDefs))
	for _, row := range existingDefs {
		defMap[row.ID] = row
	}
	runtimeMap := make(map[string]storage.AgentRuntimeRow, len(existingRuntime))
	for _, row := range existingRuntime {
		runtimeMap[row.AgentID] = row
	}
	seen := make(map[string]bool, len(defs))
	for _, def := range defs {
		seen[def.ID] = true
		createdAt := now
		if existing, ok := defMap[def.ID]; ok && strings.TrimSpace(existing.CreatedAt) != "" {
			createdAt = existing.CreatedAt
		}
		defRow := fileDefToRow(def, createdAt, now)
		if err := m.store.SaveAgentDefinition(defRow); err != nil {
			return syncResponse{}, err
		}

		runtimeRow, ok := runtimeMap[def.ID]
		if !ok {
			runtimeRow = storage.AgentRuntimeRow{
				AgentID:   def.ID,
				Status:    "idle",
				UpdatedAt: now,
			}
		} else {
			runtimeRow.UpdatedAt = now
		}
		if strings.TrimSpace(runtimeRow.Status) == "" {
			runtimeRow.Status = "idle"
		}
		if err := m.store.SaveAgentRuntime(runtimeRow); err != nil {
			return syncResponse{}, err
		}
	}
	for _, row := range existingDefs {
		if seen[row.ID] {
			continue
		}
		if _, err := m.store.DeleteAgentDefinition(row.ID); err != nil {
			return syncResponse{}, err
		}
		if _, err := m.store.DeleteAgentRuntime(row.ID); err != nil {
			return syncResponse{}, err
		}
	}
	agents, err := m.listJoinedAgents()
	if err != nil {
		return syncResponse{}, err
	}
	if m.bus != nil {
		_ = m.bus.Publish(ctx, syncedEventName, syncEvent{Count: len(agents)})
	}
	_ = m.recordEvent("team.synced", nil, nil, "Team definitions synced", strPtr(fmt.Sprintf("Loaded %d team member definition(s) from AGENTS.md.", len(agents))), map[string]any{
		"count":  len(agents),
		"source": path,
	})
	return syncResponse{
		Count:   len(agents),
		Agents:  agents,
		Source:  path,
		Updated: now,
	}, nil
}


func (m *Module) listJoinedAgents() ([]agentJSON, error) {
	defs, err := m.store.ListAgentDefinitions()
	if err != nil {
		return nil, err
	}
	runtimeRows, err := m.store.ListAgentRuntime()
	if err != nil {
		return nil, err
	}
	runtimeMap := make(map[string]storage.AgentRuntimeRow, len(runtimeRows))
	for _, row := range runtimeRows {
		runtimeMap[row.AgentID] = row
	}
	metricsRows, _ := m.store.ListAgentMetrics()
	metricsMap := make(map[string]storage.AgentMetricsRow, len(metricsRows))
	for _, row := range metricsRows {
		metricsMap[row.AgentID] = row
	}
	out := make([]agentJSON, 0, len(defs))
	for _, def := range defs {
		m := metricsMap[def.ID]
		out = append(out, agentFromRows(def, runtimeMap[def.ID], &m))
	}
	return out, nil
}

func (m *Module) getJoinedAgent(id string) (agentJSON, bool, error) {
	def, err := m.store.GetAgentDefinition(id)
	if err != nil {
		return agentJSON{}, false, err
	}
	if def == nil {
		return agentJSON{}, false, nil
	}
	runtimeRow, err := m.store.GetAgentRuntime(id)
	if err != nil {
		return agentJSON{}, false, err
	}
	var runtimeValue storage.AgentRuntimeRow
	if runtimeRow != nil {
		runtimeValue = *runtimeRow
	}
	metricsRow, _ := m.store.GetAgentMetrics(id)
	return agentFromRows(*def, runtimeValue, metricsRow), true, nil
}

func agentFromRows(def storage.AgentDefinitionRow, runtimeRow storage.AgentRuntimeRow, metricsRow *storage.AgentMetricsRow) agentJSON {
	allowedSkills := []string{}
	allowedToolClasses := []string{}
	_ = json.Unmarshal([]byte(def.AllowedSkillsJSON), &allowedSkills)
	_ = json.Unmarshal([]byte(def.AllowedToolClassesJSON), &allowedToolClasses)
	status := normalizeMemberStatus(runtimeRow.Status)
	out := agentJSON{
		Name:               def.Name,
		ID:                 def.ID,
		Role:               def.Role,
		TemplateRole:       def.TemplateRole, // Phase 7
		Mission:            def.Mission,
		Style:              def.Style,
		AllowedSkills:      allowedSkills,
		AllowedToolClasses: allowedToolClasses,
		Autonomy:           def.Autonomy,
		Activation:         def.Activation,
		ProviderType:       def.ProviderType,
		Model:              def.Model,
		Enabled:            def.IsEnabled,
		Runtime: runtimeJSON{
			Status:        status,
			CurrentTaskID: runtimeRow.CurrentTaskID,
			LastActiveAt:  runtimeRow.LastActiveAt,
			LastError:     runtimeRow.LastError,
			UpdatedAt:     runtimeRow.UpdatedAt,
		},
	}
	if metricsRow != nil {
		m := &metricsJSON{
			TasksCompleted: metricsRow.TasksCompleted,
			TasksFailed:    metricsRow.TasksFailed,
			TotalToolCalls: metricsRow.TotalToolCalls,
			LastActiveAt:   metricsRow.LastActiveAt,
		}
		if total := metricsRow.TasksCompleted + metricsRow.TasksFailed; total > 0 {
			rate := float64(metricsRow.TasksCompleted) / float64(total)
			m.SuccessRate = &rate
		}
		out.Metrics = m
	}
	return out
}

// assignTask handles POST /team/tasks — direct user-initiated task assignment.
// Phase 5: async_assignment — pre-generate a task ID, spawn the agent loop in
// a goroutine, and return 202 Accepted immediately with the task ID. Callers
// poll GET /agents/tasks/{id} for status rather than blocking on the HTTP request.
func (m *Module) assignTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID string `json:"agentID"`
		Task    string `json:"task"`
		Goal    string `json:"goal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.AgentID = strings.TrimSpace(body.AgentID)
	body.Task = strings.TrimSpace(body.Task)
	if body.AgentID == "" || body.Task == "" {
		writeError(w, http.StatusBadRequest, "agentID and task are required")
		return
	}
	def, err := m.store.GetAgentDefinition(body.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent: "+err.Error())
		return
	}
	if def == nil {
		writeError(w, http.StatusNotFound, "agent not found: "+body.AgentID)
		return
	}
	if !def.IsEnabled {
		writeError(w, http.StatusConflict, "agent is disabled")
		return
	}

	// Pre-generate the task ID so it can be returned in the 202 response before
	// the agent loop runs. The goroutine must use context.Background() — the HTTP
	// request context closes when the 202 response is sent.
	taskID := newID("teamtask")
	delegate := m.delegateFn
	if delegate == nil {
		delegate = m.delegateTask
	}
	defCopy := *def
	go func() {
		_, _ = delegate(context.Background(), defCopy, delegateArgs{
			AgentID:     body.AgentID,
			Task:        body.Task,
			Goal:        body.Goal,
			RequestedBy: "user",
			Mode:        "async_assignment",
			Pattern:     "single",
			TaskID:      taskID,
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"taskID":  taskID,
		"agentID": body.AgentID,
		"status":  "queued",
	})
}

func mergeDefinition(current, patch agentDefinition) agentDefinition {
	out := current
	if strings.TrimSpace(patch.Name) != "" {
		out.Name = strings.TrimSpace(patch.Name)
	}
	if strings.TrimSpace(patch.Role) != "" {
		out.Role = strings.TrimSpace(patch.Role)
	}
	if strings.TrimSpace(patch.Mission) != "" {
		out.Mission = strings.TrimSpace(patch.Mission)
	}
	if strings.TrimSpace(patch.Style) != "" {
		out.Style = strings.TrimSpace(patch.Style)
	}
	if len(patch.AllowedSkills) > 0 {
		out.AllowedSkills = patch.AllowedSkills
	}
	if len(patch.AllowedToolClasses) > 0 {
		out.AllowedToolClasses = patch.AllowedToolClasses
	}
	if strings.TrimSpace(patch.Autonomy) != "" {
		out.Autonomy = strings.TrimSpace(patch.Autonomy)
	}
	if strings.TrimSpace(patch.Activation) != "" {
		out.Activation = strings.TrimSpace(patch.Activation)
	}
	if patch.Enabled != current.Enabled {
		out.Enabled = patch.Enabled
	}
	return normalizeDefinition(out)
}

// normalizeTaskStatus maps legacy task status strings to V1 canonical names.
// Rows written before Phase 7 may still carry the old names; this ensures the
// HTTP responses and teamDerivedState logic always speak V1.
//
//	"running"          → "working"
//	"error"            → "failed"
//	"pending_approval" → "needs_review"
//	"complete"         → "completed"  (typo variant seen in older code)
//
// All other values pass through unchanged.
func normalizeTaskStatus(s string) string {
	switch s {
	case "running":
		return "working"
	case "error":
		return "failed"
	case "pending_approval":
		return "needs_review"
	case "complete":
		return "completed"
	default:
		return s
	}
}

// normalizeMemberStatus maps legacy runtime status strings to V1 canonical names.
//
//	"busy"             → "working"
//	"approval_needed"  → "needs_review"
//	""                 → "idle"
func normalizeMemberStatus(s string) string {
	switch s {
	case "busy":
		return MemberStatusWorking
	case "approval_needed":
		return MemberStatusNeedsReview
	case "":
		return MemberStatusIdle
	default:
		return s
	}
}

// rowToFileDef converts a storage.AgentDefinitionRow to the local agentDefinition
// struct used by the merge and validation pipeline. Used by DB-first CRUD handlers
// so they can reuse mergeDefinition / validateDefinition without duplicating logic.
func rowToFileDef(row storage.AgentDefinitionRow) agentDefinition {
	var skills []string
	_ = json.Unmarshal([]byte(row.AllowedSkillsJSON), &skills)
	var toolClasses []string
	if row.AllowedToolClassesJSON != "" && row.AllowedToolClassesJSON != "null" {
		_ = json.Unmarshal([]byte(row.AllowedToolClassesJSON), &toolClasses)
	}
	return agentDefinition{
		ID:                 row.ID,
		Name:               row.Name,
		Role:               row.Role,
		Mission:            row.Mission,
		Style:              row.Style,
		AllowedSkills:      skills,
		AllowedToolClasses: toolClasses,
		Autonomy:           row.Autonomy,
		Activation:         row.Activation,
		ProviderType:       row.ProviderType,
		Model:              row.Model,
		Enabled:            row.IsEnabled,
	}
}

// templateRoleFromRole maps the free-text legacy role string to the canonical
// V1 template role enum. Matching is case-insensitive and keyword-based.
// More specific role keywords (qa, review, monitor) are checked before generic
// ones (build, develop) to avoid false positives like "QA Engineer" → builder.
// Returns "" for unrecognized roles (falls back to generic contract in prompt.go).
func templateRoleFromRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	switch {
	case strings.HasPrefix(r, "scout") || strings.Contains(r, "research") || strings.Contains(r, "investigat"):
		return "scout"
	case strings.HasPrefix(r, "reviewer") || strings.Contains(r, "review") || strings.Contains(r, "qa") || strings.Contains(r, "quality"):
		return "reviewer"
	case strings.HasPrefix(r, "monitor") || strings.Contains(r, "monitor") || strings.Contains(r, "watch") || strings.Contains(r, "observ"):
		return "monitor"
	case strings.HasPrefix(r, "operator") || strings.Contains(r, "operat") || strings.Contains(r, "execut"):
		return "operator"
	case strings.HasPrefix(r, "builder") || strings.Contains(r, "build") || strings.Contains(r, "develop") || strings.Contains(r, "engineer"):
		return "builder"
	}
	return ""
}

// fileDefToRow converts an agentDefinition to a storage.AgentDefinitionRow.
// TemplateRole is derived from the Role field via templateRoleFromRole.
func fileDefToRow(def agentDefinition, createdAt, updatedAt string) storage.AgentDefinitionRow {
	return storage.AgentDefinitionRow{
		ID:                     def.ID,
		Name:                   def.Name,
		Role:                   def.Role,
		TemplateRole:           templateRoleFromRole(def.Role),
		Mission:                def.Mission,
		Style:                  def.Style,
		AllowedSkillsJSON:      mustJSON(def.AllowedSkills),
		AllowedToolClassesJSON: mustJSON(def.AllowedToolClasses),
		Autonomy:               def.Autonomy,
		Activation:             def.Activation,
		ProviderType:           def.ProviderType,
		Model:                  def.Model,
		IsEnabled:              def.Enabled,
		CreatedAt:              createdAt,
		UpdatedAt:              updatedAt,
	}
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func taskFromRow(row storage.AgentTaskRow, steps []storage.AgentTaskStepRow) taskJSON {
	out := taskJSON{
		TaskID:         row.TaskID,
		AgentID:        row.AgentID,
		Status:         normalizeTaskStatus(row.Status),
		Goal:           row.Goal,
		RequestedBy:    row.RequestedBy,
		ResultSummary:  row.ResultSummary,
		ErrorMessage:   row.ErrorMessage,
		ConversationID: row.ConversationID,
		StartedAt:      row.StartedAt,
		FinishedAt:     row.FinishedAt,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		// V1 structured fields (Phase 7)
		Title:          row.Title,
		Objective:      row.Objective,
		Mode:           row.Mode,
		Pattern:        row.Pattern,
		BlockingKind:   row.BlockingKind,
		BlockingDetail: row.BlockingDetail,
	}
	if len(steps) > 0 {
		out.Steps = make([]taskStepJSON, 0, len(steps))
		for _, step := range steps {
			out.Steps = append(out.Steps, taskStepJSON{
				StepID:         step.StepID,
				SequenceNumber: step.SequenceNumber,
				Role:           step.Role,
				StepType:       step.StepType,
				Content:        step.Content,
				ToolName:       step.ToolName,
				ToolCallID:     step.ToolCallID,
				CreatedAt:      step.CreatedAt,
			})
		}
	}
	return out
}

func (m *Module) listEventJSON(limit int) ([]agentEventJSON, error) {
	rows, err := m.store.ListAgentEvents(limit)
	if err != nil {
		return nil, err
	}
	out := make([]agentEventJSON, 0, len(rows))
	for _, row := range rows {
		payload := map[string]any{}
		_ = json.Unmarshal([]byte(row.PayloadJSON), &payload)
		out = append(out, agentEventJSON{
			EventID:   row.EventID,
			EventType: row.EventType,
			AgentID:   row.AgentID,
			TaskID:    row.TaskID,
			Title:     row.Title,
			Detail:    row.Detail,
			Payload:   payload,
			CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func (m *Module) teamDerivedState() ([]blockedItemJSON, []suggestedActionJSON, string, error) {
	tasks, err := m.store.ListAgentTasks(100)
	if err != nil {
		return nil, nil, "", err
	}
	defs, err := m.store.ListAgentDefinitions()
	if err != nil {
		return nil, nil, "", err
	}
	blocked := []blockedItemJSON{}
	suggested := []suggestedActionJSON{}
	atlasStatus := "online"
	if len(defs) == 0 {
		suggested = append(suggested, suggestedActionJSON{Kind: "team", ID: "add-agent", Title: "Add your first team member"})
		atlasStatus = "idle"
	}
	runningCount := 0
	for _, task := range tasks {
		// Normalize status to V1 canonical name before switching.
		// This handles both legacy rows ("running", "pending_approval", "error")
		// and V1 rows ("working", "needs_review", "failed") written by Phase 5+.
		canonical := normalizeTaskStatus(task.Status)

		// Task title: prefer structured Title field (V1), fall back to Goal.
		taskTitle := strings.TrimSpace(task.Title)
		if taskTitle == "" {
			taskTitle = task.Goal
		}

		switch canonical {
		case "running", "working":
			runningCount++
		case "needs_review":
			// Surface blocking_kind / blocking_detail from actual columns (Phase 7).
			bk, bd := "", ""
			if task.BlockingKind != nil {
				bk = *task.BlockingKind
			}
			if task.BlockingDetail != nil {
				bd = *task.BlockingDetail
			}
			// Fall back to approval kind when columns are empty (legacy rows).
			if bk == "" {
				bk = BlockingKindApproval
			}
			blocked = append(blocked, blockedItemJSON{
				Kind:           "task",
				ID:             task.TaskID,
				AgentID:        &task.AgentID,
				Title:          taskTitle,
				Status:         canonical,
				BlockingKind:   bk,
				BlockingDetail: bd,
			})
			suggested = append(suggested, suggestedActionJSON{
				Kind:    "task",
				ID:      task.TaskID,
				AgentID: &task.AgentID,
				Title:   "Review delegated task awaiting approval",
			})
			atlasStatus = "attention_needed"
		case "failed":
			blocked = append(blocked, blockedItemJSON{
				Kind:    "task",
				ID:      task.TaskID,
				AgentID: &task.AgentID,
				Title:   taskTitle,
				Status:  canonical,
			})
			if atlasStatus == "online" {
				atlasStatus = "attention_needed"
			}
		}
	}
	if runningCount > 0 && atlasStatus == "online" {
		atlasStatus = "working" // was "busy" — V1 canonical name
	}

	// M7: smart suggestions based on agent metrics
	metricsRows, _ := m.store.ListAgentMetrics()
	metricsMap := make(map[string]storage.AgentMetricsRow, len(metricsRows))
	for _, row := range metricsRows {
		metricsMap[row.AgentID] = row
	}
	for _, def := range defs {
		met, hasMet := metricsMap[def.ID]
		if !hasMet {
			continue
		}
		total := met.TasksCompleted + met.TasksFailed
		if total >= 3 {
			rate := float64(met.TasksFailed) / float64(total)
			if rate > 0.5 {
				defID := def.ID
				suggested = append(suggested, suggestedActionJSON{
					Kind:    "agent",
					ID:      "review-mission-" + def.ID,
					AgentID: &defID,
					Title:   fmt.Sprintf("%s has a high failure rate — consider reviewing its mission or allowed skills", def.Name),
				})
			}
		}
	}

	// Suggest creating a Scout if no agents at all
	if len(defs) == 0 {
		suggested = append(suggested, suggestedActionJSON{
			Kind:  "team",
			ID:    "create-scout",
			Title: "Add a Scout agent to handle research tasks for you",
		})
	}

	return blocked, suggested, atlasStatus, nil
}

func (m *Module) recordEvent(eventType string, agentID, taskID *string, title string, detail *string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	data, _ := json.Marshal(payload)
	return m.store.SaveAgentEvent(storage.AgentEventRow{
		EventID:     newID("teamevent"),
		EventType:   eventType,
		AgentID:     agentID,
		TaskID:      taskID,
		Title:       title,
		Detail:      detail,
		PayloadJSON: string(data),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	})
}


func (m *Module) resolveProvider() (agent.ProviderConfig, error) {
	if m.cfg == nil {
		return agent.ProviderConfig{}, fmt.Errorf("agents module is missing config reader")
	}
	return chat.ResolveProvider(m.cfg.Load())
}

// resolveProviderFor resolves the AI provider for a specific agent definition.
// When the agent has a ProviderType override, it uses Atlas's global config for
// the API key/baseURL but substitutes the agent's provider type and model.
func (m *Module) resolveProviderFor(def storage.AgentDefinitionRow) (agent.ProviderConfig, error) {
	base, err := m.resolveProvider()
	if err != nil {
		return agent.ProviderConfig{}, err
	}
	if def.ProviderType == "" {
		return base, nil
	}
	// Override provider type — reuse the base API key if it matches the same
	// provider family, otherwise try to resolve the named provider directly.
	overrideType := agent.ProviderType(def.ProviderType)
	if base.Type == overrideType {
		if def.Model != "" {
			base.Model = def.Model
		}
		return base, nil
	}
	// Attempt to resolve the override provider from the runtime config.
	cfg := m.cfg.Load()
	cfg.ActiveAIProvider = def.ProviderType
	if def.Model != "" {
		// Set provider-specific model field based on the provider type.
		switch overrideType {
		case agent.ProviderAnthropic:
			cfg.SelectedAnthropicModel = def.Model
		case agent.ProviderOpenAI:
			cfg.SelectedOpenAIPrimaryModel = def.Model
		case agent.ProviderGemini:
			cfg.SelectedGeminiModel = def.Model
		}
	}
	override, err := chat.ResolveProvider(cfg)
	if err != nil {
		// Fall back to global provider rather than failing hard.
		return base, nil
	}
	return override, nil
}
