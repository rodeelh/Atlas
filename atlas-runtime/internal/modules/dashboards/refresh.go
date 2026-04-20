package dashboards

// refresh.go — SSE coordinator. One goroutine per dashboard with live
// subscribers; per-source TTL cache so reconnects get an immediate replay.
//
// Lifecycle:
//   - subscribe(dashboardID) starts the coordinator if this is the first
//     subscriber. A replay of every cached source is delivered synchronously
//     before the subscriber's channel is returned, so the client paints the
//     initial frame without a round-trip.
//   - unsubscribe(dashboardID, ch) stops the coordinator when the last
//     subscriber leaves.
//   - Push(dashboardID, sourceName, data) updates the cache and fans out.
//   - ForceRefresh(dashboardID) triggers an immediate re-resolution loop.
//
// Interval refresh is handled by per-source goroutines inside the
// coordinator; manual refreshes use ForceRefresh; push sources rely on
// Push() calls from the outside.

import (
	"context"
	"sync"
	"time"
)

// RefreshEvent is the payload pushed to SSE subscribers.
type RefreshEvent struct {
	DashboardID      string `json:"dashboardId"`
	Source           string `json:"source"`
	SourceKind       string `json:"sourceKind,omitempty"`
	Success          bool   `json:"success"`
	Data             any    `json:"data,omitempty"`
	Error            string `json:"error,omitempty"`
	At               string `json:"at"`
	ResolvedAt       string `json:"resolvedAt,omitempty"`
	DurationMs       int64  `json:"durationMs,omitempty"`
	LastSuccessfulAt string `json:"lastSuccessfulAt,omitempty"`
	Stale            bool   `json:"stale,omitempty"`
	CacheAgeMs       int64  `json:"cacheAgeMs,omitempty"`
}

// coordinator manages live state for a single dashboard.
type coordinator struct {
	dashboardID string
	mu          sync.Mutex
	cache       map[string]RefreshEvent // source name -> last event
	subs        map[chan RefreshEvent]struct{}
	cancel      context.CancelFunc
	tickers     []*time.Ticker
}

// Coordinator is the public fan-out facade. One instance per dashboards
// module.
type Coordinator struct {
	mu      sync.Mutex
	per     map[string]*coordinator
	resolve func(ctx context.Context, dashboardID, sourceName string) (any, error)
	load    func(dashboardID string) (Dashboard, error)
}

// NewCoordinator constructs a Coordinator. resolve does the actual data
// fetch for a (dashboard, source); load returns the current dashboard so
// the coordinator can spin up interval timers.
func NewCoordinator(
	load func(id string) (Dashboard, error),
	resolve func(ctx context.Context, dashboardID, sourceName string) (any, error),
) *Coordinator {
	return &Coordinator{
		per:     map[string]*coordinator{},
		resolve: resolve,
		load:    load,
	}
}

// Subscribe opens an event channel for dashboardID. Callers MUST invoke the
// returned unsubscribe when done. The channel is buffered so slow consumers
// drop old events rather than block the coordinator.
func (c *Coordinator) Subscribe(dashboardID string) (<-chan RefreshEvent, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	co := c.per[dashboardID]
	if co == nil {
		co = &coordinator{
			dashboardID: dashboardID,
			cache:       map[string]RefreshEvent{},
			subs:        map[chan RefreshEvent]struct{}{},
		}
		c.per[dashboardID] = co
		c.startCoordinator(co)
	}
	ch := make(chan RefreshEvent, 32)
	co.mu.Lock()
	co.subs[ch] = struct{}{}
	// Replay the cached last-known event for every source into the new channel.
	// For returning subscribers the cache is already populated, so data arrives
	// immediately. For the very first subscriber the cache may be empty; the
	// initial-seed goroutine started by startCoordinator will populate the cache
	// and push to co.subs (which now includes this channel) once each source
	// resolves.
	for _, ev := range co.cache {
		ev = withCacheAge(ev)
		select {
		case ch <- ev:
		default:
		}
	}
	co.mu.Unlock()

	unsubscribe := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		co.mu.Lock()
		delete(co.subs, ch)
		close(ch)
		empty := len(co.subs) == 0
		co.mu.Unlock()
		if empty {
			c.stopCoordinator(co)
			delete(c.per, dashboardID)
		}
	}
	return ch, unsubscribe
}

// Push updates the cache for (dashboardID, sourceName) and fans out to any
// live subscribers. Coordinator does not need to be running — if nobody is
// subscribed the call is a no-op (the cache is bound to live coordinators).
func (c *Coordinator) Push(dashboardID, source string, data any, fetchErr error) {
	ev := RefreshEvent{
		DashboardID: dashboardID,
		Source:      source,
		Success:     fetchErr == nil,
		Data:        data,
		At:          time.Now().UTC().Format(time.RFC3339),
		ResolvedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if fetchErr != nil {
		ev.Error = fetchErr.Error()
	}
	c.pushEvent(ev)
}

func (c *Coordinator) pushEvent(ev RefreshEvent) RefreshEvent {
	c.mu.Lock()
	co := c.per[ev.DashboardID]
	c.mu.Unlock()
	if co == nil {
		return ev
	}
	co.mu.Lock()
	if ev.At == "" {
		ev.At = time.Now().UTC().Format(time.RFC3339)
	}
	if ev.ResolvedAt == "" {
		ev.ResolvedAt = ev.At
	}
	if ev.Error == "" && ev.Success {
		ev.LastSuccessfulAt = ev.ResolvedAt
	} else if cached, ok := co.cache[ev.Source]; ok {
		if ev.Data == nil && cached.Data != nil {
			ev.Data = cached.Data
			ev.Stale = true
		}
		if ev.LastSuccessfulAt == "" {
			ev.LastSuccessfulAt = cached.LastSuccessfulAt
			if ev.LastSuccessfulAt == "" && cached.Error == "" {
				ev.LastSuccessfulAt = cached.ResolvedAt
				if ev.LastSuccessfulAt == "" {
					ev.LastSuccessfulAt = cached.At
				}
			}
		}
		if ev.SourceKind == "" {
			ev.SourceKind = cached.SourceKind
		}
	}
	co.cache[ev.Source] = ev
	subs := make([]chan RefreshEvent, 0, len(co.subs))
	for ch := range co.subs {
		subs = append(subs, ch)
	}
	co.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- withCacheAge(ev):
		default:
			// Slow consumer — drop.
		}
	}
	return ev
}

// ForceRefresh resolves every source for dashboardID and pushes. Returns the
// fresh events. Safe to call even when there are no subscribers (will still
// update cache if a coordinator is running).
func (c *Coordinator) ForceRefresh(ctx context.Context, dashboardID string) []RefreshEvent {
	d, err := c.load(dashboardID)
	if err != nil {
		return nil
	}
	out := make([]RefreshEvent, len(d.Sources))
	var wg sync.WaitGroup
	for i, src := range d.Sources {
		wg.Add(1)
		go func(idx int, source DataSource) {
			defer wg.Done()
			start := time.Now()
			data, rerr := c.resolve(ctx, dashboardID, source.Name)
			resolvedAt := time.Now().UTC()
			ev := RefreshEvent{
				DashboardID: dashboardID,
				Source:      source.Name,
				SourceKind:  source.Kind,
				Success:     rerr == nil,
				Data:        data,
				At:          resolvedAt.Format(time.RFC3339),
				ResolvedAt:  resolvedAt.Format(time.RFC3339),
				DurationMs:  time.Since(start).Milliseconds(),
			}
			if rerr != nil {
				ev.Error = rerr.Error()
			}
			out[idx] = c.pushEvent(ev)
		}(i, src)
	}
	wg.Wait()
	return out
}

// ForceRefreshSource resolves one source for dashboardID and pushes the new
// event to subscribers. Returns nil if the dashboard/source cannot be loaded.
func (c *Coordinator) ForceRefreshSource(ctx context.Context, dashboardID, sourceName string) *RefreshEvent {
	d, err := c.load(dashboardID)
	if err != nil {
		return nil
	}
	var source *DataSource
	for i := range d.Sources {
		if d.Sources[i].Name == sourceName {
			source = &d.Sources[i]
			break
		}
	}
	if source == nil {
		return nil
	}
	start := time.Now()
	data, rerr := c.resolve(ctx, dashboardID, source.Name)
	resolvedAt := time.Now().UTC()
	ev := RefreshEvent{
		DashboardID: dashboardID,
		Source:      source.Name,
		SourceKind:  source.Kind,
		Success:     rerr == nil,
		Data:        data,
		At:          resolvedAt.Format(time.RFC3339),
		ResolvedAt:  resolvedAt.Format(time.RFC3339),
		DurationMs:  time.Since(start).Milliseconds(),
	}
	if rerr != nil {
		ev.Error = rerr.Error()
	}
	pushed := c.pushEvent(ev)
	return &pushed
}

// startCoordinator sets up interval tickers for push/interval sources. Must
// be called with c.mu held.
func (c *Coordinator) startCoordinator(co *coordinator) {
	ctx, cancel := context.WithCancel(context.Background())
	co.cancel = cancel

	// Spin up a one-shot goroutine to seed the cache before any intervals fire.
	go func() {
		events := c.ForceRefresh(ctx, co.dashboardID)
		_ = events
	}()

	def, err := c.load(co.dashboardID)
	if err != nil {
		return
	}
	for _, src := range def.Sources {
		if src.Refresh.Mode != RefreshInterval {
			continue
		}
		interval := src.Refresh.IntervalSeconds
		if interval <= 0 {
			continue
		}
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		co.tickers = append(co.tickers, ticker)
		sourceName := src.Name
		sourceKind := src.Kind
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					start := time.Now()
					data, rerr := c.resolve(ctx, co.dashboardID, sourceName)
					resolvedAt := time.Now().UTC()
					c.pushEvent(RefreshEvent{
						DashboardID: co.dashboardID,
						Source:      sourceName,
						SourceKind:  sourceKind,
						Success:     rerr == nil,
						Data:        data,
						Error:       errorString(rerr),
						At:          resolvedAt.Format(time.RFC3339),
						ResolvedAt:  resolvedAt.Format(time.RFC3339),
						DurationMs:  time.Since(start).Milliseconds(),
					})
				}
			}
		}()
	}
}

func withCacheAge(ev RefreshEvent) RefreshEvent {
	if ev.ResolvedAt == "" {
		return ev
	}
	t, err := time.Parse(time.RFC3339, ev.ResolvedAt)
	if err != nil {
		return ev
	}
	ev.CacheAgeMs = time.Since(t).Milliseconds()
	if ev.CacheAgeMs < 0 {
		ev.CacheAgeMs = 0
	}
	return ev
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *Coordinator) stopCoordinator(co *coordinator) {
	if co.cancel != nil {
		co.cancel()
	}
	for _, t := range co.tickers {
		t.Stop()
	}
	co.tickers = nil
}
