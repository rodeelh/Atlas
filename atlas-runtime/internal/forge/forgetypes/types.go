// Package forgetypes contains the shared Forge type definitions. It has no
// imports from within atlas-runtime-go, which lets both internal/forge and
// internal/skills import it without creating an import cycle.
package forgetypes

// PlaceholderDomains is the canonical blocklist of hostnames that indicate a
// fabricated or example API URL. Both the skill-layer validator (forge_skill.go)
// and the forge service reference this single map so they cannot diverge.
var PlaceholderDomains = map[string]bool{
	"example.com":     true,
	"example.org":     true,
	"example.net":     true,
	"localhost":       true,
	"placeholder.com": true,
	"your-api.com":    true,
	"yourdomain.com":  true,
	"sample.com":      true,
	"test.com":        true,
	"fake.com":        true,
	"api.example.com": true,
	"local-skill":     true,
	"local-api.com":   true,
}

// ForgeSkillSpec is the agent-authored specification for a new skill.
type ForgeSkillSpec struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	RiskLevel   string            `json:"riskLevel"`
	Tags        []string          `json:"tags"`
	Actions     []ForgeActionSpec `json:"actions"`
}

// ForgeActionSpec describes one action within a ForgeSkillSpec.
type ForgeActionSpec struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	PermissionLevel string `json:"permissionLevel"`
	// ActionClass overrides the class inferred from the HTTP method.
	// Valid values: read, local_write, destructive_local, external_side_effect, send_publish_delete.
	// Codegen falls back to method-based inference when this is empty.
	ActionClass string `json:"actionClass,omitempty"`
}

// ForgeActionPlan is the execution plan for one action in a Forge skill.
type ForgeActionPlan struct {
	ActionID     string            `json:"actionID"`
	Type         string            `json:"type"`                   // "http" | "local" | "llm.generate" | "atlas.tool" | "return"
	HTTPRequest  *HTTPRequestPlan  `json:"httpRequest,omitempty"`  // set when Type == "http"
	LocalPlan    *LocalPlan        `json:"localPlan,omitempty"`    // set when Type == "local"
	WorkflowStep *WorkflowStepPlan `json:"workflowStep,omitempty"` // set for workflow-backed composed plans
}

// WorkflowStepPlan describes one typed workflow step for a workflow-backed Forge capability.
type WorkflowStepPlan struct {
	Title  string         `json:"title,omitempty"`
	Prompt string         `json:"prompt,omitempty"`
	Action string         `json:"action,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Value  any            `json:"value,omitempty"`
}

// LocalPlan describes how to run a macOS-local script for a ForgeActionPlan.
// Interpreter must be one of: "osascript", "bash", "sh", "python3".
// Script is the inline script body; {param} placeholders are substituted at runtime.
type LocalPlan struct {
	Interpreter string `json:"interpreter"` // "osascript" | "bash" | "sh" | "python3"
	Script      string `json:"script"`      // inline script body, may contain {param} placeholders
}

// HTTPRequestPlan describes how to make an HTTP call for a ForgeActionPlan.
type HTTPRequestPlan struct {
	Method                string            `json:"method"`
	URL                   string            `json:"url"`
	Headers               map[string]string `json:"headers"`
	Query                 map[string]string `json:"query"`
	AuthType              string            `json:"authType"`
	AuthSecretKey         string            `json:"authSecretKey"`
	AuthHeaderName        string            `json:"authHeaderName"`
	AuthQueryParamName    string            `json:"authQueryParamName"`
	OAuth2TokenURL        string            `json:"oauth2TokenURL"`
	OAuth2ClientIDKey     string            `json:"oauth2ClientIDKey"`
	OAuth2ClientSecretKey string            `json:"oauth2ClientSecretKey"`
	OAuth2Scope           string            `json:"oauth2Scope"`
	BodyFields            map[string]string `json:"bodyFields"`
	StaticBodyFields      map[string]string `json:"staticBodyFields"`
	SecretHeader          string            `json:"secretHeader"` // legacy Bearer injection
}

// APIResearchContract captures what the agent learned from researching an API
// before proposing a Forge skill.
type APIResearchContract struct {
	ProviderName           string            `json:"providerName"`
	DocsURL                string            `json:"docsURL"`
	DocsQuality            string            `json:"docsQuality"` // low | medium | high
	BaseURL                string            `json:"baseURL"`
	Endpoint               string            `json:"endpoint"`
	Method                 string            `json:"method"`
	AuthType               string            `json:"authType"`
	RequiredParams         []string          `json:"requiredParams"`
	OptionalParams         []string          `json:"optionalParams"`
	ParamLocations         map[string]string `json:"paramLocations"`
	ExampleRequest         string            `json:"exampleRequest"`
	ExampleResponse        string            `json:"exampleResponse"`
	ExpectedResponseFields []string          `json:"expectedResponseFields"`
	MappingConfidence      string            `json:"mappingConfidence"` // low | medium | high
	ValidationStatus       string            `json:"validationStatus"`
	Notes                  string            `json:"notes"`
}
