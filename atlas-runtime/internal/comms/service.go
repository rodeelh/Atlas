// Package comms implements the communications platform service for the Go runtime.
// It provides status snapshots, channel listings, and platform enable/validate operations
// sourced from config.json, the Keychain credential bundle, and the shared SQLite database.
package comms

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/comms/discord"
	"atlas-runtime-go/internal/comms/slack"
	"atlas-runtime-go/internal/comms/telegram"
	"atlas-runtime-go/internal/comms/whatsapp"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"
)

// BridgeAttachment is a file attached to an inbound bridge message.
// Data is raw base64 (no data-URL prefix). MimeType is e.g. "image/jpeg", "application/pdf".
type BridgeAttachment struct {
	Filename string
	MimeType string
	Data     string
}

// BridgeRequest is the unified request type passed from any bridge to Atlas.
// It mirrors chat.MessageRequest so that every capability available in the web
// chat is automatically available across all channels. When a new field is added
// to chat.MessageRequest, add it here and map it in main.go — one place each.
type BridgeRequest struct {
	Text        string
	ConvID      string
	Platform    string
	Attachments []BridgeAttachment
}

// ChatHandler is the function type used by bridges to route messages to Atlas.
// Returns: reply text, generated file paths (absolute local paths), conversation ID, error.
type ChatHandler func(ctx context.Context, req BridgeRequest) (string, []string, string, error)

type AutomationDestination struct {
	Platform  string
	ChannelID string
	ThreadID  string
}

// ── JSON shapes that match the Swift CommunicationsSnapshot / CommunicationChannel ──

// PlatformStatus mirrors Swift CommunicationPlatformStatus (camelCase JSON tags).
type PlatformStatus struct {
	Platform             string            `json:"platform"`
	ID                   string            `json:"id"`
	Enabled              bool              `json:"enabled"`
	Connected            bool              `json:"connected"`
	Available            bool              `json:"available"`
	SetupState           string            `json:"setupState"`
	StatusLabel          string            `json:"statusLabel"`
	ConnectedAccountName *string           `json:"connectedAccountName"`
	CredentialConfigured bool              `json:"credentialConfigured"`
	BlockingReason       *string           `json:"blockingReason"`
	RequiredCredentials  []string          `json:"requiredCredentials"`
	LastError            *string           `json:"lastError"`
	LastUpdatedAt        *string           `json:"lastUpdatedAt"`
	Metadata             map[string]string `json:"metadata"`
}

// Snapshot mirrors Swift CommunicationsSnapshot.
type Snapshot struct {
	Platforms []PlatformStatus `json:"platforms"`
	Channels  []ChannelRecord  `json:"channels"`
}

// ChannelRecord mirrors Swift CommunicationChannel.
type ChannelRecord struct {
	ID                      string  `json:"id"`
	Platform                string  `json:"platform"`
	ChannelID               string  `json:"channelID"`
	ChannelName             *string `json:"channelName"`
	UserID                  *string `json:"userID"`
	ThreadID                *string `json:"threadID"`
	ActiveConversationID    string  `json:"activeConversationID"`
	CreatedAt               string  `json:"createdAt"`
	UpdatedAt               string  `json:"updatedAt"`
	LastMessageID           *string `json:"lastMessageID"`
	CanReceiveNotifications bool    `json:"canReceiveNotifications"`
}

// TelegramSession mirrors Swift TelegramSession.
type TelegramSession struct {
	ChatID                int64  `json:"chatID"`
	UserID                *int64 `json:"userID"`
	ActiveConversationID  string `json:"activeConversationID"`
	CreatedAt             string `json:"createdAt"`
	UpdatedAt             string `json:"updatedAt"`
	LastTelegramMessageID *int64 `json:"lastTelegramMessageID"`
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service provides communications platform operations for the Go runtime.
type Service struct {
	cfgStore         *config.Store
	db               *storage.DB
	handler          ChatHandler
	approvalResolver telegram.ApprovalResolver
	transcriber      telegram.TranscribeFunc
	mu               sync.RWMutex
	tgBridge         *telegram.Bridge
	discBridge       *discord.Bridge
	slackBridge      *slack.Bridge
	waBridge         *whatsapp.Bridge

	// webchatSender delivers text directly into the web chat UI by injecting
	// it as an assistant message into the most recent conversation.
	// Set via SetWebChatSender before calling SendAutomationResult.
	webchatSender func(text string) error
}

// New creates a new communications Service.
func New(cfgStore *config.Store, db *storage.DB) *Service {
	return &Service{cfgStore: cfgStore, db: db}
}

// SetWebChatSender wires the function used to deliver automation output to
// the web chat UI. Must be called before automations begin executing.
func (s *Service) SetWebChatSender(fn func(text string) error) {
	s.webchatSender = fn
}

// SetChatHandler sets the handler function used by bridges to route messages to Atlas.
// Must be called before Start().
func (s *Service) SetChatHandler(h ChatHandler) {
	s.handler = h
}

// SetApprovalResolver sets the function used by the Telegram bridge to resolve inline approval buttons.
// Must be called before Start().
func (s *Service) SetApprovalResolver(fn telegram.ApprovalResolver) {
	s.approvalResolver = fn
}

// SetTranscriber sets the voice-to-text function used by the Telegram bridge for
// incoming voice messages. Safe to call before or after Start() — a running bridge
// picks up the transcriber immediately.
func (s *Service) SetTranscriber(fn telegram.TranscribeFunc) {
	s.mu.Lock()
	s.transcriber = fn
	if s.tgBridge != nil {
		s.tgBridge.SetTranscriber(fn)
	}
	s.mu.Unlock()
}

// CheckTelegramWebhookSecret returns true if secretToken matches the configured
// webhook secret, or if no secret is configured (open mode). Call this before
// dispatching a webhook update so the HTTP handler can return 401 synchronously.
func (s *Service) CheckTelegramWebhookSecret(secretToken string) bool {
	secret := s.cfgStore.Load().TelegramWebhookSecret
	return secret == "" || secretToken == secret
}

// DispatchTelegramWebhookUpdate parses and dispatches a raw Telegram update body.
// The caller is responsible for secret validation (use CheckTelegramWebhookSecret).
// Returns an error if the bridge is not running or the body is malformed.
func (s *Service) DispatchTelegramWebhookUpdate(body []byte) error {
	s.mu.RLock()
	bridge := s.tgBridge
	s.mu.RUnlock()
	if bridge == nil {
		return fmt.Errorf("Telegram bridge not running")
	}
	return bridge.HandleWebhookUpdate(body)
}

// Start launches all enabled platform bridges.
func (s *Service) Start() {
	migrateTelegramAttachmentsDir()
	cfg := s.cfgStore.Load()
	bundle := readBundle()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startBridges(cfg, bundle)
}

// migrateTelegramAttachmentsDir moves the legacy TelegramAttachments folder
// (ProjectAtlas/TelegramAttachments/) into the new location under the general
// files directory (ProjectAtlas/files/Telegram/). Runs once at startup; is a
// no-op if the old path doesn't exist or the migration already happened.
func migrateTelegramAttachmentsDir() {
	oldDir := filepath.Join(config.SupportDir(), "TelegramAttachments")
	newDir := config.TelegramAttachmentsDir()

	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return // nothing to migrate
	}
	if _, err := os.Stat(newDir); err == nil {
		return // new location already exists — migration already done
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		// Rename fails across filesystems (shouldn't happen here) — log and continue.
		logstore.Write("warn", "Telegram: could not migrate attachments dir: "+err.Error(),
			map[string]string{"platform": "telegram"})
	} else {
		logstore.Write("info", "Telegram: attachments migrated to "+newDir,
			map[string]string{"platform": "telegram"})
	}
}

// Stop shuts down all running bridges.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopBridges()
}

func (s *Service) startBridges(cfg config.RuntimeConfigSnapshot, bundle credBundle) {
	if s.handler == nil {
		return
	}
	cfgFn := s.cfgStore.Load

	if cfg.TelegramEnabled && strVal(bundle.TelegramBotToken) == "" {
		logstore.Write("warn", "Telegram bridge: enabled but no bot token configured — bridge not started", map[string]string{"platform": "telegram"})
	}

	if cfg.TelegramEnabled && strVal(bundle.TelegramBotToken) != "" && s.tgBridge == nil {
		h := s.handler
		tgHandler := telegram.ChatHandler(func(ctx context.Context, req telegram.BridgeRequest) (string, []string, string, error) {
			ba := make([]BridgeAttachment, len(req.Attachments))
			for i, a := range req.Attachments {
				ba[i] = BridgeAttachment{Filename: a.Filename, MimeType: a.MimeType, Data: a.Data}
			}
			return h(ctx, BridgeRequest{Text: req.Text, ConvID: req.ConvID, Platform: req.Platform, Attachments: ba})
		})
		b := telegram.New(strVal(bundle.TelegramBotToken), s.db, cfgFn, tgHandler)
		if s.approvalResolver != nil {
			b.SetApprovalResolver(s.approvalResolver)
		}
		if s.transcriber != nil {
			b.SetTranscriber(s.transcriber)
		}
		s.tgBridge = b
		b.Start()
	}
	if cfg.DiscordEnabled && strVal(bundle.DiscordBotToken) == "" {
		logstore.Write("warn", "Discord bridge: enabled but no bot token configured — bridge not started", map[string]string{"platform": "discord"})
	}

	if cfg.DiscordEnabled && strVal(bundle.DiscordBotToken) != "" && s.discBridge == nil {
		h := s.handler
		discHandler := discord.ChatHandler(func(ctx context.Context, req discord.BridgeRequest) (string, []string, string, error) {
			ba := make([]BridgeAttachment, len(req.Attachments))
			for i, a := range req.Attachments {
				ba[i] = BridgeAttachment{Filename: a.Filename, MimeType: a.MimeType, Data: a.Data}
			}
			return h(ctx, BridgeRequest{Text: req.Text, ConvID: req.ConvID, Platform: req.Platform, Attachments: ba})
		})
		b := discord.New(strVal(bundle.DiscordBotToken), s.db, cfgFn, discHandler)
		s.discBridge = b
		b.Start()
	}
	if cfg.SlackEnabled && (strVal(bundle.SlackBotToken) == "" || strVal(bundle.SlackAppToken) == "") {
		logstore.Write("warn", "Slack bridge: enabled but bot token or app token missing — bridge not started", map[string]string{"platform": "slack"})
	}

	if cfg.SlackEnabled && strVal(bundle.SlackBotToken) != "" && strVal(bundle.SlackAppToken) != "" && s.slackBridge == nil {
		h := s.handler
		slackHandler := slack.ChatHandler(func(ctx context.Context, req slack.BridgeRequest) (string, string, error) {
			ba := make([]BridgeAttachment, len(req.Attachments))
			for i, a := range req.Attachments {
				ba[i] = BridgeAttachment{Filename: a.Filename, MimeType: a.MimeType, Data: a.Data}
			}
			text, _, convID, err := h(ctx, BridgeRequest{Text: req.Text, ConvID: req.ConvID, Platform: req.Platform, Attachments: ba})
			return text, convID, err
		})
		b := slack.New(strVal(bundle.SlackBotToken), strVal(bundle.SlackAppToken), s.db, cfgFn, slackHandler)
		s.slackBridge = b
		b.Start()
	}
	if cfg.WhatsAppEnabled && s.waBridge == nil {
		h := s.handler
		storeDSN := "file:" + filepath.Join(config.SupportDir(), "whatsapp_auth.db") + "?_pragma=foreign_keys(ON)&_foreign_keys=on"
		b := whatsapp.New(storeDSN, s.db, func(ctx context.Context, req whatsapp.BridgeRequest) (string, string, error) {
			text, _, convID, err := h(ctx, BridgeRequest{Text: req.Text, ConvID: req.ConvID, Platform: req.Platform})
			return text, convID, err
		})
		s.waBridge = b
		b.Start()
	}
}

func (s *Service) stopBridges() {
	if s.tgBridge != nil {
		s.tgBridge.Stop()
		s.tgBridge = nil
	}
	if s.discBridge != nil {
		s.discBridge.Stop()
		s.discBridge = nil
	}
	if s.slackBridge != nil {
		s.slackBridge.Stop()
		s.slackBridge = nil
	}
	if s.waBridge != nil {
		s.waBridge.Stop()
		s.waBridge = nil
	}
}

// Snapshot returns the full communications snapshot (platforms + channels).
func (s *Service) Snapshot() Snapshot {
	cfg := s.cfgStore.Load()
	bundle := readBundle()
	channels := s.channels()

	s.mu.RLock()
	tgConnected := s.tgBridge != nil && s.tgBridge.Connected()
	var tgAccount *string
	if s.tgBridge != nil && s.tgBridge.BotName() != "" {
		n := "@" + s.tgBridge.BotName()
		tgAccount = &n
	}
	var tgErr *string
	if s.tgBridge != nil && s.tgBridge.LastError() != "" {
		e := s.tgBridge.LastError()
		tgErr = &e
	}
	discConnected := s.discBridge != nil && s.discBridge.Connected()
	var discAccount *string
	if s.discBridge != nil && s.discBridge.BotName() != "" {
		n := s.discBridge.BotName()
		discAccount = &n
	}
	var discErr *string
	if s.discBridge != nil && s.discBridge.LastError() != "" {
		e := s.discBridge.LastError()
		discErr = &e
	}
	slackConnected := s.slackBridge != nil && s.slackBridge.Connected()
	var slackAccount *string
	if s.slackBridge != nil && s.slackBridge.TeamName() != "" {
		n := s.slackBridge.TeamName()
		slackAccount = &n
	}
	var slackErr *string
	if s.slackBridge != nil && s.slackBridge.LastError() != "" {
		e := s.slackBridge.LastError()
		slackErr = &e
	}
	waConnected := s.waBridge != nil && s.waBridge.Connected()
	var waAccount *string
	if s.waBridge != nil && s.waBridge.AccountName() != "" {
		n := s.waBridge.AccountName()
		waAccount = &n
	}
	var waErr *string
	if s.waBridge != nil && s.waBridge.LastError() != "" {
		e := s.waBridge.LastError()
		waErr = &e
	}
	waQR := ""
	if s.waBridge != nil {
		waQR = s.waBridge.QRCodeDataURL()
	}
	s.mu.RUnlock()

	platforms := []PlatformStatus{
		s.platformStatus("telegram", cfg, bundle, tgConnected, tgAccount, tgErr),
		s.platformStatus("discord", cfg, bundle, discConnected, discAccount, discErr),
		s.platformStatus("slack", cfg, bundle, slackConnected, slackAccount, slackErr),
		s.platformStatus("whatsapp", cfg, bundle, waConnected, waAccount, waErr),
	}
	for i := range platforms {
		if platforms[i].Platform == "whatsapp" && waQR != "" {
			platforms[i].Metadata["qrCodeDataURL"] = waQR
		}
	}

	return Snapshot{
		Platforms: platforms,
		Channels:  channels,
	}
}

// Channels returns all communication channels from SQLite.
func (s *Service) Channels() []ChannelRecord {
	return s.channels()
}

func (s *Service) channels() []ChannelRecord {
	rows, err := s.db.ListCommunicationChannels("")
	if err != nil {
		rows = nil
	}
	merged := make(map[string]ChannelRecord, len(rows))

	for _, r := range rows {
		tid := normalizedThreadID(r.ThreadID)
		record := ChannelRecord{
			ID:                      strings.Join([]string{r.Platform, r.ChannelID, r.ThreadID}, ":"),
			Platform:                r.Platform,
			ChannelID:               r.ChannelID,
			ChannelName:             r.ChannelName,
			UserID:                  r.UserID,
			ThreadID:                tid,
			ActiveConversationID:    r.ActiveConversationID,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			LastMessageID:           r.LastMessageID,
			CanReceiveNotifications: true,
		}
		merged[channelKey(r.Platform, r.ChannelID, r.ThreadID)] = record
	}

	// Telegram still stores session truth in telegram_sessions. Merge it so
	// Recent Sessions always reflects current Telegram activity.
	if tgRows, tgErr := s.db.ListTelegramSessions(); tgErr == nil {
		for _, r := range tgRows {
			channelID := fmt.Sprintf("%d", r.ChatID)
			key := channelKey("telegram", channelID, "")

			var userID *string
			if r.UserID != nil {
				v := fmt.Sprintf("%d", *r.UserID)
				userID = &v
			}
			var lastMessageID *string
			if r.LastMessageID != nil {
				v := fmt.Sprintf("%d", *r.LastMessageID)
				lastMessageID = &v
			}

			record := ChannelRecord{
				ID:                      strings.Join([]string{"telegram", channelID, ""}, ":"),
				Platform:                "telegram",
				ChannelID:               channelID,
				ChannelName:             nil,
				UserID:                  userID,
				ThreadID:                nil,
				ActiveConversationID:    r.ActiveConversationID,
				CreatedAt:               r.CreatedAt,
				UpdatedAt:               r.UpdatedAt,
				LastMessageID:           lastMessageID,
				CanReceiveNotifications: true,
			}
			if existing, ok := merged[key]; !ok || timestampAfter(record.UpdatedAt, existing.UpdatedAt) {
				merged[key] = record
			}
		}
	}

	out := make([]ChannelRecord, 0, len(merged))
	for _, channel := range merged {
		out = append(out, channel)
	}
	sort.Slice(out, func(i, j int) bool {
		return timestampAfter(out[i].UpdatedAt, out[j].UpdatedAt)
	})

	return out
}

func channelKey(platform, channelID, threadID string) string {
	return strings.Join([]string{platform, channelID, threadID}, ":")
}

func timestampAfter(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA == nil && errB == nil {
		return ta.After(tb)
	}
	return a > b
}

func normalizedThreadID(raw string) *string {
	if raw == "" {
		return nil
	}
	return &raw
}

// TelegramSessions returns all known Telegram sessions from SQLite.
func (s *Service) TelegramSessions() []TelegramSession {
	rows, err := s.db.ListTelegramSessions()
	if err != nil {
		return []TelegramSession{}
	}
	out := make([]TelegramSession, 0, len(rows))
	for _, r := range rows {
		out = append(out, TelegramSession{
			ChatID:                r.ChatID,
			UserID:                r.UserID,
			ActiveConversationID:  r.ActiveConversationID,
			CreatedAt:             r.CreatedAt,
			UpdatedAt:             r.UpdatedAt,
			LastTelegramMessageID: r.LastMessageID,
		})
	}
	return out
}

// SetupValues returns existing credential values for the given platform (for pre-filling forms).
func (s *Service) SetupValues(platform string) map[string]string {
	bundle := readBundle()
	cfg := s.cfgStore.Load()
	values := map[string]string{}

	switch platform {
	case "telegram":
		if t := strVal(bundle.TelegramBotToken); t != "" {
			values["telegram"] = t
		}
	case "discord":
		if d := strVal(bundle.DiscordBotToken); d != "" {
			values["discord"] = d
		}
		if cfg.DiscordClientID != "" {
			values["discordClientID"] = cfg.DiscordClientID
		}
	case "slack":
		if b := strVal(bundle.SlackBotToken); b != "" {
			values["slackBot"] = b
		}
		if a := strVal(bundle.SlackAppToken); a != "" {
			values["slackApp"] = a
		}
	}
	return values
}

// UpdatePlatform enables or disables a platform in config.json and returns the updated status.
func (s *Service) UpdatePlatform(platform string, enabled bool) (PlatformStatus, error) {
	cfg := s.cfgStore.Load()
	switch platform {
	case "telegram":
		cfg.TelegramEnabled = enabled
	case "discord":
		cfg.DiscordEnabled = enabled
	case "whatsapp":
		cfg.WhatsAppEnabled = enabled
	case "slack":
		cfg.SlackEnabled = enabled
	default:
		return PlatformStatus{}, fmt.Errorf("unknown platform: %s", platform)
	}
	if err := s.cfgStore.Save(cfg); err != nil {
		return PlatformStatus{}, fmt.Errorf("save config: %w", err)
	}

	bundle := readBundle()
	s.mu.Lock()
	if !enabled {
		switch platform {
		case "telegram":
			if s.tgBridge != nil {
				s.tgBridge.Stop()
				s.tgBridge = nil
			}
		case "discord":
			if s.discBridge != nil {
				s.discBridge.Stop()
				s.discBridge = nil
			}
		case "slack":
			if s.slackBridge != nil {
				s.slackBridge.Stop()
				s.slackBridge = nil
			}
		case "whatsapp":
			if s.waBridge != nil {
				s.waBridge.Stop()
				s.waBridge = nil
			}
		}
	} else {
		s.startBridges(cfg, bundle)
	}
	s.mu.Unlock()

	return s.platformStatus(platform, cfg, bundle, false, nil, nil), nil
}

// ValidatePlatform connects to the platform API with the given credentials (or Keychain creds
// if none are provided), stores any new credentials in the Keychain bundle on success,
// and returns the resulting platform status.
func (s *Service) ValidatePlatform(platform string, credentials map[string]string, discordClientID string) (PlatformStatus, error) {
	bundle := readBundle()
	cfg := s.cfgStore.Load()

	var (
		connected   bool
		accountName *string
		lastErr     *string
	)

	switch platform {
	case "telegram":
		token := credentials["telegram"]
		if token == "" {
			token = strVal(bundle.TelegramBotToken)
		}
		if token == "" {
			e := "No Telegram bot token configured."
			return s.platformStatus(platform, cfg, bundle, false, nil, &e), nil
		}

		ok, username, err := validateTelegram(token)
		if err != nil || !ok {
			errStr := "Telegram validation failed."
			if err != nil {
				errStr = err.Error()
			}
			return s.platformStatus(platform, cfg, bundle, false, nil, &errStr), nil
		}

		connected = true
		accountName = username

		// Persist token + enable platform.
		bundle.TelegramBotToken = &token
		writeBundle(bundle)
		cfg.TelegramEnabled = true
		if err := s.cfgStore.Save(cfg); err != nil {
			log.Printf("comms: ValidatePlatform: save config: %v", err)
		}
		// Restart the bridge so the new token takes effect immediately.
		// Without this, a running bridge with a stale token is never replaced.
		s.mu.Lock()
		if s.tgBridge != nil {
			s.tgBridge.Stop()
			s.tgBridge = nil
		}
		s.startBridges(cfg, readBundle())
		s.mu.Unlock()

	case "discord":
		token := credentials["discord"]
		if token == "" {
			token = strVal(bundle.DiscordBotToken)
		}
		if discordClientID != "" {
			cfg.DiscordClientID = discordClientID
		}
		if token == "" {
			e := "No Discord bot token configured."
			return s.platformStatus(platform, cfg, bundle, false, nil, &e), nil
		}

		ok, botName, err := validateDiscord(token)
		if err != nil || !ok {
			errStr := "Discord validation failed."
			if err != nil {
				errStr = err.Error()
			}
			return s.platformStatus(platform, cfg, bundle, false, nil, &errStr), nil
		}

		connected = true
		accountName = botName

		bundle.DiscordBotToken = &token
		writeBundle(bundle)
		cfg.DiscordEnabled = true
		if discordClientID != "" {
			cfg.DiscordClientID = discordClientID
		}
		if err := s.cfgStore.Save(cfg); err != nil {
			log.Printf("comms: ValidatePlatform: save config: %v", err)
		}

	case "slack":
		botToken := credentials["slackBot"]
		appToken := credentials["slackApp"]
		if botToken == "" {
			botToken = strVal(bundle.SlackBotToken)
		}
		if appToken == "" {
			appToken = strVal(bundle.SlackAppToken)
		}
		if botToken == "" {
			e := "No Slack bot token configured."
			return s.platformStatus(platform, cfg, bundle, false, nil, &e), nil
		}

		ok, workspaceName, err := validateSlack(botToken)
		if err != nil || !ok {
			errStr := "Slack validation failed."
			if err != nil {
				errStr = err.Error()
			}
			return s.platformStatus(platform, cfg, bundle, false, nil, &errStr), nil
		}

		connected = true
		accountName = workspaceName

		bundle.SlackBotToken = &botToken
		if appToken != "" {
			bundle.SlackAppToken = &appToken
		}
		writeBundle(bundle)
		cfg.SlackEnabled = true
		if err := s.cfgStore.Save(cfg); err != nil {
			log.Printf("comms: ValidatePlatform: save config: %v", err)
		}
	case "whatsapp":
		cfg.WhatsAppEnabled = true
		if err := s.cfgStore.Save(cfg); err != nil {
			log.Printf("comms: ValidatePlatform: save config: %v", err)
		}
		s.mu.Lock()
		s.startBridges(cfg, bundle)
		if s.waBridge != nil {
			connected = s.waBridge.Connected()
			if name := s.waBridge.AccountName(); name != "" {
				accountName = &name
			}
			if msg := s.waBridge.LastError(); msg != "" {
				lastErr = &msg
			}
		}
		s.mu.Unlock()

	default:
		return PlatformStatus{}, fmt.Errorf("unknown platform: %s", platform)
	}

	status := s.platformStatus(platform, cfg, bundle, connected, accountName, lastErr)
	if platform == "whatsapp" {
		s.mu.RLock()
		if s.waBridge != nil {
			if qr := s.waBridge.QRCodeDataURL(); qr != "" {
				status.Metadata["qrCodeDataURL"] = qr
			}
		}
		s.mu.RUnlock()
	}
	return status, nil
}

// ── Platform status builder ───────────────────────────────────────────────────

func (s *Service) platformStatus(
	platform string,
	cfg config.RuntimeConfigSnapshot,
	bundle credBundle,
	connected bool,
	accountName *string,
	lastErr *string,
) PlatformStatus {
	var (
		enabled        bool
		credConfigured bool
		requiredCreds  []string
		blockingReason *string
	)

	switch platform {
	case "telegram":
		enabled = cfg.TelegramEnabled
		credConfigured = strVal(bundle.TelegramBotToken) != ""
		requiredCreds = []string{"telegram_bot_token"}
		if !credConfigured {
			br := "Add a Telegram bot token to finish setup."
			blockingReason = &br
		}
	case "discord":
		enabled = cfg.DiscordEnabled
		credConfigured = strVal(bundle.DiscordBotToken) != "" && cfg.DiscordClientID != ""
		requiredCreds = []string{"discord_bot_token", "discord_client_id"}
		if !credConfigured {
			br := "Add a Discord bot token and client ID to finish setup."
			blockingReason = &br
		}
	case "slack":
		enabled = cfg.SlackEnabled
		credConfigured = strVal(bundle.SlackBotToken) != "" && strVal(bundle.SlackAppToken) != ""
		requiredCreds = []string{"slack_bot_token", "slack_app_token"}
		if !credConfigured {
			br := "Add Slack bot and app tokens to finish setup."
			blockingReason = &br
		}
	case "whatsapp":
		enabled = cfg.WhatsAppEnabled
		credConfigured = true
		requiredCreds = []string{}
		if enabled && !connected {
			br := "Scan the QR code from WhatsApp on your phone to finish setup."
			blockingReason = &br
		}
	}

	setupState := computeSetupState(enabled, credConfigured, connected, lastErr)
	statusLabel := computeStatusLabel(setupState, lastErr)
	health := computeHealthLabel(setupState, lastErr)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if lastErr != nil {
		blockingReason = lastErr
	}

	meta := map[string]string{
		"health":        health,
		"lastCheckedAt": now,
	}

	return PlatformStatus{
		Platform:             platform,
		ID:                   platform,
		Enabled:              enabled,
		Connected:            connected,
		Available:            true, // all three platforms are always supported; credConfigured is a separate field
		SetupState:           setupState,
		StatusLabel:          statusLabel,
		ConnectedAccountName: accountName,
		CredentialConfigured: credConfigured,
		BlockingReason:       blockingReason,
		RequiredCredentials:  requiredCreds,
		LastError:            lastErr,
		LastUpdatedAt:        &now,
		Metadata:             meta,
	}
}

func computeSetupState(enabled, credConfigured, connected bool, lastErr *string) string {
	if connected {
		return "ready"
	}
	if !credConfigured {
		return "missing_credentials"
	}
	if lastErr != nil {
		return "validation_failed"
	}
	if !enabled {
		return "not_started"
	}
	return "partial_setup"
}

func computeStatusLabel(setupState string, lastErr *string) string {
	switch setupState {
	case "ready":
		return "Ready"
	case "missing_credentials":
		return "Missing Credentials"
	case "validation_failed":
		return "Validation Failed"
	case "not_started":
		return "Not Started"
	default:
		return "Needs Setup"
	}
}

func computeHealthLabel(setupState string, lastErr *string) string {
	if lastErr != nil {
		return "error"
	}
	switch setupState {
	case "ready":
		return "healthy"
	case "partial_setup":
		return "degraded"
	case "missing_credentials", "not_started":
		return "idle"
	default:
		return "unknown"
	}
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// SendAutomationResult delivers automation output to a configured external destination.
func (s *Service) SendAutomationResult(ctx context.Context, dest AutomationDestination, text string) error {
	_ = ctx // reserved for future cancellation-aware bridge methods
	platform := strings.ToLower(strings.TrimSpace(dest.Platform))
	channelID := strings.TrimSpace(dest.ChannelID)
	threadID := strings.TrimSpace(dest.ThreadID)
	content := strings.TrimSpace(text)
	if platform == "" || channelID == "" {
		return fmt.Errorf("invalid automation destination")
	}
	if content == "" {
		return nil
	}

	s.mu.RLock()
	tg := s.tgBridge
	disc := s.discBridge
	sl := s.slackBridge
	wa := s.waBridge
	s.mu.RUnlock()

	switch platform {
	case "webchat":
		if s.webchatSender == nil {
			return fmt.Errorf("webchat sender is not configured")
		}
		return s.webchatSender(content)
	case "telegram":
		if tg == nil {
			return fmt.Errorf("telegram bridge is not running")
		}
		chatID, err := strconv.ParseInt(channelID, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid telegram chat id %q: %w", channelID, err)
		}
		return tg.SendAutomationMessage(chatID, content)
	case "discord":
		if disc == nil {
			return fmt.Errorf("discord bridge is not running")
		}
		return disc.SendAutomationMessage(channelID, threadID, content)
	case "slack":
		if sl == nil {
			return fmt.Errorf("slack bridge is not running")
		}
		return sl.SendAutomationMessage(channelID, threadID, content)
	case "whatsapp":
		if wa == nil {
			return fmt.Errorf("whatsapp bridge is not running")
		}
		return wa.SendAutomationMessage(channelID, content)
	default:
		return fmt.Errorf("unsupported destination platform: %s", platform)
	}
}
