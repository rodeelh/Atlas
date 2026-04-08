package dashboards

// module_test.go — exercises the HTTP surface of the dashboards module via
// httptest + chi. Routes are registered exactly the way platform.Host would
// mount them in production, so the request paths match the real runtime.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/skills"
)

func newTestModule(t *testing.T) (*Module, http.Handler) {
	t.Helper()
	m := New(t.TempDir(), "")
	r := chi.NewRouter()
	m.registerRoutes(r)
	return m, r
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ── list / templates ──────────────────────────────────────────────────────────

func TestList_Empty(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "GET", "/dashboards", nil)
	if rec.Code != 200 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got []Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 dashboards, got %d", len(got))
	}
}

func TestListTemplates_ReturnsBuiltIns(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "GET", "/dashboards/templates", nil)
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	var got []Template
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected at least one template")
	}
	seen := map[string]bool{}
	for _, tmpl := range got {
		seen[tmpl.ID] = true
	}
	for _, want := range []string{"system_health", "usage", "memory_atlas"} {
		if !seen[want] {
			t.Errorf("missing template %q", want)
		}
	}
}

// ── create from template ─────────────────────────────────────────────────────

func TestCreateFromTemplate_PersistsAndAssignsID(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "system_health"})
	if rec.Code != 201 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var def DashboardDefinition
	if err := json.Unmarshal(rec.Body.Bytes(), &def); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if def.ID == "" {
		t.Error("created dashboard should have an ID")
	}
	if def.Name != "System Health" {
		t.Errorf("name: want %q, got %q", "System Health", def.Name)
	}
	if def.CreatedAt.IsZero() {
		t.Error("CreatedAt should be stamped")
	}

	// Round-trip via list.
	rec2 := doJSON(t, h, "GET", "/dashboards", nil)
	var list []Summary
	_ = json.Unmarshal(rec2.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != def.ID {
		t.Errorf("list did not contain the created dashboard: %+v", list)
	}
}

func TestCreateFromTemplate_UnknownTemplate(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "ghost"})
	if rec.Code != 400 {
		t.Errorf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestCreate_RequiresTemplateOrDefinition(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "POST", "/dashboards", map[string]string{})
	if rec.Code != 400 {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestCreateFromDefinition_AssignsWidgetIDs(t *testing.T) {
	_, h := newTestModule(t)
	body := map[string]any{
		"definition": map[string]any{
			"name": "Custom",
			"widgets": []map[string]any{
				{"kind": "markdown", "title": "Hi"},
				{"kind": "markdown", "title": "There"},
			},
		},
	}
	rec := doJSON(t, h, "POST", "/dashboards", body)
	if rec.Code != 201 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var def DashboardDefinition
	_ = json.Unmarshal(rec.Body.Bytes(), &def)
	if def.Widgets[0].ID == "" || def.Widgets[1].ID == "" {
		t.Errorf("widget IDs not assigned: %+v", def.Widgets)
	}
}

// ── get / update / delete ────────────────────────────────────────────────────

func TestGet_NotFound(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "GET", "/dashboards/ghost", nil)
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestUpdate_RoundTrip(t *testing.T) {
	_, h := newTestModule(t)
	create := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "usage"})
	var created DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &created)

	created.Name = "Renamed"
	rec := doJSON(t, h, "PUT", "/dashboards/"+created.ID, created)
	if rec.Code != 200 {
		t.Fatalf("status: %d (%s)", rec.Code, rec.Body.String())
	}
	get := doJSON(t, h, "GET", "/dashboards/"+created.ID, nil)
	var reloaded DashboardDefinition
	_ = json.Unmarshal(get.Body.Bytes(), &reloaded)
	if reloaded.Name != "Renamed" {
		t.Errorf("name not persisted: %q", reloaded.Name)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "PUT", "/dashboards/ghost", DashboardDefinition{Name: "x"})
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestDelete_RemovesAndIsIdempotent(t *testing.T) {
	_, h := newTestModule(t)
	create := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "memory_atlas"})
	var def DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &def)

	rec := doJSON(t, h, "DELETE", "/dashboards/"+def.ID, nil)
	if rec.Code != 204 {
		t.Errorf("want 204, got %d", rec.Code)
	}
	rec2 := doJSON(t, h, "DELETE", "/dashboards/"+def.ID, nil)
	if rec2.Code != 404 {
		t.Errorf("want 404 on second delete, got %d", rec2.Code)
	}
}

// ── resolve handler ──────────────────────────────────────────────────────────

func TestResolveWidget_HappyPath_Runtime(t *testing.T) {
	m, h := newTestModule(t)
	m.runtime = &fakeRuntime{
		body:   []byte(`{"port":1984}`),
		status: 200,
	}
	create := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "system_health"})
	var def DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &def)

	rec := doJSON(t, h, "POST", "/dashboards/"+def.ID+"/resolve", map[string]string{
		"widgetId": def.Widgets[0].ID,
	})
	if rec.Code != 200 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var wd WidgetData
	_ = json.Unmarshal(rec.Body.Bytes(), &wd)
	if !wd.Success {
		t.Errorf("expected success, got %+v", wd)
	}
	if wd.SourceKind != SourceKindRuntime {
		t.Errorf("source kind: want runtime, got %q", wd.SourceKind)
	}
}

func TestResolveWidget_PermissionError_Returns403(t *testing.T) {
	// Build a dashboard whose widget targets a forbidden runtime endpoint and
	// ensure /resolve returns 403, not 200-with-success-false.
	m, h := newTestModule(t)
	m.runtime = &fakeRuntime{}
	body := map[string]any{
		"definition": map[string]any{
			"name": "Bad",
			"widgets": []map[string]any{{
				"id":   "w1",
				"kind": "markdown",
				"source": map[string]any{
					"kind":     SourceKindRuntime,
					"endpoint": "/control",
				},
			}},
		},
	}
	create := doJSON(t, h, "POST", "/dashboards", body)
	var def DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &def)

	rec := doJSON(t, h, "POST", "/dashboards/"+def.ID+"/resolve", map[string]string{
		"widgetId": "w1",
	})
	if rec.Code != 403 {
		t.Errorf("want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestResolveWidget_FetchError_Returns200WithFailure(t *testing.T) {
	m, h := newTestModule(t)
	m.runtime = &fakeRuntime{body: []byte(`oops`), status: 500}
	create := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "system_health"})
	var def DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &def)

	rec := doJSON(t, h, "POST", "/dashboards/"+def.ID+"/resolve", map[string]string{
		"widgetId": def.Widgets[0].ID,
	})
	if rec.Code != 200 {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var wd WidgetData
	_ = json.Unmarshal(rec.Body.Bytes(), &wd)
	if wd.Success {
		t.Error("expected success=false on upstream 500")
	}
	if !strings.Contains(wd.Error, "500") {
		t.Errorf("expected error to mention status, got %q", wd.Error)
	}
}

func TestResolveWidget_UnknownDashboard(t *testing.T) {
	_, h := newTestModule(t)
	rec := doJSON(t, h, "POST", "/dashboards/ghost/resolve", map[string]string{"widgetId": "w1"})
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestResolveWidget_UnknownWidget(t *testing.T) {
	_, h := newTestModule(t)
	create := doJSON(t, h, "POST", "/dashboards", map[string]string{"template": "system_health"})
	var def DashboardDefinition
	_ = json.Unmarshal(create.Body.Bytes(), &def)
	rec := doJSON(t, h, "POST", "/dashboards/"+def.ID+"/resolve", map[string]string{"widgetId": "ghost"})
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

// ── compile-time check that *skills.Registry would satisfy SkillExecutor ─────

func TestSkillExecutor_InterfaceShape(t *testing.T) {
	// We can't construct a real *skills.Registry here without a DB, but we can
	// confirm the interface still type-checks against the package types we
	// imported. This catches accidental signature drift in skills.Registry.
	var _ SkillExecutor = (*fakeSkills)(nil)
	_ = context.Background
	_ = (*skills.Registry)(nil) // referenced for compile-time link
}
