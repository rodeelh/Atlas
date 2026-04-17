package dashboards

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (m *Module) validateCommitReadiness(ctx context.Context, d Dashboard) error {
	samples, err := m.resolveSourceSamples(ctx, d)
	if err != nil {
		return err
	}
	for _, w := range d.Widgets {
		if err := validateWidgetSample(w, samples); err != nil {
			return fmt.Errorf("widget %q: %w", widgetLabel(w), err)
		}
	}
	return nil
}

func (m *Module) resolveSourceSamples(ctx context.Context, d Dashboard) (map[string]any, error) {
	samples := make(map[string]any, len(d.Sources))
	for _, src := range d.Sources {
		sourceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		data, err := m.resolveSourceByName(sourceCtx, d.ID, src.Name)
		cancel()
		if err != nil {
			if isHardSourceFailure(err) {
				return nil, fmt.Errorf("source %q has a hard validation failure: %w", src.Name, err)
			}
			return nil, fmt.Errorf("source %q failed to resolve during commit validation: %w", src.Name, err)
		}
		samples[src.Name] = data
	}
	return samples, nil
}

func validateWidgetSample(w Widget, samples map[string]any) error {
	if len(w.Bindings) == 0 || w.Code.Mode != ModePreset {
		return nil
	}
	sample, ok := samples[w.Bindings[0].Source]
	if !ok {
		return fmt.Errorf("binding sample for source %q is missing", w.Bindings[0].Source)
	}
	opts := w.Code.Options
	switch w.Code.Preset {
	case PresetMetric:
		if path := stringOption(opts, "path"); path != "" {
			if _, ok := valueAtPath(sample, path); !ok {
				return fmt.Errorf("metric path %q was not found in the sampled source output", path)
			}
		}
	case PresetTable:
		return validateTableSample(sample, opts)
	case PresetLineChart, PresetBarChart:
		return validateChartSample(w.Code.Preset, sample, opts)
	case PresetList:
		return validateListSample(sample, opts)
	case PresetMarkdown:
		if path := stringOption(opts, "path"); path != "" {
			if _, ok := valueAtPath(sample, path); !ok {
				return fmt.Errorf("markdown path %q was not found in the sampled source output", path)
			}
		}
	}
	return nil
}

func validateTableSample(sample any, opts map[string]any) error {
	target, ok := resolveTarget(sample, stringOption(opts, "path"))
	if !ok {
		return fmt.Errorf("table path %q was not found in the sampled source output", stringOption(opts, "path"))
	}
	switch rows := target.(type) {
	case []any:
		cols := stringSliceOption(opts, "columns")
		if len(cols) == 0 || len(rows) == 0 {
			return nil
		}
		obj, ok := rows[0].(map[string]any)
		if !ok {
			return fmt.Errorf("table columns require an array of objects at %q", stringOption(opts, "path"))
		}
		for _, col := range cols {
			if _, ok := obj[col]; !ok {
				return fmt.Errorf("table column %q was not present in the sampled row; use markdown or adjust columns", col)
			}
		}
	case map[string]any:
		return nil
	default:
		return fmt.Errorf("table expects an array or object, got %T", target)
	}
	return nil
}

func validateChartSample(preset string, sample any, opts map[string]any) error {
	seriesPath := stringOption(opts, "seriesPath")
	if seriesPath == "" {
		seriesPath = stringOption(opts, "path")
	}
	target, ok := resolveTarget(sample, seriesPath)
	if !ok {
		return fmt.Errorf("%s path %q was not found in the sampled source output", preset, seriesPath)
	}
	rows, ok := target.([]any)
	if !ok {
		return fmt.Errorf("%s expects an array of objects, got %T", preset, target)
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s sample at %q is empty, so commit cannot prove the x/y keys; use markdown/table or a populated source", preset, seriesPath)
	}
	obj, ok := rows[0].(map[string]any)
	if !ok {
		return fmt.Errorf("%s expects array items to be objects, got %T", preset, rows[0])
	}
	xKey := stringOptionWithDefault(opts, "x", "date")
	yKey := stringOptionWithDefault(opts, "y", "value")
	if _, ok := obj[xKey]; !ok {
		return fmt.Errorf("%s x key %q was not present in the sampled row", preset, xKey)
	}
	val, ok := obj[yKey]
	if !ok {
		return fmt.Errorf("%s y key %q was not present in the sampled row", preset, yKey)
	}
	if !isNumericValue(val) {
		return fmt.Errorf("%s y key %q must resolve to a number, got %T", preset, yKey, val)
	}
	return nil
}

func validateListSample(sample any, opts map[string]any) error {
	itemsPath := stringOption(opts, "itemsPath")
	target, ok := resolveTarget(sample, itemsPath)
	if !ok {
		return fmt.Errorf("list itemsPath %q was not found in the sampled source output", itemsPath)
	}
	items, ok := target.([]any)
	if !ok {
		return fmt.Errorf("list expects an array at %q, got %T", itemsPath, target)
	}
	if len(items) == 0 {
		return fmt.Errorf("list sample at %q is empty, so commit cannot prove the item schema; use markdown/table or a populated source", itemsPath)
	}
	obj, ok := items[0].(map[string]any)
	if !ok {
		if key := stringOption(opts, "labelKey"); key != "" {
			return fmt.Errorf("list labelKey %q requires an array of objects, got %T", key, items[0])
		}
		return nil
	}
	if key := stringOption(opts, "labelKey"); key != "" {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("list labelKey %q was not present in the sampled item; use markdown/table or adjust the binding", key)
		}
	}
	if key := stringOption(opts, "subKey"); key != "" {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("list subKey %q was not present in the sampled item", key)
		}
	}
	return nil
}

func resolveTarget(sample any, path string) (any, bool) {
	if path == "" {
		return sample, true
	}
	return valueAtPath(sample, path)
}

func valueAtPath(data any, path string) (any, bool) {
	if path == "" {
		return data, true
	}
	current := data
	for _, part := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func stringOption(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	val, _ := opts[key].(string)
	return val
}

func stringOptionWithDefault(opts map[string]any, key, fallback string) string {
	if val := stringOption(opts, key); val != "" {
		return val
	}
	return fallback
}

func stringSliceOption(opts map[string]any, key string) []string {
	if opts == nil {
		return nil
	}
	raw, ok := opts[key]
	if !ok {
		return nil
	}
	switch vals := raw.(type) {
	case []string:
		return vals
	case []any:
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func isNumericValue(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func isHardSourceFailure(err error) bool {
	if err == nil {
		return false
	}
	if isPermissionError(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	patterns := []string{
		"returned 401",
		"status 401",
		"unauthorized",
		"forbidden",
		"permission denied",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func widgetLabel(w Widget) string {
	if w.Title != "" {
		return w.Title
	}
	return w.ID
}
