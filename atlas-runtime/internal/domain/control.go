package domain

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/auth"
	corecontrol "atlas-runtime-go/internal/control"
	"atlas-runtime-go/internal/engine"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/storage"
)

// ControlDomain handles runtime control and configuration routes.
type ControlDomain struct {
	system      *corecontrol.SystemService
	models      *corecontrol.ModelsService
	credentials *corecontrol.CredentialsService
	profile     *corecontrol.ProfileService
}

// NewControlDomain creates a ControlDomain.
func NewControlDomain(cfgStore *config.Store, runtimeSvc *runtime.Service, db *storage.DB, mgr *engine.Manager) *ControlDomain {
	return &ControlDomain{
		system:      corecontrol.NewSystemService(cfgStore, runtimeSvc, db),
		models:      corecontrol.NewModelsService(cfgStore, mgr),
		credentials: corecontrol.NewCredentialsService(),
		profile:     corecontrol.NewProfileService(),
	}
}

// SetMLXManager wires the MLX engine manager into the models service so that
// GET /models/available?provider=atlas_mlx returns the downloaded model list.
func (d *ControlDomain) SetMLXManager(mlx *engine.MLXManager) {
	d.models.SetMLXManager(mlx)
}

func (d *ControlDomain) Register(r chi.Router) {
	r.Get("/status", d.getStatus)
	r.Get("/logs", d.getLogs)
	r.Get("/config", d.getConfig)
	r.Put("/config", d.putConfig)
	r.Post("/control/restart", d.postRestart)
	r.Get("/onboarding", d.getOnboarding)
	r.Put("/onboarding", d.putOnboarding)
	r.Get("/models", d.getModels)
	r.Get("/models/available", d.getModelsAvailable)
	r.Post("/models/refresh", d.postModelsRefresh)
	r.Get("/api-keys", d.getAPIKeys)
	r.Post("/api-keys", d.postAPIKeys)
	r.Post("/api-keys/invalidate-cache", d.postAPIKeysInvalidateCache)
	r.Delete("/api-keys", d.deleteAPIKeys)
	r.Get("/link-preview", d.getLinkPreview)
	r.Get("/location", d.getLocation)
	r.Put("/location", d.putLocation)
	r.Post("/location/detect", d.postLocationDetect)
	r.Get("/preferences", d.getPreferences)
	r.Put("/preferences", d.putPreferences)
	r.Get("/providers/openrouter/models", d.getOpenRouterModels)
	r.Get("/providers/openrouter/model-health", d.getOpenRouterModelHealth)
	r.Get("/providers/cloud/model-health", d.getCloudModelHealth)
	r.Get("/storage/stats", d.getStorageStats)
	r.Delete("/storage/files", d.deleteStorageFiles)
}

func (d *ControlDomain) getStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.system.Status())
}

func (d *ControlDomain) getLogs(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, d.system.Logs(limit))
}

func (d *ControlDomain) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.system.Config())
}

func (d *ControlDomain) putConfig(w http.ResponseWriter, r *http.Request) {
	next := d.system.Config()
	if !decodeJSON(w, r, &next) {
		return
	}
	updated, restartRequired, err := d.system.UpdateConfig(next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":          updated,
		"restartRequired": restartRequired,
	})
}

func (d *ControlDomain) postRestart(w http.ResponseWriter, r *http.Request) {
	if !auth.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "Restart is only available from the local Atlas session.")
		return
	}
	if err := d.system.ScheduleRestart(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"message":  "Atlas is restarting.",
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (d *ControlDomain) getOnboarding(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"completed": d.system.OnboardingCompleted()})
}

func (d *ControlDomain) putOnboarding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Completed bool `json:"completed"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := d.system.UpdateOnboarding(req.Completed); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": req.Completed})
}

func (d *ControlDomain) getModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.models.Selected())
}

func (d *ControlDomain) getModelsAvailable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.models.Available(r.URL.Query().Get("provider")))
}

func (d *ControlDomain) postModelsRefresh(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.models.RefreshActive())
}

func (d *ControlDomain) getAPIKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.credentials.Status())
}

func (d *ControlDomain) postAPIKeys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
		Name     string `json:"name"`
		Label    string `json:"label"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "Missing 'provider' field.")
		return
	}
	if err := d.credentials.Store(req.Provider, req.Key, req.Name, req.Label); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to store key: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d.credentials.Status())
}

func (d *ControlDomain) postAPIKeysInvalidateCache(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"invalidated": true})
}

func (d *ControlDomain) deleteAPIKeys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	decodeJSONLenient(r, &req)
	if req.Name != "" {
		if err := d.credentials.Delete(req.Name); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to delete key: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, d.credentials.Status())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *ControlDomain) getLinkPreview(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "Missing 'url' query parameter.")
		return
	}
	result, err := d.profile.FetchLinkPreview(rawURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to fetch URL: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d *ControlDomain) getLocation(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.profile.GetLocation())
}

func (d *ControlDomain) putLocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.City) == "" {
		http.Error(w, "city is required", http.StatusBadRequest)
		return
	}
	loc, err := d.profile.SetLocation(body.City, body.Country)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, loc)
}

func (d *ControlDomain) postLocationDetect(w http.ResponseWriter, _ *http.Request) {
	loc, err := d.profile.DetectLocation()
	if err != nil {
		http.Error(w, "Location detection failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, loc)
}

func (d *ControlDomain) getPreferences(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.profile.GetPreferences())
}

func (d *ControlDomain) putPreferences(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TemperatureUnit string `json:"temperatureUnit"`
		Currency        string `json:"currency"`
		UnitSystem      string `json:"unitSystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, d.profile.UpdatePreferences(body.TemperatureUnit, body.Currency, body.UnitSystem))
}

func (d *ControlDomain) getOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 250 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, d.models.OpenRouterModels(refresh, limit))
}

func (d *ControlDomain) getOpenRouterModelHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.models.OpenRouterModelHealth(r.URL.Query().Get("model")))
}

func (d *ControlDomain) getCloudModelHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.models.CloudModelHealth(
		r.URL.Query().Get("provider"),
		r.URL.Query().Get("model"),
	))
}

// StorageStats is returned by GET /storage/stats.
type StorageStats struct {
	Dir       string `json:"dir"`
	FileCount int    `json:"fileCount"`
	TotalSize int64  `json:"totalSize"` // bytes
}

func (d *ControlDomain) getStorageStats(w http.ResponseWriter, _ *http.Request) {
	dir := config.FilesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, StorageStats{Dir: dir})
		return
	}
	var count int
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		count++
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	writeJSON(w, http.StatusOK, StorageStats{Dir: dir, FileCount: count, TotalSize: total})
}

func (d *ControlDomain) deleteStorageFiles(w http.ResponseWriter, _ *http.Request) {
	dir := config.FilesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	w.WriteHeader(http.StatusNoContent)
}
