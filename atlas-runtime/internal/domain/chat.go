package domain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/storage"
)

// ChatDomain handles message sending, conversation management, and memories.
//
// Routes owned:
//
//	POST   /message               — send a message (triggers SSE + returns MessageResponse)
//	GET    /message/stream        — SSE event stream for a conversation
//	GET    /conversations         — list recent conversations
//	GET    /conversations/search  — search conversations (stub)
//	GET    /conversations/:id     — get conversation detail
//	GET    /memories              — list memories (optional ?category=&limit=)
//	GET    /memories/search       — search memories (?query=)
//	POST   /memories/{id}/delete  — delete a memory by ID
//	GET/PUT  /mind                — stub (Phase 5)
//	GET/PUT  /skills-memory       — stub (Phase 5)
type ChatDomain struct {
	chatSvc     *chat.Service
	broadcaster *chat.Broadcaster
	db          *storage.DB
	dreamRunner func() // runs a dream cycle in the calling goroutine; nil = not configured
}

// NewChatDomain creates a ChatDomain.
func NewChatDomain(chatSvc *chat.Service, bc *chat.Broadcaster, db *storage.DB) *ChatDomain {
	return &ChatDomain{chatSvc: chatSvc, broadcaster: bc, db: db}
}

// SetDreamRunner wires the dream-cycle trigger so POST /mind/dream works.
func (d *ChatDomain) SetDreamRunner(fn func()) { d.dreamRunner = fn }

// Register mounts all chat routes on the given router.
func (d *ChatDomain) Register(r chi.Router) {
	r.Post("/message", d.postMessage)
	r.Post("/message/cancel", d.postCancelMessage)
	r.Get("/message/stream", d.streamMessage)
	r.Get("/conversations", d.listConversations)
	r.Get("/conversations/search", d.searchConversations)
	r.Get("/conversations/{id}", d.getConversation)
	r.Delete("/conversations", d.deleteAllConversations)

	// Memories — natively served from SQLite.
	r.Get("/memories", d.listMemories)
	r.Get("/memories/search", d.searchMemories)
	r.Post("/memories/{id}/delete", d.deleteMemory)
	// Memory CRUD.
	r.Post("/memories", d.createMemory)
	r.Get("/memories/{id}", d.getMemory)
	r.Put("/memories/{id}", d.updateMemory)
	r.Delete("/memories/{id}", d.deleteMemoryByID)
	r.Post("/memories/{id}/confirm", d.confirmMemory)
	r.Get("/memories/{id}/tags", d.getMemoryTags)

	// MIND.md operator prompt.
	r.Get("/mind", d.getMind)
	r.Put("/mind", d.putMind)
	r.Post("/mind/regenerate", d.regenerateMind)
	r.Get("/mind/dream", d.getDreamState)
	r.Post("/mind/dream", d.forceDreamCycle)
	r.Get("/skills-memory", d.getSkillsMemory)
	r.Put("/skills-memory", d.putSkillsMemory)

	// DIARY.md — read-only; written exclusively by the dream cycle.
	r.Get("/diary", d.getDiary)

	// Artifact file download — token redeemable once, served inline or as attachment.
	r.Get("/artifacts/{token}", d.getArtifact)

	// Mind-thoughts greeting flow (phase 5). The web client calls
	// POST /chat/greeting on chat-open to drain any queued acted-on
	// thoughts into a live greeting message. GET /chat/pending-greetings
	// returns the queue count for the sidebar dot in phase 6.
	r.Post("/chat/greeting", d.postChatGreeting)
	r.Get("/chat/pending-greetings", d.getPendingGreetings)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (d *ChatDomain) postMessage(w http.ResponseWriter, r *http.Request) {
	var req chat.MessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "Missing 'message' field.")
		return
	}

	resp, err := d.chatSvc.HandleMessage(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (d *ChatDomain) postCancelMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversationId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ConversationID == "" {
		writeError(w, http.StatusBadRequest, "Missing 'conversationId' field.")
		return
	}
	d.chatSvc.CancelTurn(body.ConversationID)
	w.WriteHeader(http.StatusNoContent)
}

func (d *ChatDomain) streamMessage(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported by this server", http.StatusInternalServerError)
		return
	}

	convID := r.URL.Query().Get("conversationID")
	if convID == "" {
		writeError(w, http.StatusBadRequest, "Missing 'conversationID' query parameter.")
		return
	}

	subID, ch := d.broadcaster.Register(convID)
	defer d.broadcaster.Remove(convID, subID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case event, open := <-ch:
			if !open {
				return
			}
			w.Write(event.Encoded())
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func (d *ChatDomain) listConversations(w http.ResponseWriter, r *http.Request) {
	const defaultConvLimit, maxConvLimit = 20, 200
	limitStr := r.URL.Query().Get("limit")
	limit := defaultConvLimit
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
		limit = n
	}
	if limit > maxConvLimit {
		limit = maxConvLimit
	}

	rows, err := d.db.ListConversationSummaries(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type convSummary struct {
		ID                   string  `json:"id"`
		CreatedAt            string  `json:"createdAt"`
		UpdatedAt            string  `json:"updatedAt"`
		Platform             string  `json:"platform"`
		MessageCount         int     `json:"messageCount"`
		FirstUserMessage     *string `json:"firstUserMessage,omitempty"`
		LastAssistantMessage *string `json:"lastAssistantMessage,omitempty"`
		PlatformContext      *string `json:"platformContext,omitempty"`
	}
	out := make([]convSummary, len(rows))
	for i, r := range rows {
		out[i] = convSummary{
			ID:                   r.ID,
			CreatedAt:            r.CreatedAt,
			UpdatedAt:            r.UpdatedAt,
			Platform:             r.Platform,
			MessageCount:         r.MessageCount,
			FirstUserMessage:     r.FirstUserMessage,
			LastAssistantMessage: r.LastAssistantMessage,
			PlatformContext:      r.PlatformContext,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *ChatDomain) searchConversations(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	const defaultSearchLimit, maxSearchLimit = 20, 200
	limit := defaultSearchLimit
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	rows, err := d.db.SearchConversationSummaries(query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type convSummary struct {
		ID                   string  `json:"id"`
		CreatedAt            string  `json:"createdAt"`
		UpdatedAt            string  `json:"updatedAt"`
		Platform             string  `json:"platform"`
		MessageCount         int     `json:"messageCount"`
		FirstUserMessage     *string `json:"firstUserMessage,omitempty"`
		LastAssistantMessage *string `json:"lastAssistantMessage,omitempty"`
		PlatformContext      *string `json:"platformContext,omitempty"`
	}
	out := make([]convSummary, len(rows))
	for i, r := range rows {
		out[i] = convSummary{
			ID:                   r.ID,
			CreatedAt:            r.CreatedAt,
			UpdatedAt:            r.UpdatedAt,
			Platform:             r.Platform,
			MessageCount:         r.MessageCount,
			FirstUserMessage:     r.FirstUserMessage,
			LastAssistantMessage: r.LastAssistantMessage,
			PlatformContext:      r.PlatformContext,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *ChatDomain) deleteAllConversations(w http.ResponseWriter, r *http.Request) {
	if err := d.db.DeleteAllConversations(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *ChatDomain) getConversation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	conv, err := d.db.FetchConversation(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conv == nil {
		writeError(w, http.StatusNotFound, "Conversation not found.")
		return
	}

	msgs, err := d.db.ListMessages(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type msgItem struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
		Blocks    any    `json:"blocks,omitempty"`
	}
	items := make([]msgItem, len(msgs))
	for i, m := range msgs {
		item := msgItem{ID: m.ID, Role: m.Role, Content: m.Content, Timestamp: m.Timestamp}
		if m.BlocksJSON != nil {
			item.Blocks = chat.HydrateStoredBlocks(*m.BlocksJSON)
		}
		items[i] = item
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":        conv.ID,
		"createdAt": conv.CreatedAt,
		"updatedAt": conv.UpdatedAt,
		"messages":  items,
	})
}

// ── Memories ──────────────────────────────────────────────────────────────────

// memoryJSON is the JSON shape sent to the web UI, matching the MemoryItem interface.
type memoryJSON struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Source          string   `json:"source,omitempty"`
	Confidence      float64  `json:"confidence"`
	Importance      float64  `json:"importance"`
	IsUserConfirmed bool     `json:"isUserConfirmed"`
	IsSensitive     bool     `json:"isSensitive"`
	Tags            []string `json:"tags"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

func rowToMemoryJSON(r storage.MemoryRow) memoryJSON {
	var tags []string
	if err := json.Unmarshal([]byte(r.TagsJSON), &tags); err != nil || tags == nil {
		tags = []string{}
	}
	return memoryJSON{
		ID:              r.ID,
		Category:        r.Category,
		Title:           r.Title,
		Content:         r.Content,
		Source:          r.Source,
		Confidence:      r.Confidence,
		Importance:      r.Importance,
		IsUserConfirmed: r.IsUserConfirmed,
		IsSensitive:     r.IsSensitive,
		Tags:            tags,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func (d *ChatDomain) listMemories(w http.ResponseWriter, r *http.Request) {
	const defaultMemLimit, maxMemLimit = 100, 500
	category := r.URL.Query().Get("category")
	limit := defaultMemLimit
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxMemLimit {
		limit = maxMemLimit
	}

	rows, err := d.db.ListMemories(limit, category)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]memoryJSON, len(rows))
	for i, r := range rows {
		out[i] = rowToMemoryJSON(r)
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *ChatDomain) searchMemories(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writeJSON(w, http.StatusOK, []memoryJSON{})
		return
	}

	rows, err := d.db.SearchMemories(query, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]memoryJSON, len(rows))
	for i, r := range rows {
		out[i] = rowToMemoryJSON(r)
	}
	writeJSON(w, http.StatusOK, out)
}

func (d *ChatDomain) deleteMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := d.db.DeleteMemory(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Memory CRUD ───────────────────────────────────────────────────────────────

type memoryWriteRequest struct {
	Category    string   `json:"category"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Source      string   `json:"source,omitempty"`
	Confidence  float64  `json:"confidence"`
	Importance  float64  `json:"importance"`
	IsSensitive bool     `json:"isSensitive"`
	Tags        []string `json:"tags"`
}

func (d *ChatDomain) createMemory(w http.ResponseWriter, r *http.Request) {
	var req memoryWriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Category == "" || req.Title == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "category, title, and content are required")
		return
	}

	tagsJSON, _ := json.Marshal(req.Tags)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := storage.MemoryRow{
		ID:              newDomainUUID(),
		Category:        req.Category,
		Title:           req.Title,
		Content:         req.Content,
		Source:          req.Source,
		Confidence:      req.Confidence,
		Importance:      req.Importance,
		IsSensitive:     req.IsSensitive,
		IsUserConfirmed: true, // user-created memories are always confirmed
		TagsJSON:        string(tagsJSON),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := d.db.SaveMemory(row); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fetched, err := d.db.FetchMemory(row.ID)
	if err != nil || fetched == nil {
		writeJSON(w, http.StatusCreated, rowToMemoryJSON(row))
		return
	}
	writeJSON(w, http.StatusCreated, rowToMemoryJSON(*fetched))
}

func (d *ChatDomain) getMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := d.db.FetchMemory(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "Memory not found.")
		return
	}
	writeJSON(w, http.StatusOK, rowToMemoryJSON(*row))
}

func (d *ChatDomain) updateMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := d.db.FetchMemory(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "Memory not found.")
		return
	}

	var req memoryWriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	tagsJSON, _ := json.Marshal(req.Tags)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updated := storage.MemoryRow{
		ID:              id,
		Category:        req.Category,
		Title:           req.Title,
		Content:         req.Content,
		Source:          req.Source,
		Confidence:      req.Confidence,
		Importance:      req.Importance,
		IsSensitive:     req.IsSensitive,
		IsUserConfirmed: existing.IsUserConfirmed,
		TagsJSON:        string(tagsJSON),
		CreatedAt:       existing.CreatedAt,
		UpdatedAt:       now,
	}
	if err := d.db.UpdateMemory(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rowToMemoryJSON(updated))
}

func (d *ChatDomain) deleteMemoryByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := d.db.DeleteMemory(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── MIND.md ───────────────────────────────────────────────────────────────────

func (d *ChatDomain) getMind(w http.ResponseWriter, r *http.Request) {
	path := config.SupportDir() + "/MIND.md"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return empty string if file doesn't exist yet.
		writeJSON(w, http.StatusOK, map[string]string{"content": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": string(data)})
}

func (d *ChatDomain) putMind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	path := config.SupportDir() + "/MIND.md"
	if err := os.MkdirAll(config.SupportDir(), 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	content := strings.TrimSpace(body.Content)
	if err := atomicWriteFile(path, []byte(content), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (d *ChatDomain) regenerateMind(w http.ResponseWriter, r *http.Request) {
	content, err := d.chatSvc.RegenerateMind(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (d *ChatDomain) getDreamState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"running": mind.IsDreamRunning()})
}

func (d *ChatDomain) forceDreamCycle(w http.ResponseWriter, r *http.Request) {
	if d.dreamRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "dream cycle not configured")
		return
	}
	// Run in a goroutine; respond immediately so the caller isn't left hanging
	// for the full 5-phase cycle duration (~30–60s with AI calls).
	go d.dreamRunner()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// ── Skills Memory (SKILLS.md) ─────────────────────────────────────────────────

func (d *ChatDomain) getSkillsMemory(w http.ResponseWriter, r *http.Request) {
	path := config.SupportDir() + "/SKILLS.md"
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"content": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": string(data)})
}

func (d *ChatDomain) putSkillsMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	path := config.SupportDir() + "/SKILLS.md"
	if err := os.MkdirAll(config.SupportDir(), 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	content := strings.TrimSpace(body.Content)
	if err := atomicWriteFile(path, []byte(content), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

// ── Memory confirm ────────────────────────────────────────────────────────────

func (d *ChatDomain) confirmMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := d.db.ConfirmMemory(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	row, err := d.db.FetchMemory(id)
	if err != nil || row == nil {
		writeError(w, http.StatusNotFound, "Memory not found.")
		return
	}
	writeJSON(w, http.StatusOK, rowToMemoryJSON(*row))
}

func (d *ChatDomain) getMemoryTags(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := d.db.FetchMemory(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "Memory not found.")
		return
	}
	var tags []string
	if err := json.Unmarshal([]byte(row.TagsJSON), &tags); err != nil || tags == nil {
		tags = []string{}
	}
	writeJSON(w, http.StatusOK, tags)
}

// ── Artifact file download ──────────────────────────────────────────────────

// getArtifact resolves a download token to a local file path and serves the
// file inline (images) or as an attachment (everything else). The token is
// registered by the agent loop whenever a tool produces a file artifact.
func (d *ChatDomain) getArtifact(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "Missing artifact token.")
		return
	}

	path, ok := agent.ResolveArtifact(token)
	if !ok {
		writeError(w, http.StatusNotFound, "Artifact not found or token expired.")
		return
	}

	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Artifact file not found on disk.")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not stat artifact file.")
		return
	}

	mimeType := agent.MimeTypeForPath(path)
	filename := info.Name()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "private, max-age=3600")

	http.ServeContent(w, r, filename, info.ModTime(), f)
}

// ── DIARY.md ──────────────────────────────────────────────────────────────────

func (d *ChatDomain) getDiary(w http.ResponseWriter, r *http.Request) {
	content := features.ReadDiary(config.SupportDir())
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

// ── Mind-thoughts greeting (phase 5) ──────────────────────────────────────────

// postChatGreeting drains any queued acted-on-thought results into a live
// greeting assistant message, persists it to the active conversation, and
// streams it via SSE. Returns a GreetingResponse describing what happened.
// If the queue is empty, returns Delivered: false with Skipped: "queue_empty".
func (d *ChatDomain) postChatGreeting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string `json:"conversationID"`
	}
	// Optional body — empty is fine.
	_ = json.NewDecoder(r.Body).Decode(&req)

	resp, err := d.chatSvc.HandleGreeting(r.Context(), req.ConversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getPendingGreetings returns the count of queued greeting entries. Used
// by the sidebar dot to know whether to show the "Atlas has something to
// tell you" indicator.
func (d *ChatDomain) getPendingGreetings(w http.ResponseWriter, r *http.Request) {
	count, err := d.chatSvc.PendingGreetingsCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chat.PendingGreetingsCount{Count: count})
}
