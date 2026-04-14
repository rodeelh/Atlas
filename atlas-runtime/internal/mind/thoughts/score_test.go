package thoughts

import (
	"testing"
)

// TestSafetyCeiling is the most important test in this package. It asserts
// that the structural ceiling holds: no non-read action class can reach the
// auto-execute threshold, no matter what confidence and value the model
// proposes. This is the property the whole trust model depends on.
func TestSafetyCeiling(t *testing.T) {
	nonReadClasses := []ActionClass{
		ClassLocalWrite,
		ClassDestructiveLocal,
		ClassExternalSideEffect,
		ClassSendPublishDelete,
	}
	for _, class := range nonReadClasses {
		max := MaxScoreForClass(class)
		if max >= AutoExecuteThreshold {
			t.Errorf("SAFETY CEILING VIOLATED: class %q can reach score %d (threshold %d). "+
				"Fix safetyMultipliers in score.go.",
				class, max, AutoExecuteThreshold)
		}
	}

	// read must be able to reach the ceiling — otherwise auto-execute is
	// impossible by construction, which is a bug in the other direction.
	if MaxScoreForClass(ClassRead) < AutoExecuteThreshold {
		t.Errorf("ClassRead max score %d < threshold %d: auto-execute is impossible",
			MaxScoreForClass(ClassRead), AutoExecuteThreshold)
	}
}

func TestComputeScore_Read(t *testing.T) {
	cases := []struct {
		confidence, value, want int
	}{
		{100, 100, 100},
		{50, 100, 50},
		{100, 50, 50},
		{80, 80, 64},
		{0, 100, 0},
		{100, 0, 0},
	}
	for _, tc := range cases {
		got := ComputeScore(tc.confidence, tc.value, ClassRead)
		if got != tc.want {
			t.Errorf("ComputeScore(%d, %d, read) = %d, want %d",
				tc.confidence, tc.value, got, tc.want)
		}
	}
}

func TestComputeScore_NonRead(t *testing.T) {
	// For every non-read class, confirm max(score) < 95.
	cases := []struct {
		class           ActionClass
		wantAtMax       int
		wantAutoExecute bool
	}{
		{ClassLocalWrite, 94, false},
		{ClassDestructiveLocal, 93, false},
		{ClassExternalSideEffect, 93, false},
		{ClassSendPublishDelete, 90, false},
	}
	for _, tc := range cases {
		got := ComputeScore(100, 100, tc.class)
		if got != tc.wantAtMax {
			t.Errorf("ComputeScore(100, 100, %s) = %d, want %d",
				tc.class, got, tc.wantAtMax)
		}
		if CanAutoExecute(got, tc.class) != tc.wantAutoExecute {
			t.Errorf("CanAutoExecute(%d, %s) = %t, want %t",
				got, tc.class, CanAutoExecute(got, tc.class), tc.wantAutoExecute)
		}
	}
}

func TestComputeScore_Clamping(t *testing.T) {
	// Negative and >100 inputs should be clamped, not rejected.
	cases := []struct {
		confidence, value int
		class             ActionClass
		want              int
	}{
		{-50, 100, ClassRead, 0},
		{100, -10, ClassRead, 0},
		{150, 150, ClassRead, 100},
		{200, 100, ClassRead, 100},
		{50, 200, ClassRead, 50},
	}
	for _, tc := range cases {
		got := ComputeScore(tc.confidence, tc.value, tc.class)
		if got != tc.want {
			t.Errorf("ComputeScore(%d, %d, %s) = %d, want %d",
				tc.confidence, tc.value, tc.class, got, tc.want)
		}
	}
}

func TestComputeScore_UnknownClass(t *testing.T) {
	// Unknown class should produce 0 — the safest fallback.
	got := ComputeScore(100, 100, ActionClass("garbage"))
	if got != 0 {
		t.Errorf("unknown class should produce score 0, got %d", got)
	}
}

func TestCanAutoExecute(t *testing.T) {
	cases := []struct {
		score int
		class ActionClass
		want  bool
	}{
		{95, ClassRead, true},
		{100, ClassRead, true},
		{94, ClassRead, false},
		{95, ClassLocalWrite, false}, // class check blocks even at threshold
		{100, ClassDestructiveLocal, false},
		{100, ClassExternalSideEffect, false},
		{100, ClassSendPublishDelete, false},
	}
	for _, tc := range cases {
		got := CanAutoExecute(tc.score, tc.class)
		if got != tc.want {
			t.Errorf("CanAutoExecute(%d, %s) = %t, want %t",
				tc.score, tc.class, got, tc.want)
		}
	}
}

func TestShouldPropose(t *testing.T) {
	cases := []struct {
		score int
		class ActionClass
		want  bool
	}{
		{79, ClassLocalWrite, false}, // below propose threshold
		{80, ClassLocalWrite, true},  // at propose threshold
		{94, ClassLocalWrite, true},  // max for local_write
		{95, ClassRead, false},       // auto-execute, not proposal
		{100, ClassRead, false},      // auto-execute, not proposal
		{80, ClassRead, true},        // read class but not at auto-execute threshold
		{85, ClassExternalSideEffect, true},
	}
	for _, tc := range cases {
		got := ShouldPropose(tc.score, tc.class)
		if got != tc.want {
			t.Errorf("ShouldPropose(%d, %s) = %t, want %t",
				tc.score, tc.class, got, tc.want)
		}
	}
}

func TestSafetyCeiling_ExhaustiveSweep(t *testing.T) {
	// Sweep every (confidence, value) pair for every non-read class and
	// assert no point reaches 95. This is the belt-and-braces version of
	// TestSafetyCeiling — proves the ceiling by exhaustion, not just at
	// the corner.
	nonReadClasses := []ActionClass{
		ClassLocalWrite,
		ClassDestructiveLocal,
		ClassExternalSideEffect,
		ClassSendPublishDelete,
	}
	for _, class := range nonReadClasses {
		for c := 0; c <= 100; c++ {
			for v := 0; v <= 100; v++ {
				got := ComputeScore(c, v, class)
				if got >= AutoExecuteThreshold {
					t.Fatalf("SAFETY VIOLATION: ComputeScore(%d, %d, %s) = %d >= %d",
						c, v, class, got, AutoExecuteThreshold)
				}
			}
		}
	}
}
