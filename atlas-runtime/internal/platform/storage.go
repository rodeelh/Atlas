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
	ListGremlinRuns(gremlinID string, limit int) ([]storage.GremlinRunRow, error)
	SaveGremlinRun(row storage.GremlinRunRow) error
	UpdateGremlinRun(runID, status string, output *string, finishedAt float64) error
}

type CommunicationsStore interface {
	ListTelegramSessions() ([]storage.TelegramSessionRow, error)
	ListCommunicationChannels(platform string) ([]storage.CommSessionRow, error)
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

func (s automationStore) ListGremlinRuns(gremlinID string, limit int) ([]storage.GremlinRunRow, error) {
	return s.db.ListGremlinRuns(gremlinID, limit)
}

func (s automationStore) SaveGremlinRun(row storage.GremlinRunRow) error {
	return s.db.SaveGremlinRun(row)
}

func (s automationStore) UpdateGremlinRun(runID, status string, output *string, finishedAt float64) error {
	return s.db.UpdateGremlinRun(runID, status, output, finishedAt)
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

type memoryStore struct {
	db *storage.DB
}

func (s memoryStore) SaveMemory(row storage.MemoryRow) error {
	return s.db.SaveMemory(row)
}
