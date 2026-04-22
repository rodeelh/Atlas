package dashboards

// routes.go — HTTP routes for v2 dashboards.
//
// Public surface (all under /dashboards and gated by session auth):
//
//   GET    /dashboards                       list
//   POST   /dashboards                       create empty draft
//   GET    /dashboards/{id}                  fetch one
//   DELETE /dashboards/{id}                  delete
//   POST   /dashboards/{id}/draft            create/resume editable draft
//   POST   /dashboards/{id}/commit           publish a draft dashboard
//   PATCH  /dashboards/{id}/layout           update draft widget grid positions
//   POST   /dashboards/{id}/widgets          add a preset widget to a draft
//   POST   /dashboards/{id}/code-widgets     add a code widget to a draft
//   POST   /dashboards/{id}/ai-widget        add one AI-authored widget to a draft
//   POST   /dashboards/{id}/sources          add or replace a draft source
//   DELETE /dashboards/{id}/sources/{source} delete a draft source
//   PATCH  /dashboards/{id}/widgets/{widgetId} update a draft widget
//   DELETE /dashboards/{id}/widgets/{widgetId} delete a draft widget
//   POST   /dashboards/{id}/sources/{source}/refresh refresh one source
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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/dashboards", m.listDashboards)
	r.Post("/dashboards", m.createDashboard)
	r.Get("/dashboards/{id}", m.getDashboard)
	r.Delete("/dashboards/{id}", m.deleteDashboard)
	r.Post("/dashboards/{id}/draft", m.editDashboardDraft)
	r.Post("/dashboards/{id}/commit", m.commitDashboardDraft)
	r.Patch("/dashboards/{id}/layout", m.updateDashboardLayout)
	r.Post("/dashboards/{id}/widgets", m.createDashboardWidget)
	r.Post("/dashboards/{id}/code-widgets", m.createDashboardCodeWidget)
	r.Post("/dashboards/{id}/ai-widget", m.createDashboardAIWidget)
	r.Post("/dashboards/{id}/sources", m.upsertDashboardSource)
	r.Delete("/dashboards/{id}/sources/{source}", m.deleteDashboardSource)
	r.Patch("/dashboards/{id}/widgets/{widgetId}", m.updateDashboardWidget)
	r.Delete("/dashboards/{id}/widgets/{widgetId}", m.deleteDashboardWidget)
	r.Post("/dashboards/{id}/sources/{source}/refresh", m.refreshDashboardSource)
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

func (m *Module) createDashboard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	created, err := m.createDraftDashboard(req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
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

func (m *Module) editDashboardDraft(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	draft, err := m.editDraftForDashboard(id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (m *Module) commitDashboardDraft(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	published, err := m.commitDraftDashboard(ctx, id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, published)
}

func (m *Module) updateDashboardLayout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req LayoutUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	updated, err := m.updateDraftLayout(id, req)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) createDashboardWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Title       string              `json:"title"`
		Description string              `json:"description"`
		Size        string              `json:"size"`
		Group       string              `json:"group"`
		Preset      string              `json:"preset"`
		Bindings    []DataSourceBinding `json:"bindings"`
		Options     map[string]any      `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	updated, _, err := m.addDraftWidget(id, Widget{
		Title:       req.Title,
		Description: req.Description,
		Size:        req.Size,
		Group:       req.Group,
		Bindings:    req.Bindings,
		Code: WidgetCode{
			Mode:    ModePreset,
			Preset:  req.Preset,
			Options: req.Options,
		},
	})
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

func (m *Module) createDashboardCodeWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Title       string              `json:"title"`
		Description string              `json:"description"`
		Size        string              `json:"size"`
		Group       string              `json:"group"`
		Bindings    []DataSourceBinding `json:"bindings"`
		TSX         string              `json:"tsx"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	updated, _, err := m.addDraftWidget(id, Widget{
		Title:       req.Title,
		Description: req.Description,
		Size:        req.Size,
		Group:       req.Group,
		Bindings:    req.Bindings,
		Code: WidgetCode{
			Mode: ModeCode,
			TSX:  req.TSX,
		},
	})
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

func (m *Module) createDashboardAIWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Prompt string `json:"prompt"`
		Source string `json:"source"`
		Title  string `json:"title"`
		Size   string `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	updated, _, err := m.addAIWidget(ctx, id, AIWidgetPromptRequest{
		Prompt:     req.Prompt,
		SourceName: req.Source,
		TitleHint:  req.Title,
		SizeHint:   req.Size,
	})
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

func (m *Module) upsertDashboardSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name            string         `json:"name"`
		Kind            string         `json:"kind"`
		Config          map[string]any `json:"config"`
		RefreshMode     string         `json:"refreshMode"`
		IntervalSeconds int            `json:"intervalSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	updated, err := m.upsertDraftSource(id, DataSource{
		Name:   req.Name,
		Kind:   req.Kind,
		Config: req.Config,
		Refresh: RefreshPolicy{
			Mode:            req.RefreshMode,
			IntervalSeconds: req.IntervalSeconds,
		},
	})
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) deleteDashboardSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sourceName := chi.URLParam(r, "source")
	updated, err := m.deleteDraftSource(id, sourceName)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// updateDashboardWidget updates one widget on a draft dashboard.
// Body accepts the same editable fields as dashboard.update_widget and returns
// the full dashboard definition so the viewer can refresh from server state.
func (m *Module) updateDashboardWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	widgetID := chi.URLParam(r, "widgetId")
	var req WidgetUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	updated, err := m.updateDraftWidget(id, widgetID, req)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) deleteDashboardWidget(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	widgetID := chi.URLParam(r, "widgetId")
	updated, err := m.deleteDraftWidget(id, widgetID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "is not a draft") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (m *Module) refreshDashboardSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	source := chi.URLParam(r, "source")
	if _, err := m.store.Get(id); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	event := m.coordinator.ForceRefreshSource(ctx, id, source)
	if event == nil {
		writeError(w, http.StatusNotFound, "source not found: "+source)
		return
	}
	writeJSON(w, http.StatusOK, event)
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
