package mind

// nap_scheduler.go owns the "when does a nap fire" question. Two triggers:
//
//   Idle  — N minutes after the last chat turn, if no new turn has arrived.
//           Chat service calls NotifyTurnNonBlocking after every turn; the
//           scheduler debounces that into a single timer.
//
//   Floor — every M hours regardless of idle activity. A background ticker
//           wakes up periodically and checks lastNapAt against the floor.
//
// Both triggers feed into the same RunNap function. The scheduler itself
// never calls tools, never touches MIND.md directly — it just decides when
// to fire and passes NapDeps through.
//
// State:
//   ~/Library/Application Support/ProjectAtlas/nap-state.json
//   { "last_nap_at": "2026-04-07T15:30:00Z" }
//
// Package-level defaultScheduler + NotifyTurnNonBlocking pattern matches
// how ReflectNonBlocking is wired from chat/service.go: main.go sets the
// default scheduler at startup, chat service calls the package function
// after each turn without needing a reference.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind/telemetry"
	"atlas-runtime-go/internal/storage"
)

// napStateFile persists {lastNapAt} so the floor trigger survives restarts.
const napStateFile = "nap-state.json"

type napState struct {
	LastNapAt time.Time `json:"last_nap_at"`
}

// napFloorCheckInterval is how often the floor ticker wakes to check whether
// a floor-triggered nap is due. Kept short enough that catch-up after a
// restart is responsive, long enough that the ticker itself is negligible.
const napFloorCheckInterval = 5 * time.Minute

// napDedupeWindow protects against running more than one nap in a tight
// window when both idle and floor fire near each other. If a nap just ran,
// the next trigger within this window is dropped.
const napDedupeWindow = 2 * time.Minute

// NapProviderResolver returns a fresh provider for naps. Passed in so config
// changes (provider swaps) are picked up without restarting the scheduler.
type NapProviderResolver func() (agent.ProviderConfig, error)

// NapSkillsLister returns the current list of skill lines for the prompt.
type NapSkillsLister func() []SkillLine

// Scheduler drives idle + floor nap triggers. One per process.
type Scheduler struct {
	supportDir string
	db         *storage.DB
	cfgStore   *config.Store
	telemetry  *telemetry.Emitter
	resolver   NapProviderResolver
	skills     NapSkillsLister
	dispatcher *Dispatcher // optional — set via SetDispatcher before Start

	mu         sync.Mutex
	idleTimer  *time.Timer
	lastNapAt  time.Time
	runningMu  sync.Mutex // serializes concurrent RunNap attempts within this scheduler
	stopCh     chan struct{}
	wg         sync.WaitGroup
	started    bool
}

// SetDispatcher attaches a dispatcher that RunNap will invoke after each
// successful apply. Safe to call before or after Start. Passing nil clears.
func (s *Scheduler) SetDispatcher(d *Dispatcher) {
	s.mu.Lock()
	s.dispatcher = d
	s.mu.Unlock()
}

// NewScheduler constructs a Scheduler. None of the dependencies are started
// until Start is called.
func NewScheduler(supportDir string, db *storage.DB, cfgStore *config.Store, tel *telemetry.Emitter, resolver NapProviderResolver, skills NapSkillsLister) *Scheduler {
	s := &Scheduler{
		supportDir: supportDir,
		db:         db,
		cfgStore:   cfgStore,
		telemetry:  tel,
		resolver:   resolver,
		skills:     skills,
		stopCh:     make(chan struct{}),
	}
	s.lastNapAt = loadNapState(supportDir)
	return s
}

// Start launches the background floor-check goroutine and registers this
// Scheduler as the process-wide default so NotifyTurnNonBlocking can reach it.
// Safe to call multiple times; subsequent calls are no-ops on an already
// started scheduler.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	// Apply runtime config thresholds to the thoughts package vars. This is
	// a one-shot sync at start — changes made later via the config UI take
	// effect on the next daemon restart. A future refinement could refresh
	// on cfgStore changes, but for now a restart is the simple, correct path.
	applyConfigToThoughts(s.cfgStore.Load())

	setDefaultScheduler(s)

	s.wg.Add(1)
	go s.floorLoop()
}

// applyConfigToThoughts copies the nap-related runtime config fields into
// the thoughts package variables that govern auto-execute and discard
// behavior. Called once at scheduler start. Keeps the pure thoughts package
// free of any config package dependency.
func applyConfigToThoughts(cfg config.RuntimeConfigSnapshot) {
	// Lazily import to avoid cycles.
	applyConfigToThoughtsImpl(cfg)
}

// Stop cancels the floor ticker, clears the idle timer, and unregisters the
// default scheduler. Blocks until the floor goroutine exits or ctx expires.
func (s *Scheduler) Stop(ctx context.Context) {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.mu.Unlock()

	close(s.stopCh)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	clearDefaultScheduler(s)
}

// NotifyTurn resets the idle timer. Called by chat service after every
// completed turn. Safe to call with the scheduler stopped (no-op).
// Gated on both ThoughtsEnabled (master) and NapsEnabled (sub-flag).
func (s *Scheduler) NotifyTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return
	}
	cfg := s.cfgStore.Load()
	if !cfg.ThoughtsEnabled || !cfg.NapsEnabled {
		return
	}
	idle := cfg.NapIdleMinutes
	if idle <= 0 {
		idle = 60
	}

	// Cancel the pending idle timer if any, then start a new one.
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(time.Duration(idle)*time.Minute, func() {
		s.fireNap(TriggerIdle)
	})
}

// floorLoop is the background goroutine that enforces the soft daily floor.
// Wakes on napFloorCheckInterval, checks whether a floor-triggered nap is
// due, fires if so. Handles catch-up: if the daemon starts and the last nap
// was more than floor hours ago, a nap fires after a short warmup delay.
func (s *Scheduler) floorLoop() {
	defer s.wg.Done()

	// Short warmup before the first floor check — gives the daemon time to
	// finish initialization before a nap fires from a restart.
	select {
	case <-s.stopCh:
		return
	case <-time.After(30 * time.Second):
	}

	// Initial check: if the last nap is older than the floor, fire immediately.
	s.maybeFireFloor()

	ticker := time.NewTicker(napFloorCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.maybeFireFloor()
		}
	}
}

// maybeFireFloor checks whether the floor timer has elapsed and fires a nap
// if so. Honors the master ThoughtsEnabled flag AND NapsEnabled, and dedupes
// against recent idle-triggered naps.
func (s *Scheduler) maybeFireFloor() {
	cfg := s.cfgStore.Load()
	if !cfg.ThoughtsEnabled || !cfg.NapsEnabled {
		return
	}
	floor := cfg.NapFloorHours
	if floor <= 0 {
		floor = 6
	}

	s.mu.Lock()
	last := s.lastNapAt
	s.mu.Unlock()

	// First-ever nap: lastNapAt is zero, which technically is far in the
	// past. Still fire a nap — there's nothing to lose.
	if !last.IsZero() && time.Since(last) < time.Duration(floor)*time.Hour {
		return
	}
	s.fireNap(TriggerFloor)
}

// fireNap runs a nap with the given trigger. Held under runningMu so
// concurrent idle+floor triggers never run in parallel. Updates lastNapAt
// on any outcome (success or failure) so we don't hammer the model on
// repeated failures.
func (s *Scheduler) fireNap(trigger NapTrigger) {
	if !s.runningMu.TryLock() {
		logstore.Write("debug", "nap: another nap is already running, skipping", map[string]string{
			"trigger": string(trigger),
		})
		return
	}
	defer s.runningMu.Unlock()

	// Dedupe: if a nap just ran, skip this one.
	s.mu.Lock()
	last := s.lastNapAt
	s.mu.Unlock()
	if !last.IsZero() && time.Since(last) < napDedupeWindow {
		if s.telemetry != nil {
			s.telemetry.Emit(telemetry.KindNapSkipped, "", "", map[string]any{
				"trigger": string(trigger),
				"reason":  "dedupe_window",
			})
		}
		return
	}

	provider, err := s.resolver()
	if err != nil {
		logstore.Write("warn", "nap scheduler: provider unavailable: "+err.Error(), nil)
		if s.telemetry != nil {
			s.telemetry.Emit(telemetry.KindNapSkipped, "", "", map[string]any{
				"trigger": string(trigger),
				"reason":  "no_provider",
				"error":   err.Error(),
			})
		}
		return
	}

	var skillsList []string
	if s.skills != nil {
		skillsList = FormatSkillsList(s.skills())
	}

	s.mu.Lock()
	disp := s.dispatcher
	s.mu.Unlock()

	deps := NapDeps{
		SupportDir: s.supportDir,
		DB:         s.db,
		Provider:   provider,
		Cfg:        s.cfgStore.Load(),
		Telemetry:  s.telemetry,
		SkillsList: skillsList,
		Dispatcher: disp,
	}

	ctx, cancel := context.WithTimeout(context.Background(), napTimeout+napLockTimeout+10*time.Second)
	defer cancel()
	_ = RunNap(ctx, deps, trigger)

	// Bump lastNapAt regardless of outcome — if the nap failed, backing off
	// is still the right behavior.
	now := time.Now().UTC()
	s.mu.Lock()
	s.lastNapAt = now
	s.mu.Unlock()
	saveNapState(s.supportDir, napState{LastNapAt: now})
}

// loadNapState reads the persisted lastNapAt, tolerating missing/corrupt files.
func loadNapState(supportDir string) time.Time {
	path := filepath.Join(supportDir, napStateFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	var s napState
	if err := json.Unmarshal(data, &s); err != nil {
		return time.Time{}
	}
	return s.LastNapAt
}

// saveNapState writes the state atomically.
func saveNapState(supportDir string, s napState) {
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	path := filepath.Join(supportDir, napStateFile)
	_ = atomicWrite(path, data, 0o600)
}

// ── Package-level default scheduler ──────────────────────────────────────────

var (
	defaultSchedulerMu sync.RWMutex
	defaultScheduler   *Scheduler
)

func setDefaultScheduler(s *Scheduler) {
	defaultSchedulerMu.Lock()
	defaultScheduler = s
	defaultSchedulerMu.Unlock()
}

func clearDefaultScheduler(s *Scheduler) {
	defaultSchedulerMu.Lock()
	if defaultScheduler == s {
		defaultScheduler = nil
	}
	defaultSchedulerMu.Unlock()
}

// NotifyTurnNonBlocking resets the idle timer on the process-wide default
// scheduler. Safe to call from the chat service after every turn. Nil-safe
// when no scheduler is registered (e.g. tests, dormant config).
func NotifyTurnNonBlocking() {
	defaultSchedulerMu.RLock()
	s := defaultScheduler
	defaultSchedulerMu.RUnlock()
	if s != nil {
		s.NotifyTurn()
	}
}

// ── Wiring helper used by features package import avoidance ────────────────

// BuildSkillsLister returns a SkillsLister that reads enabled skills from the
// features package. Kept in this file so the main wiring site in main.go
// doesn't need to duplicate the filter.
func BuildSkillsLister(supportDir string) NapSkillsLister {
	return func() []SkillLine {
		records := features.ListSkills(supportDir)
		out := make([]SkillLine, 0, len(records))
		for _, rec := range records {
			if rec.Manifest.LifecycleState == "uninstalled" {
				continue
			}
			out = append(out, SkillLine{
				ID:          rec.Manifest.ID,
				Description: rec.Manifest.Description,
			})
		}
		return out
	}
}
