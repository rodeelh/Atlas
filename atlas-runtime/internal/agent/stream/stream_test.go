package stream_test

import (
	"context"
	"strings"
	"testing"

	"atlas-runtime-go/internal/agent/stream"
)

// ── Scanner ──────────────────────────────────────────────────────────────────

func TestScanner_DataLines(t *testing.T) {
	input := "data: hello\ndata: world\n"
	sc := stream.NewScanner(strings.NewReader(input))
	var got []string
	for sc.Next() {
		got = append(got, sc.Line())
	}
	if sc.Err() != nil {
		t.Fatalf("unexpected error: %v", sc.Err())
	}
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("unexpected lines: %v", got)
	}
}

func TestScanner_SkipsNonDataLines(t *testing.T) {
	input := ": comment\nevent: ping\ndata: payload\n"
	sc := stream.NewScanner(strings.NewReader(input))
	var got []string
	for sc.Next() {
		got = append(got, sc.Line())
	}
	if len(got) != 1 || got[0] != "payload" {
		t.Fatalf("unexpected lines: %v", got)
	}
}

func TestScanner_StopsAtDONE(t *testing.T) {
	input := "data: first\ndata: [DONE]\ndata: after\n"
	sc := stream.NewScanner(strings.NewReader(input))
	var got []string
	for sc.Next() {
		got = append(got, sc.Line())
	}
	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("expected only first chunk before DONE, got: %v", got)
	}
}

// ── Assembler ─────────────────────────────────────────────────────────────────

func TestAssembler_SingleToolCall(t *testing.T) {
	a := stream.NewAssembler()
	a.Feed(&stream.ToolDelta{Index: 0, ID: "call_1", Type: "function", Name: "my_tool"})
	a.Feed(&stream.ToolDelta{Index: 0, ArgsDelta: `{"k`})
	a.Feed(&stream.ToolDelta{Index: 0, ArgsDelta: `ey":"val"}`})
	calls := a.Assemble()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	c := calls[0]
	if c.ID != "call_1" || c.Name != "my_tool" || c.Arguments != `{"key":"val"}` {
		t.Fatalf("unexpected call: %+v", c)
	}
}

func TestAssembler_MultipleToolCalls(t *testing.T) {
	a := stream.NewAssembler()
	a.Feed(&stream.ToolDelta{Index: 0, ID: "c0", Name: "tool_a"})
	a.Feed(&stream.ToolDelta{Index: 1, ID: "c1", Name: "tool_b"})
	a.Feed(&stream.ToolDelta{Index: 0, ArgsDelta: `{}`})
	a.Feed(&stream.ToolDelta{Index: 1, ArgsDelta: `{"x":1}`})
	calls := a.Assemble()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "tool_a" || calls[1].Name != "tool_b" {
		t.Fatalf("wrong order: %v %v", calls[0].Name, calls[1].Name)
	}
}

func TestAssembler_DefaultType(t *testing.T) {
	a := stream.NewAssembler()
	a.Feed(&stream.ToolDelta{Index: 0, ID: "x", Name: "fn"})
	calls := a.Assemble()
	if len(calls) != 1 || calls[0].Type != "function" {
		t.Fatalf("expected default type 'function', got: %+v", calls)
	}
}

func TestAssembler_GeminiThoughtSignature(t *testing.T) {
	a := stream.NewAssembler()
	a.Feed(&stream.ToolDelta{Index: 0, ID: "tc1", Name: "search", ThoughtSignature: "sig_abc"})
	calls := a.Assemble()
	if calls[0].ThoughtSignature != "sig_abc" {
		t.Fatalf("thought signature not preserved: %+v", calls[0])
	}
}

func TestAssembler_Empty(t *testing.T) {
	a := stream.NewAssembler()
	if calls := a.Assemble(); calls != nil {
		t.Fatalf("expected nil for empty assembler, got %v", calls)
	}
}

// ── ParseOAICompatStream ──────────────────────────────────────────────────────

func oaiSSE(frames ...string) string {
	var sb strings.Builder
	for _, f := range frames {
		sb.WriteString("data: ")
		sb.WriteString(f)
		sb.WriteByte('\n')
	}
	sb.WriteString("data: [DONE]\n")
	return sb.String()
}

func collectChunks(ctx context.Context, r string) []stream.Chunk {
	ch := stream.ParseOAICompatStream(ctx, strings.NewReader(r))
	var out []stream.Chunk
	for c := range ch {
		out = append(out, c)
	}
	return out
}

func TestParseOAICompatStream_TextDelta(t *testing.T) {
	sse := oaiSSE(
		`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":" world"},"finish_reason":"stop"}]}`,
	)
	chunks := collectChunks(context.Background(), sse)
	var text string
	var finish string
	for _, c := range chunks {
		text += c.TextDelta
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
	}
	if text != "Hello world" {
		t.Fatalf("unexpected text: %q", text)
	}
	if finish != "stop" {
		t.Fatalf("unexpected finish reason: %q", finish)
	}
}

func TestParseOAICompatStream_ToolCall(t *testing.T) {
	sse := oaiSSE(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","type":"function","function":{"name":"weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":"tool_calls"}]}`,
	)
	chunks := collectChunks(context.Background(), sse)
	var deltas []*stream.ToolDelta
	var finish string
	for _, c := range chunks {
		if c.ToolDelta != nil {
			deltas = append(deltas, c.ToolDelta)
		}
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
	}
	if finish != "tool_calls" {
		t.Fatalf("unexpected finish reason: %q", finish)
	}
	a := stream.NewAssembler()
	for _, d := range deltas {
		a.Feed(d)
	}
	calls := a.Assemble()
	if len(calls) != 1 || calls[0].Name != "weather" || calls[0].ID != "tc1" {
		t.Fatalf("unexpected assembled calls: %+v", calls)
	}
}

func TestParseOAICompatStream_Usage(t *testing.T) {
	sse := oaiSSE(
		`{"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`{"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}}`,
	)
	chunks := collectChunks(context.Background(), sse)
	var u *stream.StreamUsage
	for _, c := range chunks {
		if c.Usage != nil {
			u = c.Usage
		}
	}
	if u == nil {
		t.Fatal("no usage chunk received")
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 || u.CachedInputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestParseOAICompatStream_MalformedLinesSkipped(t *testing.T) {
	sse := "data: not-json\ndata: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	chunks := collectChunks(context.Background(), sse)
	var text string
	for _, c := range chunks {
		text += c.TextDelta
	}
	if text != "ok" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestParseOAICompatStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sse := oaiSSE(`{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`)
	ch := stream.ParseOAICompatStream(ctx, strings.NewReader(sse))
	// drain — should not block
	for range ch {
	}
}
