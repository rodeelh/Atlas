package forge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubAIProvider struct {
	response string
	err      error
}

func (s stubAIProvider) CallNonStreaming(context.Context, string, string) (string, error) {
	return s.response, s.err
}

func TestParseProposalResponse_CanonicalPayloadProducesCanonicalSpecAndPlans(t *testing.T) {
	req := ProposeRequest{
		Name:        "Weather Helper",
		Description: "Helpful weather queries",
		APIURL:      "https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
	}
	raw := `{
		"name":"Weather Helper",
		"description":"Helpful weather queries",
		"summary":"Forecast helper",
		"rationale":"Useful for weather lookups",
		"requiredSecrets":[],
		"domains":["api.weather.gov"],
		"actionNames":["Forecast"],
		"riskLevel":"low",
		"spec":{
			"id":"weather-helper",
			"name":"Weather Helper",
			"description":"Helpful weather queries",
			"category":"utility",
			"riskLevel":"low",
			"tags":["weather"],
			"actions":[
				{
					"id":"forecast",
					"name":"Forecast",
					"description":"Get the forecast",
					"permissionLevel":"read"
				}
			]
		},
		"plans":[
			{
				"actionID":"forecast",
				"type":"http",
				"httpRequest":{
					"method":"GET",
					"url":"https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
					"authType":"none"
				}
			}
		]
	}`

	proposal, err := parseProposalResponse("prop-1", req, raw)
	if err != nil {
		t.Fatalf("parseProposalResponse: %v", err)
	}

	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(proposal.SpecJSON), &spec); err != nil {
		t.Fatalf("decode SpecJSON: %v", err)
	}
	if spec.ID != "weather-helper" {
		t.Fatalf("expected canonical skill id, got %q", spec.ID)
	}
	if len(spec.Actions) != 1 || spec.Actions[0].ID != "forecast" {
		t.Fatalf("expected canonical action spec, got %+v", spec.Actions)
	}

	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(proposal.PlansJSON), &plans); err != nil {
		t.Fatalf("decode PlansJSON: %v", err)
	}
	if len(plans) != 1 || plans[0].HTTPRequest == nil {
		t.Fatalf("expected canonical http plan, got %+v", plans)
	}
	if plans[0].HTTPRequest.URL != req.APIURL {
		t.Fatalf("expected plan URL %q, got %q", req.APIURL, plans[0].HTTPRequest.URL)
	}
}

func TestParseProposalResponse_LegacyPayloadBackfillsCanonicalSpecAndPlans(t *testing.T) {
	req := ProposeRequest{
		Name:        "Weather Helper",
		Description: "Helpful weather queries",
		APIURL:      "https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
	}
	raw := `{
		"name":"Weather Helper",
		"description":"Helpful weather queries",
		"summary":"Forecast helper",
		"rationale":"Useful for weather lookups",
		"requiredSecrets":[],
		"domains":["api.weather.gov"],
		"actionNames":["Forecast","Alerts"],
		"riskLevel":"low"
	}`

	proposal, err := parseProposalResponse("prop-1", req, raw)
	if err != nil {
		t.Fatalf("parseProposalResponse: %v", err)
	}

	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(proposal.SpecJSON), &spec); err != nil {
		t.Fatalf("decode SpecJSON: %v", err)
	}
	if spec.ID != "weather-helper" {
		t.Fatalf("expected synthesized skill id, got %q", spec.ID)
	}
	if len(spec.Actions) != 2 {
		t.Fatalf("expected synthesized actions, got %+v", spec.Actions)
	}
	if spec.Actions[0].ID != "forecast" || spec.Actions[1].ID != "alerts" {
		t.Fatalf("expected slugified action ids, got %+v", spec.Actions)
	}

	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(proposal.PlansJSON), &plans); err != nil {
		t.Fatalf("decode PlansJSON: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected synthesized plans, got %+v", plans)
	}
	for _, plan := range plans {
		if plan.Type != "http" || plan.HTTPRequest == nil {
			t.Fatalf("expected canonical http plan, got %+v", plan)
		}
		if plan.HTTPRequest.URL != req.APIURL {
			t.Fatalf("expected plan URL %q, got %q", req.APIURL, plan.HTTPRequest.URL)
		}
	}
}

func TestServicePropose_PersistsCanonicalProposalShape(t *testing.T) {
	supportDir := t.TempDir()
	service := NewService(supportDir)
	req := ProposeRequest{
		Name:        "Weather Helper",
		Description: "Helpful weather queries",
		APIURL:      "https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
	}
	provider := stubAIProvider{
		response: `{
			"name":"Weather Helper",
			"description":"Helpful weather queries",
			"summary":"Forecast helper",
			"rationale":"Useful for weather lookups",
			"requiredSecrets":[],
			"domains":["api.weather.gov"],
			"actionNames":["Forecast"],
			"riskLevel":"low",
			"spec":{
				"id":"weather-helper",
				"name":"Weather Helper",
				"description":"Helpful weather queries",
				"category":"utility",
				"riskLevel":"low",
				"tags":["weather"],
				"actions":[
					{
						"id":"forecast",
						"name":"Forecast",
						"description":"Get the forecast",
						"permissionLevel":"read"
					}
				]
			},
			"plans":[
				{
					"actionID":"forecast",
					"type":"http",
					"httpRequest":{
						"method":"GET",
						"url":"https://api.weather.gov/gridpoints/{office}/{gridX},{gridY}/forecast",
						"authType":"none"
					}
				}
			]
		}`,
	}

	proposal, err := service.Propose(context.Background(), req, provider)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	stored := GetProposal(supportDir, proposal.ID)
	if stored == nil {
		t.Fatal("expected proposal to be persisted")
	}

	var spec ForgeSkillSpec
	if err := json.Unmarshal([]byte(stored.SpecJSON), &spec); err != nil {
		t.Fatalf("decode stored SpecJSON: %v", err)
	}
	if spec.ID == "" || len(spec.Actions) == 0 {
		t.Fatalf("stored proposal should have canonical spec, got %+v", spec)
	}

	var plans []ForgeActionPlan
	if err := json.Unmarshal([]byte(stored.PlansJSON), &plans); err != nil {
		t.Fatalf("decode stored PlansJSON: %v", err)
	}
	if len(plans) == 0 || plans[0].HTTPRequest == nil {
		t.Fatalf("stored proposal should have canonical plans, got %+v", plans)
	}
}

func TestBuildResearchPromptMentionsBuiltInFileActions(t *testing.T) {
	prompt := buildResearchPrompt(ProposeRequest{
		Name:        "PDF Writer",
		Description: "Create PDF reports",
	})

	for _, expected := range []string{"fs.create_pdf", "fs.create_docx", "fs.create_zip", "fs.save_image"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected prompt to mention %s, got %q", expected, prompt)
		}
	}
}
