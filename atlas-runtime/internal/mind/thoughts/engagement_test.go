package thoughts

import (
	"sync"
	"testing"
	"time"
)

func TestRecordAndRecent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	events := []Event{
		{ThoughtID: "T-01", ConvID: "c1", Timestamp: now.Add(-time.Hour), Signal: SignalPositive, UserMessage: "thanks"},
		{ThoughtID: "T-01", ConvID: "c1", Timestamp: now.Add(-30 * time.Minute), Signal: SignalIgnored},
		{ThoughtID: "T-02", ConvID: "c2", Timestamp: now.Add(-15 * time.Minute), Signal: SignalNegative, UserMessage: "drop that"},
	}
	for _, ev := range events {
		if err := RecordEvent(dir, ev); err != nil {
			t.Fatalf("RecordEvent: %v", err)
		}
	}

	got, skipped, err := RecentEvents(dir, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped non-zero: %d", skipped)
	}
	if len(got) != len(events) {
		t.Errorf("got %d events, want %d", len(got), len(events))
	}
}

func TestRecentEvents_SinceFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	RecordEvent(dir, Event{ThoughtID: "T-01", Timestamp: now.Add(-48 * time.Hour), Signal: SignalPositive})
	RecordEvent(dir, Event{ThoughtID: "T-01", Timestamp: now.Add(-6 * time.Hour), Signal: SignalIgnored})
	RecordEvent(dir, Event{ThoughtID: "T-01", Timestamp: now.Add(-30 * time.Minute), Signal: SignalNegative})

	got, _, err := RecentEvents(dir, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("since filter: got %d events, want 2", len(got))
	}
}

func TestRecentEvents_MissingFile(t *testing.T) {
	dir := t.TempDir()
	got, skipped, err := RecentEvents(dir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Errorf("missing file should return nil error, got %v", err)
	}
	if len(got) != 0 || skipped != 0 {
		t.Errorf("missing file: got %d events %d skipped", len(got), skipped)
	}
}

func TestRecordEvent_EmptyThoughtID(t *testing.T) {
	dir := t.TempDir()
	err := RecordEvent(dir, Event{ThoughtID: "", Signal: SignalPositive})
	if err == nil {
		t.Error("expected error for empty thought_id")
	}
}

func TestRecordEvent_UnknownSignalFallsBackToIgnored(t *testing.T) {
	dir := t.TempDir()
	err := RecordEvent(dir, Event{ThoughtID: "T-01", Signal: Signal("whatever")})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	got, _, _ := RecentEvents(dir, time.Now().Add(-time.Hour))
	if len(got) != 1 || got[0].Signal != SignalIgnored {
		t.Errorf("unknown signal should fall back to ignored, got %+v", got)
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	const goroutines = 20
	const perGoroutine = 25
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				_ = RecordEvent(dir, Event{
					ThoughtID: "T-01",
					ConvID:    "c1",
					Timestamp: time.Now().UTC(),
					Signal:    SignalPositive,
				})
			}
		}(i)
	}
	wg.Wait()

	got, skipped, err := RecentEvents(dir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if skipped != 0 {
		t.Errorf("%d lines corrupted during concurrent writes", skipped)
	}
	if len(got) != goroutines*perGoroutine {
		t.Errorf("got %d events, want %d", len(got), goroutines*perGoroutine)
	}
}

func TestCountByThought(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		{ThoughtID: "T-01", Signal: SignalPositive, Timestamp: now},
		{ThoughtID: "T-01", Signal: SignalIgnored, Timestamp: now},
		{ThoughtID: "T-01", Signal: SignalIgnored, Timestamp: now},
		{ThoughtID: "T-02", Signal: SignalNegative, Timestamp: now},
		{ThoughtID: "T-02", Signal: SignalNegative, Timestamp: now},
	}
	counts := CountByThought(events)
	if counts["T-01"][SignalPositive] != 1 {
		t.Errorf("T-01 positive: got %d, want 1", counts["T-01"][SignalPositive])
	}
	if counts["T-01"][SignalIgnored] != 2 {
		t.Errorf("T-01 ignored: got %d, want 2", counts["T-01"][SignalIgnored])
	}
	if counts["T-02"][SignalNegative] != 2 {
		t.Errorf("T-02 negative: got %d, want 2", counts["T-02"][SignalNegative])
	}
}

func TestRecordSurfacing_Idempotent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	if err := RecordSurfacing(dir, "conv-1", "msg-1", "T-01", now); err != nil {
		t.Fatalf("first RecordSurfacing: %v", err)
	}
	// Second call with the same triple must not create a duplicate.
	if err := RecordSurfacing(dir, "conv-1", "msg-1", "T-01", now); err != nil {
		t.Fatalf("second RecordSurfacing: %v", err)
	}
	got, _, err := RecentEvents(dir, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("idempotent: got %d events, want 1", len(got))
	}
	if got[0].Signal != SignalPending {
		t.Errorf("new surfacing signal: got %q, want pending", got[0].Signal)
	}
	if got[0].SurfacingID == "" {
		t.Errorf("surfacing id not populated")
	}
}

func TestRecordSurfacing_DistinctByMessage(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	// Same thought, two different messages → two distinct rows.
	if err := RecordSurfacing(dir, "conv-1", "msg-1", "T-01", now); err != nil {
		t.Fatalf("1: %v", err)
	}
	if err := RecordSurfacing(dir, "conv-1", "msg-2", "T-01", now); err != nil {
		t.Fatalf("2: %v", err)
	}
	got, _, _ := RecentEvents(dir, now.Add(-time.Hour))
	if len(got) != 2 {
		t.Errorf("distinct-by-message: got %d events, want 2", len(got))
	}
}

func TestMarkSurfacingClassified_RewritesInPlace(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	surfacingID := BuildSurfacingID("conv-1", "msg-1", "T-01")

	if err := RecordSurfacing(dir, "conv-1", "msg-1", "T-01", now); err != nil {
		t.Fatalf("surface: %v", err)
	}
	// Add an unrelated terminal event to confirm the rewrite preserves it.
	if err := RecordEvent(dir, Event{
		ThoughtID: "T-02",
		ConvID:    "conv-2",
		Timestamp: now,
		Signal:    SignalPositive,
	}); err != nil {
		t.Fatalf("unrelated: %v", err)
	}

	classifiedAt := now.Add(time.Minute)
	err := MarkSurfacingClassified(
		dir,
		surfacingID,
		SignalPositive,
		88,
		"user said 'yes please tell me more'",
		"yes please tell me more",
		classifiedAt,
	)
	if err != nil {
		t.Fatalf("mark classified: %v", err)
	}

	got, _, err := RecentEvents(dir, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after rewrite: got %d events, want 2", len(got))
	}
	var classified *Event
	for i := range got {
		if got[i].SurfacingID == surfacingID {
			classified = &got[i]
		}
	}
	if classified == nil {
		t.Fatal("classified row missing after rewrite")
	}
	if classified.Signal != SignalPositive {
		t.Errorf("signal: got %q, want positive", classified.Signal)
	}
	if classified.ClassifierConfidence != 88 {
		t.Errorf("confidence: got %d, want 88", classified.ClassifierConfidence)
	}
	if classified.UserMessage != "yes please tell me more" {
		t.Errorf("user message: got %q", classified.UserMessage)
	}
	if classified.ClassifiedAt.IsZero() {
		t.Errorf("classified_at should be set")
	}
}

func TestMarkSurfacingClassified_MissingID(t *testing.T) {
	dir := t.TempDir()
	err := MarkSurfacingClassified(
		dir, "ghost-id", SignalPositive, 50, "r", "m", time.Now().UTC(),
	)
	if err == nil {
		t.Error("expected error for missing surfacing id")
	}
}

func TestMarkSurfacingClassified_RejectsPending(t *testing.T) {
	dir := t.TempDir()
	err := MarkSurfacingClassified(
		dir, "any", SignalPending, 50, "r", "m", time.Now().UTC(),
	)
	if err == nil {
		t.Error("expected error when classifying with pending signal")
	}
}

func TestMarkSurfacingIgnoredIfExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	fresh := now.Add(-10 * time.Minute)

	if err := RecordSurfacing(dir, "conv-1", "msg-old", "T-01", old); err != nil {
		t.Fatalf("old: %v", err)
	}
	if err := RecordSurfacing(dir, "conv-1", "msg-fresh", "T-02", fresh); err != nil {
		t.Fatalf("fresh: %v", err)
	}

	updated, err := MarkSurfacingIgnoredIfExpired(dir, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("mark expired: %v", err)
	}
	if updated != 1 {
		t.Errorf("updated: got %d, want 1", updated)
	}

	got, _, _ := RecentEvents(dir, now.Add(-72*time.Hour))
	for _, ev := range got {
		if ev.ThoughtID == "T-01" && ev.Signal != SignalIgnored {
			t.Errorf("old surfacing not decayed: %q", ev.Signal)
		}
		if ev.ThoughtID == "T-02" && ev.Signal != SignalPending {
			t.Errorf("fresh surfacing should still be pending: %q", ev.Signal)
		}
	}
}

func TestFindPendingSurfacingInConv(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	// Older pending in the same conv.
	if err := RecordSurfacing(dir, "conv-1", "msg-1", "T-01", now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Newer pending in the same conv — this is the one we want.
	if err := RecordSurfacing(dir, "conv-1", "msg-2", "T-02", now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Pending in a different conv — should be ignored.
	if err := RecordSurfacing(dir, "conv-2", "msg-3", "T-03", now.Add(-1*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Terminal event in the same conv — should not be returned as pending.
	if err := RecordEvent(dir, Event{
		SurfacingID: "terminal",
		ThoughtID:   "T-04",
		ConvID:      "conv-1",
		Timestamp:   now,
		SurfacedAt:  now,
		Signal:      SignalPositive,
	}); err != nil {
		t.Fatal(err)
	}

	found, err := FindPendingSurfacingInConv(dir, "conv-1", time.Hour)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find a pending surfacing")
	}
	if found.ThoughtID != "T-02" {
		t.Errorf("most-recent winner: got %q, want T-02", found.ThoughtID)
	}
}

func TestFindPendingSurfacingInConv_None(t *testing.T) {
	dir := t.TempDir()
	found, err := FindPendingSurfacingInConv(dir, "conv-1", time.Hour)
	if err != nil {
		t.Fatalf("find on empty: %v", err)
	}
	if found != nil {
		t.Errorf("expected nil, got %+v", found)
	}
}

func TestCountByThought_ExcludesPending(t *testing.T) {
	events := []Event{
		{ThoughtID: "T-01", Signal: SignalPending},
		{ThoughtID: "T-01", Signal: SignalPositive},
		{ThoughtID: "T-01", Signal: SignalNegative},
	}
	counts := CountByThought(events)
	if counts["T-01"][SignalPending] != 0 {
		t.Errorf("pending counted: %d", counts["T-01"][SignalPending])
	}
	if counts["T-01"][SignalPositive] != 1 {
		t.Errorf("positive: %d", counts["T-01"][SignalPositive])
	}
	if counts["T-01"][SignalNegative] != 1 {
		t.Errorf("negative: %d", counts["T-01"][SignalNegative])
	}
}

func TestShouldDiscard(t *testing.T) {
	cases := []struct {
		name   string
		counts map[Signal]int
		want   bool
	}{
		{"empty", map[Signal]int{}, false},
		{"one negative", map[Signal]int{SignalNegative: 1}, false},
		{"two negatives", map[Signal]int{SignalNegative: 2}, true},
		{"three negatives", map[Signal]int{SignalNegative: 3}, true},
		{"two ignores", map[Signal]int{SignalIgnored: 2}, false},
		{"three ignores", map[Signal]int{SignalIgnored: 3}, true},
		{"four ignores", map[Signal]int{SignalIgnored: 4}, true},
		{"one of each", map[Signal]int{SignalNegative: 1, SignalIgnored: 1}, false},
		{"positive doesn't help", map[Signal]int{SignalNegative: 2, SignalPositive: 5}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldDiscard(tc.counts); got != tc.want {
				t.Errorf("got %t, want %t", got, tc.want)
			}
		})
	}
}
