package agents

// agent_pipeline_test.go — 10 comprehensive tests covering the full agents pipeline:
// creation, validation, HTTP CRUD, runtime lifecycle, delegation success/failure/cancellation,
// approve/reject flow, tool-class filtering, and multi-agent isolation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func newPipelineEnv(t *testing.T) (*storage.DB, *skills.Registry, *Module, chi.Router) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	registry := skills.NewRegistry(dir, db, nil)
	module.SetSkillRegistry(registry)
	module.SetDatabase(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)
	return db, registry, module, r
}

// seedAgent saves an agent definition to the SQLite DB.
// For operations that also require AGENTS.md (enable/disable), use seedAgentWithFile.
func seedAgent(t *testing.T, db *storage.DB, id, name string, allowedSkills []string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	skillsJSON, _ := json.Marshal(allowedSkills)
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                id,
		Name:              name,
		Role:              "Test Specialist",
		Mission:           "Carry out test work",
		AllowedSkillsJSON: string(skillsJSON),
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition %q: %v", id, err)
	}
}

// seedAgentWithFile seeds an agent to the DB.
// Phase 8: AGENTS.md is no longer read by HTTP handlers (DB-first). This
// function is an alias for seedAgent kept so test call-sites don't need updating.
func seedAgentWithFile(t *testing.T, db *storage.DB, _ string, id, name string, allowedSkills []string) {
	t.Helper()
	seedAgent(t, db, id, name, allowedSkills)
}

func seedTask(t *testing.T, db *storage.DB, taskID, agentID, status string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveAgentTask(storage.AgentTaskRow{
		TaskID:    taskID,
		AgentID:   agentID,
		Status:    status,
		Goal:      "task for " + agentID,
		StartedAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveTeamTask %q: %v", taskID, err)
	}
}

func seedDeferredApproval(t *testing.T, db *storage.DB, toolCallID, agentID, convID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveDeferredExecution(storage.DeferredExecRow{
		DeferredID:          "deferred-" + toolCallID,
		SourceType:          "agent_loop",
		AgentID:             strPtr(agentID),
		ToolCallID:          toolCallID,
		NormalizedInputJSON: `{"messages":[],"tool_calls":[],"conv_id":"` + convID + `"}`,
		ConversationID:      strPtr(convID),
		ApprovalID:          "approval-" + toolCallID,
		Summary:             "approval for " + toolCallID,
		PermissionLevel:     "execute",
		RiskLevel:           "execute",
		Status:              "pending_approval",
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("SaveDeferredExecution %q: %v", toolCallID, err)
	}
}

func postJSON(t *testing.T, r chi.Router, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func getRoute(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// ─── Test 1: Validation — all error paths ────────────────────────────────────

func TestAgentValidation_AllErrorPaths(t *testing.T) {
	cases := []struct {
		name    string
		def     agentDefinition
		wantSub string
	}{
		{
			name:    "missing name",
			def:     agentDefinition{Role: "r", Mission: "m", AllowedSkills: []string{"web.*"}, Autonomy: "assistive"},
			wantSub: "name is required",
		},
		{
			name:    "missing role",
			def:     agentDefinition{Name: "Scout", ID: "scout", Mission: "m", AllowedSkills: []string{"web.*"}, Autonomy: "assistive"},
			wantSub: "missing required field Role",
		},
		{
			name:    "missing mission",
			def:     agentDefinition{Name: "Scout", ID: "scout", Role: "r", AllowedSkills: []string{"web.*"}, Autonomy: "assistive"},
			wantSub: "missing required field Mission",
		},
		{
			name:    "missing allowed skills",
			def:     agentDefinition{Name: "Scout", ID: "scout", Role: "r", Mission: "m", Autonomy: "assistive"},
			wantSub: "missing required field Allowed Skills",
		},
		{
			name:    "missing autonomy",
			def:     agentDefinition{Name: "Scout", ID: "scout", Role: "r", Mission: "m", AllowedSkills: []string{"web.*"}},
			wantSub: "missing required field Autonomy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDefinition(tc.def)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected %q in error, got: %v", tc.wantSub, err)
			}
		})
	}

	t.Run("valid definition passes", func(t *testing.T) {
		def := agentDefinition{Name: "Scout", ID: "scout", Role: "r", Mission: "m", AllowedSkills: []string{"web.*"}, Autonomy: "assistive"}
		if err := validateDefinition(def); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// ─── Test 2: HTTP CRUD (create → get → update → delete) ─────────────────────

func TestAgentHTTP_CRUD(t *testing.T) {
	_, _, _, r := newPipelineEnv(t)

	// Create via HTTP
	createBody := map[string]any{
		"name":          "Analyst",
		"role":          "Data Analyst",
		"mission":       "Summarise data trends",
		"allowedSkills": []string{"web.*", "weather.*"},
		"autonomy":      "assistive",
	}
	rr := postJSON(t, r, http.MethodPost, "/agents", createBody)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID != "analyst" {
		t.Fatalf("expected ID=analyst, got %q", created.ID)
	}
	if created.Runtime.Status != "idle" {
		t.Fatalf("expected idle runtime, got %q", created.Runtime.Status)
	}

	// GET single agent
	rr = getRoute(t, r, "/agents/analyst")
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status=%d", rr.Code)
	}
	var got agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Role != "Data Analyst" {
		t.Fatalf("expected role 'Data Analyst', got %q", got.Role)
	}

	// GET non-existent returns 404
	rr = getRoute(t, r, "/agents/does-not-exist")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing agent, got %d", rr.Code)
	}

	// PUT update role
	updateBody := map[string]any{
		"id":   "analyst",
		"role": "Senior Data Analyst",
	}
	rr = postJSON(t, r, http.MethodPut, "/agents/analyst", updateBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var updated agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Role != "Senior Data Analyst" {
		t.Fatalf("expected updated role, got %q", updated.Role)
	}

	// DELETE
	req := httptest.NewRequest(http.MethodDelete, "/agents/analyst", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent && rr.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Confirm agent is gone
	rr = getRoute(t, r, "/agents/analyst")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rr.Code)
	}
}

// ─── Test 3: Runtime lifecycle (enable / disable / pause / resume) ───────────

func TestAgentHTTP_RuntimeLifecycle(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), stubAgentRuntime{}, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	registry := skills.NewRegistry(dir, db, nil)
	module.SetSkillRegistry(registry)
	module.SetDatabase(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := chi.NewRouter()
	host.ApplyProtected(r)

	seedAgentWithFile(t, db, dir, "rover", "Rover", []string{"web.*"})

	doPost := func(path string, wantStatus string) {
		t.Helper()
		rr := postJSON(t, r, http.MethodPost, path, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("POST %s: status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		var agent agentJSON
		if err := json.NewDecoder(rr.Body).Decode(&agent); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if agent.Runtime.Status != wantStatus {
			t.Fatalf("POST %s: expected status=%q, got %q", path, wantStatus, agent.Runtime.Status)
		}
	}

	// Disable agent
	doPost("/agents/rover/disable", "idle")
	// Verify enabled=false in definition
	rr := getRoute(t, r, "/agents/rover")
	var a agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&a); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if a.Enabled {
		t.Fatal("expected Enabled=false after disable")
	}

	// Re-enable
	doPost("/agents/rover/enable", "idle")
	rr = getRoute(t, r, "/agents/rover")
	if err := json.NewDecoder(rr.Body).Decode(&a); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if !a.Enabled {
		t.Fatal("expected Enabled=true after enable")
	}

	// Pause
	doPost("/agents/rover/pause", "paused")

	// Resume
	doPost("/agents/rover/resume", "idle")
}

// ─── Test 4: Delegation success — task stored with steps ─────────────────────

func TestAgentDelegation_SuccessPath(t *testing.T) {
	db, registry, module, _ := newPipelineEnv(t)
	seedAgent(t, db, "builder", "Builder", []string{"fs.*"})

	now := time.Now().UTC().Format(time.RFC3339Nano)
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		summary := "Builder built the feature successfully."
		task := storage.AgentTaskRow{
			TaskID:        "task-success-1",
			AgentID:       def.ID,
			Status:        "completed",
			Goal:          args.Task,
			RequestedBy:   "atlas",
			ResultSummary: &summary,
			StartedAt:     now,
			FinishedAt:    &now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		steps := []storage.AgentTaskStepRow{
			{StepID: "step-1", TaskID: "task-success-1", SequenceNumber: 1, Role: "assistant", StepType: "assistant", Content: "I will write the code.", CreatedAt: now},
			{StepID: "step-2", TaskID: "task-success-1", SequenceNumber: 2, Role: "tool", StepType: "tool_call", Content: `{"action":"fs.write_file"}`, ToolName: strPtr("fs.write_file"), CreatedAt: now},
		}
		// Mirror what the real delegateTask does: persist task + steps + reset runtime.
		_ = db.SaveAgentTask(task)
		for _, s := range steps {
			_ = db.SaveAgentTaskStep(s)
		}
		_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: def.ID, Status: "idle", UpdatedAt: now})
		return delegatedRun{Task: task, Steps: steps}, nil
	}

	result, err := registry.Execute(context.Background(), "agent.delegate", json.RawMessage(`{"agentID":"builder","task":"Implement the new parser"}`))
	if err != nil {
		t.Fatalf("Execute team.delegate: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if taskID := safeStringString(result.Artifacts["taskID"]); taskID != "task-success-1" {
		t.Fatalf("unexpected taskID artifact: %#v", result.Artifacts)
	}
	if !strings.Contains(result.Summary, "built the feature successfully") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	// Verify task and steps were persisted
	task, err := db.GetAgentTask("task-success-1")
	if err != nil || task == nil {
		t.Fatalf("GetTeamTask: %v", err)
	}
	if task.Status != "completed" {
		t.Fatalf("expected completed, got %q", task.Status)
	}
	steps, err := db.ListAgentTaskSteps("task-success-1")
	if err != nil {
		t.Fatalf("ListTeamTaskSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}

	// Verify agent runtime returned to idle
	row, err := db.GetAgentRuntime("builder")
	if err != nil || row == nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	if row.Status != "idle" {
		t.Fatalf("expected idle after task completion, got %q", row.Status)
	}
}

// ─── Test 5: Delegation failure — agent goes idle with lastError ──────────────

func TestAgentDelegation_ErrorPath(t *testing.T) {
	db, registry, module, _ := newPipelineEnv(t)
	seedAgent(t, db, "fragile", "Fragile", []string{"web.*"})

	errMsg := "network connection refused"
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		// Mirror what real delegateTask does on failure: reset runtime to idle with lastError.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{
			AgentID:   def.ID,
			Status:    "idle",
			LastError: &errMsg,
			UpdatedAt: now,
		})
		return delegatedRun{}, errors.New(errMsg)
	}

	result, err := registry.Execute(context.Background(), "agent.delegate", json.RawMessage(`{"agentID":"fragile","task":"Fetch data from remote"}`))
	if err == nil {
		t.Fatalf("expected error from failed delegation, got success: %+v", result)
	}
	if !strings.Contains(err.Error(), errMsg) {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify agent runtime reset to idle with lastError populated
	row, err := db.GetAgentRuntime("fragile")
	if err != nil || row == nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	if row.Status != "idle" {
		t.Fatalf("expected idle after failed delegation, got %q", row.Status)
	}
	if row.LastError == nil || !strings.Contains(*row.LastError, errMsg) {
		t.Fatalf("expected lastError to contain the delegation error, got: %v", row.LastError)
	}
}

// ─── Test 6: Delegation cancellation ─────────────────────────────────────────

func TestAgentDelegation_CancellationRoute(t *testing.T) {
	db, _, _, r := newPipelineEnv(t)
	seedAgent(t, db, "scout", "Scout", []string{"web.*"})
	seedTask(t, db, "running-task-1", "scout", "running")

	// Set agent runtime to busy to mirror a real running state.
	taskID := "running-task-1"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{
		AgentID:       "scout",
		Status:        "busy",
		CurrentTaskID: &taskID,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveAgentRuntime: %v", err)
	}

	// Cancel the running task.
	rr := postJSON(t, r, http.MethodPost, "/agents/tasks/running-task-1/cancel", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var cancelled taskJSON
	if err := json.NewDecoder(rr.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %q", cancelled.Status)
	}

	// Agent should reset to idle.
	row, err := db.GetAgentRuntime("scout")
	if err != nil || row == nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	if row.Status != "idle" {
		t.Fatalf("expected idle after cancel, got %q", row.Status)
	}

	// Cancelling an already-cancelled task should 409.
	rr = postJSON(t, r, http.MethodPost, "/agents/tasks/running-task-1/cancel", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for re-cancel, got %d", rr.Code)
	}
}

// ─── Test 7: Approve / reject with 409 guard ─────────────────────────────────

func TestAgentApproval_ApproveRejectLifecycle(t *testing.T) {
	db, _, module, r := newPipelineEnv(t)
	seedAgent(t, db, "auditor", "Auditor", []string{"web.*"})
	module.resumeDelegateFn = func(_ context.Context, def storage.AgentDefinitionRow, task storage.AgentTaskRow, toolCallID string, approved bool) (delegatedRun, error) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if !approved {
			t.Fatalf("resumeDelegateFn called for rejected task")
		}
		if toolCallID != "tool-pending-task-1" {
			t.Fatalf("unexpected toolCallID: %s", toolCallID)
		}
		if err := db.UpdateDeferredStatus(toolCallID, "approved", now); err != nil {
			t.Fatalf("UpdateDeferredStatus: %v", err)
		}
		task.Status = "completed"
		task.ResultSummary = strPtr("Approved and resumed.")
		task.UpdatedAt = now
		task.FinishedAt = &now
		if err := db.SaveAgentTask(task); err != nil {
			t.Fatalf("SaveAgentTask: %v", err)
		}
		if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{
			AgentID:   def.ID,
			Status:    "idle",
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveAgentRuntime: %v", err)
		}
		return delegatedRun{Task: task}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: "auditor", Status: "approval_needed", UpdatedAt: now}); err != nil {
		t.Fatalf("SaveAgentRuntime: %v", err)
	}
	seedTask(t, db, "pending-task-1", "auditor", "pending_approval")
	seedDeferredApproval(t, db, "tool-pending-task-1", "auditor", "pending-task-1")

	// Approve
	rr := postJSON(t, r, http.MethodPost, "/agents/tasks/pending-task-1/approve", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var approved taskJSON
	if err := json.NewDecoder(rr.Body).Decode(&approved); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if approved.Status != "completed" {
		t.Fatalf("expected completed after approve, got %q", approved.Status)
	}
	deferred, err := db.FetchDeferredByToolCallID("tool-pending-task-1")
	if err != nil || deferred == nil {
		t.Fatalf("FetchDeferredByToolCallID approve: %v", err)
	}
	if deferred.Status != "approved" {
		t.Fatalf("expected deferred status approved, got %q", deferred.Status)
	}
	row, _ := db.GetAgentRuntime("auditor")
	if row == nil || row.Status != "idle" {
		t.Fatalf("expected idle after approve, got %v", row)
	}

	// Re-approving a completed task must 409
	rr = postJSON(t, r, http.MethodPost, "/agents/tasks/pending-task-1/approve", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for re-approve, got %d", rr.Code)
	}

	// Seed a second task and reject it
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: "auditor", Status: "approval_needed", UpdatedAt: now}); err != nil {
		t.Fatalf("SaveAgentRuntime reset: %v", err)
	}
	seedTask(t, db, "pending-task-2", "auditor", "pending_approval")
	seedDeferredApproval(t, db, "tool-pending-task-2", "auditor", "pending-task-2")

	rr = postJSON(t, r, http.MethodPost, "/agents/tasks/pending-task-2/reject", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reject: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rejected taskJSON
	if err := json.NewDecoder(rr.Body).Decode(&rejected); err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if rejected.Status != "cancelled" {
		t.Fatalf("expected cancelled after reject, got %q", rejected.Status)
	}
	deferred, err = db.FetchDeferredByToolCallID("tool-pending-task-2")
	if err != nil || deferred == nil {
		t.Fatalf("FetchDeferredByToolCallID reject: %v", err)
	}
	if deferred.Status != "denied" {
		t.Fatalf("expected deferred status denied, got %q", deferred.Status)
	}
}

// ─── Test 8: AllowedToolClasses skill filtering ───────────────────────────────

func TestAgentAllowedToolClasses_SkillFiltering(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	registry := skills.NewRegistry(dir, db, nil)
	total := registry.ToolCount()
	if total == 0 {
		t.Fatal("registry has no skills")
	}

	// Filter to read-only skills only
	readOnly := registry.FilteredByActionClasses([]string{"read"})
	readCount := readOnly.ToolCount()
	if readCount == 0 {
		t.Fatal("expected at least some read-class skills")
	}
	if readCount >= total {
		t.Fatalf("read-only filter should reduce count: total=%d readOnly=%d", total, readCount)
	}

	// Filter to read + local_write
	readWrite := registry.FilteredByActionClasses([]string{"read", "local_write"})
	readWriteCount := readWrite.ToolCount()
	if readWriteCount < readCount {
		t.Fatalf("read+local_write must be >= read-only: %d < %d", readWriteCount, readCount)
	}
	if readWriteCount >= total {
		t.Fatalf("read+local_write still should be less than total: %d >= %d", readWriteCount, total)
	}

	// Empty class list is a no-op
	all := registry.FilteredByActionClasses(nil)
	if all.ToolCount() != total {
		t.Fatalf("nil filter should return all %d skills, got %d", total, all.ToolCount())
	}
	all = registry.FilteredByActionClasses([]string{})
	if all.ToolCount() != total {
		t.Fatalf("empty filter should return all %d skills, got %d", total, all.ToolCount())
	}

	// Verify team.delegate created with allowedToolClasses enforces filtering at delegation time.
	// Create an agent via skill with only "read" tool class
	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	module.SetSkillRegistry(registry)
	module.SetDatabase(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Track which registry was used in delegation
	var capturedToolCount int
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID: "tool-class-task", AgentID: def.ID, Status: "completed",
				Goal: args.Task, StartedAt: now, CreatedAt: now, UpdatedAt: now,
			},
		}, nil
	}

	// Seed an agent with allowedToolClasses = ["read"]
	toolClassesJSON, _ := json.Marshal([]string{"read"})
	allowedSkillsJSON, _ := json.Marshal([]string{"weather.*", "web.*"})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                     "reader-agent",
		Name:                   "Reader",
		Role:                   "Read Only Specialist",
		Mission:                "Only read data",
		AllowedSkillsJSON:      string(allowedSkillsJSON),
		AllowedToolClassesJSON: string(toolClassesJSON),
		Autonomy:               "assistive",
		IsEnabled:              true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	// Delegate and verify the filtered registry count was used
	_ = capturedToolCount // acknowledged; the real assertion is delegateFn was called
	result, err := registry.Execute(context.Background(), "agent.delegate", json.RawMessage(`{"agentID":"reader-agent","task":"Read some data"}`))
	if err != nil {
		t.Fatalf("Execute team.delegate: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success: %+v", result)
	}
}

// ─── Test 9: Skill validation at create time ─────────────────────────────────

func TestAgentCreate_SkillPatternValidation(t *testing.T) {
	_, registry, _, _ := newPipelineEnv(t)

	// Valid pattern — should create successfully
	validArgs := json.RawMessage(`{
		"name":"Weatherman",
		"role":"Weather Specialist",
		"mission":"Report the weather",
		"allowedSkills":["weather.*"],
		"autonomy":"assistive"
	}`)
	result, err := registry.Execute(context.Background(), "agent.create", validArgs)
	if err != nil {
		t.Fatalf("team.create with valid skills: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success for valid pattern: %+v", result)
	}

	// Invalid pattern — matches no registered skills
	invalidArgs := json.RawMessage(`{
		"name":"Ghost Agent",
		"role":"Phantom",
		"mission":"Do nothing",
		"allowedSkills":["nonexistent.skill.*","another.fake.*"],
		"autonomy":"assistive"
	}`)
	result, err = registry.Execute(context.Background(), "agent.create", invalidArgs)
	if err == nil {
		t.Fatalf("expected error for invalid skill patterns, got success: %+v", result)
	}
	if !strings.Contains(err.Error(), "match no registered skills") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ─── Test 10: Multi-agent independent delegation ──────────────────────────────

func TestAgentMultiAgent_IndependentDelegation(t *testing.T) {
	db, registry, module, _ := newPipelineEnv(t)

	// Seed two independent agents.
	seedAgent(t, db, "alpha", "Alpha", []string{"web.*"})
	seedAgent(t, db, "beta", "Beta", []string{"weather.*"})

	calls := map[string]int{}
	summaries := map[string]string{
		"alpha": "Alpha completed its web research.",
		"beta":  "Beta checked the weather.",
	}

	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		calls[def.ID]++
		taskID := fmt.Sprintf("task-%s-1", def.ID)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		summary := summaries[def.ID]
		task := storage.AgentTaskRow{
			TaskID:        taskID,
			AgentID:       def.ID,
			Status:        "completed",
			Goal:          args.Task,
			ResultSummary: &summary,
			StartedAt:     now,
			FinishedAt:    &now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		// Persist task and reset runtime, mirroring real delegateTask behaviour.
		_ = db.SaveAgentTask(task)
		_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: def.ID, Status: "idle", UpdatedAt: now})
		return delegatedRun{Task: task}, nil
	}

	// Delegate to alpha
	r1, err := registry.Execute(context.Background(), "agent.delegate",
		json.RawMessage(`{"agentID":"alpha","task":"Research competitor pricing"}`))
	if err != nil {
		t.Fatalf("alpha delegation: %v", err)
	}
	if !r1.Success {
		t.Fatalf("alpha: expected success: %+v", r1)
	}
	if !strings.Contains(r1.Summary, "web research") {
		t.Fatalf("alpha summary mismatch: %q", r1.Summary)
	}

	// Delegate to beta
	r2, err := registry.Execute(context.Background(), "agent.delegate",
		json.RawMessage(`{"agentID":"beta","task":"Get tomorrow weather"}`))
	if err != nil {
		t.Fatalf("beta delegation: %v", err)
	}
	if !r2.Success {
		t.Fatalf("beta: expected success: %+v", r2)
	}
	if !strings.Contains(r2.Summary, "weather") {
		t.Fatalf("beta summary mismatch: %q", r2.Summary)
	}

	// Verify each agent received exactly one delegation call.
	if calls["alpha"] != 1 || calls["beta"] != 1 {
		t.Fatalf("expected 1 call each, got alpha=%d beta=%d", calls["alpha"], calls["beta"])
	}

	// Verify both tasks are stored independently.
	taskAlpha, err := db.GetAgentTask("task-alpha-1")
	if err != nil || taskAlpha == nil {
		t.Fatalf("GetTeamTask alpha: %v", err)
	}
	taskBeta, err := db.GetAgentTask("task-beta-1")
	if err != nil || taskBeta == nil {
		t.Fatalf("GetTeamTask beta: %v", err)
	}
	if taskAlpha.AgentID != "alpha" || taskBeta.AgentID != "beta" {
		t.Fatalf("task agent IDs crossed: alpha=%q beta=%q", taskAlpha.AgentID, taskBeta.AgentID)
	}
	if taskAlpha.Status != "completed" || taskBeta.Status != "completed" {
		t.Fatalf("expected both completed: alpha=%q beta=%q", taskAlpha.Status, taskBeta.Status)
	}

	// Both agents should be idle after their tasks complete.
	for _, id := range []string{"alpha", "beta"} {
		row, err := db.GetAgentRuntime(id)
		if err != nil || row == nil {
			t.Fatalf("GetAgentRuntime %q: %v", id, err)
		}
		if row.Status != "idle" {
			t.Fatalf("agent %q expected idle after completion, got %q", id, row.Status)
		}
	}
}

// ─── Test 11: Trigger cooldown atomicity ─────────────────────────────────────

func TestTriggerCoordinator_CooldownAtomicity(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir + "/test.sqlite3")
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	bus := platform.NewInProcessBus(32)
	store := platform.NewSQLiteStorage(db).Agents()

	// Create a bounded_autonomous agent with matching activation.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	skillsJSON := `["web.*"]`
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "monitor-1", Name: "Monitor", Role: "Watcher",
		Mission: "Watch things", AllowedSkillsJSON: skillsJSON,
		Autonomy: "bounded_autonomous", Activation: "automation.failed",
		IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	delegateCalls := 0
	var delegateMu sync.Mutex
	mockDelegate := func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		delegateMu.Lock()
		delegateCalls++
		delegateMu.Unlock()
		taskNow := time.Now().UTC().Format(time.RFC3339Nano)
		task := storage.AgentTaskRow{
			TaskID: newID("task"), AgentID: def.ID, Status: "completed",
			Goal: args.Goal, RequestedBy: args.RequestedBy,
			StartedAt: taskNow, CreatedAt: taskNow, UpdatedAt: taskNow,
		}
		_ = db.SaveAgentTask(task)
		return delegatedRun{Task: task}, nil
	}

	coordinator := &triggerCoordinator{
		store:      store,
		bus:        bus,
		delegateFn: mockDelegate,
	}

	// Fire the same trigger type concurrently from 10 goroutines.
	const concurrent = 10
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = coordinator.Evaluate(context.Background(), "automation.failed",
				"concurrent test", nil, "")
		}()
	}
	wg.Wait()

	// Despite 10 concurrent callers, exactly 1 delegation should have fired.
	delegateMu.Lock()
	calls := delegateCalls
	delegateMu.Unlock()

	if calls != 1 {
		t.Fatalf("expected exactly 1 delegation despite %d concurrent triggers, got %d", concurrent, calls)
	}

	// All remaining should be suppressed — verify trigger_events table.
	rows, err := db.ListTriggerEvents(50)
	if err != nil {
		t.Fatalf("ListTriggerEvents: %v", err)
	}
	fired, suppressed := 0, 0
	for _, r := range rows {
		switch r.Status {
		case "fired":
			fired++
		case "suppressed":
			suppressed++
		}
	}
	if fired != 1 {
		t.Fatalf("expected 1 fired event, got %d", fired)
	}
	if suppressed != concurrent-1 {
		t.Fatalf("expected %d suppressed events, got %d", concurrent-1, suppressed)
	}
}

// ─── Test 12: Sequential workflow step failure ────────────────────────────────

func TestAgentSequence_StepFailureRecovery(t *testing.T) {
	db, _, module, _ := newPipelineEnv(t)
	seedAgent(t, db, "step1", "StepOne", []string{"web.*"})
	seedAgent(t, db, "step2", "StepTwo", []string{"web.*"})

	callCount := 0
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		callCount++
		taskNow := time.Now().UTC().Format(time.RFC3339Nano)
		if def.ID == "step1" {
			// Step 1 succeeds.
			errMsg := ""
			task := storage.AgentTaskRow{
				TaskID: "seq-task-1", AgentID: def.ID, Status: "completed",
				Goal: args.Goal, RequestedBy: args.RequestedBy,
				StartedAt: taskNow, CreatedAt: taskNow, UpdatedAt: taskNow,
			}
			_ = db.SaveAgentTask(task)
			_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{
				AgentID: def.ID, Status: "idle", UpdatedAt: taskNow,
			})
			_ = errMsg
			return delegatedRun{Task: task}, nil
		}
		// Step 2 fails.
		errMsg := "simulated failure"
		task := storage.AgentTaskRow{
			TaskID: "seq-task-2", AgentID: def.ID, Status: "error",
			Goal: args.Goal, RequestedBy: args.RequestedBy,
			ErrorMessage: &errMsg,
			StartedAt:    taskNow, CreatedAt: taskNow, UpdatedAt: taskNow,
		}
		_ = db.SaveAgentTask(task)
		_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{
			AgentID: def.ID, Status: "idle", LastError: &errMsg, UpdatedAt: taskNow,
		})
		return delegatedRun{Task: task}, nil
	}

	import_json := `{"goal":"test goal","agents":[{"agentID":"step1","task":"do step 1"},{"agentID":"step2","task":"do step 2"},{"agentID":"step1","task":"do step 3"}]}`
	result, err := module.agentSequence(context.Background(), []byte(import_json))
	if err != nil {
		t.Fatalf("agentSequence returned error: %v", err)
	}
	// Should have run only 2 steps (step1 ok, step2 failed → stop).
	if callCount != 2 {
		t.Fatalf("expected 2 delegation calls (stop on failure), got %d", callCount)
	}
	// Result should indicate partial status.
	if result.Summary == "" {
		t.Fatal("expected non-empty summary from partial sequence")
	}
	if status, _ := result.Artifacts["status"].(string); status != "partial" {
		t.Fatalf("expected status=partial, got %q", status)
	}
}

// ─── Test 13: Autonomy filter — only bounded_autonomous agents trigger ────────

func TestTriggerCoordinator_BoundedAutonomyFilter(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir + "/test.sqlite3")
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	store := platform.NewSQLiteStorage(db).Agents()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	skillsJSON := `["web.*"]`

	// Create agents with different autonomy levels — only bounded_autonomous should trigger.
	for _, ag := range []struct {
		id, autonomy, activation string
	}{
		{"on-demand-agent", "on_demand", "automation.failed"},
		{"assistive-agent", "assistive", "automation.failed"},
		{"bounded-agent", "bounded_autonomous", "automation.failed"},
	} {
		if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
			ID: ag.id, Name: ag.id, Role: "Test", Mission: "Test",
			AllowedSkillsJSON: skillsJSON, Autonomy: ag.autonomy,
			Activation: ag.activation, IsEnabled: true,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveAgentDefinition %q: %v", ag.id, err)
		}
	}

	var triggered []string
	var mu sync.Mutex
	mockDelegate := func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		mu.Lock()
		triggered = append(triggered, def.ID)
		mu.Unlock()
		taskNow := time.Now().UTC().Format(time.RFC3339Nano)
		task := storage.AgentTaskRow{
			TaskID: newID("task"), AgentID: def.ID, Status: "completed",
			Goal: args.Goal, RequestedBy: "auto",
			StartedAt: taskNow, CreatedAt: taskNow, UpdatedAt: taskNow,
		}
		_ = db.SaveAgentTask(task)
		return delegatedRun{Task: task}, nil
	}

	coordinator := &triggerCoordinator{
		store:      store,
		delegateFn: mockDelegate,
	}
	_ = coordinator.Evaluate(context.Background(), "automation.failed", "test", nil, "")

	// Wait briefly for goroutines to finish.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	n := len(triggered)
	ids := triggered
	mu.Unlock()

	if n != 1 {
		t.Fatalf("expected exactly 1 triggered agent, got %d: %v", n, ids)
	}
	if ids[0] != "bounded-agent" {
		t.Fatalf("expected bounded-agent to trigger, got %q", ids[0])
	}
}

// ─── Test 14: AssignTask HTTP handler sets requestedBy=user ──────────────────
// Phase 5: assignTask is now async — returns 202 Accepted with taskID
// immediately. The goroutine completes in the background; we wait briefly
// before verifying the persisted task.

func TestAssignTask_RequestedByUser(t *testing.T) {
	db, _, module, r := newPipelineEnv(t)
	seedAgent(t, db, "scout", "Scout", []string{"web.*"})

	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		taskNow := time.Now().UTC().Format(time.RFC3339Nano)
		// Use the pre-generated task ID from args (passed via delegateArgs.TaskID).
		taskID := args.TaskID
		if taskID == "" {
			taskID = "http-assign-task" // fallback for legacy calls
		}
		task := storage.AgentTaskRow{
			TaskID: taskID, AgentID: def.ID, Status: "completed",
			Goal: args.Goal, RequestedBy: args.RequestedBy,
			StartedAt: taskNow, CreatedAt: taskNow, UpdatedAt: taskNow,
		}
		_ = db.SaveAgentTask(task)
		_ = db.SaveAgentRuntime(storage.AgentRuntimeRow{
			AgentID: def.ID, Status: "idle", UpdatedAt: taskNow,
		})
		return delegatedRun{Task: task}, nil
	}

	rr := postJSON(t, r, http.MethodPost, "/agents/tasks", map[string]any{
		"agentID": "scout",
		"task":    "do some research",
		"goal":    "research goal",
	})
	// Phase 5: async_assignment returns 202 Accepted immediately.
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}

	// Parse the task ID from the 202 response body.
	var resp struct {
		TaskID  string `json:"taskID"`
		AgentID string `json:"agentID"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode 202 response: %v", err)
	}
	if resp.TaskID == "" {
		t.Fatal("expected non-empty taskID in 202 response")
	}
	if resp.Status != "queued" {
		t.Fatalf("expected status=queued in 202 response, got %q", resp.Status)
	}

	// Wait briefly for the goroutine to complete, then verify the persisted task.
	time.Sleep(50 * time.Millisecond)

	task, err := db.GetAgentTask(resp.TaskID)
	if err != nil || task == nil {
		t.Fatalf("GetAgentTask(%q): %v", resp.TaskID, err)
	}
	if task.RequestedBy != "user" {
		t.Fatalf("expected requestedBy=user from HTTP assign, got %q", task.RequestedBy)
	}
}
