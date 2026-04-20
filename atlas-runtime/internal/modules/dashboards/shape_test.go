package dashboards

import (
	"strings"
	"testing"
)

func TestDescribeShapeUsesFrontendOptionNames(t *testing.T) {
	report := describeShape(map[string]any{
		"rows": []any{
			map[string]any{"date": "2026-04-20", "value": 12, "title": "alpha"},
		},
	})
	if report.SuggestedPreset != PresetLineChart {
		t.Fatalf("suggested preset = %q, want %q", report.SuggestedPreset, PresetLineChart)
	}
	if report.SuggestedOptions["seriesPath"] != "rows" {
		t.Fatalf("expected seriesPath suggestion, got %+v", report.SuggestedOptions)
	}
	if _, ok := report.SuggestedOptions["x"]; !ok {
		t.Fatalf("expected x suggestion, got %+v", report.SuggestedOptions)
	}
	if _, ok := report.SuggestedOptions["y"]; !ok {
		t.Fatalf("expected y suggestion, got %+v", report.SuggestedOptions)
	}
	if strings.Contains(report.Hint, "xField") || strings.Contains(report.Hint, "titlePath") {
		t.Fatalf("hint used stale option names: %q", report.Hint)
	}
}
