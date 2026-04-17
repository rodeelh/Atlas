package dashboards

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	m := New(dir, "")
	m.SetRuntimeFetcher(&stubFetcher{body: []byte(`{"count":1}`), status: 200})
	return m
}

func TestCreateDraftThenCommitFlow(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	// 1. create draft
	res, err := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"My Dashboard"}`))
	if err != nil || !res.Success {
		t.Fatalf("create_draft: err=%v success=%v summary=%s", err, res.Success, res.Summary)
	}
	dID, _ := res.Artifacts["id"].(string)
	if dID == "" {
		t.Fatal("expected id in artifacts")
	}

	// 2. add a runtime data source
	addSrc, err := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "status",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	}))
	if err != nil || !addSrc.Success {
		t.Fatalf("add_data_source: err=%v summary=%s", err, addSrc.Summary)
	}

	// 3. add a widget
	addW, err := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeHalf,
		"preset":   PresetMetric,
		"title":    "Status",
		"bindings": []any{map[string]any{"source": "status"}},
	}))
	if err != nil || !addW.Success {
		t.Fatalf("add_widget: err=%v summary=%s", err, addW.Summary)
	}

	// 4. commit
	commit, err := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if err != nil || !commit.Success {
		t.Fatalf("commit: err=%v summary=%s", err, commit.Summary)
	}

	// Verify status flipped to live and layout was packed.
	d, err := m.store.Get(dID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if d.Status != StatusLive {
		t.Fatalf("expected status=live, got %q", d.Status)
	}
	if d.CommittedAt == nil {
		t.Fatal("CommittedAt should be set")
	}
	if len(d.Widgets) != 1 || d.Widgets[0].GridW == 0 {
		t.Fatalf("expected packed widget, got %+v", d.Widgets)
	}
}

func TestAddDataSourceRejectsBadRuntimeEndpoint(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"X"}`))
	dID := res.Artifacts["id"].(string)

	got, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "bad",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/not-allowed"},
	}))
	if got.Success {
		t.Fatal("expected failure for non-allowlisted endpoint")
	}
	if !strings.Contains(got.Summary, "allowlist") {
		t.Fatalf("expected allowlist error in summary, got %q", got.Summary)
	}
}

func TestAddWidgetRejectsUnknownBinding(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"X"}`))
	dID := res.Artifacts["id"].(string)

	got, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeHalf,
		"preset":   PresetMetric,
		"bindings": []any{map[string]any{"source": "ghost"}},
	}))
	if got.Success {
		t.Fatal("expected failure for unknown source")
	}
	if !strings.Contains(got.Summary, "unknown source") {
		t.Fatalf("expected unknown-source error, got %q", got.Summary)
	}
}

func TestLoadDraftRefusesLive(t *testing.T) {
	m := newTestModule(t)
	// Save a live dashboard directly.
	_, err := m.store.Save(Dashboard{
		ID:     "d1",
		Name:   "Live",
		Status: StatusLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.loadDraft("d1"); err == nil {
		t.Fatal("expected loadDraft to refuse live dashboard")
	}
}

func TestCoerceObjectAcceptsJSONString(t *testing.T) {
	out, err := coerceObject(`{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if out["a"].(float64) != 1 {
		t.Fatalf("unexpected: %v", out)
	}
}

func TestCoerceBindingsFromString(t *testing.T) {
	out, err := coerceBindings(`[{"source":"a"}, {"source":"b","path":"x.y"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Source != "a" || out[1].Path != "x.y" {
		t.Fatalf("unexpected bindings: %+v", out)
	}
}

// mustJSON is a test helper that marshals v or fails.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
