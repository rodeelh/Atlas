package thoughts

import (
	"testing"
	"time"
)

func TestApply_Add(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	out, results, err := Apply(nil, []Op{
		{
			Kind: OpAdd, Body: "first thought",
			Confidence: 80, Value: 70, Class: ClassRead,
			Source: "conv-a", Provenance: "test",
		},
	}, now)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d thoughts, want 1", len(out))
	}
	if out[0].ID != "T-01" {
		t.Errorf("id: got %q, want T-01", out[0].ID)
	}
	if out[0].Score != 56 { // 80 * 70 * 1.00 / 100
		t.Errorf("score: got %d, want 56", out[0].Score)
	}
	if !out[0].Created.Equal(now) {
		t.Errorf("created: got %v, want %v", out[0].Created, now)
	}
	if len(results) != 1 || results[0].Outcome != "applied" {
		t.Errorf("results: %+v", results)
	}
}

func TestApply_AddAutoIDSequence(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	out, _, _ := Apply(nil, []Op{
		{Kind: OpAdd, Body: "a", Confidence: 10, Value: 10, Class: ClassRead, Source: "s"},
		{Kind: OpAdd, Body: "b", Confidence: 10, Value: 10, Class: ClassRead, Source: "s"},
		{Kind: OpAdd, Body: "c", Confidence: 10, Value: 10, Class: ClassRead, Source: "s"},
	}, now)
	if len(out) != 3 {
		t.Fatalf("got %d thoughts, want 3", len(out))
	}
	want := []string{"T-01", "T-02", "T-03"}
	for i, t_ := range out {
		if t_.ID != want[i] {
			t.Errorf("thought %d id: got %q, want %q", i, t_.ID, want[i])
		}
	}
}

func TestApply_AddScoreRecomputed(t *testing.T) {
	// Model-provided score should be ignored; code recomputes it.
	now := mustTime(t, "2026-04-07T12:00:00Z")
	out, _, _ := Apply(nil, []Op{
		{
			Kind: OpAdd, Body: "tricky",
			Confidence: 100, Value: 100, Class: ClassExternalSideEffect,
			Source: "s",
		},
	}, now)
	if out[0].Score != 93 { // structural ceiling
		t.Errorf("score: got %d, want 93 (structural ceiling)", out[0].Score)
	}
}

func TestApply_AddRejectsInvalid(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	_, results, _ := Apply(nil, []Op{
		{Kind: OpAdd, Body: "", Confidence: 50, Value: 50, Class: ClassRead, Source: "s"},
	}, now)
	if len(results) != 1 || results[0].Outcome != "rejected_invalid" {
		t.Errorf("expected rejected_invalid for empty body, got %+v", results)
	}

	_, results, _ = Apply(nil, []Op{
		{Kind: OpAdd, Body: "ok", Confidence: 50, Value: 50, Class: "bogus", Source: "s"},
	}, now)
	if len(results) != 1 || results[0].Outcome != "rejected_invalid" {
		t.Errorf("expected rejected_invalid for bogus class, got %+v", results)
	}
}

func TestApply_Update(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	later := now.Add(time.Hour)
	seed := []Thought{{
		ID:         "T-01",
		Body:       "original",
		Confidence: 50, Value: 50, Class: ClassRead, Score: 25,
		Created: now, Reinforced: now,
		SurfacedMax: 2,
		Source:      "s",
	}}
	out, _, err := Apply(seed, []Op{
		{Kind: OpUpdate, ID: "T-01", Body: "refined", Confidence: 80, Value: 80},
	}, later)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Body != "refined" {
		t.Errorf("body: got %q, want refined", out[0].Body)
	}
	if out[0].Score != 64 { // 80 * 80 * 1.00 / 100
		t.Errorf("score: got %d, want 64 (recomputed)", out[0].Score)
	}
	if !out[0].Reinforced.Equal(later) {
		t.Errorf("reinforced: got %v, want %v", out[0].Reinforced, later)
	}
}

func TestApply_UpdateMissing(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	_, results, err := Apply(nil, []Op{
		{Kind: OpUpdate, ID: "T-99", Body: "nobody home"},
	}, now)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != "rejected_missing" {
		t.Errorf("expected rejected_missing, got %+v", results)
	}
}

func TestApply_Reinforce(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	later := now.Add(2 * time.Hour)
	seed := []Thought{{
		ID: "T-01", Body: "keep me", Confidence: 50, Value: 50,
		Class: ClassRead, Score: 25, Created: now, Reinforced: now,
		SurfacedMax: 2, Source: "s",
	}}
	out, _, _ := Apply(seed, []Op{{Kind: OpReinforce, ID: "T-01"}}, later)
	if !out[0].Reinforced.Equal(later) {
		t.Errorf("reinforced: got %v, want %v", out[0].Reinforced, later)
	}
	// Body and score should be unchanged.
	if out[0].Body != "keep me" || out[0].Score != 25 {
		t.Errorf("reinforce mutated unrelated fields: %+v", out[0])
	}
}

func TestApply_Discard(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	seed := []Thought{
		{ID: "T-01", Body: "a", Confidence: 10, Value: 10, Class: ClassRead, Score: 1,
			Created: now, Reinforced: now, SurfacedMax: 2, Source: "s"},
		{ID: "T-02", Body: "b", Confidence: 10, Value: 10, Class: ClassRead, Score: 1,
			Created: now, Reinforced: now, SurfacedMax: 2, Source: "s"},
	}
	out, _, _ := Apply(seed, []Op{{Kind: OpDiscard, ID: "T-01"}}, now)
	if len(out) != 1 || out[0].ID != "T-02" {
		t.Errorf("after discard: %+v", out)
	}
}

func TestApply_DiscardMissing(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	_, results, _ := Apply(nil, []Op{{Kind: OpDiscard, ID: "T-99"}}, now)
	if len(results) != 1 || results[0].Outcome != "skipped_noop" {
		t.Errorf("discard of missing id should be skipped_noop, got %+v", results)
	}
}

func TestApply_Merge(t *testing.T) {
	early := mustTime(t, "2026-04-05T12:00:00Z")
	mid := mustTime(t, "2026-04-06T12:00:00Z")
	now := mustTime(t, "2026-04-07T12:00:00Z")
	seed := []Thought{
		{ID: "T-01", Body: "part one", Confidence: 60, Value: 50,
			Class: ClassRead, Score: 30, Created: early, Reinforced: mid,
			SurfacedMax: 2, Source: "conv-a", Provenance: "early"},
		{ID: "T-02", Body: "part two", Confidence: 40, Value: 70,
			Class: ClassLocalWrite, Score: 26, Created: mid, Reinforced: mid,
			SurfacedMax: 2, Source: "conv-b", Provenance: "later"},
	}
	out, _, err := Apply(seed, []Op{
		{Kind: OpMerge, IDs: []string{"T-01", "T-02"}, IntoBody: "combined observation"},
	}, now)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("merge result: got %d thoughts, want 1", len(out))
	}
	merged := out[0]
	if merged.Body != "combined observation" {
		t.Errorf("merged body: got %q, want combined observation", merged.Body)
	}
	// Merged thought inherits earliest Created.
	if !merged.Created.Equal(early) {
		t.Errorf("merged created: got %v, want %v", merged.Created, early)
	}
	// Merged thought inherits max confidence and value.
	if merged.Confidence != 60 || merged.Value != 70 {
		t.Errorf("merged confidence/value: got %d/%d, want 60/70",
			merged.Confidence, merged.Value)
	}
	// Merged thought inherits the MORE restrictive class.
	if merged.Class != ClassLocalWrite {
		t.Errorf("merged class: got %q, want local_write (more restrictive)", merged.Class)
	}
}

func TestApply_MergeInsufficientIDs(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	_, results, _ := Apply(nil, []Op{
		{Kind: OpMerge, IDs: []string{"T-01"}, IntoBody: "alone"},
	}, now)
	if len(results) != 1 || results[0].Outcome != "rejected_invalid" {
		t.Errorf("expected rejected_invalid for single-id merge, got %+v", results)
	}
}

func TestApply_PreservesInputSlice(t *testing.T) {
	// Apply must never mutate the caller's slice.
	now := mustTime(t, "2026-04-07T12:00:00Z")
	seed := []Thought{{
		ID: "T-01", Body: "original", Confidence: 50, Value: 50,
		Class: ClassRead, Score: 25, Created: now, Reinforced: now,
		SurfacedMax: 2, Source: "s",
	}}
	seedCopy := make([]Thought, len(seed))
	copy(seedCopy, seed)

	_, _, _ = Apply(seed, []Op{
		{Kind: OpUpdate, ID: "T-01", Body: "mutated"},
		{Kind: OpDiscard, ID: "T-01"},
	}, now)

	if seed[0].Body != seedCopy[0].Body {
		t.Errorf("Apply mutated input: body became %q", seed[0].Body)
	}
	if len(seed) != len(seedCopy) {
		t.Errorf("Apply mutated input: length changed to %d", len(seed))
	}
}

func TestApply_UnknownOp(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	_, results, _ := Apply(nil, []Op{{Kind: OpKind("invent"), Body: "???"}}, now)
	if len(results) != 1 || results[0].Outcome != "rejected_invalid" {
		t.Errorf("unknown op should be rejected_invalid, got %+v", results)
	}
}
