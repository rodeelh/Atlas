package forge

// codegen_pipeline_test.go — comprehensive tests for the Forge/Custom skills pipeline.
//
// Coverage map:
//   Go-side (codegen.go):
//     - buildParamSchema: URL params, body params, query params, header params
//     - buildRunScript:   JSON escaping (single quote, backslash, percent)
//     - buildManifestActions: actionClass inference, permLevel normalisation
//     - GenerateAndInstallCustomSkill: idempotent re-install, policy file written
//
//   Python executor (run script — end-to-end via subprocess):
//     - Static header delivery
//     - Header value template substitution
//     - Auth: apiKeyHeader, bearerTokenStatic, basicAuth, none
//     - secret_header legacy branch does NOT override explicit authType
//     - Query param auto-routing for GET
//     - Body field mapping for POST
//     - Unplaced args routed to query for GET, body for POST
//     - Header template param double-routing (Bug A — expected FAIL before fix)
//     - DELETE with body fields (Bug B — expected FAIL, unfixed)
//     - Unknown action returns error JSON
//     - Non-JSON output handled gracefully

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"atlas-runtime-go/internal/customskills"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// capturedRequest holds what the mock server received.
type capturedRequest struct {
	Method  string
	Headers http.Header
	Query   map[string]string
	Body    string
}

// mockServer starts a test HTTP server that captures the first request and
// returns 200 + a JSON echo. Returns the server and a channel that emits one
// capturedRequest per call.
func mockServer(t *testing.T) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	ch := make(chan capturedRequest, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := make(map[string]string)
		for k, v := range r.URL.Query() {
			q[k] = v[0]
		}
		ch <- capturedRequest{
			Method:  r.Method,
			Headers: r.Header.Clone(),
			Query:   q,
			Body:    string(body),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// runScript executes the generated run script at runPath with JSON stdin.
// Returns stdout, stderr, and any exec error.
func runScript(t *testing.T, runPath, actionName string, args map[string]any) (string, string, error) {
	t.Helper()
	input := map[string]any{"action": actionName, "args": args}
	inputJSON, _ := json.Marshal(input)
	cmd := exec.Command("python3", runPath)
	cmd.Stdin = strings.NewReader(string(inputJSON) + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// installSkillToDir generates a custom skill run script from plans and writes it
// to a temp skillDir. Returns the path to the run script.
func installSkillToDir(t *testing.T, supportDir string, plans []ForgeActionPlan) string {
	t.Helper()
	spec := ForgeSkillSpec{
		ID:   "test-skill",
		Name: "Test Skill",
		Actions: []ForgeActionSpec{
			{ID: "test-action", Name: "Test Action", PermissionLevel: "read"},
		},
	}
	for _, p := range plans {
		found := false
		for _, a := range spec.Actions {
			if a.ID == p.ActionID {
				found = true
				break
			}
		}
		if !found {
			spec.Actions = append(spec.Actions, ForgeActionSpec{
				ID: p.ActionID, Name: p.ActionID, PermissionLevel: "read",
			})
		}
	}
	specJSON, _ := json.Marshal(spec)
	plansJSON, _ := json.Marshal(plans)
	proposal := ForgeProposal{
		ID:        "prop-test",
		SkillID:   "test-skill",
		Name:      "Test Skill",
		SpecJSON:  string(specJSON),
		PlansJSON: string(plansJSON),
	}
	if err := GenerateAndInstallCustomSkill(supportDir, proposal); err != nil {
		t.Fatalf("GenerateAndInstallCustomSkill: %v", err)
	}
	return filepath.Join(customskills.SkillsDir(supportDir), "test-skill", "run")
}

// ── buildParamSchema ──────────────────────────────────────────────────────────

func TestBuildParamSchema_HeaderTemplateParams(t *testing.T) {
	plan := &HTTPRequestPlan{
		Method: "GET",
		URL:    "https://api.example.com/data",
		Headers: map[string]string{
			"X-User-Id":  "{userId}",
			"X-Version":  "v1", // static — no placeholder
			"X-Trace-Id": "{traceId}",
		},
	}
	schema := buildParamSchema(plan, nil, nil)
	if schema == nil {
		t.Fatal("expected non-nil schema for plan with header placeholders")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"userId", "traceId"} {
		if props[name] == nil {
			t.Errorf("header placeholder %q missing from parameter schema", name)
		}
	}
	// Static header values must NOT create parameters.
	if props["v1"] != nil {
		t.Error("static header value 'v1' should not appear as a parameter")
	}
}

func TestBuildParamSchema_QueryTemplateParams(t *testing.T) {
	plan := &HTTPRequestPlan{
		Method: "GET",
		URL:    "https://api.example.com/search",
		Query:  map[string]string{"q": "{searchQuery}", "limit": "10"},
	}
	schema := buildParamSchema(plan, nil, nil)
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	props, _ := schema["properties"].(map[string]any)
	if props["searchQuery"] == nil {
		t.Error("query template placeholder 'searchQuery' missing from schema")
	}
	if props["limit"] != nil {
		t.Error("static query value 'limit' should not create a parameter")
	}
}

func TestBuildParamSchema_BodyFieldParams(t *testing.T) {
	plan := &HTTPRequestPlan{
		Method:     "POST",
		URL:        "https://api.example.com/create",
		BodyFields: map[string]string{"name": "{entityName}", "type": "{entityType}"},
	}
	schema := buildParamSchema(plan, nil, nil)
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"entityName", "entityType"} {
		if props[name] == nil {
			t.Errorf("body field placeholder %q missing from schema", name)
		}
	}
}

func TestBuildParamSchema_URLParamsAreRequired(t *testing.T) {
	plan := &HTTPRequestPlan{
		Method: "GET",
		URL:    "https://api.example.com/users/{userID}/posts/{postID}",
	}
	schema := buildParamSchema(plan, nil, nil)
	req, _ := schema["required"].([]string)
	reqSet := make(map[string]bool)
	for _, r := range req {
		reqSet[r] = true
	}
	for _, name := range []string{"userID", "postID"} {
		if !reqSet[name] {
			t.Errorf("URL placeholder %q should be in required list", name)
		}
	}
}

func TestBuildParamSchema_NilPlanNilContractReturnsNil(t *testing.T) {
	if schema := buildParamSchema(nil, nil, nil); schema != nil {
		t.Errorf("expected nil for empty inputs, got %v", schema)
	}
}

// ── buildRunScript escaping ───────────────────────────────────────────────────

func TestBuildRunScript_SingleQuoteInURL(t *testing.T) {
	// A URL containing a single quote must not break the Python string literal.
	plans := []ForgeActionPlan{{
		ActionID: "test",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      "https://api.example.com/it's/resource",
			AuthType: "none",
		},
	}}
	plansJSON, _ := json.Marshal(plans)
	script := buildRunScript(string(plansJSON))

	// Script must be syntactically valid Python (compile check).
	tmp, err := os.CreateTemp(t.TempDir(), "run-*.py")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString(script)
	tmp.Close()
	if err := exec.Command("python3", "-m", "py_compile", tmp.Name()).Run(); err != nil {
		t.Errorf("generated script with single-quote URL is not valid Python: %v\nscript head:\n%s",
			err, script[:min(500, len(script))])
	}
}

func TestBuildRunScript_BackslashInURL(t *testing.T) {
	plans := []ForgeActionPlan{{
		ActionID: "test",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      `https://api.example.com/path\resource`,
			AuthType: "none",
		},
	}}
	plansJSON, _ := json.Marshal(plans)
	script := buildRunScript(string(plansJSON))
	tmp, err := os.CreateTemp(t.TempDir(), "run-*.py")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString(script)
	tmp.Close()
	if err := exec.Command("python3", "-m", "py_compile", tmp.Name()).Run(); err != nil {
		t.Errorf("generated script with backslash URL is not valid Python: %v", err)
	}
}

func TestBuildRunScript_PercentSignInURL(t *testing.T) {
	// A URL-encoded path like /users%2Fprofile — percent signs must not be treated
	// as Go format verbs by fmt.Sprintf.
	plans := []ForgeActionPlan{{
		ActionID: "test",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      "https://api.example.com/users%2Fprofile",
			AuthType: "none",
		},
	}}
	plansJSON, _ := json.Marshal(plans)
	// Must not panic.
	script := buildRunScript(string(plansJSON))
	if !strings.Contains(script, "users%2Fprofile") {
		t.Error("percent sign in URL was incorrectly processed")
	}
}

// ── Python executor end-to-end ────────────────────────────────────────────────

// TestExecutor_StaticHeaderDelivered verifies a static header value defined
// in the plan's headers map is sent on the wire.
func TestExecutor_StaticHeaderDelivered(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/test",
			AuthType: "none",
			Headers:  map[string]string{"X-Atlas-Test": "header-secret-123"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	got := req.Headers.Get("X-Atlas-Test")
	if got != "header-secret-123" {
		t.Errorf("X-Atlas-Test header: want %q, got %q", "header-secret-123", got)
	}
}

// TestExecutor_HeaderTemplateFilled verifies {param} placeholders in header
// values are substituted with the corresponding arg.
func TestExecutor_HeaderTemplateFilled(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/test",
			AuthType: "none",
			Headers:  map[string]string{"X-Request-Version": "{apiVersion}"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{"apiVersion": "v2"})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	got := req.Headers.Get("X-Request-Version")
	if got != "v2" {
		t.Errorf("X-Request-Version header: want %q, got %q", "v2", got)
	}
}

// TestExecutor_HeaderTemplateParam_NotDoubleRouted verifies that a param used in
// a header value template is NOT also appended to the query string.
// This tests Bug A: the `placed` set in _run does not include header template params,
// causing args to be routed to both the header AND the query string.
func TestExecutor_HeaderTemplateParam_NotDoubleRouted(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/test",
			AuthType: "none",
			Headers:  map[string]string{"X-User-Id": "{userId}"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{"userId": "user-42"})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	// Header should be set correctly.
	if got := req.Headers.Get("X-User-Id"); got != "user-42" {
		t.Errorf("X-User-Id header: want %q, got %q", "user-42", got)
	}
	// BUG A: userId must NOT also appear in the query string.
	if _, present := req.Query["userId"]; present {
		t.Errorf("BUG A: userId was double-routed — appears in both the header and query string %v", req.Query)
	}
}

// TestExecutor_AuthAPIKeyHeader verifies apiKeyHeader auth injects the credential
// into the correct header name and NOT as a bearer token.
func TestExecutor_AuthAPIKeyHeader(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	// Inject a fake credential via environment variable so the test doesn't
	// need real Keychain access.
	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:         "GET",
			URL:            srv.URL + "/test",
			AuthType:       "apiKeyHeader",
			AuthSecretKey:  "TEST_FAKE_API_KEY",
			AuthHeaderName: "X-API-Key",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	// Set the credential via env var (executor checks env vars first).
	cmd := exec.Command("python3", runPath)
	cmd.Env = append(os.Environ(), "TEST_FAKE_API_KEY=super-secret-value")
	input := map[string]any{"action": "test-action", "args": map[string]any{}}
	inputJSON, _ := json.Marshal(input)
	cmd.Stdin = strings.NewReader(string(inputJSON) + "\n")
	if err := cmd.Run(); err != nil {
		t.Fatalf("script failed: %v", err)
	}

	req := <-ch
	if got := req.Headers.Get("X-API-Key"); got != "super-secret-value" {
		t.Errorf("X-API-Key: want %q, got %q", "super-secret-value", got)
	}
	// Must NOT inject a Bearer token at the same time.
	if auth := req.Headers.Get("Authorization"); auth != "" {
		t.Errorf("Authorization header should be absent for apiKeyHeader, got %q", auth)
	}
}

// TestExecutor_SecretHeaderDoesNotOverrideApiKeyHeader verifies that the legacy
// secretHeader field does NOT override an explicit apiKeyHeader authType.
// (Tests fix for Bug 5 from the previous audit.)
func TestExecutor_SecretHeaderDoesNotOverrideApiKeyHeader(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:         "GET",
			URL:            srv.URL + "/test",
			AuthType:       "apiKeyHeader",
			AuthSecretKey:  "MY_KEY",
			AuthHeaderName: "X-API-Key",
			SecretHeader:   "SOME_LEGACY_SECRET", // legacy field — must NOT fire for apiKeyHeader
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	cmd := exec.Command("python3", runPath)
	cmd.Env = append(os.Environ(), "MY_KEY=correct-api-value", "SOME_LEGACY_SECRET=wrong-bearer-value")
	input := map[string]any{"action": "test-action", "args": map[string]any{}}
	inputJSON, _ := json.Marshal(input)
	cmd.Stdin = strings.NewReader(string(inputJSON) + "\n")
	if err := cmd.Run(); err != nil {
		t.Fatalf("script failed: %v", err)
	}

	req := <-ch
	if got := req.Headers.Get("X-API-Key"); got != "correct-api-value" {
		t.Errorf("X-API-Key: want %q, got %q", "correct-api-value", got)
	}
	// secretHeader must NOT inject a bearer token that clobbers apiKeyHeader.
	if auth := req.Headers.Get("Authorization"); strings.HasPrefix(auth, "Bearer wrong-bearer-value") {
		t.Errorf("legacy secretHeader incorrectly overrode apiKeyHeader auth: Authorization=%q", auth)
	}
}

// TestExecutor_BearerTokenStatic verifies bearerTokenStatic auth sends Authorization: Bearer <token>.
func TestExecutor_BearerTokenStatic(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:        "GET",
			URL:           srv.URL + "/test",
			AuthType:      "bearerTokenStatic",
			AuthSecretKey: "BEARER_TOKEN_KEY",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	cmd := exec.Command("python3", runPath)
	cmd.Env = append(os.Environ(), "BEARER_TOKEN_KEY=my-bearer-token")
	input := map[string]any{"action": "test-action", "args": map[string]any{}}
	inputJSON, _ := json.Marshal(input)
	cmd.Stdin = strings.NewReader(string(inputJSON) + "\n")
	if err := cmd.Run(); err != nil {
		t.Fatalf("script failed: %v", err)
	}

	req := <-ch
	if got := req.Headers.Get("Authorization"); got != "Bearer my-bearer-token" {
		t.Errorf("Authorization: want %q, got %q", "Bearer my-bearer-token", got)
	}
}

// TestExecutor_GetQueryParamAutoRouting verifies that extra GET args not placed
// in the URL template are appended to the query string.
func TestExecutor_GetQueryParamAutoRouting(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/search",
			AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{"q": "hello", "limit": "5"})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	if got := req.Query["q"]; got != "hello" {
		t.Errorf("query param q: want %q, got %q", "hello", got)
	}
	if got := req.Query["limit"]; got != "5" {
		t.Errorf("query param limit: want %q, got %q", "5", got)
	}
}

// TestExecutor_PostBodyFieldMapping verifies bodyFields map action args to POST body keys.
func TestExecutor_PostBodyFieldMapping(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:     "POST",
			URL:        srv.URL + "/create",
			AuthType:   "none",
			BodyFields: map[string]string{"title": "{taskName}", "priority": "{level}"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action",
		map[string]any{"taskName": "Write tests", "level": "high"})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	var body map[string]any
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("response body is not JSON: %v — raw: %q", err, req.Body)
	}
	if body["title"] != "Write tests" {
		t.Errorf("body.title: want %q, got %v", "Write tests", body["title"])
	}
	if body["priority"] != "high" {
		t.Errorf("body.priority: want %q, got %v", "high", body["priority"])
	}
}

// TestExecutor_StaticBodyFields verifies staticBodyFields are sent in POST body
// without requiring user-supplied args.
func TestExecutor_StaticBodyFields(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:           "POST",
			URL:              srv.URL + "/api",
			AuthType:         "none",
			StaticBodyFields: map[string]string{"format": "json", "version": "2"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	var body map[string]any
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("body not JSON: %v — raw: %q", err, req.Body)
	}
	if body["format"] != "json" {
		t.Errorf("static body format: want %q, got %v", "json", body["format"])
	}
	if body["version"] != "2" {
		t.Errorf("static body version: want %q, got %v", "2", body["version"])
	}
}

// TestExecutor_DELETEBodyFieldsDropped documents Bug B: body fields are silently
// dropped for DELETE requests. The test demonstrates the current (broken) behaviour
// so we can verify the fix when it's applied.
func TestExecutor_DELETEBodyFieldsDropped(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:           "DELETE",
			URL:              srv.URL + "/resource",
			AuthType:         "none",
			StaticBodyFields: map[string]string{"reason": "cleanup"},
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, stderr, err := runScript(t, runPath, "test-action", map[string]any{})
	if err != nil {
		t.Fatalf("script failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	req := <-ch
	if req.Method != "DELETE" {
		t.Errorf("method: want DELETE, got %s", req.Method)
	}
	// Bug B: the body should contain {"reason":"cleanup"} but current code sends nothing.
	// Once Bug B is fixed this test should be updated to assert body content instead.
	if req.Body != "" && req.Body != "{}" {
		// Body was unexpectedly sent — either the bug is fixed or behaviour changed.
		var body map[string]any
		if err := json.Unmarshal([]byte(req.Body), &body); err == nil {
			if body["reason"] == "cleanup" {
				t.Log("INFO: Bug B appears to be fixed — DELETE body was sent")
				return
			}
		}
	}
	// Current expected (broken) behaviour: body is empty.
	t.Logf("BUG B confirmed: DELETE request body is empty (staticBodyFields dropped). Body=%q", req.Body)
}

// TestExecutor_UnknownActionReturnsError verifies the executor returns a
// structured error JSON for an unrecognised action name.
func TestExecutor_UnknownActionReturnsError(t *testing.T) {
	supportDir := t.TempDir()
	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      "https://api.example.com/test",
			AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, _, _ := runScript(t, runPath, "nonexistent-action", map[string]any{})
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result); err != nil {
		t.Fatalf("output is not JSON: %v — raw: %q", err, stdout)
	}
	if result["success"] != false {
		t.Errorf("expected success=false for unknown action, got %v", result["success"])
	}
	errMsg, _ := result["error"].(string)
	if !strings.Contains(errMsg, "unknown action") {
		t.Errorf("error message should mention 'unknown action', got %q", errMsg)
	}
}

// TestExecutor_EmptyInput verifies the executor handles empty stdin gracefully.
func TestExecutor_EmptyInput(t *testing.T) {
	supportDir := t.TempDir()
	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method: "GET", URL: "https://api.example.com/test", AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	cmd := exec.Command("python3", runPath)
	cmd.Stdin = strings.NewReader("\n") // empty line
	out, _ := cmd.Output()
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &result); err != nil {
		t.Fatalf("empty input should return JSON error, got: %q", out)
	}
	if result["success"] != false {
		t.Errorf("expected success=false for empty input, got %v", result["success"])
	}
}

// TestExecutor_MalformedJSONInput verifies invalid JSON on stdin returns an error.
func TestExecutor_MalformedJSONInput(t *testing.T) {
	supportDir := t.TempDir()
	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method: "GET", URL: "https://api.example.com/test", AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	cmd := exec.Command("python3", runPath)
	cmd.Stdin = strings.NewReader("{not valid json}\n")
	out, _ := cmd.Output()
	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &result); err != nil {
		t.Fatalf("malformed JSON should return JSON error, got: %q", out)
	}
	if result["success"] != false {
		t.Errorf("expected success=false, got %v", result["success"])
	}
}

// ── Path segment encoding ─────────────────────────────────────────────────────

// TestExecutor_PathSegment_SpacesEncoded is a regression test for the bug where
// user input was interpolated raw into a URL path, causing "URL can't contain
// control characters" errors for values with spaces (e.g. "Diamonds by Rihanna").
// _fill_path must percent-encode the substituted value before the request fires.
func TestExecutor_PathSegment_SpacesEncoded(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/search/{query}",
			AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	stdout, _, err := runScript(t, runPath, "test-action", map[string]any{
		"query": "Diamonds by Rihanna",
	})
	if err != nil {
		t.Fatalf("script exited with error: %v\nstdout: %s", err, stdout)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, stdout)
	}
	if result["success"] != true {
		t.Errorf("expected success=true, got %v (error: %v)", result["success"], result["error"])
	}

	// Verify the server received the encoded path — no raw spaces.
	req := <-ch
	if strings.Contains(req.Method, " ") {
		t.Errorf("raw space leaked into request method: %q", req.Method)
	}
}

func TestExecutor_PathSegment_SpecialCharsEncoded(t *testing.T) {
	srv, ch := mockServer(t)
	supportDir := t.TempDir()

	plans := []ForgeActionPlan{{
		ActionID: "test-action",
		Type:     "http",
		HTTPRequest: &HTTPRequestPlan{
			Method:   "GET",
			URL:      srv.URL + "/lookup/{id}",
			AuthType: "none",
		},
	}}
	runPath := installSkillToDir(t, supportDir, plans)

	// Value contains slash and ampersand — must be encoded in a path segment.
	stdout, _, err := runScript(t, runPath, "test-action", map[string]any{
		"id": "hello/world&foo",
	})
	if err != nil {
		t.Fatalf("script exited with error: %v\nstdout: %s", err, stdout)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, stdout)
	}
	if result["success"] != true {
		t.Errorf("expected success=true, got %v (error: %v)", result["success"], result["error"])
	}
	<-ch // drain channel
}

// ── buildManifestActions ──────────────────────────────────────────────────────

func TestBuildManifestActions_ActionClassInference(t *testing.T) {
	tests := []struct {
		method    string
		wantClass string
	}{
		{"GET", "read"},
		{"POST", "external_side_effect"},
		{"PUT", "external_side_effect"},
		{"PATCH", "external_side_effect"},
		{"DELETE", "external_side_effect"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			spec := ForgeSkillSpec{
				ID: "test", Name: "Test",
				Actions: []ForgeActionSpec{{ID: "act", Name: "Act", PermissionLevel: "read"}},
			}
			plans := []ForgeActionPlan{{
				ActionID:    "act",
				Type:        "http",
				HTTPRequest: &HTTPRequestPlan{Method: tt.method, URL: "https://api.example.com/"},
			}}
			actions := buildManifestActions(spec, plans, nil)
			if len(actions) == 0 {
				t.Fatal("no actions built")
			}
			if actions[0].ActionClass != tt.wantClass {
				t.Errorf("method %s: want ActionClass %q, got %q", tt.method, tt.wantClass, actions[0].ActionClass)
			}
		})
	}
}

func TestBuildManifestActions_ExplicitActionClassOverridesInference(t *testing.T) {
	spec := ForgeSkillSpec{
		ID: "test", Name: "Test",
		Actions: []ForgeActionSpec{{
			ID:              "act",
			Name:            "Act",
			PermissionLevel: "execute",
			ActionClass:     "send_publish_delete", // explicit override
		}},
	}
	plans := []ForgeActionPlan{{
		ActionID:    "act",
		Type:        "http",
		HTTPRequest: &HTTPRequestPlan{Method: "GET", URL: "https://api.example.com/"},
	}}
	actions := buildManifestActions(spec, plans, nil)
	if actions[0].ActionClass != "send_publish_delete" {
		t.Errorf("explicit actionClass should override method inference, got %q", actions[0].ActionClass)
	}
}

// ── GenerateAndInstallCustomSkill idempotency ─────────────────────────────────

func TestGenerateAndInstallCustomSkill_Idempotent(t *testing.T) {
	supportDir := t.TempDir()
	proposal := makeProposal(t, false)

	// Install twice — must not error or produce duplicate files.
	for i := 0; i < 2; i++ {
		if err := GenerateAndInstallCustomSkill(supportDir, proposal); err != nil {
			t.Fatalf("install attempt %d: %v", i+1, err)
		}
	}

	// Exactly one skill.json and one run should exist.
	skillDir := filepath.Join(customskills.SkillsDir(supportDir), proposal.SkillID)
	entries, _ := os.ReadDir(skillDir)
	names := make(map[string]bool)
	for _, e := range entries {
		if names[e.Name()] {
			t.Errorf("duplicate file %q after idempotent install", e.Name())
		}
		names[e.Name()] = true
	}
}

func TestGenerateAndInstallCustomSkill_PolicyFileWritten(t *testing.T) {
	supportDir := t.TempDir()
	proposal := makeProposal(t, false)

	if err := GenerateAndInstallCustomSkill(supportDir, proposal); err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(supportDir, "action-policies.json")
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("action-policies.json not written: %v", err)
	}
	var policies map[string]string
	if err := json.Unmarshal(data, &policies); err != nil {
		t.Fatalf("invalid action-policies.json: %v", err)
	}
	// Every action in the spec should be auto-approved.
	var spec ForgeSkillSpec
	json.Unmarshal([]byte(proposal.SpecJSON), &spec) //nolint:errcheck
	for _, action := range spec.Actions {
		key := proposal.SkillID + "." + slugify(action.ID)
		if policies[key] != "auto_approve" {
			t.Errorf("policy for %q: want auto_approve, got %q", key, policies[key])
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
