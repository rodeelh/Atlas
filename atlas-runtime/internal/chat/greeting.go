package chat

// greeting.go implements the mind-thoughts live greeting flow. When the
// dispatcher auto-executes a thought (score ≥ 95, class read), the result
// lands in pending-greetings.json. The next time the user opens the chat,
// the web client calls POST /chat/greeting, which:
//
//   1. Drains the pending-greetings queue
//   2. Resolves the active conversation (most recent, or creates a new one)
//   3. Builds a one-shot agent turn with a warm system prompt telling the
//      model to greet the user by name and report what it did while they
//      were away
//   4. Calls the heavy background provider (non-streaming for simplicity —
//      the greeting is typically <200 tokens and doesn't benefit from
//      token-level streaming)
//   5. Saves the result as a normal assistant message so it appears in
//      conversation history
//   6. Emits a greeting_delivered SSE event carrying the content so a live
//      client can render the unprompted marker
//   7. Emits telemetry (greeting_delivered or greeting_skipped)
//   8. Clears the pending-greetings queue
//
// Phase 5 is intentionally lightweight — the surfacing polish (sidebar dot,
// persistent unprompted marker, "from a thought" badge on approvals) is
// phase 6's job.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind"
	"atlas-runtime-go/internal/mind/telemetry"
)

// GreetingTelemetry is the narrow telemetry surface the greeting path uses.
// Kept behind an interface so chat.Service doesn't depend on the concrete
// emitter type, and so tests can pass a stub.
type GreetingTelemetry interface {
	Emit(kind telemetry.Kind, thoughtID, convID string, payload any)
}

// greetingTelemetryNoop is returned when no emitter has been wired.
type greetingTelemetryNoop struct{}

func (greetingTelemetryNoop) Emit(kind telemetry.Kind, thoughtID, convID string, payload any) {
}

// SetGreetingTelemetry wires the telemetry emitter used by HandleGreeting.
// Called from main.go at startup once the mind telemetry emitter exists.
func (s *Service) SetGreetingTelemetry(tel GreetingTelemetry) {
	if tel == nil {
		s.greetingTelemetry = greetingTelemetryNoop{}
	} else {
		s.greetingTelemetry = tel
	}
}

// ensureGreetingTelemetry guarantees a non-nil emitter for greeting paths.
func (s *Service) ensureGreetingTelemetry() GreetingTelemetry {
	if s.greetingTelemetry == nil {
		return greetingTelemetryNoop{}
	}
	return s.greetingTelemetry
}

// GreetingResponse is the JSON body returned by POST /chat/greeting.
type GreetingResponse struct {
	Delivered      bool      `json:"delivered"`
	ConversationID string    `json:"conversationID,omitempty"`
	MessageID      string    `json:"messageID,omitempty"`
	Content        string    `json:"content,omitempty"`
	ThoughtIDs     []string  `json:"thoughtIDs,omitempty"`
	DeliveredAt    time.Time `json:"deliveredAt,omitempty"`
	Skipped        string    `json:"skipped,omitempty"` // "queue_empty" | "no_provider" | "model_failed"
}

// PendingGreetingsCount is the JSON body returned by GET /chat/pending-greetings.
type PendingGreetingsCount struct {
	Count int `json:"count"`
}

// PendingGreetingsCount returns how many greeting entries are queued. Used
// by the sidebar dot in phase 6. When ThoughtsEnabled is false, always
// returns 0 — the dot must never appear when the feature is off.
func (s *Service) PendingGreetingsCount() (int, error) {
	if !s.thoughtsEnabled() {
		return 0, nil
	}
	queue, err := mind.LoadPendingGreetings(config.SupportDir())
	if err != nil {
		return 0, err
	}
	return len(queue), nil
}

// HandleGreeting drains the pending-greetings queue, builds a one-shot
// greeting from the results, saves it to conversation history, emits SSE,
// and clears the queue. Returns a GreetingResponse describing what happened.
//
// If the queue is empty, returns `Delivered: false, Skipped: "queue_empty"`
// without an error — the caller (typically a web client on chat-open) gets
// a cheap no-op response.
//
// Master gate: when ThoughtsEnabled is false, this is a no-op regardless
// of what might still be in the queue.
func (s *Service) HandleGreeting(ctx context.Context, convIDHint string) (GreetingResponse, error) {
	if !s.thoughtsEnabled() {
		return GreetingResponse{Delivered: false, Skipped: "thoughts_disabled"}, nil
	}
	tel := s.ensureGreetingTelemetry()

	queue, err := mind.LoadPendingGreetings(config.SupportDir())
	if err != nil {
		return GreetingResponse{}, fmt.Errorf("load pending greetings: %w", err)
	}
	if len(queue) == 0 {
		tel.Emit(telemetry.KindGreetingSkipped, "", "", map[string]any{"reason": "queue_empty"})
		return GreetingResponse{Delivered: false, Skipped: "queue_empty"}, nil
	}

	// Collect thought ids up front for the response and telemetry.
	ids := make([]string, 0, len(queue))
	for _, entry := range queue {
		ids = append(ids, entry.ThoughtID)
	}

	// Resolve the conversation to attach this greeting to. If the caller
	// didn't provide a hint, use the most recent conversation. If there is
	// no conversation yet, mint a new one.
	convID := strings.TrimSpace(convIDHint)
	if convID == "" {
		if recent, err := s.db.ListConversations(1); err == nil && len(recent) > 0 {
			convID = recent[0].ID
		}
	}
	if convID == "" {
		convID = newUUID()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := s.db.SaveConversation(convID, now, now, "web", nil); err != nil {
			return GreetingResponse{}, fmt.Errorf("create conversation: %w", err)
		}
	}

	// Resolve provider.
	cfg := s.cfgStore.Load()
	provider, err := ResolveHeavyBackgroundProvider(cfg)
	if err != nil {
		tel.Emit(telemetry.KindGreetingSkipped, "", convID, map[string]any{
			"reason": "no_provider",
			"error":  err.Error(),
		})
		return GreetingResponse{Delivered: false, Skipped: "no_provider"}, nil
	}

	// Build the prompt.
	system, user, userName := buildGreetingPrompt(queue, cfg)

	// Call the model. Non-streaming — greetings are short enough that a
	// single round trip beats the complexity of token streaming.
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	content, err := callGreetingModel(callCtx, provider, system, user)
	if err != nil {
		tel.Emit(telemetry.KindGreetingSkipped, "", convID, map[string]any{
			"reason": "model_failed",
			"error":  err.Error(),
		})
		logstore.Write("warn", "greeting: model call failed: "+err.Error(), nil)
		return GreetingResponse{Delivered: false, Skipped: "model_failed"}, nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		tel.Emit(telemetry.KindGreetingSkipped, "", convID, map[string]any{"reason": "empty_output"})
		return GreetingResponse{Delivered: false, Skipped: "empty_output"}, nil
	}

	// Persist as a normal assistant message. Phase 6 can add a metadata
	// column or a schema change if the UI needs to distinguish greetings
	// in historical scroll.
	msgID := newUUID()
	ts := time.Now().UTC()
	tsStr := ts.Format(time.RFC3339Nano)
	if err := s.db.SaveMessage(msgID, convID, "assistant", content, tsStr); err != nil {
		return GreetingResponse{}, fmt.Errorf("save greeting message: %w", err)
	}

	// Phase 7b: surface detection for greetings too. The greeting prompt
	// instructs the model to mention thoughts with trailing [T-NN]
	// markers, so we need to capture them here the same way we do in
	// the normal turn path.
	s.detectAndRecordSurfacings(convID, msgID, content, ts)

	// Emit SSE so a live client sees the greeting stream in. Using the
	// same assistant_started/assistant_delta/done pattern as the main chat path.
	s.broadcaster.Emit(convID, SSEEvent{
		Type:           "assistant_started",
		Role:           "assistant",
		ConversationID: convID,
	})
	s.broadcaster.Emit(convID, SSEEvent{
		Type:           "assistant_delta",
		Content:        content,
		Role:           "assistant",
		ConversationID: convID,
	})
	s.broadcaster.Emit(convID, SSEEvent{
		Type:           "assistant_done",
		Role:           "assistant",
		ConversationID: convID,
	})
	s.broadcaster.Emit(convID, SSEEvent{
		Type:           "done",
		Status:         "completed",
		ConversationID: convID,
	})

	// Clear the queue now that the greeting is safely persisted.
	if err := mind.ClearPendingGreetings(config.SupportDir()); err != nil {
		logstore.Write("warn", "greeting: failed to clear pending queue: "+err.Error(), nil)
	}

	tel.Emit(telemetry.KindGreetingDelivered, "", convID, map[string]any{
		"thought_ids":    ids,
		"thought_count":  len(ids),
		"message_id":     msgID,
		"content_length": len(content),
		"user_name":      userName,
	})

	return GreetingResponse{
		Delivered:      true,
		ConversationID: convID,
		MessageID:      msgID,
		Content:        content,
		ThoughtIDs:     ids,
		DeliveredAt:    ts,
	}, nil
}

// buildGreetingPrompt constructs (system, user, userName) for the greeting
// one-shot call. userName is best-effort — pulled from config or defaulted
// to "friend" so the model has something to address.
func buildGreetingPrompt(queue []mind.GreetingEntry, cfg config.RuntimeConfigSnapshot) (string, string, string) {
	userName := greetingUserName(cfg)

	system := `You are Atlas, the operator. The user just opened the chat after being away.

While they were gone, you acted on one or more of your active thoughts and
got results back. Your job now is to greet them by name, tell them what
you did and what you found, and ask a light follow-up only if it makes
sense naturally.

Rules:
- Warm and personal. Not apologetic. This is a gift, not a confession.
- Address them by name in the opening.
- Bundle all findings into ONE message — do not list them as bullets
  unless there are three or more distinct findings.
- Keep it under 150 words.
- Match your normal tone. Do not be overly formal.
- End with a natural follow-up question or offer only if it fits — never
  force one.
- Never say "I took an action without your permission" or similar.
- Never apologize for acting. You were asked to tend your thoughts, and
  you did.`

	var b strings.Builder
	fmt.Fprintf(&b, "The user's name is %s.\n\n", userName)
	b.WriteString("You acted on these thoughts while they were away:\n\n")
	for i, entry := range queue {
		fmt.Fprintf(&b, "--- Thought %d ---\n", i+1)
		fmt.Fprintf(&b, "Original thought: %s\n", entry.Body)
		if entry.Provenance != "" {
			fmt.Fprintf(&b, "Why it came up: %s\n", entry.Provenance)
		}
		fmt.Fprintf(&b, "Skill you ran: %s\n", entry.SkillID)
		fmt.Fprintf(&b, "Result:\n%s\n\n", truncateForGreeting(entry.Result, 2000))
	}
	b.WriteString("Write the greeting message now. One message, warm, direct. No JSON, no fences, just the message body.")

	return system, b.String(), userName
}

// greetingUserName best-effort pulls a name from the config. Returns
// "friend" when nothing is configured — harmless fallback.
func greetingUserName(cfg config.RuntimeConfigSnapshot) string {
	// The runtime config doesn't currently carry a user name field. If
	// one is added later, pull from it here. For now, Atlas's MIND.md
	// typically mentions the user by name in the Identity section, but we
	// don't want to parse that here — the greeting prompt addresses them
	// as "friend" and the model will usually override that with what it
	// knows from the MIND block in its system prompt already.
	return "friend"
}

// truncateForGreeting keeps result payloads under a sensible cap so the
// greeting prompt doesn't blow past the model context.
func truncateForGreeting(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}

// callGreetingModel makes one non-streaming provider call with no tools.
// Mirrors callFast in reflection.go but lives here to avoid cross-file
// coupling and to let the greeting path evolve independently.
func callGreetingModel(ctx context.Context, provider agent.ProviderConfig, system, user string) (string, error) {
	messages := []agent.OAIMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	reply, _, _, err := agent.CallAINonStreamingExported(ctx, provider, messages, nil)
	if err != nil {
		return "", err
	}
	if s, ok := reply.Content.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", reply.Content), nil
}

// ── GreetingQueuer adapter ───────────────────────────────────────────────────

// ChatGreetingQueuer adapts the chat service to the mind.GreetingQueuer
// interface so the dispatcher can hand off results to us. The adapter
// simply delegates to the on-disk queue used by LoadPendingGreetings —
// the chat service does not own the queue directly; it just drains it.
type ChatGreetingQueuer struct {
	supportDir string
}

// NewChatGreetingQueuer returns an adapter that the dispatcher uses to
// enqueue acted-on-thought results. Writes go to pending-greetings.json
// via the same path the chat greeting handler reads from.
func NewChatGreetingQueuer(supportDir string) *ChatGreetingQueuer {
	return &ChatGreetingQueuer{supportDir: supportDir}
}

// EnqueueGreeting appends an entry to pending-greetings.json. Implements
// mind.GreetingQueuer.
func (q *ChatGreetingQueuer) EnqueueGreeting(entry mind.GreetingEntry) error {
	return mind.AppendPendingGreeting(q.supportDir, entry)
}

// jsonMustMarshal is a tiny helper used in telemetry payloads that should
// never panic even on malformed input. Kept here to keep greeting.go
// self-contained.
func jsonMustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `"(marshal error)"`
	}
	return string(b)
}
