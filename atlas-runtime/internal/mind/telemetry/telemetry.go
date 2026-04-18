// Package telemetry is the fire-and-forget event sink for the mind-thoughts
// subsystem. Every interesting event (nap started, nap completed, thought
// added, auto-execute attempted, sidebar dot shown, …) becomes one row in
// the storage mind_telemetry table.
//
// The helper is non-blocking by design. Callers push events to a buffered
// channel; a single drain goroutine consumes the channel and writes to
// SQLite. A slow disk never holds up a nap — if the buffer is full, the
// event is dropped with a warning rather than blocking the caller.
//
// This package is imported by internal/mind, internal/modules/mind, the chat
// service, and the dispatcher. It keeps a narrow interface (Emit, Query,
// Aggregate) so callers don't need to know about the storage types.
package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/storage"
)

// Kind enumerates the event types we expect in the mind_telemetry table.
// Callers should use these constants rather than raw strings so a typo
// surfaces at compile time.
type Kind string

const (
	KindNapStarted   Kind = "nap_started"
	KindNapCompleted Kind = "nap_completed"
	KindNapFailed    Kind = "nap_failed"
	KindNapSkipped   Kind = "nap_skipped"

	KindThoughtAdded      Kind = "thought_added"
	KindThoughtUpdated    Kind = "thought_updated"
	KindThoughtReinforced Kind = "thought_reinforced"
	KindThoughtDiscarded  Kind = "thought_discarded"
	KindThoughtMerged     Kind = "thought_merged"

	KindThoughtSurfaced    Kind = "thought_surfaced"
	KindEngagementRecorded Kind = "engagement_recorded"

	KindAutoExecuteAttempted Kind = "auto_execute_attempted"
	KindAutoExecuteSucceeded Kind = "auto_execute_succeeded"
	KindAutoExecuteFailed    Kind = "auto_execute_failed"

	KindApprovalProposed Kind = "approval_proposed"
	KindApprovalResolved Kind = "approval_resolved"

	KindGreetingDelivered Kind = "greeting_delivered"
	KindGreetingSkipped   Kind = "greeting_skipped"

	KindSidebarDotShown   Kind = "sidebar_dot_shown"
	KindSidebarDotClicked Kind = "sidebar_dot_clicked"
)

// Event is one entry in the buffered channel. The drain goroutine converts
// it to a storage.InsertMindTelemetry call.
type Event struct {
	Kind      Kind
	Timestamp time.Time
	ThoughtID string
	ConvID    string
	Payload   any // marshaled to JSON by the drain goroutine
}

// Emitter is the public interface callers use. It hides the channel and the
// drain goroutine behind a single method.
type Emitter struct {
	db      *storage.DB
	ch      chan Event
	stopCh  chan struct{}
	wg      sync.WaitGroup
	dropped int64 // atomic not required; approximate metric only
	mu      sync.Mutex
}

// bufferSize is the capacity of the send channel. 1000 events is enough for
// a full-speed nap that emits several ops plus dispatcher events. At ~200
// bytes per row post-marshal, the memory cost is ~200 KB at capacity.
const bufferSize = 1000

// drainBatchSize is how many events the drain goroutine writes in one DB
// round trip. Higher = fewer transactions but more latency. 32 is a
// reasonable compromise — at full speed this flushes every ~10 ms.
const drainBatchSize = 32

// drainIdleFlush is the maximum time the drain goroutine waits for a full
// batch before flushing whatever it has. Keeps latency bounded on slow
// periods where the buffer isn't filling.
const drainIdleFlush = 500 * time.Millisecond

// drainShutdownTimeout is the maximum time Stop blocks waiting for the drain
// goroutine to finish flushing. This caps shutdown time even when the caller
// passes context.Background() and guards against a slow or hanging DB write.
const drainShutdownTimeout = 5 * time.Second

// New starts a new emitter with its drain goroutine. Stop must be called on
// shutdown to ensure pending events are written before the process exits.
func New(db *storage.DB) *Emitter {
	e := &Emitter{
		db:     db,
		ch:     make(chan Event, bufferSize),
		stopCh: make(chan struct{}),
	}
	e.wg.Add(1)
	go e.drain()
	return e
}

// Emit enqueues one event for async persistence. Non-blocking: if the
// buffer is full, the event is dropped and a warning is logged. The caller
// is never held up by a slow disk.
//
// payload can be any JSON-marshalable value — a map, a struct, nil. It is
// marshaled in the drain goroutine so Emit stays cheap.
func (e *Emitter) Emit(kind Kind, thoughtID, convID string, payload any) {
	if e == nil {
		return
	}
	ev := Event{
		Kind:      kind,
		Timestamp: time.Now().UTC(),
		ThoughtID: thoughtID,
		ConvID:    convID,
		Payload:   payload,
	}
	select {
	case e.ch <- ev:
		// buffered successfully
	default:
		// buffer full — drop and log. Happens only under sustained write
		// pressure that outpaces the drain. Telemetry is best-effort.
		e.mu.Lock()
		e.dropped++
		dropped := e.dropped
		e.mu.Unlock()
		if dropped == 1 || dropped%100 == 0 {
			logstore.Write("warn",
				fmt.Sprintf("mind telemetry: buffer full, dropped %d events", dropped),
				map[string]string{"kind": string(kind)})
		}
	}
}

// Stop signals the drain goroutine to flush pending events and exit. Blocks
// until the goroutine has written everything or the deadline is reached.
// An internal drainShutdownTimeout is applied on top of any deadline already
// in ctx, so Stop is always bounded even when ctx is context.Background().
// Call on shutdown before closing the DB.
func (e *Emitter) Stop(ctx context.Context) error {
	if e == nil {
		return nil
	}
	close(e.stopCh)

	// Apply an internal deadline so this never hangs indefinitely regardless
	// of what context the caller provides.
	shutdownCtx, cancel := context.WithTimeout(ctx, drainShutdownTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		// Exits once the drain goroutine finishes its flush — bounded by
		// drainShutdownTimeout even if a DB write is slow.
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-shutdownCtx.Done():
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("telemetry drain timed out after %s", drainShutdownTimeout)
	}
}

// drain is the background goroutine that reads Events from the channel and
// writes them to storage. Batches up to drainBatchSize events before flushing
// to reduce SQLite transaction overhead. Also flushes on drainIdleFlush so
// the tail end of a burst doesn't sit in memory forever.
func (e *Emitter) drain() {
	defer e.wg.Done()

	batch := make([]Event, 0, drainBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		for _, ev := range batch {
			payloadJSON, err := json.Marshal(ev.Payload)
			if err != nil {
				payloadJSON = []byte("{}")
			}
			if ierr := e.db.InsertMindTelemetry(
				ev.Timestamp,
				string(ev.Kind),
				ev.ThoughtID,
				ev.ConvID,
				string(payloadJSON),
			); ierr != nil {
				logstore.Write("warn", "mind telemetry insert failed: "+ierr.Error(),
					map[string]string{"kind": string(ev.Kind)})
			}
		}
		batch = batch[:0]
	}

	ticker := time.NewTicker(drainIdleFlush)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			// Drain whatever is still in the channel before exiting.
			for {
				select {
				case ev := <-e.ch:
					batch = append(batch, ev)
					if len(batch) >= drainBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case ev := <-e.ch:
			batch = append(batch, ev)
			if len(batch) >= drainBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Stats captures counts and basic aggregates for a given window. Used by
// the dashboard widgets in phase 6 and by the analysis endpoint.
type Stats struct {
	ByKind map[string]int `json:"by_kind"`
	Total  int            `json:"total"`
	Since  time.Time      `json:"since"`
}

// Aggregate returns counts by event kind since the given timestamp.
func Aggregate(db *storage.DB, since time.Time) (Stats, error) {
	counts, err := db.CountMindTelemetryByKind(since)
	if err != nil {
		return Stats{}, err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return Stats{
		ByKind: counts,
		Total:  total,
		Since:  since,
	}, nil
}
