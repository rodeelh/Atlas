package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/capabilities"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/memory"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

// MessageAttachment is a file attached to a message. Data is raw base64 (no
// data-URL prefix). MimeType is e.g. "image/jpeg", "image/png", "application/pdf".
type MessageAttachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// MessageRequest is the JSON body expected by POST /message.
// Note: the web client sends "conversationId" (lowercase 'd') to match the
// contracts.ts interface, so the JSON tag uses the same casing.
type MessageRequest struct {
	Message        string              `json:"message"`
	ConversationID string              `json:"conversationId,omitempty"`
	Platform       string              `json:"platform,omitempty"`
	Attachments    []MessageAttachment `json:"attachments,omitempty"`
	ToolPolicy     *agent.ToolPolicy   `json:"toolPolicy,omitempty"`
}

// MessageResponse is the JSON body returned by POST /message.
// Matches the contracts.ts MessageResponse interface.
type MessageResponse struct {
	Conversation struct {
		ID       string        `json:"id"`
		Messages []MessageItem `json:"messages"`
	} `json:"conversation"`
	Response struct {
		AssistantMessage string `json:"assistantMessage,omitempty"`
		Status           string `json:"status"`
		ErrorMessage     string `json:"errorMessage,omitempty"`
	} `json:"response"`
	// GeneratedFiles holds absolute local file paths produced during this turn.
	// Populated from tool artifacts and FinalText scanning; used by bridges
	// (Telegram, Discord) to send files natively without relying on text parsing.
	GeneratedFiles []string `json:"generatedFiles,omitempty"`
}

// MessageItem is a single message in a conversation.
type MessageItem struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Blocks    any    `json:"blocks,omitempty"`
}

// Service handles chat message processing: stores messages, calls the AI
// provider, emits SSE events, and returns the final MessageResponse.
// InferenceRecorder is optionally satisfied by EngineAutoStarter implementations
// that can track per-turn performance stats. Type-asserted at call sites so the
// interface stays minimal for engines that don't support it.
type InferenceRecorder interface {
	RecordInference(promptTokens, cachedPromptTokens, completionTokens int, elapsed time.Duration, firstToken time.Duration, streamChunks int, streamChars int)
}

// EngineAutoStarter is satisfied by engine.Manager and engine.MLXManager.
// Defined here as a minimal interface to avoid a direct import of the engine
// package into chat. Stop() is included to support mutual exclusion between
// the llama.cpp and MLX-LM subsystems — starting one requires stopping the other.
type EngineAutoStarter interface {
	IsRunning() bool
	LoadedModel() string
	Start(modelName string, port int, ctxSize int, kvCacheQuant string, draftModel string) error
	Stop() error
	WaitUntilReady(port int, timeout time.Duration) error
	RecordActivity()
}

// mlxPrefiller is an optional capability that the MLX engine manager can
// expose to warm the server KV cache after a cold model load. Implemented
// by *engine.MLXManager; checked via interface assertion so the chat package
// stays free of a direct engine import.
type mlxPrefiller interface {
	PrefillPrompt(ctx context.Context, port int, model, systemPrompt string)
}

// conversationSummary caches a compressed summary of trimmed history messages
// for a given conversation so repeated turns don't recompute it.
type conversationSummary struct {
	summary   string
	trimCount int       // number of messages that were summarized
	createdAt time.Time // for TTL expiry
}

// summaryTTL is how long a cached conversation summary stays valid.
const summaryTTL = 10 * time.Minute

// compactHistoryChars caps how much prior conversation is replayed in compact
// mode. 1200 was far too low — a single PDF summary easily exceeds it, causing
// the agent to lose all document context on the very next turn. 8000 chars
// (~2000 words) is enough to cover a thorough document analysis while still
// trimming genuinely long histories.
const compactHistoryChars = 8000
const compactHistoryMessages = 4

type Service struct {
	db                *storage.DB
	cfgStore          *config.Store
	broadcaster       *Broadcaster
	registry          *skills.Registry
	engine            EngineAutoStarter              // optional — llama.cpp primary (atlas_engine)
	routerEngine      EngineAutoStarter              // optional — llama.cpp router (atlas_engine)
	mlxEngine         EngineAutoStarter              // optional — MLX-LM primary (atlas_mlx)
	mlxRouterEngine   EngineAutoStarter              // optional — MLX-LM router (atlas_mlx, Apple Silicon only)
	summaryCache      map[string]conversationSummary // keyed by conversationID
	greetingTelemetry GreetingTelemetry              // optional — set via SetGreetingTelemetry
	surfacingRec      SurfacingRecorder              // optional — phase 7b test seam
	turnCancels       sync.Map                       // convID → context.CancelFunc for in-flight turns
	pipeline          *Pipeline
	hooks             *HookRegistry
}

// NewService returns a ready chat Service.
func NewService(db *storage.DB, cfgStore *config.Store, bc *Broadcaster, reg *skills.Registry) *Service {
	s := &Service{db: db, cfgStore: cfgStore, broadcaster: bc, registry: reg, summaryCache: make(map[string]conversationSummary)}
	s.hooks = NewHookRegistry()
	s.pipeline = newPipeline(s, s.hooks)
	// Wire the async follow-up sender so async_assignment goroutines (agents module)
	// can push task-completion messages back to the originating conversation.
	AsyncFollowUpSender = s.SendProactive
	// Wire the non-agentic usage hook so memory extraction, reflection, forge
	// research, and other direct LLM calls are tracked alongside chat turns.
	agent.NonAgenticUsageHook = func(ctx context.Context, provider agent.ProviderConfig, usage agent.TokenUsage) {
		s.recordTokenUsage("", provider, usage)
	}
	return s
}

// SetEngineManager wires in the primary Engine LM manager so the chat service
// can auto-start the model when a message arrives and the engine isn't running.
func (s *Service) SetEngineManager(e EngineAutoStarter) {
	s.engine = e
}

// RegisterHook appends a post-turn hook to the pipeline's hook registry.
// Called by main.go to register memory extraction and mind reflection hooks
// without creating a direct dependency from the chat package on those packages.
func (s *Service) RegisterHook(h TurnHook) {
	s.hooks.Register(h)
}

// lookupThoughtBody returns the current body of a thought by id, or ""
// if the thought no longer exists in MIND.md THOUGHTS. Used by the
// engagement classifier to compare the user's reply against the
// thought's actual content. Returns "" on any error — the classifier
// treats a missing body as "skip classification".
func (s *Service) lookupThoughtBody(id string) string {
	list, err := mind.ReadThoughtsSection(config.SupportDir())
	if err != nil || len(list) == 0 {
		return ""
	}
	for _, t := range list {
		if t.ID == id {
			return t.Body
		}
	}
	return ""
}

// thoughtsEnabled returns the current value of the master mind-thoughts
// feature flag. Every mind-thoughts seam in the chat service reads
// through this helper so the gate is a single source of truth.
func (s *Service) thoughtsEnabled() bool {
	if s == nil || s.cfgStore == nil {
		return true
	}
	return s.cfgStore.Load().ThoughtsEnabled
}

// CancelTurn cancels the in-flight agent turn for convID, if any.
// The running agent context is cancelled; the turn exits and emits a
// "cancelled" SSE event so the client can clean up.
func (s *Service) CancelTurn(convID string) {
	if v, ok := s.turnCancels.Load(convID); ok {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
	}
}

// SendProactive persists an assistant message to the database and streams it
// as SSE events to any currently connected listeners for convID.
// Unlike a regular turn it does NOT call broadcaster.Finish, so the persistent
// push channel stays open and can receive further proactive deliveries.
func (s *Service) SendProactive(convID, text string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	msgID := newUUID()
	turnID := newUUID()
	if err := s.db.SaveMessage(msgID, convID, "assistant", text, now); err != nil {
		logstore.Write("warn", "SendProactive: failed to save message",
			map[string]string{"conv": convID, "error": err.Error()})
	}
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_started", ConversationID: convID, TurnID: turnID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_delta", Content: text, ConversationID: convID, TurnID: turnID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_done", ConversationID: convID, TurnID: turnID})
}

// SetRouterEngineManager wires in the tool-router Engine LM manager so the chat
// service can auto-start the router when tool selection mode is "llm".
func (s *Service) SetRouterEngineManager(e EngineAutoStarter) {
	s.routerEngine = e
}

// SetMLXEngineManager wires in the MLX-LM primary engine manager.
// The chat service uses it to auto-start the model on first message when
// atlas_mlx is the active provider, and to enforce mutual exclusion with
// the llama.cpp subsystem.
func (s *Service) SetMLXEngineManager(e EngineAutoStarter) {
	s.mlxEngine = e
}

// SetMLXRouterEngineManager wires in the MLX-LM router manager.
// The router is MLX-exclusive — it is only used when atlas_mlx is the
// active provider and tool selection mode is "llm".
func (s *Service) SetMLXRouterEngineManager(e EngineAutoStarter) {
	s.mlxRouterEngine = e
}

// broadcasterEmitter adapts *Broadcaster to agent.Emitter.
// This avoids a circular import between agent ↔ chat.
type broadcasterEmitter struct {
	bc *Broadcaster
}

func (be *broadcasterEmitter) Emit(convID string, e agent.EmitEvent) {
	be.bc.Emit(convID, SSEEvent{
		Type:           e.Type,
		Content:        e.Content,
		Role:           e.Role,
		ConversationID: e.ConvID,
		TurnID:         e.TurnID,
		ToolName:       e.ToolName,
		ToolCallID:     e.ToolCallID,
		ApprovalID:     e.ApprovalID,
		Arguments:      e.Arguments,
		Error:          e.Error,
		Status:         e.Status,
		Filename:       e.Filename,
		MimeType:       e.MimeType,
		FileSize:       e.FileSize,
		FileToken:      e.FileToken,
		Result:         e.Result,
	})
}

func (be *broadcasterEmitter) Finish(convID string) {
	be.bc.Finish(convID)
}

func appendRequestToolsMeta(tools []map[string]any) []map[string]any {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name == "request_tools" {
			return tools
		}
	}
	out := make([]map[string]any, 0, len(tools)+1)
	out = append(out, tools...)
	out = append(out, agent.RequestToolsDef())
	return out
}

// buildUserContent converts the message text and any attachments into the
// content value for the user OAIMessage.
//
//   - No attachments → plain string (no change to existing behaviour).
//   - Attachments present → []map[string]any content-parts array using the
//     OpenAI image_url format. Gemini's OpenAI-compat endpoint accepts the same
//     format. The Anthropic path in provider.go converts these parts when it
//     builds the Anthropic request.
//
// Images are embedded for cloud providers (OpenAI, Anthropic, Gemini) only.
// Call sites are responsible for handling LM Studio separately (degradation).
// PDFs are always embedded for all providers that accept them.
func buildUserContent(text string, attachments []MessageAttachment) any {
	if len(attachments) == 0 {
		return text
	}
	var parts []map[string]any
	if text != "" {
		parts = append(parts, map[string]any{"type": "text", "text": text})
	}
	for _, a := range attachments {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": "data:" + a.MimeType + ";base64," + a.Data,
			},
		})
	}
	return parts
}

// hasImageAttachments reports whether any attachment is an image (not a PDF).
func hasImageAttachments(attachments []MessageAttachment) bool {
	for _, a := range attachments {
		if strings.HasPrefix(a.MimeType, "image/") {
			return true
		}
	}
	return false
}

// openRouterModelSupportsImage reports whether a specific OpenRouter model
// advertises image input support.
func openRouterModelSupportsImage(apiKey, model string) (supported bool, known bool, err error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return false, false, nil
	}
	// OpenRouter auto routers are synthetic IDs and may not appear in /models.
	switch model {
	case "openrouter/auto":
		return true, true, nil
	case "openrouter/auto:free":
		return false, true, nil
	}

	req, err := http.NewRequest("GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/rodeelh/project-atlas")
	req.Header.Set("X-Title", "Atlas")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("openrouter models returned %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID           string `json:"id"`
			Architecture struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, false, err
	}

	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) != model {
			continue
		}
		hasImage := false
		hasText := false
		for _, modality := range m.Architecture.InputModalities {
			switch strings.ToLower(strings.TrimSpace(modality)) {
			case "image":
				hasImage = true
			case "text":
				hasText = true
			}
		}
		if hasImage {
			return true, true, nil
		}
		// If OpenRouter reports modalities and image is absent, treat as text-only.
		if len(m.Architecture.InputModalities) > 0 && hasText {
			return false, true, nil
		}
		return false, false, nil
	}
	return false, false, nil
}


// buildTrimmedHistoryNote generates the compact context note prepended when
// older messages are trimmed from the conversation window. Returns a cached
// summary if available and still valid, otherwise builds an excerpt-based
// summary (same as the original approach).
func (s *Service) buildTrimmedHistoryNote(convID string, trimCount int, trimmed []storage.MessageRow, currentMsgID string) string {
	// Check summary cache — reuse if the trim count matches and TTL is valid.
	if cached, ok := s.summaryCache[convID]; ok && cached.trimCount == trimCount && time.Since(cached.createdAt) < summaryTTL {
		return cached.summary
	}

	var excerpts []string
	var outcomes []string
	var topics []string
	for _, m := range trimmed {
		if m.Role == "user" && m.ID != currentMsgID {
			exc := strings.Join(strings.Fields(m.Content), " ")
			if len([]rune(exc)) > 80 {
				exc = string([]rune(exc)[:80]) + "…"
			}
			excerpts = append(excerpts, exc)
			for _, token := range tokenizeTrimmedHistory(m.Content) {
				if len(topics) >= 4 {
					break
				}
				if !containsString(topics, token) {
					topics = append(topics, token)
				}
			}
		} else if m.Role == "assistant" {
			outcome := strings.Join(strings.Fields(m.Content), " ")
			if outcome == "" {
				continue
			}
			if len([]rune(outcome)) > 90 {
				outcome = string([]rune(outcome)[:90]) + "…"
			}
			outcomes = append(outcomes, outcome)
		}
	}
	if len(excerpts) > 3 {
		excerpts = excerpts[len(excerpts)-3:]
	}
	if len(outcomes) > 2 {
		outcomes = outcomes[len(outcomes)-2:]
	}
	if len(excerpts) == 0 {
		return ""
	}
	note := fmt.Sprintf("[%d earlier msgs omitted.", trimCount)
	if len(topics) > 0 {
		note += " Topics: " + strings.Join(topics, ", ") + "."
	}
	note += " Recent asks: " + strings.Join(excerpts, " / ") + "."
	if len(outcomes) > 0 {
		note += " Latest progress: " + strings.Join(outcomes, " / ") + "."
	}
	note += "]"

	// Cache for reuse on subsequent turns in the same conversation.
	// Evict stale entries to prevent unbounded growth.
	now := time.Now()
	for k, v := range s.summaryCache {
		if now.Sub(v.createdAt) > summaryTTL {
			delete(s.summaryCache, k)
		}
	}
	s.summaryCache[convID] = conversationSummary{
		summary:   note,
		trimCount: trimCount,
		createdAt: now,
	}
	return note
}

func tokenizeTrimmedHistory(content string) []string {
	raw := strings.Fields(strings.ToLower(content))
	out := make([]string, 0, 4)
	for _, token := range raw {
		token = strings.Trim(token, ".,!?;:'\"()[]{}")
		if len(token) < 4 {
			continue
		}
		switch token {
		case "that", "this", "with", "from", "have", "about", "into", "your", "please":
			continue
		}
		out = append(out, token)
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func shouldCompactHistory(userMessage string) bool {
	tokens := strings.Fields(strings.ToLower(userMessage))
	referential := map[string]bool{
		"that": true, "those": true, "they": true, "them": true, "same": true, "again": true, "instead": true,
		"continue": true, "earlier": true, "previous": true, "above": true, "change": true, "update": true, "fix": true,
		"also": true, "another": true,
	}
	for _, token := range tokens {
		token = strings.Trim(token, ".,!?;:'\"()[]{}")
		if referential[token] {
			return false
		}
	}
	phrases := []string{
		"continue", "earlier", "previous", "above", "change", "update", "fix",
		"more like", "make it",
	}
	lower := strings.ToLower(userMessage)
	for _, marker := range phrases {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// ensureEngineRunning auto-starts the correct local engine if the active provider is
// not yet running. Mutual exclusion stops the competing engine first.
func (s *Service) ensureEngineRunning(cfg config.RuntimeConfigSnapshot, provider agent.ProviderConfig) {
	if provider.Type == agent.ProviderAtlasEngine && s.engine != nil && !s.engine.IsRunning() {
		if s.mlxEngine != nil && s.mlxEngine.IsRunning() {
			logstore.Write("info", "Mutual exclusion: stopping MLX engine before starting llama.cpp", nil)
			_ = s.mlxEngine.Stop()
			if s.mlxRouterEngine != nil {
				_ = s.mlxRouterEngine.Stop()
			}
		}
		model := filepath.Base(cfg.SelectedAtlasEngineModel)
		if model == "" || model == "." {
			model = filepath.Base(cfg.SelectedAtlasEngineModelFast)
		}
		if model != "" && model != "." {
			port := cfg.AtlasEnginePort
			if port == 0 {
				port = 11985
			}
			ctxSize := cfg.AtlasEngineCtxSize
			if ctxSize <= 0 {
				ctxSize = 8192
			}
			logstore.Write("info", "Engine LM not running — auto-starting model", map[string]string{"model": model})
			if err := s.engine.Start(model, port, ctxSize, cfg.AtlasEngineKVCacheQuant, cfg.AtlasEngineDraftModel); err != nil {
				logstore.Write("warn", "Engine LM auto-start failed", map[string]string{"error": err.Error()})
			} else if err := s.engine.WaitUntilReady(port, 90*time.Second); err != nil {
				logstore.Write("warn", "Engine LM ready-wait timed out", map[string]string{"error": err.Error()})
			}
		}
	}

	if provider.Type == agent.ProviderAtlasMLX && s.mlxEngine != nil && !s.mlxEngine.IsRunning() {
		if s.engine != nil && s.engine.IsRunning() {
			logstore.Write("info", "Mutual exclusion: stopping llama.cpp engine before starting MLX-LM", nil)
			_ = s.engine.Stop()
			if s.routerEngine != nil {
				_ = s.routerEngine.Stop()
			}
		}
		model := filepath.Base(cfg.SelectedAtlasMLXModel)
		if model == "" || model == "." {
			logstore.Write("warn", "MLX Engine: no model configured — auto-start skipped. Select a model in Engine → MLX Engine settings.", nil)
		} else {
			port := cfg.AtlasMLXPort
			if port == 0 {
				port = 11990
			}
			ctxSize := cfg.AtlasMLXCtxSize
			if ctxSize <= 0 {
				ctxSize = 4096
			}
			logstore.Write("info", "MLX Engine not running — auto-starting model", map[string]string{"model": model})
			if err := s.mlxEngine.Start(model, port, ctxSize, "", ""); err != nil {
				logstore.Write("warn", "MLX Engine auto-start failed", map[string]string{"error": err.Error()})
			} else if err := s.mlxEngine.WaitUntilReady(port, 120*time.Second); err != nil {
				logstore.Write("warn", "MLX Engine ready-wait timed out", map[string]string{"error": err.Error()})
			} else if p, ok := s.mlxEngine.(mlxPrefiller); ok {
				go func(snapCfg config.RuntimeConfigSnapshot, snapPort int, snapModel string) {
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					defer cancel()
					warmPrompt := buildSystemPrompt(snapCfg, s.db, config.SupportDir(), "", "")
					p.PrefillPrompt(ctx, snapPort, snapModel, warmPrompt)
				}(cfg, port, filepath.Base(cfg.SelectedAtlasMLXModel))
			}
		}
	}
}

// resolveMaxIter returns the maximum agent iterations for the active provider.
func resolveMaxIter(cfg config.RuntimeConfigSnapshot) int {
	maxIter := cfg.MaxAgentIterations
	if maxIter <= 0 {
		maxIter = 5
	}
	if cfg.ActiveAIProvider == "lm_studio" && cfg.LMStudioMaxAgentIterations > 0 {
		maxIter = cfg.LMStudioMaxAgentIterations
	}
	if cfg.ActiveAIProvider == "ollama" && cfg.OllamaMaxAgentIterations > 0 {
		maxIter = cfg.OllamaMaxAgentIterations
	}
	if cfg.ActiveAIProvider == "atlas_engine" && cfg.AtlasEngineMaxAgentIterations > 0 {
		maxIter = cfg.AtlasEngineMaxAgentIterations
	}
	return maxIter
}

// buildTurnMessages assembles the OAI message list for the current turn:
// system prompt, compacted history, trimmed-history note, and the current
// user message with inline attachment content.
func (s *Service) buildTurnMessages(
	cfg config.RuntimeConfigSnapshot,
	req MessageRequest,
	history []storage.MessageRow,
	convID, userMsgID, capabilityPolicyBlock string,
) (oaiMessages []agent.OAIMessage, historyChars int, systemPrompt string) {
	limit := cfg.ConversationWindowLimit
	if limit == 0 {
		limit = 15
	}
	systemPrompt = buildSystemPrompt(cfg, s.db, config.SupportDir(), req.Message, capabilityPolicyBlock)
	oaiMessages = []agent.OAIMessage{
		{Role: "system", Content: systemPrompt},
	}
	start := 0
	if len(history) > limit {
		start = len(history) - limit
	}
	if start > 0 {
		if cfg.MemoryEnabled {
			if cached, ok := s.summaryCache[convID]; !ok || cached.trimCount < start {
				go func(trimmed []storage.MessageRow) {
					for _, m := range trimmed {
						if m.Role == "user" {
							memory.ExtractRegexOnly(cfg, m.Content, convID, s.db)
						}
					}
				}(history[:start])
			}
		}
		note := s.buildTrimmedHistoryNote(convID, start, history[:start], userMsgID)
		if note != "" {
			oaiMessages = append(oaiMessages, agent.OAIMessage{Role: "user", Content: note})
			oaiMessages = append(oaiMessages, agent.OAIMessage{Role: "assistant", Content: "Understood."})
		}
	}
	replayStart := start
	if shouldCompactHistory(req.Message) {
		replayStart = max(replayStart, len(history)-compactHistoryMessages)
		scanChars := 0
		for i := len(history) - 1; i >= replayStart; i-- {
			if history[i].ID == userMsgID {
				continue
			}
			scanChars += len(history[i].Content)
			if scanChars > compactHistoryChars {
				replayStart = i + 1
				break
			}
		}
		for i := len(history) - 1; i >= start; i-- {
			if history[i].Role == "user" && history[i].ID != userMsgID {
				if replayStart > i {
					replayStart = i
				}
				break
			}
		}
		if replayStart > start {
			note := s.buildTrimmedHistoryNote(convID, replayStart, history[:replayStart], userMsgID)
			if note != "" {
				oaiMessages = append(oaiMessages, agent.OAIMessage{Role: "user", Content: note})
				oaiMessages = append(oaiMessages, agent.OAIMessage{Role: "assistant", Content: "Understood."})
			}
		}
	}
	for _, m := range history[replayStart:] {
		if m.ID == userMsgID {
			continue
		}
		if m.Role == "user" || m.Role == "assistant" {
			oaiMessages = append(oaiMessages, agent.OAIMessage{
				Role:    m.Role,
				Content: m.Content,
			})
			historyChars += len(m.Content)
		}
	}
	oaiMessages = append(oaiMessages, agent.OAIMessage{
		Role:    "user",
		Content: buildUserContent(req.Message, req.Attachments),
	})
	return
}

// selectTurnTools selects the tool set for this turn based on ToolSelectionMode.
// Also auto-starts the local router engine when mode is "llm".
// Returns the ToolSelector (for loop upgrades), the initial tool list, and the resolved mode.
func (s *Service) selectTurnTools(
	ctx context.Context,
	cfg config.RuntimeConfigSnapshot,
	req MessageRequest,
	turn *turnContext,
	capabilityPlan capabilities.Analysis,
) (sel agent.ToolSelector, selectedTools []map[string]any, toolMode string) {
	toolMode = cfg.ToolSelectionMode
	if toolMode == "" {
		toolMode = "heuristic"
	}

	// LLM mode: ensure the router engine is running before selection.
	if toolMode == "llm" {
		switch agent.ProviderType(cfg.ActiveAIProvider) {
		case agent.ProviderAtlasMLX:
			if s.mlxRouterEngine != nil && !s.mlxRouterEngine.IsRunning() {
				routerModel := filepath.Base(cfg.AtlasMLXRouterModel)
				if routerModel != "" && routerModel != "." {
					port := cfg.AtlasMLXRouterPort
					if port == 0 {
						port = 11991
					}
					ctxSize := cfg.AtlasMLXCtxSize
					if ctxSize <= 0 {
						ctxSize = 4096
					}
					logstore.Write("info", "MLX tool router not running — auto-starting", map[string]string{"model": routerModel})
					if err := s.mlxRouterEngine.Start(routerModel, port, ctxSize, "", ""); err != nil {
						logstore.Write("warn", "MLX router auto-start failed", map[string]string{"error": err.Error()})
					} else {
						_ = s.mlxRouterEngine.WaitUntilReady(port, 90*time.Second)
					}
				}
			}
		default:
			if s.routerEngine != nil && !s.routerEngine.IsRunning() {
				routerModel := filepath.Base(cfg.AtlasEngineRouterModel)
				if routerModel != "" && routerModel != "." {
					port := cfg.AtlasEngineRouterPort
					if port == 0 {
						port = 11986
					}
					ctxSize := cfg.AtlasEngineCtxSize
					if ctxSize <= 0 {
						ctxSize = 8192
					}
					logstore.Write("info", "Tool router not running — auto-starting", map[string]string{"model": routerModel})
					if err := s.routerEngine.Start(routerModel, port, ctxSize, cfg.AtlasEngineKVCacheQuant, ""); err != nil {
						logstore.Write("warn", "Router auto-start failed", map[string]string{"error": err.Error()})
					} else {
						_ = s.routerEngine.WaitUntilReady(port, 90*time.Second)
					}
				}
			}
		}
	}

	sel = NewSelector(toolMode, req.ToolPolicy, ctx, cfg, turn, req.Message, s.registry)
	selectedTools = applyCapabilityPlanToolHints(s.registry, sel.Initial(), req.Message, capabilityPlan)

	// LLM mode: record activity after selection so the engine idle-timer resets.
	if toolMode == "llm" {
		if cfg.ActiveAIProvider == string(agent.ProviderAtlasMLX) && s.mlxRouterEngine != nil {
			s.mlxRouterEngine.RecordActivity()
		} else if s.routerEngine != nil {
			s.routerEngine.RecordActivity()
		}
	}

	if toolMode == "lazy" {
		logstore.Write("debug", "Tool selection: smart mode — compact router + request_tools",
			map[string]string{"mode": "lazy"})
	}
	return
}

// HandleMessage processes a message request end-to-end via the turn pipeline.
func (s *Service) HandleMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	state, err := s.pipeline.Run(ctx, req)
	if err != nil {
		return MessageResponse{}, err
	}
	return s.pipeline.BuildResponse(state), nil
}

// RegenerateMind builds a context-aware prompt from the current MIND.md,
// recent conversation history, and active memories, then asks the AI to
// produce an updated MIND.md. The old file is backed up to MIND.md.bak
// before the new content is written atomically.
func (s *Service) RegenerateMind(ctx context.Context) (string, error) {
	cfg := s.cfgStore.Load()
	provider, err := resolveProvider(cfg)
	if err != nil {
		return "", err
	}

	supportDir := config.SupportDir()

	// Read the current MIND.md.
	existing := ""
	if data, readErr := os.ReadFile(filepath.Join(supportDir, "MIND.md")); readErr == nil {
		existing = strings.TrimSpace(string(data))
	}

	// Read recent memories for grounding.
	memBlock := ""
	if mems, memErr := s.db.ListMemories(20, ""); memErr == nil && len(mems) > 0 {
		var sb strings.Builder
		sb.WriteString("## Current Memories\n")
		for _, m := range mems {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
		}
		memBlock = sb.String()
	}

	// Read recent conversation messages for context (last 30 across all conversations).
	convBlock := ""
	if convs, convErr := s.db.ListConversations(5); convErr == nil && len(convs) > 0 {
		var sb strings.Builder
		sb.WriteString("## Recent Conversation Excerpts\n")
		for _, conv := range convs {
			msgs, msgErr := s.db.ListMessages(conv.ID)
			if msgErr != nil || len(msgs) == 0 {
				continue
			}
			// Include last 6 messages per conversation.
			start := 0
			if len(msgs) > 6 {
				start = len(msgs) - 6
			}
			for _, msg := range msgs[start:] {
				role := msg.Role
				content := msg.Content
				if content == "" {
					continue
				}
				if len(content) > 300 {
					content = content[:300] + "…"
				}
				sb.WriteString(fmt.Sprintf("[%s] %s\n", role, content))
			}
			sb.WriteString("\n")
		}
		convBlock = sb.String()
	}

	// Build the upgrade prompt.
	var promptBuilder strings.Builder
	promptBuilder.WriteString("You are updating the Atlas operator MIND.md — the self-model and system prompt for an AI operator named Atlas.\n\n")
	promptBuilder.WriteString("Your job: rewrite MIND.md so it accurately reflects the current state of the user's project and working relationship. ")
	promptBuilder.WriteString("Keep what is still true, remove what is stale, and add what the conversation history reveals.\n\n")
	promptBuilder.WriteString("Rules:\n")
	promptBuilder.WriteString("- Output only the updated MIND.md content — no explanations, no markdown fences.\n")
	promptBuilder.WriteString("- Preserve the section structure (Who I Am, What Matters Right Now, Working Style, My Understanding of the User, Patterns I've Noticed, Active Theories, Our Story).\n")
	promptBuilder.WriteString("- Replace stale entries with observations grounded in the conversation history below.\n")
	promptBuilder.WriteString("- 'Today's Read' should reflect the most recent session, not old notes.\n\n")
	if existing != "" {
		promptBuilder.WriteString("---\n## Current MIND.md\n")
		promptBuilder.WriteString(existing)
		promptBuilder.WriteString("\n\n")
	}
	if memBlock != "" {
		promptBuilder.WriteString("---\n")
		promptBuilder.WriteString(memBlock)
		promptBuilder.WriteString("\n")
	}
	if convBlock != "" {
		promptBuilder.WriteString("---\n")
		promptBuilder.WriteString(convBlock)
	}

	messages := []agent.OAIMessage{
		{Role: "user", Content: promptBuilder.String()},
	}

	reply, _, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil {
		return "", fmt.Errorf("mind regeneration: %w", err)
	}

	contentStr, _ := reply.Content.(string)
	content := strings.TrimSpace(contentStr)
	if content == "" {
		return "", fmt.Errorf("mind regeneration: AI returned empty content")
	}

	if err := os.MkdirAll(supportDir, 0o700); err != nil {
		return "", err
	}

	mindPath := filepath.Join(supportDir, "MIND.md")
	bakPath := filepath.Join(supportDir, "MIND.md.bak")

	// Back up existing file before overwriting.
	if existing != "" {
		_ = os.WriteFile(bakPath, []byte(existing), 0o600)
	}

	// Write atomically via temp → rename.
	tmp, err := os.CreateTemp(supportDir, "MIND-*.md.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", err
	}
	tmp.Chmod(0o600) //nolint:errcheck
	tmp.Close()
	if err := os.Rename(tmpPath, mindPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	return content, nil
}

// ResolveProvider returns the active AI ProviderConfig from config + Keychain.
// Exported so that internal packages (e.g. forge) can reuse provider resolution
// without duplicating Keychain reading logic.
func (s *Service) ResolveProvider() (agent.ProviderConfig, error) {
	cfg := s.cfgStore.Load()
	return resolveProvider(cfg)
}

// ResolveFastProvider returns the fast-model ProviderConfig from config + Keychain.
// Used by background pipelines (forge research, MIND reflection, SKILLS learning).
// Falls back to the primary provider when no fast model is configured.
func (s *Service) ResolveFastProvider() (agent.ProviderConfig, error) {
	cfg := s.cfgStore.Load()
	return resolveFastProvider(cfg)
}

// Resume is called after an approval is resolved. It delegates to the pipeline.
func (s *Service) Resume(toolCallID string, approved bool) {
	s.pipeline.Resume(context.Background(), toolCallID, approved)
}

// recordTokenUsage computes cost and persists a token usage event for one turn.
// Non-fatal — a failure here never surfaces to the user.
func (s *Service) recordTokenUsage(convID string, provider agent.ProviderConfig, usage agent.TokenUsage) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return
	}
	// Normalize the atlas_engine router slot: background tasks (reflection,
	// memory extraction, dream cycle) run on the router port with model="router",
	// but that is the same physical GGUF loaded on a different port — not a
	// separate model. Fold it into the primary model so usage groups correctly.
	if provider.Type == agent.ProviderAtlasEngine && provider.Model == "router" {
		cfg := s.cfgStore.Load()
		if primary := filepath.Base(cfg.SelectedAtlasEngineModel); primary != "" && primary != "." {
			provider.Model = primary
		}
	}
	inputCost, outputCost, known := storage.ComputeCost(
		string(provider.Type), provider.Model,
		usage.InputTokens, usage.OutputTokens,
	)
	if !known {
		logstore.Write("warn",
			fmt.Sprintf("token usage: unknown pricing for model %q — cost recorded as $0", provider.Model),
			map[string]string{"provider": string(provider.Type)})
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.db.RecordTokenUsage(
		newUUID(), convID, string(provider.Type), provider.Model,
		usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens,
		inputCost, outputCost, now,
	); err != nil {
		logstore.Write("warn", "token usage: failed to persist: "+err.Error(), nil)
	}
}

// InjectAssistantMessage delivers text as an assistant message into the web
// chat UI without running the agent loop. It persists the message to the most
// recent conversation and emits SSE events so live clients receive it instantly.
// Used by automations and workflows to deliver results to the web chat target.
func (s *Service) InjectAssistantMessage(text string) error {
	convs, err := s.db.ListConversationSummaries(1)
	if err != nil {
		return fmt.Errorf("webchat inject: list conversations: %w", err)
	}
	if len(convs) == 0 {
		return fmt.Errorf("webchat inject: no conversations exist yet")
	}
	convID := convs[0].ID
	msgID := newUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	turnID := newUUID()
	if err := s.db.SaveMessage(msgID, convID, "assistant", text, now); err != nil {
		return fmt.Errorf("webchat inject: save message: %w", err)
	}
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_started", Role: "assistant", ConversationID: convID, TurnID: turnID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_delta", Content: text, Role: "assistant", ConversationID: convID, TurnID: turnID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_done", Role: "assistant", ConversationID: convID, TurnID: turnID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "done", Status: "completed", ConversationID: convID, TurnID: turnID})
	logstore.Write("info", "Webchat inject: delivered automation result", map[string]string{"conv": convID[:8]})
	return nil
}
