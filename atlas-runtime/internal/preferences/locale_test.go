package preferences

import "testing"

func TestUnitSystemFromLocale(t *testing.T) {
	tests := []struct {
		locale     string
		wantUnit   string
		wantTemp   string
	}{
		// US → imperial / fahrenheit
		{"en_US", "imperial", "fahrenheit"},
		{"en-US", "imperial", "fahrenheit"}, // hyphen variant
		// UK → metric / celsius
		{"en_GB", "metric", "celsius"},
		// Saudi Arabia → metric / celsius
		{"ar_SA", "metric", "celsius"},
		// UAE → metric / celsius
		{"ar_AE", "metric", "celsius"},
		// Liberia → imperial / fahrenheit
		{"en_LR", "imperial", "fahrenheit"},
		// Myanmar → imperial / celsius (uses metric temp)
		{"my_MM", "imperial", "celsius"},
		// Belize → metric / fahrenheit (metric units, but fahrenheit temp)
		{"en_BZ", "metric", "fahrenheit"},
		// Australia → metric / celsius
		{"en_AU", "metric", "celsius"},
		// Canada → metric / celsius
		{"en_CA", "metric", "celsius"},
		// bare country code
		{"US", "imperial", "fahrenheit"},
		{"GB", "metric", "celsius"},
	}

	for _, tc := range tests {
		gotUnit, gotTemp := unitSystemFromLocale(tc.locale)
		if gotUnit != tc.wantUnit {
			t.Errorf("unitSystemFromLocale(%q) unitSystem = %q, want %q", tc.locale, gotUnit, tc.wantUnit)
		}
		if gotTemp != tc.wantTemp {
			t.Errorf("unitSystemFromLocale(%q) tempUnit = %q, want %q", tc.locale, gotTemp, tc.wantTemp)
		}
	}
}
