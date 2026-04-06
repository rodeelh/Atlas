package control

import (
	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/storage"
)

type SystemService struct {
	cfgStore   *config.Store
	runtimeSvc *runtime.Service
	db         *storage.DB
}

func NewSystemService(cfgStore *config.Store, runtimeSvc *runtime.Service, db *storage.DB) *SystemService {
	return &SystemService{cfgStore: cfgStore, runtimeSvc: runtimeSvc, db: db}
}

func (s *SystemService) Status() runtime.Status {
	convCount := s.db.CountConversations()
	tokensIn, tokensOut := agent.GetSessionTokens()
	status := s.runtimeSvc.GetStatus(convCount, tokensIn, tokensOut)
	status.PendingApprovalCount = s.db.CountPendingApprovals()
	return status
}

func (s *SystemService) Logs(limit int) []logstore.Entry {
	entries := logstore.Global().Entries(limit)
	if entries == nil {
		return []logstore.Entry{}
	}
	return entries
}

func (s *SystemService) Config() config.RuntimeConfigSnapshot {
	return s.cfgStore.Load()
}

func (s *SystemService) UpdateConfig(next config.RuntimeConfigSnapshot) (config.RuntimeConfigSnapshot, bool, error) {
	prev := s.cfgStore.Load()

	if next.MaxParallelAgents < 2 {
		next.MaxParallelAgents = 2
	}
	if next.MaxParallelAgents > 5 {
		next.MaxParallelAgents = 5
	}
	if next.WorkerMaxIterations < 1 {
		next.WorkerMaxIterations = 1
	}
	if next.WorkerMaxIterations > 10 {
		next.WorkerMaxIterations = 10
	}

	restartRequired := next.RuntimePort != prev.RuntimePort
	if err := s.cfgStore.Save(next); err != nil {
		return config.RuntimeConfigSnapshot{}, false, err
	}
	return next, restartRequired, nil
}

func (s *SystemService) OnboardingCompleted() bool {
	return s.cfgStore.Load().OnboardingCompleted
}

func (s *SystemService) UpdateOnboarding(completed bool) error {
	snap := s.cfgStore.Load()
	snap.OnboardingCompleted = completed
	return s.cfgStore.Save(snap)
}
