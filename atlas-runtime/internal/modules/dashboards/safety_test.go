package dashboards

import "testing"

func TestAllowedRuntimeEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"/status", true},
		{"/logs", true},
		{"/mind/thoughts", true},
		{"/workflows/abc", true},
		{"/workflows", true},
		{"/", false},
		{"", false},
		{"/not-on-list", false},
		{"/status/extra", false}, // exact-match entry doesn't accept prefix
	}
	for _, c := range cases {
		if got := allowedRuntimeEndpoint(c.endpoint); got != c.want {
			t.Errorf("allowedRuntimeEndpoint(%q) = %v, want %v", c.endpoint, got, c.want)
		}
	}
}

func TestValidateSelectSQL(t *testing.T) {
	ok := []string{
		"SELECT * FROM memories",
		"select count(*) from conversations",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SELECT * FROM t;", // trailing semicolon allowed
	}
	for _, q := range ok {
		if _, err := validateSelectSQL(q); err != nil {
			t.Errorf("expected ok for %q, got %v", q, err)
		}
	}
	bad := []string{
		"",
		"  ",
		"DELETE FROM memories",
		"SELECT 1; DROP TABLE x",
		"INSERT INTO t VALUES (1)",
		"PRAGMA foreign_keys = ON",
		"ATTACH DATABASE 'x' AS y",
	}
	for _, q := range bad {
		if _, err := validateSelectSQL(q); err == nil {
			t.Errorf("expected error for %q", q)
		}
	}
}

func TestValidateAnalyticsQuery(t *testing.T) {
	if err := validateAnalyticsQuery(""); err == nil {
		t.Error("empty should fail")
	}
	if err := validateAnalyticsQuery("bogus_query"); err == nil {
		t.Error("unknown should fail")
	}
	if err := validateAnalyticsQuery("messages_per_day"); err != nil {
		t.Errorf("known query should succeed: %v", err)
	}
}

func TestValidateLiveCompute(t *testing.T) {
	bad := []map[string]any{
		nil,
		{},
		{"prompt": "   "},
		{"prompt": "x", "inputs": []any{}},
		{"prompt": "x", "inputs": []any{""}},
		{"prompt": "x", "inputs": []any{"a"}},               // missing outputSchema
		{"prompt": "x", "inputs": []any{123}, "outputSchema": map[string]any{}},
	}
	for i, cfg := range bad {
		if err := validateLiveCompute(cfg); err == nil {
			t.Errorf("case %d expected error, got nil", i)
		}
	}
	ok := map[string]any{
		"prompt":       "summarize the feed",
		"inputs":       []any{"feed_source"},
		"outputSchema": map[string]any{"type": "object"},
	}
	if err := validateLiveCompute(ok); err != nil {
		t.Errorf("ok config should succeed: %v", err)
	}
}

func TestValidateGeneratedTSX(t *testing.T) {
	ok := `import { Card } from "@atlas/ui";
export default function W({ data }){ return <Card>{data.title}</Card>; }`
	if err := validateGeneratedTSX(ok); err != nil {
		t.Errorf("ok tsx rejected: %v", err)
	}

	bad := []string{
		``,
		`eval("alert(1)")`,
		`document.cookie = "x=1"`,
		`new Function("return 1")()`,
		`fetch("https://example.com")`,
		`const ls = localStorage.getItem("x")`,
		`window.parent.postMessage("x", "*")`,
	}
	for _, src := range bad {
		if err := validateGeneratedTSX(src); err == nil {
			t.Errorf("expected rejection for %q", src)
		}
	}
}

func TestIsImportAllowed(t *testing.T) {
	cases := map[string]bool{
		"@atlas/ui":        true,
		"@atlas/ui/card":   true,
		"preact":           true,
		"preact/hooks":     true,
		"react":            false,
		"lodash":           false,
		"@atlas":           false,
		"@atlas/other":     false,
	}
	for mod, want := range cases {
		if got := IsImportAllowed(mod); got != want {
			t.Errorf("IsImportAllowed(%q) = %v, want %v", mod, got, want)
		}
	}
}
