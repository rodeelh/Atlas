package dashboards

import (
	"context"
	"fmt"
	"time"
)

func (m *Module) editDraftForDashboard(id string) (Dashboard, error) {
	if id == "" {
		return Dashboard{}, fmt.Errorf("id is required")
	}
	source, err := m.store.Get(id)
	if err != nil {
		return Dashboard{}, err
	}
	if source.Status == StatusDraft {
		return source, nil
	}
	for _, existing := range m.store.ListByStatus(StatusDraft) {
		if existing.BaseDashboardID == source.ID {
			return existing, nil
		}
	}
	draft := source
	draft.ID = NewDashboardID()
	draft.BaseDashboardID = source.ID
	draft.Status = StatusDraft
	draft.CommittedAt = nil
	draft.CreatedAt = time.Time{}
	draft.UpdatedAt = time.Time{}
	return m.store.Save(draft)
}

func (m *Module) commitDraftDashboard(ctx context.Context, id string) (Dashboard, error) {
	d, err := m.loadDraft(id)
	if err != nil {
		return Dashboard{}, err
	}
	for i := range d.Widgets {
		if err := compileWidget(&d.Widgets[i].Code); err != nil {
			return Dashboard{}, fmt.Errorf("widget %s compile: %w", d.Widgets[i].ID, err)
		}
	}
	for _, w := range d.Widgets {
		if err := validateBindings(d, w.Bindings); err != nil {
			return Dashboard{}, fmt.Errorf("widget %s bindings: %w", w.ID, err)
		}
	}
	if err := m.validateCommitReadiness(ctx, d); err != nil {
		return Dashboard{}, err
	}
	columns := d.Layout.Columns
	if columns <= 0 {
		columns = 12
		d.Layout.Columns = columns
	}
	if err := validateGridLayout(d.Widgets, columns); err != nil {
		d.Widgets = packGrid(d.Widgets, columns)
		if err := validateGridLayout(d.Widgets, columns); err != nil {
			return Dashboard{}, err
		}
	}

	now := nowUTC()
	published := d
	published.Status = StatusLive
	published.CommittedAt = &now
	if published.BaseDashboardID != "" {
		base, err := m.store.Get(published.BaseDashboardID)
		if err != nil {
			return Dashboard{}, err
		}
		published.ID = base.ID
		published.BaseDashboardID = ""
		published.CreatedAt = base.CreatedAt
	}
	saved, err := m.store.Save(published)
	if err != nil {
		return Dashboard{}, err
	}
	if d.BaseDashboardID != "" {
		if err := m.store.Delete(d.ID); err != nil {
			return Dashboard{}, err
		}
	}
	return saved, nil
}
