// Package agent implements the multi-turn agent loop with tool execution,
// approval deferral, and conversation resumption.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	// file_generated fields — set when a tool produces a local file artifact.
	Filename  string
	MimeType  string
	FileSize  int64
	FileToken string
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
	FirstTokenAt        time.Duration
	StreamChunkCount    int
	StreamChars         int
	ToolCallSummaries   []string // tool names called during this turn (all iterations)
	ToolResultSummaries []string // short result summaries, one per tool call
	GeneratedFiles      []string // absolute local file paths emitted as file_generated events
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
				"e.g. search the web, check weather, read a file, run a skill, create a dashboard. " +
				"Do NOT call it for conversational replies: greetings, acknowledgements ('ok', 'thanks', 'got it', 'sounds good'), " +
				"casual chat, opinions, explanations, or anything you can answer from your own knowledge. " +
				"IMPORTANT: if the user asks to create, build, generate, or make a dashboard or data page, " +
				"use categories=[\"dashboards\"] — never write an HTML file to disk as a substitute. " +
				"After calling it you will receive the relevant tools and should proceed immediately. " +
				"If the provided short list is not enough, call request_tools again with broad=true or with categories.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"broad": map[string]any{
						"type":        "boolean",
						"description": "Set true if the previously provided short list is not enough and you need the broad/full tool surface.",
					},
					"categories": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
							"enum": []string{
								"weather", "web", "finance", "office", "media", "mac", "shell",
								"files", "vault", "browser", "voice", "communication", "creative",
								"workflow", "automation", "forge", "dashboards", "meta",
							},
						},
						"description": "Optional categories to request instead of the full broad list.",
					},
				},
				"required": []string{},
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
	// ToolPolicy optionally constrains tool calls for workflow/trust-bound runs.
	ToolPolicy *ToolPolicy
}

// ToolPolicy constrains tools available to a trust-bounded agent run.
type ToolPolicy struct {
	ApprovedRootPaths   []string
	AllowedToolPrefixes []string
	AllowsSensitiveRead bool
	AllowsLiveWrite     bool
	Enabled             bool
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
		case name == requestToolsName,
			strings.HasPrefix(name, "atlas__"), strings.HasPrefix(name, "info__"):
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
		firstTokenAt       time.Duration
		streamChunkCount   int
		streamChars        int
		allToolSummaries   []string
		allResultSummaries []string
		allGeneratedFiles  []string
		toolUpgradeStage   int  // lazy mode: 0 meta only, 1 short list, 2 broad/category list
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
		if firstTokenAt <= 0 && sr.FirstTokenAt > 0 {
			firstTokenAt = sr.FirstTokenAt
		}
		streamChunkCount += sr.ChunkCount
		streamChars += sr.StreamChars

		// Mark the end of this assistant model turn before any tool execution starts.
		l.BC.Emit(convID, EmitEvent{
			Type:   "assistant_done",
			Role:   "assistant",
			ConvID: convID,
		})

		// Text response — streaming already delivered the tokens.
		if len(sr.ToolCalls) == 0 {
			return RunResult{
				Status:              "complete",
				FinalText:           sr.FinalText,
				TotalUsage:          totalUsage,
				FirstTokenAt:        firstTokenAt,
				StreamChunkCount:    streamChunkCount,
				StreamChars:         streamChars,
				ToolCallSummaries:   allToolSummaries,
				ToolResultSummaries: allResultSummaries,
				GeneratedFiles:      allGeneratedFiles,
			}
		}

		// ── Lazy tool upgrade ────────────────────────────────────────────────
		// In Smart/lazy mode the model starts with request_tools. The first
		// request returns a short local tool list; a later request can expand to
		// requested categories or the broad/full tool surface.
		if tc, ok := firstRequestToolsCall(sr.ToolCalls); ok {
			upgraded, stage, summary := l.resolveToolUpgrade(cfg, tc, toolUpgradeStage)
			upgraded = appendRequestToolsDef(upgraded)
			upgraded = capToolsForProvider(upgraded, cfg.Provider.Type)
			tools = upgraded
			toolUpgradeStage = stage

			logstore.Write("info",
				fmt.Sprintf("Smart tool upgrade: %d tools selected (stage=%d, conv %s, msg: %.60q)",
					len(tools), toolUpgradeStage, shortConv, cfg.UserMessage),
				map[string]string{"conv": shortConv, "mode": "smart", "stage": fmt.Sprintf("%d", toolUpgradeStage), "tools": fmt.Sprintf("%d", len(tools))})

			// Protocol: the assistant message must contain the tool_call,
			// and we must send a tool result before the next model turn.
			messages = append(messages, OAIMessage{
				Role:      "assistant",
				Content:   sr.FinalText, // preserve any text streamed before the call
				ToolCalls: sr.ToolCalls,
			})
			messages = append(messages, OAIMessage{
				Role:       "tool",
				Content:    summary,
				ToolCallID: tc.ID,
				Name:       requestToolsName,
			})
			i--      // tool upgrade doesn't count as an iteration
			continue // re-enter loop with updated tools
		}

		// Tool calls.
		assistantMsg := OAIMessage{
			Role:      "assistant",
			Content:   sr.FinalText, // non-empty when model narrates before tool use
			ToolCalls: sr.ToolCalls,
		}

		var needApproval []OAIToolCall
		var canRun []OAIToolCall
		var blocked []toolPolicyBlock
		for _, tc := range sr.ToolCalls {
			if block, ok := l.blockedByToolPolicy(cfg.ToolPolicy, tc); ok {
				blocked = append(blocked, block)
				continue
			}
			if l.Skills.NeedsApproval(tc.Function.Name) {
				needApproval = append(needApproval, tc)
			} else {
				canRun = append(canRun, tc)
			}
		}

		if len(needApproval) > 0 {
			messages = append(messages, assistantMsg)
			for _, block := range blocked {
				messages = append(messages, block.message())
				l.BC.Emit(convID, EmitEvent{
					Type:       "tool_failed",
					ToolName:   block.ActionID,
					ToolCallID: block.ToolCallID,
					ConvID:     convID,
					Error:      block.Reason,
				})
			}
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
		for _, block := range blocked {
			logstore.Write("warn", "Workflow tool policy blocked: "+block.ActionID, map[string]string{
				"conv":   shortConv,
				"reason": block.Reason,
			})
			messages = append(messages, block.message())
			l.BC.Emit(convID, EmitEvent{
				Type:       "tool_failed",
				ToolName:   block.ActionID,
				ToolCallID: block.ToolCallID,
				ConvID:     convID,
				Error:      block.Reason,
			})
		}

		// Execute tool calls with parallelism for stateless tools.
		// Stateful tools (browser.*) run serially to protect shared Chrome state.
		// Results are collected into an index-preserving slice so the final
		// message assembly loop always appends in the original call order —
		// a requirement of the OpenAI tool-result protocol.
		results := make([]*toolExecResult, len(canRun))

		for _, tc := range canRun {
			l.BC.Emit(convID, EmitEvent{
				Type:       "tool_started",
				ToolName:   tc.Function.Name,
				ToolCallID: tc.ID,
				ConvID:     convID,
			})
		}

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
				// Emit file_generated for each local file artifact produced by the tool.
				// Scan both structured Artifacts map and the free-text Summary so that
				// skills which only describe the output path in their summary are covered.
				emittedPaths := map[string]bool{}
				allToolPaths := append(
					ExtractArtifactPaths(r.result.Artifacts),
					ExtractPathsFromText(r.result.Summary)...,
				)
				for _, filePath := range allToolPaths {
					if emittedPaths[filePath] {
						continue
					}
					emittedPaths[filePath] = true
					token := RegisterArtifact(filePath)
					if token == "" {
						continue
					}
					info, err := os.Stat(filePath)
					var size int64
					if err == nil {
						size = info.Size()
					}
					l.BC.Emit(convID, EmitEvent{
						Type:      "file_generated",
						ConvID:    convID,
						ToolName:  tc.Function.Name,
						Filename:  filepath.Base(filePath),
						MimeType:  MimeTypeForPath(filePath),
						FileSize:  size,
						FileToken: token,
					})
					allGeneratedFiles = append(allGeneratedFiles, filePath)
				}
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
		FirstTokenAt:        firstTokenAt,
		StreamChunkCount:    streamChunkCount,
		StreamChars:         streamChars,
		ToolCallSummaries:   allToolSummaries,
		ToolResultSummaries: allResultSummaries,
		GeneratedFiles:      allGeneratedFiles,
	}
}

// toolResultMaxChars is the maximum character length for a single tool result
// before head+tail truncation is applied. Keeps individual results from
// exhausting the context window on large page reads or API dumps.
const toolResultMaxChars = 12_000

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

type toolPolicyBlock struct {
	ToolCallID string
	ActionID   string
	Reason     string
}

type requestToolsArgs struct {
	Broad      bool     `json:"broad"`
	Categories []string `json:"categories"`
}

func (b toolPolicyBlock) message() OAIMessage {
	result := skills.ErrResult("run "+b.ActionID, "workflow trust scope", false, errors.New(b.Reason))
	return OAIMessage{
		Role:       "tool",
		Content:    result.FormatForModel(),
		ToolCallID: b.ToolCallID,
		Name:       b.ActionID,
	}
}

func (l *Loop) blockedByToolPolicy(policy *ToolPolicy, tc OAIToolCall) (toolPolicyBlock, bool) {
	if policy == nil || !policy.Enabled {
		return toolPolicyBlock{}, false
	}
	actionID := l.Skills.Canonicalize(tc.Function.Name)
	block := toolPolicyBlock{ToolCallID: tc.ID, ActionID: tc.Function.Name}
	if len(policy.AllowedToolPrefixes) > 0 && !hasAllowedToolPrefix(actionID, policy.AllowedToolPrefixes) && !isCoreTool(actionID) {
		block.Reason = fmt.Sprintf("workflow trust scope does not allow tool %s", actionID)
		return block, true
	}
	if !policy.AllowsLiveWrite {
		switch l.Skills.GetActionClass(actionID) {
		case skills.ActionClassLocalWrite, skills.ActionClassDestructiveLocal, skills.ActionClassExternalSideEffect, skills.ActionClassSendPublishDelete:
			block.Reason = fmt.Sprintf("workflow trust scope blocks live-write or side-effect tool %s", actionID)
			return block, true
		}
	}
	if !policy.AllowsSensitiveRead && isSensitiveReadTool(actionID) {
		block.Reason = fmt.Sprintf("workflow trust scope blocks sensitive-read tool %s", actionID)
		return block, true
	}
	if strings.HasPrefix(actionID, "fs.") {
		if path, ok := toolPathArgument(tc.Function.Arguments); ok {
			if len(policy.ApprovedRootPaths) == 0 {
				block.Reason = fmt.Sprintf("workflow trust scope blocks filesystem tool %s because no approved workflow roots are configured", actionID)
				return block, true
			}
			if !pathWithinRoots(path, policy.ApprovedRootPaths) {
				block.Reason = fmt.Sprintf("workflow trust scope blocks path %q outside approved workflow roots", path)
				return block, true
			}
		}
	}
	return toolPolicyBlock{}, false
}

func firstRequestToolsCall(calls []OAIToolCall) (OAIToolCall, bool) {
	for _, tc := range calls {
		if tc.Function.Name == requestToolsName {
			return tc, true
		}
	}
	return OAIToolCall{}, false
}

func (l *Loop) resolveToolUpgrade(cfg LoopConfig, tc OAIToolCall, currentStage int) ([]map[string]any, int, string) {
	var args requestToolsArgs
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
	if len(args.Categories) > 0 {
		tools := l.Skills.ToolDefsForGroupsForMessage(args.Categories, cfg.UserMessage)
		return tools, 2, fmt.Sprintf("Tool capabilities are now expanded for categories: %s. Proceed using the appropriate tools. If these are still insufficient, call request_tools again with broad=true.", strings.Join(args.Categories, ", "))
	}
	if args.Broad || currentStage >= 1 {
		tools := l.Skills.ToolDefinitions()
		return tools, 2, "The broad tool surface is now available. Proceed using the appropriate tools; do not ask the user to paste a spec if a tool can perform the action."
	}
	tools := l.Skills.SelectiveToolDefs(cfg.UserMessage)
	return tools, 1, "A short relevant tool list is now available. Proceed using those tools. If the short list is not enough, call request_tools again with broad=true or with categories."
}

func appendRequestToolsDef(tools []map[string]any) []map[string]any {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == requestToolsName {
			return tools
		}
	}
	out := make([]map[string]any, 0, len(tools)+1)
	out = append(out, tools...)
	out = append(out, RequestToolsDef())
	return out
}

func hasAllowedToolPrefix(actionID string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && strings.HasPrefix(actionID, prefix) {
			return true
		}
	}
	return false
}

func isCoreTool(actionID string) bool {
	return strings.HasPrefix(actionID, "info.") || actionID == requestToolsName
}

func isSensitiveReadTool(actionID string) bool {
	switch {
	case strings.HasPrefix(actionID, "vault."),
		strings.HasPrefix(actionID, "memory."),
		strings.HasPrefix(actionID, "browser."),
		strings.HasPrefix(actionID, "applescript.mail_"),
		strings.HasPrefix(actionID, "applescript.contacts_"):
		return true
	}
	return false
}

func toolPathArgument(args string) (string, bool) {
	var p struct {
		Path       string `json:"path"`
		TargetPath string `json:"targetPath"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", false
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		path = strings.TrimSpace(p.TargetPath)
	}
	if path == "" {
		return "", false
	}
	return path, true
}

func pathWithinRoots(path string, roots []string) bool {
	clean := filepath.Clean(path)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if clean == cleanRoot || strings.HasPrefix(clean, cleanRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
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
