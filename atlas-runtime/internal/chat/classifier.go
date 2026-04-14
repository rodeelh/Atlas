package chat

// classifier.go implements Phase 7c: the engagement classifier. When a
// user message arrives and a pending thought surfacing exists in the
// same conversation within a short window, we run a one-shot JSON
// prompt against the cheapest available provider to decide whether
// the user's reply was:
//
//   - positive  (engaged with the surfacing warmly or asked to act)
//   - negative  (dismissed, pushed back, said stop/drop/not now)
//   - ignored   (replied about something else entirely)
//
// The classifier result rewrites the pending row in thought-engagement.jsonl
// via thoughts.MarkSurfacingClassified. Telemetry emits engagement_recorded
// with the signal, confidence, and the raw user message for later
// hand-grading during the few-day review.
//
// Design notes:
//
//  * The prompt is intentionally JSON-shaped so we get a confidence
//    score and a one-line reasoning string for hand-grading accuracy.
//    Parsing is tolerant to code fences and leading prose the way the
//    nap envelope parser is.
//
//  * Runs inline on the HandleMessage path, but only when a pending
//    surfacing actually exists. On a quiet day with no thoughts this
//    is a single disk read to check the sidecar and then nothing.
//
//  * The classifier call is bounded by a 15s timeout. A slow classifier
//    call should never block the main chat turn — if it times out, the
//    pending row stays pending and the next nap will eventually decay
//    it to ignored.
//
//  * No provider fallback cascade: if the fast provider is unavailable,
//    we log and skip classification. The nap's 24h expiry is the
//    safety net.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/mind/thoughts"
)

// classifierTimeout bounds the classifier round trip. Short enough that
// a misbehaving provider doesn't stall the user's next turn.
const classifierTimeout = 15 * time.Second

// engagementWindow is how long a pending surfacing stays eligible for
// inline classification. Surfacings older than this get handled by the
// nap's expiry sweep instead.
const engagementWindow = 6 * time.Hour

// ClassifierTelemetry is the narrow interface the classifier path uses
// to emit telemetry. Same shape as GreetingTelemetry so callers can
// inject a single backend for both.
type ClassifierTelemetry interface {
	Emit(kind telemetry.Kind, thoughtID, convID string, payload any)
}

// classifierResult is the parsed JSON envelope returned by the model.
// Confidence is 0-100; reasoning is a one-line trace for hand-grading.
type classifierResult struct {
	Signal     string `json:"signal"`
	Confidence int    `json:"confidence"`
	Reasoning  string `json:"reasoning"`
}

// classifyPendingIfAny is called from HandleMessage at the START of a
// user turn. It looks for a pending surfacing in the current conversation,
// runs the classifier against the user message if one exists, and
// rewrites the sidecar row. Fully non-blocking from the caller's
// perspective — runs in a background goroutine so the main turn
// proceeds in parallel.
//
// Args:
//   - ctx: detached from the HTTP request so the classifier can finish
//     after the response stream ends
//   - convID: the current conversation id
//   - userMessage: the raw text of the user's new turn
//   - thoughtBodyLookup: returns the body of a thought by id. Pulled
//     from MIND.md at call time so the classifier can compare the user
//     message against the thought's actual content.
func (s *Service) classifyPendingIfAny(
	ctx context.Context,
	convID, userMessage string,
	thoughtBodyLookup func(id string) string,
) {
	if convID == "" || userMessage == "" {
		return
	}
	// Master gate. If thoughts are off, skip the sidecar read entirely —
	// no disk access, no provider call, no surprise classifier running.
	if !s.thoughtsEnabled() {
		return
	}

	// Cheap sidecar read — no provider call yet.
	pending, err := thoughts.FindPendingSurfacingInConv(
		config.SupportDir(), convID, engagementWindow,
	)
	if err != nil {
		logstore.Write("warn", "classifier: find pending: "+err.Error(),
			map[string]string{"conv": shortConvID(convID)})
		return
	}
	if pending == nil {
		return
	}

	// Resolve body for the thought. If the thought has been discarded
	// since it was surfaced (a nap ran in between), skip — there's
	// nothing to reinforce.
	body := ""
	if thoughtBodyLookup != nil {
		body = thoughtBodyLookup(pending.ThoughtID)
	}
	if body == "" {
		logstore.Write("info",
			"classifier: skipping — surfaced thought no longer exists",
			map[string]string{"thought": pending.ThoughtID})
		return
	}

	cfg := s.cfgStore.Load()
	provider, err := ResolveFastProvider(cfg)
	if err != nil {
		logstore.Write("warn", "classifier: no fast provider: "+err.Error(), nil)
		return
	}

	go s.runClassifier(
		ctx, provider, *pending, body, userMessage,
	)
}

// runClassifier runs the one-shot classifier call, parses the envelope,
// rewrites the sidecar row, and emits telemetry. Runs in a background
// goroutine; errors are logged but not propagated.
func (s *Service) runClassifier(
	ctx context.Context,
	provider agent.ProviderConfig,
	pending thoughts.Event,
	thoughtBody, userMessage string,
) {
	tel := s.ensureGreetingTelemetry()

	cctx, cancel := context.WithTimeout(ctx, classifierTimeout)
	defer cancel()

	system, user := buildClassifierPrompt(thoughtBody, userMessage)
	raw, err := callClassifierModel(cctx, provider, system, user)
	if err != nil {
		logstore.Write("warn", "classifier: model call failed: "+err.Error(),
			map[string]string{
				"thought": pending.ThoughtID,
				"conv":    shortConvID(pending.ConvID),
			})
		return
	}

	parsed, err := parseClassifierEnvelope(raw)
	if err != nil {
		logstore.Write("warn", "classifier: parse envelope: "+err.Error(),
			map[string]string{"thought": pending.ThoughtID})
		return
	}

	signal := normalizeClassifierSignal(parsed.Signal)
	if !signal.IsTerminal() {
		logstore.Write("warn", "classifier: non-terminal signal from model",
			map[string]string{"signal": parsed.Signal, "thought": pending.ThoughtID})
		return
	}

	if err := thoughts.MarkSurfacingClassified(
		config.SupportDir(),
		pending.SurfacingID,
		signal,
		parsed.Confidence,
		parsed.Reasoning,
		userMessage,
		time.Now().UTC(),
	); err != nil {
		logstore.Write("warn", "classifier: mark failed: "+err.Error(),
			map[string]string{"surfacing": pending.SurfacingID})
		return
	}

	tel.Emit(telemetry.KindEngagementRecorded, pending.ThoughtID, pending.ConvID, map[string]any{
		"surfacing_id":          pending.SurfacingID,
		"signal":                string(signal),
		"classifier_confidence": parsed.Confidence,
		"classifier_reasoning":  parsed.Reasoning,
		"user_message_preview":  truncateForTelemetry(userMessage, 200),
	})

	logstore.Write("info",
		fmt.Sprintf("engagement classified: %s → %s (conf %d)", pending.ThoughtID, signal, parsed.Confidence),
		map[string]string{
			"thought": pending.ThoughtID,
			"signal":  string(signal),
		})
}

// buildClassifierPrompt constructs the (system, user) pair for the
// one-shot classifier call. JSON output, strict format, one-line
// reasoning for hand-grading.
func buildClassifierPrompt(thoughtBody, userMessage string) (string, string) {
	system := `You classify whether a user's chat reply engages with a thought Atlas
surfaced in the previous assistant turn. You return ONE JSON object and
nothing else — no prose, no code fences.

Classification options:

  - "positive": the user engaged warmly with the thought. They asked to
    hear more, said yes, asked Atlas to act on it, expressed interest,
    or continued the topic naturally.

  - "negative": the user dismissed, pushed back, or told Atlas to stop
    or drop it. Includes "not now", "ignore that", "no thanks", or
    expressions of disinterest.

  - "ignored": the user replied about something else entirely and did
    not acknowledge the thought at all. Their message is unrelated to
    the thought.

Confidence is a 0-100 integer: your honest belief in the classification.
Reasoning is a SHORT one-line string (under 30 words) explaining why.

Output shape, exact:

{
  "signal": "positive" | "negative" | "ignored",
  "confidence": <integer 0-100>,
  "reasoning": "<one line>"
}`

	user := fmt.Sprintf(`THOUGHT ATLAS RAISED:
%s

USER'S REPLY:
%s

Classify. Return the JSON object only.`,
		truncateForClassifier(thoughtBody, 500),
		truncateForClassifier(userMessage, 500),
	)

	return system, user
}

// callClassifierModel makes the one-shot non-streaming call. Mirrors
// callGreetingModel's shape.
func callClassifierModel(ctx context.Context, provider agent.ProviderConfig, system, user string) (string, error) {
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

// parseClassifierEnvelope extracts the JSON from the model's output.
// Tolerates code fences and leading prose — same strategy as the nap
// envelope parser.
func parseClassifierEnvelope(raw string) (classifierResult, error) {
	trimmed := strings.TrimSpace(raw)

	// Strip code fences if present.
	if strings.HasPrefix(trimmed, "```") {
		if nl := strings.IndexByte(trimmed, '\n'); nl != -1 {
			trimmed = trimmed[nl+1:]
		}
		if closeIdx := strings.LastIndex(trimmed, "```"); closeIdx != -1 {
			trimmed = trimmed[:closeIdx]
		}
		trimmed = strings.TrimSpace(trimmed)
	}

	// Find first { and scan for the matching close.
	start := strings.IndexByte(trimmed, '{')
	if start == -1 {
		return classifierResult{}, fmt.Errorf("no JSON object in output")
	}
	depth := 0
	end := -1
	inString := false
	escape := false
	for i := start; i < len(trimmed); i++ {
		c := trimmed[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return classifierResult{}, fmt.Errorf("unterminated JSON")
	}

	var r classifierResult
	if err := json.Unmarshal([]byte(trimmed[start:end]), &r); err != nil {
		return classifierResult{}, fmt.Errorf("unmarshal: %w", err)
	}
	return r, nil
}

// normalizeClassifierSignal maps the model's string output to a typed
// Signal value. Lowercases and trims first so small formatting drifts
// don't reject otherwise-valid responses.
func normalizeClassifierSignal(s string) thoughts.Signal {
	normalized := strings.ToLower(strings.TrimSpace(s))
	switch normalized {
	case "positive", "pos":
		return thoughts.SignalPositive
	case "negative", "neg":
		return thoughts.SignalNegative
	case "ignored", "ignore":
		return thoughts.SignalIgnored
	}
	return thoughts.Signal(normalized) // will fail IsTerminal
}

// truncateForClassifier caps strings fed into the classifier prompt so
// one long user message doesn't blow the prompt budget.
func truncateForClassifier(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}

// truncateForTelemetry caps the user message preview that lands in
// telemetry. The full raw message is NOT persisted anywhere else.
func truncateForTelemetry(s string, n int) string {
	return truncateForClassifier(s, n)
}
