package dashboards

// generate_test.go — covers AI-output parsing and the schema/safety validator.
//
// We don't exercise the network path through agent.CallAINonStreamingExported
// here — that would either need a real API key or a much heavier fake. The
// failure modes that matter (malformed JSON, schema drift, allowlist bypass,
// SQL smuggling, custom_html rejection) are all triggered through
// parseAndValidateDefinition with hand-crafted inputs.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"atlas-runtime-go/internal/agent"
)

func TestStripJSONFences(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"```json\n{}\n```":  "{}",
		"```\n{}\n```":      "{}",
		"  ```json\n{}\n``": "{}\n``", // unclosed fence — leaves trailing
	}
	for in, want := range cases {
		got := strings.TrimSpace(stripJSONFences(in))
		want = strings.TrimSpace(want)
		if got != want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractFirstJSONObject_FindsFirstBalancedBlock(t *testing.T) {
	got, err := extractFirstJSONObject(`prefix {"a": "b{c}", "n": 1} trailing`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `{"a": "b{c}", "n": 1}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractFirstJSONObject_NoBraces(t *testing.T) {
	if _, err := extractFirstJSONObject("hello"); err == nil {
		t.Error("expected error when no JSON object present")
	}
}

func TestExtractFirstJSONObject_Unbalanced(t *testing.T) {
	if _, err := extractFirstJSONObject(`{"a": 1`); err == nil {
		t.Error("expected error on unbalanced braces")
	}
}

// ── parseAndValidateDefinition: happy path ───────────────────────────────────

func TestParseAndValidate_HappyPath_RuntimeWidget(t *testing.T) {
	resp := `{
  "name": "System Health",
  "widgets": [
    {
      "id": "status",
      "kind": "markdown",
      "gridX": 0, "gridY": 0, "gridW": 12, "gridH": 2,
      "source": { "kind": "runtime", "endpoint": "/status" }
    }
  ]
}`
	def, err := parseAndValidateDefinition(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Name != "System Health" {
		t.Errorf("name: %q", def.Name)
	}
	if len(def.Widgets) != 1 {
		t.Fatalf("widgets: %+v", def.Widgets)
	}
}

func TestParseAndValidate_HappyPath_WithCodeFences(t *testing.T) {
	resp := "```json\n{\"name\":\"X\",\"widgets\":[{\"id\":\"a\",\"kind\":\"metric\",\"source\":{\"kind\":\"runtime\",\"endpoint\":\"/usage/summary\"}}]}\n```"
	def, err := parseAndValidateDefinition(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Widgets[0].Kind != WidgetKindMetric {
		t.Error("widget kind drift through fences")
	}
}

func TestParseAndValidate_HappyPath_PromptWithProseAroundJSON(t *testing.T) {
	resp := `Here's the dashboard you asked for:
{"name":"X","widgets":[{"id":"a","kind":"markdown","source":{"kind":"runtime","endpoint":"/status"}}]}
Hope this helps!`
	if _, err := parseAndValidateDefinition(resp); err != nil {
		t.Errorf("expected to tolerate surrounding prose, got %v", err)
	}
}

// ── validation rejection cases ───────────────────────────────────────────────

func TestParseAndValidate_RejectsMissingName(t *testing.T) {
	resp := `{"widgets":[{"id":"a","kind":"markdown","source":{"kind":"runtime","endpoint":"/status"}}]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected missing-name rejection")
	}
}

func TestParseAndValidate_RejectsZeroWidgets(t *testing.T) {
	resp := `{"name":"empty","widgets":[]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected zero-widgets rejection")
	}
}

func TestParseAndValidate_RejectsTooManyWidgets(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"name":"big","widgets":[`)
	for i := 0; i < 30; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"w` + itoa(i) + `","kind":"markdown","source":{"kind":"runtime","endpoint":"/status"}}`)
	}
	sb.WriteString(`]}`)
	if _, err := parseAndValidateDefinition(sb.String()); err == nil {
		t.Error("expected too-many-widgets rejection")
	}
}

func TestParseAndValidate_RejectsDuplicateWidgetIDs(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"markdown","source":{"kind":"runtime","endpoint":"/status"}},
    {"id":"a","kind":"markdown","source":{"kind":"runtime","endpoint":"/logs"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected duplicate-id rejection")
	}
}

func TestParseAndValidate_RejectsUnknownWidgetKind(t *testing.T) {
	resp := `{"name":"X","widgets":[{"id":"a","kind":"holo3d","source":{"kind":"runtime","endpoint":"/status"}}]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected unknown-kind rejection")
	}
}

func TestParseAndValidate_RejectsCustomHTMLWithBody(t *testing.T) {
	resp := `{"name":"X","widgets":[{"id":"a","kind":"custom_html","html":"<div id=root></div>","js":"window.atlasRender=function(){};"}]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected custom_html rejection for AI-generated dashboards")
	}
}

func TestParseAndValidate_RejectsCustomHTMLWithoutBody(t *testing.T) {
	resp := `{"name":"X","widgets":[{"id":"a","kind":"custom_html"}]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected custom_html without html field to be rejected")
	}
}

func TestParseAndValidate_RejectsCustomHTMLWithSource(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[{
    "id":"a","kind":"custom_html","html":"<div></div>",
    "source":{"kind":"runtime","endpoint":"/status"}
  }]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected custom_html with source to be rejected")
	}
}

func TestParseAndValidate_RejectsWebSourceKind(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"table","source":{"kind":"web","url":"https://evil.example/"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected web-source rejection")
	}
}

func TestParseAndValidate_RejectsRuntimeEndpointNotOnAllowlist(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"table","source":{"kind":"runtime","endpoint":"/control"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected disallowed runtime endpoint rejection")
	}
}

func TestParseAndValidate_RejectsRuntimeEndpointMissingSlash(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"table","source":{"kind":"runtime","endpoint":"status"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected leading-slash rejection")
	}
}

func TestParseAndValidate_RejectsSQLWrites(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"table","source":{"kind":"sql","sql":"DELETE FROM memories"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected SQL write rejection")
	}
}

func TestParseAndValidate_AcceptsSelectSQL(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"id":"a","kind":"table","source":{"kind":"sql","sql":"SELECT id, title FROM memories LIMIT 10"}}
  ]
}`
	if _, err := parseAndValidateDefinition(resp); err != nil {
		t.Errorf("expected SELECT to be accepted, got %v", err)
	}
}

func TestParseAndValidate_NonMarkdownNeedsSource(t *testing.T) {
	resp := `{"name":"X","widgets":[{"id":"a","kind":"metric"}]}`
	if _, err := parseAndValidateDefinition(resp); err == nil {
		t.Error("expected non-markdown-without-source rejection")
	}
}

func TestParseAndValidate_MarkdownWithoutSourceIsOK(t *testing.T) {
	resp := `{"name":"X","widgets":[{"id":"a","kind":"markdown","options":{"text":"Hello"}}]}`
	if _, err := parseAndValidateDefinition(resp); err != nil {
		t.Errorf("markdown widgets may omit source, got %v", err)
	}
}

func TestParseAndValidate_StripsHTMLFromNonCustomKinds(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {
      "id":"a","kind":"markdown","html":"<script>x</script>","js":"alert(1)",
      "source":{"kind":"runtime","endpoint":"/status"}
    }
  ]
}`
	def, err := parseAndValidateDefinition(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Widgets[0].HTML != "" || def.Widgets[0].JS != "" {
		t.Errorf("HTML/JS should be stripped from non-custom widgets, got %+v", def.Widgets[0])
	}
}

func TestParseAndValidate_AssignsMissingWidgetIDs(t *testing.T) {
	resp := `{
  "name":"X",
  "widgets":[
    {"kind":"markdown","source":{"kind":"runtime","endpoint":"/status"}},
    {"kind":"markdown","source":{"kind":"runtime","endpoint":"/logs"}}
  ]
}`
	def, err := parseAndValidateDefinition(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Widgets[0].ID == "" || def.Widgets[1].ID == "" {
		t.Errorf("widget IDs not assigned: %+v", def.Widgets)
	}
}

func TestParseAndValidate_GarbageInput(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"definitely not json",
		"{",
		"```json\n{not json}\n```",
	}
	for _, in := range cases {
		if _, err := parseAndValidateDefinition(in); err == nil {
			t.Errorf("expected garbage %q to be rejected", in)
		}
	}
}

// ── Generate top-level wiring errors ─────────────────────────────────────────

func TestGenerate_NoResolver(t *testing.T) {
	if _, err := Generate(context.Background(), nil, "x", "x"); err == nil {
		t.Error("expected error when no provider resolver configured")
	}
}

func TestGenerate_EmptyPrompt_ResolverNotCalled(t *testing.T) {
	resolver := func() (agent.ProviderConfig, error) {
		t.Fatal("resolver should not be called when prompt is empty")
		return agent.ProviderConfig{}, nil
	}
	if _, err := Generate(context.Background(), resolver, "x", "  "); err == nil {
		t.Error("expected empty-prompt rejection")
	}
}

func TestGenerate_ResolverError_Bubbles(t *testing.T) {
	resolver := func() (agent.ProviderConfig, error) {
		return agent.ProviderConfig{}, errors.New("no provider configured")
	}
	if _, err := Generate(context.Background(), resolver, "x", "make me a dashboard"); err == nil {
		t.Error("expected resolver error to bubble up")
	}
}

// itoa is a tiny strconv-free int → string for the duplicate-id stress test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
