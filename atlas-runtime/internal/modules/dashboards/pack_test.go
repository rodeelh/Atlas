package dashboards

import (
	"fmt"
	"testing"
)

// overlaps reports whether two rectangles intersect.
func overlaps(a, b Widget) bool {
	return a.GridX < b.GridX+b.GridW &&
		b.GridX < a.GridX+a.GridW &&
		a.GridY < b.GridY+b.GridH &&
		b.GridY < a.GridY+a.GridH
}

// assertNoOverlap fails the test if any pair of widgets overlap.
func assertNoOverlap(t *testing.T, widgets []Widget) {
	t.Helper()
	for i := 0; i < len(widgets); i++ {
		for j := i + 1; j < len(widgets); j++ {
			if overlaps(widgets[i], widgets[j]) {
				t.Fatalf("widgets %d and %d overlap: %+v vs %+v", i, j, widgets[i], widgets[j])
			}
		}
	}
}

// assertWithinColumns fails if any widget's right edge exceeds columns.
func assertWithinColumns(t *testing.T, widgets []Widget, columns int) {
	t.Helper()
	for i, w := range widgets {
		if w.GridX < 0 || w.GridY < 0 || w.GridW <= 0 || w.GridH <= 0 {
			t.Fatalf("widget %d has non-positive geometry: %+v", i, w)
		}
		if w.GridX+w.GridW > columns {
			t.Fatalf("widget %d exceeds columns (%d+%d > %d)", i, w.GridX, w.GridW, columns)
		}
	}
}

func TestPackSingleFullWidget(t *testing.T) {
	in := []Widget{{ID: "a", Size: SizeFull}}
	out := packGrid(in, 12)
	if out[0].GridX != 0 || out[0].GridY != 0 || out[0].GridW != 12 || out[0].GridH != 1 {
		t.Fatalf("unexpected geometry: %+v", out[0])
	}
}

func TestPackFourQuartersFirstRow(t *testing.T) {
	in := []Widget{
		{ID: "a", Size: SizeQuarter},
		{ID: "b", Size: SizeQuarter},
		{ID: "c", Size: SizeQuarter},
		{ID: "d", Size: SizeQuarter},
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
	wants := []int{0, 3, 6, 9}
	for i, w := range out {
		if w.GridY != 0 || w.GridX != wants[i] || w.GridW != 3 {
			t.Fatalf("widget %s: got %+v, want row 0 col %d width 3", w.ID, w, wants[i])
		}
	}
}

func TestPackMixedFlowsToSecondRow(t *testing.T) {
	in := []Widget{
		{ID: "a", Size: SizeHalf},    // row0 cols 0-5
		{ID: "b", Size: SizeHalf},    // row0 cols 6-11
		{ID: "c", Size: SizeQuarter}, // row1 cols 0-2
		{ID: "d", Size: SizeThird},   // row1 cols 3-6
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
	if out[0].GridY != 0 || out[1].GridY != 0 {
		t.Fatalf("first two halves should be on row 0: %+v", out)
	}
	if out[2].GridY != 1 || out[3].GridY != 1 {
		t.Fatalf("next two should flow to row 1: %+v", out)
	}
}

func TestPackTallWidgetOccupiesTwoRows(t *testing.T) {
	in := []Widget{
		{ID: "tall", Size: SizeTall},    // 6 wide × 2 rows at row 0 col 0
		{ID: "q1", Size: SizeQuarter},   // row 0 col 6
		{ID: "q2", Size: SizeQuarter},   // row 0 col 9
		{ID: "q3", Size: SizeQuarter},   // row 1 col 6
		{ID: "q4", Size: SizeQuarter},   // row 1 col 9
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
	var tall Widget
	for _, w := range out {
		if w.ID == "tall" {
			tall = w
		}
	}
	if tall.GridW != 6 || tall.GridH != 2 {
		t.Fatalf("tall widget should be 6x2, got %dx%d", tall.GridW, tall.GridH)
	}
}

func TestPackGroupsLaidOutContiguously(t *testing.T) {
	in := []Widget{
		{ID: "ungrouped", Size: SizeQuarter},
		{ID: "g1-a", Size: SizeQuarter, Group: "alpha"},
		{ID: "g1-b", Size: SizeQuarter, Group: "alpha"},
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
	// Group "alpha" should be laid out before the ungrouped widget.
	var gAY, ungroupedY int = -1, -1
	for _, w := range out {
		if w.ID == "g1-a" {
			gAY = w.GridY
		}
		if w.ID == "ungrouped" {
			ungroupedY = w.GridY
		}
	}
	if gAY > ungroupedY {
		t.Fatalf("expected grouped widgets before ungrouped (alpha row=%d, ungrouped row=%d)", gAY, ungroupedY)
	}
}

func TestPackDeterministic(t *testing.T) {
	in := []Widget{
		{ID: "a", Size: SizeHalf},
		{ID: "b", Size: SizeThird},
		{ID: "c", Size: SizeQuarter},
		{ID: "d", Size: SizeTall},
		{ID: "e", Size: SizeFull},
	}
	a := packGrid(in, 12)
	b := packGrid(in, 12)
	for i := range a {
		if a[i].ID != b[i].ID ||
			a[i].GridX != b[i].GridX ||
			a[i].GridY != b[i].GridY ||
			a[i].GridW != b[i].GridW ||
			a[i].GridH != b[i].GridH {
			t.Fatalf("non-deterministic pack at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPackFullIsTopOfItsRow(t *testing.T) {
	in := []Widget{
		{ID: "a", Size: SizeHalf},
		{ID: "b", Size: SizeHalf},
		{ID: "wide", Size: SizeFull},
		{ID: "c", Size: SizeQuarter},
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
	var wideRow int
	for _, w := range out {
		if w.ID == "wide" {
			wideRow = w.GridY
		}
	}
	if wideRow != 1 {
		t.Fatalf("wide widget should be on row 1, got %d", wideRow)
	}
}

func TestPackCustomColumns(t *testing.T) {
	in := []Widget{
		{ID: "a", Size: SizeFull},
		{ID: "b", Size: SizeHalf},
	}
	out := packGrid(in, 6)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 6)
	if out[0].GridW != 6 {
		t.Fatalf("full on 6-col grid should be width 6, got %d", out[0].GridW)
	}
	// Half on a 6-col grid clamps to 6 too.
	if out[1].GridW != 6 {
		t.Fatalf("half on 6-col grid should clamp to 6, got %d", out[1].GridW)
	}
}

func TestPackManyWidgetsNoOverlap(t *testing.T) {
	sizes := []string{SizeQuarter, SizeThird, SizeHalf, SizeTall, SizeFull}
	in := make([]Widget, 0, 40)
	for i := 0; i < 40; i++ {
		in = append(in, Widget{
			ID:   fmt.Sprintf("w-%d", i),
			Size: sizes[i%len(sizes)],
		})
	}
	out := packGrid(in, 12)
	assertNoOverlap(t, out)
	assertWithinColumns(t, out, 12)
}
