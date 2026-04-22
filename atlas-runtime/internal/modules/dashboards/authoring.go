package dashboards

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"atlas-runtime-go/internal/skills"
)

func (m *Module) createDraftDashboard(name, description string) (Dashboard, error) {
	if name == "" {
		return Dashboard{}, errors.New("name is required")
	}
	return m.store.Save(Dashboard{
		ID:          NewDashboardID(),
		Name:        name,
		Description: description,
		Status:      StatusDraft,
		Sources:     []DataSource{},
		Widgets:     []Widget{},
		Layout:      LayoutHints{Columns: 12},
	})
}

func (m *Module) addDraftWidget(dashboardID string, widget Widget) (Dashboard, Widget, error) {
	d, err := m.loadDraft(dashboardID)
	if err != nil {
		return Dashboard{}, Widget{}, err
	}
	if err := validateBindings(d, widget.Bindings); err != nil {
		return Dashboard{}, Widget{}, err
	}
	if err := compileWidget(&widget.Code); err != nil {
		return Dashboard{}, Widget{}, err
	}
	widget.ID = NewWidgetID()
	columns := d.Layout.Columns
	if columns <= 0 {
		columns = 12
		d.Layout.Columns = columns
	}
	d.Widgets = appendPackedWidget(d.Widgets, widget, columns)
	saved, err := m.store.Save(d)
	if err != nil {
		return Dashboard{}, Widget{}, err
	}
	for _, candidate := range saved.Widgets {
		if candidate.ID == widget.ID {
			return saved, candidate, nil
		}
	}
	return Dashboard{}, Widget{}, fmt.Errorf("saved widget %q missing from dashboard", widget.ID)
}

func (m *Module) deleteDraftWidget(dashboardID, widgetID string) (Dashboard, error) {
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
	d.Widgets = append(d.Widgets[:idx], d.Widgets[idx+1:]...)
	return m.store.Save(d)
}

func (m *Module) upsertDraftSource(dashboardID string, source DataSource) (Dashboard, error) {
	if source.Name == "" || source.Kind == "" {
		return Dashboard{}, errors.New("source name and kind are required")
	}
	if source.Config == nil {
		source.Config = map[string]any{}
	}
	if source.Refresh.Mode == "" {
		source.Refresh.Mode = RefreshManual
	}
	if source.Refresh.Mode == RefreshInterval && source.Refresh.IntervalSeconds <= 0 {
		return Dashboard{}, errors.New("intervalSeconds must be > 0")
	}
	if source.Refresh.Mode != RefreshManual && source.Refresh.Mode != RefreshInterval && source.Refresh.Mode != RefreshPush {
		return Dashboard{}, fmt.Errorf("unknown refresh mode %q", source.Refresh.Mode)
	}
	if err := validateSourceKindConfig(source.Kind, source.Config); err != nil {
		return Dashboard{}, err
	}
	if source.Kind == SourceKindSkill && m.skillsRegistry != nil {
		if action, _ := source.Config["action"].(string); action != "" {
			canonical := m.skillsRegistry.Normalise(action)
			if !m.skillsRegistry.HasAction(canonical) {
				if suggestions := m.skillsRegistry.ActionsForSkill(canonical); len(suggestions) > 0 {
					return Dashboard{}, fmt.Errorf("skill action %q is a skill ID, not an action ID; use one of: %s", action, strings.Join(suggestions, ", "))
				}
				return Dashboard{}, fmt.Errorf("skill action %q is not registered", action)
			}
			if m.skillsRegistry.GetActionClass(canonical) != skills.ActionClassRead {
				return Dashboard{}, fmt.Errorf("skill action %q is not read-only", canonical)
			}
			source.Config["action"] = canonical
		}
	}

	d, err := m.loadDraft(dashboardID)
	if err != nil {
		return Dashboard{}, err
	}
	replaced := false
	for i := range d.Sources {
		if d.Sources[i].Name == source.Name {
			d.Sources[i] = source
			replaced = true
			break
		}
	}
	if !replaced {
		d.Sources = append(d.Sources, source)
	}
	return m.store.Save(d)
}

func (m *Module) deleteDraftSource(dashboardID, sourceName string) (Dashboard, error) {
	if sourceName == "" {
		return Dashboard{}, errors.New("source name is required")
	}
	d, err := m.loadDraft(dashboardID)
	if err != nil {
		return Dashboard{}, err
	}
	for _, w := range d.Widgets {
		for _, binding := range w.Bindings {
			if binding.Source == sourceName {
				return Dashboard{}, fmt.Errorf("source %q is still used by widget %q", sourceName, w.TitleOrID())
			}
		}
	}
	idx := -1
	for i := range d.Sources {
		if d.Sources[i].Name == sourceName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Dashboard{}, fmt.Errorf("%w: source %q", ErrNotFound, sourceName)
	}
	d.Sources = append(d.Sources[:idx], d.Sources[idx+1:]...)
	return m.store.Save(d)
}

func (m *Module) addAIWidget(ctx context.Context, dashboardID string, req AIWidgetPromptRequest) (Dashboard, Widget, error) {
	if m.widgetAuthor == nil {
		return Dashboard{}, Widget{}, errors.New("ai widget author not wired")
	}
	if _, err := m.loadDraft(dashboardID); err != nil {
		return Dashboard{}, Widget{}, err
	}

	var bindings []DataSourceBinding
	if req.SourceName != "" {
		sourceData, err := m.resolveSourceByName(ctx, dashboardID, req.SourceName)
		if err != nil {
			return Dashboard{}, Widget{}, err
		}
		req.SourceData = sourceData
		bindings = []DataSourceBinding{{Source: req.SourceName}}
	}

	spec, err := m.widgetAuthor.Generate(ctx, req)
	if err != nil {
		return Dashboard{}, Widget{}, err
	}

	widget := Widget{
		Title:       strings.TrimSpace(spec.Title),
		Description: strings.TrimSpace(spec.Description),
		Size:        fallbackWidgetSize(spec.Size),
		Bindings:    bindings,
	}
	switch spec.Mode {
	case ModeCode:
		widget.Code = WidgetCode{Mode: ModeCode, TSX: spec.TSX}
	default:
		widget.Code = WidgetCode{Mode: ModePreset, Preset: spec.Preset, Options: spec.Options}
	}
	return m.addDraftWidget(dashboardID, widget)
}

func fallbackWidgetSize(size string) string {
	if validWidgetSize(size) {
		return size
	}
	return SizeHalf
}
