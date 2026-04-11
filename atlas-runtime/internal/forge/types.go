// Package forge implements the Forge proposal lifecycle — AI-driven skill
// research, proposal persistence, install, and uninstall.
package forge

import (
	"context"

	"atlas-runtime-go/internal/forge/forgetypes"
)

// AIProvider is the interface forge.Service uses to make non-streaming AI
// calls. Using an interface instead of agent.ProviderConfig breaks the
// agent → skills → forge → agent import cycle; the domain layer provides a
// concrete adapter that wraps the real provider.
type AIProvider interface {
	CallNonStreaming(ctx context.Context, system, user string) (string, error)
}

// ForgeProposal matches contracts.ts ForgeProposalRecord exactly.
type ForgeProposal struct {
	ID              string   `json:"id"`
	SkillID         string   `json:"skillID"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Summary         string   `json:"summary"`
	Rationale       string   `json:"rationale,omitempty"`
	RequiredSecrets []string `json:"requiredSecrets"`
	Domains         []string `json:"domains"`
	ActionNames     []string `json:"actionNames"`
	RiskLevel       string   `json:"riskLevel"`
	Status          string   `json:"status"` // pending | installed | enabled | rejected | uninstalled
	SpecJSON        string   `json:"specJSON"`
	PlansJSON       string   `json:"plansJSON"`
	ContractJSON    string   `json:"contractJSON,omitempty"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

// ResearchingItem matches contracts.ts ForgeResearchingItem exactly.
type ResearchingItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	StartedAt string `json:"startedAt"`
}

// ProposeRequest is the body of POST /forge/proposals.
type ProposeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	APIURL      string `json:"apiURL"`
}

// Type aliases — canonical definitions live in forgetypes.
// Using aliases (= syntax) means forge.ForgeSkillSpec and forgetypes.ForgeSkillSpec
// are the same type, so all existing callers in this package compile unchanged.
type (
	ForgeSkillSpec      = forgetypes.ForgeSkillSpec
	ForgeActionSpec     = forgetypes.ForgeActionSpec
	ForgeActionPlan     = forgetypes.ForgeActionPlan
	WorkflowStepPlan    = forgetypes.WorkflowStepPlan
	LocalPlan           = forgetypes.LocalPlan
	HTTPRequestPlan     = forgetypes.HTTPRequestPlan
	APIResearchContract = forgetypes.APIResearchContract
)
