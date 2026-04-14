package mind

// dispatcher.go is the action gate for the mind-thoughts subsystem. After a
// nap has curated the THOUGHTS section, the dispatcher walks the current
// state and decides what to do with each thought:
//
//   1. If CanAutoExecute(score, class) → run the skill (if the thought has
//      an action), enqueue the result to pending-greetings.json, emit
//      auto_execute_{attempted,succeeded,failed} telemetry.
//
//   2. If ShouldPropose(score, class) → route to the approvals module with
//      source="thought", emit approval_proposed telemetry.
//
//   3. Otherwise → leave it alone. The thought carries until the next nap.
//
// Rate limits are belt-and-braces on top of the structural safety ceiling:
// the ceiling guarantees only read-class thoughts can reach 95, and the
// rate limits bound how many reads can auto-execute per nap and per day.
//
// The dispatcher is wired as an optional dependency on NapDeps. If no
// dispatcher is registered, RunNap skips the dispatch phase entirely —
// phase 4 can be dark-launched by leaving the wiring unset.
//
// The dispatcher does NOT hold the MIND.md lock while it runs skills.
// Skills can take seconds and we don't want to block unrelated writers.
// It DOES hold the mind lock briefly to re-read the thoughts list before
// dispatching, so it sees the post-nap state the apply produced.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/mind/thoughts"
)

// SkillExecutor is the narrow interface the dispatcher needs from the skills
// registry. Injected rather than imported so the mind package stays free of
// a dependency on internal/skills (which would create a cycle through the
// chat service path).
type SkillExecutor interface {
	// Execute runs a skill action with the given JSON-encoded arguments.
	// The returned string is the tool result text; an error indicates the
	// skill failed to run or returned an error.
	Execute(ctx context.Context, actionID string, args json.RawMessage) (result string, err error)
	// NeedsApproval reports whether the action would normally require user
	// approval. The dispatcher uses this as a second belt-and-braces check
	// alongside the structural score ceiling.
	NeedsApproval(actionID string) bool
}

// ApprovalProposer is the narrow interface the dispatcher needs to route
// a thought-sourced proposal through the approvals system. Injected so we
// don't import the approvals module here.
type ApprovalProposer interface {
	// ProposeFromThought creates a draft approval record for the given
	// thought's proposed action. The implementation is responsible for
	// persisting the record and tagging it with source="thought" so the
	// approvals UI can render it differently. Returns the approval id.
	ProposeFromThought(thoughtID, body, skillID string, args json.RawMessage, provenance string) (approvalID string, err error)
}

// GreetingQueuer writes an acted-on-thought result to the pending-greetings
// queue that the phase 5 chat-greeting endpoint drains. Kept as an injected
// interface so phase 4 and phase 5 wire together cleanly.
type GreetingQueuer interface {
	EnqueueGreeting(entry GreetingEntry) error
}

// GreetingEntry is one queued result awaiting the next chat-open greeting.
// Persisted to pending-greetings.json in phase 5; exposed here so the
// dispatcher can construct them.
type GreetingEntry struct {
	ThoughtID  string    `json:"thought_id"`
	Body       string    `json:"body"`
	SkillID    string    `json:"skill_id"`
	Result     string    `json:"result"`
	Provenance string    `json:"provenance"`
	ExecutedAt time.Time `json:"executed_at"`
	DurationMs int64     `json:"duration_ms"`
}

// Dispatcher is the action-gate implementation. One per process. Holds its
// injected dependencies plus a small in-memory rate counter for per-day
// auto-execute caps.
type Dispatcher struct {
	supportDir string
	executor   SkillExecutor
	approvals  ApprovalProposer
	greetings  GreetingQueuer
	telemetry  *telemetry.Emitter

	mu              sync.Mutex
	dailyCount      int
	dailyCountReset time.Time
}

// NewDispatcher constructs a Dispatcher. Any dependency may be nil — the
// dispatcher degrades gracefully, logging the reason it couldn't act and
// emitting telemetry so we can see what's failing in practice.
func NewDispatcher(supportDir string, executor SkillExecutor, approvals ApprovalProposer, greetings GreetingQueuer, tel *telemetry.Emitter) *Dispatcher {
	return &Dispatcher{
		supportDir: supportDir,
		executor:   executor,
		approvals:  approvals,
		greetings:  greetings,
		telemetry:  tel,
	}
}

// DispatchResult summarizes what the dispatcher did during one invocation.
// Returned from Dispatch so the caller (RunNap) can fold it into the
// NapResult for the HTTP response.
type DispatchResult struct {
	AutoExecuted int      `json:"auto_executed"`
	Proposed     int      `json:"proposed"`
	Skipped      int      `json:"skipped"`
	RateLimited  int      `json:"rate_limited"`
	Errors       []string `json:"errors,omitempty"`
	AutoExecIDs  []string `json:"auto_exec_ids,omitempty"`
	ProposedIDs  []string `json:"proposed_ids,omitempty"`
}

// dispatcherMaxPerNap and dispatcherMaxPerDay are defaults used when the
// config snapshot has zero values (e.g. legacy configs without the fields).
const (
	dispatcherDefaultMaxPerNap = 1
	dispatcherDefaultMaxPerDay = 3
	dispatcherSkillTimeout     = 45 * time.Second
)

// Dispatch walks the current thoughts list and acts on any that cross the
// auto-execute or propose thresholds. `cfg` carries the rate limits; `convID`
// is used for telemetry correlation. Never holds the mind lock while running
// a skill. Errors on individual thoughts are captured in DispatchResult but
// do not stop the whole dispatch — one bad skill should not block the rest.
func (d *Dispatcher) Dispatch(ctx context.Context, list []thoughts.Thought, maxPerNap, maxPerDay int, convID string) DispatchResult {
	result := DispatchResult{}
	if d == nil {
		return result
	}
	if maxPerNap <= 0 {
		maxPerNap = dispatcherDefaultMaxPerNap
	}
	if maxPerDay <= 0 {
		maxPerDay = dispatcherDefaultMaxPerDay
	}

	execCount := 0
	for _, t := range list {
		// Skip thoughts that can't or shouldn't become actions.
		autoExec := thoughts.CanAutoExecute(t.Score, t.Class)
		propose := thoughts.ShouldPropose(t.Score, t.Class)

		if !autoExec && !propose {
			result.Skipped++
			continue
		}
		// Both paths require a concrete ProposedAction — pure reflection
		// thoughts (no skill to call) are carried but never acted on.
		if t.Action == nil || t.Action.SkillID == "" {
			result.Skipped++
			continue
		}

		if autoExec {
			if execCount >= maxPerNap {
				result.RateLimited++
				d.telemetry.Emit(telemetry.KindAutoExecuteAttempted, t.ID, convID, map[string]any{
					"outcome": "rate_limited_per_nap",
					"skill":   t.Action.SkillID,
					"score":   t.Score,
					"class":   string(t.Class),
				})
				continue
			}
			if !d.tryIncrementDaily(maxPerDay) {
				result.RateLimited++
				d.telemetry.Emit(telemetry.KindAutoExecuteAttempted, t.ID, convID, map[string]any{
					"outcome": "rate_limited_per_day",
					"skill":   t.Action.SkillID,
					"score":   t.Score,
					"class":   string(t.Class),
				})
				continue
			}
			if err := d.autoExecute(ctx, t, convID); err != nil {
				result.Errors = append(result.Errors, t.ID+": "+err.Error())
				continue
			}
			execCount++
			result.AutoExecuted++
			result.AutoExecIDs = append(result.AutoExecIDs, t.ID)
			continue
		}

		// propose path — needs approval before running.
		approvalID, err := d.proposeApproval(t)
		if err != nil {
			result.Errors = append(result.Errors, t.ID+": "+err.Error())
			continue
		}
		result.Proposed++
		result.ProposedIDs = append(result.ProposedIDs, t.ID)
		d.telemetry.Emit(telemetry.KindApprovalProposed, t.ID, convID, map[string]any{
			"outcome":     "applied",
			"skill":       t.Action.SkillID,
			"score":       t.Score,
			"class":       string(t.Class),
			"approval_id": approvalID,
		})
	}
	return result
}

// tryIncrementDaily enforces the per-day auto-execute cap using an in-memory
// counter that resets every 24 hours. Returns true if the increment was
// allowed. Persisting this counter to disk is overkill for v1 — at worst a
// daemon restart gives us one extra auto-execute, which is acceptable given
// the read-class-only ceiling.
func (d *Dispatcher) tryIncrementDaily(maxPerDay int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	if d.dailyCountReset.IsZero() || now.Sub(d.dailyCountReset) >= 24*time.Hour {
		d.dailyCount = 0
		d.dailyCountReset = now
	}
	if d.dailyCount >= maxPerDay {
		return false
	}
	d.dailyCount++
	return true
}

// autoExecute runs a thought's proposed action, enqueues the result, and
// emits telemetry. Returns an error only for unrecoverable failures — skill
// execution errors are captured in telemetry but reported to the caller as
// a soft error so the rest of the dispatch continues.
func (d *Dispatcher) autoExecute(ctx context.Context, t thoughts.Thought, convID string) error {
	if d.executor == nil {
		d.telemetry.Emit(telemetry.KindAutoExecuteFailed, t.ID, convID, map[string]any{
			"error": "no executor configured",
			"skill": t.Action.SkillID,
		})
		return fmt.Errorf("no executor configured")
	}

	// Defense in depth: the skills registry's own approval check should
	// also return false for any skill we'd auto-execute (because only
	// read-class can reach 95 by the score ceiling). If it disagrees,
	// trust the registry and bail.
	if d.executor.NeedsApproval(t.Action.SkillID) {
		d.telemetry.Emit(telemetry.KindAutoExecuteFailed, t.ID, convID, map[string]any{
			"error": "skill requires approval but thought cleared auto-execute threshold",
			"skill": t.Action.SkillID,
		})
		return fmt.Errorf("skill requires approval")
	}

	d.telemetry.Emit(telemetry.KindAutoExecuteAttempted, t.ID, convID, map[string]any{
		"outcome": "running",
		"skill":   t.Action.SkillID,
		"score":   t.Score,
		"class":   string(t.Class),
	})

	argsJSON, err := json.Marshal(t.Action.Args)
	if err != nil {
		argsJSON = []byte("{}")
	}

	execCtx, cancel := context.WithTimeout(ctx, dispatcherSkillTimeout)
	defer cancel()

	start := time.Now()
	resultText, execErr := d.executor.Execute(execCtx, t.Action.SkillID, argsJSON)
	durationMs := time.Since(start).Milliseconds()

	if execErr != nil {
		d.telemetry.Emit(telemetry.KindAutoExecuteFailed, t.ID, convID, map[string]any{
			"error":       execErr.Error(),
			"skill":       t.Action.SkillID,
			"duration_ms": durationMs,
		})
		logstore.Write("warn",
			fmt.Sprintf("mind dispatcher: auto-execute failed for %s: %v", t.Action.SkillID, execErr),
			map[string]string{"thought": t.ID})
		return execErr
	}

	// Truncate the result before it goes into the queue — we don't want a
	// megabyte of scraped HTML sitting in pending-greetings.json.
	if len(resultText) > 8000 {
		resultText = resultText[:8000] + "… (truncated)"
	}

	d.telemetry.Emit(telemetry.KindAutoExecuteSucceeded, t.ID, convID, map[string]any{
		"skill":       t.Action.SkillID,
		"duration_ms": durationMs,
		"result_len":  len(resultText),
	})

	entry := GreetingEntry{
		ThoughtID:  t.ID,
		Body:       t.Body,
		SkillID:    t.Action.SkillID,
		Result:     resultText,
		Provenance: t.Provenance,
		ExecutedAt: time.Now().UTC(),
		DurationMs: durationMs,
	}
	if d.greetings != nil {
		if qerr := d.greetings.EnqueueGreeting(entry); qerr != nil {
			logstore.Write("warn",
				"mind dispatcher: greeting enqueue failed: "+qerr.Error(),
				map[string]string{"thought": t.ID})
		}
	} else {
		// Phase 4 ship without phase 5: fall back to writing the pending
		// queue file directly so results are not lost when phase 5 lands.
		if werr := appendPendingGreetingFile(d.supportDir, entry); werr != nil {
			logstore.Write("warn",
				"mind dispatcher: fallback greeting write failed: "+werr.Error(),
				map[string]string{"thought": t.ID})
		}
	}
	return nil
}

// proposeApproval routes a thought's proposed action through the approvals
// system. Telemetry is emitted by the caller after this returns so the
// approval_id is included.
func (d *Dispatcher) proposeApproval(t thoughts.Thought) (string, error) {
	if d.approvals == nil {
		return "", fmt.Errorf("no approval proposer configured")
	}
	argsJSON, err := json.Marshal(t.Action.Args)
	if err != nil {
		argsJSON = []byte("{}")
	}
	return d.approvals.ProposeFromThought(t.ID, t.Body, t.Action.SkillID, argsJSON, t.Provenance)
}

// pendingGreetingsFile is the on-disk fallback queue used when no
// GreetingQueuer is injected (phase 4 before phase 5 lands).
const pendingGreetingsFile = "pending-greetings.json"

var pendingGreetingsMu sync.Mutex

// AppendPendingGreeting writes one entry to the pending-greetings.json file
// with an atomic read-append-write. Exported so the chat package can adapt
// it to the GreetingQueuer interface without duplicating the file plumbing.
func AppendPendingGreeting(supportDir string, entry GreetingEntry) error {
	return appendPendingGreetingFile(supportDir, entry)
}

// appendPendingGreetingFile writes one entry to the pending greetings file
// with an atomic read-append-write. Used by autoExecute when no injected
// queuer is available.
func appendPendingGreetingFile(supportDir string, entry GreetingEntry) error {
	pendingGreetingsMu.Lock()
	defer pendingGreetingsMu.Unlock()

	path := filepath.Join(supportDir, pendingGreetingsFile)
	var queue []GreetingEntry
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &queue)
	}
	queue = append(queue, entry)
	blob, err := json.MarshalIndent(queue, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, blob, 0o600)
}

// LoadPendingGreetings reads the current queue. Exported so phase 5 / the
// HTTP endpoint can drain it. Returns an empty slice if the file is missing.
func LoadPendingGreetings(supportDir string) ([]GreetingEntry, error) {
	pendingGreetingsMu.Lock()
	defer pendingGreetingsMu.Unlock()
	path := filepath.Join(supportDir, pendingGreetingsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var queue []GreetingEntry
	if err := json.Unmarshal(data, &queue); err != nil {
		return nil, err
	}
	return queue, nil
}

// ClearPendingGreetings empties the queue file. Called by the phase 5
// greeting endpoint after it has delivered the greeting message.
func ClearPendingGreetings(supportDir string) error {
	pendingGreetingsMu.Lock()
	defer pendingGreetingsMu.Unlock()
	path := filepath.Join(supportDir, pendingGreetingsFile)
	return atomicWrite(path, []byte("[]"), 0o600)
}
