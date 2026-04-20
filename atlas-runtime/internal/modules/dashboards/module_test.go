package dashboards

import (
	"context"
	"encoding/json"
	"errors"
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

func TestUpdateDraftWidgetUpdatesPresetAndRepacks(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Editable"}`))
	dID := res.Artifacts["id"].(string)

	if got, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "status",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !got.Success {
		t.Fatalf("add_data_source: %s", got.Summary)
	}
	added, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":       dID,
		"size":     SizeQuarter,
		"preset":   PresetMetric,
		"title":    "Before",
		"bindings": []any{map[string]any{"source": "status"}},
		"options":  map[string]any{"path": "count"},
	}))
	if !added.Success {
		t.Fatalf("add_widget: %s", added.Summary)
	}
	widget := added.Artifacts["widget"].(Widget)

	title := "After"
	description := "Edited in the draft inspector"
	size := SizeHalf
	preset := PresetMarkdown
	options := map[string]any{"path": "text"}
	updated, err := m.updateDraftWidget(dID, widget.ID, WidgetUpdateRequest{
		Title:       &title,
		Description: &description,
		Size:        &size,
		Preset:      &preset,
		Options:     options,
	})
	if err != nil {
		t.Fatalf("updateDraftWidget: %v", err)
	}
	if updated.Status != StatusDraft {
		t.Fatalf("expected draft to remain draft, got %q", updated.Status)
	}
	if len(updated.Widgets) != 1 {
		t.Fatalf("expected one widget, got %d", len(updated.Widgets))
	}
	got := updated.Widgets[0]
	if got.Title != title || got.Description != description || got.Size != size || got.Code.Preset != preset {
		t.Fatalf("widget was not updated: %+v", got)
	}
	if got.GridW != 6 || got.GridH == 0 {
		t.Fatalf("expected updated widget to be repacked as half, got %+v", got)
	}
}

func TestUpdateDraftLayoutPreservesCoordinates(t *testing.T) {
	m := newTestModule(t)
	saved, err := m.store.Save(Dashboard{
		ID:     "draft-layout",
		Name:   "Draft Layout",
		Status: StatusDraft,
		Layout: LayoutHints{Columns: 12},
		Widgets: []Widget{
			{ID: "w1", Size: SizeQuarter, Code: WidgetCode{Mode: ModePreset, Preset: PresetMetric}, GridX: 0, GridY: 0, GridW: 3, GridH: 2},
			{ID: "w2", Size: SizeQuarter, Code: WidgetCode{Mode: ModePreset, Preset: PresetMetric}, GridX: 3, GridY: 0, GridW: 3, GridH: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := m.updateDraftLayout(saved.ID, LayoutUpdateRequest{Widgets: []LayoutWidgetUpdate{
		{ID: "w1", GridX: 6, GridY: 1, GridW: 4, GridH: 3},
		{ID: "w2", GridX: 0, GridY: 0, GridW: 3, GridH: 2},
	}})
	if err != nil {
		t.Fatalf("updateDraftLayout: %v", err)
	}
	if got := updated.Widgets[0]; got.GridX != 6 || got.GridY != 1 || got.GridW != 4 || got.GridH != 3 {
		t.Fatalf("layout was not preserved: %+v", got)
	}
}

func TestUpdateDraftLayoutRejectsOverlap(t *testing.T) {
	m := newTestModule(t)
	saved, err := m.store.Save(Dashboard{
		ID:     "draft-overlap",
		Name:   "Draft Overlap",
		Status: StatusDraft,
		Layout: LayoutHints{Columns: 12},
		Widgets: []Widget{
			{ID: "w1", Size: SizeQuarter, Code: WidgetCode{Mode: ModePreset, Preset: PresetMetric}, GridX: 0, GridY: 0, GridW: 3, GridH: 2},
			{ID: "w2", Size: SizeQuarter, Code: WidgetCode{Mode: ModePreset, Preset: PresetMetric}, GridX: 3, GridY: 0, GridW: 3, GridH: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.updateDraftLayout(saved.ID, LayoutUpdateRequest{Widgets: []LayoutWidgetUpdate{
		{ID: "w1", GridX: 0, GridY: 0, GridW: 4, GridH: 2},
		{ID: "w2", GridX: 2, GridY: 0, GridW: 4, GridH: 2},
	}})
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestLiveDashboardCanCreateDraftAndPublishBack(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	live, err := m.store.Save(Dashboard{
		ID:     "live-layout",
		Name:   "Live Layout",
		Status: StatusLive,
		Layout: LayoutHints{Columns: 12},
		Widgets: []Widget{{
			ID: "w1", Size: SizeQuarter, Code: WidgetCode{Mode: ModePreset, Preset: PresetMarkdown, Options: map[string]any{"text": "ok"}}, GridX: 0, GridY: 0, GridW: 3, GridH: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := m.editDraftForDashboard(live.ID)
	if err != nil {
		t.Fatalf("editDraftForDashboard: %v", err)
	}
	if draft.ID == live.ID || draft.Status != StatusDraft || draft.BaseDashboardID != live.ID {
		t.Fatalf("unexpected draft: %+v", draft)
	}
	updated, err := m.updateDraftLayout(draft.ID, LayoutUpdateRequest{Widgets: []LayoutWidgetUpdate{
		{ID: "w1", GridX: 4, GridY: 2, GridW: 5, GridH: 3},
	}})
	if err != nil {
		t.Fatalf("updateDraftLayout: %v", err)
	}
	published, err := m.commitDraftDashboard(ctx, updated.ID)
	if err != nil {
		t.Fatalf("commitDraftDashboard: %v", err)
	}
	if published.ID != live.ID || published.Status != StatusLive || published.BaseDashboardID != "" {
		t.Fatalf("unexpected published dashboard: %+v", published)
	}
	if got := published.Widgets[0]; got.GridX != 4 || got.GridY != 2 || got.GridW != 5 || got.GridH != 3 {
		t.Fatalf("published layout was not preserved: %+v", got)
	}
	if _, err := m.store.Get(draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected draft to be removed after publish, got %v", err)
	}
}

func TestUpdateDraftWidgetRefusesLiveDashboard(t *testing.T) {
	m := newTestModule(t)
	saved, err := m.store.Save(Dashboard{
		ID:     "live",
		Name:   "Live",
		Status: StatusLive,
		Widgets: []Widget{{
			ID:   "w1",
			Size: SizeQuarter,
			Code: WidgetCode{Mode: ModePreset, Preset: PresetMetric},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	title := "Nope"
	if _, err := m.updateDraftWidget(saved.ID, "w1", WidgetUpdateRequest{Title: &title}); err == nil {
		t.Fatal("expected live dashboards to reject widget updates")
	}
}

func TestUpdateDraftCodeWidgetCompilesTSX(t *testing.T) {
	m := newTestModule(t)
	saved, err := m.store.Save(Dashboard{
		ID:     "draft-code",
		Name:   "Draft Code",
		Status: StatusDraft,
		Widgets: []Widget{{
			ID:   "code-1",
			Size: SizeHalf,
			Code: WidgetCode{Mode: ModeCode, TSX: `export default function Widget(){ return null }`},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tsx := `import { Card, Text } from '@atlas/ui'
export default function Widget(){ return <Card><Text>OK</Text></Card> }`
	updated, err := m.updateDraftWidget(saved.ID, "code-1", WidgetUpdateRequest{TSX: &tsx})
	if err != nil {
		t.Fatalf("updateDraftWidget code: %v", err)
	}
	got := updated.Widgets[0].Code
	if got.TSX != tsx || got.Compiled == "" || got.Hash == "" {
		t.Fatalf("expected compiled code fields, got %+v", got)
	}
}

func TestUpdateDraftCodeWidgetReturnsCompileError(t *testing.T) {
	m := newTestModule(t)
	saved, err := m.store.Save(Dashboard{
		ID:     "draft-code-invalid",
		Name:   "Draft Code Invalid",
		Status: StatusDraft,
		Widgets: []Widget{{
			ID:   "code-1",
			Size: SizeHalf,
			Code: WidgetCode{Mode: ModeCode, TSX: `export default function Widget(){ return null }`},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tsx := `this is not valid ::::: typescript`
	if _, err := m.updateDraftWidget(saved.ID, "code-1", WidgetUpdateRequest{TSX: &tsx}); err == nil {
		t.Fatal("expected compile failure for invalid tsx")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "widget.tsx:1:6") || !strings.Contains(msg, "1 | this is not valid ::::: typescript") {
			t.Fatalf("expected compile error with source location, got %q", msg)
		}
	}
}

func TestUpdateDraftWidgetRejectsUnknownBinding(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Editable"}`))
	dID := res.Artifacts["id"].(string)
	added, _ := m.skillAddWidget(ctx, mustJSON(map[string]any{
		"id":     dID,
		"size":   SizeQuarter,
		"preset": PresetMetric,
		"title":  "Before",
	}))
	if !added.Success {
		t.Fatalf("add_widget: %s", added.Summary)
	}
	widget := added.Artifacts["widget"].(Widget)
	bindings := []DataSourceBinding{{Source: "ghost"}}
	if _, err := m.updateDraftWidget(dID, widget.ID, WidgetUpdateRequest{Bindings: &bindings}); err == nil {
		t.Fatal("expected unknown binding to be rejected")
	}
}

func TestResolveWidgetDataProjectsBindingPath(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&fiveWidgetFetcher{})
	ctx := context.Background()

	d := Dashboard{
		ID:     "projected",
		Name:   "Projected",
		Status: StatusDraft,
		Sources: []DataSource{{
			Name:    "status",
			Kind:    SourceKindRuntime,
			Config:  map[string]any{"endpoint": "/status"},
			Refresh: RefreshPolicy{Mode: RefreshManual},
		}},
		Widgets: []Widget{{
			ID:       "w1",
			Size:     SizeQuarter,
			Bindings: []DataSourceBinding{{Source: "status", Path: "items[0]"}},
			Code:     WidgetCode{Mode: ModePreset, Preset: PresetMarkdown, Options: map[string]any{"path": "title"}},
		}},
	}
	if _, err := m.store.Save(d); err != nil {
		t.Fatal(err)
	}
	got, err := m.resolveWidgetData(ctx, d, d.Widgets[0])
	if err != nil {
		t.Fatalf("resolveWidgetData: %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected projected object, got %T: %v", got, got)
	}
	if obj["title"] != "agent-alpha" {
		t.Fatalf("expected first projected item, got %+v", obj)
	}
}

func TestCommitValidationUsesProjectedBindingPath(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&fiveWidgetFetcher{})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Projected Commit"}`))
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
		"size":     SizeThird,
		"preset":   PresetList,
		"title":    "Projected agents",
		"bindings": []any{map[string]any{"source": "status", "path": "items"}},
		"options":  map[string]any{"labelKey": "title", "subKey": "state"},
	})); !r.Success {
		t.Fatalf("add_widget: %s", r.Summary)
	}
	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if !commit.Success {
		t.Fatalf("expected commit to validate projected array, got %q", commit.Summary)
	}
}

func TestCommitValidationAcceptsPhaseFivePresets(t *testing.T) {
	m := newTestModule(t)
	m.SetRuntimeFetcher(&fiveWidgetFetcher{})
	ctx := context.Background()

	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Preset Expansion"}`))
	dID := res.Artifacts["id"].(string)
	if r, _ := m.skillAddDataSource(ctx, mustJSON(map[string]any{
		"id":     dID,
		"name":   "status",
		"kind":   SourceKindRuntime,
		"config": map[string]any{"endpoint": "/status"},
	})); !r.Success {
		t.Fatalf("add_data_source: %s", r.Summary)
	}
	widgets := []map[string]any{
		{
			"id":       dID,
			"size":     SizeQuarter,
			"preset":   PresetProgress,
			"title":    "Progress",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"path": "activeConversationCount", "max": 10},
		},
		{
			"id":       dID,
			"size":     SizeQuarter,
			"preset":   PresetGauge,
			"title":    "Gauge",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"path": "activeConversationCount", "max": 10},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetStatusGrid,
			"title":    "Status grid",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"itemsPath": "items", "labelKey": "title", "statusKey": "state"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetAreaChart,
			"title":    "Area chart",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "series", "x": "date", "y": "value"},
		},
		{
			"id":       dID,
			"size":     SizeThird,
			"preset":   PresetPieChart,
			"title":    "Pie chart",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "segments", "labelKey": "label", "valueKey": "value"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetKPIGroup,
			"title":    "KPI group",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"itemsPath": "metrics", "labelKey": "label", "valueKey": "value", "deltaKey": "delta"},
		},
		{
			"id":       dID,
			"size":     SizeThird,
			"preset":   PresetDonutChart,
			"title":    "Donut chart",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "segments", "labelKey": "label", "valueKey": "value"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetScatter,
			"title":    "Scatter chart",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "series", "x": "date", "y": "value"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetStacked,
			"title":    "Stacked chart",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "stacked", "x": "date", "seriesKeys": []any{"open", "closed"}},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetTimeline,
			"title":    "Timeline",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"itemsPath": "events", "timeKey": "date", "labelKey": "title", "bodyKey": "summary", "statusKey": "state"},
		},
		{
			"id":       dID,
			"size":     SizeHalf,
			"preset":   PresetHeatmap,
			"title":    "Heatmap",
			"bindings": []any{map[string]any{"source": "status"}},
			"options":  map[string]any{"seriesPath": "daily", "dateKey": "date", "valueKey": "value"},
		},
	}
	for _, spec := range widgets {
		if r, _ := m.skillAddWidget(ctx, mustJSON(spec)); !r.Success {
			t.Fatalf("add_widget preset=%v: %s", spec["preset"], r.Summary)
		}
	}
	commit, _ := m.skillCommit(ctx, mustJSON(map[string]any{"id": dID}))
	if !commit.Success {
		t.Fatalf("expected phase 5 presets to validate, got %q", commit.Summary)
	}
}

func TestAddCodeWidgetCompilesImmediately(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	res, _ := m.skillCreateDraft(ctx, json.RawMessage(`{"name":"Code Widget Add"}`))
	dID := res.Artifacts["id"].(string)
	result, _ := m.skillAddCodeWidget(ctx, mustJSON(map[string]any{
		"id":    dID,
		"size":  SizeHalf,
		"title": "TSX widget",
		"tsx":   `import { Card, Text } from '@atlas/ui'; export default function Widget(){ return <Card><Text>OK</Text></Card> }`,
	}))
	if !result.Success {
		t.Fatalf("expected add_code_widget success, got %q", result.Summary)
	}
	widget := result.Artifacts["widget"].(Widget)
	if widget.Code.Compiled == "" || widget.Code.Hash == "" {
		t.Fatalf("expected compiled code widget, got %+v", widget.Code)
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
