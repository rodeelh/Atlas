package mind

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"atlas-runtime-go/internal/mind/thoughts"
)

// stubExecutor captures every Execute call and returns a scripted result.
type stubExecutor struct {
	mu           sync.Mutex
	calls        []stubExecCall
	resultByID   map[string]string
	errByID      map[string]error
	needsApprove map[string]bool
}

type stubExecCall struct {
	ActionID string
	Args     json.RawMessage
}

func newStubExecutor() *stubExecutor {
	return &stubExecutor{
		resultByID:   map[string]string{},
		errByID:      map[string]error{},
		needsApprove: map[string]bool{},
	}
}

func (s *stubExecutor) Execute(ctx context.Context, actionID string, args json.RawMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, stubExecCall{ActionID: actionID, Args: args})
	if err, ok := s.errByID[actionID]; ok {
		return "", err
	}
	if res, ok := s.resultByID[actionID]; ok {
		return res, nil
	}
	return `{"success":true,"summary":"ok"}`, nil
}

func (s *stubExecutor) NeedsApproval(actionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsApprove[actionID]
}

type stubProposer struct {
	calls []string
	err   error
}

func (s *stubProposer) ProposeFromThought(thoughtID, body, skillID string, args json.RawMessage, provenance string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.calls = append(s.calls, thoughtID)
	return "approval-" + thoughtID, nil
}

// thoughtAt builds a minimal Thought with the given score + class + action.
func thoughtAt(id string, score int, class thoughts.ActionClass, skillID string) thoughts.Thought {
	return thoughts.Thought{
		ID: id, Body: "body " + id,
		Confidence: 80, Value: 80, Class: class, Score: score,
		SurfacedMax: 2, Source: "test",
		Action: &thoughts.ProposedAction{SkillID: skillID, Args: map[string]any{"k": "v"}},
	}
}

func TestDispatcher_AutoExecuteRead(t *testing.T) {
	dir := t.TempDir()
	exec := newStubExecutor()
	exec.resultByID["openclaw.check"] = `{"success":true,"summary":"release 1.2.3 published"}`

	d := NewDispatcher(dir, exec, nil, nil, nil)
	list := []thoughts.Thought{thoughtAt("T-01", 98, thoughts.ClassRead, "openclaw.check")}

	result := d.Dispatch(context.Background(), list, 1, 3, "conv-1")

	if result.AutoExecuted != 1 {
		t.Errorf("AutoExecuted: got %d, want 1", result.AutoExecuted)
	}
	if result.Proposed != 0 {
		t.Errorf("Proposed: got %d, want 0", result.Proposed)
	}
	if len(exec.calls) != 1 || exec.calls[0].ActionID != "openclaw.check" {
		t.Errorf("executor not called correctly: %+v", exec.calls)
	}

	// Pending greetings should have the entry.
	queue, err := LoadPendingGreetings(dir)
	if err != nil {
		t.Fatalf("LoadPendingGreetings: %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("queue: got %d entries, want 1", len(queue))
	}
	if queue[0].ThoughtID != "T-01" || queue[0].SkillID != "openclaw.check" {
		t.Errorf("queue entry: %+v", queue[0])
	}
}

func TestDispatcher_PerNapRateLimit(t *testing.T) {
	dir := t.TempDir()
	exec := newStubExecutor()
	d := NewDispatcher(dir, exec, nil, nil, nil)

	list := []thoughts.Thought{
		thoughtAt("T-01", 98, thoughts.ClassRead, "a.one"),
		thoughtAt("T-02", 97, thoughts.ClassRead, "a.two"),
		thoughtAt("T-03", 96, thoughts.ClassRead, "a.three"),
	}
	// maxPerNap=1: only the first should execute, the rest are rate-limited.
	result := d.Dispatch(context.Background(), list, 1, 5, "conv-1")
	if result.AutoExecuted != 1 {
		t.Errorf("AutoExecuted: got %d, want 1", result.AutoExecuted)
	}
	if result.RateLimited != 2 {
		t.Errorf("RateLimited: got %d, want 2", result.RateLimited)
	}
	if len(exec.calls) != 1 {
		t.Errorf("executor calls: got %d, want 1", len(exec.calls))
	}
}

func TestDispatcher_PerDayRateLimit(t *testing.T) {
	dir := t.TempDir()
	exec := newStubExecutor()
	d := NewDispatcher(dir, exec, nil, nil, nil)

	// Burn 3 executions across 3 separate naps — at max 1 per nap, 3 per day.
	for i := 0; i < 3; i++ {
		list := []thoughts.Thought{thoughtAt("T-0"+string(rune('1'+i)), 98, thoughts.ClassRead, "s")}
		d.Dispatch(context.Background(), list, 1, 3, "conv")
	}
	// Fourth nap should hit the daily cap.
	list := []thoughts.Thought{thoughtAt("T-04", 98, thoughts.ClassRead, "s")}
	result := d.Dispatch(context.Background(), list, 1, 3, "conv")
	if result.AutoExecuted != 0 {
		t.Errorf("after daily cap: AutoExecuted %d, want 0", result.AutoExecuted)
	}
	if result.RateLimited != 1 {
		t.Errorf("after daily cap: RateLimited %d, want 1", result.RateLimited)
	}
}

func TestDispatcher_ProposeNonRead(t *testing.T) {
	dir := t.TempDir()
	exec := newStubExecutor()
	prop := &stubProposer{}
	d := NewDispatcher(dir, exec, prop, nil, nil)

	list := []thoughts.Thought{thoughtAt("T-01", 85, thoughts.ClassLocalWrite, "s")}
	result := d.Dispatch(context.Background(), list, 1, 3, "conv")

	if result.Proposed != 1 {
		t.Errorf("Proposed: got %d, want 1", result.Proposed)
	}
	if result.AutoExecuted != 0 {
		t.Errorf("AutoExecuted: got %d, want 0", result.AutoExecuted)
	}
	if len(prop.calls) != 1 || prop.calls[0] != "T-01" {
		t.Errorf("proposer not called: %+v", prop.calls)
	}
	if len(exec.calls) != 0 {
		t.Errorf("executor should not run for proposal path, got %+v", exec.calls)
	}
}

func TestDispatcher_SafetyCeilingHoldsForMaxConfidenceSideEffect(t *testing.T) {
	// An external_side_effect thought can score at most 93 (100*100*0.93/100).
	// It must NOT auto-execute, even with the executor wired and ready.
	dir := t.TempDir()
	exec := newStubExecutor()
	d := NewDispatcher(dir, exec, &stubProposer{}, nil, nil)

	// Score 93 is the math maximum for this class. The dispatcher must
	// route it to propose, never to auto-exec.
	t1 := thoughtAt("T-01", 93, thoughts.ClassExternalSideEffect, "s.send")
	result := d.Dispatch(context.Background(), []thoughts.Thought{t1}, 99, 99, "conv")
	if result.AutoExecuted != 0 {
		t.Errorf("SAFETY VIOLATION: external_side_effect at max score auto-executed")
	}
	if len(exec.calls) != 0 {
		t.Errorf("SAFETY VIOLATION: executor called for external_side_effect: %+v", exec.calls)
	}
	if result.Proposed != 1 {
		t.Errorf("expected propose, got %+v", result)
	}
}

func TestDispatcher_NoActionThoughtSkipped(t *testing.T) {
	dir := t.TempDir()
	d := NewDispatcher(dir, newStubExecutor(), nil, nil, nil)

	t1 := thoughtAt("T-01", 98, thoughts.ClassRead, "")
	t1.Action = nil // pure reflection
	result := d.Dispatch(context.Background(), []thoughts.Thought{t1}, 1, 3, "conv")

	if result.AutoExecuted != 0 || result.Proposed != 0 {
		t.Errorf("pure reflection shouldn't dispatch: %+v", result)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped: got %d, want 1", result.Skipped)
	}
}

func TestDispatcher_DefenseInDepthNeedsApproval(t *testing.T) {
	// Even if a thought somehow crosses 95 with class=read, the dispatcher
	// must bail if the registry itself reports the skill needs approval.
	// This is the belt-and-braces layer on top of the structural ceiling.
	dir := t.TempDir()
	exec := newStubExecutor()
	exec.needsApprove["weird.skill"] = true
	d := NewDispatcher(dir, exec, nil, nil, nil)

	t1 := thoughtAt("T-01", 99, thoughts.ClassRead, "weird.skill")
	result := d.Dispatch(context.Background(), []thoughts.Thought{t1}, 1, 3, "conv")

	if result.AutoExecuted != 0 {
		t.Errorf("should not auto-exec when NeedsApproval=true, got %d", result.AutoExecuted)
	}
	if len(exec.calls) != 0 {
		t.Errorf("executor should not run when NeedsApproval=true")
	}
	if len(result.Errors) == 0 {
		t.Errorf("expected error for defense-in-depth block")
	}
}

func TestDispatcher_ExecErrorCaptured(t *testing.T) {
	dir := t.TempDir()
	exec := newStubExecutor()
	exec.errByID["failing.skill"] = errors.New("network down")
	d := NewDispatcher(dir, exec, nil, nil, nil)

	t1 := thoughtAt("T-01", 98, thoughts.ClassRead, "failing.skill")
	result := d.Dispatch(context.Background(), []thoughts.Thought{t1}, 1, 3, "conv")

	if result.AutoExecuted != 0 {
		t.Errorf("failed exec should not count as executed: %+v", result)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %v", result.Errors)
	}
}

func TestPendingGreetings_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	entry := GreetingEntry{
		ThoughtID:  "T-01",
		Body:       "test body",
		SkillID:    "s.one",
		Result:     "the result",
		Provenance: "prov",
	}
	if err := appendPendingGreetingFile(dir, entry); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := appendPendingGreetingFile(dir, entry); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	queue, err := LoadPendingGreetings(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(queue) != 2 {
		t.Errorf("queue len: got %d, want 2", len(queue))
	}

	if err := ClearPendingGreetings(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	queue, _ = LoadPendingGreetings(dir)
	if len(queue) != 0 {
		t.Errorf("after clear: got %d, want 0", len(queue))
	}
}

func TestPendingGreetings_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Ensure no file exists.
	_ = os.Remove(filepath.Join(dir, "pending-greetings.json"))
	queue, err := LoadPendingGreetings(dir)
	if err != nil {
		t.Errorf("missing file should be nil error, got %v", err)
	}
	if len(queue) != 0 {
		t.Errorf("missing file: got %d entries, want 0", len(queue))
	}
}
