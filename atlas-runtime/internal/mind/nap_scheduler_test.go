package mind

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/mind/thoughts"
)

// stubSchedulerDeps builds a Scheduler wired to a throwaway cfgStore and
// stubbed provider resolver. The test controls the config via cfgPtr.
func stubSchedulerDeps(t *testing.T) (*Scheduler, *config.Store, string) {
	t.Helper()
	dir := t.TempDir()

	// A cfg store backed by a temp file so updates via Save() persist
	// predictably.
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Defaults()
	cfg.NapsEnabled = false
	cfg.NapIdleMinutes = 1 // 1-minute idle trigger for tests that actually fire
	cfg.NapFloorHours = 1
	data, _ := json.Marshal(cfg)
	_ = os.WriteFile(cfgPath, data, 0o600)

	store := config.NewStoreAt(cfgPath, "")

	sch := NewScheduler(dir, nil, store, nil,
		func() (agent.ProviderConfig, error) {
			return agent.ProviderConfig{}, nil
		},
		func() []SkillLine { return nil },
	)
	return sch, store, dir
}

func TestScheduler_StartStopIdempotent(t *testing.T) {
	sch, _, _ := stubSchedulerDeps(t)
	ctx := context.Background()
	sch.Start(ctx)
	sch.Start(ctx) // idempotent

	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sch.Stop(stopCtx)
	sch.Stop(stopCtx) // idempotent
}

func TestNotifyTurnNonBlocking_NilSafe(t *testing.T) {
	// Save and restore so we don't bleed into other tests.
	prev := defaultSchedulerSnapshot()
	defer setDefaultScheduler(prev)
	clearDefaultScheduler(prev)
	NotifyTurnNonBlocking()
}

func testStopCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestScheduler_NotifyTurnDoesNotFireWhenDisabled(t *testing.T) {
	sch, store, _ := stubSchedulerDeps(t)
	// NapsEnabled is false by default from stubSchedulerDeps.
	sch.Start(context.Background())
	defer sch.Stop(testStopCtx(t))

	sch.NotifyTurn()
	sch.mu.Lock()
	timer := sch.idleTimer
	sch.mu.Unlock()

	if timer != nil {
		t.Error("NotifyTurn should NOT arm the timer when NapsEnabled is false")
	}
	_ = store
}

func TestScheduler_NotifyTurnArmsTimerWhenEnabled(t *testing.T) {
	sch, store, _ := stubSchedulerDeps(t)

	// Flip the enable flag via the cfgStore.
	cfg := store.Load()
	cfg.NapsEnabled = true
	cfg.NapIdleMinutes = 10 // 10 minutes — long enough that the timer does not fire during the test
	_ = store.Save(cfg)

	sch.Start(context.Background())
	defer sch.Stop(testStopCtx(t))

	sch.NotifyTurn()
	sch.mu.Lock()
	timer := sch.idleTimer
	sch.mu.Unlock()

	if timer == nil {
		t.Error("NotifyTurn should arm the timer when NapsEnabled is true")
	}
}

func TestScheduler_NotifyTurnResetsExistingTimer(t *testing.T) {
	sch, store, _ := stubSchedulerDeps(t)
	cfg := store.Load()
	cfg.NapsEnabled = true
	cfg.NapIdleMinutes = 10
	_ = store.Save(cfg)

	sch.Start(context.Background())
	defer sch.Stop(testStopCtx(t))

	sch.NotifyTurn()
	sch.mu.Lock()
	first := sch.idleTimer
	sch.mu.Unlock()

	sch.NotifyTurn()
	sch.mu.Lock()
	second := sch.idleTimer
	sch.mu.Unlock()

	if first == second {
		t.Error("NotifyTurn should replace the existing timer")
	}
}

func TestNapState_PersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	saveNapState(dir, napState{LastNapAt: now})

	got := loadNapState(dir)
	if !got.Equal(now) {
		t.Errorf("lastNapAt round-trip: got %v, want %v", got, now)
	}
}

func TestNapState_LoadMissing(t *testing.T) {
	got := loadNapState(t.TempDir())
	if !got.IsZero() {
		t.Errorf("missing file should return zero time, got %v", got)
	}
}

func TestApplyConfigToThoughts(t *testing.T) {
	// Save and restore the vars to avoid bleeding state between tests.
	origAuto := thoughts.AutoExecuteThreshold
	origProp := thoughts.ProposeThreshold
	origNeg := thoughts.DiscardOnNegatives
	origIgn := thoughts.DiscardOnIgnores
	defer func() {
		thoughts.AutoExecuteThreshold = origAuto
		thoughts.ProposeThreshold = origProp
		thoughts.DiscardOnNegatives = origNeg
		thoughts.DiscardOnIgnores = origIgn
	}()

	cfg := config.Defaults()
	cfg.ThoughtAutoExecuteThreshold = 97
	cfg.ThoughtProposeThreshold = 75
	cfg.ThoughtDiscardOnNegatives = 4
	cfg.ThoughtDiscardOnIgnores = 5
	applyConfigToThoughtsImpl(cfg)

	if thoughts.AutoExecuteThreshold != 97 {
		t.Errorf("AutoExecuteThreshold: got %d, want 97", thoughts.AutoExecuteThreshold)
	}
	if thoughts.ProposeThreshold != 75 {
		t.Errorf("ProposeThreshold: got %d, want 75", thoughts.ProposeThreshold)
	}
	if thoughts.DiscardOnNegatives != 4 {
		t.Errorf("DiscardOnNegatives: got %d, want 4", thoughts.DiscardOnNegatives)
	}
	if thoughts.DiscardOnIgnores != 5 {
		t.Errorf("DiscardOnIgnores: got %d, want 5", thoughts.DiscardOnIgnores)
	}
}

func TestApplyConfigToThoughts_ZeroValuesPreserveDefaults(t *testing.T) {
	origAuto := thoughts.AutoExecuteThreshold
	defer func() { thoughts.AutoExecuteThreshold = origAuto }()

	cfg := config.Defaults()
	cfg.ThoughtAutoExecuteThreshold = 0 // zero means "keep default"
	applyConfigToThoughtsImpl(cfg)

	if thoughts.AutoExecuteThreshold != origAuto {
		t.Errorf("zero config should preserve default, got %d", thoughts.AutoExecuteThreshold)
	}
}

// defaultSchedulerSnapshot returns the current default scheduler pointer
// (nil-safe) so tests can reset it without importing sync/atomic primitives.
func defaultSchedulerSnapshot() *Scheduler {
	defaultSchedulerMu.RLock()
	defer defaultSchedulerMu.RUnlock()
	return defaultScheduler
}

