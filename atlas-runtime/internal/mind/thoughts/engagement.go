package thoughts

// engagement.go manages the thought-engagement.jsonl sidecar — one line per
// event recorded when the agent surfaces a thought in conversation and the
// next user turn reveals whether the user engaged positively, negatively, or
// not at all.
//
// This file is consumed by each nap as one of its inputs (last 7 days of
// events). The keep test weights engagement above the three other gates —
// it is the strongest signal we have, so the sidecar is load-bearing for the
// whole keep loop.
//
// Format: newline-delimited JSON, append-only. Each line is an Event struct
// marshaled with encoding/json. A malformed line is skipped with an error
// returned to the caller; the rest of the file is still readable.
//
// Concurrency: a package-level mutex serializes writes. Reads hold the mutex
// briefly for the duration of the open+read+close. No cross-process locking
// is needed because only the Atlas daemon writes this file.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Signal is the engagement outcome for one surfacing attempt. Signals
// begin life as SignalPending (the agent surfaced a thought but the user
// has not yet replied) and transition to positive / negative / ignored
// once the classifier runs against the user's next turn, or once the
// nap decides an old pending is past its window and should decay to
// ignored.
type Signal string

const (
	SignalPending  Signal = "pending"
	SignalPositive Signal = "positive"
	SignalNegative Signal = "negative"
	SignalIgnored  Signal = "ignored"
)

// Valid returns true if s is a recognized terminal engagement signal
// (what the keep test cares about). SignalPending is NOT valid here —
// it's an intermediate state, and counting it would pollute the keep
// test inputs.
func (s Signal) Valid() bool {
	return s == SignalPositive || s == SignalNegative || s == SignalIgnored
}

// IsTerminal is a clearer alias for Valid — returns true when the signal
// is a final engagement outcome rather than the pending intermediate.
func (s Signal) IsTerminal() bool { return s.Valid() }

// Event is one engagement record. Each line of thought-engagement.jsonl
// unmarshals into exactly one of these.
//
// The lifecycle is:
//
//   1. Agent surfaces a thought → Event created with Signal=pending
//      and SurfacedAt set.
//   2. User replies → classifier runs → same Event is rewritten in
//      place with Signal=positive|negative|ignored, ClassifiedAt, and
//      ClassifierConfidence.
//   3. Nap reads recent events. Pending events older than the nap's
//      window are treated as ignored for keep-test counting without
//      being rewritten (avoids races with concurrent classifiers).
//
// SurfacingID is the stable key used to find and upsert the pending row
// before classification. Built from (conv_id, message_id, thought_id) so
// two different thoughts surfaced in the same assistant reply produce
// distinct rows, and the same thought surfaced in two different replies
// also produces distinct rows.
type Event struct {
	SurfacingID          string    `json:"surfacing_id"`
	ThoughtID            string    `json:"thought_id"`
	ConvID               string    `json:"conv_id,omitempty"`
	MessageID            string    `json:"message_id,omitempty"`
	Timestamp            time.Time `json:"ts"`                    // creation time — when the surfacing landed
	SurfacedAt           time.Time `json:"surfaced_at,omitempty"` // alias for Timestamp; explicit for readability
	ClassifiedAt         time.Time `json:"classified_at,omitempty"`
	Signal               Signal    `json:"signal"`
	ClassifierConfidence int       `json:"classifier_confidence,omitempty"` // 0-100
	ClassifierReasoning  string    `json:"classifier_reasoning,omitempty"`  // one-line why, for hand-grading
	UserMessage          string    `json:"user_message,omitempty"`          // short excerpt — the reply that was classified
}

// engagementPath returns the full path to the sidecar file inside supportDir.
func engagementPath(supportDir string) string {
	return filepath.Join(supportDir, "thought-engagement.jsonl")
}

// engagementMu serializes writes + reads across goroutines. Single-process
// access means we don't need flock.
var engagementMu sync.Mutex

// RecordEvent appends one engagement event to the sidecar. Creates the file
// if missing. Returns an error if the event is malformed or the write fails.
// Unrecognized signals (neither terminal nor pending) are coerced to
// SignalIgnored — the safest interpretation of unknown user behavior.
func RecordEvent(supportDir string, ev Event) error {
	if ev.ThoughtID == "" {
		return fmt.Errorf("engagement: empty thought_id")
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	// Pending is valid for writes (surfacing detection path); terminal
	// signals are valid too. Anything else is coerced to ignored.
	if ev.Signal != SignalPending && !ev.Signal.Valid() {
		ev.Signal = SignalIgnored
	}
	blob, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("engagement: marshal: %w", err)
	}

	engagementMu.Lock()
	defer engagementMu.Unlock()

	path := engagementPath(supportDir)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("engagement: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(blob, '\n')); err != nil {
		return fmt.Errorf("engagement: write: %w", err)
	}
	return nil
}

// BuildSurfacingID returns the stable key used to upsert a surfacing's
// engagement row. Combines (conv_id, message_id, thought_id) so two
// distinct surfacings of the same thought in different messages get
// distinct rows, and two thoughts in the same message also do.
func BuildSurfacingID(convID, messageID, thoughtID string) string {
	// A short deterministic key is enough — no need for a hash.
	return convID + ":" + messageID + ":" + thoughtID
}

// RecordSurfacing appends a new pending Event for an agent-surfaced
// thought. The SurfacingID is built from (convID, messageID, thoughtID).
// This is called at SaveMessage time in the chat service when a [T-NN]
// marker is found in the assistant reply. Subsequent classification
// rewrites the row in place via MarkSurfacingClassified.
//
// The upsert is by SurfacingID: if an identical surfacing already exists,
// we don't write a duplicate. This makes the call idempotent, which is
// helpful when chat turns retry or streams reconnect.
func RecordSurfacing(supportDir, convID, messageID, thoughtID string, surfacedAt time.Time) error {
	if thoughtID == "" {
		return fmt.Errorf("engagement: empty thought_id")
	}
	if surfacedAt.IsZero() {
		surfacedAt = time.Now().UTC()
	}
	id := BuildSurfacingID(convID, messageID, thoughtID)

	engagementMu.Lock()
	defer engagementMu.Unlock()

	// Check for an existing row with this SurfacingID. If found, skip —
	// RecordSurfacing is idempotent.
	existing, _, err := readAllEventsLocked(supportDir)
	if err != nil {
		return err
	}
	for _, ev := range existing {
		if ev.SurfacingID == id {
			return nil
		}
	}

	ev := Event{
		SurfacingID: id,
		ThoughtID:   thoughtID,
		ConvID:      convID,
		MessageID:   messageID,
		Timestamp:   surfacedAt,
		SurfacedAt:  surfacedAt,
		Signal:      SignalPending,
	}
	blob, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("engagement: marshal: %w", err)
	}

	path := engagementPath(supportDir)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("engagement: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(blob, '\n')); err != nil {
		return fmt.Errorf("engagement: write: %w", err)
	}
	return nil
}

// MarkSurfacingClassified rewrites the pending row identified by
// SurfacingID with a terminal signal, the classifier's confidence +
// reasoning, and the user message that was classified. The rewrite is
// atomic: load → mutate → rewrite-to-temp → rename. All lines without
// the matching SurfacingID are preserved byte-for-byte (after parsing +
// re-marshaling, so they round-trip through Event).
//
// If no row with SurfacingID exists, returns an error (caller can log
// and ignore — this indicates the surfacing detection path didn't run
// for some reason).
func MarkSurfacingClassified(
	supportDir, surfacingID string,
	signal Signal,
	confidence int,
	reasoning string,
	userMessage string,
	classifiedAt time.Time,
) error {
	if !signal.IsTerminal() {
		return fmt.Errorf("engagement: mark classified with non-terminal signal %q", signal)
	}
	if classifiedAt.IsZero() {
		classifiedAt = time.Now().UTC()
	}

	engagementMu.Lock()
	defer engagementMu.Unlock()

	events, _, err := readAllEventsLocked(supportDir)
	if err != nil {
		return err
	}
	found := false
	for i := range events {
		if events[i].SurfacingID == surfacingID {
			events[i].Signal = signal
			events[i].ClassifierConfidence = clampInt(confidence, 0, 100)
			events[i].ClassifierReasoning = truncateString(reasoning, 500)
			events[i].UserMessage = truncateString(userMessage, 500)
			events[i].ClassifiedAt = classifiedAt
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("engagement: surfacing id %q not found", surfacingID)
	}
	return rewriteAllEventsLocked(supportDir, events)
}

// MarkSurfacingIgnoredIfExpired finds any pending surfacings older than
// `cutoff` and marks them as ignored. Returns the number of rows updated.
// Called from the nap cycle — see project_mind_thoughts.md decision C.
// The rewrite is atomic in the same way as MarkSurfacingClassified.
func MarkSurfacingIgnoredIfExpired(supportDir string, cutoff time.Time) (int, error) {
	engagementMu.Lock()
	defer engagementMu.Unlock()

	events, _, err := readAllEventsLocked(supportDir)
	if err != nil {
		return 0, err
	}
	updated := 0
	now := time.Now().UTC()
	for i := range events {
		if events[i].Signal != SignalPending {
			continue
		}
		ts := events[i].SurfacedAt
		if ts.IsZero() {
			ts = events[i].Timestamp
		}
		if ts.Before(cutoff) {
			events[i].Signal = SignalIgnored
			events[i].ClassifiedAt = now
			events[i].ClassifierReasoning = "decayed: no follow-up before nap window"
			updated++
		}
	}
	if updated == 0 {
		return 0, nil
	}
	if err := rewriteAllEventsLocked(supportDir, events); err != nil {
		return 0, err
	}
	return updated, nil
}

// FindPendingSurfacingInConv returns the most recent pending surfacing
// for the given conversation within `window`, or nil if none exists.
// Used by the classifier to decide whether the incoming user turn needs
// to be classified against a recent surfacing.
//
// Returns the Event by value so callers can safely compare/consume it
// without worrying about the sidecar being rewritten out from under them.
func FindPendingSurfacingInConv(supportDir, convID string, window time.Duration) (*Event, error) {
	engagementMu.Lock()
	defer engagementMu.Unlock()

	events, _, err := readAllEventsLocked(supportDir)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-window)
	var found *Event
	for i := range events {
		ev := &events[i]
		if ev.Signal != SignalPending {
			continue
		}
		if ev.ConvID != convID {
			continue
		}
		ts := ev.SurfacedAt
		if ts.IsZero() {
			ts = ev.Timestamp
		}
		if ts.Before(cutoff) {
			continue
		}
		// Most recent wins — keep scanning to the end.
		if found == nil || ts.After(found.SurfacedAt) {
			cp := *ev
			found = &cp
		}
	}
	return found, nil
}

// readAllEventsLocked returns every event in the sidecar, regardless of
// timestamp. Caller must already hold engagementMu. Used by the upsert
// operations to load the full file before rewriting. Skipped malformed
// lines are counted but not reported — the rewrite path can't preserve
// lines it can't parse, so we accept the small loss.
func readAllEventsLocked(supportDir string) ([]Event, int, error) {
	path := engagementPath(supportDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("engagement: open: %w", err)
	}
	defer f.Close()

	var events []Event
	skipped := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			skipped++
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return events, skipped, fmt.Errorf("engagement: scan: %w", err)
	}
	return events, skipped, nil
}

// rewriteAllEventsLocked writes the full event slice back to the sidecar
// atomically via a temp file + rename. Caller must already hold
// engagementMu.
func rewriteAllEventsLocked(supportDir string, events []Event) error {
	path := engagementPath(supportDir)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("engagement: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	bw := bufioNewWriter(tmp)
	for _, ev := range events {
		blob, err := json.Marshal(ev)
		if err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("engagement: marshal during rewrite: %w", err)
		}
		if _, err := bw.Write(append(blob, '\n')); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("engagement: write during rewrite: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("engagement: flush during rewrite: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("engagement: close during rewrite: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("engagement: chmod during rewrite: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("engagement: rename during rewrite: %w", err)
	}
	return nil
}

// bufioNewWriter wraps bufio.NewWriter so the call site stays one line.
// A thin local shim; lets us swap buffered strategies later without
// touching the op code above.
func bufioNewWriter(w *os.File) *bufio.Writer {
	return bufio.NewWriter(w)
}

// clampInt caps n to [lo, hi].
func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// truncateString returns the first n runes of s.
func truncateString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// RecentEvents returns all engagement events with timestamps on or after
// `since`. Malformed lines are skipped and the number of skipped lines is
// returned in the second value so callers can log degradation without
// failing. A missing file returns an empty slice, not an error.
func RecentEvents(supportDir string, since time.Time) ([]Event, int, error) {
	engagementMu.Lock()
	defer engagementMu.Unlock()

	path := engagementPath(supportDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("engagement: open: %w", err)
	}
	defer f.Close()

	var events []Event
	skipped := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // support up to 1MB per line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			skipped++
			continue
		}
		if ev.Timestamp.Before(since) {
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return events, skipped, fmt.Errorf("engagement: scan: %w", err)
	}
	return events, skipped, nil
}

// CountByThought returns a map of thought_id → signal → count for the given
// events. Used by the nap to apply the discard rules (2 negatives, 3 ignores)
// without re-scanning the event slice repeatedly.
//
// Pending events are NOT counted — they are intermediate states and
// including them would pollute the keep test with "ignored-like" entries
// for thoughts the user hasn't had a chance to react to yet. The nap
// cycle separately calls MarkSurfacingIgnoredIfExpired before reading
// events, which promotes stale pendings into ignored so this counter
// still sees them.
func CountByThought(events []Event) map[string]map[Signal]int {
	out := make(map[string]map[Signal]int)
	for _, ev := range events {
		if !ev.Signal.IsTerminal() {
			continue
		}
		if _, ok := out[ev.ThoughtID]; !ok {
			out[ev.ThoughtID] = make(map[Signal]int)
		}
		out[ev.ThoughtID][ev.Signal]++
	}
	return out
}

// ShouldDiscard returns true if the given counts meet either discard rule:
// 2 negatives OR 3 ignores. Exposed so the nap prompt logic and tests share
// the same threshold definition. Phase 2+ can swap these constants with
// values from RuntimeConfigSnapshot.
var (
	DiscardOnNegatives = 2
	DiscardOnIgnores   = 3
)

// ShouldDiscard returns true if the given thought's engagement counts meet
// either discard threshold.
func ShouldDiscard(counts map[Signal]int) bool {
	if counts[SignalNegative] >= DiscardOnNegatives {
		return true
	}
	if counts[SignalIgnored] >= DiscardOnIgnores {
		return true
	}
	return false
}
