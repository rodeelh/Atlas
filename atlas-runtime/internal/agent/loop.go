// Package agent implements the multi-turn agent loop with tool execution,
// approval deferral, and conversation resumption.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

// Emitter is the interface the agent loop uses to emit SSE events.
// chat.Broadcaster implements this interface — using an interface avoids
// a circular import between agent ↔ chat.
type Emitter interface {
	Emit(convID string, event EmitEvent)
	Finish(convID string)
}

// EmitEvent carries the fields needed to build an SSE event.
type EmitEvent struct {
	Type       string
	Content    string
	Role       string
	ConvID     string
	ToolName   string
	ToolCallID string
	ApprovalID string
	Arguments  string
	Error      string
	Status     string
}

// OAIMessage is an OpenAI chat message.
// Content is `any` because it can be null for tool-call assistant messages.
type OAIMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCalls  []OAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

// OAIToolCall represents one tool call in an assistant message.
// ExtraContent carries provider-specific metadata that must be echoed back
// verbatim. For Gemini thinking models this holds the thought_signature.
type OAIToolCall struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Function     OAIFunctionCall    `json:"function"`
	ExtraContent *OAIToolCallExtras `json:"extra_content,omitempty"`
}

// OAIToolCallExtras mirrors the extra_content structure Gemini returns and
// expects back on subsequent requests.
type OAIToolCallExtras struct {
	Google OAIToolCallGoogle `json:"google,omitempty"`
}

// OAIToolCallGoogle holds the Gemini-specific thought_signature.
type OAIToolCallGoogle struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// OAIFunctionCall holds the function name and JSON-encoded arguments.
type OAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// PendingApproval describes one deferred tool call waiting for user approval.
type PendingApproval struct {
	DeferredID  string
	ApprovalID  string
	ToolCallID  string
	ActionID    string
	Arguments   string
	PermLevel   string
	ActionClass string // canonical action class for the approval UI
}

// RunResult is returned by Loop.Run().
type RunResult struct {
	FinalText           string
	Status              string // "complete", "pendingApproval", "error"
	PendingApprovals    []PendingApproval
	Error               error
	TotalUsage          TokenUsage
	ToolCallSummaries   []string // tool names called during this turn (all iterations)
	ToolResultSummaries []string // short result summaries, one per tool call
}

// requestToolsName is the internal action ID for the lazy-mode meta-tool.
// The model calls this when it determines it needs real capabilities; the loop
// intercepts the call, upgrades the tool set, and continues transparently.
const requestToolsName = "request_tools"

// RequestToolsDef returns the single meta-tool injected in "lazy" tool selection
// mode. It costs ~100 input tokens vs ~2,600 for the 26-tool heuristic baseline,
// and lets the model opt-in to real tools only when it actually needs them.
func RequestToolsDef() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": requestToolsName,
			"description": "Call this ONLY if you need a real tool to answer — " +
				"e.g. search the web, check weather, read a file, run a skill. " +
				"Do NOT call it for conversational replies: greetings, acknowledgements ('ok', 'thanks', 'got it', 'sounds good'), " +
				"casual chat, opinions, explanations, or anything you can answer from your own knowledge. " +
				"After calling it you will receive the relevant tools and should proceed immediately.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
	}
}

// LoopConfig carries per-run configuration.
type LoopConfig struct {
	Provider      ProviderConfig
	MaxIterations int
	SupportDir    string
	ConvID        string
	// DryRun enables dry-run mode: non-read skill actions are simulated without
	// applying side effects. The AI receives a structured result describing what
	// would have happened. Read-class actions execute normally.
	DryRun bool
	// Tools overrides the full tool set for this run. When non-nil, these
	// definitions are sent to the model instead of calling ToolDefinitions().
	// Used by EnableSmartToolSelection to inject only the relevant capability
	// groups for the current user message.
	Tools []map[string]any
	// UserMessage is the original user text. Used by lazy tool selection:
	// when the model calls request_tools, SelectiveToolDefs runs against this
	// message to pick the right capability groups for the upgrade.
	UserMessage string
}

// Loop is the multi-turn agent loop.
type Loop struct {
	Skills *skills.Registry
	BC     Emitter
	DB     *storage.DB
}

// deferralState captures the full messages array + assistant tool_calls
// for storage in normalized_input_json.
type deferralState struct {
	Messages  []OAIMessage  `json:"messages"`
	ToolCalls []OAIToolCall `json:"tool_calls"`
	ConvID    string        `json:"conv_id"`
}

// openAIToolLimit is the maximum number of tools OpenAI (and OpenAI-compatible)
// providers accept in a single request. Requests with more tools are rejected
// with HTTP 400 "array too long". Anthropic has no documented hard limit.
const openAIToolLimit = 128

// capToolsForProvider trims the tool list to the provider's maximum when
// necessary. Trimming is priority-ordered so the most critical tools survive:
//
//  1. forge.* — always kept (the user may be in a skill-building flow)
//  2. atlas.*, info.* — core self-awareness tools
//  3. vault.*, gremlin.*, diary.*, image.* — management tools
//  4. Everything else in registration order
//
// A warning is logged whenever the list is actually truncated.
func capToolsForProvider(tools []map[string]any, providerType ProviderType) []map[string]any {
	limit := 0
	switch providerType {
	case ProviderOpenAI, ProviderLMStudio, ProviderAtlasEngine:
		limit = openAIToolLimit
	}
	if limit == 0 || len(tools) <= limit {
		return tools
	}

	// Bucket tools by priority group.
	var p1, p2, p3, p4 []map[string]any
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		name, _ := fn["name"].(string)
		switch {
		case strings.HasPrefix(name, "forge__"):
			p1 = append(p1, t)
		case strings.HasPrefix(name, "atlas__"), strings.HasPrefix(name, "info__"):
			p2 = append(p2, t)
		case strings.HasPrefix(name, "vault__"), strings.HasPrefix(name, "gremlin__"),
			strings.HasPrefix(name, "diary__"), strings.HasPrefix(name, "image__"):
			p3 = append(p3, t)
		default:
			p4 = append(p4, t)
		}
	}

	ordered := make([]map[string]any, 0, len(tools))
	ordered = append(ordered, p1...)
	ordered = append(ordered, p2...)
	ordered = append(ordered, p3...)
	ordered = append(ordered, p4...)

	trimmed := ordered[:limit]
	logstore.Write("warn",
		fmt.Sprintf("Agent: tool list capped at %d (was %d) for %s provider — lowest-priority tools dropped",
			limit, len(tools), providerType),
		nil)
	return trimmed
}

// Run executes the multi-turn agent loop for one user request.
// Each iteration makes a single streaming API call that handles both text
// and tool-call turns — no separate probe call is needed.
// messages should include system + history + user message(s).
func (l *Loop) Run(ctx context.Context, cfg LoopConfig, messages []OAIMessage, convID string) RunResult {
	tools := cfg.Tools
	if tools == nil {
		tools = l.Skills.ToolDefinitions()
	}
	tools = capToolsForProvider(tools, cfg.Provider.Type)

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}

	// Inject dry-run into context so skills and the registry can inspect it.
	if cfg.DryRun {
		ctx = skills.WithDryRun(ctx)
	}

	// Shorten conv ID for log metadata.
	shortConv := convID
	if len(shortConv) > 8 {
		shortConv = shortConv[:8]
	}

	var (
		totalUsage         TokenUsage
		allToolSummaries   []string
		allResultSummaries []string
		toolsUpgraded      bool // lazy mode: upgrade happens at most once per turn
		compacted          bool // overflow recovery: compact at most once per turn
	)

	for i := 0; i < maxIter; i++ {
		// Single streaming call — detects tool calls and emits text in one pass.
		sr, err := streamWithToolDetection(ctx, cfg.Provider, messages, tools, convID, l.BC)
		if err != nil {
			// If the model's context window was exceeded and we haven't yet
			// compacted this turn, trim old messages and retry immediately.
			if !compacted && isContextOverflow(err) {
				before := len(messages)
				messages = compactMessages(messages)
				compacted = true
				logstore.Write("warn",
					fmt.Sprintf("Agent: context overflow — compacted %d→%d messages, retrying (conv %s)",
						before, len(messages), shortConv),
					map[string]string{"conv": shortConv})
				i-- // don't count compaction as an iteration
				continue
			}
			logstore.Write("error", "Agent error: "+err.Error(), map[string]string{"conv": shortConv})
			return RunResult{Status: "error", Error: err, TotalUsage: totalUsage}
		}
		totalUsage.InputTokens += sr.Usage.InputTokens
		totalUsage.OutputTokens += sr.Usage.OutputTokens
		AddSessionTokens(sr.Usage.InputTokens, sr.Usage.OutputTokens)

		// Text response — streaming already delivered the tokens.
		if len(sr.ToolCalls) == 0 {
			return RunResult{
				Status:              "complete",
				FinalText:           sr.FinalText,
				TotalUsage:          totalUsage,
				ToolCallSummaries:   allToolSummaries,
				ToolResultSummaries: allResultSummaries,
			}
		}

		// ── Lazy tool upgrade ────────────────────────────────────────────────
		// In "lazy" mode the model is given only the request_tools meta-tool.
		// When it calls it, we upgrade to the full heuristic tool set and
		// continue the loop — completely transparent to the user.
		if !toolsUpgraded {
			for _, tc := range sr.ToolCalls {
				if tc.Function.Name != requestToolsName {
					continue
				}
				// Upgrade: heuristic selection based on the original user message.
				// Sends only the relevant capability groups — keeps token usage low
				// while ensuring the model gets the tools it actually needs.
				// The 128-cap safety net handles provider limits.
				upgraded := l.Skills.SelectiveToolDefs(cfg.UserMessage)
				upgraded = capToolsForProvider(upgraded, cfg.Provider.Type)
				tools = upgraded
				toolsUpgraded = true

				logstore.Write("info",
					fmt.Sprintf("Lazy tool upgrade: %d tools selected (conv %s, msg: %.60q)",
						len(tools), shortConv, cfg.UserMessage),
					map[string]string{"conv": shortConv, "mode": "lazy→heuristic", "tools": fmt.Sprintf("%d", len(tools))})

				// Protocol: the assistant message must contain the tool_call,
				// and we must send a tool result before the next model turn.
				messages = append(messages, OAIMessage{
					Role:      "assistant",
					Content:   sr.FinalText, // preserve any text streamed before the call
					ToolCalls: sr.ToolCalls,
				})
				messages = append(messages, OAIMessage{
					Role:       "tool",
					Content:    "Tool capabilities are now available. Proceed to answer the user's request using the appropriate tools.",
					ToolCallID: tc.ID,
					Name:       requestToolsName,
				})
				break
			}
			if toolsUpgraded {
				i--      // lazy upgrade doesn't count as an iteration
				continue // re-enter loop with real tools
			}
		}

		// Tool calls.
		assistantMsg := OAIMessage{
			Role:      "assistant",
			Content:   sr.FinalText, // non-empty when model narrates before tool use
			ToolCalls: sr.ToolCalls,
		}

		var needApproval []OAIToolCall
		var canRun []OAIToolCall
		for _, tc := range sr.ToolCalls {
			if l.Skills.NeedsApproval(tc.Function.Name) {
				needApproval = append(needApproval, tc)
			} else {
				canRun = append(canRun, tc)
			}
		}

		if len(needApproval) > 0 {
			messages = append(messages, assistantMsg)
			pendingApprovals, deferErr := l.deferToolCalls(ctx, needApproval, messages, convID, cfg.SupportDir)
			if deferErr != nil {
				return RunResult{Status: "error", Error: deferErr, TotalUsage: totalUsage}
			}
			for _, pa := range pendingApprovals {
				logstore.Write("info", "Approval required: "+pa.ActionID, map[string]string{
					"conv":  shortConv,
					"class": pa.ActionClass,
				})
				l.BC.Emit(convID, EmitEvent{
					Type:       "approval_required",
					ConvID:     convID,
					ApprovalID: pa.ApprovalID,
					ToolCallID: pa.ToolCallID,
					ToolName:   pa.ActionID,
					Arguments:  pa.Arguments,
				})
			}
			return RunResult{Status: "pendingApproval", PendingApprovals: pendingApprovals, TotalUsage: totalUsage}
		}

		// All tool calls can run without approval.
		messages = append(messages, assistantMsg)

		// Execute tool calls with parallelism for stateless tools.
		// Stateful tools (browser.*) run serially to protect shared Chrome state.
		// Results are collected into an index-preserving slice so the final
		// message assembly loop always appends in the original call order —
		// a requirement of the OpenAI tool-result protocol.
		results := make([]*toolExecResult, len(canRun))

		// Pass 1 — run all stateless tools concurrently.
		var wg sync.WaitGroup
		for i, tc := range canRun {
			if l.Skills.IsStateful(tc.Function.Name) {
				continue
			}
			wg.Add(1)
			go func(idx int, tc OAIToolCall) {
				defer wg.Done()
				results[idx] = l.execTool(ctx, tc)
			}(i, tc)
		}
		wg.Wait()

		// Pass 2 — run stateful tools serially, in original order.
		for i, tc := range canRun {
			if !l.Skills.IsStateful(tc.Function.Name) {
				continue
			}
			results[i] = l.execTool(ctx, tc)
		}

		// Pass 3 — emit events and append tool result messages in original order.
		for i, tc := range canRun {
			r := results[i]
			actionClass := string(l.Skills.GetActionClass(tc.Function.Name))
			redactedArgs := skills.RedactArgs(json.RawMessage(tc.Function.Arguments))

			logstore.Write("info", "Tool call: "+tc.Function.Name, map[string]string{
				"conv":    shortConv,
				"class":   actionClass,
				"args":    redactedArgs,
				"elapsed": fmt.Sprintf("%dms", r.elapsedMs),
			})

			// Accumulate tool summaries for post-turn reflection.
			allToolSummaries = append(allToolSummaries, tc.Function.Name)
			if r.execErr != nil {
				allResultSummaries = append(allResultSummaries, "error: "+r.execErr.Error())
			} else {
				allResultSummaries = append(allResultSummaries, sanitizeLogOutcome(r.result))
			}

			// Structured action log entry.
			entry := logstore.ActionLogEntry{
				ToolName:     tc.Function.Name,
				ActionClass:  actionClass,
				ConvID:       shortConv,
				InputSummary: redactedArgs,
				Success:      r.result.Success,
				ElapsedMs:    r.elapsedMs,
				DryRun:       r.result.DryRun,
				Outcome:      sanitizeLogOutcome(r.result),
				Warnings:     r.result.Warnings,
			}
			for k, v := range r.result.Artifacts {
				entry.Artifacts = append(entry.Artifacts, fmt.Sprintf("%s=%v", k, v))
			}

			if r.execErr != nil {
				entry.Success = false
				entry.Errors = []string{r.execErr.Error()}
				logstore.WriteAction(entry)

				l.BC.Emit(convID, EmitEvent{
					Type:       "tool_failed",
					ToolName:   tc.Function.Name,
					ToolCallID: tc.ID,
					ConvID:     convID,
					Error:      r.execErr.Error(),
				})
				messages = append(messages, OAIMessage{
					Role:       "tool",
					Content:    buildErrorContent(tc.Function.Name, r.execErr, r.result),
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				})
			} else {
				logstore.WriteAction(entry)
				l.BC.Emit(convID, EmitEvent{
					Type:       "tool_finished",
					ToolName:   tc.Function.Name,
					ToolCallID: tc.ID,
					ConvID:     convID,
				})
				messages = append(messages, OAIMessage{
					Role:       "tool",
					Content:    buildToolContent(cfg.Provider, r.result.FormatForModel()),
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				})
			}
		}
		// Continue to next iteration for the model to process tool results.
	}

	return RunResult{
		Status:              "complete",
		FinalText:           "Maximum agent iterations reached.",
		TotalUsage:          totalUsage,
		ToolCallSummaries:   allToolSummaries,
		ToolResultSummaries: allResultSummaries,
	}
}

// toolResultMaxChars is the maximum character length for a single tool result
// before head+tail truncation is applied. Keeps individual results from
// exhausting the context window on large page reads or API dumps.
const toolResultMaxChars = 40_000

// capContent truncates a tool result string that exceeds toolResultMaxChars.
// Head + tail strategy preserves the opening structure and the trailing
// summary/conclusion, which are typically the most semantically dense parts.
func capContent(s string) string {
	if len(s) <= toolResultMaxChars {
		return s
	}
	half := toolResultMaxChars / 2
	head := s[:half]
	tail := s[len(s)-half:]
	dropped := len(s) - toolResultMaxChars
	return head +
		fmt.Sprintf("\n\n[...%d characters omitted — request a smaller chunk or use pagination if you need more...]\n\n", dropped) +
		tail
}

// buildToolContent returns the appropriate content value for a tool result message.
// When content is a screenshot (prefixed with __ATLAS_IMAGE__:), it builds a
// vision content block array that OpenAI-compatible and Anthropic models understand.
// For all other content, the string is size-capped and returned as-is.
func buildToolContent(provider ProviderConfig, content string) any {
	const imagePrefix = "__ATLAS_IMAGE__:"
	if !strings.HasPrefix(content, imagePrefix) {
		return capContent(content)
	}

	dataURI := content[len(imagePrefix):]

	switch provider.Type {
	case ProviderAnthropic:
		// Anthropic expects base64 image source, not a data URI.
		// Strip the data:image/png;base64, prefix to get raw base64.
		rawB64 := dataURI
		if i := strings.Index(rawB64, ","); i >= 0 {
			rawB64 = rawB64[i+1:]
		}
		return []any{
			map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/png",
					"data":       rawB64,
				},
			},
		}
	default:
		// OpenAI, Gemini, LM Studio — use image_url content block.
		return []any{
			map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": dataURI,
				},
			},
		}
	}
}

// buildErrorContent creates an actionable error response to return to the model.
// If ToolResult already has structured failure data, that is used directly.
// Otherwise a minimal JSON envelope is synthesised from the raw error.
func buildErrorContent(actionID string, execErr error, result skills.ToolResult) string {
	if !result.Success && len(result.Artifacts) > 0 {
		return result.FormatForModel()
	}
	return fmt.Sprintf(
		`{"success":false,"summary":"Tool execution error in %s: %s","artifacts":{"action":"%s","error_detail":"%s"}}`,
		actionID, execErr.Error(), actionID, execErr.Error(),
	)
}

// deferToolCalls saves tool calls as deferred_executions in the DB.
func (l *Loop) deferToolCalls(
	ctx context.Context,
	toolCalls []OAIToolCall,
	messages []OAIMessage,
	convID string,
	supportDir string,
) ([]PendingApproval, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var approvals []PendingApproval

	for _, tc := range toolCalls {
		deferredID := newUUID()
		approvalID := newUUID()

		state := deferralState{
			Messages:  messages,
			ToolCalls: toolCalls,
			ConvID:    convID,
		}
		stateJSON, err := json.Marshal(state)
		if err != nil {
			return nil, fmt.Errorf("agent: marshal deferral state: %w", err)
		}

		permLevel := l.Skills.PermissionLevel(tc.Function.Name)
		actionClass := string(l.Skills.GetActionClass(tc.Function.Name))
		convIDPtr := &convID
		actionID := tc.Function.Name

		// Redact args in the human-readable summary so secrets are not stored in the DB.
		redacted := skills.RedactArgs(json.RawMessage(tc.Function.Arguments))
		summary := fmt.Sprintf("Run %s with %s", tc.Function.Name, redacted)

		row := storage.DeferredExecRow{
			DeferredID:          deferredID,
			SourceType:          "agent_loop",
			ActionID:            &actionID,
			ToolCallID:          tc.ID,
			NormalizedInputJSON: string(stateJSON),
			ConversationID:      convIDPtr,
			ApprovalID:          approvalID,
			Summary:             summary,
			PermissionLevel:     permLevel,
			RiskLevel:           actionClass,
			Status:              "pending_approval",
			CreatedAt:           now,
			UpdatedAt:           now,
		}

		if err := l.DB.SaveDeferredExecution(row); err != nil {
			return nil, fmt.Errorf("agent: save deferred execution: %w", err)
		}

		// For file write/patch actions, compute and store a diff preview so the
		// Approvals UI can render it as a colored diff instead of raw JSON.
		canonical := l.Skills.Canonicalize(tc.Function.Name)
		if canonical == "fs.write_file" || canonical == "fs.patch_file" {
			if diff := computeWriteDiffPreview(canonical, tc.Function.Arguments); diff != "" {
				_ = l.DB.SetPreviewDiff(tc.ID, diff)
			}
		}

		approvals = append(approvals, PendingApproval{
			DeferredID:  deferredID,
			ApprovalID:  approvalID,
			ToolCallID:  tc.ID,
			ActionID:    tc.Function.Name,
			Arguments:   tc.Function.Arguments,
			PermLevel:   permLevel,
			ActionClass: actionClass,
		})
	}
	return approvals, nil
}

// ── Diff preview ──────────────────────────────────────────────────────────────

// computeWriteDiffPreview returns a unified diff preview for fs.write_file or
// fs.patch_file before the tool executes, so the Approvals UI can display it.
func computeWriteDiffPreview(actionID, argsJSON string) string {
	switch actionID {
	case "fs.write_file":
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &p); err != nil || p.Path == "" {
			return ""
		}
		data, err := os.ReadFile(p.Path)
		if err != nil {
			// New file — show full content as additions.
			return skills.UnifiedDiff("/dev/null", p.Path, "", p.Content)
		}
		return skills.UnifiedDiff(p.Path, p.Path, string(data), p.Content)
	case "fs.patch_file":
		var p struct {
			Patch string `json:"patch"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &p); err != nil {
			return ""
		}
		return p.Patch // the patch IS the diff
	}
	return ""
}

// ── Log outcome sanitisation ──────────────────────────────────────────────────

// sanitizeLogOutcome returns a clean one-liner for the action log and post-turn
// MIND reflection. It strips binary payloads (screenshots) and truncates long
// text (page reads) that would bloat the log or MIND context.
func sanitizeLogOutcome(r skills.ToolResult) string {
	const imgPrefix = "__ATLAS_IMAGE__:"
	const maxRunes = 300

	s := r.Summary
	if strings.HasPrefix(s, imgPrefix) {
		return "Screenshot captured"
	}
	if s == "" {
		s = r.FormatForModel()
	}
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return s
}

// ── Tool execution helper ─────────────────────────────────────────────────────

// toolExecResult holds the outcome of a single tool call.
// Safe to write from a goroutine and read after WaitGroup.Wait().
type toolExecResult struct {
	result    skills.ToolResult
	execErr   error
	elapsedMs int64
}

// execTool runs one tool call and returns a toolExecResult.
// It applies the correct timeout (90s for browser, 30s for everything else)
// and is safe to call from multiple goroutines simultaneously for stateless tools.
func (l *Loop) execTool(ctx context.Context, tc OAIToolCall) *toolExecResult {
	toolTimeout := 30 * time.Second
	if strings.HasPrefix(tc.Function.Name, "browser.") || strings.HasPrefix(tc.Function.Name, "browser__") {
		toolTimeout = 90 * time.Second
	}
	toolCtx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	start := time.Now()
	result, execErr := l.Skills.Execute(toolCtx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
	return &toolExecResult{
		result:    result,
		execErr:   execErr,
		elapsedMs: time.Since(start).Milliseconds(),
	}
}

// ── Context overflow handling ─────────────────────────────────────────────────

// keepRecentMessages is the number of most-recent messages retained after
// compaction. The system message is always kept regardless of this limit.
const keepRecentMessages = 20

// isContextOverflow reports whether an API error indicates the request exceeded
// the model's context window. Matches error strings from OpenAI, Anthropic,
// and Gemini providers.
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "context_length_exceeded") ||
		strings.Contains(s, "context length") ||
		strings.Contains(s, "maximum context") ||
		strings.Contains(s, "prompt is too long") ||
		strings.Contains(s, "reduce the length") ||
		strings.Contains(s, "tokens exceed") ||
		strings.Contains(s, "token limit") ||
		strings.Contains(s, "too many tokens")
}

// compactMessages trims a message slice to fit within context limits.
// Strategy: keep the system message (always first) + the keepRecentMessages
// most-recent messages, and insert a truncation notice at the join point so
// the model knows history was omitted.
func compactMessages(messages []OAIMessage) []OAIMessage {
	// Locate system message (always index 0 when present).
	systemEnd := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		systemEnd = 1
	}
	remaining := messages[systemEnd:]
	if len(remaining) <= keepRecentMessages {
		return messages // nothing to trim
	}

	kept := remaining[len(remaining)-keepRecentMessages:]
	notice := OAIMessage{
		Role:    "user",
		Content: "[Note: Earlier conversation history was omitted to stay within the model's context limit.]",
	}

	result := make([]OAIMessage, 0, systemEnd+1+len(kept))
	result = append(result, messages[:systemEnd]...)
	result = append(result, notice)
	result = append(result, kept...)
	return result
}

// ── UUID generation ───────────────────────────────────────────────────────────

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
