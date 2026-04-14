// Package agents defines the canonical Teams V1 types.
//
// These types represent the target architecture. They are defined here in Phase 1
// and wired into the execution path incrementally across subsequent phases.
// Current code still uses AgentDefinitionRow / AgentTaskRow; these types will
// replace them phase by phase without a flag-day rewrite.
package agents

// ── Team member ────────────────────────────────────────────────────────────────

// TeamMember is the canonical V1 team member definition.
// Maps to the agent_definitions table (will be renamed team_members in Phase 8).
//
// New fields added in Phase 1: TemplateRole, PersonaStyle.
// Existing fields: all others, carried from AgentDefinitionRow.
type TeamMember struct {
	ID                   string   // Unique slug ID
	Name                 string   // Display name
	TemplateRole         string   // "scout" | "builder" | "reviewer" | "operator" | "monitor"
	Mission              string   // Clear mission statement
	PersonaStyle         string   // Optional tone / approach guidance (was "style")
	AllowedSkills        []string // Skill namespace patterns e.g. ["fs", "websearch"]
	AllowedToolClasses   []string // e.g. ["read", "local_write"]
	AutonomyMode         string   // "on_demand" | "assistive" | "bounded_autonomous"
	ActivationRules      string   // When to activate (free text for V1)
	ProviderOverride     string   // Optional AI provider override
	ModelOverride        string   // Optional model override
	IsEnabled            bool
	CreatedAt            string
	UpdatedAt            string
}

// ── Delegation plan ────────────────────────────────────────────────────────────

// DelegationPlan is the structured input that Agent sends to Teams via the
// team.delegate skill. Agent constructs the plan; Teams validates and executes it.
// This is the replacement for the old delegateArgs{AgentID, Task, Goal} string input.
type DelegationPlan struct {
	// Mode is the Atlas operating mode that produced this delegation.
	// "solo" | "specialist_assist" | "team_lead"
	Mode string `json:"mode"`

	// Pattern is the delegation pattern.
	// "single" | "sequence" | "parallel"
	// V1 preference order: single > sequence > parallel.
	Pattern string `json:"pattern"`

	// ExecutionMode controls whether the caller waits for the result.
	// "sync_assist"       — result needed for current Atlas turn (caller blocks)
	// "async_assignment"  — task has its own lifecycle (caller gets task ID immediately)
	ExecutionMode string `json:"executionMode"`

	// Tasks is the ordered list of delegated task specs.
	// For "single", exactly one task.
	// For "sequence", tasks in execution order; depends_on is set automatically.
	// For "parallel", all tasks are independent.
	Tasks []DelegationTaskSpec `json:"tasks"`
}

// DelegationTaskSpec is one delegated task as defined by Agent.
// Teams materialises each spec into a persisted DelegationTask row.
type DelegationTaskSpec struct {
	// AgentID is the target team member's ID.
	AgentID string `json:"agentId"`

	// Task is a wire-only alias accepted in simple sequence steps.
	// If Objective is empty and Task is set, teamDelegate() normalizes
	// Task → Objective (and Task → Title when Title is also empty).
	// Do not use Task in new Go code — use Objective.
	Task string `json:"task,omitempty"`

	// Title is a short human-readable label for this task (used in Team HQ).
	Title string `json:"title"`

	// Objective is the one-sentence goal statement for the worker.
	Objective string `json:"objective"`

	// Scope defines what is in/out of bounds for this task.
	Scope DelegationScope `json:"scope"`

	// SuccessCriteria defines what constitutes successful completion.
	SuccessCriteria DelegationSuccessCriteria `json:"successCriteria"`

	// InputContext provides the minimum context the worker needs to operate.
	InputContext DelegationInputContext `json:"inputContext"`

	// ExpectedOutput defines the output format the worker must produce.
	ExpectedOutput DelegationExpectedOutput `json:"expectedOutput"`

	// DependsOn lists task IDs (within the same plan) that must complete first.
	// Populated automatically for "sequence" pattern; leave empty for "single".
	DependsOn []string `json:"dependsOn,omitempty"`

	// RequestedBy tracks the initiator.  "atlas" | "user" | "auto"
	RequestedBy string `json:"requestedBy,omitempty"`
}

// DelegationScope defines the work boundary for a delegated task.
type DelegationScope struct {
	// Included lists work explicitly inside scope.
	Included []string `json:"included,omitempty"`

	// Excluded lists work explicitly outside scope.
	Excluded []string `json:"excluded,omitempty"`

	// Boundaries lists environmental or domain limits.
	Boundaries []string `json:"boundaries,omitempty"`

	// TimeHorizon is the expected temporal scope, e.g. "current turn" or "background".
	TimeHorizon string `json:"timeHorizon,omitempty"`
}

// DelegationSuccessCriteria defines what counts as task completion.
type DelegationSuccessCriteria struct {
	// Must lists required completion conditions.
	Must []string `json:"must,omitempty"`

	// Should lists desirable but non-blocking conditions.
	Should []string `json:"should,omitempty"`

	// FailureConditions lists conditions that mean the task was not successful.
	FailureConditions []string `json:"failureConditions,omitempty"`
}

// DelegationInputContext provides the minimum context needed for specialist execution.
type DelegationInputContext struct {
	// UserRequest is the original user request or a concise summary.
	UserRequest string `json:"userRequest,omitempty"`

	// ConversationExcerpt contains only relevant excerpts from the conversation.
	ConversationExcerpt []string `json:"conversationExcerpt,omitempty"`

	// AtlasTaskFrame explains why Atlas delegated this task.
	AtlasTaskFrame string `json:"atlasTaskFrame,omitempty"`

	// KnownConstraints lists task-specific or environmental constraints.
	KnownConstraints []string `json:"knownConstraints,omitempty"`

	// PriorResults holds delegated outputs from preceding steps in a sequence.
	PriorResults []string `json:"priorResults,omitempty"`

	// Artifacts lists files, paths, IDs, URLs, or references needed by the worker.
	Artifacts []string `json:"artifacts,omitempty"`
}

// DelegationExpectedOutput defines the format the worker must use for its result.
type DelegationExpectedOutput struct {
	// Type is the primary output type.
	// V1 allowed values: "summary" | "findings_list" | "structured_brief" | "artifact_update"
	Type string `json:"type"`

	// FormatNotes holds optional guidance on structure, length, or presentation.
	FormatNotes []string `json:"formatNotes,omitempty"`
}

// ── Persisted task / result ────────────────────────────────────────────────────

// DelegationTask is the persisted record created by Teams from a DelegationTaskSpec.
// Maps to the agent_tasks table (columns added in Phase 1 migration; will be
// renamed delegation_tasks in Phase 8).
//
// Fields carried from AgentTaskRow are marked with [existing].
// Fields added in Phase 1 schema migration are marked with [new].
type DelegationTask struct {
	// [existing] core identity
	TaskID    string  // [existing]
	AgentID   string  // [existing]
	Status    string  // [existing] see lifecycle statuses below
	Goal      string  // [existing] free-text goal (kept for backward compat; Objective preferred)
	RequestedBy string // [existing]

	// [existing] results / errors
	ResultSummary *string // [existing] free-text summary (kept; DelegationTaskResult preferred)
	ErrorMessage  *string // [existing]

	// [existing] timing
	ConversationID *string // [existing]
	StartedAt      string  // [existing]
	FinishedAt     *string // [existing]
	CreatedAt      string  // [existing]
	UpdatedAt      string  // [existing]
	IterationsUsed int     // [existing]

	// [new] structured task payload — written from DelegationTaskSpec
	Title             string // [new] human-readable label
	Objective         string // [new] one-sentence goal
	ScopeJSON         string // [new] JSON-encoded DelegationScope
	SuccessCriteriaJSON string // [new] JSON-encoded DelegationSuccessCriteria
	InputContextJSON  string // [new] JSON-encoded DelegationInputContext
	ExpectedOutputJSON string // [new] JSON-encoded DelegationExpectedOutput

	// [new] orchestration metadata
	Mode      string // [new] "sync_assist" | "async_assignment"
	Pattern   string // [new] "single" | "sequence" | "parallel"
	DependsOnJSON string // [new] JSON-encoded []string of upstream task IDs
	ParentTurnID  *string // [new] conversation turn that initiated delegation

	// [new] blocking metadata — populated when task pauses at an approval gate
	BlockingKind   *string // [new] "approval" | "missing_input" | "tool_failure" | "dependency_wait"
	BlockingDetail *string // [new] human-readable explanation of the block
	ResumeToken    *string // [new] opaque token for task resumption
}

// DelegationTaskStatus defines the V1 canonical task lifecycle statuses.
// These replace the previous ad-hoc status strings on AgentTaskRow.
const (
	// Pre-execution
	TaskStatusCreated  = "created"
	TaskStatusAssigned = "assigned"

	// Execution
	TaskStatusInProgress = "in_progress"
	TaskStatusWaiting    = "waiting"
	TaskStatusBlocked    = "blocked"

	// Terminal
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusCanceled  = "canceled"
)

// DelegationTaskResult is the structured worker output persisted in the
// delegation_task_results table (new in Phase 1) and returned to Agent.
type DelegationTaskResult struct {
	TaskID string

	// OutputType matches DelegationExpectedOutput.Type.
	// "summary" | "findings_list" | "structured_brief" | "artifact_update"
	OutputType string

	// Summary is a short human-readable summary of the result.
	Summary string

	// OutputJSON holds the structured result data (shape varies by OutputType).
	OutputJSON string // JSON blob

	// ArtifactsJSON lists produced files, paths, or resource references.
	ArtifactsJSON string // JSON-encoded []string

	// RisksJSON lists risks or warnings identified during execution.
	RisksJSON string // JSON-encoded []string

	// BlockersJSON lists items that prevented full completion.
	BlockersJSON string // JSON-encoded []string

	// RecommendedNextAction is optional guidance for Atlas on what to do next.
	RecommendedNextAction *string

	CreatedAt string
	UpdatedAt string
}

// TeamMemberStatus defines the V1 canonical runtime statuses for a team member.
// These replace the previous status strings on AgentRuntimeRow.
const (
	MemberStatusIdle        = "idle"
	MemberStatusWorking     = "working"      // was "busy"
	MemberStatusWaiting     = "waiting"
	MemberStatusBlocked     = "blocked"
	MemberStatusNeedsReview = "needs_review" // was "approval_needed"
	MemberStatusDone        = "done"
)

// BlockingKind defines why a delegated task is paused.
const (
	BlockingKindApproval       = "approval"
	BlockingKindMissingInput   = "missing_input"
	BlockingKindToolFailure    = "tool_failure"
	BlockingKindDependencyWait = "dependency_wait"
)
