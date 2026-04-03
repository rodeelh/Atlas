package agent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atlas-runtime-go/internal/skills"
)

// ── Mock registry ─────────────────────────────────────────────────────────────

// mockRegistry is a minimal skills.Registry substitute for loop tests.
// It holds a map of actionID → handler function and a stateful set.
type mockRegistry struct {
	handlers map[string]func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error)
	stateful map[string]bool
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		handlers: make(map[string]func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error)),
		stateful: make(map[string]bool),
	}
}

func (m *mockRegistry) register(id string, stateful bool, fn func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error)) {
	m.handlers[id] = fn
	if stateful {
		m.stateful[id] = true
	}
}

// parallelLoopSkills is the interface subset that Loop needs during tool execution.
// We implement it on mockRegistry so we can test without a real skills.Registry.

func (m *mockRegistry) IsStateful(actionID string) bool    { return m.stateful[actionID] }
func (m *mockRegistry) NeedsApproval(_ string) bool        { return false }
func (m *mockRegistry) PermissionLevel(_ string) string    { return "read" }
func (m *mockRegistry) GetActionClass(_ string) skills.ActionClass {
	return skills.ActionClassRead
}
func (m *mockRegistry) Canonicalize(id string) string { return id }
func (m *mockRegistry) Execute(ctx context.Context, actionID string, args json.RawMessage) (skills.ToolResult, error) {
	fn, ok := m.handlers[actionID]
	if !ok {
		return skills.ToolResult{}, nil
	}
	return fn(ctx, args)
}
func (m *mockRegistry) ToolDefinitions() []map[string]any        { return nil }
func (m *mockRegistry) SelectiveToolDefs(_ string) []map[string]any { return nil }
func (m *mockRegistry) RedactArgs(args json.RawMessage) string   { return string(args) }

// ── Mock broadcaster ──────────────────────────────────────────────────────────

type mockBC struct{}

func (b *mockBC) Emit(_ string, _ EmitEvent) {}
func (b *mockBC) Finish(_ string)            {}

// ── Helpers ───────────────────────────────────────────────────────────────────

// execResult is a captured result from a tool execution.
type execResult struct {
	tc      OAIToolCall
	result  *toolExecResult
}

// runExecToolBatch replicates the three-pass parallel logic from loop.go Run()
// using the mock registry, giving us fine-grained timing control in tests.
// Returns results in original call order — same guarantee as the production code.
func runExecToolBatch(t *testing.T, reg *mockRegistry, tcs []OAIToolCall) []*toolExecResult {
	t.Helper()

	results := make([]*toolExecResult, len(tcs))
	ctx := context.Background()

	// Pass 1: parallel stateless
	var wg sync.WaitGroup
	for i, tc := range tcs {
		if reg.IsStateful(tc.Function.Name) {
			continue
		}
		wg.Add(1)
		go func(idx int, tc OAIToolCall) {
			defer wg.Done()
			result, err := reg.Execute(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			results[idx] = &toolExecResult{result: result, execErr: err}
		}(i, tc)
	}
	wg.Wait()

	// Pass 2: serial stateful
	for i, tc := range tcs {
		if !reg.IsStateful(tc.Function.Name) {
			continue
		}
		result, err := reg.Execute(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
		results[i] = &toolExecResult{result: result, execErr: err}
	}

	return results
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestParallelToolExecution_Concurrent verifies that three stateless tools with
// 100ms sleeps complete in well under 300ms (serial would be ~300ms).
func TestParallelToolExecution_Concurrent(t *testing.T) {
	reg := newMockRegistry()
	delay := 100 * time.Millisecond
	for _, id := range []string{"weather.current", "weather.forecast", "finance.quote"} {
		id := id
		reg.register(id, false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
			time.Sleep(delay)
			return skills.ToolResult{Success: true, Summary: id + " done"}, nil
		})
	}

	tcs := []OAIToolCall{
		{ID: "1", Function: OAIFunctionCall{Name: "weather.current", Arguments: "{}"}},
		{ID: "2", Function: OAIFunctionCall{Name: "weather.forecast", Arguments: "{}"}},
		{ID: "3", Function: OAIFunctionCall{Name: "finance.quote", Arguments: "{}"}},
	}

	start := time.Now()
	results := runExecToolBatch(t, reg, tcs)
	elapsed := time.Since(start)

	// Serial would take ≥300ms. Parallel should finish in ≤200ms.
	if elapsed >= 250*time.Millisecond {
		t.Errorf("expected parallel execution <250ms, got %v (serial would be ~300ms)", elapsed)
	}

	// All results must be populated.
	for i, r := range results {
		if r == nil {
			t.Fatalf("result[%d] is nil", i)
		}
		if r.execErr != nil {
			t.Errorf("result[%d] unexpected error: %v", i, r.execErr)
		}
	}
}

// TestParallelToolExecution_ResultOrder verifies that results land in the original
// call index regardless of which goroutine finishes first.
func TestParallelToolExecution_ResultOrder(t *testing.T) {
	reg := newMockRegistry()

	// Tool 0 takes longest, tool 2 takes shortest — if order was wrong, 2 would appear at index 0.
	delays := map[string]time.Duration{
		"slow.op":   120 * time.Millisecond,
		"medium.op": 60 * time.Millisecond,
		"fast.op":   10 * time.Millisecond,
	}
	for id, d := range delays {
		id, d := id, d
		reg.register(id, false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
			time.Sleep(d)
			return skills.ToolResult{Success: true, Summary: id}, nil
		})
	}

	tcs := []OAIToolCall{
		{ID: "a", Function: OAIFunctionCall{Name: "slow.op",   Arguments: "{}"}},
		{ID: "b", Function: OAIFunctionCall{Name: "medium.op", Arguments: "{}"}},
		{ID: "c", Function: OAIFunctionCall{Name: "fast.op",   Arguments: "{}"}},
	}
	expected := []string{"slow.op", "medium.op", "fast.op"}

	results := runExecToolBatch(t, reg, tcs)

	for i, r := range results {
		if r == nil {
			t.Fatalf("result[%d] is nil", i)
		}
		if r.result.Summary != expected[i] {
			t.Errorf("result[%d]: want summary %q, got %q", i, expected[i], r.result.Summary)
		}
	}
}

// TestParallelToolExecution_StatefulSerial verifies that stateful tools (browser.*)
// are never run concurrently — the second call starts only after the first finishes.
func TestParallelToolExecution_StatefulSerial(t *testing.T) {
	reg := newMockRegistry()

	var activeCount int64 // peak concurrent executions
	var peakConcurrent int64

	for _, id := range []string{"browser.navigate", "browser.screenshot", "browser.click"} {
		id := id
		reg.register(id, true, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
			cur := atomic.AddInt64(&activeCount, 1)
			for {
				peak := atomic.LoadInt64(&peakConcurrent)
				if cur <= peak || atomic.CompareAndSwapInt64(&peakConcurrent, peak, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt64(&activeCount, -1)
			return skills.ToolResult{Success: true, Summary: id}, nil
		})
	}

	tcs := []OAIToolCall{
		{ID: "1", Function: OAIFunctionCall{Name: "browser.navigate",   Arguments: "{}"}},
		{ID: "2", Function: OAIFunctionCall{Name: "browser.screenshot", Arguments: "{}"}},
		{ID: "3", Function: OAIFunctionCall{Name: "browser.click",      Arguments: "{}"}},
	}

	runExecToolBatch(t, reg, tcs)

	if peakConcurrent > 1 {
		t.Errorf("stateful browser tools ran concurrently: peak concurrent = %d, want 1", peakConcurrent)
	}
}

// TestParallelToolExecution_MixedBatch verifies a batch containing both stateless
// and stateful tools: stateless run in parallel, stateful run serially after,
// and all results land in the correct index slots.
func TestParallelToolExecution_MixedBatch(t *testing.T) {
	reg := newMockRegistry()

	var browserActiveCount int64
	var browserPeak int64

	reg.register("weather.current", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		time.Sleep(50 * time.Millisecond)
		return skills.ToolResult{Success: true, Summary: "weather"}, nil
	})
	reg.register("finance.quote", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		time.Sleep(50 * time.Millisecond)
		return skills.ToolResult{Success: true, Summary: "finance"}, nil
	})
	for _, id := range []string{"browser.navigate", "browser.screenshot"} {
		id := id
		reg.register(id, true, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
			cur := atomic.AddInt64(&browserActiveCount, 1)
			for {
				peak := atomic.LoadInt64(&browserPeak)
				if cur <= peak || atomic.CompareAndSwapInt64(&browserPeak, peak, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt64(&browserActiveCount, -1)
			return skills.ToolResult{Success: true, Summary: id}, nil
		})
	}

	tcs := []OAIToolCall{
		{ID: "1", Function: OAIFunctionCall{Name: "weather.current",    Arguments: "{}"}},
		{ID: "2", Function: OAIFunctionCall{Name: "browser.navigate",   Arguments: "{}"}},
		{ID: "3", Function: OAIFunctionCall{Name: "finance.quote",      Arguments: "{}"}},
		{ID: "4", Function: OAIFunctionCall{Name: "browser.screenshot", Arguments: "{}"}},
	}
	expected := []string{"weather", "browser.navigate", "finance", "browser.screenshot"}

	results := runExecToolBatch(t, reg, tcs)

	for i, r := range results {
		if r == nil {
			t.Fatalf("result[%d] is nil", i)
		}
		if r.result.Summary != expected[i] {
			t.Errorf("result[%d]: want %q, got %q", i, expected[i], r.result.Summary)
		}
	}
	if browserPeak > 1 {
		t.Errorf("browser tools ran concurrently: peak = %d", browserPeak)
	}
}

// TestParallelToolExecution_OneError verifies that when one parallel tool fails,
// the others still complete and the error lands in the correct index slot.
func TestParallelToolExecution_OneError(t *testing.T) {
	reg := newMockRegistry()

	reg.register("good.a", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		time.Sleep(20 * time.Millisecond)
		return skills.ToolResult{Success: true, Summary: "ok-a"}, nil
	})
	reg.register("bad.op", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		time.Sleep(10 * time.Millisecond)
		return skills.ToolResult{}, &testError{"simulated failure"}
	})
	reg.register("good.b", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		time.Sleep(30 * time.Millisecond)
		return skills.ToolResult{Success: true, Summary: "ok-b"}, nil
	})

	tcs := []OAIToolCall{
		{ID: "1", Function: OAIFunctionCall{Name: "good.a", Arguments: "{}"}},
		{ID: "2", Function: OAIFunctionCall{Name: "bad.op", Arguments: "{}"}},
		{ID: "3", Function: OAIFunctionCall{Name: "good.b", Arguments: "{}"}},
	}

	results := runExecToolBatch(t, reg, tcs)

	// Index 0: success
	if results[0] == nil || results[0].execErr != nil {
		t.Errorf("result[0] should succeed, got: %v", results[0])
	}
	// Index 1: error
	if results[1] == nil || results[1].execErr == nil {
		t.Errorf("result[1] should have error, got nil")
	}
	if results[1].execErr.Error() != "simulated failure" {
		t.Errorf("result[1] wrong error: %v", results[1].execErr)
	}
	// Index 2: success
	if results[2] == nil || results[2].execErr != nil {
		t.Errorf("result[2] should succeed, got: %v", results[2])
	}
}

// TestParallelToolExecution_SingleTool verifies the common single-tool case
// doesn't regress — still executes and returns correctly.
func TestParallelToolExecution_SingleTool(t *testing.T) {
	reg := newMockRegistry()
	reg.register("weather.current", false, func(ctx context.Context, args json.RawMessage) (skills.ToolResult, error) {
		return skills.ToolResult{Success: true, Summary: "sunny"}, nil
	})

	tcs := []OAIToolCall{
		{ID: "1", Function: OAIFunctionCall{Name: "weather.current", Arguments: "{}"}},
	}

	results := runExecToolBatch(t, reg, tcs)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0] == nil {
		t.Fatal("result[0] is nil")
	}
	if results[0].result.Summary != "sunny" {
		t.Errorf("unexpected summary: %q", results[0].result.Summary)
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
