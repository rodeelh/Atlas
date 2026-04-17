package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"atlas-runtime-go/internal/agent/stream"
)

// anthropicAdapter owns the full Anthropic HTTP + SSE parse cycle directly.
// It uses stream.NewScanner for SSE line reading but implements Anthropic's
// event format inline (incompatible with OAI-compat framing).
type anthropicAdapter struct{ p ProviderConfig }

func (a *anthropicAdapter) Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
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

		systemPrompt, anthropicMsgs := convertToAnthropicMessages(req.Messages)
		reqBody := map[string]any{
			"model":      a.p.Model,
			"messages":   anthropicMsgs,
			"max_tokens": 4096,
			"stream":     true,
		}
		if systemPrompt != "" {
			reqBody["system"] = anthropicCachedSystem(systemPrompt)
		}
		if len(req.Tools) > 0 {
			reqBody["tools"] = anthropicCachedTools(convertToAnthropicTools(req.Tools))
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST",
			anthropicBaseURL+"/messages",
			bytes.NewReader(body),
		)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", a.p.APIKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)
		httpReq.Header.Set("anthropic-beta", anthropicCachingBeta)

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("Anthropic streaming request failed: %w", err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("Anthropic streaming error %d: %s", resp.StatusCode, parseProviderErrorBody(bodyBytes))})
			return
		}

		type toolAccum struct {
			id   string
			name string
			args strings.Builder
		}
		var (
			usage  TokenUsage
			accums = map[int]*toolAccum{}
		)

		sc := stream.NewScanner(resp.Body)
		for sc.Next() {
			if ctx.Err() != nil {
				return
			}
			var event struct {
				Type         string `json:"type"`
				Index        int    `json:"index"`
				ContentBlock *struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
				Delta *struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
					StopReason  string `json:"stop_reason"`
				} `json:"delta"`
				Message *struct {
					Usage *struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(sc.Line()), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil && event.Message.Usage != nil {
					usage.InputTokens = event.Message.Usage.InputTokens
				}
			case "content_block_start":
				if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
					accums[event.Index] = &toolAccum{
						id:   event.ContentBlock.ID,
						name: event.ContentBlock.Name,
					}
				}
			case "content_block_delta":
				if event.Delta == nil {
					continue
				}
				switch event.Delta.Type {
				case "text_delta":
					if event.Delta.Text != "" {
						if !send(TurnEvent{Type: EventTextDelta, Text: event.Delta.Text}) {
							return
						}
					}
				case "input_json_delta":
					if acc := accums[event.Index]; acc != nil {
						acc.args.WriteString(event.Delta.PartialJSON)
					}
				}
			case "message_delta":
				if event.Usage != nil {
					usage.OutputTokens = event.Usage.OutputTokens
				}
			}
		}

		if err := sc.Err(); err != nil {
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("Anthropic stream read error: %w", err)})
			return
		}

		for i := 0; i < len(accums); i++ {
			acc, ok := accums[i]
			if !ok {
				break
			}
			tc := OAIToolCall{
				ID:   acc.id,
				Type: "function",
				Function: OAIFunctionCall{
					Name:      acc.name,
					Arguments: acc.args.String(),
				},
			}
			if !send(TurnEvent{Type: EventToolCall, ToolCall: &tc}) {
				return
			}
		}

		send(TurnEvent{Type: EventDone, Usage: &usage})
	}()
	return ch, nil
}
