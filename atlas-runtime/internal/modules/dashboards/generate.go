package dashboards

// generate.go — AI-powered dashboard generation.
//
// Given a free-form user prompt and an injected provider resolver, this asks
// the active AI model to emit a JSON DashboardDefinition, then validates the
// result against the dashboards schema and safety rules. The validation step
// is the only thing standing between an arbitrary model response and the
// dashboards.json on disk, so it must be strict.
//
// Generation is deliberately blocking. dashboard.create is a draft-class skill
// (needs approval), so the user already explicitly OK'd it; there is no value
// in adding async polling on top of an approval flow.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"atlas-runtime-go/internal/agent"
)

// ProviderResolver returns the AI provider config to use for generation.
// Mirrors the closure pattern used by other AI-driven features (Forge, Mind).
type ProviderResolver func() (agent.ProviderConfig, error)

// generationSystemPrompt is the strict instruction sent to the AI model.
// Constraints are duplicated in human-readable form AND inside the JSON
// schema example so the model has multiple anchors.
const generationSystemPrompt = `You are Atlas Dashboard Builder. The user will describe a dashboard they want; you must respond with a single JSON object that matches the DashboardDefinition schema below. NO prose. NO markdown fences. NO commentary. Just the JSON object.

SCHEMA:
{
  "name": "string (required)",
  "description": "string (optional)",
  "widgets": [
    {
      "id": "lowercase-hyphenated-string (required, unique within dashboard)",
      "kind": "metric | table | line_chart | bar_chart | markdown | list",
      "title": "string (optional)",
      "description": "string (optional)",
      "gridX": 0-11, "gridY": 0+, "gridW": 1-12, "gridH": 1-12,
      "source": {
        "kind": "runtime | skill | sql",
        "endpoint": "/path (when kind=runtime)",
        "query":    { "key": "value" } (optional, when kind=runtime),
        "action":   "namespace.action (when kind=skill)",
        "args":     { "key": "value" } (optional, when kind=skill),
        "sql":      "SELECT ... (when kind=sql, single SELECT only, no writes)"
      },
      "options": { "free-form widget-specific config (optional)" }
    }
  ]
}

ALLOWED RUNTIME ENDPOINTS (use only these):
  /status, /logs, /memories, /diary, /mind, /skills, /skills-memory,
  /workflows, /workflows/{id}/runs, /automations, /automations/{id}/runs,
  /communications, /forge/proposals, /forge/installed, /forge/researching,
  /usage/summary, /usage/events

CUSTOM HTML WIDGETS (advanced — use sparingly):
  Set "kind":"custom_html" and provide "html" (required), optional "css", optional "js".
  The widget renders inside a sandboxed iframe with NO network access (CSP default-src 'none').
  If you also set a "source", the parent posts the resolved data into the iframe via postMessage.
  Define window.atlasRender = function(data) { ... } in your "js" — it's invoked with the data.
  The iframe CANNOT fetch external URLs, load remote scripts, or read parent state. Assume only DOM + the data you're given.

RULES:
  1. ONLY use widget kinds and source kinds listed above. Do NOT invent kinds.
  2. DO NOT use the "web" source kind — it is reserved.
  3. DO NOT include any field not in the schema.
  4. SQL queries must be a single SELECT (or WITH … SELECT). No writes, no DDL, no semicolons.
  5. Every widget must declare a source UNLESS its kind is "markdown" with embedded text in options.text, OR its kind is "custom_html".
  6. custom_html widgets MUST have a non-empty "html" field.
  7. Grid: dashboards are 12 columns wide. Lay widgets out so they don't overlap.
  8. Pick a clear, human "name" derived from the user's request.
  9. Output ONE valid JSON object. Nothing else.`

// Generate asks the AI to produce a DashboardDefinition matching prompt.
// The returned definition has been validated end-to-end against the
// dashboards safety rules and is ready to persist.
//
// generate.go is intentionally narrow: it does not save the result; the caller
// (the skill) decides whether to persist after the user approves. That keeps
// the function side-effect-free and trivial to test.
func Generate(ctx context.Context, resolver ProviderResolver, name, prompt string) (*DashboardDefinition, error) {
	if resolver == nil {
		return nil, errors.New("dashboard generate: no provider resolver configured")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("dashboard generate: prompt is required")
	}
	provider, err := resolver()
	if err != nil {
		return nil, fmt.Errorf("dashboard generate: %w", err)
	}

	userMsg := strings.TrimSpace(prompt)
	if name != "" {
		userMsg = fmt.Sprintf("Dashboard name: %s\n\n%s", name, userMsg)
	}
	messages := []agent.OAIMessage{
		{Role: "system", Content: generationSystemPrompt},
		{Role: "user", Content: userMsg},
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	_, text, _, err := agent.CallAINonStreamingExported(callCtx, provider, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("dashboard generate: AI call failed: %w", err)
	}
	def, err := parseAndValidateDefinition(text)
	if err != nil {
		return nil, fmt.Errorf("dashboard generate: invalid AI response: %w", err)
	}
	if def.Name == "" && name != "" {
		def.Name = name
	}
	return def, nil
}

// parseAndValidateDefinition extracts a DashboardDefinition from raw model
// output. The model is supposed to emit pure JSON, but production AI calls
// regularly include markdown fences or stray prose, so we tolerate both.
func parseAndValidateDefinition(text string) (*DashboardDefinition, error) {
	cleaned := stripJSONFences(text)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil, errors.New("AI returned empty response")
	}
	// If the model wrapped the JSON in commentary, find the first { and the
	// matching }. We use a brace-counting walk because the JSON may contain
	// strings with embedded braces.
	jsonText, err := extractFirstJSONObject(cleaned)
	if err != nil {
		return nil, err
	}

	var def DashboardDefinition
	if err := json.Unmarshal([]byte(jsonText), &def); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if err := validateGeneratedDefinition(&def); err != nil {
		return nil, err
	}
	return &def, nil
}

// stripJSONFences removes a leading ```json fence and trailing ``` if present.
func stripJSONFences(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		// Drop the first line.
		if nl := strings.IndexByte(t, '\n'); nl != -1 {
			t = t[nl+1:]
		} else {
			t = strings.TrimPrefix(t, "```")
		}
	}
	if strings.HasSuffix(t, "```") {
		t = strings.TrimSuffix(t, "```")
	}
	return t
}

// extractFirstJSONObject finds the first balanced {…} substring. String
// contents are skipped so embedded } characters do not throw off the depth
// counter.
func extractFirstJSONObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return "", errors.New("no JSON object found in response")
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", errors.New("unbalanced JSON object in response")
}

// validateGeneratedDefinition runs the dashboards-specific schema and safety
// checks on a freshly-parsed DashboardDefinition. Anything that would later
// fail at resolve time should be rejected here so the user never sees a
// half-broken dashboard.
func validateGeneratedDefinition(def *DashboardDefinition) error {
	if def.Name == "" {
		return errors.New("dashboard name is required")
	}
	if len(def.Widgets) == 0 {
		return errors.New("dashboard must have at least one widget")
	}
	if len(def.Widgets) > 24 {
		return fmt.Errorf("dashboard may have at most 24 widgets, got %d", len(def.Widgets))
	}
	seenIDs := make(map[string]bool, len(def.Widgets))
	for i := range def.Widgets {
		w := &def.Widgets[i]
		if w.ID == "" {
			w.ID = fmt.Sprintf("widget-%d", i+1)
		}
		if seenIDs[w.ID] {
			return fmt.Errorf("duplicate widget id %q", w.ID)
		}
		seenIDs[w.ID] = true

		if !isAllowedGeneratedKind(w.Kind) {
			return fmt.Errorf("widget %q has disallowed kind %q", w.ID, w.Kind)
		}
		if w.Source != nil {
			if err := validateGeneratedSource(w.ID, w.Source); err != nil {
				return err
			}
		} else if w.Kind != WidgetKindMarkdown && w.Kind != WidgetKindCustomHTML {
			// Most widget kinds must have a source — otherwise the resolve
			// handler returns an error tile, defeating the purpose.
			// markdown and custom_html may be self-contained.
			return fmt.Errorf("widget %q (%s) is missing a source", w.ID, w.Kind)
		}

		if w.Kind == WidgetKindCustomHTML {
			// custom_html widgets render inside a sandboxed iframe with strict
			// CSP. The HTML body is required so the iframe has something to
			// mount into.
			if strings.TrimSpace(w.HTML) == "" {
				return fmt.Errorf("widget %q (custom_html) must have a non-empty html field", w.ID)
			}
		} else {
			// Strip stray html/css/js the model may have attached to a
			// non-custom widget — those fields are only meaningful for
			// custom_html and would otherwise be silently ignored.
			w.HTML = ""
			w.CSS = ""
			w.JS = ""
		}
	}
	return nil
}

// isAllowedGeneratedKind reports whether the AI is permitted to use kind.
// custom_html is allowed but rendered inside a sandboxed iframe (CSP-locked,
// no network) so the safety surface is contained.
func isAllowedGeneratedKind(kind string) bool {
	switch kind {
	case WidgetKindMetric,
		WidgetKindTable,
		WidgetKindLineChart,
		WidgetKindBarChart,
		WidgetKindMarkdown,
		WidgetKindList,
		WidgetKindCustomHTML:
		return true
	}
	return false
}

func validateGeneratedSource(widgetID string, src *DataSource) error {
	switch src.Kind {
	case SourceKindRuntime:
		if !strings.HasPrefix(src.Endpoint, "/") {
			return fmt.Errorf("widget %q runtime endpoint must start with /", widgetID)
		}
		if !allowedRuntimeEndpoint(src.Endpoint) {
			return fmt.Errorf("widget %q runtime endpoint %q is not allowlisted", widgetID, src.Endpoint)
		}
	case SourceKindSkill:
		if src.Action == "" {
			return fmt.Errorf("widget %q skill source has no action", widgetID)
		}
	case SourceKindSQL:
		if _, err := validateSelectSQL(src.SQL); err != nil {
			return fmt.Errorf("widget %q sql source: %w", widgetID, err)
		}
	case SourceKindWeb:
		return fmt.Errorf("widget %q source kind %q is not allowed for AI-generated dashboards", widgetID, src.Kind)
	default:
		return fmt.Errorf("widget %q has unknown source kind %q", widgetID, src.Kind)
	}
	return nil
}
