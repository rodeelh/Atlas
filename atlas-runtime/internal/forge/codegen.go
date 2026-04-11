package forge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"atlas-runtime-go/internal/customskills"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/logstore"
)

// GenerateAndInstallCustomSkill generates a skill.json manifest and a
// stdlib-only Python run script from a ForgeProposal, then writes them into
// the custom skills directory so that LoadCustomSkills() picks them up on the
// next registry refresh (daemon restart or hot-reload).
//
// The generated skill carries Source: "forge" in its manifest so that it
// continues to appear with the Forge badge in the Skills UI instead of the
// generic Custom badge.
func GenerateAndInstallCustomSkill(supportDir string, proposal ForgeProposal) error {
	// ── 1. Parse embedded JSON strings ───────────────────────────────────────
	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(proposal.SpecJSON), &spec); err != nil {
		return fmt.Errorf("forge/codegen: bad specJSON: %w", err)
	}

	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(proposal.PlansJSON), &plans); err != nil {
		return fmt.Errorf("forge/codegen: bad plansJSON: %w", err)
	}

	var contract *APIResearchContract
	if proposal.ContractJSON != "" {
		var c APIResearchContract
		if err := json.Unmarshal([]byte(proposal.ContractJSON), &c); err != nil {
			logstore.Write("warn", "forge/codegen: bad contractJSON — skipping parameter hints: "+err.Error(), nil)
		} else {
			contract = &c
		}
	}

	// ── 2. Build actions for skill.json ──────────────────────────────────────
	actions := buildManifestActions(spec, plans, contract)

	manifest := customskills.CustomSkillManifest{
		ID:          proposal.SkillID,
		Name:        proposal.Name,
		Version:     "1.0",
		Description: proposal.Description,
		Source:      "forge",
		RiskLevel:   normalizeRiskLevel(spec.RiskLevel),
		Category:    spec.Category,
		Tags:        spec.Tags,
		Actions:     actions,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("forge/codegen: marshal skill.json: %w", err)
	}

	// ── 3. Generate Python run script ─────────────────────────────────────────
	plansData, err := json.Marshal(plans)
	if err != nil {
		return fmt.Errorf("forge/codegen: marshal plans: %w", err)
	}
	runScript := buildRunScript(string(plansData))

	// ── 4. Write files to skills directory ───────────────────────────────────
	skillDir := filepath.Join(customskills.SkillsDir(supportDir), proposal.SkillID)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("forge/codegen: mkdir %s: %w", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "skill.json"), manifestData, 0o644); err != nil {
		return fmt.Errorf("forge/codegen: write skill.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "run"), []byte(runScript), 0o755); err != nil {
		return fmt.Errorf("forge/codegen: write run: %w", err)
	}

	// ── 5. Auto-approve all actions ───────────────────────────────────────────
	// The user explicitly approved this skill through the forge pipeline, so
	// all of its actions should default to auto-approve rather than "always ask".
	if err := writeForgeActionPolicies(supportDir, proposal.SkillID, actions); err != nil {
		// Non-fatal: skill still works, user just gets prompted on each call.
		logstore.Write("warn", fmt.Sprintf("forge/codegen: set action policies: %s", err), nil)
	}

	logstore.Write("info", fmt.Sprintf("forge/codegen: installed %q → %s", proposal.SkillID, skillDir), nil)
	return nil
}

func InstallProposalArtifacts(supportDir string, proposal ForgeProposal) (*InstallTarget, error) {
	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(proposal.PlansJSON), &plans); err != nil {
		return nil, fmt.Errorf("forge/install: bad plansJSON: %w", err)
	}
	if usesWorkflowRuntime(plans) {
		workflowID, err := GenerateAndInstallWorkflow(supportDir, proposal)
		if err != nil {
			return nil, err
		}
		return &InstallTarget{Type: "workflow", Ref: workflowID}, nil
	}
	if err := GenerateAndInstallCustomSkill(supportDir, proposal); err != nil {
		return nil, err
	}
	return &InstallTarget{Type: "custom_skill", Ref: proposal.SkillID}, nil
}

func GenerateAndInstallWorkflow(supportDir string, proposal ForgeProposal) (string, error) {
	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(proposal.SpecJSON), &spec); err != nil {
		return "", fmt.Errorf("forge/workflow: bad specJSON: %w", err)
	}
	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(proposal.PlansJSON), &plans); err != nil {
		return "", fmt.Errorf("forge/workflow: bad plansJSON: %w", err)
	}

	workflowID := proposal.SkillID + ".v1"
	titleByAction := map[string]string{}
	for _, action := range spec.Actions {
		titleByAction[action.ID] = action.Name
	}

	steps := make([]map[string]any, 0, len(plans))
	for _, plan := range plans {
		step, err := workflowStepFromPlan(plan, titleByAction[plan.ActionID])
		if err != nil {
			return "", err
		}
		steps = append(steps, step)
	}

	definition := map[string]any{
		"id":            workflowID,
		"name":          spec.Name,
		"description":   proposal.Description,
		"artifactTypes": []string{"workflow.run_result"},
		"isEnabled":     true,
		"source":        "forge",
		"tags":          spec.Tags,
		"steps":         steps,
		"forgeSkillID":  proposal.SkillID,
	}

	if existing := features.GetWorkflowDefinition(supportDir, workflowID); existing != nil {
		if _, err := features.UpdateWorkflowDefinition(supportDir, workflowID, definition); err != nil {
			return "", fmt.Errorf("forge/workflow: update workflow definition: %w", err)
		}
	} else {
		if _, err := features.AppendWorkflowDefinition(supportDir, definition); err != nil {
			return "", fmt.Errorf("forge/workflow: append workflow definition: %w", err)
		}
	}
	logstore.Write("info", fmt.Sprintf("forge/workflow: installed %q → %s", proposal.SkillID, workflowID), nil)
	return workflowID, nil
}

func RemoveWorkflowInstall(supportDir, workflowID string) error {
	if strings.TrimSpace(workflowID) == "" {
		return nil
	}
	_, err := features.DeleteWorkflowDefinition(supportDir, workflowID)
	if err != nil {
		return fmt.Errorf("forge/workflow: delete %s: %w", workflowID, err)
	}
	return nil
}

func usesWorkflowRuntime(plans []ForgeActionPlan) bool {
	if len(plans) == 0 {
		return false
	}
	for _, plan := range plans {
		switch plan.Type {
		case "prompt", "llm.generate", "atlas.tool", "return":
			return true
		}
	}
	return false
}

func workflowStepFromPlan(plan ForgeActionPlan, fallbackTitle string) (map[string]any, error) {
	title := strings.TrimSpace(fallbackTitle)
	if plan.WorkflowStep != nil && strings.TrimSpace(plan.WorkflowStep.Title) != "" {
		title = strings.TrimSpace(plan.WorkflowStep.Title)
	}
	if title == "" {
		title = strings.TrimSpace(plan.ActionID)
	}
	step := map[string]any{
		"id":    plan.ActionID,
		"title": title,
		"type":  plan.Type,
	}
	switch plan.Type {
	case "prompt", "llm.generate":
		if plan.WorkflowStep == nil || strings.TrimSpace(plan.WorkflowStep.Prompt) == "" {
			return nil, fmt.Errorf("forge/workflow: step %q is missing prompt", plan.ActionID)
		}
		step["prompt"] = plan.WorkflowStep.Prompt
	case "atlas.tool":
		if plan.WorkflowStep == nil || strings.TrimSpace(plan.WorkflowStep.Action) == "" {
			return nil, fmt.Errorf("forge/workflow: step %q is missing action", plan.ActionID)
		}
		step["action"] = plan.WorkflowStep.Action
		if len(plan.WorkflowStep.Args) > 0 {
			step["args"] = plan.WorkflowStep.Args
		}
	case "return":
		if plan.WorkflowStep != nil {
			step["value"] = plan.WorkflowStep.Value
		}
	default:
		return nil, fmt.Errorf("forge/workflow: unsupported workflow plan type %q", plan.Type)
	}
	return step, nil
}

// RemoveCustomSkillDir removes the generated custom skill directory for a
// forge-installed skill.  Returns nil if the directory does not exist.
func RemoveCustomSkillDir(supportDir, skillID string) error {
	skillDir := filepath.Join(customskills.SkillsDir(supportDir), skillID)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("forge/codegen: remove %s: %w", skillDir, err)
	}
	return nil
}

// ── Policy helpers ───────────────────────────────────────────────────────────

const policiesFile = "action-policies.json"

// writeForgeActionPolicies sets "auto_approve" for every action in the newly
// installed forge skill.  The user already reviewed and approved the skill
// through the forge pipeline, so prompting on every call is unnecessary.
func writeForgeActionPolicies(supportDir, skillID string, actions []customskills.CustomSkillAction) error {
	policyPath := filepath.Join(supportDir, policiesFile)

	// Load existing policies (or start fresh if the file doesn't exist yet).
	policies := map[string]string{}
	if data, err := os.ReadFile(policyPath); err == nil {
		_ = json.Unmarshal(data, &policies) // ignore parse errors — use empty map
	}

	// Add auto_approve for every action in this skill.
	for _, a := range actions {
		actionID := skillID + "." + a.Name
		policies[actionID] = "auto_approve"
	}

	// Atomic write: temp file → rename.
	data, err := json.MarshalIndent(policies, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(policyPath), "action-policies-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	tmp.Close()
	if err := os.Rename(tmpPath, policyPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ── Parameter schema helpers ─────────────────────────────────────────────────

// urlParamRe matches {param} placeholders in URL and body-field templates.
var urlParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// normalizeForgePermLevel converts any AI-generated permission_level value to
// one of the three canonical values accepted by Atlas: "read", "draft", or "execute".
func normalizeForgePermLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "read", "readonly":
		return "read"
	case "draft":
		return "draft"
	case "execute":
		return "execute"
	default:
		return "read" // Forge skills are typically read-only API calls
	}
}

// normalizeActionClass validates an explicit actionClass value from the spec.
// Returns the canonical string if valid, or "" if empty/unknown (so the caller
// falls back to method-based inference).
func normalizeActionClass(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "read":
		return "read"
	case "local_write":
		return "local_write"
	case "destructive_local":
		return "destructive_local"
	case "external_side_effect":
		return "external_side_effect"
	case "send_publish_delete":
		return "send_publish_delete"
	default:
		return "" // unknown or empty — let caller infer from HTTP method
	}
}

// normalizeRiskLevel validates riskLevel from the spec, defaulting to "medium".
func normalizeRiskLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low", "medium", "high":
		return strings.ToLower(level)
	default:
		return "medium"
	}
}

// buildManifestActions derives []CustomSkillAction from spec + plans + contract.
func buildManifestActions(
	spec ForgeSkillSpec,
	plans []ForgeActionPlan,
	contract *APIResearchContract,
) []customskills.CustomSkillAction {
	// Contract-level required / optional hint lists (may be nil).
	var contractRequired, contractOptional []string
	var paramLocations map[string]string
	if contract != nil {
		contractRequired = contract.RequiredParams
		contractOptional = contract.OptionalParams
		paramLocations = contract.ParamLocations
	}
	_ = paramLocations // informational — not needed for schema generation

	actions := make([]customskills.CustomSkillAction, 0, len(spec.Actions))
	for _, sa := range spec.Actions {
		// Find matching plan by action ID (the canonical link between spec and plans).
		var httpPlan *HTTPRequestPlan
		var localPlan *LocalPlan
		for i := range plans {
			if plans[i].ActionID == sa.ID {
				httpPlan = plans[i].HTTPRequest
				localPlan = plans[i].LocalPlan
				break
			}
		}

		var params map[string]any
		if localPlan != nil {
			params = buildLocalParamSchema(localPlan, contractRequired, contractOptional)
		} else {
			params = buildParamSchema(httpPlan, contractRequired, contractOptional)
		}

		permLevel := normalizeForgePermLevel(sa.PermissionLevel)

		// Determine action class: prefer explicit override from spec, then infer from plan type.
		actionClass := normalizeActionClass(sa.ActionClass)
		if actionClass == "" {
			if localPlan != nil {
				// Local scripts interact with macOS apps or the filesystem; default to
				// external_side_effect since we cannot statically determine intent.
				actionClass = "external_side_effect"
			} else if httpPlan != nil {
				switch strings.ToUpper(httpPlan.Method) {
				case "POST", "PUT", "PATCH", "DELETE":
					actionClass = "external_side_effect"
				default:
					actionClass = "read"
				}
			} else {
				actionClass = "read"
			}
		}

		// Use the action ID (slugified) as the key — it is the canonical identifier
		// used in plans_json actionID fields and registered with the skills engine.
		// The Name field is kept as the human-readable label in description.
		actions = append(actions, customskills.CustomSkillAction{
			Name:        slugify(sa.ID),
			Description: sa.Description,
			PermLevel:   permLevel,
			ActionClass: actionClass,
			Parameters:  params,
		})
	}
	return actions
}

// buildLocalParamSchema creates a JSON Schema "object" map for a local plan.
// Parameters are extracted from {param} placeholders in the script body plus
// any contract-declared required/optional params.
func buildLocalParamSchema(plan *LocalPlan, required, optional []string) map[string]any {
	if plan == nil {
		return nil
	}
	seen := make(map[string]bool)
	var allParams, reqParams []string
	add := func(name string, isRequired bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		allParams = append(allParams, name)
		if isRequired {
			reqParams = append(reqParams, name)
		}
	}
	// Script {param} placeholders are required — missing one would produce a broken script.
	for _, m := range urlParamRe.FindAllStringSubmatch(plan.Script, -1) {
		add(m[1], true)
	}
	for _, p := range required {
		add(p, true)
	}
	for _, p := range optional {
		add(p, false)
	}
	if len(allParams) == 0 {
		return nil
	}
	properties := make(map[string]any, len(allParams))
	for _, p := range allParams {
		properties[p] = map[string]any{"type": "string", "description": p}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(reqParams) > 0 {
		schema["required"] = reqParams
	}
	return schema
}

// buildParamSchema creates a JSON Schema "object" map from all param sources.
// Returns nil when there are no parameters to describe (model won't see a
// parameters field and can call the action with no arguments).
func buildParamSchema(plan *HTTPRequestPlan, required, optional []string) map[string]any {
	seen := make(map[string]bool)
	var allParams []string
	var reqParams []string

	add := func(name string, isRequired bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		allParams = append(allParams, name)
		if isRequired {
			reqParams = append(reqParams, name)
		}
	}

	if plan != nil {
		// All {param} template occurrences are required — if any are absent at
		// runtime, _fill passes the literal placeholder through, which corrupts
		// the URL, query string, body, or header value sent to the external API.
		// URL template placeholders.
		for _, m := range urlParamRe.FindAllStringSubmatch(plan.URL, -1) {
			add(m[1], true)
		}
		// Body field templates.
		for _, v := range plan.BodyFields {
			for _, m := range urlParamRe.FindAllStringSubmatch(v, -1) {
				add(m[1], true)
			}
		}
		// Query param templates.
		for _, v := range plan.Query {
			for _, m := range urlParamRe.FindAllStringSubmatch(v, -1) {
				add(m[1], true)
			}
		}
		// Header value templates.
		for _, v := range plan.Headers {
			for _, m := range urlParamRe.FindAllStringSubmatch(v, -1) {
				add(m[1], true)
			}
		}
	}

	for _, p := range required {
		add(p, true)
	}
	for _, p := range optional {
		add(p, false)
	}

	if len(allParams) == 0 {
		return nil
	}

	properties := make(map[string]any, len(allParams))
	for _, p := range allParams {
		properties[p] = map[string]any{
			"type":        "string",
			"description": p,
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(reqParams) > 0 {
		schema["required"] = reqParams
	}
	return schema
}

// ── Python run script generator ──────────────────────────────────────────────

// buildRunScript returns a self-contained, stdlib-only Python 3 script that
// implements the Atlas custom skill subprocess protocol (one JSON line in,
// one JSON line out). It handles both "http" and "local" plan types.
//
// The plans are embedded via json.loads() so that JSON null/true/false
// are correctly translated to Python None/True/False — embedding raw JSON
// as a Python literal would cause a NameError on null.
func buildRunScript(plansJSON string) string {
	// Escape the JSON for embedding inside a Python single-quoted string.
	// Backslashes must be escaped first, then single quotes.
	safeJSON := strings.ReplaceAll(plansJSON, `\`, `\\`)
	safeJSON = strings.ReplaceAll(safeJSON, `'`, `\'`)

	const tmpl = `#!/usr/bin/env python3
"""
Atlas Forge-generated skill runner.
Auto-generated — do not edit manually.
Protocol: one JSON line on stdin, one JSON line on stdout.
  stdin:  {"action": "<name>", "args": {"param": "value", ...}}
  stdout: {"success": true,  "output": "<text>"}
          {"success": false, "error":  "<message>"}
"""
import base64
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

PLANS = json.loads('%s')

def _norm(s):
    """Normalize an action ID for fuzzy matching.
    Strips all non-alphanumeric characters and lowercases so that
    camelCase, kebab-case, and snake_case variants all compare equal.
    e.g. getRandomFact / get-random-fact / get_random_fact → getrandomfact
    """
    return re.sub(r'[^a-z0-9]', '', s.lower())

def _find_plan(action_name):
    """Return the full plan entry dict for action_name, or None if not found."""
    norm = _norm(action_name)
    for p in PLANS:
        aid = p.get("actionID", "")
        tail = aid.rsplit(".", 1)[-1]
        # Exact match first, then normalized match (handles camelCase vs kebab vs snake).
        if aid == action_name or tail == action_name:
            return p
        if _norm(aid) == norm or _norm(tail) == norm:
            return p
    return None

def _secret(key):
    """Resolve a credential: env vars → standalone Keychain item → Atlas bundle customSecrets."""
    if not key:
        return ""
    for variant in (key, key.upper(), key.upper().replace("-", "_").replace(".", "_")):
        val = os.environ.get(variant)
        if val is not None:
            return val
    try:
        # 1. Standalone Keychain generic-password item (service name = key).
        result = subprocess.run(
            ["security", "find-generic-password", "-s", key, "-w"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode == 0:
            return result.stdout.strip()
        # 2. Atlas credential bundle — custom API keys are stored inside this JSON blob
        #    under customSecrets[key], not as individual Keychain items.
        result = subprocess.run(
            ["security", "find-generic-password",
             "-s", "com.projectatlas.credentials", "-a", "bundle", "-w"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode == 0:
            bundle = json.loads(result.stdout.strip())
            val = (bundle.get("customSecrets") or {}).get(key, "")
            if val:
                return val
    except Exception:
        pass
    return ""

def _fill(template, args):
    """Replace {param} placeholders with raw values from args (headers, body fields)."""
    return re.sub(r"\{([^}]+)\}", lambda m: str(args.get(m.group(1), m.group(0))), template)

def _fill_path(template, args):
    """Replace {param} placeholders in a URL path with percent-encoded values.

    Uses urllib.parse.quote with safe="" so spaces and all reserved characters
    are encoded (e.g. 'Diamonds by Rihanna' -> 'Diamonds%%20by%%20Rihanna').
    Falls back to the literal placeholder if the key is absent so the leftover
    guard downstream can catch it.
    """
    def encode(m):
        val = args.get(m.group(1))
        if val is None:
            return m.group(0)   # keep placeholder; leftover guard will reject it
        return urllib.parse.quote(str(val), safe="")
    return re.sub(r"\{([^}]+)\}", encode, template)

_SKILL_DIR = os.path.dirname(os.path.abspath(__file__))

def _get_required_params(action_name):
    """Return the required param list for action_name by reading skill.json.

    skill.json is the canonical source — it records required params for ALL
    mapping modes (path, query, body, contract-declared, script placeholders)
    not just URL tokens. Falls back to [] on any error so existing behaviour
    is preserved.
    """
    try:
        with open(os.path.join(_SKILL_DIR, "skill.json")) as f:
            manifest = json.load(f)
        norm = _norm(action_name)
        for act in manifest.get("actions", []):
            if _norm(act.get("name", "")) == norm:
                params = act.get("parameters") or {}
                return list(params.get("required") or [])
    except Exception:
        pass
    return []

def _oauth2_token(token_url, client_id, client_secret, scope):
    """Exchange client credentials for an OAuth2 bearer token."""
    data = {"grant_type": "client_credentials", "client_id": client_id, "client_secret": client_secret}
    if scope:
        data["scope"] = scope
    body = urllib.parse.urlencode(data).encode("utf-8")
    req = urllib.request.Request(token_url, data=body,
                                 headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(req, timeout=25) as resp:
            token_data = json.loads(resp.read().decode("utf-8"))
            token = token_data.get("access_token", "")
            if not token:
                return "", "OAuth2 token response missing access_token"
            return token, None
    except urllib.error.HTTPError as e:
        detail = e.read().decode("utf-8", errors="replace")[:300]
        return "", f"OAuth2 token exchange HTTP {e.code}: {detail}"
    except Exception as exc:
        return "", f"OAuth2 token exchange failed: {exc}"

def _run_http(plan, args):
    """Execute an HTTP plan against the external API."""
    method = plan.get("method", "GET").upper()
    url_template = plan.get("url", "")

    # Fail fast: URL path placeholders are required params.
    # If any are absent from args, reject before making any HTTP request.
    required_path_params = re.findall(r"\{([^}]+)\}", url_template)
    missing = [p for p in required_path_params if p not in args]
    if missing:
        return None, "Missing required parameter(s): " + ", ".join(missing)

    url = _fill_path(url_template, args)

    # Guard: if any {placeholder} survived substitution, something went wrong.
    # Reject rather than dispatch a malformed URL to the external service.
    leftover = re.findall(r"\{([^}]+)\}", url)
    if leftover:
        return None, "Unresolved URL placeholder(s) after substitution: " + ", ".join(leftover)

    headers = {k: _fill(v, args) for k, v in (plan.get("headers") or {}).items()}
    query   = {k: _fill(v, args) for k, v in (plan.get("query") or {}).items()}
    body_fields  = dict(plan.get("bodyFields") or {})
    static_body  = dict(plan.get("staticBodyFields") or {})

    # Set a browser-like User-Agent by default so CDN/WAF bot filters (e.g.
    # Cloudflare error 1010) don't block the request.  Plans that already
    # specify a User-Agent header will override this.
    headers.setdefault("User-Agent",
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
        "AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/124.0.0.0 Safari/537.36"
    )

    auth_type        = plan.get("authType", "none")
    auth_secret_key  = plan.get("authSecretKey", "")
    auth_header_name = plan.get("authHeaderName", "")
    auth_query_param = plan.get("authQueryParamName", "")
    secret_header    = plan.get("secretHeader", "")  # legacy: key name for bearer injection
    secret = _secret(auth_secret_key)

    # Apply authentication. Auth type values match Go canonical strings exactly.
    if auth_type == "bearerTokenStatic":
        headers["Authorization"] = "Bearer " + secret
    elif secret_header and auth_type in ("", "none"):
        # Legacy: secretHeader was used before authType existed; only fires when
        # no explicit authType is set so it cannot override apiKeyHeader etc.
        headers["Authorization"] = "Bearer " + _secret(secret_header)
    elif auth_type == "apiKeyHeader" and auth_header_name:
        headers[auth_header_name] = secret
    elif auth_type == "apiKeyQuery" and auth_query_param:
        query[auth_query_param] = secret
    elif auth_type == "basicAuth":
        parts = secret.split(":", 1)
        u, p = parts[0], parts[1] if len(parts) > 1 else ""
        encoded = base64.b64encode(f"{u}:{p}".encode()).decode()
        headers["Authorization"] = "Basic " + encoded
    elif auth_type == "oauth2ClientCredentials":
        token_url      = plan.get("oauth2TokenURL", "")
        client_id      = _secret(plan.get("oauth2ClientIDKey", ""))
        client_secret  = _secret(plan.get("oauth2ClientSecretKey", ""))
        scope          = plan.get("oauth2Scope", "")
        token, err = _oauth2_token(token_url, client_id, client_secret, scope)
        if err:
            return None, err
        headers["Authorization"] = "Bearer " + token

    # Build request body from static + dynamic fields.
    body = dict(static_body)
    for field, tmpl in body_fields.items():
        body[field] = _fill(tmpl, args)

    # Guard: reject if any unresolved {placeholder} survived in body field values.
    body_leftover = [f"{field}={val}" for field, val in body.items()
                     if isinstance(val, str) and re.search(r"\{[^}]+\}", val)]
    if body_leftover:
        return None, "Unresolved body placeholder(s) after substitution: " + ", ".join(body_leftover)

    # Route remaining args: into body for mutation methods, query for reads.
    # Track all params already placed so auto-routing doesn't double-inject them.
    placed = set(re.findall(r"\{([^}]+)\}", plan.get("url", "")))
    for tmpl in body_fields.values():
        placed.update(re.findall(r"\{([^}]+)\}", tmpl))
    # Header value templates — params used here must not be auto-routed to query/body.
    for tmpl in (plan.get("headers") or {}).values():
        placed.update(re.findall(r"\{([^}]+)\}", tmpl))
    for k, v in args.items():
        if k not in placed and k not in query and k not in body:
            if method in ("POST", "PUT", "PATCH"):
                body[k] = v
            else:
                query[k] = str(v)

    # Append query string.
    if query:
        sep = "&" if "?" in url else "?"
        url = url + sep + urllib.parse.urlencode(query)

    data = None
    if method in ("POST", "PUT", "PATCH"):
        data = json.dumps(body).encode("utf-8")
        headers.setdefault("Content-Type", "application/json")

    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=25) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            try:
                return json.dumps(json.loads(raw), indent=2), None
            except json.JSONDecodeError:
                return raw, None
    except urllib.error.HTTPError as e:
        detail = e.read().decode("utf-8", errors="replace")[:500]
        return None, f"HTTP {e.code} {e.reason}: {detail}"
    except Exception as exc:
        return None, str(exc)

def _run_local(plan, args):
    """Execute a local macOS script via the specified interpreter.

    {param} placeholders in the script body are substituted with raw arg values
    before execution. The interpreter must be one of: osascript, bash, sh, python3.
    """
    interpreter = plan.get("interpreter", "osascript")
    script_template = plan.get("script", "")
    if not script_template:
        return None, "Local plan has an empty script."

    # Substitute {param} placeholders with their argument values.
    # Uses raw (unencoded) substitution — the script receives the literal value.
    script = _fill(script_template, args)

    # Guard: reject if any {placeholder} survived (missing required arg).
    leftover = re.findall(r"\{([^}]+)\}", script)
    if leftover:
        return None, "Missing required parameter(s): " + ", ".join(leftover)

    # Build the command based on interpreter.
    if interpreter == "osascript":
        cmd = ["osascript", "-e", script]
    elif interpreter in ("bash", "sh"):
        cmd = [interpreter, "-c", script]
    elif interpreter == "python3":
        cmd = ["python3", "-c", script]
    else:
        return None, f"Unsupported interpreter: {interpreter!r}"

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            err = result.stderr.strip() or f"exited with code {result.returncode}"
            return None, f"Script failed: {err}"
        out = result.stdout.strip()
        return out if out else "Done.", None
    except subprocess.TimeoutExpired:
        return None, "Script timed out after 30 seconds."
    except Exception as exc:
        return None, f"Script error: {exc}"

def main():
    line = sys.stdin.readline()
    if not line.strip():
        print(json.dumps({"success": False, "error": "empty input"}))
        return
    try:
        req = json.loads(line)
    except json.JSONDecodeError as exc:
        print(json.dumps({"success": False, "error": "invalid JSON: " + str(exc)}))
        return

    action = req.get("action", "")
    args   = req.get("args") or {}

    plan_entry = _find_plan(action)
    if plan_entry is None:
        print(json.dumps({"success": False, "error": f"unknown action: {action}"}))
        return

    # Shared required-param gate — covers path, query, body, script placeholders,
    # and contract-declared required params regardless of mapping mode.
    required = _get_required_params(action)
    missing = [p for p in required if p not in args]
    if missing:
        print(json.dumps({"success": False, "error": "Missing required parameter(s): " + ", ".join(missing)}))
        return

    plan_type = plan_entry.get("type", "http")
    if plan_type == "local":
        output, err = _run_local(plan_entry.get("localPlan") or {}, args)
    else:
        output, err = _run_http(plan_entry.get("httpRequest") or {}, args)

    if err is not None:
        print(json.dumps({"success": False, "error": err}))
    else:
        print(json.dumps({"success": True, "output": output}))

if __name__ == "__main__":
    main()
`
	return fmt.Sprintf(tmpl, safeJSON)
}
