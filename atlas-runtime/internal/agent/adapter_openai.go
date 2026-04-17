package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"atlas-runtime-go/internal/agent/stream"
)

// openAIAdapter handles the OpenAI Responses API (not the OAI-compat chat
// completions endpoint). It owns the full HTTP + SSE parse cycle using
// stream.NewScanner for line reading but implements the Responses event model
// inline (incompatible with OAI-compat framing).
type openAIAdapter struct{ p ProviderConfig }

func (a *openAIAdapter) Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
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

		ctx, cancel := withProviderTimeout(ctx, openAIResponsesStreamTimeout)
		defer cancel()

		instructions, rest := extractSystemInstructions(req.Messages)
		reqBody := map[string]any{
			"model":             a.p.Model,
			"input":             convertMessagesToResponsesInput(rest),
			"stream":            true,
			"max_output_tokens": 4096,
			"store":             false,
		}
		if instructions != "" {
			reqBody["instructions"] = instructions
		}
		if converted := convertChatToolsToResponsesTools(req.Tools); len(converted) > 0 {
			reqBody["tools"] = converted
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}

		resp, err := doOpenAIResponsesRequest(ctx, a.p, body, true)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("OpenAI Responses streaming request failed: %w", err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("OpenAI Responses streaming error %d: %s", resp.StatusCode, parseProviderErrorBody(bodyBytes))})
			return
		}

		var (
			usage       TokenUsage
			firstToken  time.Duration
			chunkCount  int
			completed   bool
			outputItems []responsesOutputItem
			fullText    string
			streamStart = time.Now()
		)

		sc := stream.NewScanner(resp.Body)
		for sc.Next() {
			if ctx.Err() != nil {
				return
			}
			var envelope struct {
				Type     string              `json:"type"`
				Delta    string              `json:"delta"`
				Item     responsesOutputItem `json:"item"`
				Response responsesResponse   `json:"response"`
				Error    *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(sc.Line()), &envelope); err != nil {
				continue
			}

			switch envelope.Type {
			case "response.output_text.delta":
				if envelope.Delta == "" {
					continue
				}
				if firstToken <= 0 {
					firstToken = time.Since(streamStart)
				}
				chunkCount++
				fullText += envelope.Delta
				if !send(TurnEvent{Type: EventTextDelta, Text: envelope.Delta}) {
					return
				}

			case "response.output_item.added", "response.output_item.done":
				if envelope.Item.Type != "" {
					outputItems = append(outputItems, envelope.Item)
				}

			case "response.completed":
				completed = true
				msg, _, u, err := extractResponsesMessage(envelope.Response)
				if err != nil {
					send(TurnEvent{Type: EventError, Err: err})
					return
				}
				usage = u
				if text, _ := msg.Content.(string); text != "" && fullText == "" {
					if !send(TurnEvent{Type: EventTextDelta, Text: text}) {
						return
					}
					fullText = text
				}
				for i := range msg.ToolCalls {
					tc := msg.ToolCalls[i]
					if !send(TurnEvent{Type: EventToolCall, ToolCall: &tc}) {
						return
					}
				}

			case "response.failed":
				if envelope.Error != nil && envelope.Error.Message != "" {
					send(TurnEvent{Type: EventError, Err: errors.New(envelope.Error.Message)})
				} else {
					send(TurnEvent{Type: EventError, Err: errors.New("OpenAI Responses stream failed")})
				}
				return
			}
		}

		if err := sc.Err(); err != nil {
			send(TurnEvent{Type: EventError, Err: fmt.Errorf("OpenAI Responses stream read error: %w", err)})
			return
		}

		if !completed && len(outputItems) > 0 {
			msg, _, u, err := extractResponsesMessage(responsesResponse{Output: outputItems})
			if err != nil {
				send(TurnEvent{Type: EventError, Err: err})
				return
			}
			usage = u
			if text, _ := msg.Content.(string); text != "" && fullText == "" {
				if !send(TurnEvent{Type: EventTextDelta, Text: text}) {
					return
				}
			}
			for i := range msg.ToolCalls {
				tc := msg.ToolCalls[i]
				if !send(TurnEvent{Type: EventToolCall, ToolCall: &tc}) {
					return
				}
			}
		}

		send(TurnEvent{Type: EventDone, Usage: &usage})
	}()
	return ch, nil
}
