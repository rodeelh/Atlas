package apivalidation

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
)

type Module struct {
	supportDir string
}

func New(supportDir string) *Module { return &Module{supportDir: supportDir} }

func (m *Module) ID() string { return "api-validation" }

func (m *Module) Manifest() platform.Manifest { return platform.Manifest{Version: "v1"} }

func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/api-validation/history", m.history)
}

func (m *Module) history(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, features.ListAPIValidationHistory(m.supportDir, limit))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
