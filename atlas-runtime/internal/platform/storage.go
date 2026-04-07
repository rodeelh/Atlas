package platform

import "atlas-runtime-go/internal/storage"

// Storage is the private storage contract exposed to internal modules.
// The first tranche scopes this surface around the next planned extractions.
type Storage interface {
	Approvals() ApprovalStore
	Automations() AutomationStore
	Communications() CommunicationsStore
	Memories() MemoryStore
}

type ApprovalStore interface {
	ListAllApprovals(limit int) ([]storage.DeferredExecRow, error)
	FetchDeferredByToolCallID(toolCallID string) (*storage.DeferredExecRow, error)
	UpdateDeferredStatus(toolCallID, status, updatedAt string) error
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

type memoryStore struct {
	db *storage.DB
}

func (s memoryStore) SaveMemory(row storage.MemoryRow) error {
	return s.db.SaveMemory(row)
}
