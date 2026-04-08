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
	"atlas-runtime-go/internal/logstore"
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
      "kind": "metric | table | line_chart | bar_chart | markdown | list | custom_html",
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
      "options": { "free-form widget-specific config (optional)" },
      "html": "full HTML body string (REQUIRED when kind=custom_html)",
      "css":  "CSS string (optional, when kind=custom_html)",
      "js":   "JS string (optional, when kind=custom_html)"
    }
  ]
}

ALLOWED RUNTIME ENDPOINTS (use only these):
  /status, /logs, /memories, /diary, /mind, /skills, /skills-memory,
  /workflows, /workflows/{id}/runs, /automations, /automations/{id}/runs,
  /communications, /forge/proposals, /forge/installed, /forge/researching,
  /usage/summary, /usage/events

READ-ONLY SKILLS YOU CAN USE AS DATA SOURCES (source kind="skill"):
  finance.quote    — args: {symbol}          — current price. Returns plain text summary.
  finance.history  — args: {symbol: string, days: integer (NOT a string)}    — daily closing prices. Returns plain text:
                     "SYMBOL — last N trading days:\n  YYYY-MM-DD: PRICE\n  YYYY-MM-DD: PRICE\n..."
  finance.portfolio — args: {symbols}        — batch quotes. Returns plain text.
  websearch.query  — args: {query, count}    — web search results. Returns plain text:
                     numbered list "1. TITLE\nURL\nDESCRIPTION\n\n2. TITLE\n..."
  weather.current  — args: {location}        — current weather. Returns plain text.

SKILL DATA FORMAT — CRITICAL:
  When a skill source is used, the data passed to atlasRender(data) is:
    { "summary": "<the plain-text string the skill returned>", "artifacts": null }
  Always read data.summary (a string) — never data.history, data.news, data.prices, or any other key.
  Parse the text yourself in JS if you need structured values.
  Example for finance.history: lines are indented — always trim before matching.
    data.summary.split('\n').map(l=>l.trim()).forEach(l=>{ const m=l.match(/^(\d{4}-\d{2}-\d{2}):\s*([\d.]+)/); if(m) rows.push({date:m[1],price:+m[2]}); });
  Example for websearch.query: split data.summary on /\n(?=\d+\.\s)/ to get individual result blocks.

WIDGET KIND CONSTRAINT — CRITICAL:
  Built-in kinds (metric, table, list, line_chart, bar_chart) ONLY work with runtime or sql sources
  that return structured JSON. They will always show empty when fed skill data.
  Rule: if source.kind is "skill", the widget kind MUST be "custom_html" with JS to parse data.summary.
  Never pair a skill source with metric/table/list/line_chart/bar_chart — it will always be empty.

CUSTOM HTML WIDGETS — use for charts, financial data, news panels, styled layouts:
  Set "kind":"custom_html" and provide "html" (REQUIRED — non-empty HTML markup string).
  Optional "css" and "js" fields.
  The widget renders in a sandboxed iframe with NO network access (CSP default-src 'none').
  The iframe cannot fetch URLs or load external scripts — use only inline DOM + data you receive.
  Define window.atlasRender = function(data) { ... } in "js" to receive resolved source data.
  Use SVG or Canvas for charts — no external chart libraries available.

FALLBACK RULE — IMPORTANT:
  If no runtime endpoint or skill perfectly fits a widget's data need, fall back to
  websearch.query with a precise query (e.g. "AAPL stock price last 5 days", "Apple news today").
  Never embed static/hardcoded data and never leave a widget without a source when live data exists
  via search. websearch.query is always available and covers any topic.

RULES:
  1. ONLY use widget kinds and source kinds listed above. Do NOT invent kinds.
  2. DO NOT use source kind "web" — it is reserved and will be rejected.
  3. DO NOT include any field not in the schema.
  4. SQL must be a single SELECT (or WITH … SELECT). No writes, no DDL, no semicolons.
  5. Every widget needs a source UNLESS kind is "markdown" (text in options.text) OR "custom_html".
  6. custom_html widgets MUST have a non-empty "html" field.
  7. Grid: 12 columns wide. Widgets must not overlap.
  8. Honor any colors, themes, or layout preferences the user specifies.
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

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	firstMsg, _, _, err := agent.CallAINonStreamingExported(callCtx, provider, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("dashboard generate: AI call failed: %w", err)
	}
	text, _ := firstMsg.Content.(string)

	def, valErr := parseAndValidateDefinition(text)
	if valErr != nil {
		// Log the raw model output so we can diagnose repeat failures.
		preview := text
		if len(preview) > 500 {
			preview = preview[:500] + "…"
		}
		logstore.Write("warn",
			fmt.Sprintf("dashboard generate: first attempt failed validation (%v); retrying. raw response: %s", valErr, preview),
			map[string]string{"name": name})

		// Retry once with the validation error fed back to the model so it can
		// self-correct. This handles the most common failure modes: markdown
		// fences, stray prose, "web" source kind, missing html field, etc.
		retryMsg := fmt.Sprintf(
			"Your previous response failed validation with this error:\n\n  %v\n\n"+
				"Output ONLY a corrected JSON object. No explanation. No markdown. No extra text.",
			valErr)
		retryMessages := append(messages,
			agent.OAIMessage{Role: "assistant", Content: text},
			agent.OAIMessage{Role: "user", Content: retryMsg},
		)
		retryCtx, retryCancel := context.WithTimeout(ctx, 60*time.Second)
		defer retryCancel()
		retryMsg2, _, _, retryErr := agent.CallAINonStreamingExported(retryCtx, provider, retryMessages, nil)
		if retryErr != nil {
			return nil, fmt.Errorf("dashboard generate: AI call failed on retry: %w", retryErr)
		}
		retryText, _ := retryMsg2.Content.(string)
		def, err = parseAndValidateDefinition(retryText)
		if err != nil {
			retryPreview := retryText
			if len(retryPreview) > 500 {
				retryPreview = retryPreview[:500] + "…"
			}
			logstore.Write("error",
				fmt.Sprintf("dashboard generate: retry also failed (%v). raw: %s", err, retryPreview),
				map[string]string{"name": name})
			return nil, fmt.Errorf("dashboard generate: invalid AI response (after retry): %w", err)
		}
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
