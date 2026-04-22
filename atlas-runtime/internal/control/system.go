package control

import (
	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/storage"
	"fmt"
	"net"
	"net/url"
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

	var err error
	next, err = validateRuntimeConfigUpdate(prev, next)
	if err != nil {
		return config.RuntimeConfigSnapshot{}, false, err
	}

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

func validateRuntimeConfigUpdate(prev, next config.RuntimeConfigSnapshot) (config.RuntimeConfigSnapshot, error) {
	var err error

	next.AutonomyMode = config.NormalizeAutonomyMode(next.AutonomyMode)

	next.LMStudioBaseURL, err = normalizeLocalBaseURL(next.LMStudioBaseURL, "http://localhost:1234", "lmStudioBaseURL")
	if err != nil {
		return config.RuntimeConfigSnapshot{}, err
	}
	next.OllamaBaseURL, err = normalizeLocalBaseURL(next.OllamaBaseURL, "http://localhost:11434", "ollamaBaseURL")
	if err != nil {
		return config.RuntimeConfigSnapshot{}, err
	}

	if next.TelegramWebhookSecret != prev.TelegramWebhookSecret {
		return config.RuntimeConfigSnapshot{}, fmt.Errorf("telegramWebhookSecret cannot be changed via config.json; keep secrets in Keychain-backed credentials")
	}

	if next.DiscordClientID != "" {
		trimmed := strings.TrimSpace(next.DiscordClientID)
		if trimmed == "" {
			next.DiscordClientID = ""
		} else if !isDigitsOnly(trimmed) || len(trimmed) > 32 {
			return config.RuntimeConfigSnapshot{}, fmt.Errorf("discordClientID must be a numeric Discord application ID")
		} else {
			next.DiscordClientID = trimmed
		}
	}

	for _, candidate := range []struct {
		name  string
		value int
	}{
		{"runtimePort", next.RuntimePort},
		{"atlasEnginePort", next.AtlasEnginePort},
		{"atlasEngineRouterPort", next.AtlasEngineRouterPort},
		{"atlasMLXPort", next.AtlasMLXPort},
		{"atlasMLXRouterPort", next.AtlasMLXRouterPort},
		{"voiceWhisperPort", next.VoiceWhisperPort},
		{"voiceKokoroPort", next.VoiceKokoroPort},
	} {
		if err := validatePort(candidate.name, candidate.value); err != nil {
			return config.RuntimeConfigSnapshot{}, err
		}
	}

	return next, nil
}

func normalizeLocalBaseURL(raw, fallback, field string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%s must be a valid URL: %w", field, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s must use http or https", field)
	}
	if u.User != nil {
		return "", fmt.Errorf("%s must not embed credentials in the URL", field)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("%s must include a host", field)
	}
	if !isLoopbackHost(host) {
		return "", fmt.Errorf("%s must point to a local loopback host", field)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%s must not include a path (do not add /v1 — it is appended automatically)", field)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must not include query strings or fragments", field)
	}
	if port := u.Port(); port == "" {
		return "", fmt.Errorf("%s must include an explicit port", field)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))
	switch lower {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(lower)
	return ip != nil && ip.IsLoopback()
}

func validatePort(name string, value int) error {
	if value < 1024 || value > 65535 {
		return fmt.Errorf("%s must be between 1024 and 65535 (privileged ports 1–1023 cannot be bound by a user-level launchd agent)", name)
	}
	return nil
}

func isDigitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
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
