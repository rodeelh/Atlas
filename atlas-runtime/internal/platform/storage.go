package platform

import (
	"time"

	"atlas-runtime-go/internal/storage"
)

// Storage is the private storage contract exposed to internal modules.
// The first tranche scopes this surface around the next planned extractions.
type Storage interface {
	Approvals() ApprovalStore
	Automations() AutomationStore
	Communications() CommunicationsStore
	Agents() AgentStore
	Workflows() WorkflowStore
	Memories() MemoryStore
}

type ApprovalStore interface {
	ListAllApprovals(limit int) ([]storage.DeferredExecRow, error)
	FetchDeferredByToolCallID(toolCallID string) (*storage.DeferredExecRow, error)
	UpdateDeferredStatus(toolCallID, status, updatedAt string) error
	// SaveApproval inserts a new deferred_executions row. Used by the
	// mind-thoughts subsystem to create thought-sourced approvals.
	SaveApproval(row storage.DeferredExecRow) error
	// SetApprovalError stores a last_error string on an existing row.
	SetApprovalError(toolCallID, errText, updatedAt string) error
}

type AutomationStore interface {
	ListAutomations() ([]storage.AutomationRow, error)
	GetAutomation(id string) (*storage.AutomationRow, error)
	SaveAutomation(row storage.AutomationRow) error
	DeleteAutomation(id string) (bool, error)
	ListGremlinRuns(gremlinID string, limit int) ([]storage.GremlinRunRow, error)
	SaveGremlinRun(row storage.GremlinRunRow) error
	UpdateGremlinRun(runID, status string, output *string, finishedAt float64) error
	CompleteGremlinRun(runID, status string, output, errorMessage *string, finishedAt float64, deliveryStatus string, deliveryError *string, durationMs int64, artifactsJSON *string) error
	UpdateGremlinRunWorkflowRunID(runID, workflowRunID string) error
}

type CommunicationsStore interface {
	ListTelegramSessions() ([]storage.TelegramSessionRow, error)
	ListCommunicationChannels(platform string) ([]storage.CommSessionRow, error)
	FetchCommSession(platform, channelID, threadID string) (*storage.CommSessionRow, error)
}

type AgentStore interface {
	ListAgentDefinitions() ([]storage.AgentDefinitionRow, error)
	ListEnabledAgentDefinitions() ([]storage.AgentDefinitionRow, error)
	GetAgentDefinition(id string) (*storage.AgentDefinitionRow, error)
	SaveAgentDefinition(row storage.AgentDefinitionRow) error
	DeleteAgentDefinition(id string) (bool, error)
	ListAgentRuntime() ([]storage.AgentRuntimeRow, error)
	GetAgentRuntime(agentID string) (*storage.AgentRuntimeRow, error)
	SaveAgentRuntime(row storage.AgentRuntimeRow) error
	DeleteAgentRuntime(agentID string) (bool, error)
	ListAgentTasks(limit int) ([]storage.AgentTaskRow, error)
	GetAgentTask(taskID string) (*storage.AgentTaskRow, error)
	SaveAgentTask(row storage.AgentTaskRow) error
	AddAgentTaskIterations(taskID string, count int) error
	FetchDeferredsByAgentTaskID(taskID string, status string) ([]storage.DeferredExecRow, error)
	ListAgentTaskSteps(taskID string) ([]storage.AgentTaskStepRow, error)
	SaveAgentTaskStep(row storage.AgentTaskStepRow) error
	ListAgentEvents(limit int) ([]storage.AgentEventRow, error)
	SaveAgentEvent(row storage.AgentEventRow) error
	ClearAgentEvents() error
	GetAgentMetrics(agentID string) (*storage.AgentMetricsRow, error)
	UpsertAgentMetrics(row storage.AgentMetricsRow) error
	ListAgentMetrics() ([]storage.AgentMetricsRow, error)
	SaveTriggerEvent(row storage.TriggerEventRow) error
	ListTriggerEvents(limit int) ([]storage.TriggerEventRow, error)
	SaveTriggerCooldown(row storage.TriggerCooldownRow) error
	IsOnCooldown(triggerType, agentID string, window time.Duration) (bool, error)
	TryAcquireTriggerCooldown(cooldownID, triggerType, agentID string, window time.Duration) (bool, error)
}

type WorkflowStore interface {
	ListWorkflows() ([]storage.WorkflowRow, error)
	GetWorkflow(id string) (*storage.WorkflowRow, error)
	SaveWorkflow(row storage.WorkflowRow) error
	DeleteWorkflow(id string) (bool, error)
	ListWorkflowRuns(workflowID string, limit int) ([]storage.WorkflowRunRow, error)
	GetWorkflowRun(runID string) (*storage.WorkflowRunRow, error)
	SaveWorkflowRun(row storage.WorkflowRunRow) error
	CompleteWorkflowRun(runID, status string, outcome, assistantSummary, errorMessage, finishedAt *string, durationMs int64, artifactsJSON *string) error
	UpdateWorkflowRunStepRuns(runID, stepRunsJSON string) error
	UpdateWorkflowRunStatus(runID, status string) (*storage.WorkflowRunRow, error)
}

type MemoryStore interface {
	SaveMemory(row storage.MemoryRow) error
}

// SQLiteStorage adapts the current runtime DB to the private platform storage contracts.
type SQLiteStorage struct {
	db *storage.DB
}

func NewSQLiteStorage(db *storage.DB) *SQLiteStorage {
	return &SQLiteStorage{db: db}
}

func (s *SQLiteStorage) Approvals() ApprovalStore { return approvalStore{s.db} }

func (s *SQLiteStorage) Automations() AutomationStore { return automationStore{s.db} }

func (s *SQLiteStorage) Communications() CommunicationsStore { return communicationsStore{s.db} }

func (s *SQLiteStorage) Agents() AgentStore { return agentStore{s.db} }

func (s *SQLiteStorage) Workflows() WorkflowStore { return workflowStore{s.db} }

func (s *SQLiteStorage) Memories() MemoryStore { return memoryStore{s.db} }

type approvalStore struct {
	db *storage.DB
}

func (s approvalStore) ListAllApprovals(limit int) ([]storage.DeferredExecRow, error) {
	return s.db.ListAllApprovals(limit)
}

func (s approvalStore) FetchDeferredByToolCallID(toolCallID string) (*storage.DeferredExecRow, error) {
	return s.db.FetchDeferredByToolCallID(toolCallID)
}

func (s approvalStore) UpdateDeferredStatus(toolCallID, status, updatedAt string) error {
	return s.db.UpdateDeferredStatus(toolCallID, status, updatedAt)
}

func (s approvalStore) SaveApproval(row storage.DeferredExecRow) error {
	return s.db.SaveDeferredExecution(row)
}

func (s approvalStore) SetApprovalError(toolCallID, errText, updatedAt string) error {
	return s.db.SetDeferredLastError(toolCallID, errText, updatedAt)
}

type automationStore struct {
	db *storage.DB
}

func (s automationStore) ListAutomations() ([]storage.AutomationRow, error) {
	return s.db.ListAutomations()
}

func (s automationStore) GetAutomation(id string) (*storage.AutomationRow, error) {
	return s.db.GetAutomation(id)
}

func (s automationStore) SaveAutomation(row storage.AutomationRow) error {
	return s.db.SaveAutomation(row)
}

func (s automationStore) DeleteAutomation(id string) (bool, error) {
	return s.db.DeleteAutomation(id)
}

func (s automationStore) ListGremlinRuns(gremlinID string, limit int) ([]storage.GremlinRunRow, error) {
	return s.db.ListGremlinRuns(gremlinID, limit)
}

func (s automationStore) SaveGremlinRun(row storage.GremlinRunRow) error {
	return s.db.SaveGremlinRun(row)
}

func (s automationStore) UpdateGremlinRun(runID, status string, output *string, finishedAt float64) error {
	return s.db.UpdateGremlinRun(runID, status, output, finishedAt)
}

func (s automationStore) CompleteGremlinRun(runID, status string, output, errorMessage *string, finishedAt float64, deliveryStatus string, deliveryError *string, durationMs int64, artifactsJSON *string) error {
	return s.db.CompleteGremlinRun(runID, status, output, errorMessage, finishedAt, deliveryStatus, deliveryError, durationMs, artifactsJSON)
}

func (s automationStore) UpdateGremlinRunWorkflowRunID(runID, workflowRunID string) error {
	return s.db.UpdateGremlinRunWorkflowRunID(runID, workflowRunID)
}

type communicationsStore struct {
	db *storage.DB
}

func (s communicationsStore) ListTelegramSessions() ([]storage.TelegramSessionRow, error) {
	return s.db.ListTelegramSessions()
}

func (s communicationsStore) ListCommunicationChannels(platform string) ([]storage.CommSessionRow, error) {
	return s.db.ListCommunicationChannels(platform)
}

func (s communicationsStore) FetchCommSession(platform, channelID, threadID string) (*storage.CommSessionRow, error) {
	return s.db.FetchCommSession(platform, channelID, threadID)
}

type agentStore struct {
	db *storage.DB
}

func (s agentStore) ListAgentDefinitions() ([]storage.AgentDefinitionRow, error) {
	return s.db.ListAgentDefinitions()
}

func (s agentStore) ListEnabledAgentDefinitions() ([]storage.AgentDefinitionRow, error) {
	return s.db.ListEnabledAgentDefinitions()
}

func (s agentStore) GetAgentDefinition(id string) (*storage.AgentDefinitionRow, error) {
	return s.db.GetAgentDefinition(id)
}

func (s agentStore) SaveAgentDefinition(row storage.AgentDefinitionRow) error {
	return s.db.SaveAgentDefinition(row)
}

func (s agentStore) DeleteAgentDefinition(id string) (bool, error) {
	return s.db.DeleteAgentDefinition(id)
}

func (s agentStore) ListAgentRuntime() ([]storage.AgentRuntimeRow, error) {
	return s.db.ListAgentRuntime()
}

func (s agentStore) GetAgentRuntime(agentID string) (*storage.AgentRuntimeRow, error) {
	return s.db.GetAgentRuntime(agentID)
}

func (s agentStore) SaveAgentRuntime(row storage.AgentRuntimeRow) error {
	return s.db.SaveAgentRuntime(row)
}

func (s agentStore) DeleteAgentRuntime(agentID string) (bool, error) {
	return s.db.DeleteAgentRuntime(agentID)
}

func (s agentStore) ListAgentTasks(limit int) ([]storage.AgentTaskRow, error) {
	return s.db.ListAgentTasks(limit)
}

func (s agentStore) GetAgentTask(taskID string) (*storage.AgentTaskRow, error) {
	return s.db.GetAgentTask(taskID)
}

func (s agentStore) SaveAgentTask(row storage.AgentTaskRow) error {
	return s.db.SaveAgentTask(row)
}

func (s agentStore) AddAgentTaskIterations(taskID string, count int) error {
	return s.db.AddAgentTaskIterations(taskID, count)
}

func (s agentStore) FetchDeferredsByAgentTaskID(taskID, status string) ([]storage.DeferredExecRow, error) {
	return s.db.FetchDeferredsByAgentTaskID(taskID, status)
}

func (s agentStore) ListAgentTaskSteps(taskID string) ([]storage.AgentTaskStepRow, error) {
	return s.db.ListAgentTaskSteps(taskID)
}

func (s agentStore) SaveAgentTaskStep(row storage.AgentTaskStepRow) error {
	return s.db.SaveAgentTaskStep(row)
}

func (s agentStore) ListAgentEvents(limit int) ([]storage.AgentEventRow, error) {
	return s.db.ListAgentEvents(limit)
}

func (s agentStore) SaveAgentEvent(row storage.AgentEventRow) error {
	return s.db.SaveAgentEvent(row)
}

func (s agentStore) ClearAgentEvents() error {
	return s.db.ClearAgentEvents()
}

func (s agentStore) GetAgentMetrics(agentID string) (*storage.AgentMetricsRow, error) {
	return s.db.GetAgentMetrics(agentID)
}

func (s agentStore) UpsertAgentMetrics(row storage.AgentMetricsRow) error {
	return s.db.UpsertAgentMetrics(row)
}

func (s agentStore) ListAgentMetrics() ([]storage.AgentMetricsRow, error) {
	return s.db.ListAgentMetrics()
}

func (s agentStore) SaveTriggerEvent(row storage.TriggerEventRow) error {
	return s.db.SaveTriggerEvent(row)
}

func (s agentStore) ListTriggerEvents(limit int) ([]storage.TriggerEventRow, error) {
	return s.db.ListTriggerEvents(limit)
}

func (s agentStore) SaveTriggerCooldown(row storage.TriggerCooldownRow) error {
	return s.db.SaveTriggerCooldown(row)
}

func (s agentStore) IsOnCooldown(triggerType, agentID string, window time.Duration) (bool, error) {
	return s.db.IsOnCooldown(triggerType, agentID, window)
}

func (s agentStore) TryAcquireTriggerCooldown(cooldownID, triggerType, agentID string, window time.Duration) (bool, error) {
	return s.db.TryAcquireTriggerCooldown(cooldownID, triggerType, agentID, window)
}

type workflowStore struct {
	db *storage.DB
}

func (s workflowStore) ListWorkflows() ([]storage.WorkflowRow, error) {
	return s.db.ListWorkflows()
}

func (s workflowStore) GetWorkflow(id string) (*storage.WorkflowRow, error) {
	return s.db.GetWorkflow(id)
}

func (s workflowStore) SaveWorkflow(row storage.WorkflowRow) error {
	return s.db.SaveWorkflow(row)
}

func (s workflowStore) DeleteWorkflow(id string) (bool, error) {
	return s.db.DeleteWorkflow(id)
}

func (s workflowStore) ListWorkflowRuns(workflowID string, limit int) ([]storage.WorkflowRunRow, error) {
	return s.db.ListWorkflowRuns(workflowID, limit)
}

func (s workflowStore) GetWorkflowRun(runID string) (*storage.WorkflowRunRow, error) {
	return s.db.GetWorkflowRun(runID)
}

func (s workflowStore) SaveWorkflowRun(row storage.WorkflowRunRow) error {
	return s.db.SaveWorkflowRun(row)
}

func (s workflowStore) CompleteWorkflowRun(runID, status string, outcome, assistantSummary, errorMessage, finishedAt *string, durationMs int64, artifactsJSON *string) error {
	return s.db.CompleteWorkflowRun(runID, status, outcome, assistantSummary, errorMessage, finishedAt, durationMs, artifactsJSON)
}

func (s workflowStore) UpdateWorkflowRunStepRuns(runID, stepRunsJSON string) error {
	return s.db.UpdateWorkflowRunStepRuns(runID, stepRunsJSON)
}

func (s workflowStore) UpdateWorkflowRunStatus(runID, status string) (*storage.WorkflowRunRow, error) {
	return s.db.UpdateWorkflowRunStatus(runID, status)
}

type memoryStore struct {
	db *storage.DB
}

func (s memoryStore) SaveMemory(row storage.MemoryRow) error {
	return s.db.SaveMemory(row)
}
