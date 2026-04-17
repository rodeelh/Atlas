package agent

import (
	"context"
	"encoding/json"
)

// EventType tags the kind of event emitted by a ProviderAdapter.
type EventType int

const (
	EventTextDelta EventType = iota // partial assistant text token
	EventToolCall                   // one complete tool call (assembled from stream)
	EventDone                       // stream finished; Usage is populated
	EventError                      // unrecoverable error; Err is populated
)

// TurnEvent is one item on the channel returned by ProviderAdapter.Stream.
// Only the fields relevant to the EventType are set.
type TurnEvent struct {
	Type     EventType
	Text     string       // EventTextDelta
	ToolCall *OAIToolCall // EventToolCall — pointer so zero-value is distinguishable
	Usage    *TokenUsage  // EventDone
	Err      error        // EventError
}

// TurnRequest is the normalised input every ProviderAdapter receives.
// It mirrors the fields the agent loop needs to make one streaming call.
type TurnRequest struct {
	Messages []OAIMessage
	Tools    []map[string]any
	ConvID   string
	TurnID   string
}

// parseProviderErrorBody extracts a clean error message from an HTTP error
// response body. It understands:
//   - OpenRouter extended errors: error.metadata.raw (most descriptive)
//   - Standard OAI errors:        error.message
//   - Anthropic errors:           error.error.message
//   - Fallback:                   raw body string
func parseProviderErrorBody(body []byte) string {
	var envelope struct {
		Error *struct {
			Message  string          `json:"message"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != nil {
		// OpenRouter: metadata.raw is the upstream provider's actual message.
		if len(envelope.Error.Metadata) > 0 {
			var meta struct {
				Raw string `json:"raw"`
			}
			if err := json.Unmarshal(envelope.Error.Metadata, &meta); err == nil && meta.Raw != "" {
				return meta.Raw
			}
		}
		if envelope.Error.Message != "" {
			return envelope.Error.Message
		}
	}
	return string(body)
}

// ProviderAdapter is the single interface the agent loop will use once Phase 4
// lands. In Phase 1 it is only constructed and exercised by adapter tests;
// the loop still calls streamWithToolDetection directly.
//
// Stream returns a channel that emits TurnEvents in order:
//
//	0..n EventTextDelta   — partial text tokens, in arrival order
//	0..n EventToolCall    — one per assembled tool call
//	1    EventDone        — terminal: usage populated, channel closes after
//	or
//	1    EventError       — terminal: Err populated, channel closes after
//
// The channel is always closed when the stream ends (including on ctx cancel).
// Callers must drain the channel to avoid goroutine leaks.
type ProviderAdapter interface {
	Stream(ctx context.Context, req TurnRequest) (<-chan TurnEvent, error)
}
