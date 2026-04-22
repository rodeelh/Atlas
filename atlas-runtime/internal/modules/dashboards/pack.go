package dashboards

// pack.go — deterministic 12-column layout packer for dashboard widgets.
//
// The packer maps Size tokens to (width, height) in grid units, then fills
// the grid row-major, greedily placing each widget in the first cell where
// its full rectangle fits without overlap. Widgets sharing the same
// non-empty Group are laid out contiguously in a single pass before the
// packer advances to ungrouped widgets, so agent-authored groupings remain
// visually coherent.
//
// The algorithm is pure (no randomness, no timing dependency) so the same
// input always produces the same output — critical for reproducible
// dashboards and for snapshot tests.

import "sort"

// sizeExtent maps a size token to (widthCols, heightRows).
// The CSS grid uses `grid-auto-rows: minmax(200px, auto)` so each row is at
// least 200px and auto-expands when content overflows. Heights here control
// the *minimum span* — choose values that match typical content for each size:
//
//	quarter (3 cols)   — single metric or KPI card:     1 row  ≈ 200px+
//	third   (4 cols)   — small metric with context:     1 row  ≈ 200px+
//	half    (6 cols)   — chart, list, grouped metrics:  2 rows ≈ 400px+
//	tall    (6 cols)   — large chart or long list:      4 rows ≈ 800px+
//	full    (12 cols)  — wide table, wide chart:        2 rows ≈ 400px+
//
// Unknown sizes default to half.
func sizeExtent(size string, columns int) (int, int) {
	switch size {
	case SizeQuarter:
		return clampCols(3, columns), 1
	case SizeThird:
		return clampCols(4, columns), 1
	case SizeHalf:
		return clampCols(6, columns), 2
	case SizeTall:
		return clampCols(6, columns), 4
	case SizeFull:
		return columns, 2
	default:
		return clampCols(6, columns), 2
	}
}

func clampCols(want, columns int) int {
	if want > columns {
		return columns
	}
	if want < 1 {
		return 1
	}
	return want
}

// packGrid assigns GridX/GridY/GridW/GridH to each widget. columns defaults
// to 12 when zero or negative. Returns a new slice; input is not mutated.
func packGrid(widgets []Widget, columns int) []Widget {
	if columns <= 0 {
		columns = 12
	}
	out := make([]Widget, len(widgets))
	copy(out, widgets)

	// Stable-sort widgets by group so grouped widgets are emitted together,
	// preserving each widget's original index as the secondary key.
	type idxWidget struct {
		original int
		w        *Widget
	}
	indexed := make([]idxWidget, len(out))
	for i := range out {
		indexed[i] = idxWidget{original: i, w: &out[i]}
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		gi := indexed[i].w.Group
		gj := indexed[j].w.Group
		if gi == gj {
			return indexed[i].original < indexed[j].original
		}
		// Ungrouped widgets sort after grouped ones (so groups are laid out
		// first). Within groupedness, order by group name for determinism.
		if gi == "" {
			return false
		}
		if gj == "" {
			return true
		}
		return gi < gj
	})

	// occupied[row] is a bitmap of occupied columns in that row. We grow
	// rows on demand.
	occupied := []uint64{}
	ensureRow := func(row int) {
		for len(occupied) <= row {
			occupied = append(occupied, 0)
		}
	}
	isFree := func(row, col, w, h int) bool {
		for r := row; r < row+h; r++ {
			ensureRow(r)
			for c := col; c < col+w; c++ {
				if occupied[r]&(uint64(1)<<uint(c)) != 0 {
					return false
				}
			}
		}
		return true
	}
	mark := func(row, col, w, h int) {
		for r := row; r < row+h; r++ {
			ensureRow(r)
			for c := col; c < col+w; c++ {
				occupied[r] |= uint64(1) << uint(c)
			}
		}
	}

	for _, iw := range indexed {
		w := iw.w
		gw, gh := sizeExtent(w.Size, columns)

		// Scan rows starting from 0, then columns, placing at the first fit.
		placed := false
		for row := 0; !placed; row++ {
			for col := 0; col+gw <= columns; col++ {
				if isFree(row, col, gw, gh) {
					mark(row, col, gw, gh)
					w.GridX = col
					w.GridY = row
					w.GridW = gw
					w.GridH = gh
					placed = true
					break
				}
			}
		}
	}
	return out
}

// appendPackedWidget preserves any existing valid layout, then places the new
// widget in the next open slot that fits its size token.
func appendPackedWidget(widgets []Widget, widget Widget, columns int) []Widget {
	if columns <= 0 {
		columns = 12
	}
	base := make([]Widget, len(widgets))
	copy(base, widgets)
	if err := validateGridLayout(base, columns); err != nil {
		base = packGrid(base, columns)
	}

	gw, gh := sizeExtent(widget.Size, columns)
	occupied := []uint64{}
	ensureRow := func(row int) {
		for len(occupied) <= row {
			occupied = append(occupied, 0)
		}
	}
	mark := func(row, col, w, h int) {
		for r := row; r < row+h; r++ {
			ensureRow(r)
			for c := col; c < col+w; c++ {
				occupied[r] |= uint64(1) << uint(c)
			}
		}
	}
	isFree := func(row, col, w, h int) bool {
		for r := row; r < row+h; r++ {
			ensureRow(r)
			for c := col; c < col+w; c++ {
				if occupied[r]&(uint64(1)<<uint(c)) != 0 {
					return false
				}
			}
		}
		return true
	}

	for _, existing := range base {
		if existing.GridW <= 0 || existing.GridH <= 0 {
			continue
		}
		mark(existing.GridY, existing.GridX, existing.GridW, existing.GridH)
	}

	for row := 0; ; row++ {
		for col := 0; col+gw <= columns; col++ {
			if isFree(row, col, gw, gh) {
				widget.GridX = col
				widget.GridY = row
				widget.GridW = gw
				widget.GridH = gh
				return append(base, widget)
			}
		}
	}
}
