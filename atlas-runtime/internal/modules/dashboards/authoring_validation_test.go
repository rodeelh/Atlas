package dashboards

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateLiveComputeRejectsStandaloneFreshPrompt(t *testing.T) {
	err := validateLiveCompute(map[string]any{
		"prompt":       "Write the latest AI news as JSON",
		"outputSchema": map[string]any{"items": "array"},
	})
	if err == nil || !strings.Contains(err.Error(), "must declare inputs") {
		t.Fatalf("expected freshness-inputs error, got %v", err)
	}
}

func TestPromptNeedsFreshInputs_KnowDoesNotTrigger(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{"Write what we know about neural networks", false}, // "know" contains "now" — must not match
		{"Summarize known issues in the codebase", false},   // "known" contains "now"
		{"Generate a summary right now", true},              // standalone "now" — must match
		{"What is the current weather in Paris", true},      // "current" as whole word
		{"Show concurrent request metrics", false},          // "concurrent" contains "current"
		{"List the latest releases", true},
	}
	for _, tc := range cases {
		got := promptNeedsFreshInputs(tc.prompt)
		if got != tc.want {
			t.Errorf("promptNeedsFreshInputs(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

func TestSkillCommitAllowsSoftSourceFailure(t *testing.T) {
	m := newTestModule(t)
	// 503 is a soft failure (not 401/403) — commit should succeed.
	m.SetRuntimeFetcher(&stubFetcher{body: []byte(`{"error":"unavailable"}`), status: 503})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Soft Fail"}`))
	dID := res.Artifacts["id"].(string)

	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "status",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}
	if r, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeQuarter,
		"preset":   PresetMetric,
		"title":    "Status",
		"bindings": []any{map[string]any{"source": "status"}},
	})); !r.Success {
		t.Fatalf("add_widget: %s", r.Summary)
	}

	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if !commit.Success {
		t.Fatalf("expected commit to succeed despite soft source failure, got %q", commit.Summary)
	}
}

func TestSkillCommitAllowsEmptyArraySource(t *testing.T) {
	m := newTestModule(t)
	// Empty array — fresh install with no chat history.
	m.SetRuntimeFetcher(&stubFetcher{body: []byte(`{"rows":[]}`), status: 200})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Empty Chart"}`))
	dID := res.Artifacts["id"].(string)

	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "metrics",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}
	if r, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeHalf,
		"preset":   PresetLineChart,
		"title":    "Metrics Over Time",
		"bindings": []any{map[string]any{"source": "metrics"}},
		"options":  map[string]any{"seriesPath": "rows", "x": "date", "y": "value"},
	})); !r.Success {
		t.Fatalf("add_widget: %s", r.Summary)
	}

	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if !commit.Success {
		t.Fatalf("expected commit to succeed with empty array source, got %q", commit.Summary)
	}
}

func TestSkillCommitRejectsHardSourceFailure(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&stubFetcher{body: []byte(`{"error":"unauthorized"}`), status: 401})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Auth Block"}`))
	dID := res.Artifacts["id"].(string)

	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "usage",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/usage/summary"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}
	if r, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeQuarter,
		"preset":   PresetMetric,
		"title":    "Usage",
		"bindings": []any{map[string]any{"source": "usage"}},
	})); !r.Success {
		t.Fatalf("add_widget: %s", r.Summary)
	}

	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if commit.Success {
		t.Fatal("expected commit to fail when a source returns 401")
	}
	if !strings.Contains(commit.Summary, "401") {
		t.Fatalf("expected 401 in commit error, got %q", commit.Summary)
	}
}

func TestSkillCommitRejectsInvalidListSchema(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&stubFetcher{body: []byte(`{"items":[{"name":"alpha"}]}`), status: 200})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Schema Block"}`))
	dID := res.Artifacts["id"].(string)

	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "agents",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}
	if r, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeThird,
		"preset":   PresetList,
		"title":    "Agents",
		"bindings": []any{map[string]any{"source": "agents"}},
		"options":  map[string]any{"itemsPath": "items", "labelKey": "title"},
	})); !r.Success {
		t.Fatalf("add_widget: %s", r.Summary)
	}

	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if commit.Success {
		t.Fatal("expected commit to fail when list schema does not prove labelKey")
	}
	if !strings.Contains(commit.Summary, "labelKey") {
		t.Fatalf("expected labelKey schema error, got %q", commit.Summary)
	}
}
