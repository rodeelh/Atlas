package whatsapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type BridgeAttachment struct {
	Filename  string
	MimeType  string
	Data      string // raw base64, no data-URL prefix
	LocalPath string // absolute path where the file was saved on disk
}

type BridgeRequest struct {
	Text        string
	ConvID      string
	Platform    string
	Attachments []BridgeAttachment
}

type ChatHandler func(ctx context.Context, req BridgeRequest) (string, []string, string, error)

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

// Logout fully revokes the WhatsApp session: notifies WhatsApp servers,
// deletes the device entry from the local auth store, and removes the
// database file so the next Start() always begins with a fresh QR scan.
// Use this when the user explicitly disables WhatsApp; use Stop() for
// graceful shutdown where the session should be preserved.
func (b *Bridge) Logout() {
	b.mu.Lock()
	client := b.client
	b.client = nil
	b.connected = false
	b.qrDataURL = ""
	b.account = ""
	b.lastErr = ""
	b.mu.Unlock()

	if client != nil {
		// Notify WhatsApp servers and delete the device from the store.
		// Logout() calls client.Store.Delete() internally on success.
		if err := client.Logout(context.Background()); err != nil {
			logstore.Write("warn",
				fmt.Sprintf("whatsapp: logout: %v — local session will still be cleared", err),
				map[string]string{"platform": "whatsapp"})
		}
		client.Disconnect()
	}

	// Belt-and-suspenders: remove the DB file so that even if Logout()
	// failed (e.g. bridge was stopped before Logout was called), the next
	// Start() finds no stored session and generates a fresh QR.
	if dbPath := waDBPath(b.storeDSN); dbPath != "" {
		if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
			logstore.Write("warn",
				fmt.Sprintf("whatsapp: remove auth db: %v", err),
				map[string]string{"platform": "whatsapp"})
		}
	}
}

// waDBPath extracts the filesystem path from a SQLite DSN of the form
// "file:/path/to/file.db?params".
func waDBPath(dsn string) string {
	path := strings.TrimPrefix(dsn, "file:")
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimSpace(path)
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

// SendAutomationMessage sends automation output to a specific WhatsApp chat JID.
func (b *Bridge) SendAutomationMessage(chatJID, text string) error {
	if !b.Connected() {
		return fmt.Errorf("whatsapp bridge is not connected")
	}
	content := strings.TrimSpace(text)
	if content == "" {
		return nil
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid whatsapp destination JID %q: %w", chatJID, err)
	}
	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("whatsapp bridge client unavailable")
	}
	out := &waProto.Message{Conversation: proto.String(content)}
	resp, err := client.SendMessage(context.Background(), jid, out)
	if err != nil {
		return fmt.Errorf("whatsapp send: %w", err)
	}
	b.markSentByBridge(resp.ID)
	return nil
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
	attachments := b.extractAttachments(ev)

	// Embed local file paths in the text so conversation history retains a
	// resolvable reference for follow-up turns (base64 is never stored in SQLite).
	for _, att := range attachments {
		if att.LocalPath != "" {
			text = strings.TrimSpace(text + "\n\n[File saved to: " + att.LocalPath + "]")
		}
	}

	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
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
	reply, filePaths, newConvID, err := b.handler(ctx, BridgeRequest{
		Text:        text,
		ConvID:      convID,
		Platform:    "whatsapp",
		Attachments: attachments,
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

	// Send any generated files (images, charts, etc.) before the text reply.
	for _, fp := range filePaths {
		b.sendImageFile(ev.Info.Chat, fp)
	}

	// Send text reply, stripping raw file paths the model may have included.
	cleanReply := reply
	for _, fp := range filePaths {
		cleanReply = strings.ReplaceAll(cleanReply, fp, filepath.Base(fp))
	}
	if strings.TrimSpace(cleanReply) != "" {
		out := &waProto.Message{
			Conversation: proto.String(cleanReply),
		}
		resp, err := client.SendMessage(context.Background(), ev.Info.Chat, out)
		if err != nil {
			b.setError(fmt.Sprintf("whatsapp: send: %v", err))
			return
		}
		b.markSentByBridge(resp.ID)
	}
}

// sendImageFile uploads a local file to WhatsApp as an image or document.
func (b *Bridge) sendImageFile(jid types.JID, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		logstore.Write("warn", fmt.Sprintf("whatsapp: read file: %v", err), map[string]string{"platform": "whatsapp"})
		return
	}

	mime := "image/jpeg"
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".png":
		mime = "image/png"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	case ".pdf":
		mime = "application/pdf"
	}

	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	if client == nil {
		return
	}

	mediaType := whatsmeow.MediaImage
	if mime == "application/pdf" {
		mediaType = whatsmeow.MediaDocument
	}

	uploaded, err := client.Upload(context.Background(), data, mediaType)
	if err != nil {
		logstore.Write("warn", fmt.Sprintf("whatsapp: upload media: %v", err), map[string]string{"platform": "whatsapp"})
		return
	}

	var out *waProto.Message
	if mediaType == whatsmeow.MediaImage {
		out = &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String(mime),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	} else {
		out = &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String(mime),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
				FileName:      proto.String(filepath.Base(filePath)),
			},
		}
	}

	resp, err := client.SendMessage(context.Background(), jid, out)
	if err != nil {
		logstore.Write("warn", fmt.Sprintf("whatsapp: send media: %v", err), map[string]string{"platform": "whatsapp"})
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
	if img := msg.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}

// extractAttachments downloads media from an inbound WhatsApp message.
// Files are saved to disk so follow-up turns can reference them by path.
// Only images and PDFs are forwarded to the AI model.
func (b *Bridge) extractAttachments(ev *events.Message) []BridgeAttachment {
	b.mu.RLock()
	client := b.client
	b.mu.RUnlock()
	if client == nil || ev.Message == nil {
		return nil
	}
	msg := ev.Message
	msgID := ev.Info.ID

	if img := msg.GetImageMessage(); img != nil {
		data, err := client.Download(context.Background(), img)
		if err != nil {
			logstore.Write("warn", fmt.Sprintf("whatsapp: download image: %v", err), map[string]string{"platform": "whatsapp"})
			return nil
		}
		mime := img.GetMimetype()
		if mime == "" {
			mime = "image/jpeg"
		}
		filename := "image" + mimeToExt(mime)
		localPath := b.saveAttachment(msgID, filename, data)
		return []BridgeAttachment{{
			Filename:  filename,
			MimeType:  mime,
			Data:      base64.StdEncoding.EncodeToString(data),
			LocalPath: localPath,
		}}
	}

	if doc := msg.GetDocumentMessage(); doc != nil {
		mime := doc.GetMimetype()
		if !strings.HasPrefix(mime, "image/") && mime != "application/pdf" {
			return nil
		}
		data, err := client.Download(context.Background(), doc)
		if err != nil {
			logstore.Write("warn", fmt.Sprintf("whatsapp: download document: %v", err), map[string]string{"platform": "whatsapp"})
			return nil
		}
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document" + mimeToExt(mime)
		}
		localPath := b.saveAttachment(msgID, filename, data)
		return []BridgeAttachment{{
			Filename:  filename,
			MimeType:  mime,
			Data:      base64.StdEncoding.EncodeToString(data),
			LocalPath: localPath,
		}}
	}

	return nil
}

// saveAttachment writes attachment bytes to disk under WhatsAppAttachmentsDir/<msgID>/<filename>.
// Returns the absolute path, or "" on error.
func (b *Bridge) saveAttachment(msgID, filename string, data []byte) string {
	dir := filepath.Join(config.WhatsAppAttachmentsDir(), msgID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	localPath := filepath.Join(dir, filename)
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return ""
	}
	return localPath
}

func mimeToExt(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	default:
		return ""
	}
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
