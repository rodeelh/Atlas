package automations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

const (
	triggeredEventName = "automation.triggered.v1"
	completedEventName = "automation.completed.v1"
)

type Module struct {
	supportDir string
	store      platform.AutomationStore
	agent      platform.AgentRuntime
	bus        platform.EventBus
}

func New(supportDir string) *Module {
	return &Module{supportDir: supportDir}
}

func (m *Module) ID() string { return "automations" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{
		Version:   "v1",
		Publishes: []string{triggeredEventName, completedEventName},
	}
}

func (m *Module) Register(host platform.Host) error {
	m.store = host.Storage().Automations()
	m.agent = host.AgentRuntime()
	m.bus = host.Bus()
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/automations", m.listAutomations)
	r.Post("/automations", m.createAutomation)
	r.Get("/automations/file", m.getAutomationsFile)
	r.Put("/automations/file", m.putAutomationsFile)
	r.Get("/automations/{id}", m.getAutomation)
	r.Put("/automations/{id}", m.updateAutomation)
	r.Delete("/automations/{id}", m.deleteAutomation)
	r.Get("/automations/{id}/runs", m.getAutomationRuns)
	r.Post("/automations/{id}/enable", m.enableAutomation)
	r.Post("/automations/{id}/disable", m.disableAutomation)
	r.Post("/automations/{id}/run", m.runAutomation)
}

func (m *Module) listAutomations(w http.ResponseWriter, _ *http.Request) {
	items := features.ParseGremlins(m.supportDir)
	if items == nil {
		items = []features.GremlinItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (m *Module) getAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	for _, item := range features.ParseGremlins(m.supportDir) {
		if item.ID == id {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	writeError(w, http.StatusNotFound, "automation not found: "+id)
}

func (m *Module) getAutomationsFile(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"content": features.ReadGremlinsRaw(m.supportDir),
	})
}

func (m *Module) putAutomationsFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := features.WriteGremlinsRaw(m.supportDir, body.Content); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write automations file: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *Module) getAutomationRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rows, err := m.store.ListGremlinRuns(id, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read runs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toGremlinRunRecords(rows))
}

func (m *Module) enableAutomation(w http.ResponseWriter, r *http.Request) {
	m.setAutomationState(w, r, true)
}

func (m *Module) disableAutomation(w http.ResponseWriter, r *http.Request) {
	m.setAutomationState(w, r, false)
}

func (m *Module) setAutomationState(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := chi.URLParam(r, "id")
	items := features.ParseGremlins(m.supportDir)
	var found *features.GremlinItem
	for i := range items {
		if items[i].ID == id {
			found = &items[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	if err := features.SetAutomationEnabled(m.supportDir, id, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update automation: "+err.Error())
		return
	}
	found.IsEnabled = enabled
	writeJSON(w, http.StatusOK, found)
}

func (m *Module) createAutomation(w http.ResponseWriter, r *http.Request) {
	var item features.GremlinItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if item.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := features.AppendGremlin(m.supportDir, item); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, g := range features.ParseGremlins(m.supportDir) {
		if g.Name == item.Name {
			writeJSON(w, http.StatusCreated, g)
			return
		}
	}
	writeJSON(w, http.StatusCreated, item)
}

func (m *Module) updateAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var updates features.GremlinItem
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	updated, err := features.UpdateGremlin(m.supportDir, id, updates)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	found, err := features.DeleteGremlin(m.supportDir, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) runAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var found *features.GremlinItem
	for _, g := range features.ParseGremlins(m.supportDir) {
		g := g
		if g.ID == id {
			found = &g
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "automation not found: "+id)
		return
	}

	if m.agent == nil {
		writeError(w, http.StatusNotImplemented, "agent loop not available")
		return
	}

	runID := newID()
	now := time.Now().UTC()
	nowUnix := float64(now.UnixNano()) / 1e9
	convID := newID()

	if err := m.store.SaveGremlinRun(storage.GremlinRunRow{
		RunID:          runID,
		GremlinID:      found.ID,
		StartedAt:      nowUnix,
		Status:         "running",
		ConversationID: &convID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run: "+err.Error())
		return
	}

	if m.bus != nil {
		_ = m.bus.Publish(context.Background(), triggeredEventName, map[string]string{
			"id":             runID,
			"gremlinID":      found.ID,
			"conversationID": convID,
		})
	}

	go func(item features.GremlinItem) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		req := chat.MessageRequest{Message: item.Prompt, ConversationID: convID}
		resp, err := m.agent.HandleMessage(ctx, req)
		finishedAt := float64(time.Now().UnixNano()) / 1e9

		status := "completed"
		var output *string
		if err != nil {
			status = "failed"
			msg := err.Error()
			output = &msg
		} else {
			out := resp.Response.AssistantMessage
			if resp.Response.Status == "error" {
				status = "failed"
				out = resp.Response.ErrorMessage
			}
			output = &out
		}

		_ = m.store.UpdateGremlinRun(runID, status, output, finishedAt)
		if m.bus != nil {
			payload := map[string]string{
				"id":        runID,
				"gremlinID": item.ID,
				"status":    status,
			}
			if output != nil {
				payload["output"] = *output
			}
			_ = m.bus.Publish(context.Background(), completedEventName, payload)
		}
	}(*found)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":             runID,
		"gremlinID":      found.ID,
		"conversationID": convID,
		"status":         "running",
	})
}

func toGremlinRunRecords(rows []storage.GremlinRunRow) []features.GremlinRunRecord {
	out := make([]features.GremlinRunRecord, 0, len(rows))
	for _, row := range rows {
		rec := features.GremlinRunRecord{
			RunID:          row.RunID,
			GremlinID:      row.GremlinID,
			StartedAt:      time.Unix(int64(row.StartedAt), 0).UTC().Format(time.RFC3339),
			Status:         row.Status,
			Output:         row.Output,
			ErrorMessage:   row.ErrorMessage,
			ConversationID: row.ConversationID,
			WorkflowRunID:  row.WorkflowRunID,
		}
		if row.FinishedAt != nil {
			ts := time.Unix(int64(*row.FinishedAt), 0).UTC().Format(time.RFC3339)
			rec.FinishedAt = &ts
		}
		out = append(out, rec)
	}
	return out
}

func newID() string {
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
