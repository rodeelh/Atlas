package skills

// forge_skill.go — forge.orchestration.propose with full 8-gate validation pipeline.
//
// Forge persistence is injected at startup via Registry.SetForgePersistFn so the
// skills package never imports internal/forge directly (which would create an
// import cycle through agent). Shared types come from internal/forge/forgetypes,
// a zero-dependency leaf package that both sides can import safely.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"atlas-runtime-go/internal/creds"
	"atlas-runtime-go/internal/customskills"
	"atlas-runtime-go/internal/forge/forgetypes"
	"atlas-runtime-go/internal/validate"
)

var forgeAllowedPythonStdlibImports = map[string]bool{
	"base64": true, "collections": true, "csv": true, "datetime": true,
	"decimal": true, "functools": true, "hashlib": true, "io": true,
	"itertools": true, "json": true, "math": true, "os": true,
	"pathlib": true, "random": true, "re": true, "shlex": true,
	"shutil": true, "statistics": true, "string": true, "subprocess": true,
	"sys": true, "tempfile": true, "textwrap": true, "time": true,
	"typing": true, "urllib": true, "uuid": true, "xml": true,
	"zipfile": true,
}

var forgePythonImportRe = regexp.MustCompile(`(?m)^\s*(?:from\s+([A-Za-z0-9_\.]+)\s+import|import\s+([A-Za-z0-9_\. ,]+))`)

// ── Registration ──────────────────────────────────────────────────────────────

func (r *Registry) registerForge() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name: "forge.orchestration.propose",
			Description: "Propose a new Forge skill. " +
				"VALIDATION RULES — the proposal is REJECTED if any rule is violated: " +
				"(1) spec.id must be lowercase-hyphenated starting with a letter, e.g. 'cat-facts'. " +
				"(2) spec.id must not conflict with any already-installed skill. " +
				"(3) spec.riskLevel must be exactly: low, medium, or high — no other values accepted. " +
				"(4) spec.category must be exactly one of: system, utility, creative, communication, automation, research, developer, productivity. " +
				"(5) Each action id must be lowercase-hyphenated and must exactly match an actionID entry in plans_json — orphaned actions (no matching plan) are rejected. " +
				"(6) permissionLevel must be exactly: read, draft, or execute — no other values (not 'readonly', not 'AUTO_APPROVE', not 'write'). " +
				"(7) actionClass (optional) must be one of: read, local_write, destructive_local, external_side_effect, send_publish_delete. " +
				"(8) Every plan URL must be a valid absolute HTTPS URL (placeholders like {param} are allowed). " +
				"(9) For api skills, contract_json is required and must pass quality gates (docsQuality>=medium, mappingConfidence=high). " +
				"(10) All referenced Keychain credential keys must already exist in Settings → Credentials.",
			Properties: map[string]ToolParam{
				"kind": {
					Description: "Skill kind: 'api' (calls external HTTP API), 'composed' (chains Atlas skills), " +
						"'transform' (converts data), 'workflow' (sequences steps), " +
						"'local' (runs a macOS-native script via osascript/bash/python3 — use for Apple Music, Calendar, Reminders, system automation). " +
						"Defaults to 'api'. IMPORTANT: use 'local' for any macOS app control — do NOT fabricate an HTTP API for local app automation.",
					Type: "string",
					Enum: []string{"api", "composed", "transform", "workflow", "local"},
				},
				"contract_json": {
					Description: "Required for api skills. JSON-encoded APIResearchContract. " +
						"Fields: providerName (string), docsURL (string), docsQuality (must be 'medium' or 'high'), " +
						"baseURL (e.g. 'https://api.example.com'), endpoint (path, e.g. '/v1/facts'), " +
						"method ('GET'|'POST'|'PUT'|'PATCH'|'DELETE'), " +
						"authType (exactly: 'none'|'apiKeyHeader'|'apiKeyQuery'|'bearerTokenStatic'|'basicAuth'|'oauth2ClientCredentials'), " +
						"requiredParams ([]string), optionalParams ([]string), " +
						"paramLocations (map of param→'path'|'query'|'body' for EVERY required param), " +
						"exampleRequest (string), exampleResponse (string), expectedResponseFields ([]string), " +
						"mappingConfidence (must be 'high'), validationStatus (string), notes (string).",
					Type: "string",
				},
				"spec_json": {
					Description: "JSON-encoded ForgeSkillSpec. " +
						"Required fields: " +
						"id (lowercase-hyphenated, unique, e.g. 'cat-facts'), " +
						"name (human-readable, e.g. 'Cat Facts'), " +
						"description (one sentence), " +
						"category (exactly one of: system/utility/creative/communication/automation/research/developer/productivity), " +
						"riskLevel (exactly: low, medium, or high), " +
						"tags ([]string, e.g. [\"cats\",\"fun\"]), " +
						"actions (array — each action must have: " +
						"id (lowercase-hyphenated, MUST match an actionID in plans_json exactly), " +
						"name (human-readable label), " +
						"description (one sentence), " +
						"permissionLevel (exactly: read | draft | execute — use read for GET-only, draft for local writes, execute for external side effects), " +
						"actionClass (optional — exactly: read | local_write | destructive_local | external_side_effect | send_publish_delete; " +
						"omit to auto-infer from HTTP method: GET→read, POST/PUT/PATCH/DELETE→external_side_effect)). " +
						`Example: {"id":"cat-facts","name":"Cat Facts","description":"Fetches random cat facts.","category":"utility","riskLevel":"low","tags":["cats"],"actions":[{"id":"get-fact","name":"Get Fact","description":"Returns a random cat fact.","permissionLevel":"read"}]}`,
					Type: "string",
				},
				"plans_json": {
					Description: "JSON-encoded array of ForgeActionPlan — one entry per action in spec_json. " +
						"For API skills (type 'http'): {\"actionID\":\"<id>\",\"type\":\"http\"," +
						"\"httpRequest\":{\"method\":\"GET|POST|PUT|PATCH|DELETE\"," +
						"\"url\":\"https://real-api.com/v1/endpoint/{param}\"," +
						"\"authType\":\"none|apiKeyHeader|apiKeyQuery|bearerTokenStatic|basicAuth|oauth2ClientCredentials\"," +
						"\"authSecretKey\":\"com.projectatlas.myapi\" (Keychain key, required when authType is not 'none')," +
						"\"authHeaderName\":\"X-API-Key\" (required for apiKeyHeader)," +
						"\"authQueryParamName\":\"api_key\" (required for apiKeyQuery)," +
						"\"headers\":{\"X-Custom-Header\":\"value\"} (static request headers)," +
						"\"query\":{\"limit\":\"10\"} (static query params)," +
						"\"bodyFields\":{\"q\":\"{query}\"} (maps action params to POST body keys)," +
						"\"staticBodyFields\":{\"format\":\"json\"}}}. " +
						"For local skills (type 'local'): {\"actionID\":\"<id>\",\"type\":\"local\"," +
						"\"localPlan\":{\"interpreter\":\"osascript|bash|sh|python3\"," +
						"\"script\":\"<inline script with {param} placeholders>\"}}. " +
						"osascript example: {\"interpreter\":\"osascript\",\"script\":\"tell application \\\"Music\\\"\\nplay track \\\"{query}\\\"\\nend tell\"}. " +
						"IMPORTANT: URL hostname must match the contract baseURL — never use example.com or placeholder domains. " +
						"Every actionID must match a spec action id exactly.",
					Type: "string",
				},
				"summary": {
					Description: "Human-readable explanation of what this skill does and why it is useful. " +
						"Displayed to the user in the Forge approval UI.",
					Type: "string",
				},
				"rationale": {
					Description: "Optional: why you are proposing this skill now — what user request triggered it.",
					Type:        "string",
				},
			},
			Required: []string{"spec_json", "plans_json", "summary"},
		},
		PermLevel: "draft",
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			return r.forgeOrchestrationPropose(ctx, args)
		},
	})
}

// ── Execution ─────────────────────────────────────────────────────────────────

func (r *Registry) forgeOrchestrationPropose(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Kind         string `json:"kind"`
		ContractJSON string `json:"contract_json"`
		SpecJSON     string `json:"spec_json"`
		PlansJSON    string `json:"plans_json"`
		Summary      string `json:"summary"`
		Rationale    string `json:"rationale"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if strings.TrimSpace(p.SpecJSON) == "" {
		return "forge.orchestration.propose requires 'spec_json'. Provide a JSON-encoded ForgeSkillSpec.", nil
	}
	if strings.TrimSpace(p.PlansJSON) == "" {
		return "forge.orchestration.propose requires 'plans_json'. Provide a JSON-encoded array of ForgeActionPlan.", nil
	}
	if strings.TrimSpace(p.Summary) == "" {
		return "forge.orchestration.propose requires 'summary'. Provide a brief description of what the skill does.", nil
	}
	if p.Kind == "" {
		p.Kind = "api"
	}

	// Decode spec and plans for gate evaluation.
	var spec forgetypes.ForgeSkillSpec
	if err := json.Unmarshal([]byte(p.SpecJSON), &spec); err != nil {
		return fmt.Sprintf("Could not decode spec_json as ForgeSkillSpec: %v. Ensure spec_json is well-formed JSON.", err), nil
	}
	var plans []forgetypes.ForgeActionPlan
	if err := json.Unmarshal([]byte(p.PlansJSON), &plans); err != nil {
		return fmt.Sprintf("Could not decode plans_json as []ForgeActionPlan: %v. Ensure plans_json is a well-formed JSON array.", err), nil
	}

	// ── Validation pipeline ────────────────────────────────────────────────────

	var persistedContractJSON string

	// Phase 1: spec structural validation (all kinds).
	if msg := forgeValidateSpec(spec, plans); msg != "" {
		return msg, nil
	}

	// Phase 2: URL syntax + placeholder domain check on all HTTP plans.
	if msg := forgeValidatePlanURLs(plans); msg != "" {
		return msg, nil
	}

	// Phase 2: skill ID uniqueness against installed forge skills and custom skills.
	if msg := forgeSkillIDConflict(r.supportDir, spec.ID); msg != "" {
		return msg, nil
	}

	if p.Kind == "api" {
		if strings.TrimSpace(p.ContractJSON) == "" {
			return "forge.orchestration.propose requires 'contract_json' for API skills. " +
				"Research the target API first, then provide a populated APIResearchContract. " +
				"Set kind to 'composed', 'transform', 'workflow', or 'local' if this is not an HTTP API skill.", nil
		}

		var contract forgetypes.APIResearchContract
		if err := json.Unmarshal([]byte(p.ContractJSON), &contract); err != nil {
			return fmt.Sprintf("Could not decode contract_json as APIResearchContract: %v. "+
				"Ensure contract_json is a valid JSON object with providerName, docsQuality, "+
				"mappingConfidence, and other required fields.", err), nil
		}

		// Gates 1–6: contract quality.
		if msg := forgeValidateContract(contract); msg != "" {
			return msg, nil
		}

		// Gate: plan URL hostnames must match contract baseURL hostname.
		if msg := forgeValidatePlanHostnames(plans, contract.BaseURL); msg != "" {
			return msg, nil
		}

		// Gate 7: auth plan field completeness.
		if msg := forgeValidatePlansAuth(plans); msg != "" {
			return msg, nil
		}

		// Gate 8: credential readiness (API skills).
		if msg := forgeValidateCredentials(plans); msg != "" {
			return msg, nil
		}

		// Live API pre-validation via validate.Gate.
		if msg := r.forgeValidateAPI(ctx, contract, plans); msg != "" {
			return msg, nil
		}

		persistedContractJSON = p.ContractJSON

	} else if p.Kind == "local" {
		// Local skills: validate each plan has a supported interpreter and non-empty script.
		// No HTTP URLs, no contract, no API validation, no credentials required.
		if msg := forgeValidateLocalPlans(plans); msg != "" {
			return msg, nil
		}

	} else {
		// Composed / transform / workflow: validate workflow-capable step shapes,
		// then check auth field completeness and credential readiness for any HTTP plans.
		if msg := forgeValidateWorkflowPlans(plans); msg != "" {
			return msg, nil
		}
		if msg := forgeValidatePlansAuth(plans); msg != "" {
			return msg, nil
		}
		if msg := forgeValidateCredentials(plans); msg != "" {
			return msg, nil
		}
	}

	// Guard: persistence callback must be injected.
	if r.forgePersistFn == nil {
		return "Forge is not yet ready — the runtime is still initialising. Please try again in a moment.", nil
	}

	// Persist the proposal.
	id, name, skillID, riskLevel, actionNames, domains, err := r.forgePersistFn(
		p.SpecJSON, p.PlansJSON, p.Summary, p.Rationale, persistedContractJSON,
	)
	if err != nil {
		return fmt.Sprintf("Forge proposal creation failed: %v", err), nil
	}

	domainsNote := "no external domains"
	if len(domains) > 0 {
		domainsNote = strings.Join(domains, ", ")
	}

	return fmt.Sprintf(`Forge proposal created.

Proposal ID: %s
Skill: %s (%s)
Actions: %s
Domains: %s
Risk level: %s

The proposal is pending your review. Open the Skills → Forge panel to inspect, install, and enable it. The skill will not be active until you approve it.`,
		id, name, skillID,
		strings.Join(actionNames, ", "),
		domainsNote, riskLevel), nil
}

// ── Gate functions ─────────────────────────────────────────────────────────────

// forgeValidateContract checks gates 1–6 against the APIResearchContract.
// Returns a refusal message on failure, or "" on pass.
func forgeValidateContract(c forgetypes.APIResearchContract) string {
	// Gate 1: docsQuality >= medium.
	switch c.DocsQuality {
	case "medium", "high":
	default:
		return fmt.Sprintf("Forge refused: docsQuality is '%s'. Research the API docs further — docsQuality must be 'medium' or 'high' before a proposal can be created.", c.DocsQuality)
	}

	// Gate 2: mappingConfidence must be high.
	if c.MappingConfidence != "high" {
		return fmt.Sprintf("Forge refused: mappingConfidence is '%s'. All field names, locations, and auth details must be confirmed from the official docs (mappingConfidence must be 'high').", c.MappingConfidence)
	}

	// Gate 3: endpoint must be defined.
	if strings.TrimSpace(c.Endpoint) == "" {
		return "Forge refused: endpoint is not defined in the contract. Identify the exact API endpoint path before proposing."
	}

	// Gate 4: valid HTTP method.
	switch strings.ToUpper(c.Method) {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Sprintf("Forge refused: '%s' is not a valid HTTP method. Use GET, POST, PUT, PATCH, or DELETE.", c.Method)
	}

	// Gate 5: paramLocations defined for every required param.
	for _, param := range c.RequiredParams {
		if _, ok := c.ParamLocations[param]; !ok {
			return fmt.Sprintf("Forge refused: parameter location not defined for required param '%s'. Specify 'path', 'query', or 'body' for every required parameter.", param)
		}
	}

	// Gate 6: authType must be natively supported.
	switch c.AuthType {
	case "none", "apiKeyHeader", "apiKeyQuery", "bearerTokenStatic", "basicAuth", "oauth2ClientCredentials":
	case "oauth2AuthorizationCode":
		return "Forge refused: oauth2AuthorizationCode requires a browser login flow and is not supported. Use oauth2ClientCredentials for server-to-server OAuth2 flows."
	default:
		return fmt.Sprintf("Forge refused: auth type '%s' is not supported. Atlas Forge supports: none, apiKeyHeader, apiKeyQuery, bearerTokenStatic, basicAuth, oauth2ClientCredentials.", c.AuthType)
	}

	return ""
}

// forgeValidatePlansAuth checks Gate 7 — each plan's authType has all required
// companion fields for runtime injection.
func forgeValidatePlansAuth(plans []forgetypes.ForgeActionPlan) string {
	for _, plan := range plans {
		h := plan.HTTPRequest
		if h == nil {
			continue
		}
		switch h.AuthType {
		case "apiKeyHeader":
			if h.AuthSecretKey == "" || h.AuthHeaderName == "" {
				return fmt.Sprintf("Forge refused: plan '%s' uses apiKeyHeader auth but is missing authSecretKey or authHeaderName.", plan.ActionID)
			}
		case "apiKeyQuery":
			if h.AuthSecretKey == "" || h.AuthQueryParamName == "" {
				return fmt.Sprintf("Forge refused: plan '%s' uses apiKeyQuery auth but is missing authSecretKey or authQueryParamName.", plan.ActionID)
			}
		case "bearerTokenStatic", "basicAuth":
			if h.AuthSecretKey == "" {
				return fmt.Sprintf("Forge refused: plan '%s' uses %s auth but is missing authSecretKey.", plan.ActionID, h.AuthType)
			}
		case "oauth2ClientCredentials":
			if h.OAuth2TokenURL == "" || h.OAuth2ClientIDKey == "" || h.OAuth2ClientSecretKey == "" {
				return fmt.Sprintf("Forge refused: plan '%s' uses oauth2ClientCredentials but is missing oauth2TokenURL, oauth2ClientIDKey, or oauth2ClientSecretKey.", plan.ActionID)
			}
		}
	}
	return ""
}

// forgeValidateCredentials checks Gate 8 — all referenced Keychain secrets exist.
// Keys may be standalone Keychain items OR custom keys stored inside the Atlas
// credential bundle (com.projectatlas.credentials → customSecrets).
func forgeValidateCredentials(plans []forgetypes.ForgeActionPlan) string {
	// Read the credential bundle once so we can check custom keys.
	bundle, _ := creds.Read()

	checked := map[string]bool{}
	for _, plan := range plans {
		h := plan.HTTPRequest
		if h == nil {
			continue
		}
		for _, key := range []string{h.AuthSecretKey, h.OAuth2ClientIDKey, h.OAuth2ClientSecretKey, h.SecretHeader} {
			if key == "" || checked[key] {
				continue
			}
			checked[key] = true
			if !isValidKeychainServiceName(key) {
				return fmt.Sprintf("Forge refused: credential key '%s' contains invalid characters. Use only alphanumeric characters, dots, hyphens, and underscores.", key)
			}
			// Accept the key if it exists as a standalone Keychain item OR as
			// a custom secret inside the Atlas credential bundle.
			if !forgeKeychainExists(key) && bundle.CustomSecret(key) == "" {
				return fmt.Sprintf("Forge refused: credential '%s' is not in the Keychain. Add it in Settings → Credentials before proposing this skill.", key)
			}
		}
	}
	return ""
}

// isValidKeychainServiceName returns true if s is safe to pass as the -s
// argument to the `security` CLI. Only alphanumeric, dot, hyphen, and
// underscore characters are allowed — this prevents shell metacharacter
// injection even though exec.Command does not invoke a shell.
func isValidKeychainServiceName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// forgeKeychainExists returns true if a Keychain generic-password item exists
// for the given service name. Callers must validate the service name with
// isValidKeychainServiceName before calling this function.
func forgeKeychainExists(service string) bool {
	return exec.Command("security", "find-generic-password", "-s", service, "-w").Run() == nil
}

// forgeReadKeychain reads a credential value by key name.
// It first tries a standalone Keychain item, then falls back to the Atlas
// credential bundle's customSecrets map.
// Callers must validate the key name with isValidKeychainServiceName before
// calling this function.
func forgeReadKeychain(service string) string {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	// Fall back to the Atlas credential bundle.
	bundle, _ := creds.Read()
	return bundle.CustomSecret(service)
}

// forgeValidateAPI runs a live pre-validation against the primary GET plan via
// the validate.Gate pipeline.
func (r *Registry) forgeValidateAPI(ctx context.Context, contract forgetypes.APIResearchContract, plans []forgetypes.ForgeActionPlan) string {
	// Find the first GET-capable plan.
	var primary *forgetypes.HTTPRequestPlan
	for i := range plans {
		h := plans[i].HTTPRequest
		if h != nil && strings.ToUpper(h.Method) == "GET" {
			primary = h
			break
		}
	}
	if primary == nil {
		// No GET plan — skip live validation (write-only API).
		return ""
	}

	// Resolve credential value from Keychain.
	credValue := ""
	switch primary.AuthType {
	case "apiKeyHeader", "apiKeyQuery", "bearerTokenStatic", "basicAuth":
		if primary.AuthSecretKey != "" && isValidKeychainServiceName(primary.AuthSecretKey) {
			credValue = forgeReadKeychain(primary.AuthSecretKey)
		}
	}

	req := validate.ValidationRequest{
		ProviderName:           contract.ProviderName,
		BaseURL:                contract.BaseURL,
		Endpoint:               contract.Endpoint,
		Method:                 primary.Method,
		AuthType:               validate.AuthType(primary.AuthType),
		AuthHeaderName:         primary.AuthHeaderName,
		AuthQueryParam:         primary.AuthQueryParamName,
		CredentialValue:        credValue,
		RequiredParams:         contract.RequiredParams,
		ExpectedResponseFields: contract.ExpectedResponseFields,
	}

	gate := validate.Gate{SupportDir: r.supportDir}
	result := gate.Run(ctx, req)

	switch result.Recommendation {
	case validate.RecommendationReject:
		return fmt.Sprintf("API validation rejected this proposal.\n\n%s\n\nCheck the endpoint URL and authentication configuration, then try again.", result.Summary)
	case validate.RecommendationNeedsRevision:
		return fmt.Sprintf("API validation completed but the response needs attention.\n\n%s\n\nConfidence: %.0f%%\n\nReview the API configuration and try again.", result.Summary, result.Confidence*100)
	}
	// RecommendationUsable or RecommendationSkipped — proceed.
	// Skipped with write actions is noted in the proposal summary but is not a hard block:
	// write-only endpoints cannot be live-tested without side effects, and many legitimate
	// APIs (Slack, Gmail, Linear, etc.) have no safe read endpoint to validate against.
	return ""
}

// slugRe matches valid lowercase-hyphenated identifiers: starts with a letter,
// contains only lowercase letters, digits, and hyphens.
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// forgeValidateSpec checks the spec structure for all skill kinds.
// Phase 1: enum validation, format validation, and no-orphan cross-check with plans.
func forgeValidateSpec(spec forgetypes.ForgeSkillSpec, plans []forgetypes.ForgeActionPlan) string {
	var issues []string

	// ── Skill ID ──────────────────────────────────────────────────────────────
	if strings.TrimSpace(spec.ID) == "" {
		issues = append(issues, "spec.id is required (lowercase-hyphenated, e.g. 'my-skill')")
	} else if !slugRe.MatchString(spec.ID) {
		issues = append(issues, fmt.Sprintf(
			"spec.id %q is not valid — must be lowercase-hyphenated starting with a letter (e.g. 'cat-facts')", spec.ID))
	}

	if strings.TrimSpace(spec.Name) == "" {
		issues = append(issues, "spec.name is required")
	}

	// ── riskLevel enum ────────────────────────────────────────────────────────
	switch spec.RiskLevel {
	case "low", "medium", "high", "":
		// "" is allowed; codegen defaults to "medium"
	default:
		issues = append(issues, fmt.Sprintf(
			"spec.riskLevel %q is not valid — must be exactly: low, medium, or high", spec.RiskLevel))
	}

	// ── category enum ─────────────────────────────────────────────────────────
	validCategories := map[string]bool{
		"system": true, "utility": true, "creative": true, "communication": true,
		"automation": true, "research": true, "developer": true, "productivity": true,
	}
	if spec.Category != "" && !validCategories[spec.Category] {
		issues = append(issues, fmt.Sprintf(
			"spec.category %q is not valid — must be one of: system, utility, creative, communication, automation, research, developer, productivity", spec.Category))
	}

	// ── actions ───────────────────────────────────────────────────────────────
	if len(spec.Actions) == 0 {
		issues = append(issues, "spec.actions must contain at least one action")
	}

	// Build a set of plan actionIDs for cross-check.
	planIDs := make(map[string]bool, len(plans))
	for _, p := range plans {
		planIDs[p.ActionID] = true
	}

	seenActionIDs := make(map[string]bool, len(spec.Actions))
	for _, a := range spec.Actions {
		// id presence + format
		if strings.TrimSpace(a.ID) == "" {
			issues = append(issues, fmt.Sprintf("action '%s' is missing an id", a.Name))
		} else {
			if !slugRe.MatchString(a.ID) {
				issues = append(issues, fmt.Sprintf(
					"action id %q must be lowercase-hyphenated starting with a letter (e.g. 'get-fact')", a.ID))
			}
			// duplicate action IDs
			if seenActionIDs[a.ID] {
				issues = append(issues, fmt.Sprintf("duplicate action id %q — each action must have a unique id", a.ID))
			}
			seenActionIDs[a.ID] = true
			// every action must have a matching plan
			if !planIDs[a.ID] {
				issues = append(issues, fmt.Sprintf(
					"action %q has no matching plan in plans_json — add a plan with actionID %q", a.ID, a.ID))
			}
		}

		if strings.TrimSpace(a.Name) == "" {
			issues = append(issues, fmt.Sprintf("action '%s' is missing a name", a.ID))
		}

		// permissionLevel enum — reject, don't coerce
		switch a.PermissionLevel {
		case "read", "draft", "execute", "":
			// "" is allowed; codegen defaults to "read" for Forge skills
		default:
			issues = append(issues, fmt.Sprintf(
				"action %q has invalid permissionLevel %q — must be exactly: read, draft, or execute",
				a.ID, a.PermissionLevel))
		}

		// actionClass enum (optional field)
		switch a.ActionClass {
		case "", "read", "local_write", "destructive_local", "external_side_effect", "send_publish_delete":
		default:
			issues = append(issues, fmt.Sprintf(
				"action %q has invalid actionClass %q — must be one of: read, local_write, destructive_local, external_side_effect, send_publish_delete",
				a.ID, a.ActionClass))
		}
	}

	if len(issues) == 0 {
		return ""
	}
	bullets := make([]string, len(issues))
	for i, iss := range issues {
		bullets[i] = "• " + iss
	}
	return "Forge spec validation failed:\n" + strings.Join(bullets, "\n") + "\nFix these issues and call forge.orchestration.propose again."
}

// forgeValidatePlanURLs checks that every HTTP plan URL is well-formed and
// does not use a placeholder/example domain.
// Phase 2: catches malformed and fabricated URLs before they reach codegen.
// Local-type plans are skipped — they have no URL.
func forgeValidatePlanURLs(plans []forgetypes.ForgeActionPlan) string {
	placeholderRe := regexp.MustCompile(`\{[^}]+\}`)
	for _, plan := range plans {
		if plan.Type == "local" {
			continue // local plans have no HTTP URL
		}
		h := plan.HTTPRequest
		if h == nil || strings.TrimSpace(h.URL) == "" {
			continue
		}
		// Strip {param} placeholders before parsing so they don't invalidate the URL.
		testURL := placeholderRe.ReplaceAllString(h.URL, "placeholder")
		u, err := url.ParseRequestURI(testURL)
		if err != nil {
			return fmt.Sprintf(
				"Forge refused: plan %q has a malformed URL %q — provide a valid absolute URL (e.g. https://api.real-service.com/v1/endpoint).",
				plan.ActionID, h.URL)
		}
		// Reject placeholder/example/fabricated domains.
		if forgetypes.PlaceholderDomains[strings.ToLower(u.Hostname())] {
			return fmt.Sprintf(
				"Forge refused: plan %q uses the placeholder domain %q. "+
					"Provide the real API base URL — do not use example.com or other stand-in domains.",
				plan.ActionID, u.Hostname())
		}
	}
	return ""
}

// baseDomain returns the registrable domain portion of a hostname.
// "api.foo.com" → "foo.com", "foo.com" → "foo.com", "foo" → "foo".
func baseDomain(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// forgeValidatePlanHostnames verifies that every HTTP plan URL shares the same
// registrable base domain as the contract's baseURL. Subdomains are allowed —
// e.g. plan "api.foo.com" passes when contract says "foo.com" or "auth.foo.com".
// This prevents entirely fabricated hostnames while permitting multi-subdomain APIs.
func forgeValidatePlanHostnames(plans []forgetypes.ForgeActionPlan, contractBaseURL string) string {
	if strings.TrimSpace(contractBaseURL) == "" {
		return ""
	}
	cu, err := url.Parse(contractBaseURL)
	if err != nil || cu.Hostname() == "" {
		return fmt.Sprintf(
			"Forge refused: contract baseURL %q is not a valid URL with a hostname — hostname matching cannot be performed.",
			contractBaseURL)
	}
	contractBase := baseDomain(strings.ToLower(cu.Hostname()))

	placeholderRe := regexp.MustCompile(`\{[^}]+\}`)
	for _, plan := range plans {
		if plan.Type == "local" {
			continue
		}
		h := plan.HTTPRequest
		if h == nil || strings.TrimSpace(h.URL) == "" {
			continue
		}
		testURL := placeholderRe.ReplaceAllString(h.URL, "placeholder")
		u, err := url.ParseRequestURI(testURL)
		if err != nil {
			continue // already caught by forgeValidatePlanURLs
		}
		planBase := baseDomain(strings.ToLower(u.Hostname()))
		if planBase != contractBase {
			return fmt.Sprintf(
				"Forge refused: plan %q URL domain %q does not match contract baseURL domain %q. "+
					"The plan must call the same service documented in the contract.",
				plan.ActionID, u.Hostname(), cu.Hostname())
		}
	}
	return ""
}

// forgeValidateLocalPlans validates all local-type plans: each must specify a
// supported interpreter and a non-empty script body.
func forgeValidateLocalPlans(plans []forgetypes.ForgeActionPlan) string {
	validInterpreters := map[string]bool{
		"osascript": true, "bash": true, "sh": true, "python3": true,
	}
	for _, plan := range plans {
		lp := plan.LocalPlan
		if lp == nil {
			return fmt.Sprintf(
				"Forge refused: local plan %q is missing 'localPlan' — provide {\"interpreter\":\"osascript|bash|sh|python3\",\"script\":\"...\"}.",
				plan.ActionID)
		}
		if !validInterpreters[lp.Interpreter] {
			return fmt.Sprintf(
				"Forge refused: plan %q has unsupported interpreter %q — must be one of: osascript, bash, sh, python3.",
				plan.ActionID, lp.Interpreter)
		}
		if strings.TrimSpace(lp.Script) == "" {
			return fmt.Sprintf("Forge refused: local plan %q has an empty script.", plan.ActionID)
		}
		if msg := forgeValidateLocalScript(plan.ActionID, lp.Interpreter, lp.Script); msg != "" {
			return msg
		}
	}
	return ""
}

func forgeValidateWorkflowPlans(plans []forgetypes.ForgeActionPlan) string {
	for _, plan := range plans {
		switch plan.Type {
		case "http":
			if plan.HTTPRequest == nil {
				return fmt.Sprintf("Forge refused: workflow plan %q is missing 'httpRequest'.", plan.ActionID)
			}
		case "local":
			if plan.LocalPlan == nil {
				return fmt.Sprintf("Forge refused: workflow plan %q is missing 'localPlan'.", plan.ActionID)
			}
		case "prompt", "llm.generate":
			if plan.WorkflowStep == nil || strings.TrimSpace(plan.WorkflowStep.Prompt) == "" {
				return fmt.Sprintf("Forge refused: workflow step %q must include workflowStep.prompt for type %q.", plan.ActionID, plan.Type)
			}
		case "atlas.tool":
			if plan.WorkflowStep == nil || strings.TrimSpace(plan.WorkflowStep.Action) == "" {
				return fmt.Sprintf("Forge refused: workflow step %q must include workflowStep.action for type atlas.tool.", plan.ActionID)
			}
		case "return":
			if plan.WorkflowStep == nil {
				return fmt.Sprintf("Forge refused: workflow step %q must include workflowStep for type return.", plan.ActionID)
			}
		default:
			return fmt.Sprintf("Forge refused: unsupported workflow/composed plan type %q for action %q.", plan.Type, plan.ActionID)
		}
	}
	return ""
}

func forgeValidateLocalScript(actionID, interpreter, script string) string {
	lower := strings.ToLower(script)

	if interpreter == "python3" {
		for _, blocked := range []string{
			"pip install", "python -m pip", "python3 -m pip",
			"reportlab", "pypdf", "fpdf", "docx", "python-docx", "pillow", "pil",
		} {
			if strings.Contains(lower, blocked) {
				return fmt.Sprintf(
					"Forge refused: local python plan %q depends on %q. Forge-generated python skills must use the standard library only. "+
						"For PDFs, DOCX, ZIPs, and images, use Atlas built-ins like fs.create_pdf, fs.create_docx, fs.create_zip, or fs.save_image instead of forging a custom skill.",
					actionID, blocked)
			}
		}
		for _, match := range forgePythonImportRe.FindAllStringSubmatch(script, -1) {
			modules := match[1]
			if modules == "" {
				modules = match[2]
			}
			for _, part := range strings.Split(modules, ",") {
				module := strings.TrimSpace(part)
				module = strings.TrimPrefix(module, "import ")
				if module == "" {
					continue
				}
				root := strings.Split(strings.Split(module, " as ")[0], ".")[0]
				if !forgeAllowedPythonStdlibImports[root] {
					return fmt.Sprintf(
						"Forge refused: local python plan %q imports %q, which is not in Forge's stdlib allowlist. "+
							"Forge-generated python skills must avoid third-party dependencies. Prefer Atlas built-ins for file creation tasks.",
						actionID, root)
				}
			}
		}
	}

	if strings.Contains(lower, "create pdf") || strings.Contains(lower, ".pdf") ||
		strings.Contains(lower, "create docx") || strings.Contains(lower, ".docx") ||
		strings.Contains(lower, "create zip") || strings.Contains(lower, ".zip") ||
		strings.Contains(lower, "save image") || strings.Contains(lower, ".png") ||
		strings.Contains(lower, ".jpg") || strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".gif") {
		return fmt.Sprintf(
			"Forge refused: local plan %q is trying to generate a document/archive/image format that Atlas already supports natively. "+
				"Use the built-in filesystem actions fs.create_pdf, fs.create_docx, fs.create_zip, fs.save_image, or fs.write_binary_file instead of forging a custom skill.",
			actionID)
	}

	return ""
}

// forgeSkillIDConflict returns a non-empty message if skillID already exists
// in forge-installed.json or as a user-installed custom skill directory.
// Phase 2: prevents silent overwrites.
func forgeSkillIDConflict(supportDir, skillID string) string {
	// Check forge-installed.json.
	var installed []struct {
		SkillID string `json:"skillID"`
	}
	if data, err := os.ReadFile(filepath.Join(supportDir, "forge-installed.json")); err == nil {
		json.Unmarshal(data, &installed) //nolint:errcheck
		for _, rec := range installed {
			if rec.SkillID == skillID {
				return fmt.Sprintf(
					"Forge refused: a skill with id %q is already installed. Choose a different id or remove the existing skill first.", skillID)
			}
		}
	}

	// Check user-installed custom skill directories.
	for _, m := range customskills.ListManifests(supportDir) {
		if m.ID == skillID {
			return fmt.Sprintf(
				"Forge refused: a custom skill with id %q is already installed. Choose a different id or remove the existing skill first.", skillID)
		}
	}

	return ""
}
