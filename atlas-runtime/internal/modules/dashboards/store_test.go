package dashboards

// store_test.go — stage 1 coverage for the dashboards store and templates.
//
// Mirrors the patterns used by internal/forge/store_test.go: temp dir per test,
// race-detector friendly, and validates the on-disk file remains valid JSON
// after concurrent writes.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newDef(id, name string) DashboardDefinition {
	return DashboardDefinition{
		ID:   id,
		Name: name,
		Widgets: []Widget{
			{ID: "w1", Kind: WidgetKindMarkdown, Title: "Hello", GridW: 12, GridH: 2},
		},
	}
}

// ── List / Get ────────────────────────────────────────────────────────────────

func TestList_EmptyDir_ReturnsEmptySlice(t *testing.T) {
	s := NewStore(t.TempDir())
	got := s.List()
	if got == nil {
		t.Fatal("List should return empty slice (not nil) when file absent")
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %d", len(got))
	}
}

func TestGet_UnknownID_ReturnsErrNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	_, err := s.Get("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── Save round-trip ───────────────────────────────────────────────────────────

func TestSave_NewDashboard_PersistsAndStampsTimes(t *testing.T) {
	s := NewStore(t.TempDir())
	saved, err := s.Save(newDef("d1", "First"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Errorf("CreatedAt/UpdatedAt should be stamped: %+v", saved)
	}
	if !saved.UpdatedAt.Equal(saved.CreatedAt) {
		t.Errorf("on insert UpdatedAt should equal CreatedAt; got created=%v updated=%v",
			saved.CreatedAt, saved.UpdatedAt)
	}

	got, err := s.Get("d1")
	if err != nil {
		t.Fatalf("Get after Save: %v", err)
	}
	if got.Name != "First" {
		t.Errorf("name: want %q, got %q", "First", got.Name)
	}
	if len(got.Widgets) != 1 || got.Widgets[0].ID != "w1" {
		t.Errorf("widgets did not round-trip: %+v", got.Widgets)
	}
}

func TestSave_SameID_Replaces_PreservesCreatedAt(t *testing.T) {
	s := NewStore(t.TempDir())

	first, _ := s.Save(newDef("d1", "Original"))
	createdAt := first.CreatedAt

	// Force a tick so UpdatedAt is observably newer.
	time.Sleep(2 * time.Millisecond)

	updatedDef := newDef("d1", "Renamed")
	second, err := s.Save(updatedDef)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if !second.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt must be preserved on update: was %v, now %v", createdAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(createdAt) {
		t.Errorf("UpdatedAt must advance on update: created=%v updated=%v", createdAt, second.UpdatedAt)
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("same-ID Save must replace, not append; got %d entries", len(list))
	}
	if list[0].Name != "Renamed" {
		t.Errorf("name after replace: want %q, got %q", "Renamed", list[0].Name)
	}
}

func TestSave_RejectsMissingFields(t *testing.T) {
	s := NewStore(t.TempDir())

	if _, err := s.Save(DashboardDefinition{Name: "no id"}); err == nil {
		t.Error("Save with empty ID should error")
	}
	if _, err := s.Save(DashboardDefinition{ID: "no-name"}); err == nil {
		t.Error("Save with empty name should error")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestDelete_ExistingID_RemovesAndReturnsNil(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _ = s.Save(newDef("d1", "First"))
	_, _ = s.Save(newDef("d2", "Second"))

	if err := s.Delete("d1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("d1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Get should return ErrNotFound, got %v", err)
	}
	remaining := s.List()
	if len(remaining) != 1 || remaining[0].ID != "d2" {
		t.Errorf("after Delete, want only d2 remaining; got %+v", remaining)
	}
}

func TestDelete_UnknownID_ReturnsErrNotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Delete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── Concurrency / atomicity ───────────────────────────────────────────────────

func TestSave_ConcurrentWrites_FileStaysValidJSON(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	var wg sync.WaitGroup
	const n = 40
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = s.Save(newDef(fmt.Sprintf("d%d", idx), fmt.Sprintf("Dashboard %d", idx)))
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, dashboardsFile))
	if err != nil {
		t.Fatalf("file unreadable after concurrent writes: %v", err)
	}
	var list []DashboardDefinition
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("file corrupted after concurrent writes: %v\n%s", err, data)
	}
	// Some writers may overwrite each other (last-write-wins for the whole
	// array), but the file must still be valid JSON and not empty.
	if len(list) == 0 {
		t.Error("expected at least one dashboard persisted")
	}
}

func TestWriteJSON_AtomicRename_NoPartialFile(t *testing.T) {
	dir := t.TempDir()
	// Large payload to make any non-atomic write more visible.
	type entry struct {
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	entries := make([]entry, 500)
	for i := range entries {
		entries[i] = entry{ID: "id", Data: strings.Repeat("x", 200)}
	}
	if err := writeDashboardsJSON(dir, entries); err != nil {
		t.Fatalf("writeDashboardsJSON: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, dashboardsFile))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	var out []entry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(out) != 500 {
		t.Errorf("want 500 entries, got %d", len(out))
	}
}

// ── SummaryFor ────────────────────────────────────────────────────────────────

func TestSummaryFor_ProjectsWidgetCount(t *testing.T) {
	d := DashboardDefinition{
		ID:      "d1",
		Name:    "First",
		Widgets: []Widget{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}
	s := SummaryFor(d)
	if s.WidgetCount != 3 {
		t.Errorf("WidgetCount: want 3, got %d", s.WidgetCount)
	}
	if s.ID != "d1" || s.Name != "First" {
		t.Errorf("summary fields not copied: %+v", s)
	}
}

// ── Templates ─────────────────────────────────────────────────────────────────

func TestTemplates_AllWellFormed(t *testing.T) {
	tmpls := Templates()
	if len(tmpls) == 0 {
		t.Fatal("expected at least one template")
	}
	seenIDs := map[string]bool{}
	for _, tmpl := range tmpls {
		if tmpl.ID == "" {
			t.Errorf("template missing ID: %+v", tmpl)
		}
		if seenIDs[tmpl.ID] {
			t.Errorf("duplicate template ID: %s", tmpl.ID)
		}
		seenIDs[tmpl.ID] = true
		if tmpl.Name == "" {
			t.Errorf("template %s missing name", tmpl.ID)
		}
		if len(tmpl.Definition.Widgets) == 0 {
			t.Errorf("template %s has no widgets", tmpl.ID)
		}
		widgetIDs := map[string]bool{}
		for _, w := range tmpl.Definition.Widgets {
			if w.ID == "" {
				t.Errorf("template %s has widget with empty ID", tmpl.ID)
			}
			if widgetIDs[w.ID] {
				t.Errorf("template %s has duplicate widget ID %q", tmpl.ID, w.ID)
			}
			widgetIDs[w.ID] = true
			if w.Kind == "" {
				t.Errorf("template %s widget %s missing kind", tmpl.ID, w.ID)
			}
			// Every widget must reference a known runtime endpoint OR be a
			// pure markdown widget with embedded content.
			if w.Source == nil && w.Kind != WidgetKindMarkdown && w.Kind != WidgetKindCustomHTML {
				t.Errorf("template %s widget %s has no source", tmpl.ID, w.ID)
			}
			if w.Source != nil && w.Source.Kind != SourceKindRuntime {
				t.Errorf("template %s widget %s should use runtime source (got %q) — templates must work without extra setup",
					tmpl.ID, w.ID, w.Source.Kind)
			}
		}
	}
}

func TestTemplateByID_KnownAndUnknown(t *testing.T) {
	if _, ok := TemplateByID("system_health"); !ok {
		t.Error("system_health template should exist")
	}
	if _, ok := TemplateByID("does-not-exist"); ok {
		t.Error("unknown template should return false")
	}
}

func TestTemplate_RoundTripsThroughStore(t *testing.T) {
	// End-to-end stage-1 sanity check: every template can be cloned, saved,
	// and reloaded without losing structure.
	s := NewStore(t.TempDir())
	for _, tmpl := range Templates() {
		def := tmpl.Definition
		def.ID = "tmpl-" + tmpl.ID
		saved, err := s.Save(def)
		if err != nil {
			t.Fatalf("Save template %s: %v", tmpl.ID, err)
		}
		got, err := s.Get(saved.ID)
		if err != nil {
			t.Fatalf("Get template %s: %v", tmpl.ID, err)
		}
		if len(got.Widgets) != len(tmpl.Definition.Widgets) {
			t.Errorf("template %s: widget count drift after round-trip: want %d, got %d",
				tmpl.ID, len(tmpl.Definition.Widgets), len(got.Widgets))
		}
	}
}
