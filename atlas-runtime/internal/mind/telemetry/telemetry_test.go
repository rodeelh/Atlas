package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"atlas-runtime-go/internal/storage"
)

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEmitAndAggregate(t *testing.T) {
	db := newTestDB(t)
	em := New(db)

	em.Emit(KindNapStarted, "", "conv-1", map[string]any{"trigger": "manual"})
	em.Emit(KindThoughtAdded, "T-01", "conv-1", map[string]any{"body": "hi", "score": 50})
	em.Emit(KindThoughtAdded, "T-02", "conv-1", map[string]any{"body": "there", "score": 60})
	em.Emit(KindNapCompleted, "", "conv-1", map[string]any{"ops": 2})

	// Stop with a generous timeout — drain should flush within ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := em.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	stats, err := Aggregate(db, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if stats.Total != 4 {
		t.Errorf("total: got %d, want 4", stats.Total)
	}
	if stats.ByKind["thought_added"] != 2 {
		t.Errorf("thought_added count: got %d, want 2", stats.ByKind["thought_added"])
	}
	if stats.ByKind["nap_started"] != 1 {
		t.Errorf("nap_started count: got %d, want 1", stats.ByKind["nap_started"])
	}
}

func TestEmitter_NilSafe(t *testing.T) {
	// A nil emitter is valid and Emit is a no-op. Useful for tests that
	// don't want to spin up a real DB.
	var em *Emitter
	em.Emit(KindNapStarted, "", "", nil)
	_ = em.Stop(context.Background())
}

func TestEmit_QueryByThought(t *testing.T) {
	db := newTestDB(t)
	em := New(db)
	em.Emit(KindThoughtAdded, "T-01", "c1", map[string]string{"body": "a"})
	em.Emit(KindThoughtUpdated, "T-01", "c1", map[string]string{"body": "a refined"})
	em.Emit(KindThoughtDiscarded, "T-01", "c1", map[string]string{})
	em.Emit(KindThoughtAdded, "T-02", "c1", map[string]string{"body": "b"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	em.Stop(ctx)

	rows, err := db.QueryMindTelemetry(storage.MindTelemetryFilter{
		ThoughtID: "T-01",
		Since:     time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Should see the 3 events for T-01, in ts DESC order.
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
	// Most recent first.
	if len(rows) > 0 && rows[0].Kind != "thought_discarded" {
		t.Errorf("first row: got %q, want thought_discarded", rows[0].Kind)
	}
}
