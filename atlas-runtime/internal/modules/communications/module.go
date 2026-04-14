package communications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/comms/telegram"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
)

type Module struct {
	service *comms.Service
	skills  *skills.Registry
}

func New(service *comms.Service) *Module {
	return &Module{service: service}
}

func (m *Module) ID() string { return "communications" }

func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{Version: "v1"}
}

func (m *Module) Register(host platform.Host) error {
	m.registerAgentActions()
	host.MountProtected(m.registerRoutes)
	host.MountPublic(m.registerPublicRoutes)
	return nil
}

func (m *Module) Start(context.Context) error {
	m.service.Start()
	return nil
}

func (m *Module) Stop(context.Context) error {
	m.service.Stop()
	return nil
}

func (m *Module) SetChatHandler(handler comms.ChatHandler) {
	m.service.SetChatHandler(handler)
}

func (m *Module) SetApprovalResolver(resolver func(toolCallID string, approved bool) error) {
	m.service.SetApprovalResolver(resolver)
}

func (m *Module) SetSkillRegistry(registry *skills.Registry) {
	m.skills = registry
}

// SetTranscriber wires the voice-to-text function into the Telegram bridge so
// incoming voice messages are automatically transcribed before reaching the agent.
func (m *Module) SetTranscriber(fn telegram.TranscribeFunc) {
	m.service.SetTranscriber(fn)
}

// registerPublicRoutes mounts routes that bypass session auth.
// The Telegram webhook endpoint must be reachable by Telegram's servers without
// an Atlas session cookie.
func (m *Module) registerPublicRoutes(r chi.Router) {
	r.Post("/telegram/webhook", m.handleTelegramWebhook)
}

func (m *Module) handleTelegramWebhook(w http.ResponseWriter, r *http.Request) {
	secretToken := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")

	// Validate secret synchronously — must happen before the goroutine so we
	// can return 401 while the connection is still open.
	if !m.service.CheckTelegramWebhookSecret(secretToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB max
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Respond 200 immediately — Telegram retries if we don't respond within 60s,
	// but agent turns can take several minutes. Dispatch processing asynchronously.
	w.WriteHeader(http.StatusOK)

	go func() {
		if err := m.service.DispatchTelegramWebhookUpdate(body); err != nil {
			logstore.Write("warn", "Telegram webhook dispatch: "+err.Error(), map[string]string{"platform": "telegram"})
		}
	}()
}

func (m *Module) registerRoutes(r chi.Router) {
	r.Get("/communications", m.getSnapshot)
	r.Get("/communications/channels", m.getChannels)
	r.Get("/communications/platforms/{platform}/setup", m.getSetupValues)
	r.Put("/communications/platforms/{platform}", m.updatePlatform)
	r.Post("/communications/platforms/{platform}/validate", m.validatePlatform)
	r.Get("/telegram/chats", m.getTelegramChats)
}

func (m *Module) getSnapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.service.Snapshot())
}

func (m *Module) getChannels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.service.Channels())
}

func (m *Module) getTelegramChats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, m.service.TelegramSessions())
}

func (m *Module) getSetupValues(w http.ResponseWriter, r *http.Request) {
	platformID := chi.URLParam(r, "platform")
	values := m.service.SetupValues(platformID)
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}

func (m *Module) updatePlatform(w http.ResponseWriter, r *http.Request) {
	platformID := chi.URLParam(r, "platform")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	status, err := m.service.UpdatePlatform(platformID, body.Enabled)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (m *Module) validatePlatform(w http.ResponseWriter, r *http.Request) {
	platformID := chi.URLParam(r, "platform")

	var body struct {
		Credentials map[string]string `json:"credentials"`
		Config      *struct {
			DiscordClientID string `json:"discordClientID"`
		} `json:"config"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	creds := body.Credentials
	if creds == nil {
		creds = map[string]string{}
	}
	discordClientID := ""
	if body.Config != nil {
		discordClientID = body.Config.DiscordClientID
	}

	status, err := m.service.ValidatePlatform(platformID, creds, discordClientID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
