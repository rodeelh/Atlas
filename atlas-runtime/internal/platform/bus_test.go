package platform

import (
	"context"
	"testing"
	"time"
)

func TestInProcessBus_ExactMatch(t *testing.T) {
	bus := NewInProcessBus(1)
	got := make(chan Event, 1)

	_, err := bus.Subscribe("approval.resolved.v1", func(_ context.Context, event Event) error {
		got <- event
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), "approval.resolved.v1", "ok"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case event := <-got:
		if event.Name != "approval.resolved.v1" || event.Payload != "ok" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestInProcessBus_WildcardMatch(t *testing.T) {
	bus := NewInProcessBus(1)
	got := make(chan string, 1)

	_, err := bus.Subscribe("automation.*", func(_ context.Context, event Event) error {
		got <- event.Name
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), "automation.trigger.v1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case name := <-got:
		if name != "automation.trigger.v1" {
			t.Fatalf("got %q", name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wildcard event")
	}
}

func TestInProcessBus_DroppedCountIncrements(t *testing.T) {
	bus := NewInProcessBus(1)

	if err := bus.Publish(context.Background(), "no.listeners.v1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := bus.DroppedCount(); got != 1 {
		t.Fatalf("want 1 dropped event, got %d", got)
	}
}

func TestInProcessBus_UnsubscribeStopsDelivery(t *testing.T) {
	bus := NewInProcessBus(1)
	got := make(chan Event, 1)

	unsubscribe, err := bus.Subscribe("team.*", func(_ context.Context, event Event) error {
		got <- event
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	unsubscribe()

	if err := bus.Publish(context.Background(), "team.task.created.v1", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case event := <-got:
		t.Fatalf("unexpected event after unsubscribe: %+v", event)
	case <-time.After(150 * time.Millisecond):
	}
}
