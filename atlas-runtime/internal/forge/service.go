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

// research calls the AI and parses the response into a ForgeProposal.
func (s *Service) research(ctx context.Context, id string, req ProposeRequest, provider AIProvider) (ForgeProposal, error) {
	system := "You are an expert API integration planner. Respond only with valid JSON — no markdown fences, no commentary."
	prompt := buildResearchPrompt(req)

	raw, err := provider.CallNonStreaming(ctx, system, prompt)
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: AI research call: %w", err)
	}

	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if the model wrapped the JSON anyway.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	return parseProposalResponse(id, req, raw)
}

// BuildInstalledRecord converts a ForgeProposal into a SkillRecord-shaped map
// suitable for forge-installed.json. The web UI should prefer the live registry
// view, but this snapshot remains useful for install/uninstall bookkeeping.
func BuildInstalledRecord(p ForgeProposal, lifecycleState string, target *InstallTarget) map[string]any {
	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(p.SpecJSON), &spec); err != nil {
		spec = ForgeSkillSpec{}
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
	}
}

// buildResearchPrompt creates the AI prompt for skill research.
func buildResearchPrompt(req ProposeRequest) string {
	var sb strings.Builder
	sb.WriteString("Research and propose a new Atlas skill integration.\n\n")
	sb.WriteString("Skill name: " + req.Name + "\n")
	sb.WriteString("Description: " + req.Description + "\n")
	if req.APIURL != "" {
		sb.WriteString("API base URL: " + req.APIURL + "\n")
	}
	sb.WriteString("Important constraints:\n")
	sb.WriteString("- Do not propose a Forge skill for creating PDFs, DOCX files, ZIP archives, or image files. Atlas already supports those natively with fs.create_pdf, fs.create_docx, fs.create_zip, fs.save_image, and fs.write_binary_file.\n")
	sb.WriteString("- If a local plan uses python3, it must use the Python standard library only. Do not rely on reportlab, pillow, python-docx, pypdf, requests, or any other third-party dependency.\n")
	sb.WriteString("- Prefer a real external API integration or a true local app automation workflow. Do not invent custom local file-generation skills for capabilities Atlas already has.\n")
	sb.WriteString(`
Return a JSON object with these exact fields:
{
  "name": "human-readable skill name",
  "description": "one sentence description",
  "summary": "2-3 sentence summary of what this skill does and why it is useful",
  "rationale": "why Atlas users would benefit from this skill",
  "requiredSecrets": ["list of API key names needed, e.g. 'myAPIKey' — empty array if no key required"],
  "domains": ["list of hostnames this skill contacts, e.g. 'api.example.com'"],
  "actionNames": ["list of 2-4 action names, e.g. 'Query Data', 'Get Details'"],
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
        "authType": "none | apiKeyHeader | apiKeyQuery | bearerTokenStatic | basicAuth | oauth2ClientCredentials"
      }
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
