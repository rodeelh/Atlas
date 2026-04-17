package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"atlas-runtime-go/internal/agent/stream"
)

// oaiCompatAdapter handles Gemini, OpenRouter, and any future OAI-compatible
// providers. It owns the full HTTP + SSE parse cycle directly using
// stream.ParseOAICompatStream, with no channelEmitter bridge.
type oaiCompatAdapter struct{ p ProviderConfig }

func (a *oaiCompatAdapter) Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
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

		reqBody := map[string]any{
			"model":          a.p.Model,
			"messages":       req.Messages,
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
		}
		if !isLocalProvider(a.p.Type) && a.p.Type != ProviderOpenAI {
			reqBody["max_tokens"] = 4096
		}
		if len(req.Tools) > 0 {
			reqBody["tools"] = req.Tools
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}

		url := oaiCompatBaseURL(a.p) + "/chat/completions"
		resp, err := doOpenAICompatRequest(ctx, a.p, url, body)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("AI streaming request failed (%s): %w", a.p.Type, err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("AI streaming error %d (%s): %s", resp.StatusCode, a.p.Type, parseProviderErrorBody(bodyBytes))})
			return
		}

		assembler := stream.NewAssembler()
		var usage TokenUsage
		var firstTokenAt time.Duration
		var chunkCount int
		streamStart := time.Now()

		for chunk := range stream.ParseOAICompatStream(ctx, resp.Body) {
			switch {
			case chunk.Err != nil:
				send(TurnEvent{Type: EventError, Err: chunk.Err})
				return
			case chunk.Usage != nil:
				usage = TokenUsage{
					InputTokens:       chunk.Usage.InputTokens,
					OutputTokens:      chunk.Usage.OutputTokens,
					CachedInputTokens: chunk.Usage.CachedInputTokens,
				}
			case chunk.ToolDelta != nil:
				assembler.Feed(chunk.ToolDelta)
			case chunk.TextDelta != "":
				if firstTokenAt <= 0 {
					firstTokenAt = time.Since(streamStart)
				}
				chunkCount++
				if !send(TurnEvent{Type: EventTextDelta, Text: chunk.TextDelta}) {
					return
				}
			}
		}

		for _, tc := range assembler.Assemble() {
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
			if !send(TurnEvent{Type: EventToolCall, ToolCall: &oaiTC}) {
				return
			}
		}

		send(TurnEvent{Type: EventDone, Usage: &usage})
	}()
	return ch, nil
}
