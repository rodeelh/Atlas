package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/capabilities"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/memory"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

// ── TurnState ─────────────────────────────────────────────────────────────────

// TurnState is the single mutable struct that flows through pipeline stages.
// Each stage reads from and writes to it; no stage returns data directly.
type TurnState struct {
	// ── inputs (set before Run) ──────────────────────────────────────────
	Req MessageRequest
	Cfg config.RuntimeConfigSnapshot

	// ── prepareContext ────────────────────────────────────────────────────
	ConvID    string
	TurnID    string
	UserMsgID string
	Now       string
	History   []storage.MessageRow
	turn      *turnContext // unexported; used by tool router + reflection

	// ── resolveProvider ──────────────────────────────────────────────────
	Provider        agent.ProviderConfig
	HeavyBgProvider agent.ProviderConfig

	// ── buildInput ───────────────────────────────────────────────────────
	OAIMessages  []agent.OAIMessage
	HistoryChars int
	SystemPrompt string
	CapPlan      capabilities.Analysis

	// ── selectTools ──────────────────────────────────────────────────────
	Selector      agent.ToolSelector
	SelectedTools []map[string]any
	ToolMode      string

	// ── execute ──────────────────────────────────────────────────────────
	Result    agent.RunResult
	TurnStart time.Time

	// ── persist ──────────────────────────────────────────────────────────
	AssistantMsgID string
	AssistantText  string
	ReplyAt        string
	GeneratedFiles []string

	// ── outcome ──────────────────────────────────────────────────────────
	// "complete" | "error" | "cancelled" | "pendingApproval" | "earlyExit"
	Status    string
	ErrorMsg  string
	earlyResp MessageResponse // used by vision-degradation earlyExit path
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// Pipeline executes the ordered sequence of stages for a chat turn.
// It holds a reference to the Service for access to DB, registry, broadcaster,
// and all service-level dependencies.
type Pipeline struct {
	svc   *Service
	hooks *HookRegistry
}

func newPipeline(svc *Service, hooks *HookRegistry) *Pipeline {
	return &Pipeline{svc: svc, hooks: hooks}
}

// Run executes all pipeline stages for a new HandleMessage call.
// Returns a TurnState ready for BuildResponse; never returns a hard error
// unless the DB fails to save the user message (before any SSE is emitted).
func (p *Pipeline) Run(ctx context.Context, req MessageRequest) (*TurnState, error) {
	s := &TurnState{
		Req: req,
		Cfg: p.svc.cfgStore.Load(),
	}

	if err := p.prepareContext(ctx, s); err != nil {
		return s, err
	}
	if err := p.resolveProvider(s); err != nil {
		// providerError emits SSE and sets s.Status = "error"
		p.emitProviderError(s, err)
		return s, nil
	}
	if p.tryVisionDegradation(s) {
		return s, nil // earlyExit — response already fully built
	}
	p.buildInput(ctx, s)
	p.selectTools(ctx, s)
	p.execute(ctx, s)
	if s.Status == "error" || s.Status == "cancelled" || s.Status == "pendingApproval" {
		return s, nil
	}
	p.persist(ctx, s)
	p.postTurn(ctx, s)
	return s, nil
}

// BuildResponse converts a completed TurnState into the MessageResponse that
// HandleMessage returns to the HTTP layer.
func (p *Pipeline) BuildResponse(s *TurnState) MessageResponse {
	var resp MessageResponse
	resp.Conversation.ID = s.ConvID

	switch s.Status {
	case "error":
		for _, m := range s.History {
			resp.Conversation.Messages = append(resp.Conversation.Messages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
				Blocks:    HydrateStoredBlocks(firstNonNilString(m.BlocksJSON)),
			})
		}
		resp.Response.Status = "error"
		resp.Response.ErrorMessage = s.ErrorMsg
		return resp

	case "cancelled":
		resp.Response.Status = "cancelled"
		return resp

	case "pendingApproval":
		for _, m := range s.History {
			resp.Conversation.Messages = append(resp.Conversation.Messages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
				Blocks:    HydrateStoredBlocks(firstNonNilString(m.BlocksJSON)),
			})
		}
		resp.Response.Status = "pendingApproval"
		return resp

	case "earlyExit":
		// Vision degradation path — response already in s.earlyResp.
		return s.earlyResp

	default: // "complete"
		allMessages := make([]MessageItem, 0, len(s.History)+1)
		for _, m := range s.History {
			allMessages = append(allMessages, MessageItem{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				Timestamp: m.Timestamp,
				Blocks:    HydrateStoredBlocks(firstNonNilString(m.BlocksJSON)),
			})
		}
		allMessages = append(allMessages, MessageItem{
			ID:        s.AssistantMsgID,
			Role:      "assistant",
			Content:   s.AssistantText,
			Timestamp: s.ReplyAt,
			Blocks:    s.Result.MessageBlocks,
		})
		resp.Conversation.Messages = allMessages
		resp.Response.AssistantMessage = s.AssistantText
		resp.Response.Status = "complete"
		resp.GeneratedFiles = s.GeneratedFiles
		return resp
	}
}

// Resume loads a deferred approval, executes or denies it, and continues the
// agent loop. Mirrors the original service.Resume logic.
func (p *Pipeline) Resume(ctx context.Context, toolCallID string, approved bool) {
	svc := p.svc
	turnID := newUUID()

	row, err := svc.db.FetchDeferredByToolCallID(toolCallID)
	if err != nil || row == nil {
		return
	}

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

	var toolResult string
	if approved {
		toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		result, execErr := svc.registry.Execute(toolCtx, targetTC.Function.Name, json.RawMessage(targetTC.Function.Arguments))
		cancel()
		if execErr != nil {
			toolResult = fmt.Sprintf("Tool execution error: %v", execErr)
		} else {
			toolResult = result.FormatForModel()
		}
	} else {
		toolResult = "Action denied by user."
	}

	messages := append(state.Messages, agent.OAIMessage{
		Role:       "tool",
		Content:    toolResult,
		ToolCallID: toolCallID,
		Name:       targetTC.Function.Name,
	})

	pending, _ := svc.db.FetchDeferredsByConversationID(convID, "pending_approval")
	for _, pp := range pending {
		if pp.ToolCallID == toolCallID {
			continue
		}
		actionID := ""
		if pp.ActionID != nil {
			actionID = *pp.ActionID
		}
		messages = append(messages, agent.OAIMessage{
			Role:       "tool",
			Content:    "Action deferred (separate approval required).",
			ToolCallID: pp.ToolCallID,
			Name:       actionID,
		})
	}

	cfg := svc.cfgStore.Load()
	provider, provErr := resolveProvider(cfg)
	if provErr != nil {
		return
	}

	maxIter := resolveMaxIter(cfg)
	loopCfg := agent.LoopConfig{
		Provider:      provider,
		MaxIterations: maxIter,
		SupportDir:    config.SupportDir(),
		ConvID:        convID,
		TurnID:        turnID,
		Adapter:       agent.NewAdapter(provider),
	}

	resumeConvID := convID
	resumeProvider := provider
	agentLoop := &agent.Loop{
		Skills: svc.registry,
		BC:     &broadcasterEmitter{bc: svc.broadcaster},
		DB:     svc.db,
		OnUsage: func(ctx context.Context, prov agent.ProviderConfig, usage agent.TokenUsage) {
			svc.recordTokenUsage(resumeConvID, resumeProvider, usage)
		},
	}

	svc.broadcaster.Emit(convID, SSEEvent{
		Type:           "assistant_started",
		ConversationID: convID,
		TurnID:         turnID,
	})

	resumeStart := time.Now()
	result := agentLoop.Run(ctx, loopCfg, messages, convID)

	if result.Status == "complete" && (result.FinalText != "" || len(result.MessageBlocks) > 0) {
		replyAt := time.Now().UTC().Format(time.RFC3339Nano)
		assistantMsgID := newUUID()
		if err := svc.db.SaveMessageWithBlocks(assistantMsgID, convID, "assistant", result.FinalText, replyAt, messageBlocksJSON(result.MessageBlocks)); err != nil {
			logstore.Write("warn", "Resume: failed to persist assistant message: "+err.Error(),
				map[string]string{"conv": convID})
		}

		svc.detectAndRecordSurfacings(convID, assistantMsgID, result.FinalText, time.Now().UTC())

		logstore.Write("info", "Resume complete",
			map[string]string{
				"conv":    convID[:8],
				"elapsed": fmt.Sprintf("%.1fs", time.Since(resumeStart).Seconds()),
				"in":      fmt.Sprintf("%d", result.TotalUsage.InputTokens),
				"out":     fmt.Sprintf("%d", result.TotalUsage.OutputTokens),
			})

		svc.broadcaster.Emit(convID, SSEEvent{
			Type:           "done",
			Status:         "completed",
			ConversationID: convID,
			TurnID:         turnID,
		})
		svc.broadcaster.Finish(convID)
	}
}

// ── stages ────────────────────────────────────────────────────────────────────

func (p *Pipeline) prepareContext(ctx context.Context, s *TurnState) error {
	svc := p.svc
	s.Now = time.Now().UTC().Format(time.RFC3339Nano)
	s.TurnID = newUUID()
	s.turn = &turnContext{}

	s.ConvID = s.Req.ConversationID
	if s.ConvID == "" {
		s.ConvID = newUUID()
	}

	platform := s.Req.Platform
	if platform == "" {
		platform = "web"
	}
	if err := svc.db.SaveConversation(s.ConvID, s.Now, s.Now, platform, nil); err != nil {
		return fmt.Errorf("chat: save conversation: %w", err)
	}

	s.UserMsgID = newUUID()
	if err := svc.db.SaveMessage(s.UserMsgID, s.ConvID, "user", s.Req.Message, s.Now); err != nil {
		return fmt.Errorf("chat: save user message: %w", err)
	}

	// Engagement classifier: non-blocking, detached context.
	classifierCtx := context.WithoutCancel(ctx)
	svc.classifyPendingIfAny(classifierCtx, s.ConvID, s.Req.Message, func(id string) string {
		return svc.lookupThoughtBody(id)
	})

	history, err := svc.db.ListMessages(s.ConvID)
	if err != nil {
		return fmt.Errorf("chat: list messages: %w", err)
	}
	s.History = history
	return nil
}

func (p *Pipeline) resolveProvider(s *TurnState) error {
	svc := p.svc
	provider, err := resolveProvider(s.Cfg)
	if err != nil {
		return err
	}
	svc.ensureEngineRunning(s.Cfg, provider)

	heavyBg, err := resolveHeavyBackgroundProvider(s.Cfg)
	if err != nil {
		heavyBg = provider
	}

	// OpenRouter image fallback: upgrade to vision-capable route when the selected
	// model is text-only.
	if provider.Type == agent.ProviderOpenRouter && hasImageAttachments(s.Req.Attachments) {
		origModel := strings.TrimSpace(provider.Model)
		if origModel == "" {
			origModel = "openrouter/auto:free"
		}
		if origModel == "openrouter/auto:free" {
			provider.Model = "openrouter/auto"
			logstore.Write("info", "OpenRouter image turn fallback",
				map[string]string{"from_model": origModel, "to_model": provider.Model, "reason": "free_router_text_only", "conv": s.ConvID[:8]})
		} else {
			supportsImage, known, checkErr := openRouterModelSupportsImage(provider.APIKey, origModel)
			if checkErr != nil {
				logstore.Write("warn", "OpenRouter image capability check failed",
					map[string]string{"model": origModel, "error": checkErr.Error(), "conv": s.ConvID[:8]})
			}
			if known && !supportsImage {
				provider.Model = "openrouter/auto"
				logstore.Write("info", "OpenRouter image turn fallback",
					map[string]string{"from_model": origModel, "to_model": provider.Model, "reason": "selected_model_text_only", "conv": s.ConvID[:8]})
			}
		}
	}

	s.Provider = provider
	s.HeavyBgProvider = heavyBg
	return nil
}

// tryVisionDegradation returns true if the turn was short-circuited due to a
// local provider receiving an image attachment. The response is written into
// s.earlyResp and s.Status = "earlyExit".
func (p *Pipeline) tryVisionDegradation(s *TurnState) bool {
	svc := p.svc
	pt := s.Provider.Type
	if (pt == agent.ProviderLMStudio || pt == agent.ProviderOllama ||
		pt == agent.ProviderAtlasEngine || pt == agent.ProviderAtlasMLX) &&
		hasImageAttachments(s.Req.Attachments) {

		const degradeMsg = "Vision is not available with local models. " +
			"Switch to OpenAI, Anthropic, or Gemini to analyse images."
		replyAt := time.Now().UTC().Format(time.RFC3339Nano)
		assistantMsgID := newUUID()
		_ = svc.db.SaveMessage(assistantMsgID, s.ConvID, "assistant", degradeMsg, replyAt)
		svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "assistant_started", Role: "assistant", ConversationID: s.ConvID, TurnID: s.TurnID})
		svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "assistant_delta", Content: degradeMsg, Role: "assistant", ConversationID: s.ConvID, TurnID: s.TurnID})
		svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "assistant_done", Role: "assistant", ConversationID: s.ConvID, TurnID: s.TurnID})
		svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "done", Status: "completed", ConversationID: s.ConvID, TurnID: s.TurnID})
		svc.broadcaster.Finish(s.ConvID)

		allMessages := make([]MessageItem, 0, len(s.History)+1)
		for _, m := range s.History {
			allMessages = append(allMessages, MessageItem{ID: m.ID, Role: m.Role, Content: m.Content, Timestamp: m.Timestamp, Blocks: HydrateStoredBlocks(firstNonNilString(m.BlocksJSON))})
		}
		allMessages = append(allMessages, MessageItem{ID: assistantMsgID, Role: "assistant", Content: degradeMsg, Timestamp: replyAt})

		s.Status = "earlyExit"
		s.earlyResp = MessageResponse{}
		s.earlyResp.Conversation.ID = s.ConvID
		s.earlyResp.Conversation.Messages = allMessages
		s.earlyResp.Response.AssistantMessage = degradeMsg
		s.earlyResp.Response.Status = "complete"
		return true
	}
	return false
}

func (p *Pipeline) buildInput(ctx context.Context, s *TurnState) {
	capPlan, capPolicy := capabilityPolicy(s.Req.Message, config.SupportDir(), p.svc.db, p.svc.db)
	s.CapPlan = capPlan
	var queryVec []float32
	if shouldComputeHyDE(s.Cfg, s.Req.Message) {
		queryVec = memory.HyDEVector(ctx, s.Provider, s.Req.Message)
	}
	s.OAIMessages, s.HistoryChars, s.SystemPrompt = p.svc.buildTurnMessages(
		s.Cfg, s.Req, s.History, s.ConvID, s.UserMsgID, capPolicy.PromptBlock, queryVec,
	)
}

func shouldComputeHyDE(cfg config.RuntimeConfigSnapshot, userMessage string) bool {
	return cfg.MaxRetrievedMemoriesPerTurn > 0 && shouldInjectMemories(userMessage)
}

func (p *Pipeline) selectTools(ctx context.Context, s *TurnState) {
	s.Selector, s.SelectedTools, s.ToolMode = p.svc.selectTurnTools(ctx, s.Cfg, s.Req, s.turn, s.CapPlan)
}

func (p *Pipeline) execute(ctx context.Context, s *TurnState) {
	svc := p.svc

	maxIter := resolveMaxIter(s.Cfg)
	loopCfg := agent.LoopConfig{
		Provider:      s.Provider,
		MaxIterations: maxIter,
		SupportDir:    config.SupportDir(),
		ConvID:        s.ConvID,
		TurnID:        s.TurnID,
		Tools:         s.SelectedTools,
		UserMessage:   s.Req.Message,
		ToolPolicy:    s.Req.ToolPolicy,
		Selector:      s.Selector,
		Adapter:       agent.NewAdapter(s.Provider),
	}

	loopConvID := s.ConvID
	loopProvider := s.Provider
	agentLoop := &agent.Loop{
		Skills: svc.registry,
		BC:     &broadcasterEmitter{bc: svc.broadcaster},
		DB:     svc.db,
		OnUsage: func(ctx context.Context, prov agent.ProviderConfig, usage agent.TokenUsage) {
			svc.recordTokenUsage(loopConvID, loopProvider, usage)
		},
	}

	toolCount := len(s.SelectedTools)
	if toolCount == 0 {
		toolCount = svc.registry.ToolCount()
	}
	logModel := s.Provider.Model
	if s.Provider.Type == agent.ProviderAtlasMLX {
		logModel = filepath.Base(s.Provider.Model)
	}
	logstore.Write("info",
		fmt.Sprintf("Turn started: %s via %s (%d tools, mode=%s)", logModel, s.Provider.Type, toolCount, s.ToolMode),
		map[string]string{"conv": s.ConvID[:8], "mode": s.ToolMode})

	// Detached context: turn survives client disconnect + supports user cancel.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	svc.turnCancels.Store(s.ConvID, agentCancel)
	defer func() {
		agentCancel()
		svc.turnCancels.Delete(s.ConvID)
	}()

	agentCtx = skills.WithProactiveSender(agentCtx, func(text string) {
		svc.SendProactive(s.ConvID, text)
	})
	agentCtx = skills.WithMemoryEmbedder(agentCtx, func(ctx context.Context, query string) ([]float32, error) {
		return agent.Embed(ctx, s.Provider, agent.NomicPrefixQuery+query)
	})
	agentCtx = WithOriginConvID(agentCtx, s.ConvID) // L2 seam

	s.TurnStart = time.Now()
	s.Result = agentLoop.Run(agentCtx, loopCfg, s.OAIMessages, s.ConvID)

	// Record engine activity / per-turn inference stats.
	switch s.Provider.Type {
	case agent.ProviderAtlasEngine:
		if svc.engine != nil {
			svc.engine.RecordActivity()
		}
	case agent.ProviderAtlasMLX:
		if svc.mlxEngine != nil {
			svc.mlxEngine.RecordActivity()
			if rec, ok := svc.mlxEngine.(InferenceRecorder); ok {
				rec.RecordInference(
					s.Result.TotalUsage.InputTokens,
					s.Result.TotalUsage.CachedInputTokens,
					s.Result.TotalUsage.OutputTokens,
					time.Since(s.TurnStart),
					s.Result.FirstTokenAt,
					s.Result.StreamChunkCount,
					s.Result.StreamChars,
				)
			}
		}
	}

	switch s.Result.Status {
	case "error":
		if s.Result.Error != nil && errors.Is(s.Result.Error, context.Canceled) {
			logstore.Write("info", "Turn cancelled by user", map[string]string{"conv": s.ConvID[:8]})
			svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "cancelled", ConversationID: s.ConvID, TurnID: s.TurnID})
			svc.broadcaster.Finish(s.ConvID)
			s.Status = "cancelled"
			return
		}
		errMsg := "Agent loop error"
		if s.Result.Error != nil {
			errMsg = s.Result.Error.Error()
		}
		if s.Provider.Type == agent.ProviderOpenRouter &&
			hasImageAttachments(s.Req.Attachments) &&
			strings.Contains(strings.ToLower(errMsg), "no endpoints found that support image input") {
			errMsg = "The selected OpenRouter model could not accept image input. Atlas tried an automatic image route, but no compatible endpoint is currently available."
		}
		logstore.Write("error", "Turn error: "+errMsg,
			map[string]string{
				"conv":    s.ConvID[:8],
				"elapsed": fmt.Sprintf("%.1fs", time.Since(s.TurnStart).Seconds()),
				"in":      fmt.Sprintf("%d", s.Result.TotalUsage.InputTokens),
				"out":     fmt.Sprintf("%d", s.Result.TotalUsage.OutputTokens),
			})
		svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "error", Error: errMsg, ConversationID: s.ConvID, TurnID: s.TurnID})
		svc.broadcaster.Finish(s.ConvID)
		s.Status = "error"
		s.ErrorMsg = errMsg

	case "pendingApproval":
		svc.broadcaster.Emit(s.ConvID, SSEEvent{
			Type:           "done",
			ConversationID: s.ConvID,
			Status:         "waitingForApproval",
			TurnID:         s.TurnID,
		})
		s.Status = "pendingApproval"
	}
}

func (p *Pipeline) persist(ctx context.Context, s *TurnState) {
	svc := p.svc
	s.ReplyAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.AssistantText = s.Result.FinalText
	s.AssistantMsgID = newUUID()

	if err := svc.db.SaveMessageWithBlocks(s.AssistantMsgID, s.ConvID, "assistant", s.AssistantText, s.ReplyAt, messageBlocksJSON(s.Result.MessageBlocks)); err != nil {
		// Hard error on persist — propagate (caller will return error response).
		logstore.Write("error", "persist: save assistant message failed: "+err.Error(), map[string]string{"conv": s.ConvID})
	}

	svc.detectAndRecordSurfacings(s.ConvID, s.AssistantMsgID, s.AssistantText, time.Now().UTC())

	logstore.Write("info", "Turn complete",
		map[string]string{
			"conv":     s.ConvID[:8],
			"elapsed":  fmt.Sprintf("%.1fs", time.Since(s.TurnStart).Seconds()),
			"in":       fmt.Sprintf("%d", s.Result.TotalUsage.InputTokens),
			"out":      fmt.Sprintf("%d", s.Result.TotalUsage.OutputTokens),
			"sys_est":  fmt.Sprintf("~%d", len(s.SystemPrompt)/4),
			"hist_est": fmt.Sprintf("~%d", s.HistoryChars/4),
		})

	// Scan generated files.
	s.GeneratedFiles = append([]string(nil), s.Result.GeneratedFiles...)
	emittedSet := map[string]bool{}
	for _, fp := range s.GeneratedFiles {
		emittedSet[fp] = true
	}
	if s.AssistantText != "" {
		for _, filePath := range agent.ExtractPathsFromText(s.AssistantText) {
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
			svc.broadcaster.Emit(s.ConvID, SSEEvent{
				Type:           "file_generated",
				ConversationID: s.ConvID,
				TurnID:         s.TurnID,
				Filename:       filepath.Base(filePath),
				MimeType:       agent.MimeTypeForPath(filePath),
				FileSize:       size,
				FileToken:      token,
			})
			s.GeneratedFiles = append(s.GeneratedFiles, filePath)
		}
	}

	svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "done", Status: "completed", ConversationID: s.ConvID, TurnID: s.TurnID})
	svc.broadcaster.Finish(s.ConvID)
	s.Status = "complete"
}

func (p *Pipeline) postTurn(ctx context.Context, s *TurnState) {
	bgCtx := context.WithoutCancel(ctx)
	record := TurnRecord{
		ConvID:              s.ConvID,
		UserMessage:         s.Req.Message,
		AssistantResponse:   s.AssistantText,
		Provider:            s.Provider,
		HeavyBgProvider:     s.HeavyBgProvider,
		ToolCallSummaries:   s.Result.ToolCallSummaries,
		ToolResultSummaries: s.Result.ToolResultSummaries,
		Cfg:                 s.Cfg,
	}
	p.hooks.Run(bgCtx, record)
}

// emitProviderError broadcasts the provider-unavailable error and sets state.
func (p *Pipeline) emitProviderError(s *TurnState, err error) {
	errMsg := err.Error()
	logstore.Write("error", "Provider unavailable", map[string]string{"error": errMsg})
	p.svc.broadcaster.Emit(s.ConvID, SSEEvent{Type: "error", Error: errMsg, ConversationID: s.ConvID, TurnID: s.TurnID})
	p.svc.broadcaster.Finish(s.ConvID)
	s.Status = "error"
	s.ErrorMsg = errMsg
}
