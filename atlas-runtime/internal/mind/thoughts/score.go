package thoughts

// score.go implements the structural safety ceiling for nap-thought actions.
//
// Sandboxed mode keeps the original hard ceiling: only read thoughts can
// mathematically clear the auto-execute threshold. Unleashed mode opens a
// narrow second lane for background autonomy by allowing local_write and
// external_side_effect thoughts to clear the threshold too, while still
// keeping destructive_local and send_publish_delete structurally below it.

// AutoExecuteThreshold is the minimum score at which the dispatcher will
// auto-execute a thought's proposed action without user approval. Exposed as
// a var (not a const) so phase 2+ can override it from RuntimeConfigSnapshot.
var AutoExecuteThreshold = 95

// ProposeThreshold is the minimum score at which the dispatcher routes a
// thought's proposed action to the approvals screen. Below this, a thought
// is carried in MIND.md but never becomes an action. Exposed as a var for
// runtime config override.
var ProposeThreshold = 80

// UnleashedAutoExecuteEnabled relaxes the background-thought ceiling so Atlas
// can auto-execute selected non-read actions during naps when runtime autonomy
// is explicitly unleashed. Sandboxed mode keeps the original read-only ceiling.
var UnleashedAutoExecuteEnabled bool

// safetyMultipliers maps each ActionClass to the fraction its max score is
// capped at. read is 1.00 (no ceiling); every other class is strictly below
// the value needed to hit 95 with confidence=value=100.
//
// Math check at compile time via TestSafetyCeiling in score_test.go.
var sandboxedSafetyMultipliers = map[ActionClass]float64{
	ClassRead:               1.00, // 100 × 100 × 1.00 / 100 = 100 — can clear 95
	ClassLocalWrite:         0.94, // 100 × 100 × 0.94 / 100 = 94  — cannot clear 95
	ClassDestructiveLocal:   0.93, // 100 × 100 × 0.93 / 100 = 93  — cannot clear 95
	ClassExternalSideEffect: 0.93, // 100 × 100 × 0.93 / 100 = 93  — cannot clear 95
	ClassSendPublishDelete:  0.90, // 100 × 100 × 0.90 / 100 = 90  — cannot clear 95
}

var unleashedSafetyMultipliers = map[ActionClass]float64{
	ClassRead:               1.00, // may auto-execute
	ClassLocalWrite:         1.00, // may auto-execute in unleashed mode
	ClassDestructiveLocal:   0.93, // still cannot auto-execute in background
	ClassExternalSideEffect: 1.00, // may auto-execute in unleashed mode
	ClassSendPublishDelete:  0.90, // explicit outbound send/publish still capped
}

// SafetyMultiplier returns the multiplier for a given action class. Unknown
// classes return 0.0 — meaning their computed score is always 0, which is
// the safest possible fallback for a class the system doesn't recognize.
func SafetyMultiplier(class ActionClass) float64 {
	if UnleashedAutoExecuteEnabled {
		return unleashedSafetyMultipliers[class]
	}
	return sandboxedSafetyMultipliers[class]
}

// ComputeScore returns the final score for a thought based on the model's
// proposed confidence and value and its action class. Inputs are clamped to
// [0, 100] before the calculation so malformed model outputs cannot produce
// scores above 100.
//
// Formula:
//
//	score = (clamp(confidence) × clamp(value) × safety_multiplier[class]) / 100
//
// For class=read:                max score = 100
// For class=local_write:         max score = 94
// For class=destructive_local:   max score = 93
// For class=external_side_effect: max score = 93
// For class=send_publish_delete: max score = 90
// For unknown class:             max score = 0
//
// The return value is always an integer in [0, 100].
func ComputeScore(confidence, value int, class ActionClass) int {
	c := clamp(confidence)
	v := clamp(value)
	mult := SafetyMultiplier(class)
	raw := float64(c*v) * mult / 100.0
	// Round to nearest integer, then clamp to [0, 100] as a final safety net.
	score := int(raw + 0.5)
	return clamp(score)
}

// clamp confines an int to [0, 100]. Inline-safe helper.
func clamp(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// MaxScoreForClass returns the ceiling for a given class — the score produced
// by confidence=100, value=100. Used in tests to verify the structural ceiling
// is intact, and available to callers that want to reason about class limits.
func MaxScoreForClass(class ActionClass) int {
	return ComputeScore(100, 100, class)
}

// CanAutoExecute returns true if a thought at score s with action class c is
// allowed to auto-execute. Sandboxed mode only permits read-class thoughts.
// Unleashed mode also permits local writes and external side effects, while
// still excluding destructive local actions and explicit send/publish actions.
func CanAutoExecute(s int, c ActionClass) bool {
	if s < AutoExecuteThreshold {
		return false
	}
	if UnleashedAutoExecuteEnabled {
		switch c {
		case ClassRead, ClassLocalWrite, ClassExternalSideEffect:
			return true
		default:
			return false
		}
	}
	return c == ClassRead
}

// ShouldPropose returns true if a thought at score s should be routed to the
// approvals screen. Score is above the propose threshold and it did not
// already qualify for auto-execute.
func ShouldPropose(s int, c ActionClass) bool {
	if CanAutoExecute(s, c) {
		return false
	}
	return s >= ProposeThreshold
}
