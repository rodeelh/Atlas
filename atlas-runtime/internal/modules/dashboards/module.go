package dashboards

// module.go — first-party "dashboards" platform module: routes, handlers, and
// dependency wiring. This is stage 2 of the dashboards plan; it provides every
// HTTP surface needed for the (later) frontend to render real dashboards.
//
// Wiring shape (will be set up from cmd/atlas-runtime/main.go in stage 2 or
// stage 3 — module exposes setters so wiring can happen after construction):
//
//   m := dashboards.New(supportDir, dbPath)
//   m.SetRuntimeFetcher(dashboards.NewLoopbackFetcher(cfg.RuntimePort))
//   m.SetSkillExecutor(skillRegistry)
//   registry.Register(m)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/platform"
)

// Module is the platform.Module implementation for dashboards.
type Module struct {
	supportDir       string
	dbPath           string
	store            *Store
	runtime          RuntimeFetcher
	skills           SkillExecutor
	webClient        *http.Client     // overridable in tests
	providerResolver ProviderResolver // injected at startup; needed by dashboard.create
}

// New constructs a Module rooted at supportDir. dbPath is the path to the
// runtime SQLite database (used by the SQL resolver in read-only mode).
func New(supportDir, dbPath string) *Module {
	return &Module{
		supportDir: supportDir,
		dbPath:     dbPath,
		store:      NewStore(supportDir),
	}
}

// ID implements platform.Module.
func (m *Module) ID() string { return "dashboards" }

// Manifest implements platform.Module.
func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{Version: "v1"}
}

// Register implements platform.Module. Mounts protected routes onto the host.
func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

// Start implements platform.Module.
func (m *Module) Start(context.Context) error { return nil }

// Stop implements platform.Module.
func (m *Module) Stop(context.Context) error { return nil }

// SetRuntimeFetcher injects the loopback fetcher (or a stub in tests).
func (m *Module) SetRuntimeFetcher(f RuntimeFetcher) { m.runtime = f }

// SetSkillExecutor injects the skill registry (or a stub in tests).
func (m *Module) SetSkillExecutor(s SkillExecutor) { m.skills = s }

// SetProviderResolver injects the AI provider resolver used by dashboard.create.
func (m *Module) SetProviderResolver(r ProviderResolver) { m.providerResolver = r }

// Store returns the underlying dashboard store. Used by stage-3 skills to
// avoid round-tripping HTTP for read-only listing/get.
func (m *Module) Store() *Store { return m.store }

// ── routes ────────────────────────────────────────────────────────────────────

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/dashboards", m.listDashboards)
	r.Post("/dashboards", m.createDashboard)
	r.Get("/dashboards/templates", m.listTemplates)
	r.Get("/dashboards/{id}", m.getDashboard)
	r.Put("/dashboards/{id}", m.updateDashboard)
	r.Delete("/dashboards/{id}", m.deleteDashboard)
	r.Post("/dashboards/{id}/resolve", m.resolveWidget)
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (m *Module) listDashboards(w http.ResponseWriter, _ *http.Request) {
	defs := m.store.List()
	out := make([]Summary, 0, len(defs))
	for _, d := range defs {
		out = append(out, SummaryFor(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) listTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Templates())
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

// createDashboard accepts either { template: "<id>" } to clone a template or
// { definition: { ... } } to install a hand-crafted definition.
func (m *Module) createDashboard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Template   string               `json:"template"`
		Definition *DashboardDefinition `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	var def DashboardDefinition
	switch {
	case body.Template != "":
		tmpl, ok := TemplateByID(body.Template)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown template: "+body.Template)
			return
		}
		def = tmpl.Definition
	case body.Definition != nil:
		def = *body.Definition
	default:
		writeError(w, http.StatusBadRequest, "request must include either template or definition")
		return
	}

	if def.ID == "" {
		def.ID = newDashboardID()
	}
	if def.Name == "" {
		writeError(w, http.StatusBadRequest, "dashboard name is required")
		return
	}
	// Ensure widgets have IDs.
	for i := range def.Widgets {
		if def.Widgets[i].ID == "" {
			def.Widgets[i].ID = fmt.Sprintf("widget-%d", i+1)
		}
	}

	saved, err := m.store.Save(def)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (m *Module) updateDashboard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body DashboardDefinition
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	body.ID = id
	if _, err := m.store.Get(id); errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "dashboard not found: "+id)
		return
	}
	saved, err := m.store.Save(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
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

// resolveWidget loads a single widget's data from its declared source.
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

	// Per-resolve hard cap. Web fetches and SQL queries each apply their own
	// tighter timeout on top of this.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	start := time.Now()
	data, err := m.resolveSource(ctx, widget.Source)
	dur := time.Since(start).Milliseconds()

	wd := WidgetData{
		WidgetID:   body.WidgetID,
		ResolvedAt: time.Now().UTC().Format(time.RFC3339),
		DurationMs: dur,
	}
	if widget.Source != nil {
		wd.SourceKind = widget.Source.Kind
	}
	if err != nil {
		wd.Success = false
		wd.Error = err.Error()
		// Surface auth/permission errors as 403, everything else 200 with
		// success=false so the dashboard can render error tiles.
		if isPermissionError(err) {
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

// isPermissionError reports whether err originated from a safety/allowlist
// rejection rather than a runtime fetch failure. Used so the resolve handler
// returns 403 for sandbox violations.
//
// Keep this list aligned with the error strings produced by safety.go and the
// per-resolver allowlist checks. Any safety rejection that is not classified
// here would leak through as a 200/success=false, which is harmless but
// confuses callers expecting an HTTP-level signal.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	// runtime allowlist
	case strings.Contains(msg, "not on the dashboards allowlist"):
		return true
	case strings.Contains(msg, "runtime endpoint must start with /"):
		return true
	// skill allowlist
	case strings.Contains(msg, "is not read-only"):
		return true
	case strings.Contains(msg, "unknown skill action"):
		return true
	// web SSRF / scheme guards
	case strings.Contains(msg, "non-public address"):
		return true
	case strings.Contains(msg, "host is local"):
		return true
	case strings.Contains(msg, "scheme must be http or https"):
		return true
	case strings.Contains(msg, "invalid web url"):
		return true
	// SQL lexer
	case strings.Contains(msg, "may not contain"):
		return true
	case strings.Contains(msg, "must start with SELECT"):
		return true
	case strings.Contains(msg, "must contain a single statement"):
		return true
	}
	return false
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newDashboardID() string {
	return "dashboard-" + time.Now().UTC().Format("20060102150405.000000000")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
