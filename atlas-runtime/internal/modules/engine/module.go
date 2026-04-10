package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	runtimeengine "atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/platform"
)

type Module struct {
	mgr          *runtimeengine.Manager
	routerMgr    *runtimeengine.Manager
	mlxMgr       *runtimeengine.MLXManager
	mlxRouterMgr *runtimeengine.MLXManager
	cfgStore     *config.Store
}

func New(mgr *runtimeengine.Manager, routerMgr *runtimeengine.Manager, cfgStore *config.Store) *Module {
	return &Module{mgr: mgr, routerMgr: routerMgr, cfgStore: cfgStore}
}

// WithMLX wires in the MLX-LM primary and router managers.
// Call before registering the module when running on Apple Silicon.
func (m *Module) WithMLX(mlxMgr, mlxRouterMgr *runtimeengine.MLXManager) *Module {
	m.mlxMgr = mlxMgr
	m.mlxRouterMgr = mlxRouterMgr
	return m
}

func (m *Module) ID() string { return "engine" }

func (m *Module) Manifest() platform.Manifest { return platform.Manifest{Version: "v1"} }

func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

func (m *Module) registerRoutes(r chi.Router) {
	// llama.cpp (Engine LM) routes.
	r.Get("/engine/status", m.getStatus)
	r.Get("/engine/models", m.getModels)
	r.Post("/engine/start", m.postStart)
	r.Post("/engine/stop", m.postStop)
	r.Post("/engine/models/download", m.postDownload)
	r.Get("/engine/models/download/status", m.getDownloadStatus)
	r.Delete("/engine/models/download", m.deleteDownloadStatus)
	r.Post("/engine/update", m.postUpdate)
	r.Delete("/engine/models/{name}", m.deleteModel)
	r.Get("/engine/router/status", m.getRouterStatus)
	r.Post("/engine/router/start", m.postRouterStart)
	r.Post("/engine/router/stop", m.postRouterStop)

	// MLX-LM routes — only mounted when an MLXManager is wired in.
	// The /engine/mlx/* namespace mirrors /engine/* exactly.
	if m.mlxMgr != nil {
		r.Get("/engine/mlx/status", m.getMLXStatus)
		r.Get("/engine/mlx/models", m.getMLXModels)
		r.Post("/engine/mlx/start", m.postMLXStart)
		r.Post("/engine/mlx/stop", m.postMLXStop)
		r.Post("/engine/mlx/models/download", m.postMLXDownload)
		r.Get("/engine/mlx/models/download/status", m.getMLXDownloadStatus)
		r.Delete("/engine/mlx/models/download", m.deleteMLXDownloadStatus)
		r.Post("/engine/mlx/install", m.postMLXInstall) // install/upgrade mlx-lm package (mirrors /engine/update)
		r.Delete("/engine/mlx/models/{name}", m.deleteMLXModel)
		r.Get("/engine/mlx/router/status", m.getMLXRouterStatus)
		r.Post("/engine/mlx/router/start", m.postMLXRouterStart)
		r.Post("/engine/mlx/router/stop", m.postMLXRouterStop)
	}
}

func (m *Module) getStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := m.cfgStore.Load()
	s := m.mgr.Status(cfg.AtlasEnginePort)
	if s.Running {
		if m.mgr.IsLoading(s.Port) {
			s.Loading = true
		} else {
			snap := m.mgr.FetchMetrics(s.Port)
			s.LastTPS = snap.DecodeTPS
			s.PromptTPS = snap.PromptTPS
			s.GenTimeSec = snap.GenTimeSec
			s.ActiveRequests = snap.ActiveRequests
			s.ContextTokens = m.mgr.FetchContextTokens(s.Port)
		}
	}
	writeJSON(w, http.StatusOK, s)
}

func (m *Module) getModels(w http.ResponseWriter, _ *http.Request) {
	models, err := m.mgr.ListModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []runtimeengine.ModelInfo{}
	}
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) postStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model   string `json:"model"`
		Port    int    `json:"port"`
		CtxSize int    `json:"ctxSize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	// Mutual exclusion: stop MLX engine (and its router) before starting llama.cpp.
	if m.mlxMgr != nil && m.mlxMgr.IsRunning() {
		_ = m.mlxMgr.Stop()
	}
	if m.mlxRouterMgr != nil && m.mlxRouterMgr.IsRunning() {
		_ = m.mlxRouterMgr.Stop()
	}
	cfg := m.cfgStore.Load()
	port := req.Port
	if port == 0 {
		port = cfg.AtlasEnginePort
	}
	if port == 0 {
		port = 11985
	}
	ctxSize := req.CtxSize
	if ctxSize <= 0 {
		ctxSize = cfg.AtlasEngineCtxSize
	}
	if err := m.mgr.Start(req.Model, port, ctxSize, cfg.AtlasEngineKVCacheQuant, cfg.AtlasEngineDraftModel); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.mgr.Status(port))
}

func (m *Module) postStop(w http.ResponseWriter, _ *http.Request) {
	if err := m.mgr.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg := m.cfgStore.Load()
	writeJSON(w, http.StatusOK, m.mgr.Status(cfg.AtlasEnginePort))
}

func (m *Module) getDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.mgr.ActiveDownload())
}

func (m *Module) deleteDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	m.mgr.ClearDownload()
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) postDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL == "" || req.Filename == "" {
		writeError(w, http.StatusBadRequest, "url and filename are required")
		return
	}
	if _, err := url.ParseRequestURI(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, hasFlusher := w.(http.Flusher)
	emit := func(eventType string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		if hasFlusher {
			flusher.Flush()
		}
	}
	emit("start", map[string]any{"filename": req.Filename})
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	err := m.mgr.DownloadModel(ctx, req.URL, req.Filename, func(downloaded, total int64) {
		pct := 0.0
		if total > 0 {
			pct = float64(downloaded) / float64(total) * 100
		}
		emit("progress", map[string]any{"downloaded": downloaded, "total": total, "percent": pct})
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}
	models, _ := m.mgr.ListModels()
	if models == nil {
		models = []runtimeengine.ModelInfo{}
	}
	emit("done", map[string]any{"filename": req.Filename, "models": models})
}

func (m *Module) postUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Version == "" {
		req.Version = "b8641"
	}
	prevModel := m.mgr.LoadedModel()
	cfg := m.cfgStore.Load()
	prevPort := cfg.AtlasEnginePort
	if prevPort == 0 {
		prevPort = 11985
	}
	_ = m.mgr.Stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, hasFlusher := w.(http.Flusher)
	emit := func(eventType string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		if hasFlusher {
			flusher.Flush()
		}
	}
	emit("start", map[string]any{"version": req.Version})
	err := m.mgr.DownloadBinary(req.Version, func(downloaded, total int64) {
		pct := 0.0
		if total > 0 {
			pct = float64(downloaded) / float64(total) * 100
		}
		emit("progress", map[string]any{"downloaded": downloaded, "total": total, "percent": pct})
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}
	if prevModel != "" {
		ctxSize := cfg.AtlasEngineCtxSize
		if ctxSize <= 0 {
			ctxSize = 8192
		}
		_ = m.mgr.Start(prevModel, prevPort, ctxSize, cfg.AtlasEngineKVCacheQuant, cfg.AtlasEngineDraftModel)
	}
	emit("done", map[string]any{"version": req.Version, "restarted": prevModel != "", "status": m.mgr.Status(prevPort)})
}

func (m *Module) deleteModel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	if err := m.mgr.DeleteModel(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := m.mgr.ListModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []runtimeengine.ModelInfo{}
	}
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) getRouterStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := m.cfgStore.Load()
	port := cfg.AtlasEngineRouterPort
	if port == 0 {
		port = 11986
	}
	writeJSON(w, http.StatusOK, m.routerMgr.Status(port))
}

func (m *Module) postRouterStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := m.cfgStore.Load()
	model := req.Model
	if model == "" {
		model = cfg.AtlasEngineRouterModel
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	port := cfg.AtlasEngineRouterPort
	if port == 0 {
		port = 11986
	}
	routerCtxSize := cfg.AtlasEngineCtxSize
	if routerCtxSize < 4096 {
		routerCtxSize = 4096
	}
	if err := m.routerMgr.Start(model, port, routerCtxSize, cfg.AtlasEngineKVCacheQuant, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.routerMgr.Status(port))
}

func (m *Module) postRouterStop(w http.ResponseWriter, _ *http.Request) {
	cfg := m.cfgStore.Load()
	port := cfg.AtlasEngineRouterPort
	if port == 0 {
		port = 11986
	}
	if err := m.routerMgr.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.routerMgr.Status(port))
}

// ── MLX-LM route handlers ─────────────────────────────────────────────────────
// These mirror the llama.cpp handlers above, adapted for MLXManager.

func (m *Module) getMLXStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := m.cfgStore.Load()
	s := m.mlxMgr.Status(cfg.AtlasMLXPort)
	if s.Running && m.mlxMgr.IsLoading(s.Port) {
		s.Loading = true
	}
	writeJSON(w, http.StatusOK, s)
}

func (m *Module) getMLXModels(w http.ResponseWriter, _ *http.Request) {
	models, err := m.mlxMgr.ListModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []runtimeengine.MLXModelInfo{}
	}
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) postMLXStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model   string `json:"model"`
		Port    int    `json:"port"`
		CtxSize int    `json:"ctxSize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	// Mutual exclusion: stop llama.cpp engine (and its router) before starting MLX-LM.
	if m.mgr.IsRunning() {
		_ = m.mgr.Stop()
	}
	if m.routerMgr.IsRunning() {
		_ = m.routerMgr.Stop()
	}
	cfg := m.cfgStore.Load()
	port := req.Port
	if port == 0 {
		port = cfg.AtlasMLXPort
	}
	if port == 0 {
		port = 11990
	}
	ctxSize := req.CtxSize
	if ctxSize <= 0 {
		ctxSize = cfg.AtlasMLXCtxSize
	}
	if err := m.mlxMgr.Start(req.Model, port, ctxSize, "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.mlxMgr.Status(port))
}

func (m *Module) postMLXStop(w http.ResponseWriter, _ *http.Request) {
	if err := m.mlxMgr.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg := m.cfgStore.Load()
	writeJSON(w, http.StatusOK, m.mlxMgr.Status(cfg.AtlasMLXPort))
}

func (m *Module) getMLXDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.mlxMgr.ActiveDownload())
}

func (m *Module) deleteMLXDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	m.mlxMgr.ClearDownload()
	w.WriteHeader(http.StatusNoContent)
}

// postMLXDownload downloads an MLX model from HuggingFace.
// Request body: {"repo": "mlx-community/Llama-3.2-3B-Instruct-4bit"}
// The "repo" field is a HuggingFace repository ID, not a direct URL.
// Progress is streamed as SSE events with line-by-line output from mlx_lm.
func (m *Module) postMLXDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required (e.g. \"mlx-community/Llama-3.2-3B-Instruct-4bit\")")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, hasFlusher := w.(http.Flusher)
	emit := func(eventType string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		if hasFlusher {
			flusher.Flush()
		}
	}

	emit("start", map[string]any{"repo": req.Repo})
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	err := m.mlxMgr.DownloadModelFromSnapshot(ctx, req.Repo, func(line string) {
		emit("progress", map[string]any{"line": line})
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}

	models, _ := m.mlxMgr.ListModels()
	if models == nil {
		models = []runtimeengine.MLXModelInfo{}
	}
	emit("done", map[string]any{"repo": req.Repo, "models": models})
}

// postMLXInstall installs or upgrades the mlx-lm Python package.
// This is the MLX equivalent of /engine/update (which replaces the llama-server binary).
// It runs `pip install --upgrade mlx-lm` from the official repo:
// https://github.com/ml-explore/mlx-lm (distributed via PyPI as "mlx-lm").
// Progress is streamed as SSE events. After upgrade, the previously running model
// is automatically restarted if one was loaded.
func (m *Module) postMLXInstall(w http.ResponseWriter, r *http.Request) {
	prevModel := m.mlxMgr.LoadedModel()
	cfg := m.cfgStore.Load()
	prevPort := cfg.AtlasMLXPort
	if prevPort == 0 {
		prevPort = 11990
	}
	_ = m.mlxMgr.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, hasFlusher := w.(http.Flusher)
	emit := func(eventType string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
		if hasFlusher {
			flusher.Flush()
		}
	}

	emit("start", map[string]any{"package": "mlx-lm", "source": "https://github.com/ml-explore/mlx-lm"})

	err := m.mlxMgr.InstallOrUpgrade(func(line string) {
		emit("progress", map[string]any{"line": line})
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}

	// Auto-restart the previously loaded model after the upgrade.
	if prevModel != "" {
		ctxSize := cfg.AtlasMLXCtxSize
		if ctxSize <= 0 {
			ctxSize = 4096
		}
		_ = m.mlxMgr.Start(prevModel, prevPort, ctxSize, "", "")
	}

	emit("done", map[string]any{
		"version":   m.mlxMgr.PackageVersion(),
		"restarted": prevModel != "",
		"status":    m.mlxMgr.Status(prevPort),
	})
}

func (m *Module) deleteMLXModel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	if err := m.mlxMgr.DeleteModel(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := m.mlxMgr.ListModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []runtimeengine.MLXModelInfo{}
	}
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) getMLXRouterStatus(w http.ResponseWriter, _ *http.Request) {
	if m.mlxRouterMgr == nil {
		writeJSON(w, http.StatusOK, runtimeengine.MLXStatus{IsAppleSilicon: runtimeengine.IsAppleSilicon()})
		return
	}
	cfg := m.cfgStore.Load()
	port := cfg.AtlasMLXRouterPort
	if port == 0 {
		port = 11991
	}
	writeJSON(w, http.StatusOK, m.mlxRouterMgr.Status(port))
}

func (m *Module) postMLXRouterStart(w http.ResponseWriter, r *http.Request) {
	if m.mlxRouterMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "MLX router manager not configured")
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := m.cfgStore.Load()
	model := req.Model
	if model == "" {
		model = cfg.AtlasMLXRouterModel
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	port := cfg.AtlasMLXRouterPort
	if port == 0 {
		port = 11991
	}
	ctxSize := cfg.AtlasMLXCtxSize
	if ctxSize < 2048 {
		ctxSize = 2048
	}
	if err := m.mlxRouterMgr.Start(model, port, ctxSize, "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.mlxRouterMgr.Status(port))
}

func (m *Module) postMLXRouterStop(w http.ResponseWriter, _ *http.Request) {
	if m.mlxRouterMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "MLX router manager not configured")
		return
	}
	cfg := m.cfgStore.Load()
	port := cfg.AtlasMLXRouterPort
	if port == 0 {
		port = 11991
	}
	if err := m.mlxRouterMgr.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.mlxRouterMgr.Status(port))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
