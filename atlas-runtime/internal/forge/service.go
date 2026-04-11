package forge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/forge/forgetypes"
	"atlas-runtime-go/internal/logstore"
)

// Service manages the Forge proposal lifecycle.
// It holds in-memory researching state and delegates persistence to store.go.
type Service struct {
	mu          sync.RWMutex
	researching []ResearchingItem
	supportDir  string
}

type InstallTarget struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

// NewService returns a ready Forge Service.
func NewService(supportDir string) *Service {
	return &Service{supportDir: supportDir}
}

// GetResearching returns a snapshot of the in-flight research items.
func (s *Service) GetResearching() []ResearchingItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.researching == nil {
		return []ResearchingItem{}
	}
	out := make([]ResearchingItem, len(s.researching))
	copy(out, s.researching)
	return out
}

// validateProposalAPIURL returns a non-empty error string if rawURL uses a
// placeholder/example domain or is otherwise unsuitable as a real API base URL.
func validateProposalAPIURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Sprintf("invalid API URL %q: %v", rawURL, err)
	}
	host := strings.ToLower(u.Hostname())
	if forgetypes.PlaceholderDomains[host] {
		return fmt.Sprintf("domain %q is a placeholder — provide the real API base URL", host)
	}
	return ""
}

// Propose runs the full research pipeline for a new skill proposal:
//  1. Validates the API URL against the placeholder domain blocklist.
//  2. Adds a ResearchingItem to the in-memory list.
//  3. Calls the AI to generate a structured ForgeProposal.
//  4. Validates the AI-returned domains against the same blocklist.
//  5. Saves the proposal to forge-proposals.json.
//  6. Removes the ResearchingItem.
//
// It runs synchronously so the caller can decide whether to background it.
// Returns the created ForgeProposal on success.
func (s *Service) Propose(ctx context.Context, req ProposeRequest, provider AIProvider) (ForgeProposal, error) {
	// Gate: reject placeholder/example API URLs before spending an AI call.
	if msg := validateProposalAPIURL(req.APIURL); msg != "" {
		return ForgeProposal{}, fmt.Errorf("forge: %s", msg)
	}

	id := newID()
	now := time.Now().UTC().Format(time.RFC3339)

	item := ResearchingItem{
		ID:        id,
		Title:     req.Name,
		Message:   fmt.Sprintf("Researching \"%s\"…", req.Name),
		StartedAt: now,
	}
	s.addResearching(item)
	defer s.removeResearching(id)

	logstore.Write("info", "Forge research started: "+req.Name, map[string]string{"id": id})
	researchStart := time.Now()

	proposal, err := s.research(ctx, id, req, provider)
	elapsed := fmt.Sprintf("%.1fs", time.Since(researchStart).Seconds())
	if err != nil {
		logstore.Write("error", "Forge research failed: "+req.Name,
			map[string]string{"id": id, "elapsed": elapsed, "error": err.Error()})
		return ForgeProposal{}, err
	}

	// Gate: validate AI-returned domains — reject if the model fabricated placeholder hostnames.
	for _, domain := range proposal.Domains {
		if forgetypes.PlaceholderDomains[strings.ToLower(domain)] {
			return ForgeProposal{}, fmt.Errorf(
				"forge: AI returned placeholder domain %q — provide a real API URL and try again", domain)
		}
	}

	if err := SaveProposal(s.supportDir, proposal); err != nil {
		logstore.Write("error", "Forge save failed: "+req.Name,
			map[string]string{"id": id, "error": err.Error()})
		return ForgeProposal{}, fmt.Errorf("forge: save proposal: %w", err)
	}

	logstore.Write("info", "Forge research complete: "+proposal.Name,
		map[string]string{"id": id, "skill": proposal.SkillID, "elapsed": elapsed})
	return proposal, nil
}

// research calls the AI in two stages and parses the response into a ForgeProposal.
//
// Stage 1 (draft): kind-specific conceptual exploration — the model's persona and
// prompt vocabulary are tailored to the skill type (HTTP API / local macOS / workflow).
//
// Stage 2 (formalize): feeds Stage 1's output back with a schema checklist that is
// also kind-specific, explicitly preventing the wrong plan type (e.g. HTTP plans in
// a local skill) and verifying actionID matching, auth completeness, and real URLs.
//
// Splitting the work this way catches structurally plausible but semantically wrong
// plans before the 10-gate validation layer runs.
func (s *Service) research(ctx context.Context, id string, req ProposeRequest, provider AIProvider) (ForgeProposal, error) {
	kind := inferSkillKind(req)

	// Kind-specific system prompts anchor the model's persona for each stage.
	var draftSystem, formalizeSystem string
	switch kind {
	case "local":
		draftSystem = "You are an expert macOS automation engineer. Think carefully about macOS system commands, AppleScript, and shell scripting. Return a JSON draft of your findings."
		formalizeSystem = "You are a strict macOS skill spec formatter. Your ONLY output is valid JSON — no markdown fences, no prose."
	case "workflow":
		draftSystem = "You are an expert AI workflow designer. Think carefully about how to chain Atlas tools and LLM reasoning steps. Return a JSON draft of your workflow design."
		formalizeSystem = "You are a strict Atlas workflow spec formatter. Your ONLY output is valid JSON — no markdown fences, no prose."
	default:
		draftSystem = "You are an expert API researcher. Think carefully about the API and return a JSON draft of your research findings. Be thorough — focus on accuracy, not exact field names."
		formalizeSystem = "You are a strict API spec formatter. Your ONLY output is valid JSON — no markdown fences, no prose."
	}

	// Stage 1: conceptual research / design.
	draftRaw, err := provider.CallNonStreaming(ctx, draftSystem, buildDraftPrompt(req))
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: AI draft call: %w", err)
	}
	draftRaw = stripFences(draftRaw)

	// Stage 2: formalize into exact schema with kind-appropriate consistency checks.
	raw, err := provider.CallNonStreaming(ctx, formalizeSystem, buildFormalizePrompt(req, draftRaw))
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: AI formalize call: %w", err)
	}
	raw = stripFences(raw)

	return parseProposalResponse(id, req, raw)
}

// stripFences removes markdown code fences that models sometimes wrap JSON in.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// BuildInstalledRecord converts a ForgeProposal into a SkillRecord-shaped map
// suitable for forge-installed.json. Returns an error if SpecJSON is malformed —
// a broken install is worse than a failed install.
func BuildInstalledRecord(p ForgeProposal, lifecycleState string, target *InstallTarget) (map[string]any, error) {
	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(p.SpecJSON), &spec); err != nil {
		return nil, fmt.Errorf("forge: BuildInstalledRecord: malformed SpecJSON for proposal %q: %w", p.ID, err)
	}

	isEnabled := lifecycleState == "enabled"
	actions := []map[string]any{}
	for _, action := range spec.Actions {
		permissionLevel := action.PermissionLevel
		if permissionLevel == "" {
			permissionLevel = "read"
		}
		actions = append(actions, map[string]any{
			"id":              p.SkillID + "." + slugify(action.ID),
			"name":            action.Name,
			"description":     action.Description,
			"permissionLevel": permissionLevel,
			"approvalPolicy":  "auto_approve",
			"isEnabled":       isEnabled,
		})
	}
	requiredSecrets := p.RequiredSecrets
	if requiredSecrets == nil {
		requiredSecrets = []string{}
	}
	metadata := map[string]any{}
	if target != nil && strings.TrimSpace(target.Type) != "" {
		metadata["executorType"] = target.Type
		metadata["target"] = map[string]any{"type": target.Type, "ref": target.Ref}
	}
	return map[string]any{
		"id": p.SkillID,
		"manifest": map[string]any{
			"id":              p.SkillID,
			"name":            p.Name,
			"version":         "1.0",
			"description":     p.Description,
			"lifecycleState":  lifecycleState,
			"riskLevel":       p.RiskLevel,
			"isUserVisible":   true,
			"category":        spec.Category,
			"source":          "forge",
			"capabilities":    []string{},
			"tags":            spec.Tags,
			"requiredSecrets": requiredSecrets,
			"metadata":        metadata,
		},
		"actions":         actions,
		"requiredSecrets": requiredSecrets,
		"target":          metadata["target"],
	}, nil
}

// ── Skill type inference ──────────────────────────────────────────────────────

// inferSkillKind determines the execution model for a proposal:
//   - "http"     → req.APIURL is set (external REST/GraphQL integration)
//   - "workflow" → no apiURL + description implies LLM reasoning or synthesis
//   - "local"    → no apiURL, macOS automation (osascript, bash, system tools)
func inferSkillKind(req ProposeRequest) string {
	if req.APIURL != "" {
		return "http"
	}
	desc := strings.ToLower(req.Name + " " + req.Description)
	for _, kw := range []string{
		"llm", " ai ", "synthesize", "summarize", "analyze", "reasoning",
		"briefing", "compile", "research and", "review with",
	} {
		if strings.Contains(desc, kw) {
			return "workflow"
		}
	}
	return "local"
}

// ── Draft prompt builders (Stage 1) ──────────────────────────────────────────

// buildDraftPrompt routes to the kind-appropriate Stage 1 draft prompt.
func buildDraftPrompt(req ProposeRequest) string {
	switch inferSkillKind(req) {
	case "local":
		return buildLocalDraftPrompt(req)
	case "workflow":
		return buildWorkflowDraftPrompt(req)
	default:
		return buildHTTPDraftPrompt(req)
	}
}

// buildHTTPDraftPrompt is Stage 1 for external HTTP API skills.
func buildHTTPDraftPrompt(req ProposeRequest) string {
	var sb strings.Builder
	sb.WriteString("Research the following API integration and return your findings as JSON.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	if req.APIURL != "" {
		sb.WriteString("API base URL: " + req.APIURL + "\n")
	}
	sb.WriteString("\nConstraints:\n")
	sb.WriteString("- Do not propose a Forge skill for creating PDFs, DOCX files, ZIP archives, or image files. Atlas already handles those natively with fs.create_pdf, fs.create_docx, fs.create_zip, fs.save_image, and fs.write_binary_file.\n")
	sb.WriteString("- If a local plan uses python3, use the Python standard library only. Do not rely on reportlab, pillow, requests, or any other third-party dependency.\n")
	sb.WriteString("- Prefer a real external API integration or true macOS automation. Do not invent local file-generation skills.\n")
	sb.WriteString(`
Return a JSON object (field names can be loose — this is a draft):
{
  "purpose": "what this skill does and why it is useful",
  "endpoints": [
    {
      "actionName": "human name for this action",
      "method": "GET | POST | PUT | PATCH | DELETE",
      "url": "absolute HTTPS endpoint URL — use {placeholder} for path params",
      "description": "what this endpoint does"
    }
  ],
  "authMechanism": "none | apiKeyHeader | apiKeyQuery | bearerTokenStatic | basicAuth | oauth2ClientCredentials",
  "credentialNames": ["Keychain key names needed, e.g. 'githubAPIKey' — empty array if none"],
  "contactedDomains": ["hostnames this skill contacts, e.g. 'api.github.com'"],
  "riskLevel": "low | medium | high",
  "rationale": "why Atlas users benefit from this skill"
}`)
	return sb.String()
}

// buildLocalDraftPrompt is Stage 1 for macOS local automation skills.
// These run shell commands, AppleScript, or stdlib python3 locally — no HTTP, no API keys.
func buildLocalDraftPrompt(req ProposeRequest) string {
	var sb strings.Builder
	sb.WriteString("Design a macOS local automation skill using shell commands or AppleScript.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	sb.WriteString("\nConstraints:\n")
	sb.WriteString("- Do not propose for creating PDFs, DOCX files, ZIP archives, or image files — Atlas already handles those natively with fs.create_pdf, fs.create_docx, fs.create_zip, fs.save_image, and fs.write_binary_file.\n")
	sb.WriteString("- This skill runs LOCALLY on macOS. It does NOT make HTTP requests to any URL.\n")
	sb.WriteString("- Interpreter must be one of: osascript, bash, sh, python3 (standard library only).\n")
	sb.WriteString("- For Finder, Calendar, Contacts, Reminders, Safari: use osascript (AppleScript or JXA).\n")
	sb.WriteString("- For system info: use bash with system_profiler, sysctl, top, df, diskutil, etc.\n")
	sb.WriteString("- For file operations: use bash.\n")
	sb.WriteString("- Use {param} placeholders in scripts for runtime arguments.\n")
	sb.WriteString(`
Return a JSON object describing the macOS commands to run:
{
  "purpose": "what this skill does and why it is useful",
  "actions": [
    {
      "actionName": "human-readable action name",
      "interpreter": "osascript | bash | sh | python3",
      "script": "the actual command or AppleScript to run — use {param} for arguments",
      "description": "what this action does"
    }
  ],
  "riskLevel": "low | medium | high",
  "rationale": "why Atlas users benefit from this skill"
}`)
	return sb.String()
}

// buildWorkflowDraftPrompt is Stage 1 for multi-step AI workflow skills.
// These chain Atlas built-in tools and LLM reasoning steps — no direct HTTP calls.
func buildWorkflowDraftPrompt(req ProposeRequest) string {
	var sb strings.Builder
	sb.WriteString("Design a multi-step AI workflow skill for Atlas.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	sb.WriteString("\nThis skill chains Atlas tools and LLM reasoning — it does NOT call external HTTP APIs directly.\n")
	sb.WriteString("\nAvailable Atlas tools (for atlas.tool steps):\n")
	sb.WriteString("  websearch.search — search the web for a query\n")
	sb.WriteString("  weather.current  — get current weather for a location\n")
	sb.WriteString("  fs.read_file     — read a local file\n")
	sb.WriteString("  diary.read       — read the Atlas diary\n")
	sb.WriteString("  system.info      — get macOS system information\n")
	sb.WriteString("\nWorkflow step types:\n")
	sb.WriteString("  atlas.tool   — call one of the Atlas built-in tools above\n")
	sb.WriteString("  llm.generate — ask the LLM to analyze, synthesize, or generate text\n")
	sb.WriteString("  return       — collect and return the final result\n")
	sb.WriteString(`
Return a JSON object describing the workflow:
{
  "purpose": "what this workflow does and why it is useful",
  "actions": [
    {
      "actionName": "human-readable name for the whole capability",
      "steps": [
        {"type": "atlas.tool", "action": "websearch.search", "args": {"query": "{topic}"}, "description": "search"},
        {"type": "llm.generate", "prompt": "Summarize these results about {topic}: ...", "description": "synthesize"},
        {"type": "return", "description": "return the summary"}
      ]
    }
  ],
  "riskLevel": "low",
  "rationale": "why Atlas users benefit from this workflow"
}`)
	return sb.String()
}

// ── Formalize prompt builders (Stage 2) ──────────────────────────────────────

// buildFormalizePrompt routes to the kind-appropriate Stage 2 formalize prompt.
func buildFormalizePrompt(req ProposeRequest, draft string) string {
	switch inferSkillKind(req) {
	case "local":
		return buildLocalFormalizePrompt(req, draft)
	case "workflow":
		return buildWorkflowFormalizePrompt(req, draft)
	default:
		return buildHTTPFormalizePrompt(req, draft)
	}
}

// buildHTTPFormalizePrompt is Stage 2 for HTTP API skills.
func buildHTTPFormalizePrompt(req ProposeRequest, draft string) string {
	var sb strings.Builder
	sb.WriteString("Below is a research draft for an Atlas HTTP API skill. Convert it into the exact final JSON schema.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	if req.APIURL != "" {
		sb.WriteString("API base URL: " + req.APIURL + "\n")
	}
	sb.WriteString("\n=== DRAFT ===\n")
	sb.WriteString(draft)
	sb.WriteString("\n=== END DRAFT ===\n")
	sb.WriteString(`
Before returning, verify ALL of the following:
1. Every plans[].actionID is IDENTICAL to one of the id values in spec.actions[] — no orphans, no mismatches.
2. All URLs in plans[] are real absolute HTTPS endpoints — no placeholder domains (example.com, test.com, your-api.com, localhost, etc.).
3. Auth fields are complete for the chosen authType:
   - apiKeyHeader       → requires authSecretKey + authHeaderName
   - apiKeyQuery        → requires authSecretKey + authQueryParamName
   - bearerTokenStatic  → requires authSecretKey
   - basicAuth          → requires authSecretKey
   - oauth2ClientCredentials → requires oauth2ClientIDKey + oauth2ClientSecretKey + oauth2TokenURL
4. spec.actions[].id and plans[].actionID are lowercase-hyphenated (e.g. "get-user", not "getUser").
5. plans[].type is "http" and plans[].httpRequest is set — never use localPlan or workflowStep here.

Return a JSON object with these EXACT fields:
{
  "name": "human-readable skill name",
  "description": "one sentence description",
  "summary": "2-3 sentence summary of what this skill does and why it is useful",
  "rationale": "why Atlas users would benefit from this skill",
  "requiredSecrets": ["list of API key names needed — empty array if none"],
  "domains": ["list of hostnames this skill contacts"],
  "actionNames": ["list of human-readable action names"],
  "riskLevel": "low | medium | high",
  "spec": {
    "id": "lowercase-hyphenated skill id",
    "name": "display name",
    "description": "one sentence description",
    "category": "system | utility | creative | communication | automation | research | developer | productivity",
    "riskLevel": "low | medium | high",
    "tags": ["short", "searchable", "labels"],
    "actions": [
      {
        "id": "lowercase-hyphenated action id",
        "name": "human-readable action name",
        "description": "one sentence description",
        "permissionLevel": "read | draft | execute"
      }
    ]
  },
  "plans": [
    {
      "actionID": "must exactly match spec.actions[].id",
      "type": "http",
      "httpRequest": {
        "method": "GET | POST | PUT | PATCH | DELETE",
        "url": "absolute HTTPS endpoint URL",
        "authType": "none | apiKeyHeader | apiKeyQuery | bearerTokenStatic | basicAuth | oauth2ClientCredentials",
        "authSecretKey": "keychain key name (required for all auth types except none)",
        "authHeaderName": "header name (required for apiKeyHeader only)",
        "authQueryParamName": "query param name (required for apiKeyQuery only)"
      }
    }
  ]
}`)
	return sb.String()
}

// buildLocalFormalizePrompt is Stage 2 for local macOS automation skills.
func buildLocalFormalizePrompt(req ProposeRequest, draft string) string {
	var sb strings.Builder
	sb.WriteString("Below is a design draft for a macOS local automation skill. Convert it into the exact final JSON schema.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	sb.WriteString("\n=== DRAFT ===\n")
	sb.WriteString(draft)
	sb.WriteString("\n=== END DRAFT ===\n")
	sb.WriteString(`
CRITICAL RULES for local macOS skills — violations will cause installation failure:
1. plans[].type MUST be "local" — NEVER use "http" or "httpRequest" for a local macOS skill.
2. plans[].localPlan MUST have both: interpreter (osascript|bash|sh|python3) and a non-empty script.
3. Use {param} placeholders in scripts for runtime arguments (e.g. {appName}, {query}).
4. domains[] MUST be [] — local skills make no HTTP requests.
5. requiredSecrets[] MUST be [] — local skills use no API keys.
6. Every plans[].actionID MUST exactly match one of the id values in spec.actions[].

Return a JSON object with these EXACT fields:
{
  "name": "human-readable skill name",
  "description": "one sentence description",
  "summary": "2-3 sentence summary",
  "rationale": "why Atlas users benefit from this skill",
  "requiredSecrets": [],
  "domains": [],
  "actionNames": ["list of human-readable action names"],
  "riskLevel": "low | medium | high",
  "spec": {
    "id": "lowercase-hyphenated-skill-id",
    "name": "display name",
    "description": "one sentence description",
    "category": "system | utility | automation",
    "riskLevel": "low | medium | high",
    "tags": ["macos", "local"],
    "actions": [
      {
        "id": "lowercase-hyphenated-action-id",
        "name": "human-readable action name",
        "description": "one sentence description",
        "permissionLevel": "read"
      }
    ]
  },
  "plans": [
    {
      "actionID": "must exactly match spec.actions[].id",
      "type": "local",
      "localPlan": {
        "interpreter": "bash",
        "script": "system_profiler SPPowerDataType | grep -E 'Charge Remaining|State of Charge|Condition'"
      }
    }
  ]
}`)
	return sb.String()
}

// buildWorkflowFormalizePrompt is Stage 2 for multi-step AI workflow skills.
func buildWorkflowFormalizePrompt(req ProposeRequest, draft string) string {
	var sb strings.Builder
	sb.WriteString("Below is a design draft for a multi-step Atlas workflow skill. Convert it into the exact final JSON schema.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	sb.WriteString("\n=== DRAFT ===\n")
	sb.WriteString(draft)
	sb.WriteString("\n=== END DRAFT ===\n")
	sb.WriteString(`
CRITICAL RULES for workflow skills — violations will cause installation failure:
1. plans[].type MUST be one of: "llm.generate", "atlas.tool", or "return" — NEVER "http".
2. plans[].workflowStep MUST be set — never use httpRequest or localPlan here.
3. For "atlas.tool": workflowStep MUST have title, action (e.g. "websearch.search"), and args.
4. For "llm.generate": workflowStep MUST have title and prompt (use {param} for runtime values).
5. For "return": workflowStep MUST have title.
6. Every plans[].actionID MUST exactly match one of the id values in spec.actions[].
7. Multiple plans can share one actionID — they form a sequential pipeline for that action.
8. domains[] MUST be [] — workflow skills use Atlas tools, not direct HTTP calls.

Return a JSON object with these EXACT fields:
{
  "name": "human-readable skill name",
  "description": "one sentence description",
  "summary": "2-3 sentence summary",
  "rationale": "why Atlas users benefit from this workflow",
  "requiredSecrets": [],
  "domains": [],
  "actionNames": ["the capability name"],
  "riskLevel": "low",
  "spec": {
    "id": "lowercase-hyphenated-skill-id",
    "name": "display name",
    "description": "one sentence description",
    "category": "automation | research | productivity",
    "riskLevel": "low",
    "tags": ["workflow", "ai"],
    "actions": [
      {
        "id": "lowercase-hyphenated-action-id",
        "name": "human-readable action name",
        "description": "one sentence description",
        "permissionLevel": "read"
      }
    ]
  },
  "plans": [
    {
      "actionID": "must match spec.actions[].id",
      "type": "atlas.tool",
      "workflowStep": {
        "title": "Search Web",
        "action": "websearch.search",
        "args": {"query": "{topic}"}
      }
    },
    {
      "actionID": "must match spec.actions[].id",
      "type": "llm.generate",
      "workflowStep": {
        "title": "Synthesize Results",
        "prompt": "Analyze the following and produce a structured summary with key findings: ..."
      }
    },
    {
      "actionID": "must match spec.actions[].id",
      "type": "return",
      "workflowStep": {"title": "Return Summary"}
    }
  ]
}`)
	return sb.String()
}

// PersistProposal creates a ForgeProposal from pre-researched agent data and
// persists it directly to disk without running AI research. Used by the
// in-agent forge.orchestration.propose skill action.
func (s *Service) PersistProposal(spec ForgeSkillSpec, plans []ForgeActionPlan, summary, rationale, contractJSON string) (ForgeProposal, error) {
	id := newID()
	now := time.Now().UTC().Format(time.RFC3339)

	// Extract unique hostnames from plan HTTP URLs.
	domainSet := map[string]bool{}
	for _, plan := range plans {
		if plan.HTTPRequest != nil && plan.HTTPRequest.URL != "" {
			if u, err := url.Parse(plan.HTTPRequest.URL); err == nil && u.Host != "" {
				domainSet[u.Host] = true
			}
		}
	}
	domains := make([]string, 0, len(domainSet))
	for d := range domainSet {
		domains = append(domains, d)
	}

	// Collect unique required Keychain secrets from auth fields.
	secretSet := map[string]bool{}
	for _, plan := range plans {
		h := plan.HTTPRequest
		if h == nil {
			continue
		}
		for _, key := range []string{h.AuthSecretKey, h.OAuth2ClientIDKey, h.OAuth2ClientSecretKey, h.SecretHeader} {
			if key != "" {
				secretSet[key] = true
			}
		}
	}
	secrets := make([]string, 0, len(secretSet))
	for k := range secretSet {
		secrets = append(secrets, k)
	}

	// Action names from spec.
	actionNames := make([]string, 0, len(spec.Actions))
	for _, a := range spec.Actions {
		actionNames = append(actionNames, a.Name)
	}

	specBytes, err := json.Marshal(spec)
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("marshal spec: %w", err)
	}
	plansBytes, err := json.Marshal(plans)
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("marshal plans: %w", err)
	}

	riskLevel := spec.RiskLevel
	if riskLevel == "" {
		riskLevel = "low"
	}

	proposal := ForgeProposal{
		ID:              id,
		SkillID:         spec.ID,
		Name:            spec.Name,
		Description:     spec.Description,
		Summary:         summary,
		Rationale:       rationale,
		RequiredSecrets: secrets,
		Domains:         domains,
		ActionNames:     actionNames,
		RiskLevel:       riskLevel,
		Status:          "pending",
		SpecJSON:        string(specBytes),
		PlansJSON:       string(plansBytes),
		ContractJSON:    contractJSON,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := SaveProposal(s.supportDir, proposal); err != nil {
		return ForgeProposal{}, fmt.Errorf("save proposal: %w", err)
	}
	return proposal, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// PersistProposalFromJSON is the JSON-string variant of PersistProposal.
// It decodes specJSON and plansJSON internally so callers (e.g. the skills
// package) do not need to import the forge package and its types.
// Returns the created proposal's ID, display name, skill ID, risk level,
// action names, and external domains.
func (s *Service) PersistProposalFromJSON(specJSON, plansJSON, summary, rationale, contractJSON string) (
	id, name, skillID, riskLevel string,
	actionNames, domains []string,
	err error,
) {
	var spec ForgeSkillSpec
	if err = json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return "", "", "", "", nil, nil, fmt.Errorf("decode spec: %w", err)
	}
	var plans []ForgeActionPlan
	if err = json.Unmarshal([]byte(plansJSON), &plans); err != nil {
		return "", "", "", "", nil, nil, fmt.Errorf("decode plans: %w", err)
	}
	p, err := s.PersistProposal(spec, plans, summary, rationale, contractJSON)
	if err != nil {
		return "", "", "", "", nil, nil, err
	}
	return p.ID, p.Name, p.SkillID, p.RiskLevel, p.ActionNames, p.Domains, nil
}

func (s *Service) addResearching(item ResearchingItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.researching = append(s.researching, item)
}

func (s *Service) removeResearching(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var remaining []ResearchingItem
	for _, item := range s.researching {
		if item.ID != id {
			remaining = append(remaining, item)
		}
	}
	s.researching = remaining
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			out.WriteRune('-')
		}
	}
	return strings.Trim(out.String(), "-")
}
