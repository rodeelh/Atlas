package dashboards

// five_widgets_test.go — end-to-end authoring & execution test.
//
// Exercises the "agent plans a dashboard, Atlas executes it" path. Every
// step maps to a real dashboard.* skill invocation; no shortcuts. After
// commit, widgets are resolved through the real resolver pipeline against
// stub data sources, confirming the packer, compile step, and data
// join-by-source all behave together.
//
// Five widgets cover every preset kind the UI renders via the packer:
//   1. metric       bound to /status
//   2. line_chart   bound to /memories     (series extraction)
//   3. bar_chart    bound to /usage/summary
//   4. table        bound to /memories
//   5. list         bound to /status       (pretends status has a list)
//
// A separate case (TestCommitCompilesCodeWidget) exercises the TSX compile
// step that `dashboard.commit` runs over every widget — the skill args
// don't expose code-mode authoring yet, so we inject it at the Widget
// struct level and then call skillCommit to confirm compileWidget fires
// during commit.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fiveWidgetFetcher returns canned JSON per allowlisted endpoint so each
// source resolves with distinct data.
type fiveWidgetFetcher struct {
	calls []string
}

func (f *fiveWidgetFetcher) Get(_ context.Context, path string, _ map[string]string) ([]byte, int, error) {
	f.calls = append(f.calls, path)
	switch path {
	case "/status":
		return []byte(`{
			"isRunning": true,
			"activeConversationCount": 3,
			"latencyMs": 182,
			"metrics": [
				{"label":"Open tasks","value":18,"delta":2.4,"tone":"ok"},
				{"label":"Alerts","value":3,"delta":-1.2,"tone":"warn"},
				{"label":"Blocked","value":1,"delta":0,"tone":"neutral"}
			],
			"series": [
				{"date":"Mon","value":1200},
				{"date":"Tue","value":1800},
				{"date":"Wed","value":2400}
			],
			"stacked": [
				{"date":"Mon","open":7,"closed":4},
				{"date":"Tue","open":5,"closed":6},
				{"date":"Wed","open":8,"closed":3}
			],
			"segments": [
				{"label":"Chat","value":4200},
				{"label":"Workflows","value":900},
				{"label":"Research","value":300}
			],
			"events": [
				{"date":"2026-04-10T09:00:00Z","title":"Atlas started","summary":"Runtime boot completed","state":"ok"},
				{"date":"2026-04-10T11:30:00Z","title":"Refresh lag","summary":"One source retried","state":"warn"}
			],
			"daily": [
				{"date":"2026-04-08","value":2},
				{"date":"2026-04-09","value":4},
				{"date":"2026-04-10","value":6}
			],
			"items": [
				{"title":"agent-alpha","state":"idle"},
				{"title":"agent-beta","state":"thinking"},
				{"title":"agent-gamma","state":"running"}
			]
		}`), 200, nil
	case "/memories":
		return []byte(`{
			"rows": [
				{"date":"2026-04-10","value":4,"title":"preferences"},
				{"date":"2026-04-11","value":6,"title":"projects"},
				{"date":"2026-04-12","value":9,"title":"workflow"},
				{"date":"2026-04-13","value":12,"title":"facts"}
			]
		}`), 200, nil
	case "/usage/summary":
		return []byte(`{
			"series": [
				{"date":"Mon","value":1200},
				{"date":"Tue","value":1800},
				{"date":"Wed","value":2400},
				{"date":"Thu","value":1550}
			]
		}`), 200, nil
	}
	return []byte(`{}`), 404, nil
}

func TestFiveWidgetPlanAndExecute(t *testing.T) {
	m := newTestModule(t)
	fetcher := &fiveWidgetFetcher{}
	m.SetRuntimeFetcher(fetcher)
	ctx := context.Background()

	// ── Plan phase: the agent calls these skills in order ───────────────────
	// 1. create_draft
	res, err := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Five-Widget Smoke"}`))
	if err != nil || !res.Success {
		t.Fatalf("create_draft failed: err=%v summary=%s", err, res.Summary)
	}
	dID, _ := res.Artifacts["id"].(string)
	if dID == "" {
		t.Fatal("expected id in artifacts")
	}

	// 2. set_metadata — adds a description
	if r, err := m.skillSetMetadata(ctx, mustJSON(map[string]any{
		"id":          dID,
		"description": "Smoke-test dashboard with five diverse widgets.",
	})); err != nil || !r.Success {
		t.Fatalf("set_metadata: err=%v summary=%s", err, r.Summary)
	}

	// 3. add_data_source × 3
	sources := []map[string]any{
		{"id": dID, "name": "status", "kind": SourceKindRuntime, "config": map[string]any{"endpoint": "/status"}},
		{"id": dID, "name": "memories", "kind": SourceKindRuntime, "config": map[string]any{"endpoint": "/memories"}},
		{"id": dID, "name": "usage", "kind": SourceKindRuntime, "config": map[string]any{"endpoint": "/usage/summary"}},
	}
	for _, src := range sources {
		r, err := m.skillAddDataSource(ctx, mustJSON(src))
		if err != nil || !r.Success {
			t.Fatalf("add_data_source %q: err=%v summary=%s", src["name"], err, r.Summary)
		}
	}

	// 4. add_widget × 5 — one of every preset kind the viewer ships.
	widgetSpecs := []map[string]any{
		{
			"id":       dID,
			"size":     SizeQuarter,
			"preset":   PresetMetric,
			"title":    "Active conversations",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"path": "activeConversationCount", "format": "integer"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetLineChart,
			"title":    "Memories recorded",
			"bindings": []any{map[string]any{"source": "memories"}},
			"options":  map[string]any{"seriesPath": "rows", "x": "date", "y": "value"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetBarChart,
			"title":    "Token usage per day",
			"bindings": []any{map[string]any{"source": "usage"}},
			"options":  map[string]any{"seriesPath": "series", "x": "date", "y": "value", "color": "#6366f1"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetTable,
			"title":    "Memory rows",
			"bindings": []any{map[string]any{"source": "memories"}},
			"options":  map[string]any{"path": "rows", "columns": []any{"date", "title", "value"}, "limit": 10},
		},
		{
			"id":       dID,
			"size":     SizeThird,
			"preset":   PresetList,
			"title":    "Agents",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"itemsPath": "items", "labelKey": "title", "subKey": "state"},
		},
	}
	widgetIDs := make([]string, 0, len(widgetSpecs))
	for i, spec := range widgetSpecs {
		r, err := m.skillAddWidget(ctx, mustJSON(spec))
		if err != nil || !r.Success {
			t.Fatalf("add_widget[%d] preset=%v: err=%v summary=%s", i, spec["preset"], err, r.Summary)
		}
		w, ok := r.Artifacts["widget"].(Widget)
		if !ok {
			t.Fatalf("add_widget[%d]: widget artifact missing", i)
		}
		widgetIDs = append(widgetIDs, w.ID)
	}

	// 5. preview on one widget — proves the resolver is wired.
	if pr, err := m.skillPreview(ctx, mustJSON(map[string]any{
		"id": dID, "widgetId": widgetIDs[0],
	})); err != nil || !pr.Success {
		t.Fatalf("preview: err=%v summary=%s", err, pr.Summary)
	}

	// 6. commit — should flip status to live, compile any code widgets,
	//    and run the packer.
	cr, err := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if err != nil || !cr.Success {
		t.Fatalf("commit: err=%v summary=%s", err, cr.Summary)
	}

	// ── Execute phase: inspect the committed dashboard ──────────────────────
	d, err := m.store.Get(dID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if d.Status != StatusLive {
		t.Fatalf("expected status=live, got %q", d.Status)
	}
	if d.CommittedAt == nil {
		t.Fatal("CommittedAt should be set after commit")
	}
	if len(d.Widgets) != 5 {
		t.Fatalf("expected 5 widgets, got %d", len(d.Widgets))
	}
	if len(d.Sources) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(d.Sources))
	}
	if d.Layout.Columns != 12 {
		t.Fatalf("expected 12-column layout, got %d", d.Layout.Columns)
	}

	// Packer invariants: every widget gets a positive size and no two
	// widgets overlap on the grid.
	for i, w := range d.Widgets {
		if w.GridW == 0 || w.GridH == 0 {
			t.Fatalf("widget[%d] %s not packed: %+v", i, w.ID, w)
		}
		if w.GridX+w.GridW > d.Layout.Columns {
			t.Fatalf("widget[%d] %s overflows columns: x=%d w=%d cols=%d", i, w.ID, w.GridX, w.GridW, d.Layout.Columns)
		}
		for j := i + 1; j < len(d.Widgets); j++ {
			o := d.Widgets[j]
			if rectsIntersect(w, o) {
				t.Fatalf("widgets %s and %s overlap: %+v vs %+v", w.ID, o.ID, w, o)
			}
		}
	}

	// Resolve every widget through the real runtime resolver. Confirms the
	// binding-→-source-→-fetcher plumbing survived commit intact.
	for i, w := range d.Widgets {
		out, rerr := m.resolveWidgetData(ctx, d, w)
		if rerr != nil {
			t.Fatalf("resolve widget[%d] %s (preset=%s): %v", i, w.ID, w.Code.Preset, rerr)
		}
		if out == nil {
			t.Fatalf("resolve widget[%d] %s returned nil", i, w.ID)
		}
	}

	// Every runtime endpoint should have been hit at least once across the
	// preview + 5 resolves.
	for _, path := range []string{"/status", "/memories", "/usage/summary"} {
		if !containsStr(fetcher.calls, path) {
			t.Fatalf("fetcher was never asked for %s (calls=%v)", path, fetcher.calls)
		}
	}

	// 7. Agent-facing list/get skills reflect the new dashboard.
	listRes, err := m.skillList(ctx, json.RawMessage(`{}`))
	if err != nil || !listRes.Success {
		t.Fatalf("list skill: err=%v summary=%s", err, listRes.Summary)
	}
	if !strings.Contains(listRes.Summary, dID) && !strings.Contains(listRes.Summary, "Five-Widget") {
		t.Logf("list summary did not name the dashboard — non-fatal: %q", listRes.Summary)
	}
	getRes, err := m.skillGet(ctx, mustJSON(map[string]any{"id": dID}))
	if err != nil || !getRes.Success {
		t.Fatalf("get skill: err=%v summary=%s", err, getRes.Summary)
	}
}

// TestCommitCompilesCodeWidget proves the commit step preserves compiled code
// widgets added through the runtime authoring path.
func TestCommitCompilesCodeWidget(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&fiveWidgetFetcher{})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Code Widget"}`))
	dID := res.Artifacts["id"].(string)

	// Add a runtime source so the code widget has something to bind to.
	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "status",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}

	const tsx = `import { Card, Metric } from '@atlas/ui'
export default function Widget({ data }) {
  const value = (data && data.activeConversationCount) || 0
  return <Card title="Active"><Metric value={value} label="chats" /></Card>
}`
	if r, _ := m.skillAddCodeWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"title":    "Active chats",
		"size":     SizeQuarter,
		"bindings": []any{map[string]any{"source": "status"}},
		"tsx":      tsx,
	})); !r.Success {
		t.Fatalf("add_code_widget: %s", r.Summary)
	}

	// Commit should run compile over the code widget.
	if cr, err := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID})); err != nil || !cr.Success {
		t.Fatalf("commit: err=%v summary=%s", err, cr.Summary)
	}

	d2, err := m.store.Get(dID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(d2.Widgets))
	}
	w := d2.Widgets[0]
	if w.Code.Compiled == "" {
		t.Fatal("expected compiled ESM output to be populated after commit")
	}
	if w.Code.Hash == "" {
		t.Fatal("expected hash to be populated after commit")
	}
	if !strings.Contains(w.Code.Compiled, "@atlas/ui") {
		t.Fatal("expected @atlas/ui import to survive as external in compiled output")
	}
}

// ── test helpers ────────────────────────────────────────────────────────────

func rectsIntersect(a, b Widget) bool {
	return a.GridX < b.GridX+b.GridW &&
		a.GridX+a.GridW > b.GridX &&
		a.GridY < b.GridY+b.GridH &&
		a.GridY+a.GridH > b.GridY
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
