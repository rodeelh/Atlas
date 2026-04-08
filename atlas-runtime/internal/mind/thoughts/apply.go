package thoughts

// apply.go applies a batch of ops (from a nap or dream envelope) to the
// current THOUGHTS list and returns the new list. Pure function — no I/O.
//
// Invariants the applier enforces:
//
//   - Code owns the Score field. For OpAdd and OpUpdate, the score is always
//     recomputed from Confidence, Value, and Class via ComputeScore. The
//     model's self-reported score (if present) is discarded.
//
//   - Code owns timestamps. Created and Reinforced are set by the applier
//     using the provided `now`, not by the model.
//
//   - OpAdd auto-generates a thought id if the model didn't provide one. The
//     generator walks existing ids and picks the next "T-NN" sequentially.
//
//   - OpDiscard of a missing id is a no-op (not an error). Naps that propose
//     discards based on stale state should degrade gracefully rather than
//     crashing the whole batch.
//
//   - OpMerge requires at least two existing ids and at least one to actually
//     exist. The merged thought inherits the earliest Created timestamp and
//     the most recent Reinforced timestamp from the inputs. Confidence and
//     value default to the maximum of the inputs if the op doesn't specify
//     them explicitly.
//
//   - The applier never mutates the input slice. It returns a fresh slice.
//
//   - A Result is returned alongside so telemetry can record per-op outcomes
//     without the caller re-deriving them. Each Op gets a corresponding
//     OpResult entry in the same order.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpResult records what happened when an op was applied. Telemetry emits one
// of these per op. "Outcome" is always one of "applied", "skipped_noop",
// "rejected_invalid", or "rejected_missing".
type OpResult struct {
	Op        Op     `json:"op"`
	Outcome   string `json:"outcome"`
	ThoughtID string `json:"thought_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Apply takes the current thoughts list and a batch of ops and returns the
// new list, a list of per-op results, and the first error encountered (if
// any). Individual op failures do not stop the batch — invalid ops are
// rejected and recorded in results, valid ones are applied. The returned
// error is non-nil only if a validation failure made the whole batch
// incoherent (e.g. duplicate generated ids, impossible state after ops).
//
// `now` is the timestamp used for any Created/Reinforced updates. Passed in
// instead of called via time.Now() so tests can pin it.
func Apply(current []Thought, ops []Op, now time.Time) ([]Thought, []OpResult, error) {
	// Copy the current list so we never mutate the caller's slice.
	out := make([]Thought, len(current))
	for i, t := range current {
		out[i] = t.Clone()
	}

	results := make([]OpResult, 0, len(ops))

	for _, op := range ops {
		res := OpResult{Op: op}
		switch op.Kind {
		case OpAdd:
			newT, err := buildAdded(op, out, now)
			if err != nil {
				res.Outcome = "rejected_invalid"
				res.Error = err.Error()
				results = append(results, res)
				continue
			}
			out = append(out, newT)
			res.Outcome = "applied"
			res.ThoughtID = newT.ID
			results = append(results, res)

		case OpUpdate:
			idx := findThought(out, op.ID)
			if idx == -1 {
				res.Outcome = "rejected_missing"
				res.ThoughtID = op.ID
				results = append(results, res)
				continue
			}
			updated, err := applyUpdate(out[idx], op, now)
			if err != nil {
				res.Outcome = "rejected_invalid"
				res.Error = err.Error()
				res.ThoughtID = op.ID
				results = append(results, res)
				continue
			}
			out[idx] = updated
			res.Outcome = "applied"
			res.ThoughtID = op.ID
			results = append(results, res)

		case OpReinforce:
			idx := findThought(out, op.ID)
			if idx == -1 {
				res.Outcome = "rejected_missing"
				res.ThoughtID = op.ID
				results = append(results, res)
				continue
			}
			out[idx].Reinforced = now
			res.Outcome = "applied"
			res.ThoughtID = op.ID
			results = append(results, res)

		case OpDiscard:
			idx := findThought(out, op.ID)
			if idx == -1 {
				// Silent no-op — discard of missing id is graceful.
				res.Outcome = "skipped_noop"
				res.ThoughtID = op.ID
				results = append(results, res)
				continue
			}
			out = append(out[:idx], out[idx+1:]...)
			res.Outcome = "applied"
			res.ThoughtID = op.ID
			results = append(results, res)

		case OpMerge:
			merged, removed, err := applyMerge(out, op, now)
			if err != nil {
				res.Outcome = "rejected_invalid"
				res.Error = err.Error()
				results = append(results, res)
				continue
			}
			out = removed
			out = append(out, merged)
			res.Outcome = "applied"
			res.ThoughtID = merged.ID
			results = append(results, res)

		default:
			res.Outcome = "rejected_invalid"
			res.Error = fmt.Sprintf("unknown op kind: %q", op.Kind)
			results = append(results, res)
		}
	}

	// Final sanity pass — every thought in the output must pass Validate.
	// If any don't, that's a bug we want to surface loudly.
	for _, t := range out {
		if err := t.Validate(); err != nil {
			return out, results, fmt.Errorf("post-apply validation: %w", err)
		}
	}
	return out, results, nil
}

// findThought returns the index of a thought by id, or -1 if not found.
func findThought(list []Thought, id string) int {
	for i, t := range list {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// nextID produces the next unused "T-NN" id by walking existing ids and
// picking max+1. If no thoughts exist, returns "T-01".
func nextID(list []Thought) string {
	maxN := 0
	for _, t := range list {
		if !strings.HasPrefix(t.ID, "T-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(t.ID, "T-"))
		if err != nil {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("T-%02d", maxN+1)
}

func buildAdded(op Op, current []Thought, now time.Time) (Thought, error) {
	if strings.TrimSpace(op.Body) == "" {
		return Thought{}, fmt.Errorf("add: empty body")
	}
	if !op.Class.Valid() {
		return Thought{}, fmt.Errorf("add: invalid class %q", op.Class)
	}
	id := op.ID
	if id == "" {
		id = nextID(current)
	}
	t := Thought{
		ID:          id,
		Body:        strings.TrimSpace(op.Body),
		Confidence:  clamp(op.Confidence),
		Value:       clamp(op.Value),
		Class:       op.Class,
		Created:     now,
		Reinforced:  now,
		SurfacedN:   0,
		SurfacedMax: 2,
		Source:      strings.TrimSpace(op.Source),
		Provenance:  strings.TrimSpace(op.Provenance),
		Action:      op.Action,
	}
	t.Score = ComputeScore(t.Confidence, t.Value, t.Class)
	if err := t.Validate(); err != nil {
		return Thought{}, err
	}
	return t, nil
}

func applyUpdate(existing Thought, op Op, now time.Time) (Thought, error) {
	updated := existing.Clone()
	if strings.TrimSpace(op.Body) != "" {
		updated.Body = strings.TrimSpace(op.Body)
	}
	// Confidence, value, class can be updated; score is always recomputed.
	// We distinguish "unset" from "set to 0" by checking against the spec
	// default of 0 — an update that wants to pin confidence or value to 0
	// must use a full replacement via discard+add. This is intentionally
	// simple: updates should be about body refinement and confidence bumps.
	if op.Confidence > 0 {
		updated.Confidence = clamp(op.Confidence)
	}
	if op.Value > 0 {
		updated.Value = clamp(op.Value)
	}
	if op.Class != "" {
		if !op.Class.Valid() {
			return existing, fmt.Errorf("update: invalid class %q", op.Class)
		}
		updated.Class = op.Class
	}
	updated.Score = ComputeScore(updated.Confidence, updated.Value, updated.Class)
	updated.Reinforced = now
	if op.Provenance != "" {
		updated.Provenance = strings.TrimSpace(op.Provenance)
	}
	if op.Action != nil {
		updated.Action = op.Action
	}
	return updated, nil
}

func applyMerge(current []Thought, op Op, now time.Time) (Thought, []Thought, error) {
	if len(op.IDs) < 2 {
		return Thought{}, current, fmt.Errorf("merge: need at least two ids, got %d", len(op.IDs))
	}
	if strings.TrimSpace(op.IntoBody) == "" {
		return Thought{}, current, fmt.Errorf("merge: empty into_body")
	}

	// Collect the inputs and the indices we'll remove.
	var inputs []Thought
	var removeIdxs []int
	for _, id := range op.IDs {
		idx := findThought(current, id)
		if idx == -1 {
			continue
		}
		inputs = append(inputs, current[idx])
		removeIdxs = append(removeIdxs, idx)
	}
	if len(inputs) == 0 {
		return Thought{}, current, fmt.Errorf("merge: none of the input ids exist: %v", op.IDs)
	}

	// Synthesize the merged thought. Earliest created, latest reinforced,
	// max confidence and value, most-restrictive class (any merge that
	// combines read + side_effect becomes side_effect).
	earliestCreated := inputs[0].Created
	latestReinforced := inputs[0].Reinforced
	maxConf := inputs[0].Confidence
	maxVal := inputs[0].Value
	mostRestrictiveClass := inputs[0].Class
	var sources []string
	var provenances []string
	for _, t := range inputs {
		if t.Created.Before(earliestCreated) {
			earliestCreated = t.Created
		}
		if t.Reinforced.After(latestReinforced) {
			latestReinforced = t.Reinforced
		}
		if t.Confidence > maxConf {
			maxConf = t.Confidence
		}
		if t.Value > maxVal {
			maxVal = t.Value
		}
		if classRank(t.Class) > classRank(mostRestrictiveClass) {
			mostRestrictiveClass = t.Class
		}
		if t.Source != "" {
			sources = append(sources, t.Source)
		}
		if t.Provenance != "" {
			provenances = append(provenances, t.Provenance)
		}
	}

	// Apply caller overrides.
	if op.Confidence > 0 {
		maxConf = clamp(op.Confidence)
	}
	if op.Value > 0 {
		maxVal = clamp(op.Value)
	}
	if op.Class != "" && op.Class.Valid() {
		mostRestrictiveClass = op.Class
	}

	// Remove the merged inputs from the current list. Sort indices descending
	// so removal doesn't shift later indices.
	sort.Sort(sort.Reverse(sort.IntSlice(removeIdxs)))
	remaining := make([]Thought, 0, len(current))
	remaining = append(remaining, current...)
	for _, idx := range removeIdxs {
		remaining = append(remaining[:idx], remaining[idx+1:]...)
	}

	// Mint the merged thought with a fresh id so downstream references are
	// unambiguous about which entry the merge produced.
	merged := Thought{
		ID:          nextID(remaining),
		Body:        strings.TrimSpace(op.IntoBody),
		Confidence:  maxConf,
		Value:       maxVal,
		Class:       mostRestrictiveClass,
		Created:     earliestCreated,
		Reinforced:  latestReinforced,
		SurfacedN:   0,
		SurfacedMax: 2,
		Source:      strings.Join(sources, ","),
		Provenance:  fmt.Sprintf("merged from %s: %s", strings.Join(op.IDs, ","), strings.Join(provenances, "; ")),
	}
	if !merged.Reinforced.IsZero() {
		// Bump reinforced to `now` — a merge is an act of curation, not just
		// a passive inheritance. This ensures the merged thought doesn't
		// immediately decay because its inputs had stale reinforced fields.
		merged.Reinforced = now
	}
	merged.Score = ComputeScore(merged.Confidence, merged.Value, merged.Class)
	if err := merged.Validate(); err != nil {
		return Thought{}, current, fmt.Errorf("merge: invalid result: %w", err)
	}
	return merged, remaining, nil
}

// classRank gives each ActionClass a numeric "dangerousness" rank. Used when
// merging thoughts with different classes — we always inherit the highest
// rank to err on the side of caution. This is important: merging a read and
// an external_side_effect must not produce a read.
func classRank(c ActionClass) int {
	switch c {
	case ClassRead:
		return 0
	case ClassLocalWrite:
		return 1
	case ClassDestructiveLocal:
		return 2
	case ClassExternalSideEffect:
		return 3
	case ClassSendPublishDelete:
		return 4
	}
	return 99 // unknown class — most restrictive
}
