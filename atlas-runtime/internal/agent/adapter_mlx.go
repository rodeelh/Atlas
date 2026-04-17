package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"atlas-runtime-go/internal/agent/stream"
)

// mlxAdapter handles the Atlas MLX provider with a three-level fallback:
//  1. Streaming via stream.ParseOAICompatStream
//  2. Non-streaming with tools (if stream yields nothing)
//  3. Non-streaming without tools (if level 2 still yields nothing and tools were provided)
type mlxAdapter struct{ p ProviderConfig }

func (a *mlxAdapter) Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	ch := make(chan TurnEvent, 64)
	go func() {
		defer close(ch)
		send := func(ev TurnEvent) bool {
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// ── Level 1: streaming attempt ──────────────────────────────────────
		text, toolCalls, usage, err := a.tryStream(ctx, req.Messages, req.Tools)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}
		log.Printf("mlx stream result: text_len=%d tool_calls=%d tools_in=%d", len(text), len(toolCalls), len(req.Tools))

		// ── Level 2: non-streaming with tools ───────────────────────────────
		if text == "" && len(toolCalls) == 0 {
			start := time.Now()
			msg, _, u, err2 := callOpenAICompatNonStreaming(ctx, a.p, req.Messages, req.Tools)
			if err2 == nil {
				t, _ := msg.Content.(string)
				log.Printf("mlx retry with tools: text_len=%d tool_calls=%d", len(t), len(msg.ToolCalls))
				text = t
				if len(msg.ToolCalls) > 0 {
					toolCalls = msg.ToolCalls
				}
				usage = u
				_ = start
			} else {
				log.Printf("mlx retry with tools failed: %v", err2)
			}

			// ── Level 3: non-streaming without tools ─────────────────────────
			if text == "" && len(toolCalls) == 0 && len(req.Tools) > 0 {
				msg2, _, u2, err3 := callOpenAICompatNonStreaming(ctx, a.p, req.Messages, nil)
				if err3 == nil {
					t2, _ := msg2.Content.(string)
					log.Printf("mlx retry without tools: text_len=%d tool_calls=%d", len(t2), len(msg2.ToolCalls))
					text = t2
					if len(msg2.ToolCalls) > 0 {
						toolCalls = msg2.ToolCalls
					}
					usage = u2
				} else {
					log.Printf("mlx retry without tools failed: %v", err3)
				}
			}
		}

		if text != "" {
			if !send(TurnEvent{Type: EventTextDelta, Text: text}) {
				return
			}
		}
		for i := range toolCalls {
			tc := toolCalls[i]
			if !send(TurnEvent{Type: EventToolCall, ToolCall: &tc}) {
				return
			}
		}
		send(TurnEvent{Type: EventDone, Usage: &usage})
	}()
	return ch, nil
}

// tryStream makes one streaming HTTP call to the MLX endpoint and drains the
// response into plain text + assembled tool calls. The channel-based emitter
// is bypassed — events are accumulated here because MLX needs to inspect the
// full result before deciding whether to fall back.
func (a *mlxAdapter) tryStream(ctx context.Context, messages []OAIMessage, tools []map[string]any) (string, []OAIToolCall, TokenUsage, error) {
	reqBody := map[string]any{
		"model":    a.p.Model,
		"messages": messages,
		"stream":   true,
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	applyMLXRequestOptions(reqBody, a.p, tools)

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, TokenUsage{}, err
	}

	url := oaiCompatBaseURL(a.p) + "/chat/completions"

	releaseGate, _, _, err := acquireMLXRequestGate(ctx, a.p)
	if err != nil {
		return "", nil, TokenUsage{}, err
	}
	defer releaseGate()

	resp, err := doOpenAICompatRequest(ctx, a.p, url, body)
	if err != nil {
		return "", nil, TokenUsage{}, fmt.Errorf("MLX streaming request failed: %w", err)
	}

	// Retry on 503 (model loading) — up to 30s with 2s backoff.
	if resp.StatusCode == http.StatusServiceUnavailable {
		resp.Body.Close()
		for attempt := 0; attempt < 15; attempt++ {
			select {
			case <-ctx.Done():
				return "", nil, TokenUsage{}, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			resp, err = doOpenAICompatRequest(ctx, a.p, url, body)
			if err != nil {
				return "", nil, TokenUsage{}, fmt.Errorf("MLX streaming request failed: %w", err)
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				break
			}
			resp.Body.Close()
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", nil, TokenUsage{}, fmt.Errorf("MLX streaming error %d: %s", resp.StatusCode, parseProviderErrorBody(bodyBytes))
	}

	var (
		textBuf  string
		usage    TokenUsage
		assembler = stream.NewAssembler()
	)

	for chunk := range stream.ParseOAICompatStream(ctx, resp.Body) {
		switch {
		case chunk.Err != nil:
			return "", nil, TokenUsage{}, chunk.Err
		case chunk.Usage != nil:
			usage = TokenUsage{
				InputTokens:       chunk.Usage.InputTokens,
				OutputTokens:      chunk.Usage.OutputTokens,
				CachedInputTokens: chunk.Usage.CachedInputTokens,
			}
		case chunk.ToolDelta != nil:
			assembler.Feed(chunk.ToolDelta)
		case chunk.TextDelta != "":
			textBuf += chunk.TextDelta
		}
	}

	assembled := assembler.Assemble()
	toolCalls := make([]OAIToolCall, 0, len(assembled))
	for _, tc := range assembled {
		oaiTC := OAIToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: OAIFunctionCall{
				Name:      tc.Name,
				Arguments: tc.Arguments,
			},
		}
		if tc.ThoughtSignature != "" {
			oaiTC.ExtraContent = &OAIToolCallExtras{
				Google: OAIToolCallGoogle{ThoughtSignature: tc.ThoughtSignature},
			}
		}
		toolCalls = append(toolCalls, oaiTC)
	}

	return textBuf, toolCalls, usage, nil
}
