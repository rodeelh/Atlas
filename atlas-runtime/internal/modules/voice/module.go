// Package voice is the first-party voice feature module — routes and lifecycle
// for the Whisper STT and Kokoro TTS subsystems.
package voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	runtimevoice "atlas-runtime-go/internal/voice"
)

type Module struct {
	mgr      *runtimevoice.Manager
	cfgStore *config.Store
}

const (
	// Keep speech natural but a bit snappier than default real-time.
	ttsDefaultSpeed = 1.08
	ttsDefaultLang  = "en-us"
)

func New(mgr *runtimevoice.Manager, cfgStore *config.Store) *Module {
	return &Module{mgr: mgr, cfgStore: cfgStore}
}

func (m *Module) ID() string { return "voice" }

func (m *Module) Manifest() platform.Manifest { return platform.Manifest{Version: "v1"} }

func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

// Stop ends any active voice session during daemon shutdown so no whisper/
// kokoro subprocesses leak across daemon restarts.
func (m *Module) Stop(context.Context) error {
	m.mgr.Close()
	return nil
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/voice/status", m.getStatus)
	r.Post("/voice/session/start", m.postSessionStart)
	r.Post("/voice/session/end", m.postSessionEnd)
	r.Post("/voice/transcribe", m.postTranscribe)
	r.Post("/voice/synthesize", m.postSynthesize)
	r.Post("/voice/kokoro/warmup", m.postKokoroWarmup)
	r.Get("/voice/models/{component}", m.getModels)
	r.Post("/voice/models/{component}/download", m.postModelDownload)
	r.Delete("/voice/models/{component}/{name}", m.deleteModel)
	r.Get("/voice/models/download/status", m.getDownloadStatus)
	r.Delete("/voice/models/download", m.deleteDownloadStatus)
}

func (m *Module) getStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.mgr.Status())
}

func (m *Module) postSessionStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WhisperModel string `json:"whisperModel"`
		WhisperPort  int    `json:"whisperPort"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	cfg := m.cfgStore.Load()
	model := req.WhisperModel
	if model == "" {
		model = cfg.VoiceWhisperModel
	}
	if model == "" {
		model = "ggml-base.en.bin"
	}
	port := req.WhisperPort
	if port == 0 {
		port = cfg.VoiceWhisperPort
	}
	if port == 0 {
		port = 11987
	}
	id, err := m.mgr.StartSession(model, port)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.mgr.WaitWhisperReady(15 * time.Second); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionID": id,
		"status":    m.mgr.Status(),
	})
}

func (m *Module) postSessionEnd(w http.ResponseWriter, _ *http.Request) {
	if err := m.mgr.EndSession(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.mgr.Status())
}

func (m *Module) postTranscribe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio field missing")
		return
	}
	defer file.Close()

	audio, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read audio: "+err.Error())
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "audio/webm"
	}

	language := r.URL.Query().Get("language")
	cfg := m.cfgStore.Load()
	if language == "" {
		language = cfg.VoiceWhisperLanguage
	}
	defaultModel := cfg.VoiceWhisperModel
	if defaultModel == "" {
		defaultModel = "ggml-base.en.bin"
	}
	defaultPort := cfg.VoiceWhisperPort
	if defaultPort == 0 {
		defaultPort = 11987
	}

	result, err := m.mgr.Transcribe(r.Context(), audio, mimeType, language, defaultModel, defaultPort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// postSynthesize runs Kokoro TTS on the request body text and streams raw PCM
// chunks to the client via SSE. Voice/speed/lang are fixed at the runtime
// level (am_onyx / 1.08 / en-us); the request body only carries text.
func (m *Module) postSynthesize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	cfg := m.cfgStore.Load()
	port := cfg.VoiceKokoroPort
	if port == 0 {
		port = 11989
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, hasFlusher := w.(http.Flusher)
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if hasFlusher {
			flusher.Flush()
		}
	}
	emit("start", map[string]any{"voice": runtimevoice.KokoroVoiceDefault})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	err := m.mgr.SynthesizeKokoroStream(ctx, req.Text, runtimevoice.KokoroVoiceDefault, ttsDefaultSpeed, ttsDefaultLang, port, func(c runtimevoice.SynthesizeChunk) error {
		emit("voice_audio", map[string]any{
			"index":      c.Index,
			"text":       c.Text,
			"final":      c.Final,
			"sampleRate": c.SampleRate,
			"pcm":        base64.StdEncoding.EncodeToString(c.PCM),
		})
		return nil
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}
	emit("voice_audio_end", map[string]any{"ok": true})
}

// postKokoroWarmup spins up the Kokoro subprocess immediately so the first
// real synthesize call doesn't pay the ~600 ms model-load cost. Idempotent —
// if Kokoro is already running this is a no-op.
func (m *Module) postKokoroWarmup(w http.ResponseWriter, r *http.Request) {
	cfg := m.cfgStore.Load()
	port := cfg.VoiceKokoroPort
	if port == 0 {
		port = 11989
	}
	if err := m.mgr.StartKokoro(port); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.mgr.WaitKokoroReady(15 * time.Second); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "port": port})
}

func (m *Module) getModels(w http.ResponseWriter, r *http.Request) {
	component := chi.URLParam(r, "component")
	models, err := m.mgr.ListModels(component)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) deleteModel(w http.ResponseWriter, r *http.Request) {
	component := chi.URLParam(r, "component")
	name := chi.URLParam(r, "name")
	if err := m.mgr.DeleteModel(component, name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	models, _ := m.mgr.ListModels(component)
	writeJSON(w, http.StatusOK, models)
}

func (m *Module) getDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.mgr.ActiveDownload())
}

func (m *Module) deleteDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	m.mgr.ClearDownload()
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) postModelDownload(w http.ResponseWriter, r *http.Request) {
	component := chi.URLParam(r, "component")
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
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if hasFlusher {
			flusher.Flush()
		}
	}
	emit("start", map[string]any{"filename": req.Filename, "component": component})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	err := m.mgr.DownloadModel(ctx, req.URL, req.Filename, component, func(downloaded, total int64) {
		pct := 0.0
		if total > 0 {
			pct = float64(downloaded) / float64(total) * 100
		}
		emit("progress", map[string]any{
			"downloaded": downloaded,
			"total":      total,
			"percent":    pct,
		})
	})
	if err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}
	models, _ := m.mgr.ListModels(component)
	emit("done", map[string]any{"filename": req.Filename, "models": models})
}
