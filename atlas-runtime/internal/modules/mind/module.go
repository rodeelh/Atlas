// Package mind (module) exposes HTTP routes for the mind-thoughts subsystem:
// manually firing a nap, reading telemetry, and (in later phases) the Mind
// Health dashboard data endpoints. Registered from cmd/atlas-runtime/main.go
// via platform.Register.
//
// This package is intentionally thin — all behavior lives in internal/mind
// and internal/mind/telemetry. The module just wires HTTP handlers to the
// underlying functions and formats JSON responses.
package mind

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/mind/thoughts"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

// ProviderResolver returns a fresh provider config from the current runtime
// settings. Matches the shape used by the dream cycle.
type ProviderResolver func() (agent.ProviderConfig, error)

// SkillsLister returns the current list of skill ids + descriptions that
// the nap prompt should see. Passed in instead of importing the features
// package here to keep the module graph shallow.
type SkillsLister func() []mind.SkillLine

// Module is the platform.Module implementation for mind-thoughts HTTP surface.
type Module struct {
	supportDir   string
	db           *storage.DB
	cfgStore     *config.Store
	provider     ProviderResolver
	skills       SkillsLister
	telemetry    *telemetry.Emitter
	dispatcher   *mind.Dispatcher
}

// New constructs a mind HTTP module. All dependencies are optional at
// construction time — use the setters before Register to inject them.
func New(supportDir string, db *storage.DB, cfgStore *config.Store, tel *telemetry.Emitter) *Module {
	return &Module{
		supportDir: supportDir,
		db:         db,
		cfgStore:   cfgStore,
		telemetry:  tel,
	}
}

// SetProviderResolver wires the provider resolver.
func (m *Module) SetProviderResolver(resolver ProviderResolver) { m.provider = resolver }

// SetSkillsLister wires the skills lister.
func (m *Module) SetSkillsLister(lister SkillsLister) { m.skills = lister }

// SetDispatcher wires the action-gate dispatcher so manual naps via
// POST /mind/nap also run through dispatch.
func (m *Module) SetDispatcher(d *mind.Dispatcher) { m.dispatcher = d }

// ID implements platform.Module.
func (m *Module) ID() string { return "mind" }

// Manifest implements platform.Module.
func (m *Module) Manifest() platform.Manifest { return platform.Manifest{Version: "v1"} }

// Register mounts the protected routes on the host.
func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Post("/mind/nap", m.runNap)
	r.Post("/mind/dispatch", m.runDispatch)
	r.Get("/mind/thoughts", m.readThoughts)
	r.Post("/mind/thoughts/seed", m.seedThought)
	r.Delete("/mind/thoughts/{id}", m.deleteThought)
	r.Get("/mind/telemetry", m.queryTelemetry)
	r.Get("/mind/telemetry/summary", m.telemetrySummary)
}

// runDispatch handles POST /mind/dispatch — runs the action gate directly on
// the current thoughts list without firing a nap. Developer-facing endpoint
// used to exercise the auto-execute + propose paths in isolation from nap
// curation. Gated on the master ThoughtsEnabled flag.
func (m *Module) runDispatch(w http.ResponseWriter, r *http.Request) {
	cfg := m.cfgStore.Load()
	if !cfg.ThoughtsEnabled {
		writeError(w, http.StatusConflict, "mind-thoughts feature is disabled")
		return
	}
	if m.dispatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatcher not configured")
		return
	}
	list, err := mind.ReadThoughtsSection(m.supportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read thoughts: "+err.Error())
		return
	}
	result := m.dispatcher.Dispatch(r.Context(), list,
		cfg.ThoughtMaxAutoExecPerNap, cfg.ThoughtMaxAutoExecPerDay, "manual-dispatch")
	writeJSON(w, http.StatusOK, result)
}

// runNap handles POST /mind/nap — the manual nap trigger. Runs synchronously
// so the caller (typically the developer via curl) sees the full NapResult
// in the response body. Phase 3 adds automatic triggers elsewhere; this
// endpoint remains useful for debugging and demos.
//
// Master gate: refuses with 409 when ThoughtsEnabled is false so the
// manual trigger is not a loophole around the opt-in.
func (m *Module) runNap(w http.ResponseWriter, r *http.Request) {
	cfg := m.cfgStore.Load()
	if !cfg.ThoughtsEnabled {
		writeError(w, http.StatusConflict, "mind-thoughts feature is disabled (set thoughtsEnabled: true in config)")
		return
	}
	if m.provider == nil {
		writeError(w, http.StatusServiceUnavailable, "mind: provider resolver not configured")
		return
	}
	provider, err := m.provider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mind: provider: "+err.Error())
		return
	}
	var skills []string
	if m.skills != nil {
		skills = mind.FormatSkillsList(m.skills())
	}
	deps := mind.NapDeps{
		SupportDir: m.supportDir,
		DB:         m.db,
		Provider:   provider,
		Cfg:        m.cfgStore.Load(),
		Telemetry:  m.telemetry,
		SkillsList: skills,
		Dispatcher: m.dispatcher,
	}
	result := mind.RunNap(r.Context(), deps, mind.TriggerManual)
	writeJSON(w, http.StatusOK, result)
}

// readThoughts handles GET /mind/thoughts — returns the current THOUGHTS
// section as a JSON array. Read-only, no lock. When the master feature
// flag is off, returns an empty list so the frontend presence line and
// dashboard widgets don't leak historical state.
func (m *Module) readThoughts(w http.ResponseWriter, r *http.Request) {
	if !m.cfgStore.Load().ThoughtsEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"thoughts": []any{}, "count": 0})
		return
	}
	list, err := mind.ReadThoughtsSection(m.supportDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read thoughts: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thoughts": list, "count": len(list)})
}

// seedThoughtRequest is the JSON body for POST /mind/thoughts/seed.
// All fields are required except provenance and action.
type seedThoughtRequest struct {
	Body       string                  `json:"body"`
	Confidence int                     `json:"confidence"`
	Value      int                     `json:"value"`
	Class      string                  `json:"class"`
	Source     string                  `json:"source"`
	Provenance string                  `json:"provenance,omitempty"`
	Action     *thoughts.ProposedAction `json:"action,omitempty"`
}

// seedThought handles POST /mind/thoughts/seed — a developer-facing endpoint
// that injects a hand-crafted thought into the THOUGHTS section by running
// it through thoughts.Apply. Useful for exercising the write path without
// an actual nap, and for seeding scenarios for the few-day review. Emits
// the same thought_added telemetry event a nap would.
//
// Gated on the master ThoughtsEnabled flag — no writing to the thoughts
// section when the feature is off.
func (m *Module) seedThought(w http.ResponseWriter, r *http.Request) {
	if !m.cfgStore.Load().ThoughtsEnabled {
		writeError(w, http.StatusConflict, "mind-thoughts feature is disabled")
		return
	}
	var req seedThoughtRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}
	if req.Source == "" {
		req.Source = "seed"
	}
	class := thoughts.ActionClass(req.Class)
	if !class.Valid() {
		writeError(w, http.StatusBadRequest, "class must be one of read, local_write, destructive_local, external_side_effect, send_publish_delete")
		return
	}

	op := thoughts.Op{
		Kind:       thoughts.OpAdd,
		Body:       req.Body,
		Confidence: req.Confidence,
		Value:      req.Value,
		Class:      class,
		Source:     req.Source,
		Provenance: req.Provenance,
		Action:     req.Action,
	}

	now := time.Now().UTC()
	var added thoughts.Thought
	err := mind.UpdateThoughtsSection(m.supportDir, func(current []thoughts.Thought) ([]thoughts.Thought, error) {
		out, results, err := thoughts.Apply(current, []thoughts.Op{op}, now)
		if err != nil {
			return nil, err
		}
		for _, res := range results {
			if res.Outcome != "applied" {
				return nil, fmtErr("seed rejected: "+res.Outcome, res.Error)
			}
			// Pick the newly-added thought out of the result list.
			for _, t := range out {
				if t.ID == res.ThoughtID {
					added = t
					break
				}
			}
		}
		return out, nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apply: "+err.Error())
		return
	}

	// Emit a thought_added telemetry event so the seeded thought appears in
	// the same analysis surface as nap-born thoughts.
	if m.telemetry != nil {
		m.telemetry.Emit(telemetry.KindThoughtAdded, added.ID, "", map[string]any{
			"outcome":    "applied",
			"source":     "seed",
			"body":       added.Body,
			"score":      added.Score,
			"class":      string(added.Class),
			"confidence": added.Confidence,
			"value":      added.Value,
			"provenance": added.Provenance,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"thought": added})
}

// deleteThought handles DELETE /mind/thoughts/{id} — developer-facing
// surgical removal of a single thought, bypassing nap curation. Used for
// cleanup during the few-day review.
func (m *Module) deleteThought(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	now := time.Now().UTC()
	found := false
	err := mind.UpdateThoughtsSection(m.supportDir, func(current []thoughts.Thought) ([]thoughts.Thought, error) {
		out, results, err := thoughts.Apply(current, []thoughts.Op{{
			Kind: thoughts.OpDiscard, ID: id,
		}}, now)
		if err != nil {
			return nil, err
		}
		for _, res := range results {
			if res.Outcome == "applied" {
				found = true
			}
		}
		return out, nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apply: "+err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "thought not found")
		return
	}
	if m.telemetry != nil {
		m.telemetry.Emit(telemetry.KindThoughtDiscarded, id, "", map[string]any{
			"outcome": "applied",
			"source":  "manual_delete",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// fmtErr is a tiny helper for wrapping two strings into an error.
func fmtErr(msg, detail string) error {
	if detail == "" {
		return &simpleError{msg: msg}
	}
	return &simpleError{msg: msg + ": " + detail}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// queryTelemetry handles GET /mind/telemetry — filter by kind, thought id,
// and time window. Supports the analysis workflow during the few-day review.
func (m *Module) queryTelemetry(w http.ResponseWriter, r *http.Request) {
	filter := storage.MindTelemetryFilter{}

	if k := r.URL.Query().Get("kind"); k != "" {
		filter.Kinds = []string{k}
	}
	if t := r.URL.Query().Get("thought_id"); t != "" {
		filter.ThoughtID = t
	}
	if c := r.URL.Query().Get("conv_id"); c != "" {
		filter.ConvID = c
	}
	if s := r.URL.Query().Get("since"); s != "" {
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			filter.Since = ts
		} else if dur, err := time.ParseDuration(s); err == nil {
			filter.Since = time.Now().UTC().Add(-dur)
		}
	} else {
		filter.Since = time.Now().UTC().Add(-24 * time.Hour)
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			filter.Limit = n
		}
	}

	rows, err := m.db.QueryMindTelemetry(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query telemetry: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  rows,
		"count": len(rows),
	})
}

// telemetrySummary handles GET /mind/telemetry/summary — counts by kind
// since the given timestamp. Feeds the Mind Health dashboard widgets.
func (m *Module) telemetrySummary(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	if s := r.URL.Query().Get("since"); s != "" {
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			since = ts
		} else if dur, err := time.ParseDuration(s); err == nil {
			since = time.Now().UTC().Add(-dur)
		}
	}
	stats, err := telemetry.Aggregate(m.db, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "aggregate: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
