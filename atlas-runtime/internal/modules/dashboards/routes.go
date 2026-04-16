package dashboards

// routes.go — HTTP routes for v2 dashboards.
//
// Public surface (all under /dashboards and gated by session auth):
//
//   GET    /dashboards                       list
//   GET    /dashboards/{id}                  fetch one
//   DELETE /dashboards/{id}                  delete
//   POST   /dashboards/{id}/resolve          resolve a widget's data (one-shot)
//   POST   /dashboards/{id}/refresh          force all sources to refresh
//   GET    /dashboards/{id}/events           SSE stream of RefreshEvents
//
// Creation, mutation, and commit of drafts are driven through the agent
// skills in skills.go rather than direct HTTP endpoints — agents are the
// primary author.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/dashboards", m.listDashboards)
	r.Get("/dashboards/{id}", m.getDashboard)
	r.Delete("/dashboards/{id}", m.deleteDashboard)
	r.Post("/dashboards/{id}/resolve", m.resolveWidget)
	r.Post("/dashboards/{id}/refresh", m.refreshDashboard)
	r.Get("/dashboards/{id}/events", m.streamDashboardEvents)
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (m *Module) listDashboards(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	defs := m.store.ListByStatus(status)
	out := make([]Summary, 0, len(defs))
	for _, d := range defs {
		out = append(out, SummaryFor(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) getDashboard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	def, err := m.store.Get(id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, def)
}

func (m *Module) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := m.store.Delete(id); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveWidget loads a single widget's data from its bindings.
// Body: { "widgetId": "..." }
func (m *Module) resolveWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	def, err := m.store.Get(id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var body struct {
		WidgetID string `json:"widgetId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.WidgetID == "" {
		writeError(w, http.StatusBadRequest, "widgetId is required")
		return
	}

	var widget *Widget
	for i := range def.Widgets {
		if def.Widgets[i].ID == body.WidgetID {
			widget = &def.Widgets[i]
			break
		}
	}
	if widget == nil {
		writeError(w, http.StatusNotFound, "widget not found: "+body.WidgetID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	start := time.Now()
	data, resolveErr := m.resolveWidgetData(ctx, def, *widget)
	wd := WidgetData{
		WidgetID:   body.WidgetID,
		ResolvedAt: time.Now().UTC().Format(time.RFC3339),
		DurationMs: time.Since(start).Milliseconds(),
	}
	if len(widget.Bindings) > 0 {
		wd.Source = widget.Bindings[0].Source
		// Surface the kind for the first binding (best-effort).
		for _, s := range def.Sources {
			if s.Name == widget.Bindings[0].Source {
				wd.SourceKind = s.Kind
				break
			}
		}
	}
	if resolveErr != nil {
		wd.Success = false
		wd.Error = resolveErr.Error()
		if isPermissionError(resolveErr) {
			writeJSON(w, http.StatusForbidden, wd)
			return
		}
		writeJSON(w, http.StatusOK, wd)
		return
	}
	wd.Success = true
	wd.Data = data
	writeJSON(w, http.StatusOK, wd)
}

// refreshDashboard forces every source to re-resolve and returns the events.
func (m *Module) refreshDashboard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := m.store.Get(id); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	events := m.coordinator.ForceRefresh(ctx, id)
	writeJSON(w, http.StatusOK, events)
}

// streamDashboardEvents is an SSE endpoint. It subscribes to the coordinator
// for the given dashboard and writes one event per refresh until the client
// disconnects.
func (m *Module) streamDashboardEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := m.store.Get(id); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsubscribe := m.coordinator.Subscribe(id)
	defer unsubscribe()

	// Initial nudge so the browser EventSource fires onopen.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
