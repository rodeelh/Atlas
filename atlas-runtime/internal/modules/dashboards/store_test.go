package dashboards

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir), dir
}

func TestStoreListEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	out := s.List()
	if out == nil {
		t.Fatal("List() returned nil; want empty slice")
	}
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(out))
	}
}

func TestStoreSaveAndGet(t *testing.T) {
	s, _ := newTestStore(t)
	d := Dashboard{
		ID:   "d1",
		Name: "Test",
	}
	saved, err := s.Save(d)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.SchemaVersion != SchemaVersion {
		t.Fatalf("saved schema version = %d, want %d", saved.SchemaVersion, SchemaVersion)
	}
	if saved.Status != StatusDraft {
		t.Fatalf("default status should be draft, got %q", saved.Status)
	}
	got, err := s.Get("d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Test" {
		t.Fatalf("get name = %q, want Test", got.Name)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Get("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreSaveRejectsBadStatus(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Save(Dashboard{ID: "x", Name: "N", Status: "bogus"})
	if err == nil {
		t.Fatal("expected error for bad status")
	}
}

func TestStoreSaveRequiresIDAndName(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Save(Dashboard{Name: "x"}); err == nil {
		t.Fatal("expected id error")
	}
	if _, err := s.Save(Dashboard{ID: "x"}); err == nil {
		t.Fatal("expected name error")
	}
}

func TestStoreDelete(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Save(Dashboard{ID: "d1", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete("d1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete should be ErrNotFound, got %v", err)
	}
}

func TestStoreListByStatus(t *testing.T) {
	s, _ := newTestStore(t)
	_, _ = s.Save(Dashboard{ID: "d1", Name: "Draft", Status: StatusDraft})
	_, _ = s.Save(Dashboard{ID: "d2", Name: "Live", Status: StatusLive})

	drafts := s.ListByStatus(StatusDraft)
	if len(drafts) != 1 || drafts[0].ID != "d1" {
		t.Fatalf("drafts = %+v", drafts)
	}
	lives := s.ListByStatus(StatusLive)
	if len(lives) != 1 || lives[0].ID != "d2" {
		t.Fatalf("lives = %+v", lives)
	}
	all := s.ListByStatus("")
	if len(all) != 2 {
		t.Fatalf("all = %d, want 2", len(all))
	}
}

func TestStoreArchiveV1(t *testing.T) {
	s, dir := newTestStore(t)
	v1Path := filepath.Join(dir, dashboardsFileV1)
	if err := os.WriteFile(v1Path, []byte(`[{"id":"old"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveV1IfPresent(); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := os.Stat(v1Path); !os.IsNotExist(err) {
		t.Fatalf("v1 file should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(v1Path + v1ArchiveSuffix); err != nil {
		t.Fatalf("archive file should exist: %v", err)
	}
	// Second call should be a no-op (sync.Once).
	if err := s.ArchiveV1IfPresent(); err != nil {
		t.Fatalf("second archive: %v", err)
	}
}

func TestStoreRejectsWrongSchemaVersion(t *testing.T) {
	s, dir := newTestStore(t)
	path := filepath.Join(dir, dashboardsFileV2)
	if err := os.WriteFile(path, []byte(`[{"schemaVersion":1,"id":"x","name":"n","status":"draft"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("x"); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("expected ErrSchemaMismatch, got %v", err)
	}
}

func TestStorePreservesCreatedAtOnUpdate(t *testing.T) {
	s, _ := newTestStore(t)
	created, _ := s.Save(Dashboard{ID: "d1", Name: "A"})
	updated, _ := s.Save(Dashboard{ID: "d1", Name: "B"})
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("CreatedAt changed on update: %v vs %v", updated.CreatedAt, created.CreatedAt)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("UpdatedAt should not regress")
	}
}
