// Package mind — nap.go implements the nap cycle: a lightweight reflective
// pass that tends Atlas's THOUGHTS section between user turns.
//
// A nap is NOT an action. It cannot call skills. It cannot send messages.
// It takes a model turn of input (recent conversation, MIND.md, DIARY, top
// memories, engagement events, installed skills list), asks the model to
// curate the active thoughts, parses a structured JSON envelope of ops, and
// applies them via thoughts.Apply. The entire side-effect surface is writes
// to MIND.md (THOUGHTS section only) and telemetry rows.
//
// Naps share the MIND.md write lock with reflection and the dream cycle.
// A nap that times out waiting for the lock is logged and skipped.
//
// See project_mind_thoughts.md (auto-memory) for the full design spec and
// the literal prompt template used here.
package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/mind/thoughts"
	"atlas-runtime-go/internal/storage"
)

// NapTrigger identifies why a nap was fired. Recorded in telemetry so we
// can see the idle/floor/manual breakdown after a few days of real use.
type NapTrigger string

const (
	TriggerManual NapTrigger = "manual" // POST /mind/nap
	TriggerIdle   NapTrigger = "idle"   // phase 3 — idle gap fired
	TriggerFloor  NapTrigger = "floor"  // phase 3 — soft daily floor
)

// NapDeps bundles everything a nap needs to run. Passed in rather than held
// globally so the manual trigger and the future scheduler can share the same
// runNap implementation.
type NapDeps struct {
	SupportDir string
	DB         *storage.DB
	Provider   agent.ProviderConfig
	Cfg        config.RuntimeConfigSnapshot
	Telemetry  *telemetry.Emitter
	// SkillsList is a short list of "id — description" lines for the model's
	// awareness of what Atlas can act on later. Pure input — no calls.
	SkillsList []string
	// Dispatcher is optional — when set, RunNap walks the post-apply thoughts
	// list and routes any that cross auto-execute or propose thresholds.
	// When nil, the dispatch phase is skipped entirely (dark-launch mode).
	Dispatcher *Dispatcher
}

// NapResult summarises what a nap did. Returned from RunNap for the manual
// trigger endpoint and captured in telemetry for the nap_completed event.
type NapResult struct {
	Trigger      NapTrigger          `json:"trigger"`
	DurationMs   int64               `json:"duration_ms"`
	InputTokens  int                 `json:"input_tokens"`  // approximate
	OutputTokens int                 `json:"output_tokens"` // approximate
	OpsRequested int                 `json:"ops_requested"`
	OpsApplied   int                 `json:"ops_applied"`
	OpsRejected  int                 `json:"ops_rejected"`
	Rationale    string              `json:"rationale"`
	Before       int                 `json:"thoughts_before"`
	After        int                 `json:"thoughts_after"`
	OpsBreakdown map[string]int      `json:"ops_breakdown"` // add, update, reinforce, discard, merge counts
	Error        string              `json:"error,omitempty"`
	Results      []thoughts.OpResult `json:"results,omitempty"`
	Dispatch     *DispatchResult     `json:"dispatch,omitempty"`
}

// Nap token budgets. These are the defaults; phase 3 wires them to
// RuntimeConfigSnapshot so they can be tuned live.
const (
	napInputBudget         = 4000 // approximate chars of input context, NOT tokens
	napOutputBudget        = 1000 // output tokens cap for the model call
	napTimeout             = 120 * time.Second
	napLockTimeout         = 30 * time.Second
	dispatcherPhaseTimeout = 90 * time.Second // hard cap on the whole dispatch phase

	// napPendingExpiry is how long a pending engagement surfacing can
	// sit unclassified before the nap decays it to ignored. Matches the
	// spec's 24h window: a user who hasn't replied by the next day has
	// effectively ignored the surfacing.
	napPendingExpiry = 24 * time.Hour

	// Nap input limits (items, not tokens).
	napRecentTurnCount  = 10
	napDiaryEntryCount  = 5
	napMemoryCount      = 10
	napEngagementWindow = 7 * 24 * time.Hour
)

// RunNap executes one nap cycle. Blocks until the nap completes, the lock
// times out, or the context is cancelled. Returns a NapResult describing
// what happened. Never panics — failures are captured in result.Error and
// telemetry.
//
// The caller is responsible for deciding when naps may fire (the idle/cap
// scheduler in phase 3). This function just runs one nap.
func RunNap(ctx context.Context, deps NapDeps, trigger NapTrigger) NapResult {
	start := time.Now()
	result := NapResult{
		Trigger:      trigger,
		OpsBreakdown: map[string]int{},
	}

	// Emit nap_started immediately so telemetry captures attempts, not just
	// successes. The payload is fleshed out once we've gathered inputs.
	deps.Telemetry.Emit(telemetry.KindNapStarted, "", "", map[string]any{
		"trigger": string(trigger),
	})

	// Build the full input context first. This involves disk reads but no
	// AI calls, so it's quick and safe to fail fast.
	inputs, convID, err := gatherNapInputs(deps)
	if err != nil {
		result.Error = fmt.Sprintf("gather inputs: %v", err)
		result.DurationMs = time.Since(start).Milliseconds()
		deps.Telemetry.Emit(telemetry.KindNapFailed, "", "", map[string]any{
			"trigger": string(trigger),
			"error":   result.Error,
			"phase":   "gather_inputs",
		})
		logstore.Write("warn", "nap: gather inputs failed: "+err.Error(), nil)
		return result
	}
	result.Before = len(inputs.CurrentThoughts)

	// Build the prompt. System + user messages. Cheap.
	system, user := buildNapPrompt(inputs, deps.SkillsList)
	result.InputTokens = approxTokens(system) + approxTokens(user)

	// Call the model. This is the main cost and the main failure mode.
	ctx, cancel := context.WithTimeout(ctx, napTimeout)
	defer cancel()

	rawOutput, err := callNapModel(ctx, deps.Provider, system, user)
	if err != nil {
		result.Error = fmt.Sprintf("model call: %v", err)
		result.DurationMs = time.Since(start).Milliseconds()
		deps.Telemetry.Emit(telemetry.KindNapFailed, "", convID, map[string]any{
			"trigger": string(trigger),
			"error":   result.Error,
			"phase":   "model_call",
		})
		logstore.Write("warn", "nap: model call failed: "+err.Error(), nil)
		return result
	}
	result.OutputTokens = approxTokens(rawOutput)

	// Parse the JSON envelope. The model sometimes wraps JSON in prose or
	// markdown fences — tolerate those.
	envelope, err := parseNapEnvelope(rawOutput)
	if err != nil {
		result.Error = fmt.Sprintf("parse envelope: %v", err)
		result.DurationMs = time.Since(start).Milliseconds()
		// Truncate raw output for telemetry — don't drag huge blobs in.
		raw := rawOutput
		if len(raw) > 4000 {
			raw = raw[:4000] + "…"
		}
		deps.Telemetry.Emit(telemetry.KindNapFailed, "", convID, map[string]any{
			"trigger":    string(trigger),
			"error":      result.Error,
			"phase":      "parse_envelope",
			"raw_output": raw,
		})
		logstore.Write("warn", "nap: parse envelope failed: "+err.Error(), nil)
		return result
	}
	result.Rationale = envelope.Rationale
	result.OpsRequested = len(envelope.Ops)

	// Apply the ops. Holds the MIND.md write lock for the read-modify-write
	// cycle. If the lock can't be acquired within napLockTimeout, the nap
	// is skipped gracefully.
	now := time.Now().UTC()
	var applied []thoughts.OpResult
	var finalList []thoughts.Thought
	lockErr := UpdateThoughtsSectionWithTimeout(ctx, deps.SupportDir, napLockTimeout,
		func(current []thoughts.Thought) ([]thoughts.Thought, error) {
			out, results, err := thoughts.Apply(current, envelope.Ops, now)
			if err != nil {
				return nil, err
			}
			applied = results
			finalList = out
			return out, nil
		})
	if lockErr != nil {
		result.Error = fmt.Sprintf("apply: %v", lockErr)
		result.DurationMs = time.Since(start).Milliseconds()
		deps.Telemetry.Emit(telemetry.KindNapFailed, "", convID, map[string]any{
			"trigger": string(trigger),
			"error":   result.Error,
			"phase":   "apply",
		})
		logstore.Write("warn", "nap: apply failed: "+lockErr.Error(), nil)
		return result
	}
	result.After = len(finalList)
	result.Results = applied
	emitOpTelemetry(deps.Telemetry, applied, finalList, convID, &result)

	// Dispatch phase — walks the post-apply list and acts on any thoughts
	// that cross the auto-execute or propose thresholds. Only runs when a
	// dispatcher is wired; absence = dark-launch mode.
	if deps.Dispatcher != nil {
		// Use a fresh context for dispatch so skill calls don't share the
		// (potentially expired) nap model-call deadline.
		dispCtx, dispCancel := context.WithTimeout(context.Background(), dispatcherPhaseTimeout)
		dr := deps.Dispatcher.Dispatch(dispCtx, finalList,
			deps.Cfg.ThoughtMaxAutoExecPerNap, deps.Cfg.ThoughtMaxAutoExecPerDay, convID)
		dispCancel()
		result.Dispatch = &dr
	}

	// Success.
	result.DurationMs = time.Since(start).Milliseconds()
	deps.Telemetry.Emit(telemetry.KindNapCompleted, "", convID, map[string]any{
		"trigger":         string(trigger),
		"duration_ms":     result.DurationMs,
		"input_tokens":    result.InputTokens,
		"output_tokens":   result.OutputTokens,
		"ops_requested":   result.OpsRequested,
		"ops_applied":     result.OpsApplied,
		"ops_rejected":    result.OpsRejected,
		"ops_breakdown":   result.OpsBreakdown,
		"thoughts_before": result.Before,
		"thoughts_after":  result.After,
		"rationale":       result.Rationale,
	})
	logstore.Write("info",
		fmt.Sprintf("nap: done in %dms — %d ops requested, %d applied, %d→%d thoughts",
			result.DurationMs, result.OpsRequested, result.OpsApplied, result.Before, result.After),
		map[string]string{"trigger": string(trigger)})
	return result
}

// NapInputs is the full payload gathered for a single nap before the prompt
// is built. Kept as a struct so tests can construct one directly and the
// prompt builder stays a pure function.
type NapInputs struct {
	CurrentThoughts  []thoughts.Thought
	RecentTurns      []TurnRecord
	MindMD           string
	DiaryContext     string
	RelevantMemories []storage.MemoryRow
	EngagementEvents []thoughts.Event
	Timestamp        time.Time
}

// gatherNapInputs reads the state the nap needs from disk, SQLite, and the
// engagement sidecar. Returns the inputs plus the most recent conversation
// id (for telemetry correlation).
func gatherNapInputs(deps NapDeps) (NapInputs, string, error) {
	inputs := NapInputs{
		Timestamp: time.Now().UTC(),
	}

	// Current THOUGHTS section (no lock needed — read-only use).
	list, err := ReadThoughtsSection(deps.SupportDir)
	if err != nil {
		return inputs, "", fmt.Errorf("read thoughts section: %w", err)
	}
	inputs.CurrentThoughts = list

	// MIND.md raw content.
	mindPath := filepath.Join(deps.SupportDir, "MIND.md")
	mindBytes, err := os.ReadFile(mindPath)
	if err == nil {
		inputs.MindMD = string(mindBytes)
	}

	// DIARY context — last 5 entries.
	inputs.DiaryContext = features.DiaryContext(deps.SupportDir, napDiaryEntryCount)

	// Recent conversation turns from the most recent active conversation.
	var recentConvID string
	if deps.DB != nil {
		convs, cerr := deps.DB.ListConversations(1)
		if cerr == nil && len(convs) > 0 {
			recentConvID = convs[0].ID
			msgs, merr := deps.DB.ListMessages(recentConvID)
			if merr == nil {
				inputs.RecentTurns = messagesToTurns(msgs, napRecentTurnCount)
			}
		}
	}

	// BM25 memories relevant to recent conversation context.
	if deps.DB != nil && len(inputs.RecentTurns) > 0 {
		queryParts := []string{}
		for _, t := range inputs.RecentTurns {
			queryParts = append(queryParts, t.UserMessage)
		}
		query := strings.Join(queryParts, " ")
		if len(query) > 2000 {
			query = query[:2000]
		}
		memories, merr := deps.DB.RelevantMemories(query, napMemoryCount, nil)
		if merr == nil {
			inputs.RelevantMemories = memories
		}
	}

	// Phase 7d: expire any pending surfacings that are older than the
	// stale cutoff BEFORE reading engagement events. A pending
	// surfacing that's been sitting unclassified past the window means
	// the user never replied to it — the correct interpretation is
	// "ignored". This runs in-place on the sidecar so both the nap's
	// keep test AND future naps see the updated terminal state.
	//
	// The cutoff is napPendingExpiry (24h by default per the spec).
	// Runs before RecentEvents so the returned events already reflect
	// the decay.
	expiryCutoff := time.Now().UTC().Add(-napPendingExpiry)
	if n, err := thoughts.MarkSurfacingIgnoredIfExpired(deps.SupportDir, expiryCutoff); err != nil {
		logstore.Write("warn", "nap: expire stale pending surfacings failed: "+err.Error(), nil)
	} else if n > 0 {
		logstore.Write("info", fmt.Sprintf("nap: expired %d stale pending surfacings to ignored", n), nil)
	}

	// Engagement events, last 7 days.
	since := time.Now().UTC().Add(-napEngagementWindow)
	events, _, err := thoughts.RecentEvents(deps.SupportDir, since)
	if err == nil {
		inputs.EngagementEvents = events
	}

	return inputs, recentConvID, nil
}

// messagesToTurns collapses raw message rows into TurnRecord turns. Pairs
// adjacent user + assistant messages into one turn. Returns the most recent
// `limit` turns in chronological order (oldest first).
func messagesToTurns(msgs []storage.MessageRow, limit int) []TurnRecord {
	var turns []TurnRecord
	var cur TurnRecord
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if cur.UserMessage != "" || cur.AssistantResponse != "" {
				turns = append(turns, cur)
			}
			cur = TurnRecord{ConversationID: m.ConversationID, UserMessage: m.Content}
			if ts, err := time.Parse(time.RFC3339, m.Timestamp); err == nil {
				cur.Timestamp = ts
			}
		case "assistant":
			cur.AssistantResponse = m.Content
		}
	}
	if cur.UserMessage != "" || cur.AssistantResponse != "" {
		turns = append(turns, cur)
	}
	if len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return turns
}

// approxTokens is a cheap rune/4 heuristic. Not accurate per model, but
// close enough for telemetry — we want "order of magnitude", not tokenizer
// precision.
func approxTokens(s string) int {
	return len([]rune(s)) / 4
}

// emitOpTelemetry walks the Apply results and emits one telemetry row per
// applied op. Also fills in result.OpsApplied, OpsRejected, and OpsBreakdown.
func emitOpTelemetry(em *telemetry.Emitter, results []thoughts.OpResult, finalList []thoughts.Thought, convID string, napResult *NapResult) {
	// Build a lookup from id → final Thought (for add/update/merge we want
	// to record the resulting state, not just the op that produced it).
	byID := make(map[string]thoughts.Thought, len(finalList))
	for _, t := range finalList {
		byID[t.ID] = t
	}

	for _, r := range results {
		if r.Outcome == "applied" {
			napResult.OpsApplied++
			napResult.OpsBreakdown[string(r.Op.Kind)]++
		} else if r.Outcome == "rejected_invalid" || r.Outcome == "rejected_missing" {
			napResult.OpsRejected++
		}

		// Choose the kind based on op kind + outcome.
		var kind telemetry.Kind
		switch r.Op.Kind {
		case thoughts.OpAdd:
			kind = telemetry.KindThoughtAdded
		case thoughts.OpUpdate:
			kind = telemetry.KindThoughtUpdated
		case thoughts.OpReinforce:
			kind = telemetry.KindThoughtReinforced
		case thoughts.OpDiscard:
			kind = telemetry.KindThoughtDiscarded
		case thoughts.OpMerge:
			kind = telemetry.KindThoughtMerged
		default:
			continue
		}

		payload := map[string]any{
			"outcome": r.Outcome,
		}
		if r.Error != "" {
			payload["error"] = r.Error
		}
		if t, ok := byID[r.ThoughtID]; ok {
			payload["body"] = t.Body
			payload["score"] = t.Score
			payload["class"] = string(t.Class)
			payload["confidence"] = t.Confidence
			payload["value"] = t.Value
			payload["source"] = t.Source
			payload["provenance"] = t.Provenance
			if t.Action != nil {
				payload["action"] = t.Action
			}
		} else {
			// For discards, the thought is gone — record what we can from the op.
			payload["body"] = r.Op.Body
			payload["class"] = string(r.Op.Class)
		}
		em.Emit(kind, r.ThoughtID, convID, payload)
	}
}

// parseNapEnvelope extracts the JSON envelope from the model's raw output.
// Tolerates code fences (```json … ```), leading prose, and trailing content.
func parseNapEnvelope(raw string) (thoughts.Envelope, error) {
	trimmed := strings.TrimSpace(raw)

	// Strip code fences if present.
	if strings.HasPrefix(trimmed, "```") {
		// Find the first line break after the fence — that's where the JSON
		// starts. Find the closing fence — that's where it ends.
		if nl := strings.IndexByte(trimmed, '\n'); nl != -1 {
			trimmed = trimmed[nl+1:]
		}
		if closeIdx := strings.LastIndex(trimmed, "```"); closeIdx != -1 {
			trimmed = trimmed[:closeIdx]
		}
		trimmed = strings.TrimSpace(trimmed)
	}

	// Find the first `{` and scan for the matching closing brace.
	start := strings.IndexByte(trimmed, '{')
	if start == -1 {
		return thoughts.Envelope{}, fmt.Errorf("no JSON object found in output")
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
		return thoughts.Envelope{}, fmt.Errorf("unterminated JSON object")
	}

	var env thoughts.Envelope
	if err := json.Unmarshal([]byte(trimmed[start:end]), &env); err != nil {
		return thoughts.Envelope{}, fmt.Errorf("unmarshal: %w", err)
	}
	return env, nil
}

// callNapModel makes a single non-streaming AI call for the nap and returns
// the raw text output. The caller parses the JSON envelope.
func callNapModel(ctx context.Context, provider agent.ProviderConfig, system, user string) (string, error) {
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
