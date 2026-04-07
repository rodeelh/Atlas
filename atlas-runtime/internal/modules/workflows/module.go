package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/workflowexec"
)

const (
	startedEventName   = "workflow.started.v1"
	completedEventName = "workflow.completed.v1"
)

type Module struct {
	supportDir string
	store      platform.WorkflowStore
	agent      platform.AgentRuntime
	bus        platform.EventBus
	skills     *skills.Registry
}

func New(supportDir string) *Module {
	return &Module{supportDir: supportDir}
}

func (m *Module) ID() string { return "workflows" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{
		Version:   "v1",
		Publishes: []string{startedEventName, completedEventName},
	}
}

func (m *Module) Register(host platform.Host) error {
	m.store = host.Storage().Workflows()
	m.agent = host.AgentRuntime()
	m.bus = host.Bus()
	if err := m.importLegacyDefinitions(); err != nil {
		return fmt.Errorf("import legacy workflows: %w", err)
	}
	m.registerAgentActions()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) SetSkillRegistry(registry *skills.Registry) {
	m.skills = registry
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/workflows", m.listWorkflows)
	r.Post("/workflows", m.createWorkflow)
	r.Get("/workflows/summaries", m.listWorkflowSummaries)
	r.Get("/workflows/runs", m.listWorkflowRuns)
	r.Post("/workflows/runs/{runID}/approve", m.approveWorkflowRun)
	r.Post("/workflows/runs/{runID}/deny", m.denyWorkflowRun)
	r.Get("/workflows/{id}", m.getWorkflow)
	r.Put("/workflows/{id}", m.updateWorkflow)
	r.Delete("/workflows/{id}", m.deleteWorkflow)
	r.Get("/workflows/{id}/runs", m.getWorkflowRuns)
	r.Post("/workflows/{id}/run", m.runWorkflow)
}

func (m *Module) listWorkflows(w http.ResponseWriter, _ *http.Request) {
	items, err := m.listDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflows: "+err.Error())
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (m *Module) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	def, ok, err := m.getDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, def)
}

func (m *Module) listWorkflowRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := m.listRuns("", 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow runs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (m *Module) getWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	runs, err := m.listRuns(id, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow runs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (m *Module) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := m.createDefinition(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (m *Module) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, ok, err := m.updateDefinition(id, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	found, err := m.deleteDefinition(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) runWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		InputValues map[string]string `json:"inputValues"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	prepared, err := m.startWorkflowRun(id, body.InputValues, "http")
	if err != nil {
		writeWorkflowRunError(w, err)
		return
	}
	go m.finishWorkflowRun(context.Background(), prepared)
	writeJSON(w, http.StatusAccepted, prepared.Record)
}

func (m *Module) approveWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runID")
	result, err := m.updateRunStatus(runID, "approved")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) denyWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runID")
	result, err := m.updateRunStatus(runID, "denied")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) startWorkflowRun(id string, inputValues map[string]string, triggerSource string) (workflowexec.PreparedRun, error) {
	if m.agent == nil {
		return workflowexec.PreparedRun{}, errAgentUnavailable
	}
	def, ok, err := m.getDefinition(id)
	if err != nil {
		return workflowexec.PreparedRun{}, err
	}
	if !ok {
		return workflowexec.PreparedRun{}, fmt.Errorf("workflow not found: %s", id)
	}
	if enabled, ok := def["isEnabled"].(bool); ok && !enabled {
		return workflowexec.PreparedRun{}, fmt.Errorf("workflow disabled: %s", id)
	}
	runID := newID()
	convID := newID()
	prepared, err := workflowexec.PrepareRun(m.store, id, runID, convID, triggerSource, inputValues, "")
	if err != nil {
		return workflowexec.PreparedRun{}, err
	}
	m.publishStarted(prepared)
	return prepared, nil
}

func (m *Module) finishWorkflowRun(parent context.Context, prepared workflowexec.PreparedRun) map[string]any {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	status, summary, errorMessage, stepRuns := m.executePreparedWorkflow(ctx, prepared)
	_ = workflowexec.CompleteRun(m.store, prepared.RunID, status, summary, errorMessage, prepared.StartedAt)
	m.publishCompleted(prepared, status)
	record := prepared.Record
	record["status"] = status
	record["stepRuns"] = stepRuns
	if summary != "" {
		record["assistantSummary"] = summary
	}
	if errorMessage != "" {
		record["errorMessage"] = errorMessage
	}
	return record
}

func (m *Module) runWorkflowSync(ctx context.Context, id string, inputValues map[string]string, triggerSource string) (map[string]any, error) {
	prepared, err := m.startWorkflowRun(id, inputValues, triggerSource)
	if err != nil {
		return nil, err
	}
	record := m.finishWorkflowRun(ctx, prepared)
	if status, _ := record["status"].(string); status == "failed" {
		if msg, _ := record["errorMessage"].(string); msg != "" {
			return record, fmt.Errorf("workflow %q failed: %s", id, msg)
		}
		return record, fmt.Errorf("workflow %q failed", id)
	}
	return record, nil
}

func (m *Module) publishStarted(prepared workflowexec.PreparedRun) {
	if m.bus == nil {
		return
	}
	_ = m.bus.Publish(context.Background(), startedEventName, map[string]string{
		"id":             prepared.RunID,
		"workflowID":     prepared.WorkflowID,
		"conversationID": prepared.ConversationID,
	})
}

func (m *Module) publishCompleted(prepared workflowexec.PreparedRun, status string) {
	if m.bus == nil {
		return
	}
	_ = m.bus.Publish(context.Background(), completedEventName, map[string]string{
		"id":             prepared.RunID,
		"workflowID":     prepared.WorkflowID,
		"conversationID": prepared.ConversationID,
		"status":         status,
	})
}

var errAgentUnavailable = fmt.Errorf("agent loop not available")

func writeWorkflowRunError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case err == errAgentUnavailable:
		writeError(w, http.StatusNotImplemented, msg)
	case strings.HasPrefix(msg, "workflow not found:"):
		writeError(w, http.StatusNotFound, msg)
	case strings.HasPrefix(msg, "workflow disabled:"):
		writeError(w, http.StatusConflict, msg)
	default:
		writeError(w, http.StatusInternalServerError, msg)
	}
}

func newID() string {
	return "workflow-" + time.Now().UTC().Format("20060102150405.000000000")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
