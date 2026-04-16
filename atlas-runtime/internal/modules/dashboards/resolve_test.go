package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ── stub implementations ──────────────────────────────────────────────────────

type stubFetcher struct {
	lastPath  string
	lastQuery map[string]string
	body      []byte
	status    int
	err       error
}

func (s *stubFetcher) Get(_ context.Context, path string, q map[string]string) ([]byte, int, error) {
	s.lastPath = path
	s.lastQuery = q
	return s.body, s.status, s.err
}

type stubSkillExec struct {
	lastAction string
	lastArgs   json.RawMessage
	result     skillExecResult
	err        error
}

func (s *stubSkillExec) Execute(_ context.Context, action string, args json.RawMessage) (skillExecResult, error) {
	s.lastAction = action
	s.lastArgs = args
	return s.result, s.err
}

type stubLiveRunner struct {
	lastSpec   LiveComputeSpec
	lastInputs map[string]any
	result     any
	err        error
}

func (s *stubLiveRunner) Run(_ context.Context, spec LiveComputeSpec, inputs map[string]any) (any, error) {
	s.lastSpec = spec
	s.lastInputs = inputs
	return s.result, s.err
}

// ── runtime resolver ──────────────────────────────────────────────────────────

func TestResolveRuntimeAllowed(t *testing.T) {
	f := &stubFetcher{body: []byte(`{"x":1}`), status: 200}
	data, err := resolveRuntime(context.Background(), f, map[string]any{"endpoint": "/status"})
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok || m["x"] == nil {
		t.Fatalf("expected parsed JSON, got %T: %v", data, data)
	}
	if f.lastPath != "/status" {
		t.Fatalf("fetcher called with wrong path: %q", f.lastPath)
	}
}

func TestResolveRuntimeBlocksDisallowed(t *testing.T) {
	f := &stubFetcher{body: []byte("{}"), status: 200}
	_, err := resolveRuntime(context.Background(), f, map[string]any{"endpoint": "/secrets"})
	if err == nil {
		t.Fatal("expected error for disallowed endpoint")
	}
	if !strings.Contains(err.Error(), "not on the dashboards allowlist") {
		t.Fatalf("expected allowlist error, got %v", err)
	}
	// Must not reach the fetcher.
	if f.lastPath != "" {
		t.Fatalf("fetcher should not have been called, lastPath=%q", f.lastPath)
	}
}

func TestResolveRuntimeRejectsRelativePath(t *testing.T) {
	f := &stubFetcher{}
	_, err := resolveRuntime(context.Background(), f, map[string]any{"endpoint": "status"})
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("expected leading-slash error, got %v", err)
	}
}

// ── skill resolver ────────────────────────────────────────────────────────────

func TestResolveSkillRequiresAction(t *testing.T) {
	e := &stubSkillExec{}
	_, err := resolveSkill(context.Background(), e, map[string]any{})
	if err == nil {
		t.Fatal("expected action required error")
	}
}

func TestResolveSkillPropagatesSuccess(t *testing.T) {
	e := &stubSkillExec{result: skillExecResult{Success: true, Summary: `{"ok":true}`}}
	data, err := resolveSkill(context.Background(), e, map[string]any{
		"action": "websearch.query",
		"args":   map[string]any{"q": "hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := data.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("expected parsed map{ok:true}, got %T: %v", data, data)
	}
	if e.lastAction != "websearch.query" {
		t.Fatalf("wrong action: %s", e.lastAction)
	}
}

func TestResolveSkillPropagatesFailure(t *testing.T) {
	e := &stubSkillExec{result: skillExecResult{Success: false, Summary: "boom"}}
	_, err := resolveSkill(context.Background(), e, map[string]any{"action": "x"})
	if err == nil {
		t.Fatal("expected error when skill fails")
	}
}

// ── live compute resolver ─────────────────────────────────────────────────────

func TestResolveLiveComputeRequiresRunner(t *testing.T) {
	_, err := resolveLiveCompute(context.Background(), resolverDeps{}, map[string]any{
		"prompt":       "summarize",
		"inputs":       []any{"a"},
		"outputSchema": map[string]any{},
	}, map[string]any{"a": 1})
	if err == nil {
		t.Fatal("expected runner-not-wired error")
	}
}

func TestResolveLiveComputeMissingInput(t *testing.T) {
	r := &stubLiveRunner{result: map[string]any{}}
	_, err := resolveLiveCompute(context.Background(), resolverDeps{liveRunner: r}, map[string]any{
		"prompt":       "x",
		"inputs":       []any{"missing"},
		"outputSchema": map[string]any{},
	}, map[string]any{})
	if err == nil || !errors.Is(err, ErrSourceMissing) {
		t.Fatalf("expected ErrSourceMissing, got %v", err)
	}
}

func TestResolveLiveComputeHappyPath(t *testing.T) {
	r := &stubLiveRunner{result: map[string]any{"title": "hi"}}
	data, err := resolveLiveCompute(context.Background(), resolverDeps{liveRunner: r}, map[string]any{
		"prompt":       "summarize",
		"inputs":       []any{"a"},
		"outputSchema": map[string]any{"type": "object"},
	}, map[string]any{"a": "payload"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m, ok := data.(map[string]any); !ok || m["title"] != "hi" {
		t.Fatalf("unexpected result: %v", data)
	}
	if r.lastSpec.Prompt != "summarize" {
		t.Fatalf("spec not propagated")
	}
	if got := r.lastInputs["a"]; got != "payload" {
		t.Fatalf("inputs not propagated, got %v", got)
	}
}

// ── isPermissionError ─────────────────────────────────────────────────────────

func TestIsPermissionError(t *testing.T) {
	cases := map[string]bool{
		"runtime endpoint /x is not on the dashboards allowlist": true,
		"sql query may not contain \"delete\"":                   true,
		"sql query must start with SELECT (...)":                 true,
		"unknown chat_analytics query \"x\"":                     true,
		"widget tsx contains forbidden token":                    true,
		"network timeout":                                        false,
		"":                                                       false,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := isPermissionError(err); got != want {
			t.Errorf("isPermissionError(%q) = %v, want %v", msg, got, want)
		}
	}
}
