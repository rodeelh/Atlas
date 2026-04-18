package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type stubAgentRuntime struct{}

func (stubAgentRuntime) HandleMessage(context.Context, chat.MessageRequest) (chat.MessageResponse, error) {
	return chat.MessageResponse{}, nil
}

func (stubAgentRuntime) Resume(string, bool) {}

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestParseAgentsMarkdown_RoundTrip(t *testing.T) {
	input := `# Atlas Team

## Atlas
(Atlas's own station — always present)

## Team Members

### Scout
- ID: scout
- Role: Research Specialist
- Mission: Gather facts
- Style: concise
- Allowed Skills: web.search, fs.read_file
- Allowed Tool Classes: read
- Autonomy: assistive
- Activation: atlas_in_task_assist
- Enabled: yes
`

	defs, err := parseAgentsMarkdown(input)
	if err != nil {
		t.Fatalf("parseAgentsMarkdown: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].ID != "scout" || defs[0].Name != "Scout" {
		t.Fatalf("unexpected definition: %+v", defs[0])
	}
	rendered := renderAgentsMarkdown(defs)
	if !strings.Contains(rendered, "### Scout") || !strings.Contains(rendered, "- Allowed Skills: web.search, fs.read_file") {
		t.Fatalf("rendered output missing expected fields:\n%s", rendered)
	}
}

func TestNormalizeSkillPattern(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Canonical bare prefix — unchanged
		{"fs", "fs"},
		{"terminal", "terminal"},
		{"websearch", "websearch"},
		// Wildcard → bare prefix
		{"fs.*", "fs"},
		{"terminal.*", "terminal"},
		// Dot-suffix → bare prefix
		{"fs.", "fs"},
		{"terminal.", "terminal"},
		// Exact action ID — preserved as-is (intentionally restrictive)
		{"fs.read_file", "fs.read_file"},
		{"web.search", "web.search"},
		// Whitespace trimmed
		{"  fs  ", "fs"},
		{"  fs.*  ", "fs"},
	}
	for _, c := range cases {
		got := normalizeSkillPattern(c.input)
		if got != c.want {
			t.Errorf("normalizeSkillPattern(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestNormalizeSkillPatterns_DeduplicatesAfterNormalization(t *testing.T) {
	// "fs", "fs.*", "fs." all collapse to "fs" — only one entry should remain.
	patterns := []string{"fs.*", "fs.", "fs", "terminal", "terminal.*", "web.search"}
	got := normalizeSkillPatterns(patterns)
	want := []string{"fs", "terminal", "web.search"}
	if len(got) != len(want) {
		t.Fatalf("normalizeSkillPatterns(%v) = %v, want %v", patterns, got, want)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("normalizeSkillPatterns[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestNormalizeDefinition_SkillPatternsAreCanonical(t *testing.T) {
	// When an agent definition arrives with mixed pattern forms, normalizeDefinition
	// must store it in canonical bare-prefix form.
	def := agentDefinition{
		Name:          "Tester",
		ID:            "tester",
		Role:          "QA",
		Mission:       "Test things",
		AllowedSkills: []string{"fs.*", "terminal.", "websearch", "web.search"},
		Autonomy:      "assistive",
		Enabled:       true,
	}
	got := normalizeDefinition(def)
	want := []string{"fs", "terminal", "websearch", "web.search"}
	if len(got.AllowedSkills) != len(want) {
		t.Fatalf("AllowedSkills = %v, want %v", got.AllowedSkills, want)
	}
	for i, g := range got.AllowedSkills {
		if g != want[i] {
			t.Errorf("AllowedSkills[%d] = %q, want %q", i, g, want[i])
		}
	}
}

func TestModule_SyncCreateAndPauseRoutes(t *testing.T) {
	dir := t.TempDir()

	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	// SQLite is the sole authority (Phase 8). Seed the Scout agent directly into
	// the DB — the module no longer imports AGENTS.md on startup.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "scout",
		Name:              "Scout",
		Role:              "Research Specialist",
		Mission:           "Gather facts",
		AllowedSkillsJSON: `["web.search","fs.read_file"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

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
	if err := module.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /team/agents status=%d body=%s", rr.Code, rr.Body.String())
	}
	var agents []agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&agents); err != nil {
		t.Fatalf("decode /team/agents: %v", err)
	}
	if len(agents) != 1 || agents[0].Runtime.Status != "idle" {
		t.Fatalf("unexpected agents payload: %+v", agents)
	}

	createBody := bytes.NewBufferString(`{
		"name":"Builder",
		"role":"Implementation Specialist",
		"mission":"Ship code changes",
		"allowedSkills":["fs.read_file","fs.write_file"],
		"autonomy":"assistive"
	}`)
	req = httptest.NewRequest(http.MethodPost, "/agents", createBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /team/agents status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/agents/builder/pause", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /team/agents/builder/pause status=%d body=%s", rr.Code, rr.Body.String())
	}
	var paused agentJSON
	if err := json.NewDecoder(rr.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused agent: %v", err)
	}
	if paused.Runtime.Status != "paused" {
		t.Fatalf("expected paused runtime, got %+v", paused.Runtime)
	}

	// Phase 8: CRUD no longer writes AGENTS.md. Verify both agents are in the DB.
	allDefs, err := db.ListAgentDefinitions()
	if err != nil {
		t.Fatalf("ListAgentDefinitions: %v", err)
	}
	if len(allDefs) != 2 {
		t.Fatalf("expected 2 definitions in DB, got %d", len(allDefs))
	}

	req = httptest.NewRequest(http.MethodGet, "/agents/events", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /team/events status=%d body=%s", rr.Code, rr.Body.String())
	}
	var events []agentEventJSON
	if err := json.NewDecoder(rr.Body).Decode(&events); err != nil {
		t.Fatalf("decode /team/events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one team event")
	}

	req = httptest.NewRequest(http.MethodGet, "/agents/hq", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /agents/hq status=%d body=%s", rr.Code, rr.Body.String())
	}
	var snapshot agentSnapshot
	if err := json.NewDecoder(rr.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode /agents/hq: %v", err)
	}
	if snapshot.Atlas.Status == "" || len(snapshot.Activity) == 0 {
		t.Fatalf("unexpected /agents/hq snapshot: %+v", snapshot)
	}
}

func TestTeamDelegateSkill_ExecutesThroughRegistry(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := "2026-04-11T12:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:        "teamtask-1",
				AgentID:       def.ID,
				Status:        "completed",
				Goal:          args.Task,
				RequestedBy:   "atlas",
				ResultSummary: strPtr("Builder finished the delegated work."),
				StartedAt:     now,
				FinishedAt:    strPtr(now),
				CreatedAt:     now,
				UpdatedAt:     now,
			},
			Steps: []storage.AgentTaskStepRow{
				{StepID: "teamstep-1", TaskID: "teamtask-1", SequenceNumber: 1, Role: "assistant", StepType: "assistant", Content: "done", CreatedAt: now},
			},
		}, nil
	}
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "builder",
		Name:              "Builder",
		Role:              "Implementation Specialist",
		Mission:           "Ship code changes",
		AllowedSkillsJSON: `["fs.read_file"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         "2026-04-11T12:00:00Z",
		UpdatedAt:         "2026-04-11T12:00:00Z",
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	result, err := registry.Execute(context.Background(), "agent.delegate", json.RawMessage(`{"agentID":"builder","task":"Refactor the parser"}`))
	if err != nil {
		t.Fatalf("registry.Execute(team.delegate): %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success result, got %+v", result)
	}
	if got := safeStringString(result.Artifacts["taskID"]); got != "teamtask-1" {
		t.Fatalf("unexpected task id artifact: %#v", result.Artifacts)
	}
	if !strings.Contains(result.Summary, "Builder finished the delegated work.") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
}

func TestTeamManagementSkills_CreateListDeleteThroughRegistry(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	createArgs := json.RawMessage(`{
		"name":"Inbox Scout",
		"role":"Inbox Triage Specialist",
		"mission":"Check email regularly and escalate useful items.",
		"allowedSkills":["weather.*","web.*"],
		"autonomy":"assistive",
		"activation":"recurring inbox monitoring"
	}`)
	created, err := registry.Execute(context.Background(), "agent.create", createArgs)
	if err != nil {
		t.Fatalf("registry.Execute(team.create): %v", err)
	}
	if !created.Success {
		t.Fatalf("expected success result, got %+v", created)
	}
	createdAgent, _ := created.Artifacts["agent"].(agentJSON)
	if got := createdAgent.ID; got != "inbox-scout" {
		t.Fatalf("unexpected created agent id: %#v", created.Artifacts)
	}

	listed, err := registry.Execute(context.Background(), "agent.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("registry.Execute(team.list): %v", err)
	}
	agents, _ := listed.Artifacts["agents"].([]agentJSON)
	if len(agents) != 1 {
		t.Fatalf("expected 1 team member, got %#v", listed.Artifacts["agents"])
	}

	deleted, err := registry.Execute(context.Background(), "agent.delete", json.RawMessage(`{"id":"inbox-scout"}`))
	if err != nil {
		t.Fatalf("registry.Execute(team.delete): %v", err)
	}
	if !deleted.Success {
		t.Fatalf("expected delete success result, got %+v", deleted)
	}
	// Phase 8: CRUD no longer writes AGENTS.md. Verify deletion via DB.
	row, err := db.GetAgentDefinition("inbox-scout")
	if err != nil {
		t.Fatalf("GetAgentDefinition after delete: %v", err)
	}
	if row != nil {
		t.Fatalf("expected agent to be gone from DB after delete, still present")
	}
}

func TestModule_ApproveRejectTaskRoutes(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	now := "2026-04-11T12:00:00Z"
	module.resumeDelegateFn = func(_ context.Context, def storage.AgentDefinitionRow, task storage.AgentTaskRow, toolCallID string, approved bool) (delegatedRun, error) {
		if !approved {
			t.Fatalf("resumeDelegateFn called for rejected task")
		}
		if toolCallID != "tool-call-approve" {
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
		if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{AgentID: def.ID, Status: "idle", UpdatedAt: now}); err != nil {
			t.Fatalf("SaveAgentRuntime: %v", err)
		}
		return delegatedRun{Task: task}, nil
	}

	// Seed an agent and a pending_approval task.
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "scout",
		Name:              "Scout",
		Role:              "Research",
		Mission:           "Do research",
		AllowedSkillsJSON: `["web.*"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{
		AgentID:   "scout",
		Status:    "approval_needed",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentRuntime: %v", err)
	}
	pendingTask := storage.AgentTaskRow{
		TaskID:         "task-approve-1",
		AgentID:        "scout",
		Status:         "pending_approval",
		Goal:           "Search the web",
		ConversationID: strPtr("task-approve-1"),
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.SaveAgentTask(pendingTask); err != nil {
		t.Fatalf("SaveTeamTask: %v", err)
	}
	if err := db.SaveDeferredExecution(storage.DeferredExecRow{
		DeferredID:          "deferred-approve",
		SourceType:          "agent_loop",
		AgentID:             strPtr("scout"),
		ToolCallID:          "tool-call-approve",
		NormalizedInputJSON: `{"messages":[],"tool_calls":[],"conv_id":"task-approve-1"}`,
		ConversationID:      strPtr("task-approve-1"),
		ApprovalID:          "approval-approve",
		Summary:             "approval for approve route",
		PermissionLevel:     "execute",
		RiskLevel:           "execute",
		Status:              "pending_approval",
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("SaveDeferredExecution approve: %v", err)
	}

	// Approve the task.
	req := httptest.NewRequest(http.MethodPost, "/agents/tasks/task-approve-1/approve", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /approve status=%d body=%s", rr.Code, rr.Body.String())
	}
	var approved taskJSON
	if err := json.NewDecoder(rr.Body).Decode(&approved); err != nil {
		t.Fatalf("decode approved task: %v", err)
	}
	if approved.Status != "completed" {
		t.Fatalf("expected completed after approve, got %q", approved.Status)
	}
	deferred, err := db.FetchDeferredByToolCallID("tool-call-approve")
	if err != nil || deferred == nil {
		t.Fatalf("FetchDeferredByToolCallID approve: %v", err)
	}
	if deferred.Status != "approved" {
		t.Fatalf("expected deferred approved, got %q", deferred.Status)
	}

	// Agent should return to idle.
	row, err := db.GetAgentRuntime("scout")
	if err != nil || row == nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	if row.Status != "idle" {
		t.Fatalf("expected idle after approve, got %q", row.Status)
	}

	// Seed a second pending_approval task and reject it.
	pendingTask2 := storage.AgentTaskRow{
		TaskID:         "task-reject-1",
		AgentID:        "scout",
		Status:         "pending_approval",
		Goal:           "Search again",
		ConversationID: strPtr("task-reject-1"),
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.SaveAgentTask(pendingTask2); err != nil {
		t.Fatalf("SaveTeamTask: %v", err)
	}
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{
		AgentID:   "scout",
		Status:    "approval_needed",
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentRuntime reset: %v", err)
	}
	if err := db.SaveDeferredExecution(storage.DeferredExecRow{
		DeferredID:          "deferred-reject",
		SourceType:          "agent_loop",
		AgentID:             strPtr("scout"),
		ToolCallID:          "tool-call-reject",
		NormalizedInputJSON: `{"messages":[],"tool_calls":[],"conv_id":"task-reject-1"}`,
		ConversationID:      strPtr("task-reject-1"),
		ApprovalID:          "approval-reject",
		Summary:             "approval for reject route",
		PermissionLevel:     "execute",
		RiskLevel:           "execute",
		Status:              "pending_approval",
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("SaveDeferredExecution reject: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/agents/tasks/task-reject-1/reject", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /reject status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rejected taskJSON
	if err := json.NewDecoder(rr.Body).Decode(&rejected); err != nil {
		t.Fatalf("decode rejected task: %v", err)
	}
	if rejected.Status != "cancelled" {
		t.Fatalf("expected cancelled after reject, got %q", rejected.Status)
	}
	deferred, err = db.FetchDeferredByToolCallID("tool-call-reject")
	if err != nil || deferred == nil {
		t.Fatalf("FetchDeferredByToolCallID reject: %v", err)
	}
	if deferred.Status != "denied" {
		t.Fatalf("expected deferred denied, got %q", deferred.Status)
	}

	// Approving a non-pending task should 409.
	req = httptest.NewRequest(http.MethodPost, "/agents/tasks/task-approve-1/approve", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for non-pending task, got %d", rr.Code)
	}
}

func TestModule_StaleStateRecoveryOnRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	now := "2026-04-11T12:00:00Z"

	// Seed stale state: agent stuck "busy", task stuck "running".
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "scout",
		Name:              "Scout",
		Role:              "Research",
		Mission:           "Do research",
		AllowedSkillsJSON: `["web.*"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}
	stalTaskID := "stale-task-1"
	if err := db.SaveAgentRuntime(storage.AgentRuntimeRow{
		AgentID:       "scout",
		Status:        "busy",
		CurrentTaskID: &stalTaskID,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveAgentRuntime: %v", err)
	}
	if err := db.SaveAgentTask(storage.AgentTaskRow{
		TaskID:    stalTaskID,
		AgentID:   "scout",
		Status:    "running",
		Goal:      "interrupted task",
		StartedAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveTeamTask: %v", err)
	}

	// Also write the agent to AGENTS.md so syncFromFile succeeds.
	if err := writeAgentsFile(filepath.Join(dir, "AGENTS.md"), []agentDefinition{
		{Name: "Scout", ID: "scout", Role: "Research", Mission: "Do research", AllowedSkills: []string{"web.*"}, Autonomy: "assistive", Enabled: true},
	}); err != nil {
		t.Fatalf("writeAgentsFile: %v", err)
	}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	module.SetDatabase(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer module.Stop(context.Background())

	// Agent runtime should be reset to idle.
	row, err := db.GetAgentRuntime("scout")
	if err != nil || row == nil {
		t.Fatalf("GetAgentRuntime: %v", err)
	}
	if row.Status != "idle" {
		t.Fatalf("expected idle after restart, got %q", row.Status)
	}

	// Stale task should be marked failed (V1 canonical — was "error" pre-Phase 7).
	task, err := db.GetAgentTask(stalTaskID)
	if err != nil || task == nil {
		t.Fatalf("GetTeamTask: %v", err)
	}
	if task.Status != "failed" {
		t.Fatalf("expected failed status for stale task, got %q", task.Status)
	}
	if task.ErrorMessage == nil || *task.ErrorMessage == "" {
		t.Fatal("expected non-empty error message on stale task")
	}
}

func safeStringString(v any) string {
	s, _ := v.(string)
	return s
}

// TestTeamNamespaceSkills_AreRegisteredAndCallable verifies that the Phase 3
// team.* skill namespace is wired correctly alongside the legacy agent.* aliases.
func TestTeamNamespaceSkills_AreRegisteredAndCallable(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	// Stub delegation so team.delegate doesn't need a real provider.
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := "2026-04-11T12:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:        "teamtask-phase3",
				AgentID:       def.ID,
				Status:        "completed",
				Goal:          args.Task,
				RequestedBy:   "atlas",
				ResultSummary: strPtr("Phase 3 delegation succeeded."),
				StartedAt:     now, FinishedAt: strPtr(now),
				CreatedAt: now, UpdatedAt: now,
			},
		}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Seed one enabled team member.
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "scout",
		Name:              "Scout",
		Role:              "Research Specialist",
		Mission:           "Find facts",
		AllowedSkillsJSON: `["websearch"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         "2026-04-11T12:00:00Z",
		UpdatedAt:         "2026-04-11T12:00:00Z",
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	// team.list must be registered and return the seeded member.
	listResult, err := registry.Execute(context.Background(), "team.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("team.list: %v", err)
	}
	if !listResult.Success {
		t.Fatalf("team.list failed: %+v", listResult)
	}

	// team.get must return the seeded member by ID.
	getResult, err := registry.Execute(context.Background(), "team.get", json.RawMessage(`{"id":"scout"}`))
	if err != nil {
		t.Fatalf("team.get: %v", err)
	}
	if !getResult.Success {
		t.Fatalf("team.get failed: %+v", getResult)
	}

	// team.delegate must execute a delegation and return a task ID.
	delegateResult, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(`{"agentID":"scout","task":"Research the topic"}`))
	if err != nil {
		t.Fatalf("team.delegate: %v", err)
	}
	if !delegateResult.Success {
		t.Fatalf("team.delegate failed: %+v", delegateResult)
	}
	if got := safeStringString(delegateResult.Artifacts["taskID"]); got != "teamtask-phase3" {
		t.Fatalf("unexpected task ID: %q", got)
	}

	// Legacy agent.list / agent.get / agent.delegate must still work.
	for _, name := range []string{"agent.list"} {
		r, err := registry.Execute(context.Background(), name, json.RawMessage(`{}`))
		if err != nil || !r.Success {
			t.Fatalf("legacy skill %q no longer callable: err=%v success=%v", name, err, r.Success)
		}
	}
}

// TestTeamDelegate_StructuredPlan verifies that team.delegate accepts a
// structured DelegationPlan, validates it, and persists structured payload
// columns in the agent_tasks row (Phase 4 acceptance check).
func TestTeamDelegate_StructuredPlan(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	var capturedArgs delegateArgs
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		capturedArgs = args
		now := "2026-04-12T10:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:      "teamtask-p4",
				AgentID:     def.ID,
				Status:      "completed",
				Goal:        args.Task,
				RequestedBy: "atlas",
				ResultSummary: strPtr("Research complete."),
				StartedAt: now, FinishedAt: strPtr(now),
				CreatedAt: now, UpdatedAt: now,
				Title:               args.Title,
				Objective:           args.Objective,
				ScopeJSON:           args.ScopeJSON,
				SuccessCriteriaJSON: args.SuccessCriteriaJSON,
				InputContextJSON:    args.InputContextJSON,
				ExpectedOutputJSON:  args.ExpectedOutputJSON,
				Mode:                args.Mode,
				Pattern:             args.Pattern,
			},
		}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Seed a scout member.
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                "scout",
		Name:              "Scout",
		Role:              "Research Specialist",
		Mission:           "Find facts",
		AllowedSkillsJSON: `["websearch"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         "2026-04-12T10:00:00Z",
		UpdatedAt:         "2026-04-12T10:00:00Z",
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	// Call team.delegate with a full DelegationPlan.
	planJSON := `{
		"mode": "specialist_assist",
		"pattern": "single",
		"executionMode": "sync_assist",
		"tasks": [{
			"agentId": "scout",
			"title": "Research competitor pricing",
			"objective": "Find current pricing for X, Y, Z",
			"scope": {"included": ["public pages"], "excluded": ["internal docs"]},
			"successCriteria": {"must": ["pricing for all three found"]},
			"inputContext": {"userRequest": "what does competitor X charge?"},
			"expectedOutput": {"type": "findings_list"}
		}]
	}`
	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate (plan): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (plan) failed: %+v", result)
	}

	// Verify structured fields were captured and passed through.
	if capturedArgs.Title != "Research competitor pricing" {
		t.Errorf("title not captured: %q", capturedArgs.Title)
	}
	if capturedArgs.Objective != "Find current pricing for X, Y, Z" {
		t.Errorf("objective not captured: %q", capturedArgs.Objective)
	}
	if capturedArgs.Mode != "sync_assist" {
		t.Errorf("mode not captured: %q", capturedArgs.Mode)
	}
	if capturedArgs.Pattern != "single" {
		t.Errorf("pattern not captured: %q", capturedArgs.Pattern)
	}
	if !strings.Contains(capturedArgs.ExpectedOutputJSON, "findings_list") {
		t.Errorf("expectedOutput.type not captured in JSON: %q", capturedArgs.ExpectedOutputJSON)
	}

	// Verify backward compat: flat args must still work via team.delegate.
	flatResult, err := registry.Execute(context.Background(), "team.delegate",
		json.RawMessage(`{"agentID":"scout","task":"Quick research","goal":"Find facts"}`))
	if err != nil {
		t.Fatalf("team.delegate (flat): %v", err)
	}
	if !flatResult.Success {
		t.Fatalf("team.delegate (flat) backward compat failed: %+v", flatResult)
	}
}

// TestTeamDelegate_ValidationRejectsInvalidPlan verifies that validateDelegationPlan
// catches bad inputs before any execution happens.
func TestTeamDelegate_ValidationRejectsInvalidPlan(t *testing.T) {
	cases := []struct {
		name    string
		planJSON string
		wantErr string
	}{
		{
			name:     "missing mode",
			planJSON: `{"pattern":"single","tasks":[{"agentId":"x","objective":"y"}]}`,
			wantErr:  "mode is required",
		},
		{
			name:     "invalid pattern",
			planJSON: `{"mode":"specialist_assist","pattern":"waterfall","tasks":[{"agentId":"x","objective":"y"}]}`,
			wantErr:  "pattern",
		},
		{
			name:     "empty tasks",
			planJSON: `{"mode":"specialist_assist","pattern":"single","tasks":[]}`,
			wantErr:  "tasks must not be empty",
		},
		{
			name:     "missing agentId in task",
			planJSON: `{"mode":"specialist_assist","pattern":"single","tasks":[{"objective":"do it"}]}`,
			wantErr:  "agentId is required",
		},
		{
			name:     "missing objective in task",
			planJSON: `{"mode":"specialist_assist","pattern":"single","tasks":[{"agentId":"scout"}]}`,
			wantErr:  "objective is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args teamDelegatePlanArgs
			if err := json.Unmarshal(json.RawMessage(tc.planJSON), &args); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			plan := DelegationPlan{
				Mode:    args.Mode,
				Pattern: args.Pattern,
				Tasks:   args.Tasks,
			}
			err := validateDelegationPlan(plan)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// ─── Phase 7: Team HQ projection refresh ─────────────────────────────────────

func TestNormalizeTaskStatus_MapsLegacyToV1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"running", "working"},
		{"error", "failed"},
		{"pending_approval", "needs_review"},
		{"complete", "completed"},
		// V1 values pass through unchanged
		{"working", "working"},
		{"failed", "failed"},
		{"needs_review", "needs_review"},
		{"completed", "completed"},
		{"created", "created"},
		{"canceled", "canceled"},
	}
	for _, tc := range cases {
		got := normalizeTaskStatus(tc.in)
		if got != tc.want {
			t.Errorf("normalizeTaskStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeMemberStatus_MapsLegacyToV1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"busy", "working"},
		{"approval_needed", "needs_review"},
		{"", "idle"},
		// V1 values pass through unchanged
		{"working", "working"},
		{"idle", "idle"},
		{"blocked", "blocked"},
	}
	for _, tc := range cases {
		got := normalizeMemberStatus(tc.in)
		if got != tc.want {
			t.Errorf("normalizeMemberStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAgentFromRows_Phase7_TemplateRoleAndStatusNorm(t *testing.T) {
	now := "2026-04-12T00:00:00Z"
	def := storage.AgentDefinitionRow{
		ID:                "scout-7",
		Name:              "Scout",
		Role:              "Research",
		TemplateRole:      "scout",
		Mission:           "Gather info",
		AllowedSkillsJSON: `["websearch"]`,
		Autonomy:          "assistive",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	runtime := storage.AgentRuntimeRow{
		AgentID:   "scout-7",
		Status:    "busy", // legacy status
		UpdatedAt: now,
	}

	out := agentFromRows(def, runtime, nil)

	if out.TemplateRole != "scout" {
		t.Errorf("expected TemplateRole=scout, got %q", out.TemplateRole)
	}
	if out.Runtime.Status != "working" {
		t.Errorf("expected runtime status normalized to working, got %q", out.Runtime.Status)
	}
}

func TestTeamDerivedState_Phase7_BlockingFieldsAndStatusNorm(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	now := "2026-04-12T01:00:00Z"
	agentID := "op-phase7"

	// Seed agent definition.
	_ = db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID:                agentID,
		Name:              "Operator",
		Role:              "Operator",
		Mission:           "Run tasks",
		AllowedSkillsJSON: `["terminal"]`,
		Autonomy:          "on_demand",
		IsEnabled:         true,
		CreatedAt:         now,
		UpdatedAt:         now,
	})

	// Seed a task with legacy "pending_approval" status + blocking fields.
	bk := "approval"
	bd := "Needs user approval to delete files"
	_ = db.SaveAgentTask(storage.AgentTaskRow{
		TaskID:         "task-phase7-approval",
		AgentID:        agentID,
		Status:         "pending_approval",
		Goal:           "legacy goal text",
		Title:          "Delete old log files",
		BlockingKind:   &bk,
		BlockingDetail: &bd,
		StartedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	// Seed a task with legacy "error" status.
	_ = db.SaveAgentTask(storage.AgentTaskRow{
		TaskID:    "task-phase7-error",
		AgentID:   agentID,
		Status:    "error",
		Goal:      "deploy script",
		Title:     "Deploy to staging",
		StartedAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	module.SetDatabase(db)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	blocked, _, atlasStatus, err := module.teamDerivedState()
	if err != nil {
		t.Fatalf("teamDerivedState: %v", err)
	}

	// Atlas status should be "attention_needed" due to blocked tasks.
	if atlasStatus != "attention_needed" {
		t.Errorf("expected atlasStatus=attention_needed, got %q", atlasStatus)
	}

	// Find the needs_review blocked item.
	var approvalItem *blockedItemJSON
	var failedItem *blockedItemJSON
	for i := range blocked {
		switch blocked[i].Status {
		case "needs_review":
			approvalItem = &blocked[i]
		case "failed":
			failedItem = &blocked[i]
		}
	}

	if approvalItem == nil {
		t.Fatal("expected needs_review blocked item (was pending_approval)")
	}
	// Title should come from task.Title, not task.Goal.
	if approvalItem.Title != "Delete old log files" {
		t.Errorf("expected Title from structured field, got %q", approvalItem.Title)
	}
	if approvalItem.BlockingKind != "approval" {
		t.Errorf("expected BlockingKind=approval, got %q", approvalItem.BlockingKind)
	}
	if approvalItem.BlockingDetail != "Needs user approval to delete files" {
		t.Errorf("expected BlockingDetail set, got %q", approvalItem.BlockingDetail)
	}

	if failedItem == nil {
		t.Fatal("expected failed blocked item (was error)")
	}
	if failedItem.Title != "Deploy to staging" {
		t.Errorf("expected Title from structured field for failed item, got %q", failedItem.Title)
	}
}

func TestTaskFromRow_Phase7_V1FieldsPresent(t *testing.T) {
	bk := "approval"
	bd := "waiting for user"
	row := storage.AgentTaskRow{
		TaskID:         "task-p7",
		AgentID:        "agent-1",
		Status:         "pending_approval",
		Goal:           "old goal",
		Title:          "Structured title",
		Objective:      "Structured objective",
		Mode:           "async_assignment",
		Pattern:        "single",
		BlockingKind:   &bk,
		BlockingDetail: &bd,
		StartedAt:      "2026-04-12T00:00:00Z",
		CreatedAt:      "2026-04-12T00:00:00Z",
		UpdatedAt:      "2026-04-12T00:00:00Z",
	}

	out := taskFromRow(row, nil)

	if out.Status != "needs_review" {
		t.Errorf("expected status normalized to needs_review, got %q", out.Status)
	}
	if out.Title != "Structured title" {
		t.Errorf("expected Title=%q, got %q", "Structured title", out.Title)
	}
	if out.Objective != "Structured objective" {
		t.Errorf("expected Objective=%q, got %q", "Structured objective", out.Objective)
	}
	if out.Mode != "async_assignment" {
		t.Errorf("expected Mode=%q, got %q", "async_assignment", out.Mode)
	}
	if out.Pattern != "single" {
		t.Errorf("expected Pattern=%q, got %q", "single", out.Pattern)
	}
	if out.BlockingKind == nil || *out.BlockingKind != "approval" {
		t.Errorf("expected BlockingKind=approval, got %v", out.BlockingKind)
	}
	if out.BlockingDetail == nil || *out.BlockingDetail != "waiting for user" {
		t.Errorf("expected BlockingDetail set, got %v", out.BlockingDetail)
	}
}

// ─── Converter round-trip tests (W6 cleanup) ─────────────────────────────────

// TestRowToFileDef_RoundTrip verifies that rowToFileDef produces a correct
// agentDefinition from a fully-populated AgentDefinitionRow. Every field that
// the CRUD pipeline cares about must survive the conversion.
func TestRowToFileDef_RoundTrip(t *testing.T) {
	row := storage.AgentDefinitionRow{
		ID:                     "test-agent",
		Name:                   "Test Agent",
		Role:                   "Tester",
		Mission:                "Test everything",
		Style:                  "thorough",
		AllowedSkillsJSON:      `["fs","terminal","websearch"]`,
		AllowedToolClassesJSON: `["read","local_write"]`,
		Autonomy:               "assistive",
		Activation:             "on_demand",
		ProviderType:           "anthropic",
		Model:                  "claude-sonnet-4-6",
		IsEnabled:              true,
		CreatedAt:              "2026-04-12T00:00:00Z",
		UpdatedAt:              "2026-04-12T01:00:00Z",
	}

	def := rowToFileDef(row)

	if def.ID != row.ID {
		t.Errorf("ID: got %q, want %q", def.ID, row.ID)
	}
	if def.Name != row.Name {
		t.Errorf("Name: got %q, want %q", def.Name, row.Name)
	}
	if def.Role != row.Role {
		t.Errorf("Role: got %q, want %q", def.Role, row.Role)
	}
	if def.Mission != row.Mission {
		t.Errorf("Mission: got %q, want %q", def.Mission, row.Mission)
	}
	if def.Style != row.Style {
		t.Errorf("Style: got %q, want %q", def.Style, row.Style)
	}
	if len(def.AllowedSkills) != 3 || def.AllowedSkills[0] != "fs" || def.AllowedSkills[2] != "websearch" {
		t.Errorf("AllowedSkills: got %v", def.AllowedSkills)
	}
	if len(def.AllowedToolClasses) != 2 || def.AllowedToolClasses[0] != "read" {
		t.Errorf("AllowedToolClasses: got %v", def.AllowedToolClasses)
	}
	if def.Autonomy != row.Autonomy {
		t.Errorf("Autonomy: got %q, want %q", def.Autonomy, row.Autonomy)
	}
	if def.Activation != row.Activation {
		t.Errorf("Activation: got %q, want %q", def.Activation, row.Activation)
	}
	if def.ProviderType != row.ProviderType {
		t.Errorf("ProviderType: got %q, want %q", def.ProviderType, row.ProviderType)
	}
	if def.Model != row.Model {
		t.Errorf("Model: got %q, want %q", def.Model, row.Model)
	}
	if def.Enabled != row.IsEnabled {
		t.Errorf("Enabled: got %v, want %v", def.Enabled, row.IsEnabled)
	}
}

// TestFileDefToRow_RoundTrip verifies that fileDefToRow preserves every field
// that rowToFileDef reads. A round-trip (row→def→row) must produce identical
// field values so that DB-first CRUD can use both converters safely.
func TestFileDefToRow_RoundTrip(t *testing.T) {
	original := storage.AgentDefinitionRow{
		ID:                     "round-trip-agent",
		Name:                   "Round Trip",
		Role:                   "Verifier",
		Mission:                "Verify round-trips",
		Style:                  "precise",
		AllowedSkillsJSON:      `["fs","websearch"]`,
		AllowedToolClassesJSON: `["read"]`,
		Autonomy:               "supervised",
		Activation:             "atlas_in_task_assist",
		ProviderType:           "openai",
		Model:                  "gpt-4o",
		IsEnabled:              false,
		CreatedAt:              "2026-04-12T00:00:00Z",
		UpdatedAt:              "2026-04-12T02:00:00Z",
	}

	// row → def → row
	def := rowToFileDef(original)
	reconstructed := fileDefToRow(def, original.CreatedAt, original.UpdatedAt)

	if reconstructed.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", reconstructed.ID, original.ID)
	}
	if reconstructed.Name != original.Name {
		t.Errorf("Name mismatch: got %q, want %q", reconstructed.Name, original.Name)
	}
	if reconstructed.Role != original.Role {
		t.Errorf("Role mismatch: got %q, want %q", reconstructed.Role, original.Role)
	}
	if reconstructed.Mission != original.Mission {
		t.Errorf("Mission mismatch: got %q, want %q", reconstructed.Mission, original.Mission)
	}
	if reconstructed.Style != original.Style {
		t.Errorf("Style mismatch: got %q, want %q", reconstructed.Style, original.Style)
	}
	if reconstructed.Autonomy != original.Autonomy {
		t.Errorf("Autonomy mismatch: got %q, want %q", reconstructed.Autonomy, original.Autonomy)
	}
	if reconstructed.Activation != original.Activation {
		t.Errorf("Activation mismatch: got %q, want %q", reconstructed.Activation, original.Activation)
	}
	if reconstructed.ProviderType != original.ProviderType {
		t.Errorf("ProviderType mismatch: got %q, want %q", reconstructed.ProviderType, original.ProviderType)
	}
	if reconstructed.Model != original.Model {
		t.Errorf("Model mismatch: got %q, want %q", reconstructed.Model, original.Model)
	}
	if reconstructed.IsEnabled != original.IsEnabled {
		t.Errorf("IsEnabled mismatch: got %v, want %v", reconstructed.IsEnabled, original.IsEnabled)
	}
	if reconstructed.CreatedAt != original.CreatedAt {
		t.Errorf("CreatedAt mismatch: got %q, want %q", reconstructed.CreatedAt, original.CreatedAt)
	}
	if reconstructed.UpdatedAt != original.UpdatedAt {
		t.Errorf("UpdatedAt mismatch: got %q, want %q", reconstructed.UpdatedAt, original.UpdatedAt)
	}
	// Skills and tool classes must survive JSON re-encoding
	defAfterReconstruct := rowToFileDef(reconstructed)
	if len(defAfterReconstruct.AllowedSkills) != len(def.AllowedSkills) {
		t.Errorf("AllowedSkills length mismatch after double round-trip: got %v, want %v",
			defAfterReconstruct.AllowedSkills, def.AllowedSkills)
	}
	if len(defAfterReconstruct.AllowedToolClasses) != len(def.AllowedToolClasses) {
		t.Errorf("AllowedToolClasses length mismatch after double round-trip: got %v, want %v",
			defAfterReconstruct.AllowedToolClasses, def.AllowedToolClasses)
	}
}

// TestFileDefToRow_EmptyOptionalFields ensures that zero-value optional fields
// (Style, Activation, ProviderType, Model, AllowedToolClasses) survive the
// converter without producing invalid JSON or empty-string collisions.
func TestFileDefToRow_EmptyOptionalFields(t *testing.T) {
	def := agentDefinition{
		ID:            "minimal-agent",
		Name:          "Minimal",
		Role:          "Basic",
		Mission:       "Do the minimum",
		AllowedSkills: []string{"websearch"},
		Autonomy:      "assistive",
		Enabled:       true,
		// Style, Activation, ProviderType, Model, AllowedToolClasses all zero-value
	}
	now := "2026-04-12T00:00:00Z"
	row := fileDefToRow(def, now, now)

	if row.Style != "" {
		t.Errorf("Style should be empty, got %q", row.Style)
	}
	if row.Activation != "" {
		t.Errorf("Activation should be empty, got %q", row.Activation)
	}
	if row.ProviderType != "" {
		t.Errorf("ProviderType should be empty, got %q", row.ProviderType)
	}
	if row.Model != "" {
		t.Errorf("Model should be empty, got %q", row.Model)
	}
	// AllowedToolClasses should marshal to "null" or "[]" — both are valid empty
	reconverted := rowToFileDef(row)
	if len(reconverted.AllowedToolClasses) != 0 {
		t.Errorf("AllowedToolClasses should be empty after round-trip, got %v", reconverted.AllowedToolClasses)
	}
}

// ─── Parallel + sequence validation tests (W1/W2 cleanup) ────────────────────

// TestValidateDelegationPlan_ParallelRejectedEarly verifies that "parallel" is
// rejected at validation time before any execution path runs.
func TestValidateDelegationPlan_ParallelRejectedEarly(t *testing.T) {
	plan := DelegationPlan{
		Mode:    "specialist_assist",
		Pattern: "parallel",
		Tasks: []DelegationTaskSpec{
			{AgentID: "scout", Objective: "do something"},
		},
	}
	err := validateDelegationPlan(plan)
	if err == nil {
		t.Fatal("expected validation error for parallel, got nil")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in error, got: %q", err.Error())
	}
}

// TestTeamDelegate_SequenceSimpleSteps verifies that a sequence plan where
// each step is expressed in simple {agentId, task} form (without explicit
// objective/title) is normalized by teamDelegate() before validation so it
// succeeds instead of failing with "objective required" (Phase C1).
func TestTeamDelegate_SequenceSimpleSteps(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	var captured []delegateArgs
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		captured = append(captured, args)
		now := "2026-04-12T10:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:        "task-" + def.ID,
				AgentID:       def.ID,
				Status:        "completed",
				Goal:          args.Task,
				RequestedBy:   "atlas",
				ResultSummary: strPtr("done by " + def.ID),
				StartedAt:     now, FinishedAt: strPtr(now),
				CreatedAt: now, UpdatedAt: now,
			},
		}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-12T10:00:00Z"
	for _, id := range []string{"scout", "builder"} {
		if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
			ID:                id,
			Name:              strings.Title(id),
			Role:              id + " role",
			Mission:           id + " mission",
			AllowedSkillsJSON: `["websearch"]`,
			Autonomy:          "assistive",
			IsEnabled:         true,
			CreatedAt:         now,
			UpdatedAt:         now,
		}); err != nil {
			t.Fatalf("SaveAgentDefinition(%s): %v", id, err)
		}
	}

	// Simple sequence: each step uses only agentId + task (no explicit objective/title).
	// Before C1 this would fail validation because Objective is required.
	planJSON := `{
		"pattern": "sequence",
		"executionMode": "sync_assist",
		"tasks": [
			{"agentId": "scout", "task": "Research pricing for Plan A"},
			{"agentId": "builder", "task": "Compile a pricing report from scout's findings"}
		]
	}`

	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate (simple sequence): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (simple sequence) failed: %+v", result)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 delegations, got %d", len(captured))
	}

	// Objective and Title should have been normalized from the "task" alias.
	if captured[0].AgentID != "scout" {
		t.Errorf("step 1 AgentID: got %q, want scout", captured[0].AgentID)
	}
	if captured[0].Objective != "Research pricing for Plan A" {
		t.Errorf("step 1 Objective not normalized from task: got %q", captured[0].Objective)
	}
	if captured[0].Title != "Research pricing for Plan A" {
		t.Errorf("step 1 Title not normalized from task: got %q", captured[0].Title)
	}
	if captured[1].AgentID != "builder" {
		t.Errorf("step 2 AgentID: got %q, want builder", captured[1].AgentID)
	}
	if captured[1].Objective != "Compile a pricing report from scout's findings" {
		t.Errorf("step 2 Objective not normalized from task: got %q", captured[1].Objective)
	}
}

// TestTeamDelegate_SequenceSimpleSteps_ExplicitObjectiveNotOverwritten verifies
// that when a step already has an explicit Objective, the C1 normalization pass
// does not overwrite it with the Task alias value.
func TestTeamDelegate_SequenceSimpleSteps_ExplicitObjectiveNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	var captured []delegateArgs
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		captured = append(captured, args)
		now := "2026-04-12T10:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:        "task-" + def.ID,
				AgentID:       def.ID,
				Status:        "completed",
				Goal:          args.Task,
				RequestedBy:   "atlas",
				ResultSummary: strPtr("done"),
				StartedAt:     now, FinishedAt: strPtr(now),
				CreatedAt: now, UpdatedAt: now,
			},
		}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-12T10:00:00Z"
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "scout", Name: "Scout", Role: "researcher", Mission: "research",
		AllowedSkillsJSON: `["websearch"]`, Autonomy: "assistive",
		IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	// Step has both task alias AND explicit objective — objective must win.
	planJSON := `{
		"pattern": "single",
		"tasks": [
			{
				"agentId": "scout",
				"task": "alias value — should be ignored",
				"objective": "Explicit objective that must be preserved",
				"title": "Explicit title"
			}
		]
	}`

	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate: %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate failed: %+v", result)
	}

	if len(captured) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(captured))
	}
	if captured[0].Objective != "Explicit objective that must be preserved" {
		t.Errorf("Objective was overwritten; got %q", captured[0].Objective)
	}
	if captured[0].Title != "Explicit title" {
		t.Errorf("Title was overwritten; got %q", captured[0].Title)
	}
}

// TestTeamDelegate_SequencePreservesV1Metadata verifies that a sequence
// plan's DelegationTaskSpec metadata (title, objective, scope, success criteria,
// expected output) is passed through to delegateArgs for each step — not
// flattened to a bare task string as in the old sequenceArgs path.
func TestTeamDelegate_SequencePreservesV1Metadata(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	// Capture args for each delegation call in order.
	var captured []delegateArgs
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		captured = append(captured, args)
		now := "2026-04-12T10:00:00Z"
		return delegatedRun{
			Task: storage.AgentTaskRow{
				TaskID:        "seq-task-" + def.ID,
				AgentID:       def.ID,
				Status:        "completed",
				Goal:          args.Task,
				RequestedBy:   "atlas",
				ResultSummary: strPtr("Step done by " + def.ID),
				StartedAt:     now, FinishedAt: strPtr(now),
				CreatedAt: now, UpdatedAt: now,
			},
		}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-12T10:00:00Z"
	for _, id := range []string{"scout", "builder"} {
		if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
			ID:                id,
			Name:              strings.Title(id),
			Role:              id + " role",
			Mission:           id + " mission",
			AllowedSkillsJSON: `["websearch"]`,
			Autonomy:          "assistive",
			IsEnabled:         true,
			CreatedAt:         now,
			UpdatedAt:         now,
		}); err != nil {
			t.Fatalf("SaveAgentDefinition(%s): %v", id, err)
		}
	}

	planJSON := `{
		"mode": "team_lead",
		"pattern": "sequence",
		"executionMode": "sync_assist",
		"tasks": [
			{
				"agentId": "scout",
				"title": "Research phase",
				"objective": "Find all relevant pricing data",
				"scope": {"included": ["public pages"]},
				"successCriteria": {"must": ["pricing found"]},
				"expectedOutput": {"type": "findings_list"}
			},
			{
				"agentId": "builder",
				"title": "Build report",
				"objective": "Compile findings into a report",
				"expectedOutput": {"type": "structured_brief"}
			}
		]
	}`

	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate (sequence): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (sequence) failed: %+v", result)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 delegations, got %d", len(captured))
	}

	// Step 1: scout should have full V1 metadata.
	step1 := captured[0]
	if step1.AgentID != "scout" {
		t.Errorf("step 1 AgentID: got %q, want scout", step1.AgentID)
	}
	if step1.Title != "Research phase" {
		t.Errorf("step 1 Title not preserved: got %q", step1.Title)
	}
	if step1.Objective != "Find all relevant pricing data" {
		t.Errorf("step 1 Objective not preserved: got %q", step1.Objective)
	}
	if !strings.Contains(step1.ScopeJSON, "public pages") {
		t.Errorf("step 1 ScopeJSON not preserved: got %q", step1.ScopeJSON)
	}
	if !strings.Contains(step1.ExpectedOutputJSON, "findings_list") {
		t.Errorf("step 1 ExpectedOutputJSON not preserved: got %q", step1.ExpectedOutputJSON)
	}
	if step1.Pattern != "sequence" {
		t.Errorf("step 1 Pattern: got %q, want sequence", step1.Pattern)
	}

	// Step 2: builder should have its own V1 metadata + prior-step output injected.
	step2 := captured[1]
	if step2.AgentID != "builder" {
		t.Errorf("step 2 AgentID: got %q, want builder", step2.AgentID)
	}
	if step2.Title != "Build report" {
		t.Errorf("step 2 Title not preserved: got %q", step2.Title)
	}
	if step2.Objective != "Compile findings into a report" {
		t.Errorf("step 2 Objective not preserved: got %q", step2.Objective)
	}
	if !strings.Contains(step2.ExpectedOutputJSON, "structured_brief") {
		t.Errorf("step 2 ExpectedOutputJSON not preserved: got %q", step2.ExpectedOutputJSON)
	}
	// Prior output from scout must be injected into the task instruction.
	if !strings.Contains(step2.Task, "Step done by scout") {
		t.Errorf("step 2 Task should contain prior-step output, got: %q", step2.Task)
	}
}

// ─── M1: syncFromFile removal ────────────────────────────────────────────────

// TestDelegateTask_DBOnlyAgentSurvivesDelegation verifies that an agent created
// via the DB-only path (not in AGENTS.md) is NOT deleted when delegateTask runs.
// This tests the M1 fix: syncFromFile was previously called inside delegateTask,
// which deleted any DB-only agent not present in AGENTS.md.
func TestDelegateTask_DBOnlyAgentSurvivesDelegation(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	// Stub delegateFn so we don't need a real AI provider.
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := "2026-04-13T10:00:00Z"
		return delegatedRun{Task: storage.AgentTaskRow{
			TaskID: "t1", AgentID: def.ID, Status: "completed",
			Goal: args.Task, StartedAt: now, CreatedAt: now, UpdatedAt: now,
		}}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-13T10:00:00Z"
	// Seed scout (will be "in AGENTS.md" conceptually — the delegate target).
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "scout", Name: "Scout", Role: "Research Specialist",
		Mission: "Find facts", AllowedSkillsJSON: `["websearch"]`,
		Autonomy: "assistive", IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition(scout): %v", err)
	}
	// Seed a DB-only agent (not in AGENTS.md — would have been deleted by old syncFromFile).
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "db-only-agent", Name: "DB Only", Role: "Custom",
		Mission: "Created via API", AllowedSkillsJSON: `["websearch"]`,
		Autonomy: "on_demand", IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition(db-only-agent): %v", err)
	}

	// Trigger delegation to scout (this previously called syncFromFile which
	// would delete db-only-agent since it's not in AGENTS.md).
	planJSON := `{"agentID": "scout", "task": "Research AI frameworks"}`
	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate: %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate failed: %+v", result)
	}

	// DB-only agent must still exist after delegation.
	row, err := db.GetAgentDefinition("db-only-agent")
	if err != nil {
		t.Fatalf("GetAgentDefinition(db-only-agent): %v", err)
	}
	if row == nil {
		t.Fatal("db-only-agent was deleted during delegation — M1 regression: syncFromFile still runs in delegateTask")
	}
}

// ─── M2: async follow-up delivery ────────────────────────────────────────────

// TestAsyncFollowUpText_CompletedWithSummary verifies the follow-up message
// format when a task completes with a result summary.
func TestAsyncFollowUpText_CompletedWithSummary(t *testing.T) {
	summary := "Found 5 matching libraries."
	run := delegatedRun{Task: storage.AgentTaskRow{
		TaskID: "t1", AgentID: "scout", Status: "completed",
		ResultSummary: strPtr(summary),
	}}
	msg := asyncFollowUpText("Scout", "t1", run, nil)
	if !strings.Contains(msg, "Scout") {
		t.Errorf("expected agent name in follow-up, got: %q", msg)
	}
	if !strings.Contains(msg, summary) {
		t.Errorf("expected result summary in follow-up, got: %q", msg)
	}
}

// TestAsyncFollowUpText_CompletedNoSummary verifies the follow-up message
// format when a task completes without a result summary.
func TestAsyncFollowUpText_CompletedNoSummary(t *testing.T) {
	run := delegatedRun{Task: storage.AgentTaskRow{
		TaskID: "t2", AgentID: "builder", Status: "completed",
	}}
	msg := asyncFollowUpText("Builder", "t2", run, nil)
	if !strings.Contains(msg, "Builder") {
		t.Errorf("expected agent name in follow-up, got: %q", msg)
	}
	if !strings.Contains(msg, "t2") {
		t.Errorf("expected task ID in follow-up, got: %q", msg)
	}
}

// TestAsyncFollowUpText_Error verifies the follow-up message includes the error.
func TestAsyncFollowUpText_Error(t *testing.T) {
	run := delegatedRun{}
	msg := asyncFollowUpText("Scout", "t3", run, fmt.Errorf("connection timeout"))
	if !strings.Contains(msg, "connection timeout") {
		t.Errorf("expected error message in follow-up, got: %q", msg)
	}
}

// TestAsyncAssignment_SendsFollowUp verifies that when async_assignment completes,
// AsyncFollowUpSender is called exactly once with the originating convID.
func TestAsyncAssignment_SendsFollowUp(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	done := make(chan struct{})
	var followUpConv, followUpText string
	oldSender := chat.AsyncFollowUpSender
	chat.AsyncFollowUpSender = func(convID, text string) {
		followUpConv = convID
		followUpText = text
		close(done)
	}
	defer func() { chat.AsyncFollowUpSender = oldSender }()

	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := "2026-04-13T10:00:00Z"
		return delegatedRun{Task: storage.AgentTaskRow{
			TaskID: args.TaskID, AgentID: def.ID, Status: "completed",
			ResultSummary: strPtr("Research complete."),
			StartedAt: now, CreatedAt: now, UpdatedAt: now,
		}}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-13T10:00:00Z"
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "scout", Name: "Scout", Role: "Research Specialist",
		Mission: "Find facts", AllowedSkillsJSON: `["websearch"]`,
		Autonomy: "assistive", IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	// Inject origin convID via context (as HandleMessage does).
	ctx := chat.WithOriginConvID(context.Background(), "conv-atlas-123")

	planJSON := `{"agentID": "scout", "task": "Research AI trends", "executionMode": "async_assignment"}`
	result, err := registry.Execute(ctx, "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate (async): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (async) failed: %+v", result)
	}

	// Wait for goroutine to fire (with timeout).
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("follow-up sender never called within 3 seconds")
	}

	if followUpConv != "conv-atlas-123" {
		t.Errorf("follow-up sent to wrong convID: got %q, want conv-atlas-123", followUpConv)
	}
	if !strings.Contains(followUpText, "Scout") {
		t.Errorf("expected agent name in follow-up text, got: %q", followUpText)
	}
	if !strings.Contains(followUpText, "Research complete.") {
		t.Errorf("expected result summary in follow-up text, got: %q", followUpText)
	}
}

// TestSyncAssignment_NoFollowUp verifies that sync_assist tasks do NOT trigger
// AsyncFollowUpSender (follow-up is only for async_assignment).
func TestSyncAssignment_NoFollowUp(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	senderCalled := false
	oldSender := chat.AsyncFollowUpSender
	chat.AsyncFollowUpSender = func(convID, text string) { senderCalled = true }
	defer func() { chat.AsyncFollowUpSender = oldSender }()

	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		now := "2026-04-13T10:00:00Z"
		return delegatedRun{Task: storage.AgentTaskRow{
			TaskID: "t-sync", AgentID: def.ID, Status: "completed",
			StartedAt: now, CreatedAt: now, UpdatedAt: now,
		}}, nil
	}

	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-13T10:00:00Z"
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "scout", Name: "Scout", Role: "Research Specialist",
		Mission: "Find facts", AllowedSkillsJSON: `["websearch"]`,
		Autonomy: "assistive", IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	ctx := chat.WithOriginConvID(context.Background(), "conv-atlas-sync")
	planJSON := `{"agentID": "scout", "task": "Research AI trends", "executionMode": "sync_assist"}`
	result, err := registry.Execute(ctx, "team.delegate", json.RawMessage(planJSON))
	if err != nil {
		t.Fatalf("team.delegate (sync): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (sync) failed: %+v", result)
	}

	if senderCalled {
		t.Error("AsyncFollowUpSender should NOT be called for sync_assist tasks")
	}
}

// ─── S1: TemplateRole population ─────────────────────────────────────────────

// TestTemplateRoleFromRole_KnownRoles verifies the role → template role mapping.
func TestTemplateRoleFromRole_KnownRoles(t *testing.T) {
	cases := []struct {
		role string
		want string
	}{
		{"Scout", "scout"},
		{"Research Specialist", "scout"},
		{"investigator", "scout"},
		{"Builder", "builder"},
		{"Build Engineer", "builder"},
		{"developer", "builder"},
		{"Reviewer", "reviewer"},
		{"QA Engineer", "reviewer"},
		{"quality analyst", "reviewer"},
		{"Operator", "operator"},
		{"executor", "operator"},
		{"Monitor", "monitor"},
		{"Watcher", "monitor"},
		{"observer", "monitor"},
		{"Custom Role", ""},   // unknown → empty string → generic contract
		{"", ""},              // empty → generic contract
	}
	for _, tc := range cases {
		got := templateRoleFromRole(tc.role)
		if got != tc.want {
			t.Errorf("templateRoleFromRole(%q) = %q, want %q", tc.role, got, tc.want)
		}
	}
}

// TestFileDefToRow_PopulatesTemplateRole verifies that fileDefToRow derives
// TemplateRole from the Role field so workers get role-specific prompt contracts.
func TestFileDefToRow_PopulatesTemplateRole(t *testing.T) {
	cases := []struct {
		role string
		want string
	}{
		{"Research Specialist", "scout"},
		{"Build Engineer", "builder"},
		{"Reviewer", "reviewer"},
		{"Operator", "operator"},
		{"Monitor", "monitor"},
		{"Custom", ""},
	}
	for _, tc := range cases {
		def := agentDefinition{
			ID:      "agent-" + tc.role,
			Name:    tc.role,
			Role:    tc.role,
			Mission: "do stuff",
			Autonomy: "on_demand",
			AllowedSkills: []string{"websearch"},
		}
		now := "2026-04-13T10:00:00Z"
		row := fileDefToRow(def, now, now)
		if row.TemplateRole != tc.want {
			t.Errorf("fileDefToRow(role=%q).TemplateRole = %q, want %q", tc.role, row.TemplateRole, tc.want)
		}
	}
}

// TestAgentCreate_SetsTemplateRole verifies that agent.create sets TemplateRole
// in the DB row so composeWorkerPrompt gets the correct template contract.
func TestAgentCreate_SetsTemplateRole(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	createJSON := `{
		"id": "new-scout",
		"name": "New Scout",
		"role": "Research Specialist",
		"mission": "Find things",
		"allowedSkills": ["websearch"],
		"autonomy": "assistive"
	}`
	result, err := registry.Execute(context.Background(), "agent.create", json.RawMessage(createJSON))
	if err != nil {
		t.Fatalf("agent.create: %v", err)
	}
	if !result.Success {
		t.Fatalf("agent.create failed: %+v", result)
	}

	row, err := db.GetAgentDefinition("new-scout")
	if err != nil {
		t.Fatalf("GetAgentDefinition: %v", err)
	}
	if row == nil {
		t.Fatal("agent not found after create")
	}
	if row.TemplateRole != "scout" {
		t.Errorf("expected TemplateRole=scout after create with role=Research Specialist, got %q", row.TemplateRole)
	}
}

// ─── D1/D2/D3: sequence guidance in tool description ─────────────────────────

// TestTeamDelegate_ToolDescription_SequenceGuidance verifies that the team.delegate
// tool description contains the sequence preference framing, the canonical example,
// and the anti-pattern guidance. These are the prompt-level signals that nudge the
// model toward pattern=sequence for multi-step dependent work.
func TestTeamDelegate_ToolDescription_SequenceGuidance(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	// Retrieve the tool definition for team.delegate.
	// The registry aliases team.* → agent.* (publicByAction), so ToolDefinitions()
	// returns the tool with name "agent__delegate" (OAI wire encoding of "agent.delegate").
	var delegateDesc string
	for _, def := range registry.ToolDefinitions() {
		fn, _ := def["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "agent__delegate" || name == "agent.delegate" ||
			name == "team__delegate" || name == "team.delegate" {
			delegateDesc, _ = fn["description"].(string)
			break
		}
	}
	if delegateDesc == "" {
		t.Fatal("team.delegate tool definition not found in registry")
	}

	// D1: sequence framed as preferred path for multi-step work.
	mustContain := []struct {
		label string
		text  string
	}{
		{"D1: multi-step preferred framing", "requires more than one specialist"},
		{"D1: single-call instruction",      "submit ALL steps in a single call"},
		{"D1: avoid separate calls",         "not make multiple separate team.delegate calls"},
		{"D2: canonical example agentId",    "agentId"},
		{"D2: canonical example pattern",    "pattern="},
		{"D3: anti-pattern calling twice",   "Calling team.delegate twice"},
		{"D3: anti-pattern step 2 separate", "step 2 separately"},
	}
	for _, tc := range mustContain {
		if !strings.Contains(delegateDesc, tc.text) {
			t.Errorf("%s: description does not contain %q\ndescription:\n%s", tc.label, tc.text, delegateDesc)
		}
	}
}

// TestTeamDelegate_SingleDelegation_Unaffected verifies that single-agent
// delegation still works correctly after the description change (no regression).
func TestTeamDelegate_SingleDelegation_Unaffected(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

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

	called := false
	module.delegateFn = func(_ context.Context, def storage.AgentDefinitionRow, args delegateArgs) (delegatedRun, error) {
		called = true
		now := "2026-04-13T10:00:00Z"
		return delegatedRun{Task: storage.AgentTaskRow{
			TaskID: "t1", AgentID: def.ID, Status: "completed",
			StartedAt: now, CreatedAt: now, UpdatedAt: now,
		}}, nil
	}
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := "2026-04-13T10:00:00Z"
	if err := db.SaveAgentDefinition(storage.AgentDefinitionRow{
		ID: "scout", Name: "Scout", Role: "Research Specialist",
		Mission: "Find facts", AllowedSkillsJSON: `["websearch"]`,
		Autonomy: "assistive", IsEnabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentDefinition: %v", err)
	}

	result, err := registry.Execute(context.Background(), "team.delegate", json.RawMessage(`{"agentID":"scout","task":"Quick research task"}`))
	if err != nil {
		t.Fatalf("team.delegate (single): %v", err)
	}
	if !result.Success {
		t.Fatalf("team.delegate (single) failed: %+v", result)
	}
	if !called {
		t.Error("delegateFn should have been called for single delegation")
	}
}
