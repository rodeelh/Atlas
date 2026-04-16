package dashboards

// shape.go — lightweight schema introspection for resolved source data.
//
// The dashboard authoring flow has historically been blind to the shape of a
// source's output: the agent adds a source, guesses widget options, and only
// discovers the true shape on preview. describeShape walks a resolved value
// and returns a compact path inventory plus a suggested widget preset so the
// agent can wire widgets correctly on the first try.

import (
	"fmt"
	"sort"
	"strings"
)

// pathInfo describes one field in the resolved data.
type pathInfo struct {
	Path   string `json:"path"`             // dot path (e.g. "days[].high"); empty for root
	Type   string `json:"type"`             // string | number | boolean | array | object | null
	Sample any    `json:"sample,omitempty"` // primitive (truncated for strings) or count for arrays
}

// shapeReport is returned to the agent alongside a resolved sample. The agent
// uses suggestedPreset + paths to configure widget bindings without guessing.
type shapeReport struct {
	Kind            string     `json:"kind"`            // scalar | array | object | text_blob | empty
	SuggestedPreset string     `json:"suggestedPreset"` // metric | list | table | line_chart | markdown
	Hint            string     `json:"hint"`            // one-line guidance on how to bind a widget
	Paths           []pathInfo `json:"paths"`           // flat list of fields with types and samples
}

// describeShape produces a shapeReport for v. v is the resolved payload from
// resolveSource; it may be a scalar, array, object, or the {"text": "..."}
// wrapper returned for text-only skills.
func describeShape(v any) shapeReport {
	report := shapeReport{Paths: []pathInfo{}}
	walkShape(v, "", &report.Paths, 0)

	switch x := v.(type) {
	case nil:
		report.Kind = "empty"
		report.SuggestedPreset = "markdown"
		report.Hint = "Source returned nothing. Check the config args and re-add."
	case string:
		report.Kind = "text_blob"
		report.SuggestedPreset = "markdown"
		report.Hint = "Source returned a plain string. Use preset=markdown; no path needed."
	case []any:
		report.Kind = "array"
		report.SuggestedPreset = arrayPreset(x)
		report.Hint = arrayHint(x)
	case map[string]any:
		// Detect the {"text": "..."} wrapper produced by resolveSkill for
		// skills that return only human-readable text.
		if t, ok := x["text"].(string); ok && len(x) == 1 {
			report.Kind = "text_blob"
			report.SuggestedPreset = "markdown"
			report.Hint = "Source returned text only (wrapped as {text}). Use preset=markdown with options.path=\"text\"."
			_ = t
			break
		}
		report.Kind = "object"
		report.SuggestedPreset, report.Hint = objectPresetAndHint(x)
	default:
		report.Kind = "scalar"
		report.SuggestedPreset = "metric"
		report.Hint = fmt.Sprintf("Source returned a single %T value. Use preset=metric; no path needed.", v)
	}
	return report
}

// walkShape appends one pathInfo per interesting node in v. Limits recursion
// depth and total entries so pathological inputs don't balloon the response.
func walkShape(v any, path string, out *[]pathInfo, depth int) {
	const maxDepth = 4
	const maxEntries = 50
	if depth > maxDepth || len(*out) >= maxEntries {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		if path != "" {
			*out = append(*out, pathInfo{Path: path, Type: "object"})
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := k
			if path != "" {
				child = path + "." + k
			}
			walkShape(x[k], child, out, depth+1)
		}
	case []any:
		*out = append(*out, pathInfo{Path: path, Type: "array", Sample: fmt.Sprintf("%d items", len(x))})
		if len(x) > 0 {
			walkShape(x[0], path+"[]", out, depth+1)
		}
	case string:
		*out = append(*out, pathInfo{Path: path, Type: "string", Sample: truncString(x, 80)})
	case float64:
		*out = append(*out, pathInfo{Path: path, Type: "number", Sample: x})
	case int, int64:
		*out = append(*out, pathInfo{Path: path, Type: "number", Sample: x})
	case bool:
		*out = append(*out, pathInfo{Path: path, Type: "boolean", Sample: x})
	case nil:
		*out = append(*out, pathInfo{Path: path, Type: "null"})
	default:
		*out = append(*out, pathInfo{Path: path, Type: fmt.Sprintf("%T", v)})
	}
}

// arrayPreset picks a preset for a top-level array result.
func arrayPreset(arr []any) string {
	if len(arr) == 0 {
		return "list"
	}
	// Objects with numeric "x"/"y" or "date"/"value" → chart-friendly
	if m, ok := arr[0].(map[string]any); ok {
		if hasKey(m, "date") || hasKey(m, "time") || hasKey(m, "timestamp") {
			if hasNumericField(m) {
				return "line_chart"
			}
		}
		return "table"
	}
	return "list"
}

func arrayHint(arr []any) string {
	if len(arr) == 0 {
		return "Source returned an empty array. Bind with preset=list or table; no path needed."
	}
	if _, ok := arr[0].(map[string]any); ok {
		return "Source is an array of objects. Use preset=table with options.columns, or preset=list with options.titlePath."
	}
	return "Source is an array of scalars. Use preset=list; no path needed."
}

// objectPresetAndHint picks a preset when the root is an object. It looks for
// a single nested array (common pattern: {"memories": [...]}, {"days": [...]})
// and recommends list/table with options.path pointing at that array. If no
// nested array, falls back to metric with the first numeric field.
func objectPresetAndHint(m map[string]any) (preset, hint string) {
	// Find nested arrays.
	var arrayKeys []string
	for k, v := range m {
		if _, ok := v.([]any); ok {
			arrayKeys = append(arrayKeys, k)
		}
	}
	sort.Strings(arrayKeys)
	if len(arrayKeys) == 1 {
		k := arrayKeys[0]
		inner, _ := m[k].([]any)
		if len(inner) > 0 {
			if im, ok := inner[0].(map[string]any); ok && (hasKey(im, "date") || hasKey(im, "time") || hasKey(im, "timestamp")) && hasNumericField(im) {
				return "line_chart", fmt.Sprintf("Source has a time series at %q. Use preset=line_chart with options.path=%q, options.xField=<date|time>, options.yField=<numeric>.", k, k)
			}
			if _, ok := inner[0].(map[string]any); ok {
				return "table", fmt.Sprintf("Source has an array of objects at %q. Use preset=table with options.path=%q and options.columns=[...].", k, k)
			}
			return "list", fmt.Sprintf("Source has an array at %q. Use preset=list with options.path=%q.", k, k)
		}
		return "list", fmt.Sprintf("Source has an (empty) array at %q. Use preset=list with options.path=%q.", k, k)
	}
	// Multiple arrays → let the agent choose; recommend table by default.
	if len(arrayKeys) > 1 {
		return "table", fmt.Sprintf("Source has multiple arrays (%s). Pick one and use preset=table with options.path=<key>.", strings.Join(arrayKeys, ", "))
	}
	// No arrays — try to find a numeric field for a metric.
	if numKey := firstNumericKey(m); numKey != "" {
		return "metric", fmt.Sprintf("Source is a flat object. Use preset=metric with options.path=%q (or pick another numeric field).", numKey)
	}
	// No arrays, no numerics — probably a label-style record.
	return "markdown", "Source is a flat object with no numeric fields. Use preset=markdown, or preset=metric with options.path=<field>."
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func hasNumericField(m map[string]any) bool {
	for _, v := range m {
		switch v.(type) {
		case float64, int, int64:
			return true
		}
	}
	return false
}

func firstNumericKey(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch m[k].(type) {
		case float64, int, int64:
			return k
		}
	}
	return ""
}

func truncString(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// truncateSample returns a trimmed copy of v suitable for embedding in a skill
// result. Long strings are truncated; large arrays keep only the first few
// entries. Keeps the response small and readable for the agent.
func truncateSample(v any) any {
	return truncateSampleAt(v, 0)
}

func truncateSampleAt(v any, depth int) any {
	const maxDepth = 4
	const maxStringLen = 240
	const maxArrayLen = 3
	if depth > maxDepth {
		return "…"
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = truncateSampleAt(val, depth+1)
		}
		return out
	case []any:
		n := len(x)
		if n > maxArrayLen {
			n = maxArrayLen
		}
		out := make([]any, 0, n+1)
		for i := 0; i < n; i++ {
			out = append(out, truncateSampleAt(x[i], depth+1))
		}
		if len(x) > maxArrayLen {
			out = append(out, fmt.Sprintf("… (%d more)", len(x)-maxArrayLen))
		}
		return out
	case string:
		return truncString(x, maxStringLen)
	default:
		return v
	}
}
