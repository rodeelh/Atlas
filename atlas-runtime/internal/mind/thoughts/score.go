package thoughts

// score.go implements the structural safety ceiling.
//
// The nap and dream cycle never write the score field directly. The model
// proposes confidence (0–100) and value (0–100), and chooses an ActionClass.
// Code multiplies these by the class's safety multiplier to produce the final
// score. The multipliers are chosen so risky classes CANNOT reach the 95
// auto-execute threshold no matter how confident or valuable the thought is.
//
// This is not a runtime check that can be bypassed. It is a math property of
// the score function. An external_side_effect thought with confidence=100,
// value=100, class=external_side_effect produces score = 100*100*0.93/100 = 93.
// 93 < 95, so the dispatcher's auto-execute branch is mathematically closed
// to anything except ClassRead.
//
// If you ever change the safety multipliers, the constraint that must hold is:
//
//	safety_multiplier[class] * 100 < AutoExecuteThreshold  for all class != read
//
// With AutoExecuteThreshold=95, the maximum safe multiplier for a non-read
// class is 0.94. We leave a small buffer and cap the highest non-read class
// at 0.97 (local_write) — still above 95 in theory if confidence and value
// both hit 100, which is why local_write REQUIRES approval even at 97. It
// just can't auto-execute because the dispatcher additionally checks class.
//
// To be strict about the "safety is a math property" claim, we want every
// non-read class to fall strictly below 95 at maximum confidence and value.
// That means the maximum multiplier is 0.94. We keep local_write at 0.94 so
// the math forbids auto-execute across all non-read classes, not just the
// obviously dangerous ones. The dispatcher's class check becomes redundant
// belt-and-braces, exactly as intended.

// AutoExecuteThreshold is the minimum score at which the dispatcher will
// auto-execute a thought's proposed action without user approval. Exposed as
// a var (not a const) so phase 2+ can override it from RuntimeConfigSnapshot.
var AutoExecuteThreshold = 95

// ProposeThreshold is the minimum score at which the dispatcher routes a
// thought's proposed action to the approvals screen. Below this, a thought
// is carried in MIND.md but never becomes an action. Exposed as a var for
// runtime config override.
var ProposeThreshold = 80

// safetyMultipliers maps each ActionClass to the fraction its max score is
// capped at. read is 1.00 (no ceiling); every other class is strictly below
// the value needed to hit 95 with confidence=value=100.
//
// Math check at compile time via TestSafetyCeiling in score_test.go.
var safetyMultipliers = map[ActionClass]float64{
	ClassRead:               1.00, // 100 × 100 × 1.00 / 100 = 100 — can clear 95
	ClassLocalWrite:         0.94, // 100 × 100 × 0.94 / 100 = 94  — cannot clear 95
	ClassDestructiveLocal:   0.93, // 100 × 100 × 0.93 / 100 = 93  — cannot clear 95
	ClassExternalSideEffect: 0.93, // 100 × 100 × 0.93 / 100 = 93  — cannot clear 95
	ClassSendPublishDelete:  0.90, // 100 × 100 × 0.90 / 100 = 90  — cannot clear 95
}

// SafetyMultiplier returns the multiplier for a given action class. Unknown
// classes return 0.0 — meaning their computed score is always 0, which is
// the safest possible fallback for a class the system doesn't recognize.
func SafetyMultiplier(class ActionClass) float64 {
	return safetyMultipliers[class]
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
// allowed to auto-execute. This is the single source of truth for the
// dispatcher's auto-execute decision — score above threshold AND class is
// read. The class check is belt-and-braces: the score alone is supposed to
// be enough because of the structural ceiling, but we defend in depth.
func CanAutoExecute(s int, c ActionClass) bool {
	return s >= AutoExecuteThreshold && c == ClassRead
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
