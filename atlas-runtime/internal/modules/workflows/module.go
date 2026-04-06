package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
)

const (
	startedEventName   = "workflow.started.v1"
	completedEventName = "workflow.completed.v1"
)

type Module struct {
	supportDir string
	agent      platform.AgentRuntime
	bus        platform.EventBus
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
	m.agent = host.AgentRuntime()
	m.bus = host.Bus()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/workflows", m.listWorkflows)
	r.Post("/workflows", m.createWorkflow)
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
	writeJSON(w, http.StatusOK, features.ListWorkflowDefinitions(m.supportDir))
}

func (m *Module) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	raw := features.GetWorkflowDefinition(m.supportDir, id)
	if raw == nil {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (m *Module) listWorkflowRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, features.ListWorkflowRuns(m.supportDir, ""))
}

func (m *Module) getWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, features.ListWorkflowRuns(m.supportDir, id))
}

func (m *Module) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, ok := body["id"]; !ok {
		body["id"] = newID()
	}
	if _, ok := body["createdAt"]; !ok {
		body["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := features.AppendWorkflowDefinition(m.supportDir, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	body["id"] = id
	result, err := features.UpdateWorkflowDefinition(m.supportDir, id, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	found, err := features.DeleteWorkflowDefinition(m.supportDir, id)
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
	raw := features.GetWorkflowDefinition(m.supportDir, id)
	if raw == nil {
		writeError(w, http.StatusNotFound, "workflow not found: "+id)
		return
	}

	var def map[string]any
	if err := json.Unmarshal(raw, &def); err != nil {
		writeError(w, http.StatusInternalServerError, "corrupt workflow definition")
		return
	}

	if m.agent == nil {
		writeError(w, http.StatusNotImplemented, "agent loop not available")
		return
	}

	runID := newID()
	convID := newID()
	now := time.Now().UTC().Format(time.RFC3339)

	prompt, _ := def["prompt"].(string)
	if prompt == "" {
		if desc, _ := def["description"].(string); desc != "" {
			prompt = desc
		} else if name, _ := def["name"].(string); name != "" {
			prompt = "Execute workflow: " + name
		} else {
			prompt = "Execute this workflow."
		}
	}

	run := map[string]any{
		"id":             runID,
		"workflowID":     id,
		"status":         "running",
		"startedAt":      now,
		"conversationID": convID,
	}
	_ = features.AppendWorkflowRun(m.supportDir, run)

	if m.bus != nil {
		_ = m.bus.Publish(context.Background(), startedEventName, map[string]string{
			"id":             runID,
			"workflowID":     id,
			"conversationID": convID,
		})
	}

	go func(prompt, runID, workflowID, conversationID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		req := chat.MessageRequest{Message: prompt, ConversationID: conversationID}
		resp, execErr := m.agent.HandleMessage(ctx, req)
		status := "completed"
		if execErr != nil || resp.Response.Status == "error" {
			status = "failed"
		}
		_, _ = features.UpdateWorkflowRunStatus(m.supportDir, runID, status)
		if m.bus != nil {
			_ = m.bus.Publish(context.Background(), completedEventName, map[string]string{
				"id":             runID,
				"workflowID":     workflowID,
				"conversationID": conversationID,
				"status":         status,
			})
		}
	}(prompt, runID, id, convID)

	writeJSON(w, http.StatusAccepted, run)
}

func (m *Module) approveWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runID")
	result, err := features.UpdateWorkflowRunStatus(m.supportDir, runID, "approved")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) denyWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runID")
	result, err := features.UpdateWorkflowRunStatus(m.supportDir, runID, "denied")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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
