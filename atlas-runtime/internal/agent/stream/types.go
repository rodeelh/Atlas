// Package stream provides stdlib-only primitives for parsing AI provider
// server-sent event (SSE) streams. It has no dependency on internal/agent —
// adapters convert stream.Chunk to agent.TurnEvent at the boundary.
package stream

// Chunk is one event emitted by ParseOAICompatStream. Exactly one of the
// fields is set per chunk; Err is set only on terminal errors.
type Chunk struct {
	TextDelta    string
	FinishReason string
	ToolDelta    *ToolDelta
	Usage        *StreamUsage
	Err          error
}

// ToolDelta carries one fragment of a streaming tool call. Multiple deltas
// with the same Index are accumulated by the caller or by ParseOAICompatStream
// before emitting an assembled ToolCall chunk.
type ToolDelta struct {
	Index            int
	ID               string
	Type             string
	Name             string
	ArgsDelta        string
	ThoughtSignature string // Gemini thinking models only
}

// StreamUsage holds token counts from the final summary chunk.
type StreamUsage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}
