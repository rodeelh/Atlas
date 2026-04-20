package dashboards

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCoordinatorSubscribeSeedsAndReplaysCachedSource(t *testing.T) {
	def := Dashboard{
		ID: "dash-1",
		Sources: []DataSource{{
			Name:    "status",
			Kind:    SourceKindRuntime,
			Refresh: RefreshPolicy{Mode: RefreshManual},
		}},
	}
	resolveCalls := 0
	c := NewCoordinator(
		func(id string) (Dashboard, error) {
			if id != def.ID {
				t.Fatalf("load called with %q", id)
			}
			return def, nil
		},
		func(_ context.Context, dashboardID, sourceName string) (any, error) {
			if dashboardID != def.ID || sourceName != "status" {
				t.Fatalf("resolve called with dashboard=%q source=%q", dashboardID, sourceName)
			}
			resolveCalls++
			return map[string]any{"count": resolveCalls}, nil
		},
	)

	first, unsubscribeFirst := c.Subscribe(def.ID)
	defer unsubscribeFirst()
	ev := receiveRefreshEvent(t, first)
	if ev.DashboardID != def.ID || ev.Source != "status" || ev.Error != "" {
		t.Fatalf("unexpected first event: %+v", ev)
	}
	if !ev.Success || ev.SourceKind != SourceKindRuntime || ev.ResolvedAt == "" || ev.LastSuccessfulAt == "" {
		t.Fatalf("expected success metadata on first event, got %+v", ev)
	}

	second, unsubscribeSecond := c.Subscribe(def.ID)
	defer unsubscribeSecond()
	replay := receiveRefreshEvent(t, second)
	if replay.DashboardID != ev.DashboardID || replay.Source != ev.Source || replay.At != ev.At {
		t.Fatalf("expected cached replay %+v, got %+v", ev, replay)
	}
	if replay.CacheAgeMs < 0 {
		t.Fatalf("expected non-negative cache age, got %+v", replay)
	}
}

func TestCoordinatorForceRefreshPushesErrorsToSubscribers(t *testing.T) {
	def := Dashboard{
		ID: "dash-err",
		Sources: []DataSource{{
			Name:    "news",
			Kind:    SourceKindRuntime,
			Refresh: RefreshPolicy{Mode: RefreshManual},
		}},
	}
	wantErr := errors.New("runtime endpoint /usage/summary returned 401")
	c := NewCoordinator(
		func(string) (Dashboard, error) { return def, nil },
		func(context.Context, string, string) (any, error) { return nil, wantErr },
	)

	ch, unsubscribe := c.Subscribe(def.ID)
	defer unsubscribe()
	ev := receiveRefreshEvent(t, ch)
	if ev.Source != "news" {
		t.Fatalf("expected news source, got %+v", ev)
	}
	if ev.Error != wantErr.Error() {
		t.Fatalf("expected pushed error %q, got %+v", wantErr.Error(), ev)
	}
	if ev.Success {
		t.Fatalf("error event should not be successful: %+v", ev)
	}
}

func TestCoordinatorErrorKeepsLastGoodDataAsStale(t *testing.T) {
	def := Dashboard{
		ID: "dash-stale",
		Sources: []DataSource{{
			Name:    "status",
			Kind:    SourceKindRuntime,
			Refresh: RefreshPolicy{Mode: RefreshManual},
		}},
	}
	fail := false
	c := NewCoordinator(
		func(string) (Dashboard, error) { return def, nil },
		func(context.Context, string, string) (any, error) {
			if fail {
				return nil, errors.New("temporary outage")
			}
			return map[string]any{"count": 7}, nil
		},
	)

	ch, unsubscribe := c.Subscribe(def.ID)
	defer unsubscribe()
	first := receiveRefreshEvent(t, ch)
	if first.Error != "" || first.Data == nil {
		t.Fatalf("expected first good event, got %+v", first)
	}

	fail = true
	events := c.ForceRefresh(context.Background(), def.ID)
	if len(events) != 1 {
		t.Fatalf("expected one event, got %+v", events)
	}
	stale := receiveRefreshEvent(t, ch)
	if stale.Error != "temporary outage" || !stale.Stale || stale.Data == nil {
		t.Fatalf("expected stale event with last good data, got %+v", stale)
	}
	if stale.LastSuccessfulAt == "" {
		t.Fatalf("expected lastSuccessfulAt on stale event, got %+v", stale)
	}
}

func TestCoordinatorForceRefreshSourceOnlyRefreshesRequestedSource(t *testing.T) {
	def := Dashboard{
		ID: "dash-one",
		Sources: []DataSource{
			{Name: "status", Kind: SourceKindRuntime, Refresh: RefreshPolicy{Mode: RefreshManual}},
			{Name: "usage", Kind: SourceKindRuntime, Refresh: RefreshPolicy{Mode: RefreshManual}},
		},
	}
	calls := []string{}
	c := NewCoordinator(
		func(string) (Dashboard, error) { return def, nil },
		func(_ context.Context, _ string, source string) (any, error) {
			calls = append(calls, source)
			return map[string]any{"source": source}, nil
		},
	)

	ch, unsubscribe := c.Subscribe(def.ID)
	defer unsubscribe()
	_ = receiveRefreshEvent(t, ch)
	_ = receiveRefreshEvent(t, ch)

	ev := c.ForceRefreshSource(context.Background(), def.ID, "usage")
	if ev == nil || ev.Source != "usage" {
		t.Fatalf("expected usage refresh event, got %+v", ev)
	}
	pushed := receiveRefreshEvent(t, ch)
	if pushed.Source != "usage" {
		t.Fatalf("expected pushed usage event, got %+v", pushed)
	}
	if calls[len(calls)-1] != "usage" {
		t.Fatalf("expected last refresh call for usage, got %+v", calls)
	}
}

func TestCoordinatorUnsubscribeStopsDashboardCoordinator(t *testing.T) {
	def := Dashboard{
		ID: "dash-stop",
		Sources: []DataSource{{
			Name:    "status",
			Kind:    SourceKindRuntime,
			Refresh: RefreshPolicy{Mode: RefreshManual},
		}},
	}
	c := NewCoordinator(
		func(string) (Dashboard, error) { return def, nil },
		func(context.Context, string, string) (any, error) { return map[string]any{"ok": true}, nil },
	)

	ch, unsubscribe := c.Subscribe(def.ID)
	_ = receiveRefreshEvent(t, ch)
	unsubscribe()

	c.mu.Lock()
	_, exists := c.per[def.ID]
	c.mu.Unlock()
	if exists {
		t.Fatal("expected coordinator to be removed after last unsubscribe")
	}

	if _, ok := <-ch; ok {
		t.Fatal("expected subscriber channel to close after unsubscribe")
	}
}

func receiveRefreshEvent(t *testing.T, ch <-chan RefreshEvent) RefreshEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("refresh event channel closed before event")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refresh event")
	}
	return RefreshEvent{}
}
