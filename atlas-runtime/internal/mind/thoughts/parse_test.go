package thoughts

import (
	"strings"
	"testing"
	"time"
)

func TestParseSection_Empty(t *testing.T) {
	list, errs := ParseSection("")
	if len(list) != 0 || len(errs) != 0 {
		t.Errorf("empty section: got list=%d errs=%d", len(list), len(errs))
	}

	list, errs = ParseSection("   \n  \n  ")
	if len(list) != 0 || len(errs) != 0 {
		t.Errorf("whitespace section: got list=%d errs=%d", len(list), len(errs))
	}
}

func TestParseSection_Placeholder(t *testing.T) {
	// RenderSection emits this for an empty list — ParseSection should
	// tolerate it.
	list, errs := ParseSection("_(no active thoughts)_")
	if len(list) != 0 {
		t.Errorf("placeholder: got %d thoughts, want 0", len(list))
	}
	if len(errs) != 0 {
		t.Errorf("placeholder: got errs %v", errs)
	}
}

func TestRoundTrip_SingleThought(t *testing.T) {
	created := mustTime(t, "2026-04-07T14:30:00Z")
	reinforced := mustTime(t, "2026-04-07T15:00:00Z")

	original := []Thought{
		{
			ID:          "T-01",
			Body:        "Rami keeps coming back to the OpenClaw release rhythm. Feels like he's circling around something.",
			Confidence:  80,
			Value:       70,
			Class:       ClassRead,
			Score:       56, // 80 * 70 * 1.00 / 100 = 56
			Created:     created,
			Reinforced:  reinforced,
			SurfacedN:   0,
			SurfacedMax: 2,
			Source:      "conv-7f3a:nap-3",
			Provenance:  "Rami mentioned openclaw twice in the last week",
		},
	}
	rendered := RenderSection(original)
	parsed, errs := ParseSection(rendered)
	if len(errs) != 0 {
		t.Fatalf("parse errors on round-trip: %v\nrendered:\n%s", errs, rendered)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 thought, got %d\nrendered:\n%s", len(parsed), rendered)
	}
	got := parsed[0]
	want := original[0]
	if got.ID != want.ID || got.Body != want.Body || got.Confidence != want.Confidence ||
		got.Value != want.Value || got.Class != want.Class || got.Score != want.Score ||
		got.SurfacedN != want.SurfacedN || got.SurfacedMax != want.SurfacedMax ||
		got.Source != want.Source || got.Provenance != want.Provenance {
		t.Errorf("round-trip mismatch:\nwant %+v\ngot  %+v\nrendered:\n%s", want, got, rendered)
	}
	if !got.Created.Equal(want.Created) {
		t.Errorf("created mismatch: want %v, got %v", want.Created, got.Created)
	}
	if !got.Reinforced.Equal(want.Reinforced) {
		t.Errorf("reinforced mismatch: want %v, got %v", want.Reinforced, got.Reinforced)
	}
}

func TestRoundTrip_MultipleThoughts(t *testing.T) {
	now := mustTime(t, "2026-04-07T12:00:00Z")
	original := []Thought{
		{
			ID: "T-01", Body: "First thought body.",
			Confidence: 80, Value: 70, Class: ClassRead, Score: 56,
			Created: now, Reinforced: now,
			SurfacedN: 0, SurfacedMax: 2,
			Source: "conv-a",
		},
		{
			ID: "T-02", Body: "Second thought — with some punctuation.",
			Confidence: 60, Value: 50, Class: ClassLocalWrite, Score: 28,
			Created: now, Reinforced: now,
			SurfacedN: 1, SurfacedMax: 2,
			Source: "conv-b", Provenance: "noticed a pattern",
		},
		{
			ID: "T-03", Body: "Third, with an action.",
			Confidence: 95, Value: 90, Class: ClassRead, Score: 86,
			Created: now, Reinforced: now,
			SurfacedN: 0, SurfacedMax: 2,
			Source: "conv-c",
			Action: &ProposedAction{
				SkillID: "openclaw.check",
				Args:    map[string]any{"repo": "openclaw/openclaw"},
			},
		},
	}
	rendered := RenderSection(original)
	parsed, errs := ParseSection(rendered)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v\n%s", errs, rendered)
	}
	if len(parsed) != len(original) {
		t.Fatalf("round-trip count mismatch: got %d, want %d\n%s", len(parsed), len(original), rendered)
	}
	for i, want := range original {
		got := parsed[i]
		if got.ID != want.ID {
			t.Errorf("thought[%d] id: got %q, want %q", i, got.ID, want.ID)
		}
		if got.Body != want.Body {
			t.Errorf("thought[%d] body: got %q, want %q", i, got.Body, want.Body)
		}
		if got.Class != want.Class {
			t.Errorf("thought[%d] class: got %q, want %q", i, got.Class, want.Class)
		}
		if got.Score != want.Score {
			t.Errorf("thought[%d] score: got %d, want %d", i, got.Score, want.Score)
		}
		if (got.Action == nil) != (want.Action == nil) {
			t.Errorf("thought[%d] action presence mismatch", i)
		}
		if got.Action != nil && want.Action != nil {
			if got.Action.SkillID != want.Action.SkillID {
				t.Errorf("thought[%d] action skill: got %q, want %q",
					i, got.Action.SkillID, want.Action.SkillID)
			}
		}
	}
}

func TestParseSection_MalformedBullet(t *testing.T) {
	// A bullet without a T-NN id is malformed and should be skipped with
	// an error, not crash the whole parse.
	body := `- **[T-01]** Good thought.
  · score 50 · class read · confidence 100 · value 50 · created 2026-04-07T12:00:00Z · reinforced 2026-04-07T12:00:00Z · surfaced 0/2 · source conv-a

- **Missing id marker** bad bullet.
  · score 50 · class read · confidence 100 · value 50 · created 2026-04-07T12:00:00Z · reinforced 2026-04-07T12:00:00Z · surfaced 0/2 · source conv-a
`
	list, _ := ParseSection(body)
	if len(list) != 1 {
		t.Errorf("malformed bullet: got %d thoughts, want 1", len(list))
	}
}

func TestParseSection_MissingRequiredFields(t *testing.T) {
	// A bullet missing class and dates should be rejected as invalid.
	body := `- **[T-01]** Body only, no metadata.
`
	list, errs := ParseSection(body)
	if len(list) != 0 {
		t.Errorf("got %d thoughts, want 0", len(list))
	}
	if len(errs) == 0 {
		t.Error("expected at least one parse error")
	}
}

func TestRenderSection_EmptyList(t *testing.T) {
	got := RenderSection(nil)
	if !strings.Contains(got, "no active thoughts") {
		t.Errorf("empty render should contain placeholder, got %q", got)
	}

	got = RenderSection([]Thought{})
	if !strings.Contains(got, "no active thoughts") {
		t.Errorf("empty render should contain placeholder, got %q", got)
	}
}

func TestParseSection_MultilineBody(t *testing.T) {
	body := `- **[T-01]** This thought body
  wraps across multiple lines because the
  metadata starts with a bullet.
  · score 56 · class read · confidence 80 · value 70 · created 2026-04-07T12:00:00Z · reinforced 2026-04-07T12:00:00Z · surfaced 0/2 · source conv-a
`
	list, errs := ParseSection(body)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if len(list) != 1 {
		t.Fatalf("got %d thoughts, want 1", len(list))
	}
	// Body should be joined with spaces, not contain newlines.
	if strings.Contains(list[0].Body, "\n") {
		t.Errorf("body should not contain newlines, got %q", list[0].Body)
	}
	if !strings.Contains(list[0].Body, "wraps") {
		t.Errorf("body missing wrap content: %q", list[0].Body)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustTime: %v", err)
	}
	return ts
}
