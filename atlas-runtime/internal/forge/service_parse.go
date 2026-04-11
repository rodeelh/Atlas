package forge

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"atlas-runtime-go/internal/logstore"
)

type aiProposalResponse struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Summary         string            `json:"summary"`
	Rationale       string            `json:"rationale"`
	RequiredSecrets []string          `json:"requiredSecrets"`
	Domains         []string          `json:"domains"`
	ActionNames     []string          `json:"actionNames"`
	RiskLevel       string            `json:"riskLevel"`
	Spec            *ForgeSkillSpec   `json:"spec"`
	Plans           []ForgeActionPlan `json:"plans"`
}

func parseProposalResponse(id string, req ProposeRequest, raw string) (ForgeProposal, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	var aiResp aiProposalResponse
	if err := json.Unmarshal([]byte(raw), &aiResp); err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: invalid proposal JSON: %w", err)
	}

	if aiResp.Name == "" {
		aiResp.Name = req.Name
	}
	if aiResp.Description == "" {
		aiResp.Description = req.Description
	}
	if aiResp.Summary == "" {
		aiResp.Summary = aiResp.Description
	}
	if aiResp.RiskLevel == "" {
		aiResp.RiskLevel = "low"
	}
	if aiResp.RequiredSecrets == nil {
		aiResp.RequiredSecrets = []string{}
	}
	if aiResp.Domains == nil {
		aiResp.Domains = []string{}
	}

	spec, plans := normalizeCanonicalProposal(req, aiResp)
	// Auto-correct risk level from actual HTTP methods — removes AI responsibility
	// for this dimension and makes it structurally correct by construction:
	//   DELETE present           → "high"
	//   POST/PUT/PATCH present   → "medium"
	//   all GET or no HTTP plans → "low"
	spec.RiskLevel = autoCorrectRiskLevel(plans)

	// Reject proposals where an HTTP plan has an empty or non-http/https URL.
	// This catches the case where the AI omits the URL (req.APIURL was empty and
	// the model didn't generate one), which would produce an unrunnable custom skill.
	if msg := validateHTTPPlanURLs(plans); msg != "" {
		return ForgeProposal{}, fmt.Errorf("forge: %s", msg)
	}

	specBytes, err := json.Marshal(spec)
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: marshal spec: %w", err)
	}
	plansBytes, err := json.Marshal(plans)
	if err != nil {
		return ForgeProposal{}, fmt.Errorf("forge: marshal plans: %w", err)
	}

	actionNames := aiResp.ActionNames
	if len(actionNames) == 0 {
		actionNames = make([]string, 0, len(spec.Actions))
		for _, action := range spec.Actions {
			actionNames = append(actionNames, action.Name)
		}
	}
	if len(aiResp.Domains) == 0 {
		aiResp.Domains = collectDomains(plans)
	}

	return ForgeProposal{
		ID:              id,
		SkillID:         spec.ID,
		Name:            aiResp.Name,
		Description:     aiResp.Description,
		Summary:         aiResp.Summary,
		Rationale:       aiResp.Rationale,
		RequiredSecrets: aiResp.RequiredSecrets,
		Domains:         aiResp.Domains,
		ActionNames:     actionNames,
		RiskLevel:       spec.RiskLevel,
		Status:          "pending",
		SpecJSON:        string(specBytes),
		PlansJSON:       string(plansBytes),
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func normalizeCanonicalProposal(req ProposeRequest, aiResp aiProposalResponse) (ForgeSkillSpec, []ForgeActionPlan) {
	skillID := slugify(req.Name)

	spec := ForgeSkillSpec{
		ID:          skillID,
		Name:        aiResp.Name,
		Description: aiResp.Description,
		Category:    "utility",
		RiskLevel:   normalizeRiskLevel(aiResp.RiskLevel),
		Tags:        []string{},
		Actions:     []ForgeActionSpec{},
	}
	if aiResp.Spec != nil {
		spec = *aiResp.Spec
	}
	if spec.ID == "" {
		spec.ID = skillID
	}
	if spec.Name == "" {
		spec.Name = aiResp.Name
	}
	if spec.Description == "" {
		spec.Description = aiResp.Description
	}
	if spec.Category == "" {
		spec.Category = "utility"
	}
	spec.RiskLevel = normalizeRiskLevel(spec.RiskLevel)
	if spec.Tags == nil {
		spec.Tags = []string{}
	}

	plans := aiResp.Plans

	actionNames := aiResp.ActionNames
	if len(actionNames) == 0 && len(spec.Actions) > 0 {
		for _, action := range spec.Actions {
			actionNames = append(actionNames, action.Name)
		}
	}
	if len(spec.Actions) == 0 {
		if len(actionNames) == 0 {
			actionNames = []string{"Query"}
			logstore.Write("warn", "forge: AI returned no actions and no actionNames — synthesizing default 'Query' action; proposal may need manual review",
				map[string]string{"skill": req.Name})
		}
		for _, name := range actionNames {
			actionID := slugify(name)
			spec.Actions = append(spec.Actions, ForgeActionSpec{
				ID:              actionID,
				Name:            name,
				Description:     fmt.Sprintf("%s action for %s", name, spec.Name),
				PermissionLevel: "read",
			})
		}
	}

	if len(plans) == 0 {
		plans = make([]ForgeActionPlan, 0, len(spec.Actions))
		for _, action := range spec.Actions {
			plans = append(plans, ForgeActionPlan{
				ActionID: action.ID,
				Type:     "http",
				HTTPRequest: &HTTPRequestPlan{
					Method:   "GET",
					URL:      req.APIURL,
					AuthType: "none",
				},
			})
		}
	}

	for i := range spec.Actions {
		if spec.Actions[i].ID == "" {
			spec.Actions[i].ID = slugify(spec.Actions[i].Name)
		}
		if spec.Actions[i].Name == "" {
			spec.Actions[i].Name = humanizeSlug(spec.Actions[i].ID)
		}
		if spec.Actions[i].Description == "" {
			spec.Actions[i].Description = fmt.Sprintf("%s action for %s", spec.Actions[i].Name, spec.Name)
		}
		if spec.Actions[i].PermissionLevel == "" {
			spec.Actions[i].PermissionLevel = "read"
		}
	}

	for i := range plans {
		if plans[i].ActionID == "" && i < len(spec.Actions) {
			plans[i].ActionID = spec.Actions[i].ID
		}
		if plans[i].Type == "" {
			plans[i].Type = "http"
		}
		if plans[i].HTTPRequest != nil {
			if plans[i].HTTPRequest.Method == "" {
				plans[i].HTTPRequest.Method = "GET"
			}
			if plans[i].HTTPRequest.AuthType == "" {
				plans[i].HTTPRequest.AuthType = "none"
			}
			if plans[i].HTTPRequest.URL == "" {
				plans[i].HTTPRequest.URL = req.APIURL
			}
		}
	}

	return spec, plans
}

// autoCorrectRiskLevel derives riskLevel from the HTTP methods present in plans,
// making risk assessment code-derived rather than AI-decided.
func autoCorrectRiskLevel(plans []ForgeActionPlan) string {
	hasDelete, hasMutation, hasHTTP := false, false, false
	for _, plan := range plans {
		if plan.HTTPRequest == nil {
			continue
		}
		hasHTTP = true
		switch strings.ToUpper(plan.HTTPRequest.Method) {
		case "DELETE":
			hasDelete = true
		case "POST", "PUT", "PATCH":
			hasMutation = true
		}
	}
	if !hasHTTP {
		return "low" // local or workflow skills — no HTTP operations
	}
	if hasDelete {
		return "high"
	}
	if hasMutation {
		return "medium"
	}
	return "low"
}

func collectDomains(plans []ForgeActionPlan) []string {
	seen := map[string]bool{}
	domains := []string{}
	for _, plan := range plans {
		if plan.HTTPRequest == nil || strings.TrimSpace(plan.HTTPRequest.URL) == "" {
			continue
		}
		u, err := url.Parse(plan.HTTPRequest.URL)
		if err != nil || u.Host == "" || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		domains = append(domains, u.Host)
	}
	return domains
}

// validateHTTPPlanURLs returns a non-empty error string if any HTTP plan has an
// empty URL or a URL with a scheme other than http/https. Local and workflow
// plans are skipped — they do not have HTTP requests.
func validateHTTPPlanURLs(plans []ForgeActionPlan) string {
	for _, plan := range plans {
		h := plan.HTTPRequest
		if h == nil {
			continue
		}
		if strings.TrimSpace(h.URL) == "" {
			return fmt.Sprintf("plan %q has an empty URL — the AI must return a real HTTPS endpoint", plan.ActionID)
		}
		u, err := url.Parse(h.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return fmt.Sprintf("plan %q URL %q is not a valid http/https URL — the AI must return a real HTTPS endpoint", plan.ActionID, h.URL)
		}
	}
	return ""
}

func humanizeSlug(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}
