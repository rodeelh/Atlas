package dashboards

// safety_test.go — pure unit tests for the allowlist + validators in safety.go.
// No I/O. These probes are the first line of defense; they must be ruthless.

import "testing"

func TestAllowedRuntimeEndpoint_AllowsKnown(t *testing.T) {
	cases := []string{
		"/status",
		"/logs",
		"/memories",
		"/usage/summary",
		"/usage/events",
		"/workflows",
		"/workflows/abc/runs",
		"/automations",
		"/forge/proposals",
	}
	for _, ep := range cases {
		if !allowedRuntimeEndpoint(ep) {
			t.Errorf("expected %q to be allowed", ep)
		}
	}
}

func TestAllowedRuntimeEndpoint_RejectsUnknown(t *testing.T) {
	cases := []string{
		"",
		"status",          // missing leading slash
		"/control",        // not on the list
		"/auth/token",     // sensitive
		"/messages",       // not on the list (yet)
		"/usagex/summary", // partial-match attempt
	}
	for _, ep := range cases {
		if allowedRuntimeEndpoint(ep) {
			t.Errorf("expected %q to be rejected", ep)
		}
	}
}

func TestValidateWebURL_RejectsUnsafeSchemes(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/data",
		"javascript:alert(1)",
		"",
	}
	for _, raw := range cases {
		if _, err := validateWebURL(raw); err == nil {
			t.Errorf("expected %q to be rejected", raw)
		}
	}
}

func TestValidateWebURL_RejectsLocalhostNames(t *testing.T) {
	cases := []string{
		"http://localhost/",
		"http://LOCALHOST:1984/control",
		"http://my.local/",
		"http://127.0.0.1:1984/control",
		"http://10.0.0.1/admin",
		"http://192.168.1.1/",
		"http://[::1]/",
		"http://0.0.0.0/",
	}
	for _, raw := range cases {
		if _, err := validateWebURL(raw); err == nil {
			t.Errorf("expected %q to be rejected as private/local, got nil", raw)
		}
	}
}

func TestValidateWebURL_AcceptsPublicURL(t *testing.T) {
	// Use a public IP literal so the test does not depend on DNS resolution.
	if _, err := validateWebURL("https://1.1.1.1/"); err != nil {
		t.Errorf("expected public URL to be accepted, got %v", err)
	}
}

func TestValidateSelectSQL_AcceptsSimpleSelect(t *testing.T) {
	cleaned, err := validateSelectSQL("SELECT id, name FROM memories WHERE category = 'commitment'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleaned == "" {
		t.Error("expected cleaned query, got empty")
	}
}

func TestValidateSelectSQL_AcceptsCTE(t *testing.T) {
	q := "WITH recent AS (SELECT * FROM memories ORDER BY updated_at DESC) SELECT * FROM recent"
	if _, err := validateSelectSQL(q); err != nil {
		t.Errorf("WITH … SELECT should be accepted, got %v", err)
	}
}

func TestValidateSelectSQL_AcceptsTrailingSemicolon(t *testing.T) {
	if _, err := validateSelectSQL("SELECT 1;"); err != nil {
		t.Errorf("trailing ; should be tolerated, got %v", err)
	}
}

func TestValidateSelectSQL_RejectsWritesAndDDL(t *testing.T) {
	cases := []string{
		"DELETE FROM memories",
		"UPDATE memories SET title = 'x'",
		"INSERT INTO memories VALUES (1)",
		"DROP TABLE memories",
		"ALTER TABLE memories ADD COLUMN x INT",
		"CREATE TABLE evil(x int)",
		"REPLACE INTO memories VALUES (1)",
		"VACUUM",
		"ATTACH DATABASE 'evil.db' AS evil",
		"DETACH DATABASE main",
		"PRAGMA writable_schema = ON",
		"BEGIN; SELECT 1; COMMIT",
		"select 1; delete from memories",
		"-- comment\nDELETE FROM memories", // doesn't start with SELECT/WITH
		"",
	}
	for _, q := range cases {
		if _, err := validateSelectSQL(q); err == nil {
			t.Errorf("expected %q to be rejected", q)
		}
	}
}

func TestValidateSelectSQL_RejectsForbiddenKeywordEvenInsideSelect(t *testing.T) {
	// A SELECT that smuggles a write keyword (e.g. via a subquery) is rejected.
	q := "SELECT * FROM (DELETE FROM memories RETURNING *)"
	if _, err := validateSelectSQL(q); err == nil {
		t.Error("expected smuggled DELETE to be rejected")
	}
}

func TestContainsKeyword_WordBoundary(t *testing.T) {
	if !containsKeyword("select id from foo where x = 1", "select") {
		t.Error("should match select at start")
	}
	if containsKeyword("selectivity_score", "select") {
		t.Error("should not match within identifier")
	}
	if !containsKeyword("a delete b", "delete") {
		t.Error("should match delete with surrounding spaces")
	}
	if containsKeyword("undeleted", "delete") {
		t.Error("should not match delete inside undeleted")
	}
}
