package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

// Cooldown duration — same (trigger_type, agent_id) pair cannot fire more than once per window.
const triggerCooldown = 5 * time.Minute

// triggerCoordinator implements M6 bounded autonomy.
// It subscribes to EventBus events, finds matching bounded_autonomous agents,
// enforces cooldowns atomically, and fires delegations.
type triggerCoordinator struct {
	store      platform.AgentStore
	bus        platform.EventBus
	delegateFn func(ctx context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error)
	unsubFns   []func()
	pollStop   chan struct{}
	mu         sync.Mutex
	ctx        context.Context    // cancelled on Stop to signal in-flight goroutines
	cancel     context.CancelFunc // cancels ctx
	wg         sync.WaitGroup     // tracks all in-flight trigger goroutines
}

// Start subscribes to events and starts the approval-backlog poll goroutine.
func (tc *triggerCoordinator) Start(ctx context.Context) error {
	tc.ctx, tc.cancel = context.WithCancel(ctx)
	if tc.bus == nil {
		return nil
	}

	// automation.failed → evaluate Monitor agents
	unsub1, err := tc.bus.Subscribe("automation.failed", func(ctx context.Context, event platform.Event) error {
		payload := payloadMap(event.Payload)
		return tc.Evaluate(ctx, "automation.failed", "An automation has failed. Review the failure and summarize the root cause.", payload, "")
	})
	if err == nil {
		tc.unsubFns = append(tc.unsubFns, unsub1)
	}

	// agent.task.failed → evaluate Reviewer agents (skip originating agent to prevent recursion)
	unsub2, err := tc.bus.Subscribe("agent.task.failed", func(ctx context.Context, event platform.Event) error {
		payload := payloadMap(event.Payload)
		originAgentID, _ := payload["agent_id"].(string)
		if originAgentID == "" {
			originAgentID, _ = payload["agentID"].(string)
		}
		return tc.Evaluate(ctx, "agent.task.failed", "A delegated task has failed. Investigate the error and recommend next steps.", payload, originAgentID)
	})
	if err == nil {
		tc.unsubFns = append(tc.unsubFns, unsub2)
	}

	// Approval backlog poll — runs every 60s
	tc.pollStop = make(chan struct{})
	go tc.runApprovalBacklogPoller(ctx)

	return nil
}

// Stop cancels the coordinator context, unsubscribes all event handlers, stops
// the poll goroutine, and waits for all in-flight trigger goroutines to finish.
func (tc *triggerCoordinator) Stop() {
	if tc.cancel != nil {
		tc.cancel()
	}

	tc.mu.Lock()
	fns := tc.unsubFns
	tc.unsubFns = nil
	pollStop := tc.pollStop
	tc.pollStop = nil
	tc.mu.Unlock()

	for _, fn := range fns {
		fn()
	}
	if pollStop != nil {
		close(pollStop)
	}

	tc.wg.Wait() // wait for all in-flight trigger goroutines
}

// Evaluate finds eligible bounded_autonomous agents for triggerType and fires delegations.
// skipAgentID prevents recursive triggering of the originating agent.
func (tc *triggerCoordinator) Evaluate(ctx context.Context, triggerType, instruction string, payload map[string]any, skipAgentID string) error {
	defs, err := tc.store.ListAgentDefinitions()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, def := range defs {
		if !def.IsEnabled {
			continue
		}
		if def.Autonomy != "bounded_autonomous" {
			continue
		}
		if def.ID == skipAgentID {
			continue
		}
		if !matchesActivation(def.Activation, triggerType) {
			continue
		}

		// Atomically acquire the cooldown slot. TryAcquireTriggerCooldown performs
		// a single INSERT … SELECT … WHERE NOT EXISTS, so the check and the write
		// happen in one SQLite operation — no race window between two goroutines.
		cooldownID := newID("cooldown")
		acquired, err := tc.store.TryAcquireTriggerCooldown(cooldownID, triggerType, def.ID, triggerCooldown)
		if err != nil {
			continue
		}
		if !acquired {
			// Still within cooldown window — record suppressed event with expiry info.
			expiresAt := time.Now().UTC().Add(triggerCooldown).Format(time.RFC3339Nano)
			suppressReason := fmt.Sprintf("On cooldown until ~%s", expiresAt)
			_ = tc.store.SaveTriggerEvent(storage.TriggerEventRow{
				TriggerID:   newID("trigger"),
				TriggerType: triggerType,
				AgentID:     strPtr(def.ID),
				Instruction: suppressReason,
				Status:      "suppressed",
				CreatedAt:   now,
			})
			continue
		}

		// Cooldown acquired — fire delegation.
		triggerID := newID("trigger")
		_ = tc.store.SaveTriggerEvent(storage.TriggerEventRow{
			TriggerID:   triggerID,
			TriggerType: triggerType,
			AgentID:     strPtr(def.ID),
			Instruction: instruction,
			Status:      "fired",
			FiredAt:     strPtr(now),
			CreatedAt:   now,
		})

		defCopy := def
		tc.wg.Add(1)
		go func() {
			defer tc.wg.Done()
			_, _ = tc.delegateFn(tc.ctx, defCopy, delegateArgs{
				AgentID:     defCopy.ID,
				Task:        instruction,
				Goal:        "[auto] " + triggerType,
				RequestedBy: "auto",
			})
		}()
	}
	return nil
}

// matchesActivation returns true if the agent's Activation field includes triggerType.
// The Activation field can be a comma-separated list of trigger types.
func matchesActivation(activation, triggerType string) bool {
	if strings.TrimSpace(activation) == "" {
		return false
	}
	for _, part := range strings.Split(activation, ",") {
		if strings.TrimSpace(part) == triggerType {
			return true
		}
	}
	return false
}

// runApprovalBacklogPoller fires an approval.backlog trigger when pending tasks exceed 3.
func (tc *triggerCoordinator) runApprovalBacklogPoller(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-tc.pollStop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if tasks, err := tc.store.ListAgentTasks(200); err == nil {
				count := 0
				for _, t := range tasks {
					if t.Status == "pending_approval" {
						count++
					}
				}
				if count > 3 {
					instruction := "Several tasks are pending approval. Review the backlog and recommend which to approve or reject."
					_ = tc.Evaluate(ctx, "approval.backlog", instruction, map[string]any{"pendingCount": count}, "")
				}
			}
		}
	}
}

// payloadMap safely decodes an event payload into a string map.
func payloadMap(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		return v
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return out
	}
}
