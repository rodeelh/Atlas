package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// Event is one private in-process platform event.
type Event struct {
	Name    string
	Payload any
}

// EventHandler is invoked asynchronously for events that match a subscription.
type EventHandler func(ctx context.Context, event Event) error

// EventBus is the internal event primitive used for selectively decoupled
// cross-module flows. It is intentionally private to the runtime.
type EventBus interface {
	Publish(ctx context.Context, name string, payload any) error
	Subscribe(pattern string, handler EventHandler) (unsubscribe func(), err error)
	DroppedCount() int64
}

type busSubscription struct {
	id      uint64
	pattern string
	ch      chan Event
	stop    chan struct{}
}

// InProcessBus is a buffered, non-blocking event bus for private runtime use.
type InProcessBus struct {
	mu     sync.RWMutex
	nextID atomic.Uint64
	drops  atomic.Int64
	buffer int
	subs   map[uint64]busSubscription
}

func NewInProcessBus(buffer int) *InProcessBus {
	if buffer <= 0 {
		buffer = 256
	}
	return &InProcessBus{
		buffer: buffer,
		subs:   make(map[uint64]busSubscription),
	}
}

func (b *InProcessBus) Publish(ctx context.Context, name string, payload any) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("platform: event name is required")
	}

	event := Event{Name: name, Payload: payload}

	b.mu.RLock()
	var matches []busSubscription
	for _, sub := range b.subs {
		if matchesPattern(sub.pattern, name) {
			matches = append(matches, sub)
		}
	}
	b.mu.RUnlock()

	if len(matches) == 0 {
		b.drops.Add(1)
		return nil
	}

	for _, sub := range matches {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sub.stop:
			continue
		case sub.ch <- event:
		default:
			b.drops.Add(1)
		}
	}
	return nil
}

func (b *InProcessBus) Subscribe(pattern string, handler EventHandler) (func(), error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("platform: subscription pattern is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("platform: event handler is required")
	}

	id := b.nextID.Add(1)
	sub := busSubscription{
		id:      id,
		pattern: pattern,
		ch:      make(chan Event, b.buffer),
		stop:    make(chan struct{}),
	}

	b.mu.Lock()
	b.subs[id] = sub
	b.mu.Unlock()

	go func() {
		for {
			select {
			case <-sub.stop:
				return
			case event := <-sub.ch:
				_ = handler(context.Background(), event)
			}
		}
	}()

	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		close(sub.stop)
	}, nil
}

func (b *InProcessBus) DroppedCount() int64 {
	return b.drops.Load()
}

func matchesPattern(pattern, name string) bool {
	switch {
	case pattern == "*":
		return true
	case pattern == name:
		return true
	case strings.HasSuffix(pattern, ".*"):
		prefix := strings.TrimSuffix(pattern, ".*")
		return name == prefix || strings.HasPrefix(name, prefix+".")
	default:
		return false
	}
}
