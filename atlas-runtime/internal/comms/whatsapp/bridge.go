package whatsapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"

	"github.com/skip2/go-qrcode"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waStore "go.mau.fi/whatsmeow/store"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/types/events"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	"google.golang.org/protobuf/proto"
)

type BridgeRequest struct {
	Text     string
	ConvID   string
	Platform string
}

type ChatHandler func(ctx context.Context, req BridgeRequest) (string, string, error)

type Bridge struct {
	db      *storage.DB
	handler ChatHandler

	storeDSN string

	mu        sync.RWMutex
	client    *whatsmeow.Client
	connected bool
	account   string
	lastErr   string
	qrDataURL string
	sentIDs   map[string]time.Time
}

func New(storeDSN string, db *storage.DB, handler ChatHandler) *Bridge {
	return &Bridge{
		storeDSN: storeDSN,
		db:       db,
		handler:  handler,
		sentIDs:  map[string]time.Time{},
	}
}

func (b *Bridge) Start() {
	b.mu.Lock()
	if b.client != nil {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	// Brand this linked companion so WhatsApp doesn't label it generically.
	waStore.SetOSInfo("Atlas", [3]uint32{1, 0, 0})
	waStore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()

	container, err := sqlstore.New(context.Background(), "sqlite", b.storeDSN, waLog.Noop)
	if err != nil {
		b.setError(fmt.Sprintf("whatsapp: open auth store: %v", err))
		return
	}
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		b.setError(fmt.Sprintf("whatsapp: load device: %v", err))
		return
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)
	client.AddEventHandler(b.handleEvent)

	b.mu.Lock()
	b.client = client
	b.connected = false
	b.mu.Unlock()

	if client.Store.ID == nil {
		qrChan, qrErr := client.GetQRChannel(context.Background())
		if qrErr != nil {
			b.setError(fmt.Sprintf("whatsapp: qr channel: %v", qrErr))
		} else {
			go b.consumeQR(qrChan)
		}
	}

	if err := client.Connect(); err != nil {
		b.setError(fmt.Sprintf("whatsapp: connect: %v", err))
		return
	}
}

func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		b.client.Disconnect()
		b.client = nil
	}
	b.connected = false
}

func (b *Bridge) Connected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

func (b *Bridge) LastError() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastErr
}

func (b *Bridge) AccountName() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.account
}

func (b *Bridge) QRCodeDataURL() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.qrDataURL
}

func (b *Bridge) consumeQR(ch <-chan whatsmeow.QRChannelItem) {
	for item := range ch {
		switch item.Event {
		case "code":
			png, err := qrcode.Encode(item.Code, qrcode.Medium, 256)
			if err != nil {
				b.setError(fmt.Sprintf("whatsapp: qr encode: %v", err))
				continue
			}
			dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
			b.mu.Lock()
			b.qrDataURL = dataURL
			b.lastErr = ""
			b.mu.Unlock()
			logstore.Write("info", "WhatsApp QR ready. Scan with your phone.", map[string]string{"platform": "whatsapp"})
		case "success":
			b.mu.Lock()
			b.qrDataURL = ""
			b.connected = true
			b.lastErr = ""
			b.mu.Unlock()
		case "timeout":
			b.setError("whatsapp: qr timed out. validate again to generate a new code")
		case "error":
			b.setError("whatsapp: qr flow failed")
		}
	}
}

func (b *Bridge) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		b.mu.Lock()
		b.connected = true
		b.lastErr = ""
		if b.client != nil && b.client.Store.ID != nil {
			b.account = b.client.Store.ID.User
		}
		b.mu.Unlock()
		logstore.Write("info", "WhatsApp bridge connected", map[string]string{"platform": "whatsapp"})
	case *events.Disconnected:
		b.mu.Lock()
		b.connected = false
		b.mu.Unlock()
	case *events.Message:
		b.handleMessage(v)
	}
}

func (b *Bridge) handleMessage(ev *events.Message) {
	if !b.isAllowedSelfChat(ev) {
		return
	}

	if ev.Info.IsFromMe && b.wasSentByBridge(ev.Info.ID) {
		return
	}
	text := extractMessageText(ev.Message)
	if strings.TrimSpace(text) == "" {
		return
	}
	logstore.Write("info", fmt.Sprintf("WhatsApp: message received in chat=%s", ev.Info.Chat.String()), map[string]string{"platform": "whatsapp"})

	chatID := ev.Info.Chat.String()
	session, _ := b.db.FetchCommSession("whatsapp", chatID, "")
	convID := ""
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if session != nil {
		convID = session.ActiveConversationID
		createdAt = session.CreatedAt
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	reply, newConvID, err := b.handler(ctx, BridgeRequest{
		Text:     text,
		ConvID:   convID,
		Platform: "whatsapp",
	})
	if err != nil {
		b.setError(fmt.Sprintf("whatsapp: handler: %v", err))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	lastMessageID := ev.Info.ID
	row := storage.CommSessionRow{
		Platform:             "whatsapp",
		ChannelID:            chatID,
		ThreadID:             "",
		ActiveConversationID: newConvID,
		CreatedAt:            createdAt,
		UpdatedAt:            now,
		LastMessageID:        &lastMessageID,
	}
	if err := b.db.UpsertCommSession(row); err != nil {
		b.setError(fmt.Sprintf("whatsapp: upsert session: %v", err))
	}

	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	if client == nil {
		return
	}
	out := &waProto.Message{
		Conversation: proto.String(reply),
	}
	resp, err := client.SendMessage(context.Background(), ev.Info.Chat, out)
	if err != nil {
		b.setError(fmt.Sprintf("whatsapp: send: %v", err))
		return
	}
	b.markSentByBridge(resp.ID)
}

func extractMessageText(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if v := msg.GetConversation(); v != "" {
		return v
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	return ""
}

func (b *Bridge) setError(message string) {
	b.mu.Lock()
	b.lastErr = message
	b.mu.Unlock()
	logstore.Write("error", message, map[string]string{"platform": "whatsapp"})
}

func (b *Bridge) isAllowedSelfChat(ev *events.Message) bool {
	if ev == nil {
		return false
	}
	if ev.Info.IsGroup {
		return false
	}
	own := b.ownUser()
	if strings.TrimSpace(own) == "" {
		return false
	}
	// WhatsApp may emit self-chat with alternative addressing (PN vs LID).
	// Accept only threads that still resolve to "me", never other contacts.
	return jidMatchesOwn(ev.Info.Chat.User, own) ||
		jidMatchesOwn(ev.Info.Sender.User, own) ||
		jidMatchesOwn(ev.Info.RecipientAlt.User, own)
}

func (b *Bridge) ownUser() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.client != nil && b.client.Store.ID != nil && b.client.Store.ID.User != "" {
		return b.client.Store.ID.User
	}
	return b.account
}

func jidMatchesOwn(user, own string) bool {
	user = strings.TrimSpace(user)
	own = strings.TrimSpace(own)
	if user == "" || own == "" {
		return false
	}
	return user == own
}

func (b *Bridge) markSentByBridge(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sentIDs[id] = now
	cutoff := now.Add(-10 * time.Minute)
	for messageID, ts := range b.sentIDs {
		if ts.Before(cutoff) {
			delete(b.sentIDs, messageID)
		}
	}
}

func (b *Bridge) wasSentByBridge(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.sentIDs[id]
	return ok
}
