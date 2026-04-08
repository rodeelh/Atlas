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
      "kind": "metric | table | line_chart | bar_chart | list | news | markdown",
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
      "options": { "widget-specific field paths and display config — see per-kind docs below" }
    }
  ]
}

NO html/css/js fields. No custom_html kind. The built-in renderers handle all display.

────────────────────────────────────────────────────────────────
WIDGET KINDS — options reference
────────────────────────────────────────────────────────────────

  metric      — single number with optional trend indicator
    options.path        dot-path to the value in resolved data  e.g. "totalCostUSD"
    options.format      "currency" | "integer" | "percent" | "decimal"
    options.label       subtitle text shown below the value
    options.prefix      text before the value  e.g. "$"
    options.suffix      text after the value   e.g. " USD"
    options.changePath  dot-path to a change/delta value (shows ▲/▼ trend indicator)

  line_chart  — time-series line chart (Chart.js)
    options.seriesPath  dot-path to the array of data points  e.g. "history"
    options.x           key for the x-axis label  default "date"
    options.y           key for the y-axis value  default "value"  e.g. "price"
    options.color       hex color  default "#3b82f6"
    options.filled      true/false area fill under line  default true
    options.format      "currency" | "integer" | "decimal"  for y-axis tick labels

  bar_chart   — categorical bar chart (Chart.js)
    options.seriesPath  dot-path to the array
    options.x           key for bar labels  default "date"
    options.y           key for bar values  default "value"
    options.color       hex color  default "#6366f1"
    options.format      format for y-axis tick labels

  table       — scrollable rows
    options.columns     array of column names to show  e.g. ["date","price"]
    options.limit       max rows to render  default 100

  list        — bulleted list of items
    options.itemsPath   dot-path to array within data  e.g. "byModel"
    options.labelKey    key to use as list item label  e.g. "model"
    options.limit       max items

  news        — card feed for search results or any titled-item list
    options.itemsPath   dot-path to results array  default "results"
    options.titleKey    key for card title  default "title"
    options.bodyKey     key for card body text  default "description"
    options.urlKey      key for URL  default "url"
    options.limit       max cards  default 6

  markdown    — static formatted text (NO source needed)
    options.text        markdown string to render

────────────────────────────────────────────────────────────────
RUNTIME ENDPOINTS (source kind="runtime")
────────────────────────────────────────────────────────────────

  /status         → { isRunning, state, activeConversationCount, pendingApprovalCount,
                       runtimePort, startedAt, tokensIn, tokensOut }
  /usage/summary  → { totalTokens, totalInputTokens, totalOutputTokens, totalCostUSD,
                       turnCount,
                       byModel: [{provider, model, inputTokens, outputTokens, totalCostUSD, turnCount}] }
                    query: { days: "N" }  (omit for all-time)
                    NOTE: field is turnCount (AI turns). No tasks_executed / successful / failed.
  /usage/events   → { events: [{id, conversationId, provider, model, inputTokens, outputTokens,
                                 totalCostUSD, recordedAt}] }
                    query: { limit: "N" }
                    Use recordedAt as x-axis for time-series charts.
  /logs           → [{level, message, timestamp, meta}]
  /memories       → [{id, category, title, body, importance, createdAt, updatedAt}]
  /skills         → [{id, name, description, enabled, source, actions:[...]}]
  /workflows      → [{id, name, description, steps, createdAt}]
  /automations    → [{id, name, schedule, enabled, lastRun}]
  /forge/proposals → [{id, name, status, createdAt}]
  /forge/installed → [{id, name, version, installedAt}]

────────────────────────────────────────────────────────────────
SKILLS (source kind="skill") — structured data shapes
────────────────────────────────────────────────────────────────

The runtime resolver parses skill output into structured JSON before widgets see it.
Use the structured shapes below — NOT data.summary.

  finance.history  — args: { symbol: "AAPL", days: 30 }   (days is an integer, not a string)
    Resolved shape: { symbol: "AAPL", history: [{date: "2026-04-06", price: 258.86}, ...], summary: "..." }
    → Use line_chart with seriesPath="history", x="date", y="price"

  finance.quote    — args: { symbol: "AAPL" }
    Resolved shape: { price: 253.50, summary: "AAPL — 253.50 USD | +1.20 (+0.48%) ..." }
    → Use metric with path="price", format="currency"

  finance.portfolio — args: { symbols: "AAPL,MSFT,GOOG" }
    Resolved shape: { summary: "plain text multi-symbol quote" }
    → Use markdown (no source needed — put summary text in options.text) OR
      use list with a runtime SQL source if you need tabular display.
    Note: portfolio returns plain text only; the line_chart/bar_chart/metric kinds won't work.

  websearch.query  — args: { query: "...", count: 5 }
    Resolved shape: { results: [{title: "...", url: "...", description: "..."}, ...], summary: "..." }
    → Use news with itemsPath="results"

  weather.current  — args: { location: "..." }
    Resolved shape: { summary: "plain text weather description" }
    → Use markdown (put in options.text by rendering the summary as static text is not ideal;
      instead query weather and use a metric or markdown widget showing data.summary)
    Note: weather returns plain text only. Use markdown kind.

SKILL KIND RULE:
  finance.history  → line_chart or bar_chart (structured history array)
  finance.quote    → metric (structured price field)
  websearch.query  → news (structured results array)
  finance.portfolio / weather.current → markdown (plain text summary only)

────────────────────────────────────────────────────────────────
FALLBACK RULE
────────────────────────────────────────────────────────────────

If no runtime endpoint perfectly fits, use websearch.query with a precise query
(e.g. "AAPL stock price today", "Apple earnings news").
Never embed static/hardcoded data. websearch.query covers any topic.

────────────────────────────────────────────────────────────────
RULES
────────────────────────────────────────────────────────────────

  1. ONLY use widget kinds: metric, table, line_chart, bar_chart, list, news, markdown.
     Do NOT use custom_html — it will be rejected.
  2. ONLY use source kinds: runtime, skill, sql.
     Do NOT use source kind "web" — it will be rejected.
  3. Do NOT include html/css/js fields anywhere in the JSON.
  4. SQL must be a single SELECT (or WITH … SELECT). No writes, no DDL, no semicolons.
  5. Every widget needs a source EXCEPT markdown (text in options.text).
  6. Grid: 12 columns wide. Widgets must not overlap.
  7. Honor any colors, themes, or layout preferences the user specifies.
  8. Output ONE valid JSON object. Nothing else.`

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
		} else if w.Kind != WidgetKindMarkdown {
			// All widget kinds except markdown must have a source.
			return fmt.Errorf("widget %q (%s) is missing a source", w.ID, w.Kind)
		}

		// Strip any html/css/js fields — custom_html is not allowed in AI
		// generation. Built-in widgets have pre-built renderers.
		w.HTML = ""
		w.CSS  = ""
		w.JS   = ""
	}
	return nil
}

// isAllowedGeneratedKind reports whether the AI is permitted to use kind.
// custom_html is intentionally excluded — AI-generated JS is unreliable.
// Use the built-in widget kinds which have pre-built renderers.
func isAllowedGeneratedKind(kind string) bool {
	switch kind {
	case WidgetKindMetric,
		WidgetKindTable,
		WidgetKindLineChart,
		WidgetKindBarChart,
		WidgetKindMarkdown,
		WidgetKindList,
		WidgetKindNews:
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
