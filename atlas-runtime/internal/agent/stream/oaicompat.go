package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// oaiCompatChunk is the wire shape of one OAI-compat SSE data frame.
type oaiCompatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
				ExtraContent struct {
					Google struct {
						ThoughtSignature string `json:"thought_signature"`
					} `json:"google"`
				} `json:"extra_content"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// ParseOAICompatStream reads an OAI-compatible SSE stream from r and sends
// Chunk values into the returned channel. The channel is closed when the
// stream ends normally or the context is cancelled. A terminal Err chunk is
// sent before closing on any hard error.
//
// Emitted chunk types:
//   - TextDelta set   — one text token
//   - ToolDelta set   — one partial tool call fragment
//   - FinishReason set — finish signal (sent once, before usage)
//   - Usage set       — token counts (last chunk before close)
//   - Err set         — terminal error, channel closes immediately after
func ParseOAICompatStream(ctx context.Context, r io.Reader) <-chan Chunk {
	ch := make(chan Chunk, 64)
	go func() {
		defer close(ch)
		send := func(c Chunk) bool {
			select {
			case ch <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}

		sc := NewScanner(r)
		for sc.Next() {
			if ctx.Err() != nil {
				return
			}
			var frame oaiCompatChunk
			if err := json.Unmarshal([]byte(sc.Line()), &frame); err != nil {
				continue
			}

			// Usage arrives in a summary chunk (choices empty).
			if frame.Usage != nil {
				u := &StreamUsage{
					InputTokens:       frame.Usage.PromptTokens,
					OutputTokens:      frame.Usage.CompletionTokens,
					CachedInputTokens: frame.Usage.PromptTokensDetails.CachedTokens,
				}
				send(Chunk{Usage: u})
			}
			if len(frame.Choices) == 0 {
				continue
			}

			choice := frame.Choices[0]
			if choice.FinishReason != "" {
				if !send(Chunk{FinishReason: choice.FinishReason}) {
					return
				}
			}

			for _, tc := range choice.Delta.ToolCalls {
				d := &ToolDelta{
					Index:            tc.Index,
					ID:               tc.ID,
					Type:             tc.Type,
					Name:             tc.Function.Name,
					ArgsDelta:        tc.Function.Arguments,
					ThoughtSignature: tc.ExtraContent.Google.ThoughtSignature,
				}
				if !send(Chunk{ToolDelta: d}) {
					return
				}
			}

			if token := choice.Delta.Content; token != "" {
				if !send(Chunk{TextDelta: token}) {
					return
				}
			}
		}

		if err := sc.Err(); err != nil {
			send(Chunk{Err: fmt.Errorf("stream read error: %w", err)})
		}
	}()
	return ch
}
