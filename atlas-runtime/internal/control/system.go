package control

import (
	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/storage"
	"fmt"
	"os"
	"os/exec"
	stdruntime "runtime"
	"strings"
	"sync"
	"time"
)

type SystemService struct {
	cfgStore     *config.Store
	runtimeSvc   *runtime.Service
	db           *storage.DB
	restartMu    sync.Mutex
	restarting   bool
	restartFn    func() error
	restartDelay time.Duration
}

func NewSystemService(cfgStore *config.Store, runtimeSvc *runtime.Service, db *storage.DB) *SystemService {
	return &SystemService{
		cfgStore:     cfgStore,
		runtimeSvc:   runtimeSvc,
		db:           db,
		restartFn:    restartViaLaunchd,
		restartDelay: 500 * time.Millisecond,
	}
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

func (s *SystemService) ScheduleRestart() error {
	s.restartMu.Lock()
	if s.restarting {
		s.restartMu.Unlock()
		return fmt.Errorf("restart already in progress")
	}
	s.restarting = true
	restartFn := s.restartFn
	delay := s.restartDelay
	s.restartMu.Unlock()

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := restartFn(); err != nil {
			logstore.Write("error", "Atlas restart failed: "+err.Error(), nil)
			s.restartMu.Lock()
			s.restarting = false
			s.restartMu.Unlock()
		}
	}()

	return nil
}

func restartViaLaunchd() error {
	if stdruntime.GOOS != "darwin" {
		return fmt.Errorf("launchd restart is only supported on macOS")
	}

	label := "Atlas"
	uid := os.Getuid()
	targets := []string{
		fmt.Sprintf("gui/%d/%s", uid, label),
		fmt.Sprintf("user/%d/%s", uid, label),
		label,
	}

	var failures []string
	for _, target := range targets {
		cmd := exec.Command("launchctl", "kickstart", "-k", target)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		failures = append(failures, fmt.Sprintf("%s: %s", target, msg))
	}

	return fmt.Errorf("launchctl kickstart failed (%s)", strings.Join(failures, "; "))
}
