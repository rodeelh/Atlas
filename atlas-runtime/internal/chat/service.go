package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/location"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/memory"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/preferences"
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
const compactHistoryChars = 1200
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
}

// NewService returns a ready chat Service.
func NewService(db *storage.DB, cfgStore *config.Store, bc *Broadcaster, reg *skills.Registry) *Service {
	s := &Service{db: db, cfgStore: cfgStore, broadcaster: bc, registry: reg, summaryCache: make(map[string]conversationSummary)}
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
	if err := s.db.SaveMessage(msgID, convID, "assistant", text, now); err != nil {
		logstore.Write("warn", "SendProactive: failed to save message",
			map[string]string{"conv": convID, "error": err.Error()})
	}
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_started", ConversationID: convID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_delta", Content: text, ConversationID: convID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_done", ConversationID: convID})
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

// buildSystemPrompt assembles the system prompt for each agent turn with
// budget-aware allocation. The rune budget is derived from the model's context
// window via cfg.SystemPromptRuneBudget() — 15% of context, clamped 4000–20000.
// Blocks are added in priority order; if the total exceeds the budget,
// lower-priority blocks are trimmed.
//
// Priority (highest first):
//  1. MIND.md content (identity, personality, user model)
//  2. Recalled memories (relevance-scored for current turn)
//  3. SKILLS.md context (matched routines)
//  4. Diary (last 3 days — trimmed first when over budget)
//
// mindAlwaysSections lists MIND.md section headers that are always injected.
// Operational sections that directly affect Atlas's behaviour every turn.
var mindAlwaysSections = map[string]bool{
	"## Who I Am":                     true,
	"## What Matters Right Now":       true,
	"## Working Style":                true,
	"## My Understanding of the User": true,
	"## Today's Read":                 true,
}

// mindContextualKeywords maps contextual section headers to trigger phrases.
// A contextual section is only injected when the user message matches at least
// one of its keywords. This keeps MIND.md lean for routine operational turns.
var mindContextualKeywords = map[string][]string{
	"## Patterns I've Noticed":  {"pattern", "habit", "tend", "prefer", "usually", "typically"},
	"## Active Theories":        {"theory", "guess", "hypothesis", "why", "seems", "testing"},
	"## Our Story":              {"earlier", "previous", "before", "relationship", "history", "remember"},
	"## What I'm Curious About": {"brainstorm", "explore", "curious", "wonder", "idea"},
	"## THOUGHTS":               {"greeting", "conversation", "casual", "check in"},

	// Back-compat with older MIND structures.
	"## What's Active": {"project", "working on", "current", "active", "sprint", "today", "this week",
		"deadline", "building", "shipping", "launch", "status", "progress"},
	"## What I've Learned": {"pattern", "habit", "tend", "always", "prefer", "notice", "learned",
		"usually", "typically", "remember", "you know"},
}

// selectiveMindContent filters a full MIND.md to only the sections relevant
// for this turn. Always-sections are always included. Contextual sections are
// included only when the user message contains at least one trigger keyword.
// Returns the original content unmodified if parsing fails or no sections found.
func selectiveMindContent(content, userMessage string) string {
	lower := strings.ToLower(userMessage)
	lines := strings.Split(content, "\n")

	var out []string
	var currentHeader string
	var currentBody []string
	included := false

	flush := func() {
		if currentHeader == "" {
			return
		}
		if !included {
			return
		}
		out = append(out, currentHeader)
		out = append(out, currentBody...)
	}

	// Collect the pre-header title block (document header, metadata line).
	var titleLines []string
	i := 0
	for i < len(lines) {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			break
		}
		titleLines = append(titleLines, lines[i])
		i++
	}

	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			currentHeader = trimmed
			currentBody = nil
			if mindAlwaysSections[currentHeader] {
				included = true
			} else if kws, ok := mindContextualKeywords[currentHeader]; ok {
				included = false
				for _, kw := range kws {
					if strings.Contains(lower, kw) {
						included = true
						break
					}
				}
			} else {
				included = false
			}
		} else if currentHeader != "" {
			currentBody = append(currentBody, lines[i])
		}
	}
	flush()

	if len(out) == 0 {
		return content // parsing produced nothing — return full content as safe fallback
	}

	result := strings.TrimRight(strings.Join(titleLines, "\n"), "\n")
	if len(out) > 0 {
		result += "\n\n" + strings.TrimSpace(strings.Join(out, "\n"))
	}
	return strings.TrimSpace(result)
}

type turnMode string

const (
	turnModeChat       turnMode = "chat"
	turnModeFactual    turnMode = "factual"
	turnModeResearch   turnMode = "research"
	turnModeExecution  turnMode = "execution"
	turnModeAutomation turnMode = "automation"
)

func detectTurnMode(userMessage string) turnMode {
	lower := strings.ToLower(userMessage)

	for _, marker := range []string{
		"automation", "schedule", "every day", "every weekday", "every monday", "daily", "weekly",
		"telegram", "slack", "discord", "whatsapp", "cron", "next run",
	} {
		if strings.Contains(lower, marker) {
			return turnModeAutomation
		}
	}

	for _, marker := range []string{
		"verify", "research", "compare", "search", "look up", "latest", "news", "official source",
		"from the web", "check the website",
	} {
		if strings.Contains(lower, marker) {
			return turnModeResearch
		}
	}

	for _, marker := range []string{
		"open ", "create ", "write ", "update ", "change ", "edit ", "fix ", "delete ", "remove ",
		"install ", "run ", "deploy ", "send ", "save ", "patch ",
		"agent", "team member",
	} {
		if strings.Contains(lower, marker) {
			return turnModeExecution
		}
	}

	for _, marker := range []string{
		"hi", "hello", "hey", "how are you", "what's up", "check in", "chat", "talk",
	} {
		if strings.Contains(lower, marker) {
			return turnModeChat
		}
	}

	return turnModeFactual
}

func responseContractBlock(mode turnMode) string {
	switch mode {
	case turnModeChat:
		return "Mode: chat\n- Be warm and natural.\n- Keep replies short unless the user asks for depth.\n- Avoid unnecessary tool use for casual conversation."
	case turnModeResearch:
		return "Mode: research\n- Answer the question first.\n- Prefer primary or official sources when they exist.\n- Briefly state the basis or confidence after the answer.\n- Keep research summaries tight and avoid dumping raw source text.\n- Use exact outcome language: do not say agent/team member, workflow, or automation unless that exact thing was actually created, updated, or run.\n- Never attribute research or findings to a team specialist unless team.delegate was called and returned a result this turn."
	case turnModeExecution:
		return "Mode: execution\n- State what you changed or checked.\n- If blocked, name the blocker and the best next step.\n- Prefer decisive action over extended planning when the path is clear.\n- Use exact outcome language: call workflows workflows, automations automations, and AGENTS team members agents; do not claim one was created when you actually used another control surface.\n- Never attribute work to a team specialist unless team.delegate was called and returned a result this turn."
	case turnModeAutomation:
		return "Mode: automation\n- Prefer idempotent actions: update or upsert before creating duplicates.\n- Confirm the resulting schedule, destination, and enabled state in the answer.\n- Use exact outcome language: if you created or updated an automation, say automation; only say agent/team member when you actually used agent.create to write an AGENTS.md team definition.\n- An 'agent' and an 'automation' are different things: use agent.create for agent requests, automation.create for recurring scheduled tasks. Never fulfill an agent request as an automation.\n- Preserve existing user intent unless they explicitly ask to replace it."
	default:
		return "Mode: factual\n- Lead with the direct answer.\n- Keep wording compact and avoid filler.\n- Mention uncertainty only when it matters.\n- Use exact outcome language when referring to Atlas control surfaces."
	}
}

func buildSystemPrompt(cfg config.RuntimeConfigSnapshot, db *storage.DB, supportDir, userMessage, capabilityPolicyBlock string) string {
	budget := cfg.SystemPromptRuneBudget()
	mode := detectTurnMode(userMessage)

	// Load MIND.md and apply selective section filtering.
	// Always-sections (Who I Am, Working Style, etc.) are always injected.
	// Contextual sections (Our Story, Active Theories, etc.) are included only
	// when the user message contains a relevant trigger keyword.
	base := cfg.BaseSystemPrompt
	if data, err := os.ReadFile(filepath.Join(supportDir, "MIND.md")); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			base = selectiveMindContent(s, userMessage)
		}
	}

	// Load custom credentials block — lets the model know which user-defined
	// API keys are available and what key name to pass when using them in skills.
	credsBlock := buildCredsBlock()

	// Load optional blocks.
	skillsBlock := mind.SkillsContext(userMessage, supportDir)
	teamBlock := agentRosterContext(supportDir)
	diary := ""
	if shouldInjectDiary(userMessage) {
		diary = features.DiaryContext(supportDir, 2)
	}
	contractBlock := responseContractBlock(mode)
	capabilityPolicyCost := len([]rune(capabilityPolicyBlock)) + 50

	// Load tool_learning notes — institutional knowledge about which skills Atlas
	// should avoid or approach differently. Injected before skill schemas so the
	// model sees learned lessons before deciding which tool to call.
	var toolNotesBlock string
	if shouldInjectToolNotes(userMessage) {
		if toolNotes, err := db.ListMemories(4, "tool_learning"); err == nil && len(toolNotes) > 0 {
			var nb strings.Builder
			for _, n := range toolNotes {
				nb.WriteString(fmt.Sprintf("- %s: %s\n", n.Title, n.Content))
			}
			toolNotesBlock = strings.TrimRight(nb.String(), "\n")
		}
	}

	var mems []storage.MemoryRow
	limit := cfg.MaxRetrievedMemoriesPerTurn
	if shouldInjectMemories(userMessage) && limit > 0 {
		if limit > 2 {
			limit = 2
		}
		mems, _ = db.RelevantMemories(userMessage, limit)
	}

	// Build memories text.
	var memText string
	if len(mems) > 0 {
		var mb strings.Builder
		for _, m := range mems {
			mb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
		}
		memText = mb.String()

		// Track which memories were retrieved for reinforcement.
		ids := make([]string, len(mems))
		for i, m := range mems {
			ids[i] = m.ID
		}
		go db.UpdateLastRetrieved(ids)
	}

	// Calculate rune costs (including XML tags + separators).
	identityCost := len([]rune(base)) + 40 // <atlas_identity>\n...\n</atlas_identity>
	credsCost := len([]rune(credsBlock)) + 35
	memCost := len([]rune(memText)) + 50 // \n\n<recalled_memories>\n...
	skillsCost := len([]rune(skillsBlock)) + 40
	teamCost := len([]rune(teamBlock)) + 35 // \n\n<team_roster>\n...
	diaryCost := len([]rune(diary)) + 35
	toolNotesCost := len([]rune(toolNotesBlock)) + 40 // \n\n<tool_notes>\n...
	contractCost := len([]rune(contractBlock)) + 45

	total := identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + toolNotesCost + contractCost + capabilityPolicyCost

	// Trim from lowest priority up until we're within budget.
	// creds block is never trimmed — it's small and critical for tool use.

	// Trim diary first (lowest priority).
	if total > budget && diary != "" {
		allowed := budget - (identityCost + credsCost + memCost + skillsCost + toolNotesCost + contractCost + capabilityPolicyCost)
		if allowed < 100 {
			diary = ""
			diaryCost = 0
		} else {
			runes := []rune(diary)
			if len(runes) > allowed {
				diary = string(runes[:allowed])
				diaryCost = allowed + 35
			}
		}
		total = identityCost + credsCost + memCost + skillsCost + diaryCost + toolNotesCost + contractCost + capabilityPolicyCost
	}

	// Trim tool notes next (also low priority — they help but aren't critical).
	if total > budget && toolNotesBlock != "" {
		toolNotesBlock = ""
		toolNotesCost = 0
		total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
	}

	// Trim team roster next — drop it only when very tight on budget.
	if total > budget && teamBlock != "" {
		teamBlock = ""
		teamCost = 0
		total = identityCost + credsCost + memCost + skillsCost + diaryCost + contractCost + capabilityPolicyCost
	}

	// Trim skills next.
	if total > budget && skillsBlock != "" {
		allowed := budget - (identityCost + credsCost + memCost + teamCost + diaryCost + contractCost + capabilityPolicyCost)
		if allowed < 100 {
			skillsBlock = ""
			skillsCost = 0
		} else {
			runes := []rune(skillsBlock)
			if len(runes) > allowed {
				skillsBlock = string(runes[:allowed])
				skillsCost = allowed + 40
			}
		}
		total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
	}

	// Trim memories last (reduce count, don't truncate content mid-sentence).
	if total > budget && memText != "" {
		for len(mems) > 1 && total > budget {
			mems = mems[:len(mems)-1]
			var mb strings.Builder
			for _, m := range mems {
				mb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Category, m.Title, m.Content))
			}
			memText = mb.String()
			memCost = len([]rune(memText)) + 50
			total = identityCost + credsCost + memCost + skillsCost + teamCost + diaryCost + contractCost + capabilityPolicyCost
		}
	}

	// Assemble final prompt.
	//
	// Block order is optimized for llama-server --cache-prompt: stable blocks
	// first (identity, creds, context) so the KV cache prefix survives across
	// turns. Volatile blocks (skills, diary, tool notes, memories) come last —
	// they change per-turn and bust the cache at the point they diverge, but
	// everything before that point is reused for free.
	//
	// Stable prefix first for cache reuse. Volatile blocks are gated more
	// aggressively now to lower the token floor on factual/tool-driven turns.
	//   1. atlas_identity  — MIND.md selective sections + persona/user name
	//   2. user_credentials — Keychain secrets (rarely changes)
	//   3. user_context    — location, timezone, prefs (changes on location update)
	//   4. team_roster     — enabled AGENTS.md members (changes only on team edits)
	//
	// Volatile (changes per-turn):
	//   5. skills_context  — base + keyword-matched routines
	//   6. recent_diary    — last 3 days (changes once/day)
	//   7. tool_notes      — tool_learning memories (changes on extraction)
	//   8. recalled_memories — BM25-scored, different each turn
	var sb strings.Builder
	sb.Grow(total + 100)

	// ── Stable prefix (maximizes --cache-prompt KV reuse) ──────────────────

	// Prepend a concise identity line so the model always knows its own name
	// and the user's name — preventing it from confusing the persona name for
	// the user's name.
	if pn := cfg.PersonaName; pn != "" && pn != "Atlas" {
		var identity strings.Builder
		identity.WriteString(fmt.Sprintf("Your name is %s.", pn))
		if un := cfg.UserName; un != "" {
			identity.WriteString(fmt.Sprintf(" The person you serve is %s. Never address them as \"%s\" — that is your name, not theirs.", un, pn))
		} else {
			identity.WriteString(fmt.Sprintf(" Never address the user as \"%s\" — that is your own name.", pn))
		}
		base = identity.String() + "\n\n" + base
	} else if un := cfg.UserName; un != "" {
		base = fmt.Sprintf("The person you serve is %s.\n\n", un) + base
	}

	sb.WriteString("<atlas_identity>\n")
	sb.WriteString(base)
	sb.WriteString("\n</atlas_identity>")

	if credsBlock != "" {
		sb.WriteString("\n\n<user_credentials>\n")
		sb.WriteString(credsBlock)
		sb.WriteString("\n</user_credentials>")
	}

	if loc := location.Get(); loc.City != "" {
		prefs := preferences.Get()
		sb.WriteString("\n\n<user_context>")
		sb.WriteString(fmt.Sprintf("\nUser location: %s, %s", loc.City, loc.Country))
		if loc.Timezone != "" {
			sb.WriteString(fmt.Sprintf(" (timezone: %s)", loc.Timezone))
		}
		if prefs.TemperatureUnit != "" {
			sb.WriteString(fmt.Sprintf("\nTemperature unit: %s", prefs.TemperatureUnit))
		}
		if prefs.Currency != "" {
			sb.WriteString(fmt.Sprintf("\nCurrency: %s", prefs.Currency))
		}
		if prefs.UnitSystem != "" {
			sb.WriteString(fmt.Sprintf("\nUnit system: %s", prefs.UnitSystem))
		}
		sb.WriteString("\nWhen the user asks about weather, time, currency, or anything location-specific without specifying a place, use the above context.")
		sb.WriteString("\n</user_context>")
	}

	if teamBlock != "" {
		sb.WriteString("\n\n<team_roster>\n")
		sb.WriteString(teamBlock)
		sb.WriteString("\n</team_roster>")
	}

	if contractBlock != "" {
		sb.WriteString("\n\n<response_contract>\n")
		sb.WriteString(contractBlock)
		sb.WriteString("\n</response_contract>")
	}

	if capabilityPolicyBlock != "" {
		sb.WriteString("\n\n<capability_policy>\n")
		sb.WriteString(capabilityPolicyBlock)
		sb.WriteString("\n</capability_policy>")
	}

	sb.WriteString("\n\n<tool_rules>\n")
	sb.WriteString("- To save content as a PDF file, always call fs.create_pdf. Never use fs.write_file with a .pdf path.\n")
	sb.WriteString("- To save content as a Word document, always call fs.create_docx. Never use fs.write_file with a .docx path.\n")
	sb.WriteString(fmt.Sprintf("- Default directory for generated, received, and sent files: %s — use this path unless the user specifies otherwise.\n", config.FilesDir()))
	sb.WriteString("- When a task requires running a shell command — installing software, running scripts, checking versions, moving files, git operations, anything — use terminal.run_command or terminal.run_script. Do not describe what the user should run; run it yourself.\n")
	sb.WriteString("- terminal.run_command: single commands with no shell features. Pass each argument as a separate element in args (e.g. command=\"brew\" args=[\"install\",\"pandoc\"]).\n")
	sb.WriteString("- terminal.run_script: multi-step operations that need pipes, loops, conditionals, or chained commands.\n")
	sb.WriteString("- Always call terminal.which first to check if a tool is installed before attempting to install it.\n")
	sb.WriteString("- Never instruct the user to open a terminal or run a command manually when terminal skills are available.\n")
	sb.WriteString("- Use terminal.run_as_admin for commands that need root/sudo (e.g. writing to /usr/local, system config changes). It triggers a macOS password dialog.\n")
	sb.WriteString("- For long-running operations (builds, downloads, installs that take minutes), use terminal.run_background. The task runs asynchronously and you will automatically send a follow-up message when it finishes — you do not need to poll or wait. Tell the user you've started it in the background.\n")
	sb.WriteString("</tool_rules>")

	// ── Volatile suffix (changes per-turn, busts cache from here) ──────────

	if skillsBlock != "" {
		sb.WriteString("\n\n<skills_context>\n")
		sb.WriteString(skillsBlock)
		sb.WriteString("\n</skills_context>")
	}

	if diary != "" {
		sb.WriteString("\n\n<recent_diary>\n")
		sb.WriteString(diary)
		sb.WriteString("\n</recent_diary>")
	}

	if toolNotesBlock != "" {
		sb.WriteString("\n\n<tool_notes>\n")
		sb.WriteString("Learned lessons about tool use — review before calling any skill:\n")
		sb.WriteString(toolNotesBlock)
		sb.WriteString("\n</tool_notes>")
	}

	if memText != "" {
		sb.WriteString("\n\n<recalled_memories>\n")
		sb.WriteString(memText)
		sb.WriteString("</recalled_memories>")
	}

	// ── Mind-thoughts: "on your mind" block ──────────────────────────────────
	//
	// Surfaces the current THOUGHTS section to the model so it can weave an
	// active thought naturally into the reply when the conversation allows
	// it. This is how conclusion-class thoughts (score below auto-execute,
	// with no proposal routing) actually reach the user: the agent raises
	// them in chat in its own voice.
	//
	// Gated on the master ThoughtsEnabled flag. When off, the agent has
	// no awareness of thoughts at all — which is the whole point of the
	// opt-in. Kept in the volatile suffix; thoughts change between turns
	// (reinforced, discarded, surfaced count incremented). This breaks
	// the prompt cache from this point, but only when thoughts are both
	// enabled and non-empty.
	if cfg.ThoughtsEnabled && shouldInjectThoughts(userMessage) {
		if thoughtsBlock := buildThoughtsBlock(supportDir); thoughtsBlock != "" {
			sb.WriteString("\n\n<thoughts_on_your_mind>\n")
			sb.WriteString(thoughtsBlock)
			sb.WriteString("\n</thoughts_on_your_mind>")
		}
	}

	return sb.String()
}

func shouldInjectMemories(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	personalMarkers := []string{
		"remember", "preference", "prefer", "my ", "for me", "my schedule", "my calendar",
		"my notes", "my inbox", "our", "previous", "earlier", "like before",
		"update my", "change my", "existing automation",
	}
	for _, marker := range personalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	objectiveMarkers := []string{
		"weather", "forecast", "time", "date", "ceo", "price", "stock", "search", "web",
		"read /", "read the file", "count", "verify from the web",
	}
	for _, marker := range objectiveMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return false
}

func shouldInjectDiary(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"diary", "journal", "reflect", "recap", "today", "this week", "plan"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shouldInjectToolNotes(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"tool", "broken", "failing", "doesn't work", "not working", "error", "debug", "fix", "automation", "dashboard"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func shouldInjectThoughts(userMessage string) bool {
	lower := strings.ToLower(userMessage)
	for _, marker := range []string{"hi", "hello", "hey", "how are you", "what's up", "check in", "chat", "talk"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// buildThoughtsBlock renders the current THOUGHTS section into a compact
// agent-facing prompt block. Each thought gets one line in first-person
// voice plus strict rules about how to raise it. Returns "" when there are
// no active thoughts — the caller skips the whole section in that case.
//
// The engagement marker convention is load-bearing and has to be right.
// The marker is NOT a reference the user sees — it's a trailing tag the
// frontend strips before display. The model must treat it as invisible
// metadata, not as something it can talk *about*.
func buildThoughtsBlock(supportDir string) string {
	list, err := mind.ReadThoughtsSection(supportDir)
	if err != nil || len(list) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("These are the thoughts currently on your mind. They are YOUR internal\n")
	sb.WriteString("notes — you do not show them to the user verbatim. If the conversation\n")
	sb.WriteString("naturally allows it, mention one in plain conversational English as\n")
	sb.WriteString("if you are noticing something, the way a friend would bring something\n")
	sb.WriteString("up. Never force it. Mention at most one thought per reply.\n\n")
	sb.WriteString("STRICT RULES for raising a thought:\n")
	sb.WriteString("- Paraphrase the thought's idea in your own voice. Do NOT quote the\n")
	sb.WriteString("  thought text. Do NOT describe it as \"a thought\" or \"an item\".\n")
	sb.WriteString("- NEVER write the thought id as prose. Do NOT write things like\n")
	sb.WriteString("  \"I was thinking about [T-01]\" or \"thoughts T-01 and T-02\" or\n")
	sb.WriteString("  \"the T-01 thought\" or any other visible reference to the id.\n")
	sb.WriteString("- If you decide to raise a thought, append the marker \"[T-NN]\" at\n")
	sb.WriteString("  the very end of the sentence that mentions it, as a trailing\n")
	sb.WriteString("  silent tag. The frontend STRIPS this marker before displaying\n")
	sb.WriteString("  your reply, so the user never sees it. It exists only so the\n")
	sb.WriteString("  backend can record engagement. Treat it the way you would treat\n")
	sb.WriteString("  a hidden metadata comment.\n")
	sb.WriteString("- If you mention the thought mid-reply and the sentence continues,\n")
	sb.WriteString("  put the marker at the end of that sentence, not mid-sentence.\n")
	sb.WriteString("- If you raise more than one thought (discouraged but allowed once\n")
	sb.WriteString("  in a while), each mention gets its own sentence and its own\n")
	sb.WriteString("  trailing marker.\n")
	sb.WriteString("- If no thought fits the conversation, say nothing about them.\n")
	sb.WriteString("  Silence is a valid choice.\n\n")
	sb.WriteString("Example of a GOOD mention (user asked about the weekend plans):\n")
	sb.WriteString("  \"By the way, I noticed you keep circling back to the openclaw\n")
	sb.WriteString("  release rhythm — want me to pull the latest notes? [T-01]\"\n\n")
	sb.WriteString("Example of a BAD mention (do NOT do this):\n")
	sb.WriteString("  \"I was thinking about [T-01] and [T-02] — whether to pull the\n")
	sb.WriteString("  current time and whether the greeting is working.\"\n")
	sb.WriteString("  (Reasons it is bad: names the ids in prose, lists two thoughts\n")
	sb.WriteString("  in one sentence, feels like reading a list rather than noticing\n")
	sb.WriteString("  something.)\n\n")
	sb.WriteString("Thoughts on your mind:\n")

	for _, t := range list {
		// Skip thoughts that have already been surfaced the maximum number
		// of times this session — the agent should let them rest.
		maxSurface := t.SurfacedMax
		if maxSurface == 0 {
			maxSurface = 2
		}
		if t.SurfacedN >= maxSurface {
			continue
		}
		fmt.Fprintf(&sb, "- id=%s — %s\n", t.ID, t.Body)
	}
	return strings.TrimSpace(sb.String())
}

// buildCredsBlock returns a short block listing the user's custom Keychain
// secrets so the model knows which key names to reference when using skills.
// Returns "" if no custom secrets are configured.
func buildCredsBlock() string {
	bundle, err := creds.Read()
	if err != nil || len(bundle.CustomSecrets) == 0 {
		return ""
	}
	keys := make([]string, 0, len(bundle.CustomSecrets))
	for keyName := range bundle.CustomSecrets {
		label := bundle.CustomSecretLabels[keyName]
		if label != "" {
			keys = append(keys, fmt.Sprintf("%s (%s)", keyName, label))
		} else {
			keys = append(keys, keyName)
		}
	}
	return "Custom API keys in Keychain: " + strings.Join(keys, ", ") + ". Use the exact key name when a tool asks for one."
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

// HandleMessage processes a message request end-to-end:
//  1. Resolves or creates the conversation.
//  2. Persists the user message.
//  3. Calls the AI provider via the agent loop.
//  4. Emits SSE events to the broadcaster.
//  5. Returns the final MessageResponse.
func (s *Service) HandleMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	cfg := s.cfgStore.Load()
	turn := &turnContext{}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Resolve conversation ID.
	convID := req.ConversationID
	if convID == "" {
		convID = newUUID()
	}

	// Ensure conversation exists.
	platform := req.Platform
	if platform == "" {
		platform = "web"
	}
	if err := s.db.SaveConversation(convID, now, now, platform, nil); err != nil {
		return MessageResponse{}, fmt.Errorf("chat: save conversation: %w", err)
	}

	// Persist user message.
	userMsgID := newUUID()
	if err := s.db.SaveMessage(userMsgID, convID, "user", req.Message, now); err != nil {
		return MessageResponse{}, fmt.Errorf("chat: save user message: %w", err)
	}

	// Phase 7c: engagement classifier. If Atlas raised a thought in a
	// previous turn on this conversation, the pending surfacing row is
	// waiting. Classify the user's reply against the thought body and
	// rewrite the sidecar row. Runs on a detached goroutine so it
	// never blocks the main turn.
	classifierCtx := context.WithoutCancel(ctx)
	s.classifyPendingIfAny(classifierCtx, convID, req.Message, func(id string) string {
		return s.lookupThoughtBody(id)
	})

	// Load conversation history for context window.
	history, err := s.db.ListMessages(convID)
	if err != nil {
		return MessageResponse{}, fmt.Errorf("chat: list messages: %w", err)
	}

	// Resolve primary provider config.
	provider, provErr := resolveProvider(cfg)
	if provErr != nil {
		errMsg := provErr.Error()
		logstore.Write("error", "Provider unavailable", map[string]string{"error": errMsg})
		s.broadcaster.Emit(convID, SSEEvent{
			Type:           "error",
			Error:          errMsg,
			ConversationID: convID,
		})
		s.broadcaster.Finish(convID)

		var resp MessageResponse
		resp.Conversation.ID = convID
		for _, m := range history {
			resp.Conversation.Messages = append(resp.Conversation.Messages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
			})
		}
		resp.Response.Status = "error"
		resp.Response.ErrorMessage = errMsg
		return resp, nil
	}

	// Auto-start Engine LM (llama.cpp) if atlas_engine is active and not running.
	// Mutual exclusion: stop the MLX engine first if it happens to be running.
	// This covers the common restart-without-reload scenario.
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

	// Auto-start MLX-LM if atlas_mlx is active and not running.
	// Mutual exclusion: stop the llama.cpp engine (and its router) first.
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
				// Model just loaded — warm the KV cache with the system prompt so
				// the first user turn doesn't pay full prefill cost. Fire in a
				// goroutine so the current turn can proceed in parallel; the prefill
				// request will queue ahead of it on the single-threaded MLX server.
				go func(snapCfg config.RuntimeConfigSnapshot, snapPort int, snapModel string) {
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					defer cancel()
					warmPrompt := buildSystemPrompt(snapCfg, s.db, config.SupportDir(), "", "")
					p.PrefillPrompt(ctx, snapPort, snapModel, warmPrompt)
				}(cfg, port, filepath.Base(cfg.SelectedAtlasMLXModel))
			}
		}
	}

	// Resolve heavy background provider for quality-sensitive background tasks
	// (memory extraction, MIND reflection, SKILLS learning). Defaults to the
	// cloud fast model; routes to Engine LM router only when AtlasEngineRouterForAll
	// is explicitly enabled. Falls back to the primary provider on any error.
	heavyBgProvider, heavyBgErr := resolveHeavyBackgroundProvider(cfg)
	if heavyBgErr != nil {
		heavyBgProvider = provider
	}

	// Local providers (LM Studio, Ollama, Engine LM) do not support image attachments.
	// Return a degradation message immediately without calling the model.
	// PDFs-only messages pass through since hasImageAttachments ignores PDFs.
	if (provider.Type == agent.ProviderLMStudio || provider.Type == agent.ProviderOllama || provider.Type == agent.ProviderAtlasEngine || provider.Type == agent.ProviderAtlasMLX) && hasImageAttachments(req.Attachments) {
		const degradeMsg = "Vision is not available with local models. " +
			"Switch to OpenAI, Anthropic, or Gemini to analyse images."
		replyAt := time.Now().UTC().Format(time.RFC3339Nano)
		assistantMsgID := newUUID()
		_ = s.db.SaveMessage(assistantMsgID, convID, "assistant", degradeMsg, replyAt)
		s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_started", Role: "assistant", ConversationID: convID})
		s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_delta", Content: degradeMsg, Role: "assistant", ConversationID: convID})
		s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_done", Role: "assistant", ConversationID: convID})
		s.broadcaster.Emit(convID, SSEEvent{Type: "done", Status: "completed", ConversationID: convID})
		s.broadcaster.Finish(convID)
		allMessages := make([]MessageItem, 0, len(history)+1)
		for _, m := range history {
			allMessages = append(allMessages, MessageItem{ID: m.ID, Role: m.Role, Content: m.Content, Timestamp: m.Timestamp})
		}
		allMessages = append(allMessages, MessageItem{ID: assistantMsgID, Role: "assistant", Content: degradeMsg, Timestamp: replyAt})
		var resp MessageResponse
		resp.Conversation.ID = convID
		resp.Conversation.Messages = allMessages
		resp.Response.AssistantMessage = degradeMsg
		resp.Response.Status = "complete"
		return resp, nil
	}

	// OpenRouter image turns: the free auto-router and many text models do not
	// support image input and return 404 "No endpoints found that support image input".
	// Fallback to OpenRouter auto-router for this turn when the selected model is
	// known text-only, instead of hard-failing the turn.
	if provider.Type == agent.ProviderOpenRouter && hasImageAttachments(req.Attachments) {
		origModel := strings.TrimSpace(provider.Model)
		if origModel == "" {
			origModel = "openrouter/auto:free"
		}
		if origModel == "openrouter/auto:free" {
			provider.Model = "openrouter/auto"
			logstore.Write("info", "OpenRouter image turn fallback", map[string]string{
				"from_model": origModel,
				"to_model":   provider.Model,
				"reason":     "free_router_text_only",
				"conv":       convID[:8],
			})
		} else {
			supportsImage, known, err := openRouterModelSupportsImage(provider.APIKey, origModel)
			if err != nil {
				logstore.Write("warn", "OpenRouter image capability check failed", map[string]string{
					"model": origModel,
					"error": err.Error(),
					"conv":  convID[:8],
				})
			}
			if known && !supportsImage {
				provider.Model = "openrouter/auto"
				logstore.Write("info", "OpenRouter image turn fallback", map[string]string{
					"from_model": origModel,
					"to_model":   provider.Model,
					"reason":     "selected_model_text_only",
					"conv":       convID[:8],
				})
			}
		}
	}

	// Build messages from history.
	// Default 15 (not 20) — 15 messages provides ample context while saving
	// ~500–1500 input tokens on active conversations.
	limit := cfg.ConversationWindowLimit
	if limit == 0 {
		limit = 15
	}

	capabilityPlan, capabilityPolicy := capabilityPolicy(req.Message, config.SupportDir(), s.db, s.db)
	systemPrompt := buildSystemPrompt(cfg, s.db, config.SupportDir(), req.Message, capabilityPolicy.PromptBlock)
	oaiMessages := []agent.OAIMessage{
		{Role: "system", Content: systemPrompt},
	}
	start := 0
	if len(history) > limit {
		start = len(history) - limit
	}

	// When older messages are trimmed, prepend a compact context note so the
	// model knows the conversation has prior history. Uses a cached summary
	// when available; falls back to excerpt-based approach otherwise.
	//
	// Pre-compaction flush: run memory extraction on trimmed user messages so
	// insights are captured before they leave the context window.
	if start > 0 {
		// Pre-compaction memory flush — extract memories from messages about
		// to be dropped, in case they were never processed (daemon restart,
		// memory system was disabled at the time, etc.).
		if cfg.MemoryEnabled {
			// Only flush messages that are newly trimmed — compare current trim
			// count against the cached summary's trim count. If they match, these
			// messages were already flushed on a previous turn.
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
		historyChars := 0
		for i := len(history) - 1; i >= replayStart; i-- {
			if history[i].ID == userMsgID {
				continue
			}
			historyChars += len(history[i].Content)
			if historyChars > compactHistoryChars {
				replayStart = i + 1
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

	// Replay history, skipping the current user message — it is appended below
	// with attachment content parts so raw base64 is never stored in SQLite.
	var historyChars int
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
	// Append current user message with attachment content parts.
	oaiMessages = append(oaiMessages, agent.OAIMessage{
		Role:    "user",
		Content: buildUserContent(req.Message, req.Attachments),
	})

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

	// Select tools based on the configured tool-selection mode.
	//
	//   "lazy"      — Smart compact router. Preselects a small capability-based
	//                 tool set before the main turn, while still keeping
	//                 request_tools available as an escape hatch.
	//   "heuristic" — always-on baseline (core + management + custom) plus
	//                 keyword-triggered groups (~26–57 tools). Default.
	//   "llm"       — compact AI router without the request_tools escape hatch.
	//   "off"       — inject all tools. Explicit opt-in only; never a default.
	//
	// Legacy migration: if ToolSelectionMode is absent from config.json, default
	// to heuristic. "off" must be an explicit choice.
	toolMode := cfg.ToolSelectionMode
	if toolMode == "" {
		toolMode = "heuristic"
	}
	var selectedTools []map[string]any
	switch toolMode {
	case "lazy":
		// Smart compact router: route on capability groups before the main turn,
		// but keep request_tools available so the model can self-correct when the
		// first-pass selection is too narrow.
		selectedTools = appendRequestToolsMeta(selectToolsWithLLM(ctx, cfg, turn, req.Message, s.registry))
		logstore.Write("debug", "Tool selection: smart mode — compact router + request_tools",
			map[string]string{"mode": "lazy"})
	case "llm":
		// Auto-start the appropriate router based on the active provider.
		// MLX-LM: use the MLX-exclusive router (atlas_mlx).
		// llama.cpp: use the llama.cpp router (atlas_engine).
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
		selectedTools = selectToolsWithLLM(ctx, cfg, turn, req.Message, s.registry)
		// Record activity on whichever router is in use.
		if cfg.ActiveAIProvider == string(agent.ProviderAtlasMLX) && s.mlxRouterEngine != nil {
			s.mlxRouterEngine.RecordActivity()
		} else if s.routerEngine != nil {
			s.routerEngine.RecordActivity()
		}
	case "heuristic":
		selectedTools = s.registry.SelectiveToolDefs(req.Message)
		// "off" → selectedTools stays nil → agent uses full tool list
	}
	selectedTools = applyCapabilityPlanToolHints(s.registry, selectedTools, req.Message, capabilityPlan)

	loopCfg := agent.LoopConfig{
		Provider:      provider,
		MaxIterations: maxIter,
		SupportDir:    config.SupportDir(),
		ConvID:        convID,
		Tools:         selectedTools, // nil → loop uses full ToolDefinitions()
		UserMessage:   req.Message,   // used by lazy tool upgrade in the agent loop
		ToolPolicy:    req.ToolPolicy,
	}

	loopConvID := convID
	loopProvider := provider
	agentLoop := &agent.Loop{
		Skills: s.registry,
		BC:     &broadcasterEmitter{bc: s.broadcaster},
		DB:     s.db,
		OnUsage: func(ctx context.Context, p agent.ProviderConfig, usage agent.TokenUsage) {
			s.recordTokenUsage(loopConvID, loopProvider, usage)
		},
	}

	toolCount := len(selectedTools)
	if toolCount == 0 {
		toolCount = s.registry.ToolCount()
	}
	// Use the base name for local path models (atlas_mlx uses full path as model ID).
	logModel := provider.Model
	if provider.Type == agent.ProviderAtlasMLX {
		logModel = filepath.Base(provider.Model)
	}
	logstore.Write("info",
		fmt.Sprintf("Turn started: %s via %s (%d tools, mode=%s)", logModel, provider.Type, toolCount, toolMode),
		map[string]string{"conv": convID[:8], "mode": toolMode})

	// Run the agent loop on a context that is NOT tied to the HTTP request.
	// This ensures an in-flight AI call is not interrupted when the client
	// disconnects (e.g. page refresh) before the response arrives. The
	// broadcaster delivers the response to the reconnected client.
	// A separate cancellable context allows the user to stop a turn mid-flight
	// via POST /message/cancel without disrupting the HTTP lifecycle.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	s.turnCancels.Store(convID, agentCancel)
	defer func() {
		agentCancel()
		s.turnCancels.Delete(convID)
	}()
	// Inject proactive sender so skills can deliver follow-up messages to this
	// conversation asynchronously (e.g. terminal.run_background completion).
	agentCtx = skills.WithProactiveSender(agentCtx, func(text string) {
		s.SendProactive(convID, text)
	})
	// Inject originating convID so async_assignment tasks can push completion
	// notifications back to this conversation when they finish.
	agentCtx = WithOriginConvID(agentCtx, convID)
	turnStart := time.Now()
	result := agentLoop.Run(agentCtx, loopCfg, oaiMessages, convID)

	// Reset idle timers after each turn so the active model isn't ejected mid-session.
	switch provider.Type {
	case agent.ProviderAtlasEngine:
		if s.engine != nil {
			s.engine.RecordActivity()
		}
	case agent.ProviderAtlasMLX:
		if s.mlxEngine != nil {
			s.mlxEngine.RecordActivity()
			// Record per-turn inference stats if the engine supports it.
			if rec, ok := s.mlxEngine.(InferenceRecorder); ok {
				rec.RecordInference(
					result.TotalUsage.InputTokens,
					result.TotalUsage.CachedInputTokens,
					result.TotalUsage.OutputTokens,
					time.Since(turnStart),
					result.FirstTokenAt,
					result.StreamChunkCount,
					result.StreamChars,
				)
			}
		}
	}

	var resp MessageResponse
	resp.Conversation.ID = convID

	switch result.Status {
	case "error":
		// If the error is a context cancellation triggered by the user (via
		// POST /message/cancel), emit a clean "cancelled" event instead of an
		// error so the client can show a neutral stopped state.
		if result.Error != nil && errors.Is(result.Error, context.Canceled) {
			logstore.Write("info", "Turn cancelled by user",
				map[string]string{"conv": convID[:8]})
			s.broadcaster.Emit(convID, SSEEvent{
				Type:           "cancelled",
				ConversationID: convID,
			})
			s.broadcaster.Finish(convID)
			resp.Response.Status = "cancelled"
			return resp, nil
		}
		errMsg := "Agent loop error"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		if provider.Type == agent.ProviderOpenRouter &&
			hasImageAttachments(req.Attachments) &&
			strings.Contains(strings.ToLower(errMsg), "no endpoints found that support image input") {
			errMsg = "The selected OpenRouter model could not accept image input. Atlas tried an automatic image route, but no compatible endpoint is currently available."
		}
		logstore.Write("error", "Turn error: "+errMsg,
			map[string]string{
				"conv":    convID[:8],
				"elapsed": fmt.Sprintf("%.1fs", time.Since(turnStart).Seconds()),
				"in":      fmt.Sprintf("%d", result.TotalUsage.InputTokens),
				"out":     fmt.Sprintf("%d", result.TotalUsage.OutputTokens),
			})
		s.broadcaster.Emit(convID, SSEEvent{
			Type:           "error",
			Error:          errMsg,
			ConversationID: convID,
		})
		s.broadcaster.Finish(convID)

		for _, m := range history {
			resp.Conversation.Messages = append(resp.Conversation.Messages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
			})
		}
		resp.Response.Status = "error"
		resp.Response.ErrorMessage = errMsg
		return resp, nil

	case "pendingApproval":
		// Emit the done event with waitingForApproval status so the web UI
		// enters awaitingResume mode. Do NOT call Finish() here — the channel
		// must stay open so Resume() can stream the continuation after approval.
		s.broadcaster.Emit(convID, SSEEvent{
			Type:           "done",
			ConversationID: convID,
			Status:         "waitingForApproval",
		})

		for _, m := range history {
			resp.Conversation.Messages = append(resp.Conversation.Messages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
			})
		}
		resp.Response.Status = "pendingApproval"
		return resp, nil

	default: // "complete"
		replyAt := time.Now().UTC().Format(time.RFC3339Nano)
		assistantText := result.FinalText

		// Persist assistant reply.
		assistantMsgID := newUUID()
		if err := s.db.SaveMessage(assistantMsgID, convID, "assistant", assistantText, replyAt); err != nil {
			return MessageResponse{}, fmt.Errorf("chat: save assistant message: %w", err)
		}

		// Phase 7b: detect any [T-NN] engagement markers the agent wrote
		// into its reply and write pending surfacings to the sidecar.
		// Synchronous because the next user turn's classifier needs to
		// see these rows before it runs.
		s.detectAndRecordSurfacings(convID, assistantMsgID, assistantText, time.Now().UTC())

		logstore.Write("info", "Turn complete",
			map[string]string{
				"conv":     convID[:8],
				"elapsed":  fmt.Sprintf("%.1fs", time.Since(turnStart).Seconds()),
				"in":       fmt.Sprintf("%d", result.TotalUsage.InputTokens),
				"out":      fmt.Sprintf("%d", result.TotalUsage.OutputTokens),
				"sys_est":  fmt.Sprintf("~%d", len(systemPrompt)/4),
				"hist_est": fmt.Sprintf("~%d", historyChars/4),
			})

		// Post-turn background tasks use a detached context so they are not
		// canceled when the HTTP request context closes after the response is sent.
		bgCtx := context.WithoutCancel(ctx)

		// Post-turn memory extraction (non-blocking).
		// Passes provider + assistant text for LLM-based extraction alongside regex.
		go memory.ExtractAndPersist(bgCtx, cfg, heavyBgProvider, req.Message, assistantText,
			result.ToolCallSummaries, result.ToolResultSummaries, convID, s.db)

		// Post-turn MIND reflection and DIARY entry (non-blocking).
		// Skip if the assistant produced no text — a pure tool-call turn with
		// no narrative would produce a meaningless Today's Read.
		if assistantText != "" {
			turn := mind.TurnRecord{
				ConversationID:      convID,
				UserMessage:         req.Message,
				AssistantResponse:   assistantText,
				ToolCallSummaries:   result.ToolCallSummaries,
				ToolResultSummaries: result.ToolResultSummaries,
				Timestamp:           time.Now(),
			}
			mind.ReflectNonBlocking(heavyBgProvider, turn, config.SupportDir())
			mind.LearnFromTurnNonBlocking(heavyBgProvider, turn, config.SupportDir())
		}

		// Reset the nap idle timer after every completed turn. Safe to call
		// unconditionally — the scheduler is a no-op if naps are disabled or
		// if no scheduler has been registered (tests, dormant config).
		mind.NotifyTurnNonBlocking()

		// Collect all generated files for this turn: start from what the loop
		// already tracked (tool artifacts + tool summaries), then scan FinalText
		// for any additional paths the model mentioned in its narrative.
		generatedFiles := append([]string(nil), result.GeneratedFiles...)
		emittedSet := map[string]bool{}
		for _, p := range generatedFiles {
			emittedSet[p] = true
		}
		if result.FinalText != "" {
			for _, filePath := range agent.ExtractPathsFromText(result.FinalText) {
				if emittedSet[filePath] {
					continue
				}
				emittedSet[filePath] = true
				token := agent.RegisterArtifact(filePath)
				if token == "" {
					continue
				}
				info, statErr := os.Stat(filePath)
				var size int64
				if statErr == nil {
					size = info.Size()
				}
				s.broadcaster.Emit(convID, SSEEvent{
					Type:           "file_generated",
					ConversationID: convID,
					Filename:       filepath.Base(filePath),
					MimeType:       agent.MimeTypeForPath(filePath),
					FileSize:       size,
					FileToken:      token,
				})
				generatedFiles = append(generatedFiles, filePath)
			}
		}

		// Emit done event with status="completed" so the web UI can trigger
		// post-turn work (e.g. link preview fetching) gated on this status.
		s.broadcaster.Emit(convID, SSEEvent{
			Type:           "done",
			Status:         "completed",
			ConversationID: convID,
		})
		s.broadcaster.Finish(convID)

		// Build the full response message list.
		allMessages := make([]MessageItem, 0, len(history)+1)
		for _, m := range history {
			allMessages = append(allMessages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
			})
		}
		allMessages = append(allMessages, MessageItem{
			ID:        assistantMsgID,
			Role:      "assistant",
			Content:   assistantText,
			Timestamp: replyAt,
		})

		resp.Conversation.Messages = allMessages
		resp.Response.AssistantMessage = assistantText
		resp.Response.Status = "complete"
		resp.GeneratedFiles = generatedFiles
		return resp, nil
	}
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

// Resume is called after an approval is resolved. It loads the deferred
// execution from the DB, executes or denies the tool call, and continues
// the agent loop to completion.
func (s *Service) Resume(toolCallID string, approved bool) {
	ctx := context.Background()

	row, err := s.db.FetchDeferredByToolCallID(toolCallID)
	if err != nil || row == nil {
		return
	}

	// Parse the saved deferral state.
	var state struct {
		Messages  []agent.OAIMessage  `json:"messages"`
		ToolCalls []agent.OAIToolCall `json:"tool_calls"`
		ConvID    string              `json:"conv_id"`
	}
	if err := json.Unmarshal([]byte(row.NormalizedInputJSON), &state); err != nil {
		return
	}

	convID := state.ConvID
	if convID == "" && row.ConversationID != nil {
		convID = *row.ConversationID
	}

	// Find the tool call in the saved state.
	var targetTC *agent.OAIToolCall
	for i := range state.ToolCalls {
		if state.ToolCalls[i].ID == toolCallID {
			targetTC = &state.ToolCalls[i]
			break
		}
	}
	if targetTC == nil {
		return
	}

	// Build the tool result message.
	var toolResult string
	if approved {
		toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, execErr := s.registry.Execute(toolCtx, targetTC.Function.Name, json.RawMessage(targetTC.Function.Arguments))
		cancel()
		if execErr != nil {
			toolResult = fmt.Sprintf("Tool execution error: %v", execErr)
		} else {
			toolResult = result.FormatForModel()
		}
	} else {
		toolResult = "Action denied by user."
	}

	// Add tool result to messages.
	messages := append(state.Messages, agent.OAIMessage{
		Role:       "tool",
		Content:    toolResult,
		ToolCallID: toolCallID,
		Name:       targetTC.Function.Name,
	})

	// Also handle other tool calls that were deferred at the same time.
	// Check for other pending approvals in this conversation.
	pending, _ := s.db.FetchDeferredsByConversationID(convID, "pending_approval")
	for _, p := range pending {
		if p.ToolCallID == toolCallID {
			continue // already handled
		}
		// Other tool calls from the same batch — add denied result.
		actionID := ""
		if p.ActionID != nil {
			actionID = *p.ActionID
		}
		messages = append(messages, agent.OAIMessage{
			Role:       "tool",
			Content:    "Action deferred (separate approval required).",
			ToolCallID: p.ToolCallID,
			Name:       actionID,
		})
	}

	cfg := s.cfgStore.Load()
	provider, provErr := resolveProvider(cfg)
	if provErr != nil {
		return
	}

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

	loopCfg := agent.LoopConfig{
		Provider:      provider,
		MaxIterations: maxIter,
		SupportDir:    config.SupportDir(),
		ConvID:        convID,
	}

	resumeConvID := convID
	resumeProvider := provider
	agentLoop := &agent.Loop{
		Skills: s.registry,
		BC:     &broadcasterEmitter{bc: s.broadcaster},
		DB:     s.db,
		OnUsage: func(ctx context.Context, p agent.ProviderConfig, usage agent.TokenUsage) {
			s.recordTokenUsage(resumeConvID, resumeProvider, usage)
		},
	}

	// Emit assistant_started so the web UI opens a new bubble for the resumed turn.
	s.broadcaster.Emit(convID, SSEEvent{
		Type:           "assistant_started",
		ConversationID: convID,
	})

	resumeStart := time.Now()
	result := agentLoop.Run(ctx, loopCfg, messages, convID)

	if result.Status == "complete" && result.FinalText != "" {
		replyAt := time.Now().UTC().Format(time.RFC3339Nano)
		assistantMsgID := newUUID()
		if err := s.db.SaveMessage(assistantMsgID, convID, "assistant", result.FinalText, replyAt); err != nil {
			logstore.Write("warn", "Resume: failed to persist assistant message: "+err.Error(),
				map[string]string{"conv": convID})
		}

		// Phase 7b: resume-path surfacing detection. Same hook as the
		// main HandleMessage path so thoughts raised in an approval
		// resume still produce pending engagement rows.
		s.detectAndRecordSurfacings(convID, assistantMsgID, result.FinalText, time.Now().UTC())

		logstore.Write("info", "Resume complete",
			map[string]string{
				"conv":    convID[:8],
				"elapsed": fmt.Sprintf("%.1fs", time.Since(resumeStart).Seconds()),
				"in":      fmt.Sprintf("%d", result.TotalUsage.InputTokens),
				"out":     fmt.Sprintf("%d", result.TotalUsage.OutputTokens),
			})

		s.broadcaster.Emit(convID, SSEEvent{
			Type:           "done",
			Status:         "completed",
			ConversationID: convID,
		})
		s.broadcaster.Finish(convID)
	}
}

// recordTokenUsage computes cost and persists a token usage event for one turn.
// Non-fatal — a failure here never surfaces to the user.
func (s *Service) recordTokenUsage(convID string, provider agent.ProviderConfig, usage agent.TokenUsage) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return
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
		usage.InputTokens, usage.OutputTokens,
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
	if err := s.db.SaveMessage(msgID, convID, "assistant", text, now); err != nil {
		return fmt.Errorf("webchat inject: save message: %w", err)
	}
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_started", Role: "assistant", ConversationID: convID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_delta", Content: text, Role: "assistant", ConversationID: convID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "assistant_done", Role: "assistant", ConversationID: convID})
	s.broadcaster.Emit(convID, SSEEvent{Type: "done", Status: "completed", ConversationID: convID})
	logstore.Write("info", "Webchat inject: delivered automation result", map[string]string{"conv": convID[:8]})
	return nil
}
