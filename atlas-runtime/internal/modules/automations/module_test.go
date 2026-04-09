package automations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type stubAgentRuntime struct {
	mu       sync.Mutex
	response chat.MessageResponse
	err      error
	lastReq  chat.MessageRequest
}

func (s *stubAgentRuntime) HandleMessage(_ context.Context, req chat.MessageRequest) (chat.MessageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReq = req
	return s.response, s.err
}

func (s *stubAgentRuntime) Resume(string, bool) {}

func (s *stubAgentRuntime) LastRequest() chat.MessageRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastReq
}

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

type stubDelivery struct {
	mu     sync.Mutex
	called bool
	dest   comms.AutomationDestination
	text   string
	err    error
	ctxErr error
}

func (s *stubDelivery) SendAutomationResult(ctx context.Context, dest comms.AutomationDestination, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = true
	s.dest = dest
	s.text = text
	s.ctxErr = ctx.Err()
	return s.err
}

func (s *stubDelivery) Snapshot() (bool, comms.AutomationDestination, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called, s.dest, s.text, s.ctxErr
}

func seedCommSession(t *testing.T, db *storage.DB, platformName, channelID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := db.UpsertCommSession(storage.CommSessionRow{
		Platform:             platformName,
		ChannelID:            channelID,
		ThreadID:             "",
		ActiveConversationID: "conv-" + channelID,
		CreatedAt:            now,
		UpdatedAt:            now,
	}); err != nil {
		t.Fatalf("UpsertCommSession: %v", err)
	}
}

func TestModule_RunAutomationCreatesRunAndRoutesPrompt(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Nightly Digest",
		Emoji:       "⚡",
		Prompt:      "Summarize today",
		ScheduleRaw: "every day at 9am",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "done"
	delivery := &stubDelivery{}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetDeliveryService(delivery)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/automations/"+items[0].ID+"/run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := db.ListGremlinRuns(items[0].ID, 10)
		if err != nil {
			t.Fatalf("ListGremlinRuns: %v", err)
		}
		if len(runs) > 0 && runs[0].Status == "completed" {
			lastReq := stub.LastRequest()
			if !strings.Contains(lastReq.Message, "Summarize today") {
				t.Fatalf("unexpected prompt: %+v", lastReq)
			}
			called, _, _, _ := delivery.Snapshot()
			if called {
				t.Fatalf("did not expect delivery without destination")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for automation run completion")
}

func TestModule_RunAutomationDeliversToDestination(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Notify Me",
		Emoji:       "⚡",
		Prompt:      "Share summary",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Automation output"
	delivery := &stubDelivery{}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetDeliveryService(delivery)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/automations/"+items[0].ID+"/run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		called, dest, text, ctxErr := delivery.Snapshot()
		if called {
			if dest.Platform != "telegram" || dest.ChannelID != "123" {
				t.Fatalf("unexpected delivery destination: %+v", dest)
			}
			lastReq := stub.LastRequest()
			if !strings.Contains(lastReq.Message, "Do not mention delivery limitations") {
				t.Fatalf("expected delivery guardrail prompt, got: %q", lastReq.Message)
			}
			if text != "Automation output" {
				t.Fatalf("unexpected delivery text: %q", text)
			}
			if ctxErr != nil {
				t.Fatalf("expected delivery context to remain active, got %v", ctxErr)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for automation delivery")
}

func TestModule_RunAutomationPersistsDeliveryFailureSeparately(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Notify Failure",
		Emoji:       "⚡",
		Prompt:      "Share summary",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Generated output"
	delivery := &stubDelivery{err: errors.New("telegram unavailable")}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetDeliveryService(delivery)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result := module.executeAutomationRun(context.Background(), items[0], "run-delivery-fail", "conv-delivery-fail")
	if result.Status != "completed" {
		t.Fatalf("generation should remain completed, got %+v", result)
	}
	runs, err := db.ListGremlinRuns(items[0].ID, 10)
	if err != nil {
		t.Fatalf("ListGremlinRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("direct execute without SaveGremlinRun should not create rows, got %+v", runs)
	}

	full, err := module.runAutomationSync(context.Background(), items[0].ID, "agent")
	if err != nil {
		t.Fatalf("runAutomationSync: %v", err)
	}
	if full.Status != "completed" {
		t.Fatalf("generation should remain completed, got %+v", full)
	}
	runs, err = db.ListGremlinRuns(items[0].ID, 10)
	if err != nil {
		t.Fatalf("ListGremlinRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %+v", runs)
	}
	run := runs[0]
	if run.Status != "completed" || run.ExecutionStatus != "completed" {
		t.Fatalf("expected completed execution status, got %+v", run)
	}
	if run.Output == nil || *run.Output != "Generated output" {
		t.Fatalf("expected generated output to be preserved, got %+v", run.Output)
	}
	if run.ErrorMessage != nil {
		t.Fatalf("generation error should be empty, got %q", *run.ErrorMessage)
	}
	if run.DeliveryStatus != "failed" {
		t.Fatalf("expected failed delivery status, got %+v", run)
	}
	if run.DeliveryError == nil || !strings.Contains(*run.DeliveryError, "telegram unavailable") {
		t.Fatalf("expected delivery error, got %+v", run.DeliveryError)
	}
}

func TestModule_RunNowForAgentUsesModuleRunner(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Agent Briefing",
		Emoji:       "⚡",
		Prompt:      "Daily briefing with weather and calendar.",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Agent automation output"
	delivery := &stubDelivery{}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetDeliveryService(delivery)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	out, err := module.RunNowForAgent(context.Background(), items[0].ID)
	if err != nil {
		t.Fatalf("RunNowForAgent: %v", err)
	}
	if !strings.Contains(out, "Automation \"Agent Briefing\" ran successfully") {
		t.Fatalf("unexpected agent output: %q", out)
	}
	if !strings.Contains(stub.lastReq.Message, "Do not mention delivery limitations") {
		t.Fatalf("expected delivery guardrail prompt, got: %q", stub.lastReq.Message)
	}
	if !strings.Contains(stub.lastReq.Message, "Briefing format requirements") {
		t.Fatalf("expected briefing guardrail prompt, got: %q", stub.lastReq.Message)
	}
	if stub.lastReq.Platform != "automation" {
		t.Fatalf("expected automation platform, got %q", stub.lastReq.Platform)
	}
	if !delivery.called || delivery.dest.Platform != "telegram" || delivery.text != "Agent automation output" {
		t.Fatalf("expected delivery via module runner, got called=%v dest=%+v text=%q", delivery.called, delivery.dest, delivery.text)
	}
	runs, err := db.ListGremlinRuns(items[0].ID, 10)
	if err != nil {
		t.Fatalf("ListGremlinRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatalf("expected completed run record, got %+v", runs)
	}
}

func TestModule_AutomationRunActionIsAutoApprovedAndUsesModuleRunner(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Action Briefing",
		Emoji:       "⚡",
		Prompt:      "Daily briefing with weather and calendar.",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Action automation output"
	delivery := &stubDelivery{}
	registry := skills.NewRegistry(dir, db, nil)

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetDeliveryService(delivery)
	module.SetSkillRegistry(registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if registry.NeedsApproval("automation.run") {
		t.Fatal("automation.run should be auto-approved as module-owned automation control")
	}
	result, err := registry.Execute(context.Background(), "automation.run", []byte(`{"id":"`+items[0].ID+`"}`))
	if err != nil {
		t.Fatalf("automation.run: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success result: %+v", result)
	}
	if !strings.Contains(result.Summary, "ran successfully") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if !strings.Contains(stub.lastReq.Message, "Do not mention delivery limitations") {
		t.Fatalf("expected delivery guardrail prompt, got: %q", stub.lastReq.Message)
	}
	if stub.lastReq.Platform != "automation" {
		t.Fatalf("expected automation platform, got %q", stub.lastReq.Platform)
	}
	if !delivery.called || delivery.dest.Platform != "telegram" || delivery.text != "Action automation output" {
		t.Fatalf("expected delivery via module runner, got called=%v dest=%+v text=%q", delivery.called, delivery.dest, delivery.text)
	}
	runs, err := db.ListGremlinRuns(items[0].ID, 10)
	if err != nil {
		t.Fatalf("ListGremlinRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatalf("expected completed run record, got %+v", runs)
	}
}

func TestModule_AutomationCreateActionAcceptsAuthorizedCommunicationDestination(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	registry := skills.NewRegistry(dir, db, nil)
	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	module.SetSkillRegistry(registry)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := registry.Execute(context.Background(), "automation.create", []byte(`{
		"name":"Telegram Reminder",
		"prompt":"Send the Friday reminder.",
		"schedule":"weekly Friday at 09:00",
		"destinationID":"telegram:123:"
	}`))
	if err != nil {
		t.Fatalf("automation.create: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success result: %+v", result)
	}

	items, err := module.listDefinitions()
	if err != nil {
		t.Fatalf("listDefinitions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %+v", items)
	}
	dest := items[0].CommunicationDestination
	if dest == nil || dest.Platform != "telegram" || dest.ChannelID != "123" || dest.ID != "telegram:123:" {
		t.Fatalf("unexpected destination: %+v", dest)
	}
}

func TestModule_SchedulerRunsDueEnabledAutomationsOnce(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Scheduled Digest",
		Emoji:       "⚡",
		Prompt:      "Scheduled summary",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Scheduled output"
	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	module.now = func() time.Time {
		return time.Date(2026, 4, 7, 9, 0, 30, 0, time.Local)
	}
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	registered, ok, err := module.getDefinition(items[0].ID)
	if err != nil || !ok {
		t.Fatalf("getDefinition: ok=%v err=%v", ok, err)
	}
	dueAt := time.Date(2026, 4, 7, 9, 0, 0, 0, time.Local).UTC().Format(time.RFC3339)
	registered.NextRunAt = &dueAt
	if _, err := module.saveDefinition(registered); err != nil {
		t.Fatalf("saveDefinition due next run: %v", err)
	}

	module.runSchedulerTick(context.Background())
	module.runSchedulerTick(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := db.ListGremlinRuns(items[0].ID, 10)
		if err != nil {
			t.Fatalf("ListGremlinRuns: %v", err)
		}
		if len(runs) == 1 && runs[0].Status == "completed" {
			if runs[0].TriggerSource != "schedule" {
				t.Fatalf("expected schedule trigger source, got %+v", runs[0])
			}
			if stub.lastReq.Platform != "automation" {
				t.Fatalf("expected automation platform, got %q", stub.lastReq.Platform)
			}
			updated, ok, err := module.getDefinition(items[0].ID)
			if err != nil || !ok {
				t.Fatalf("getDefinition after schedule: ok=%v err=%v", ok, err)
			}
			if updated.ScheduleJSON == nil || updated.NextRunAt == nil {
				t.Fatalf("expected persisted schedule state, got %+v", updated)
			}
			advanced, err := time.Parse(time.RFC3339, *updated.NextRunAt)
			if err != nil {
				t.Fatalf("parse next run: %v", err)
			}
			if !advanced.After(time.Date(2026, 4, 7, 9, 0, 0, 0, time.Local)) {
				t.Fatalf("expected nextRunAt to advance, got %s", *updated.NextRunAt)
			}
			return
		}
		if len(runs) > 1 {
			t.Fatalf("expected scheduler to run once for the slot, got %+v", runs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for scheduled automation run")
}

func TestModule_WorkflowBoundAutomationCreatesWorkflowRunLink(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":          "wf-brief",
		"name":        "Briefing Workflow",
		"description": "Workflow description",
		"prompt":      "Run the workflow prompt",
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}
	workflowDefJSON := `{"id":"wf-brief","name":"Briefing Workflow","description":"Workflow description","prompt":"Run the workflow prompt","promptTemplate":"Run the workflow prompt","isEnabled":true,"createdAt":"2026-04-05T00:00:00Z","updatedAt":"2026-04-05T00:00:00Z","steps":[],"tags":[]}`
	if err := db.SaveWorkflow(storage.WorkflowRow{
		ID:             "wf-brief",
		Name:           "Briefing Workflow",
		DefinitionJSON: workflowDefJSON,
		IsEnabled:      true,
		CreatedAt:      "2026-04-05T00:00:00Z",
		UpdatedAt:      "2026-04-05T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}
	workflowID := "wf-brief"
	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Workflow Automation",
		Emoji:       "⚡",
		Prompt:      "Then produce the final summary",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		WorkflowID:  &workflowID,
		WorkflowInputValues: map[string]string{
			"city": "Orlando",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Workflow output"
	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		stub,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := module.runAutomationSync(context.Background(), items[0].ID, "agent")
	if err != nil {
		t.Fatalf("runAutomationSync: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow automation, got %+v", result)
	}
	if !strings.Contains(stub.lastReq.Message, "Run the workflow prompt") ||
		!strings.Contains(stub.lastReq.Message, "Then produce the final summary") ||
		!strings.Contains(stub.lastReq.Message, `"city":"Orlando"`) {
		t.Fatalf("expected workflow prompt composition, got %q", stub.lastReq.Message)
	}
	runs, err := db.ListGremlinRuns(items[0].ID, 10)
	if err != nil {
		t.Fatalf("ListGremlinRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].WorkflowRunID == nil || !strings.HasPrefix(*runs[0].WorkflowRunID, "workflow-") {
		t.Fatalf("expected workflow run link, got %+v", runs)
	}
	workflowRuns, err := db.ListWorkflowRuns(workflowID, 10)
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if len(workflowRuns) != 1 {
		t.Fatalf("expected one workflow run, got %+v", workflowRuns)
	}
	if workflowRuns[0].Status != "completed" {
		t.Fatalf("expected completed workflow run, got %+v", workflowRuns[0])
	}
}

func TestModule_ListAutomationsReturnsCurrentShape(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/automations", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	var body []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body == nil {
		t.Fatal("expected [] not null")
	}
}

func TestModule_ImportsLegacyGremlinsIntoSQLiteDefinitions(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Legacy Import",
		Emoji:       "⚡",
		Prompt:      "Imported prompt",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rows, err := db.ListAutomations()
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "legacy-import" || rows[0].Prompt != "Imported prompt" {
		t.Fatalf("unexpected imported rows: %+v", rows)
	}
}

func TestModule_AdvancedFileImportRouteUpdatesDefinitions(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := chi.NewRouter()
	host.ApplyProtected(r)

	content := "## Advanced Import [⚡]\n" +
		"schedule: daily 09:00\n" +
		"status: enabled\n" +
		"created: 2026-04-07 via import\n\n" +
		"Imported through the advanced route.\n---"
	body := `{"content":` + strconv.Quote(content) + `}`
	req := httptest.NewRequest(http.MethodPut, "/automations/advanced/import", strings.NewReader(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	rows, err := db.ListAutomations()
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "advanced-import" || rows[0].Prompt != "Imported through the advanced route." {
		t.Fatalf("unexpected rows after advanced import: %+v", rows)
	}
}

func TestModule_AdvancedFileImportRejectsUnauthorizedDestination(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := chi.NewRouter()
	host.ApplyProtected(r)

	content := "## Unsafe Import [⚡]\n" +
		"schedule: daily 09:00\n" +
		"status: enabled\n" +
		"created: 2026-04-07 via import\n" +
		`notify_destination: {"id":"telegram:999","platform":"telegram","channelID":"999"}` + "\n\n" +
		"Imported through the advanced route.\n---"
	body := `{"content":` + strconv.Quote(content) + `}`
	req := httptest.NewRequest(http.MethodPut, "/automations/advanced/import", strings.NewReader(body))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not an authorized communication channel") {
		t.Fatalf("expected unauthorized destination error, got %s", rr.Body.String())
	}
}

func TestModule_UpdateAutomationPartialPatchPreservesFields(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "telegram", "123")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Patch Me",
		Emoji:       "⚡",
		Prompt:      "Original prompt",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:123",
			Platform:  "telegram",
			ChannelID: "123",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPut, "/automations/"+items[0].ID, strings.NewReader(`{"prompt":"Updated prompt"}`))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var updated features.GremlinItem
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !updated.IsEnabled {
		t.Fatalf("partial update should preserve enabled=true, got %+v", updated)
	}
	if updated.CommunicationDestination == nil || updated.CommunicationDestination.ChannelID != "123" {
		t.Fatalf("partial update should preserve destination, got %+v", updated.CommunicationDestination)
	}
	if updated.Prompt != "Updated prompt" || updated.ScheduleRaw != "daily 09:00" {
		t.Fatalf("unexpected updated automation: %+v", updated)
	}
}

func TestModule_RenameKeepsStableAutomationID(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Stable Name",
		Emoji:       "⚡",
		Prompt:      "Keep ID",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	updated, err := module.saveDefinition(features.GremlinItem{
		ID:          "stable-name",
		Name:        "Renamed Automation",
		Emoji:       "⚡",
		Prompt:      "Keep ID",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	})
	if err != nil {
		t.Fatalf("saveDefinition: %v", err)
	}
	if updated.ID != "stable-name" {
		t.Fatalf("expected stable ID, got %+v", updated)
	}
	rows, err := db.ListAutomations()
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "stable-name" || rows[0].Name != "Renamed Automation" {
		t.Fatalf("unexpected rows after rename: %+v", rows)
	}
}

func TestModule_SaveRejectsUnauthorizedDestination(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = module.createDefinition(features.GremlinItem{
		Name:        "Unsafe Delivery",
		Emoji:       "⚡",
		Prompt:      "Send somewhere",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "telegram:999",
			Platform:  "telegram",
			ChannelID: "999",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not an authorized communication channel") {
		t.Fatalf("expected unauthorized destination error, got %v", err)
	}
}

func TestModule_ListAutomationSummariesIncludesRunAndDeliveryHealth(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	seedCommSession(t, db, "whatsapp", "me")

	if err := features.AppendGremlin(dir, features.GremlinItem{
		Name:        "Dashboard Digest",
		Emoji:       "⚡",
		Prompt:      "Summarize",
		ScheduleRaw: "daily 09:00",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
		CommunicationDestination: &features.CommunicationDestination{
			ID:        "whatsapp:me",
			Platform:  "whatsapp",
			ChannelID: "me",
		},
	}); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}
	items := features.ParseGremlins(dir)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}
	out := "ok"
	deliveryErr := "whatsapp unavailable"
	finishedAt := float64(time.Now().UTC().Unix())
	if err := db.SaveGremlinRun(storage.GremlinRunRow{
		RunID:           "run-summary",
		GremlinID:       items[0].ID,
		StartedAt:       finishedAt - 5,
		Status:          "running",
		TriggerSource:   "agent",
		ExecutionStatus: "running",
		DeliveryStatus:  "pending",
	}); err != nil {
		t.Fatalf("SaveGremlinRun: %v", err)
	}
	if err := db.CompleteGremlinRun("run-summary", "completed", &out, nil, finishedAt, "failed", &deliveryErr, 5000, nil); err != nil {
		t.Fatalf("CompleteGremlinRun: %v", err)
	}

	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		&stubAgentRuntime{},
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodGet, "/automations/summaries", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body []AutomationSummary
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected one summary, got %+v", body)
	}
	if body[0].Health != "degraded" || body[0].DeliveryHealth != "failing" {
		t.Fatalf("unexpected summary health: %+v", body[0])
	}
	if body[0].LastRunError == nil || !strings.Contains(*body[0].LastRunError, "whatsapp unavailable") {
		t.Fatalf("expected delivery error in summary, got %+v", body[0].LastRunError)
	}
}

func TestBuildAutomationPrompt_AddsBriefingTemplate(t *testing.T) {
	item := features.GremlinItem{
		Prompt: "Orlando daily briefing with weather, top U.S. headlines, and calendar events for today.",
	}
	out := buildAutomationPrompt(item)
	if !strings.Contains(out, "Briefing format requirements") {
		t.Fatalf("expected briefing template guardrail, got: %q", out)
	}
	if !strings.Contains(out, "Always fetch weather via weather.brief") {
		t.Fatalf("expected weather tool guidance, got: %q", out)
	}
	if !strings.Contains(out, "Always fetch calendar via applescript.calendar_read") {
		t.Fatalf("expected calendar tool guidance, got: %q", out)
	}
}

func TestBuildAutomationPrompt_DeliveryAndBriefingGuardsCompose(t *testing.T) {
	item := features.GremlinItem{
		Prompt: "Daily briefing with weather and calendar.",
		CommunicationDestination: &features.CommunicationDestination{
			Platform:  "telegram",
			ChannelID: "123",
		},
	}
	out := buildAutomationPrompt(item)
	if !strings.Contains(out, "Do not mention delivery limitations") {
		t.Fatalf("expected delivery guardrail, got: %q", out)
	}
	if !strings.Contains(out, "Briefing format requirements") {
		t.Fatalf("expected briefing guardrail, got: %q", out)
	}
}

func TestValidateScheduleSummaryMatchesSchedulerSupport(t *testing.T) {
	if got := validateScheduleSummary("cron 0 9 * * *"); !strings.Contains(got, "Unsupported") {
		t.Fatalf("cron should not be reported executable, got %q", got)
	}
	if got := validateScheduleSummary("once 2026-04-08"); !strings.Contains(got, "Unsupported") {
		t.Fatalf("once should not be reported executable, got %q", got)
	}
	if got := validateScheduleSummary("daily 09:00"); got != "Valid daily schedule." {
		t.Fatalf("expected valid daily schedule, got %q", got)
	}
}
