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
	r.Get("/voice/voices", m.getVoices)
	r.Get("/voice/provider-models", m.getProviderModels)
	r.Post("/voice/session/start", m.postSessionStart)
	r.Post("/voice/session/end", m.postSessionEnd)
	r.Post("/voice/transcribe", m.postTranscribe)
	r.Post("/voice/synthesize", m.postSynthesize)
	r.Post("/voice/kokoro/warmup", m.postKokoroWarmup)
	r.Post("/voice/whisper/update", m.postWhisperUpdate)
	r.Post("/voice/kokoro/update", m.postKokoroUpdate)
	r.Get("/voice/models/{component}", m.getModels)
	r.Post("/voice/models/{component}/download", m.postModelDownload)
	r.Delete("/voice/models/{component}/{name}", m.deleteModel)
	r.Get("/voice/models/download/status", m.getDownloadStatus)
	r.Delete("/voice/models/download", m.deleteDownloadStatus)
}

func (m *Module) getProviderModels(w http.ResponseWriter, r *http.Request) {
	cfg := m.cfgStore.Load()
	provider := runtimevoice.ProviderType(r.URL.Query().Get("provider"))
	if provider == "" {
		provider = runtimevoice.ProviderType(cfg.ActiveAudioProvider)
	}
	if provider == "" {
		provider = runtimevoice.ProviderLocal
	}
	models := runtimevoice.ProviderModelSet{}
	switch provider {
	case runtimevoice.ProviderOpenAI, runtimevoice.ProviderGemini, runtimevoice.ProviderElevenLabs:
		models = runtimevoice.DiscoverProviderModels(provider, cfg)
	default:
		models = runtimevoice.ProviderModelSet{}
	}
	writeJSON(w, http.StatusOK, models)
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
	if language == "" {
		language = m.cfgStore.Load().AudioSTTLanguage
	}

	result, err := m.mgr.Transcribe(r.Context(), audio, mimeType, language)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// postSynthesize routes TTS through the active audio provider and streams raw
// PCM chunks to the client via SSE. The request body carries text and an optional
// voice override; provider/model/speed are resolved from config.
func (m *Module) postSynthesize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string `json:"text"`
		Voice string `json:"voice"` // optional; falls back to configured default
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
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
	emit("start", map[string]any{"voice": req.Voice})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	err := m.mgr.Synthesize(ctx, req.Text, req.Voice, func(c runtimevoice.SynthesizeChunk) error {
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

// getVoices returns the curated voice list for the active (or requested) provider.
// Query param: ?provider=local|openai|gemini  (defaults to active provider from config)
func (m *Module) getVoices(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		cfg := m.cfgStore.Load()
		provider = cfg.ActiveAudioProvider
	}
	if provider == "" {
		provider = "local"
	}

	switch runtimevoice.ProviderType(provider) {
	case runtimevoice.ProviderOpenAI:
		writeJSON(w, http.StatusOK, openAIVoices())
	case runtimevoice.ProviderGemini:
		writeJSON(w, http.StatusOK, geminiVoices())
	case runtimevoice.ProviderElevenLabs:
		writeJSON(w, http.StatusOK, elevenLabsVoices())
	default:
		voices, err := m.mgr.KokoroVoices(r.Context(), 11989)
		if err != nil {
			// Kokoro not installed or not running — return curated static list.
			writeJSON(w, http.StatusOK, localVoicesFallback())
			return
		}
		writeJSON(w, http.StatusOK, buildLocalVoices(voices))
	}
}

// ── Curated voice lists ───────────────────────────────────────────────────────

// featured top-5 for OpenAI; remaining 6 shown under "Show more".
func openAIVoices() []runtimevoice.VoiceOption {
	return []runtimevoice.VoiceOption{
		{ID: "alloy", Label: "Alloy", Description: "Neutral · Balanced", Featured: true},
		{ID: "nova", Label: "Nova", Description: "Bright · Natural", Featured: true},
		{ID: "onyx", Label: "Onyx", Description: "Deep · Authoritative", Featured: true},
		{ID: "shimmer", Label: "Shimmer", Description: "Gentle · Warm", Featured: true},
		{ID: "ash", Label: "Ash", Description: "Conversational · Warm", Featured: true},
		// show-more tier
		{ID: "echo", Label: "Echo", Description: "Steady · Calm"},
		{ID: "fable", Label: "Fable", Description: "Narrative · Expressive"},
		{ID: "coral", Label: "Coral", Description: "Clear · Expressive"},
		{ID: "sage", Label: "Sage", Description: "Measured · Thoughtful"},
		// model-gated: only available with gpt-4o-mini-tts
		{ID: "marin", Label: "Marin", Description: "Modern · Clear", ModelGate: "gpt-4o-mini-tts"},
		{ID: "cedar", Label: "Cedar", Description: "Natural · Warm", ModelGate: "gpt-4o-mini-tts"},
	}
}

// featured top-5 for Gemini; remaining 25 shown under "Show more".
func geminiVoices() []runtimevoice.VoiceOption {
	featured := []runtimevoice.VoiceOption{
		{ID: "Aoede", Label: "Aoede", Description: "Breezy", Featured: true},
		{ID: "Puck", Label: "Puck", Description: "Upbeat", Featured: true},
		{ID: "Kore", Label: "Kore", Description: "Firm", Featured: true},
		{ID: "Charon", Label: "Charon", Description: "Informative", Featured: true},
		{ID: "Fenrir", Label: "Fenrir", Description: "Excitable", Featured: true},
	}
	rest := []runtimevoice.VoiceOption{
		{ID: "Zephyr", Label: "Zephyr", Description: "Bright"},
		{ID: "Leda", Label: "Leda", Description: "Youthful"},
		{ID: "Orus", Label: "Orus", Description: "Firm"},
		{ID: "Callirrhoe", Label: "Callirrhoe", Description: "Easy-going"},
		{ID: "Autonoe", Label: "Autonoe", Description: "Bright"},
		{ID: "Enceladus", Label: "Enceladus", Description: "Breathy"},
		{ID: "Iapetus", Label: "Iapetus", Description: "Clear"},
		{ID: "Umbriel", Label: "Umbriel", Description: "Easy-going"},
		{ID: "Algieba", Label: "Algieba", Description: "Smooth"},
		{ID: "Despina", Label: "Despina", Description: "Smooth"},
		{ID: "Erinome", Label: "Erinome", Description: "Clear"},
		{ID: "Algenib", Label: "Algenib", Description: "Gravelly"},
		{ID: "Rasalgethi", Label: "Rasalgethi", Description: "Informative"},
		{ID: "Laomedeia", Label: "Laomedeia", Description: "Upbeat"},
		{ID: "Achernar", Label: "Achernar", Description: "Soft"},
		{ID: "Alnilam", Label: "Alnilam", Description: "Firm"},
		{ID: "Schedar", Label: "Schedar", Description: "Even"},
		{ID: "Gacrux", Label: "Gacrux", Description: "Mature"},
		{ID: "Pulcherrima", Label: "Pulcherrima", Description: "Forward"},
		{ID: "Achird", Label: "Achird", Description: "Friendly"},
		{ID: "Zubenelgenubi", Label: "Zubenelgenubi", Description: "Casual"},
		{ID: "Vindemiatrix", Label: "Vindemiatrix", Description: "Gentle"},
		{ID: "Sadachbia", Label: "Sadachbia", Description: "Lively"},
		{ID: "Sadaltager", Label: "Sadaltager", Description: "Knowledgeable"},
		{ID: "Sulafat", Label: "Sulafat", Description: "Warm"},
	}
	return append(featured, rest...)
}

// featured top-5 for ElevenLabs; remaining voices shown under "Show more".
func elevenLabsVoices() []runtimevoice.VoiceOption {
	return []runtimevoice.VoiceOption{
		{ID: "21m00Tcm4TlvDq8ikWAM", Label: "Rachel", Description: "Calm · American", Featured: true},
		{ID: "AZnzlk1XvdvUeBnXmlld", Label: "Domi", Description: "Strong · American", Featured: true},
		{ID: "EXAVITQu4vr4xnSDxMaL", Label: "Bella", Description: "Soft · American", Featured: true},
		{ID: "ErXwobaYiN019PkySvjV", Label: "Antoni", Description: "Well-rounded · American", Featured: true},
		{ID: "MF3mGyEYCl7XYWbV9V6O", Label: "Elli", Description: "Emotional · American", Featured: true},
		// show-more tier
		{ID: "TxGEqnHWrfWFTfGW9XjX", Label: "Josh", Description: "Deep · American"},
		{ID: "VR6AewLTigWG4xSOukaG", Label: "Arnold", Description: "Crisp · American"},
		{ID: "pNInz6obpgDQGcFmaJgB", Label: "Adam", Description: "Deep · American"},
		{ID: "yoZ06aMxZJJ28mfd3POQ", Label: "Sam", Description: "Raspy · American"},
		{ID: "onwK4e9ZLuTAKqWW03F9", Label: "Daniel", Description: "Deep · British"},
		{ID: "g5CIjZEefAph4nQFvHAz", Label: "Ethan", Description: "Soft · American"},
	}
}

// buildLocalVoices takes the raw voice IDs from Kokoro's /health endpoint and
// returns the 3 curated options first (all featured), with remaining voices
// appended non-featured for completeness.
func buildLocalVoices(all []string) []runtimevoice.VoiceOption {
	curated := []runtimevoice.VoiceOption{
		{ID: "af_heart", Label: "Heart", Description: "American · Female", Featured: true},
		{ID: "am_onyx", Label: "Onyx", Description: "American · Male", Featured: true},
		{ID: "bf_emma", Label: "Emma", Description: "British · Female", Featured: true},
	}
	curatedIDs := map[string]bool{"af_heart": true, "am_onyx": true, "bf_emma": true}

	for _, id := range all {
		if curatedIDs[id] {
			continue
		}
		curated = append(curated, runtimevoice.VoiceOption{
			ID:          id,
			Label:       kokoroLabel(id),
			Description: kokoroDescription(id),
		})
	}
	return curated
}

// localVoicesFallback returns the static curated list when Kokoro is offline.
func localVoicesFallback() []runtimevoice.VoiceOption {
	return []runtimevoice.VoiceOption{
		{ID: "af_heart", Label: "Heart", Description: "American · Female", Featured: true},
		{ID: "am_onyx", Label: "Onyx", Description: "American · Male", Featured: true},
		{ID: "bf_emma", Label: "Emma", Description: "British · Female", Featured: true},
	}
}

// kokoroLabel derives a display name from a Kokoro voice ID (e.g. "am_onyx" → "Onyx").
func kokoroLabel(id string) string {
	parts := splitKokoroID(id)
	if parts[1] != "" {
		// Capitalise first letter.
		n := parts[1]
		if len(n) > 0 {
			return string(n[0]-32) + n[1:]
		}
	}
	return id
}

// kokoroDescription decodes accent + gender from the ID prefix.
func kokoroDescription(id string) string {
	if len(id) < 2 {
		return ""
	}
	accent := map[byte]string{'a': "American", 'b': "British"}[id[0]]
	gender := map[byte]string{'f': "Female", 'm': "Male"}[id[1]]
	if accent == "" || gender == "" {
		return ""
	}
	return accent + " · " + gender
}

func splitKokoroID(id string) [2]string {
	for i, c := range id {
		if c == '_' {
			return [2]string{id[:i], id[i+1:]}
		}
	}
	return [2]string{id, ""}
}

// postWhisperUpdate rebuilds whisper-server from source at the requested tag.
// Body: {"version":"v1.8.4"}  — omit or "" to use the pinned default.
// Streams SSE progress events: start | progress | done | error.
func (m *Module) postWhisperUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Version == "" {
		req.Version = "v1.8.4"
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

	emit("start", map[string]any{"version": req.Version})

	if err := m.mgr.RebuildWhisper(req.Version, func(line string) {
		emit("progress", map[string]any{"line": line})
	}); err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}

	emit("done", map[string]any{"version": req.Version, "status": m.mgr.Status()})
}

// postKokoroUpdate upgrades the kokoro-onnx pip package to the requested version.
// Body: {"version":"0.4.7"}  — omit or "" to upgrade to the latest.
// Streams SSE progress events: start | progress | done | error.
func (m *Module) postKokoroUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

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

	emit("start", map[string]any{"package": "kokoro-onnx", "version": req.Version})

	if err := m.mgr.UpgradeKokoro(req.Version, func(line string) {
		emit("progress", map[string]any{"line": line})
	}); err != nil {
		emit("error", map[string]any{"message": err.Error()})
		return
	}

	emit("done", map[string]any{"version": m.mgr.KokoroVersion(), "status": m.mgr.Status()})
}
