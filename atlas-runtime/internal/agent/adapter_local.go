package agent

import "context"

// localAdapter handles providers that do not reliably support streaming with
// tools: AtlasEngine (llama.cpp), LM Studio, and Ollama. It makes a single
// non-streaming call and emits the full response as one text delta event.
type localAdapter struct{ p ProviderConfig }

func (a *localAdapter) Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error) {
	ch := make(chan TurnEvent, 64)
	go func() {
		defer close(ch)
		if ctx.Err() != nil {
			return
		}
		send := func(ev TurnEvent) bool {
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}

		msgs := coalesceForLocalProvider(req.Messages)
		msg, _, usage, err := callOpenAICompatNonStreaming(ctx, a.p, msgs, req.Tools)
		if err != nil {
			send(TurnEvent{Type: EventError, Err: err})
			return
		}

		if text, ok := msg.Content.(string); ok && text != "" {
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

		u := TokenUsage{
			InputTokens:       usage.InputTokens,
			OutputTokens:      usage.OutputTokens,
			CachedInputTokens: usage.CachedInputTokens,
		}
		send(TurnEvent{Type: EventDone, Usage: &u})
	}()
	return ch, nil
}
