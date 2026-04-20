package dashboards

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
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

// softFailSentinel is stored in the samples map for sources that fail to
// resolve transiently (network errors, timeouts, 5xx). Widget schema
// validation is skipped for these sources so a temporary outage does not
// permanently block commit. Hard failures (401/403) still abort immediately.
type softFailSentinel struct{}

func (m *Module) resolveSourceSamples(ctx context.Context, d Dashboard) (map[string]any, error) {
	samples := make(map[string]any, len(d.Sources))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var hardErr error

	for _, src := range d.Sources {
		wg.Add(1)
		go func(s DataSource) {
			defer wg.Done()
			sourceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			data, err := m.resolveSourceByName(sourceCtx, d.ID, s.Name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if isHardSourceFailure(err) && hardErr == nil {
					hardErr = fmt.Errorf("source %q: %w", s.Name, err)
				}
				// Soft failure: record sentinel so widget validation is skipped.
				samples[s.Name] = softFailSentinel{}
				return
			}
			samples[s.Name] = data
		}(src)
	}
	wg.Wait()
	return samples, hardErr
}

func validateWidgetSample(w Widget, samples map[string]any) error {
	if len(w.Bindings) == 0 || w.Code.Mode != ModePreset {
		return nil
	}
	sample, ok := samples[w.Bindings[0].Source]
	if !ok {
		return fmt.Errorf("binding sample for source %q is missing", w.Bindings[0].Source)
	}
	if _, soft := sample.(softFailSentinel); soft {
		return nil
	}
	if projected, ok := applyBindingProjection(sample, w.Bindings[0]); ok {
		sample = projected
	} else if w.Bindings[0].Path != "" {
		return fmt.Errorf("binding path %q was not found in source %q", w.Bindings[0].Path, w.Bindings[0].Source)
	}
	opts := w.Code.Options
	switch w.Code.Preset {
	case PresetMetric, PresetProgress, PresetGauge:
		if path := stringOption(opts, "path"); path != "" {
			if _, ok := valueAtPath(sample, path); !ok {
				return fmt.Errorf("%s path %q was not found in the sampled source output", w.Code.Preset, path)
			}
		}
		if maxPath := stringOption(opts, "maxPath"); maxPath != "" {
			if _, ok := valueAtPath(sample, maxPath); !ok {
				return fmt.Errorf("%s maxPath %q was not found in the sampled source output", w.Code.Preset, maxPath)
			}
		}
	case PresetTable:
		return validateTableSample(sample, opts)
	case PresetLineChart, PresetAreaChart, PresetBarChart, PresetScatter:
		return validateChartSample(w.Code.Preset, sample, opts)
	case PresetPieChart, PresetDonutChart:
		return validatePieChartSample(sample, opts)
	case PresetStacked:
		return validateStackedChartSample(sample, opts)
	case PresetList:
		return validateListSample(sample, opts)
	case PresetStatusGrid:
		return validateStatusGridSample(sample, opts)
	case PresetKPIGroup:
		return validateKPIGroupSample(sample, opts)
	case PresetTimeline:
		return validateTimelineSample(sample, opts)
	case PresetHeatmap:
		return validateHeatmapSample(sample, opts)
	case PresetMarkdown:
		if path := stringOption(opts, "path"); path != "" {
			if _, ok := valueAtPath(sample, path); !ok {
				return fmt.Errorf("markdown path %q was not found in the sampled source output", path)
			}
		}
	}
	return nil
}

func validateStatusGridSample(sample any, opts map[string]any) error {
	itemsPath := stringOption(opts, "itemsPath")
	target, ok := resolveTarget(sample, itemsPath)
	if !ok {
		return fmt.Errorf("status_grid itemsPath %q was not found in the sampled source output", itemsPath)
	}
	items, ok := target.([]any)
	if !ok {
		return fmt.Errorf("status_grid expects an array at %q, got %T", itemsPath, target)
	}
	if len(items) == 0 {
		return nil
	}
	obj, ok := items[0].(map[string]any)
	if !ok {
		return fmt.Errorf("status_grid expects array items to be objects, got %T", items[0])
	}
	labelKey := stringOptionWithDefault(opts, "labelKey", "title")
	statusKey := stringOptionWithDefault(opts, "statusKey", "status")
	if _, ok := obj[labelKey]; !ok {
		return fmt.Errorf("status_grid labelKey %q was not present in the sampled item", labelKey)
	}
	if _, ok := obj[statusKey]; !ok {
		return fmt.Errorf("status_grid statusKey %q was not present in the sampled item", statusKey)
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
		return nil // empty sample on fresh install — can't prove schema but not an error
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

func validatePieChartSample(sample any, opts map[string]any) error {
	seriesPath := stringOption(opts, "seriesPath")
	if seriesPath == "" {
		seriesPath = stringOption(opts, "path")
	}
	target, ok := resolveTarget(sample, seriesPath)
	if !ok {
		return fmt.Errorf("pie_chart path %q was not found in the sampled source output", seriesPath)
	}
	rows, ok := target.([]any)
	if !ok {
		return fmt.Errorf("pie_chart expects an array of objects, got %T", target)
	}
	if len(rows) == 0 {
		return nil
	}
	obj, ok := rows[0].(map[string]any)
	if !ok {
		return fmt.Errorf("pie_chart expects array items to be objects, got %T", rows[0])
	}
	labelKey := stringOptionWithDefault(opts, "labelKey", "label")
	valueKey := stringOptionWithDefault(opts, "valueKey", "value")
	if _, ok := obj[labelKey]; !ok {
		return fmt.Errorf("pie_chart labelKey %q was not present in the sampled row", labelKey)
	}
	val, ok := obj[valueKey]
	if !ok {
		return fmt.Errorf("pie_chart valueKey %q was not present in the sampled row", valueKey)
	}
	if !isNumericValue(val) {
		return fmt.Errorf("pie_chart valueKey %q must resolve to a number, got %T", valueKey, val)
	}
	return nil
}

func validateStackedChartSample(sample any, opts map[string]any) error {
	seriesPath := stringOption(opts, "seriesPath")
	if seriesPath == "" {
		seriesPath = stringOption(opts, "path")
	}
	target, ok := resolveTarget(sample, seriesPath)
	if !ok {
		return fmt.Errorf("stacked_chart path %q was not found in the sampled source output", seriesPath)
	}
	rows, ok := target.([]any)
	if !ok {
		return fmt.Errorf("stacked_chart expects an array of objects, got %T", target)
	}
	if len(rows) == 0 {
		return nil
	}
	obj, ok := rows[0].(map[string]any)
	if !ok {
		return fmt.Errorf("stacked_chart expects array items to be objects, got %T", rows[0])
	}
	xKey := stringOptionWithDefault(opts, "x", "date")
	if _, ok := obj[xKey]; !ok {
		return fmt.Errorf("stacked_chart x key %q was not present in the sampled row", xKey)
	}
	seriesKeys := stringSliceOption(opts, "seriesKeys")
	if len(seriesKeys) == 0 {
		return fmt.Errorf("stacked_chart requires seriesKeys")
	}
	for _, key := range seriesKeys {
		val, ok := obj[key]
		if !ok {
			return fmt.Errorf("stacked_chart series key %q was not present in the sampled row", key)
		}
		if !isNumericValue(val) {
			return fmt.Errorf("stacked_chart series key %q must resolve to a number, got %T", key, val)
		}
	}
	return nil
}

func validateTimelineSample(sample any, opts map[string]any) error {
	itemsPath := stringOption(opts, "itemsPath")
	target, ok := resolveTarget(sample, itemsPath)
	if !ok {
		return fmt.Errorf("timeline itemsPath %q was not found in the sampled source output", itemsPath)
	}
	items, ok := target.([]any)
	if !ok {
		return fmt.Errorf("timeline expects an array of objects, got %T", target)
	}
	if len(items) == 0 {
		return nil
	}
	obj, ok := items[0].(map[string]any)
	if !ok {
		return fmt.Errorf("timeline expects array items to be objects, got %T", items[0])
	}
	timeKey := stringOptionWithDefault(opts, "timeKey", "date")
	labelKey := stringOptionWithDefault(opts, "labelKey", "title")
	if _, ok := obj[timeKey]; !ok {
		return fmt.Errorf("timeline timeKey %q was not present in the sampled item", timeKey)
	}
	if _, ok := obj[labelKey]; !ok {
		return fmt.Errorf("timeline labelKey %q was not present in the sampled item", labelKey)
	}
	return nil
}

func validateHeatmapSample(sample any, opts map[string]any) error {
	seriesPath := stringOption(opts, "seriesPath")
	if seriesPath == "" {
		seriesPath = stringOption(opts, "path")
	}
	target, ok := resolveTarget(sample, seriesPath)
	if !ok {
		return fmt.Errorf("heatmap path %q was not found in the sampled source output", seriesPath)
	}
	rows, ok := target.([]any)
	if !ok {
		return fmt.Errorf("heatmap expects an array of objects, got %T", target)
	}
	if len(rows) == 0 {
		return nil
	}
	obj, ok := rows[0].(map[string]any)
	if !ok {
		return fmt.Errorf("heatmap expects array items to be objects, got %T", rows[0])
	}
	dateKey := stringOptionWithDefault(opts, "dateKey", "date")
	valueKey := stringOptionWithDefault(opts, "valueKey", "value")
	if _, ok := obj[dateKey]; !ok {
		return fmt.Errorf("heatmap dateKey %q was not present in the sampled row", dateKey)
	}
	val, ok := obj[valueKey]
	if !ok {
		return fmt.Errorf("heatmap valueKey %q was not present in the sampled row", valueKey)
	}
	if !isNumericValue(val) {
		return fmt.Errorf("heatmap valueKey %q must resolve to a number, got %T", valueKey, val)
	}
	return nil
}

func validateKPIGroupSample(sample any, opts map[string]any) error {
	itemsPath := stringOption(opts, "itemsPath")
	target, ok := resolveTarget(sample, itemsPath)
	if !ok {
		return fmt.Errorf("kpi_group itemsPath %q was not found in the sampled source output", itemsPath)
	}
	items, ok := target.([]any)
	if !ok {
		return fmt.Errorf("kpi_group expects an array at %q, got %T", itemsPath, target)
	}
	if len(items) == 0 {
		return nil
	}
	obj, ok := items[0].(map[string]any)
	if !ok {
		return fmt.Errorf("kpi_group expects array items to be objects, got %T", items[0])
	}
	labelKey := stringOptionWithDefault(opts, "labelKey", "label")
	valueKey := stringOptionWithDefault(opts, "valueKey", "value")
	if _, ok := obj[labelKey]; !ok {
		return fmt.Errorf("kpi_group labelKey %q was not present in the sampled item", labelKey)
	}
	val, ok := obj[valueKey]
	if !ok {
		return fmt.Errorf("kpi_group valueKey %q was not present in the sampled item", valueKey)
	}
	if !isNumericValue(val) {
		return fmt.Errorf("kpi_group valueKey %q must resolve to a number, got %T", valueKey, val)
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
		return nil // empty sample on fresh install — can't prove schema but not an error
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
	return evalPathTokens(data, parsePathTokens(path))
}

type pathTokenKind int

const (
	pathTokenKey pathTokenKind = iota
	pathTokenIndex
	pathTokenEach
)

type pathToken struct {
	kind  pathTokenKind
	key   string
	index int
}

func parsePathTokens(path string) []pathToken {
	parts := strings.Split(path, ".")
	tokens := make([]pathToken, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		for part != "" {
			bracket := strings.IndexByte(part, '[')
			if bracket == -1 {
				if idx, err := strconv.Atoi(part); err == nil {
					tokens = append(tokens, pathToken{kind: pathTokenIndex, index: idx})
				} else {
					tokens = append(tokens, pathToken{kind: pathTokenKey, key: part})
				}
				break
			}
			if bracket > 0 {
				tokens = append(tokens, pathToken{kind: pathTokenKey, key: part[:bracket]})
			}
			close := strings.IndexByte(part[bracket:], ']')
			if close == -1 {
				tokens = append(tokens, pathToken{kind: pathTokenKey, key: part[bracket:]})
				break
			}
			content := part[bracket+1 : bracket+close]
			if content == "" {
				tokens = append(tokens, pathToken{kind: pathTokenEach})
			} else if idx, err := strconv.Atoi(content); err == nil {
				tokens = append(tokens, pathToken{kind: pathTokenIndex, index: idx})
			} else {
				tokens = append(tokens, pathToken{kind: pathTokenKey, key: content})
			}
			part = part[bracket+close+1:]
		}
	}
	return tokens
}

func evalPathTokens(data any, tokens []pathToken) (any, bool) {
	current := data
	for i, token := range tokens {
		if token.kind == pathTokenEach {
			arr, ok := current.([]any)
			if !ok {
				return nil, false
			}
			projected := make([]any, 0, len(arr))
			rest := tokens[i+1:]
			for _, item := range arr {
				val, ok := evalPathTokens(item, rest)
				if ok {
					projected = append(projected, val)
				}
			}
			return projected, true
		}
		next, ok := evalPathToken(current, token)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func evalPathToken(data any, token pathToken) (any, bool) {
	switch token.kind {
	case pathTokenKey:
		obj, ok := data.(map[string]any)
		if !ok {
			return nil, false
		}
		val, ok := obj[token.key]
		return val, ok
	case pathTokenIndex:
		arr, ok := data.([]any)
		if !ok || token.index < 0 || token.index >= len(arr) {
			return nil, false
		}
		return arr[token.index], true
	default:
		return nil, false
	}
}

func applyBindingProjection(data any, binding DataSourceBinding) (any, bool) {
	if binding.Path == "" {
		return data, true
	}
	return valueAtPath(data, binding.Path)
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
