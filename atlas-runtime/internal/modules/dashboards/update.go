package dashboards

import (
	"errors"
	"fmt"
)

// WidgetUpdateRequest is the draft-edit payload accepted by the HTTP widget
// update route. Pointer fields let clients intentionally clear string values.
type WidgetUpdateRequest struct {
	Title       *string              `json:"title,omitempty"`
	Description *string              `json:"description,omitempty"`
	Size        *string              `json:"size,omitempty"`
	Group       *string              `json:"group,omitempty"`
	Preset      *string              `json:"preset,omitempty"`
	TSX         *string              `json:"tsx,omitempty"`
	Bindings    *[]DataSourceBinding `json:"bindings,omitempty"`
	Options     map[string]any       `json:"options,omitempty"`
	GridX       *int                 `json:"gridX,omitempty"`
	GridY       *int                 `json:"gridY,omitempty"`
	GridW       *int                 `json:"gridW,omitempty"`
	GridH       *int                 `json:"gridH,omitempty"`
}

type LayoutWidgetUpdate struct {
	ID    string `json:"id"`
	GridX int    `json:"gridX"`
	GridY int    `json:"gridY"`
	GridW int    `json:"gridW"`
	GridH int    `json:"gridH"`
}

type LayoutUpdateRequest struct {
	Widgets []LayoutWidgetUpdate `json:"widgets"`
}

func (m *Module) updateDraftLayout(dashboardID string, req LayoutUpdateRequest) (Dashboard, error) {
	if len(req.Widgets) == 0 {
		return Dashboard{}, errors.New("widgets are required")
	}
	d, err := m.loadDraft(dashboardID)
	if err != nil {
		return Dashboard{}, err
	}
	byID := make(map[string]LayoutWidgetUpdate, len(req.Widgets))
	for _, next := range req.Widgets {
		if next.ID == "" {
			return Dashboard{}, errors.New("widget id is required")
		}
		byID[next.ID] = next
	}
	for i := range d.Widgets {
		next, ok := byID[d.Widgets[i].ID]
		if !ok {
			continue
		}
		d.Widgets[i].GridX = next.GridX
		d.Widgets[i].GridY = next.GridY
		d.Widgets[i].GridW = next.GridW
		d.Widgets[i].GridH = next.GridH
	}
	for id := range byID {
		found := false
		for _, w := range d.Widgets {
			if w.ID == id {
				found = true
				break
			}
		}
		if !found {
			return Dashboard{}, fmt.Errorf("%w: widget %q", ErrNotFound, id)
		}
	}
	columns := d.Layout.Columns
	if columns <= 0 {
		columns = 12
		d.Layout.Columns = columns
	}
	if err := validateGridLayout(d.Widgets, columns); err != nil {
		return Dashboard{}, err
	}
	return m.store.Save(d)
}

func (m *Module) updateDraftWidget(dashboardID, widgetID string, req WidgetUpdateRequest) (Dashboard, error) {
	if widgetID == "" {
		return Dashboard{}, errors.New("widgetId is required")
	}
	d, err := m.loadDraft(dashboardID)
	if err != nil {
		return Dashboard{}, err
	}
	idx := -1
	for i := range d.Widgets {
		if d.Widgets[i].ID == widgetID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Dashboard{}, fmt.Errorf("%w: widget %q", ErrNotFound, widgetID)
	}

	w := d.Widgets[idx]
	if req.Title != nil {
		w.Title = *req.Title
	}
	if req.Description != nil {
		w.Description = *req.Description
	}
	if req.Size != nil {
		if !validWidgetSize(*req.Size) {
			return Dashboard{}, fmt.Errorf("invalid widget size %q", *req.Size)
		}
		w.Size = *req.Size
	}
	if req.Group != nil {
		w.Group = *req.Group
	}
	if req.GridX != nil {
		w.GridX = *req.GridX
	}
	if req.GridY != nil {
		w.GridY = *req.GridY
	}
	if req.GridW != nil {
		w.GridW = *req.GridW
	}
	if req.GridH != nil {
		w.GridH = *req.GridH
	}
	if req.Bindings != nil {
		w.Bindings = *req.Bindings
	}
	if err := validateBindings(d, w.Bindings); err != nil {
		return Dashboard{}, err
	}

	if w.Code.Mode == ModeCode {
		if req.Preset != nil || req.Options != nil {
			return Dashboard{}, errors.New("code widgets cannot change preset or options through the draft inspector")
		}
		if req.TSX != nil {
			w.Code.TSX = *req.TSX
		}
		if err := compileWidget(&w.Code); err != nil {
			return Dashboard{}, err
		}
	} else {
		if req.TSX != nil {
			return Dashboard{}, errors.New("preset widgets cannot store TSX")
		}
		w.Code.Mode = ModePreset
		if req.Preset != nil {
			w.Code.Preset = *req.Preset
		}
		if req.Options != nil {
			w.Code.Options = req.Options
		}
		if err := compileWidget(&w.Code); err != nil {
			return Dashboard{}, err
		}
	}

	d.Widgets[idx] = w
	columns := d.Layout.Columns
	if columns <= 0 {
		columns = 12
		d.Layout.Columns = columns
	}
	// Only validate grid layout when widgets have been explicitly placed (GridW>0).
	// Unplaced widgets (GridW=0) will be positioned by packGrid at commit time.
	if widgetsArePlaced(d.Widgets) {
		if err := validateGridLayout(d.Widgets, columns); err != nil {
			return Dashboard{}, err
		}
	}
	return m.store.Save(d)
}

// widgetsArePlaced reports whether all widgets have been explicitly placed in
// the grid (GridW > 0). Unplaced widgets get positions from packGrid at commit.
func widgetsArePlaced(widgets []Widget) bool {
	for _, w := range widgets {
		if w.GridW <= 0 {
			return false
		}
	}
	return true
}

func validateGridLayout(widgets []Widget, columns int) error {
	if columns <= 0 {
		columns = 12
	}
	for i := range widgets {
		w := widgets[i]
		if w.GridX < 0 || w.GridY < 0 {
			return fmt.Errorf("widget %q has negative grid position", w.ID)
		}
		if w.GridW <= 0 || w.GridH <= 0 {
			return fmt.Errorf("widget %q has invalid grid size", w.ID)
		}
		if w.GridX+w.GridW > columns {
			return fmt.Errorf("widget %q exceeds %d-column dashboard grid", w.ID, columns)
		}
		for j := i + 1; j < len(widgets); j++ {
			if rectsOverlap(w, widgets[j]) {
				return fmt.Errorf("widget %q overlaps widget %q", w.ID, widgets[j].ID)
			}
		}
	}
	return nil
}

func rectsOverlap(a, b Widget) bool {
	return a.GridX < b.GridX+b.GridW &&
		a.GridX+a.GridW > b.GridX &&
		a.GridY < b.GridY+b.GridH &&
		a.GridY+a.GridH > b.GridY
}

func validWidgetSize(size string) bool {
	switch size {
	case SizeQuarter, SizeThird, SizeHalf, SizeTall, SizeFull:
		return true
	default:
		return false
	}
}
