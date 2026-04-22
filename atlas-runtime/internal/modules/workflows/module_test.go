package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	runtimeskills "atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type stubAgentRuntime struct {
	response chat.MessageResponse
	err      error
	lastReq  chat.MessageRequest
	requests []chat.MessageRequest
}

func (s *stubAgentRuntime) HandleMessage(_ context.Context, req chat.MessageRequest) (chat.MessageResponse, error) {
	s.lastReq = req
	s.requests = append(s.requests, req)
	return s.response, s.err
}

func (s *stubAgentRuntime) Resume(string, bool) {}

type stubConfig struct {
	snap config.RuntimeConfigSnapshot
}

func (s stubConfig) Load() config.RuntimeConfigSnapshot {
	if s.snap.PersonaName == "" && s.snap.BaseSystemPrompt == "" && s.snap.RuntimePort == 0 {
		return config.Defaults()
	}
	return s.snap
}

func TestModule_RunWorkflowCreatesRunAndRoutesPrompt(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":          "wf-1",
		"name":        "Nightly Review",
		"description": "Summarize the day",
		"prompt":      "Summarize the day",
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "done"

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

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/workflows/wf-1/run", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := db.ListWorkflowRuns("wf-1", 10)
		if err != nil {
			t.Fatalf("ListWorkflowRuns: %v", err)
		}
		if len(runs) > 0 {
			if runs[0].Status == "completed" {
				if !strings.Contains(stub.lastReq.Message, "Summarize the day") ||
					!strings.Contains(stub.lastReq.Message, "Workflow trust scope") {
					t.Fatalf("unexpected prompt: %+v", stub.lastReq)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for workflow run completion")
}

func TestModule_ListWorkflowsReturnsCurrentShape(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
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

func TestModule_RunWorkflowExecutesPromptStepsAndPersistsStepRuns(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":             "wf-steps",
		"name":           "Stepped Review",
		"promptTemplate": "Review the source material",
		"steps": []map[string]any{
			{"id": "extract", "title": "Extract", "kind": "prompt", "prompt": "Extract the key facts."},
			{"id": "summarize", "title": "Summarize", "kind": "prompt", "prompt": "Summarize the facts."},
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "step done"
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), stub, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	record, err := module.runWorkflowSync(context.Background(), "wf-steps", nil, "test")
	if err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	if record["status"] != "completed" {
		t.Fatalf("expected completed run, got %+v", record)
	}
	if len(stub.requests) != 2 {
		t.Fatalf("expected two step turns, got %d", len(stub.requests))
	}
	if !strings.Contains(stub.requests[0].Message, "Workflow step 1 of 2: Extract") ||
		!strings.Contains(stub.requests[1].Message, "Workflow step 2 of 2: Summarize") {
		t.Fatalf("unexpected step prompts: %+v", stub.requests)
	}
	runs, err := db.ListWorkflowRuns("wf-steps", 1)
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one workflow run, got %+v", runs)
	}
	var stepRuns []map[string]any
	if err := json.Unmarshal([]byte(runs[0].StepRunsJSON), &stepRuns); err != nil {
		t.Fatalf("decode step runs: %v", err)
	}
	if len(stepRuns) != 2 || stepRuns[0]["status"] != "completed" || stepRuns[1]["status"] != "completed" {
		t.Fatalf("expected completed step runs, got %+v", stepRuns)
	}
}

func TestModule_RunWorkflowExecutesTypedStepsAndDirectToolCall(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	outputDir := filepath.Join(dir, "exports")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := runtimeskills.SaveFsRoots(dir, []runtimeskills.FsRoot{{ID: "root-1", Path: outputDir}}); err != nil {
		t.Fatalf("SaveFsRoots: %v", err)
	}

	pdfPath := filepath.Join(outputDir, "theme.pdf")
	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":   "wf-typed",
		"name": "Typed Theme To PDF",
		"steps": []map[string]any{
			{"id": "draft", "title": "Draft", "type": "llm.generate", "prompt": "Write a short brief about {input.theme}."},
			{"id": "save", "title": "Save PDF", "type": "atlas.tool", "action": "fs.create_pdf", "args": map[string]any{
				"path":    "{input.path}",
				"title":   "{input.theme}",
				"content": "{steps.draft.output}",
			}},
			{"id": "done", "title": "Return", "type": "return", "value": "Saved themed PDF to {input.path}"},
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "Atlas productivity systems are compounding well."
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), stub, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	module.SetSkillRegistry(runtimeskills.NewRegistry(dir, db, nil))
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	record, err := module.runWorkflowSync(context.Background(), "wf-typed", map[string]string{
		"theme": "Atlas productivity",
		"path":  pdfPath,
	}, "test")
	if err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	if record["status"] != "completed" {
		t.Fatalf("expected completed run, got %+v", record)
	}
	if !strings.Contains(record["assistantSummary"].(string), "Saved themed PDF") {
		t.Fatalf("expected return summary, got %+v", record["assistantSummary"])
	}
	if len(stub.requests) != 1 {
		t.Fatalf("expected one llm.generate request, got %d", len(stub.requests))
	}
	if !strings.Contains(stub.requests[0].Message, "Atlas productivity") {
		t.Fatalf("expected interpolated prompt, got %+v", stub.requests[0])
	}
	if _, err := os.Stat(pdfPath); err != nil {
		t.Fatalf("expected pdf to be written: %v", err)
	}

	runs, err := db.ListWorkflowRuns("wf-typed", 1)
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one workflow run, got %+v", runs)
	}
	var stepRuns []map[string]any
	if err := json.Unmarshal([]byte(runs[0].StepRunsJSON), &stepRuns); err != nil {
		t.Fatalf("decode step runs: %v", err)
	}
	if len(stepRuns) != 3 {
		t.Fatalf("expected three step runs, got %+v", stepRuns)
	}
	for _, stepRun := range stepRuns {
		if stepRun["status"] != "completed" {
			t.Fatalf("expected completed typed step runs, got %+v", stepRuns)
		}
	}
}

func TestModule_RunWorkflowPropagatesTrustScopeToolPolicy(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":             "wf-policy",
		"name":           "Policy Review",
		"promptTemplate": "Read the approved file only",
		"trustScope": map[string]any{
			"approvedRootPaths":   []string{"/tmp/atlas-approved"},
			"allowedApps":         []string{"filesystem"},
			"allowsSensitiveRead": false,
			"allowsLiveWrite":     false,
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "done"
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), stub, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := module.runWorkflowSync(context.Background(), "wf-policy", nil, "test"); err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	if stub.lastReq.ToolPolicy == nil || !stub.lastReq.ToolPolicy.Enabled {
		t.Fatalf("expected workflow tool policy, got %+v", stub.lastReq.ToolPolicy)
	}
	if stub.lastReq.ToolPolicy.AllowsLiveWrite || stub.lastReq.ToolPolicy.AllowsSensitiveRead {
		t.Fatalf("expected restrictive trust policy, got %+v", stub.lastReq.ToolPolicy)
	}
	if len(stub.lastReq.ToolPolicy.ApprovedRootPaths) != 1 || stub.lastReq.ToolPolicy.ApprovedRootPaths[0] != "/tmp/atlas-approved" {
		t.Fatalf("unexpected approved roots: %+v", stub.lastReq.ToolPolicy.ApprovedRootPaths)
	}
	if len(stub.lastReq.ToolPolicy.AllowedToolPrefixes) != 1 || stub.lastReq.ToolPolicy.AllowedToolPrefixes[0] != "fs." {
		t.Fatalf("unexpected allowed prefixes: %+v", stub.lastReq.ToolPolicy.AllowedToolPrefixes)
	}
}

func TestModule_RunWorkflowUnleashedLiftsTrustScopeWriteRestrictions(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":             "wf-unleashed",
		"name":           "Unleashed Policy Review",
		"promptTemplate": "Send the update through the authorized bridge",
		"trustScope": map[string]any{
			"approvedRootPaths":   []string{"/tmp/atlas-approved"},
			"allowedApps":         []string{"communications"},
			"allowsSensitiveRead": false,
			"allowsLiveWrite":     false,
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "done"
	cfg := config.Defaults()
	cfg.AutonomyMode = config.AutonomyModeUnleashed
	host := platform.NewHost(stubConfig{snap: cfg}, platform.NewSQLiteStorage(db), stub, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := module.runWorkflowSync(context.Background(), "wf-unleashed", nil, "test"); err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	if stub.lastReq.ToolPolicy == nil || !stub.lastReq.ToolPolicy.Enabled {
		t.Fatalf("expected workflow tool policy, got %+v", stub.lastReq.ToolPolicy)
	}
	if !stub.lastReq.ToolPolicy.AllowsLiveWrite || !stub.lastReq.ToolPolicy.AllowsSensitiveRead {
		t.Fatalf("expected unleashed workflow policy to allow live writes and sensitive reads, got %+v", stub.lastReq.ToolPolicy)
	}
	if len(stub.lastReq.ToolPolicy.AllowedToolPrefixes) != 0 {
		t.Fatalf("expected unleashed workflow policy to drop allowed-tool restrictions, got %+v", stub.lastReq.ToolPolicy.AllowedToolPrefixes)
	}
	if len(stub.lastReq.ToolPolicy.ApprovedRootPaths) != 0 {
		t.Fatalf("expected unleashed workflow policy to drop approved-root restrictions, got %+v", stub.lastReq.ToolPolicy.ApprovedRootPaths)
	}
}

func TestModule_RunWorkflowStepByStepWaitsForApprovalAndAdvances(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":             "wf-approval",
		"name":           "Approval Review",
		"promptTemplate": "Review the source material",
		"approvalMode":   "step_by_step",
		"steps": []map[string]any{
			{"id": "extract", "title": "Extract", "kind": "prompt", "prompt": "Extract the key facts."},
			{"id": "summarize", "title": "Summarize", "kind": "prompt", "prompt": "Summarize the facts."},
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	stub := &stubAgentRuntime{}
	stub.response.Response.AssistantMessage = "step done"
	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), stub, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	record, err := module.runWorkflowSync(context.Background(), "wf-approval", nil, "test")
	if err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	if record["status"] != "waiting_for_approval" {
		t.Fatalf("expected waiting_for_approval, got %+v", record)
	}
	if len(stub.requests) != 0 {
		t.Fatalf("expected no agent calls before approval, got %d", len(stub.requests))
	}
	if approval, _ := record["approval"].(map[string]any); approval["reason"] == nil {
		t.Fatalf("expected approval payload, got %+v", record["approval"])
	}

	runID, _ := record["id"].(string)
	updated, err := module.resumeWorkflowAfterApproval(context.Background(), runID)
	if err != nil {
		t.Fatalf("resumeWorkflowAfterApproval: %v", err)
	}
	if updated["status"] != "waiting_for_approval" {
		t.Fatalf("expected second checkpoint, got %+v", updated)
	}
	if len(stub.requests) != 1 || !strings.Contains(stub.requests[0].Message, "Workflow step 1 of 2: Extract") {
		t.Fatalf("unexpected first-step requests: %+v", stub.requests)
	}

	finalRecord, err := module.resumeWorkflowAfterApproval(context.Background(), runID)
	if err != nil {
		t.Fatalf("second resumeWorkflowAfterApproval: %v", err)
	}
	if finalRecord["status"] != "completed" {
		t.Fatalf("expected completed run, got %+v", finalRecord)
	}
	if len(stub.requests) != 2 || !strings.Contains(stub.requests[1].Message, "Workflow step 2 of 2: Summarize") {
		t.Fatalf("unexpected requests after second approval: %+v", stub.requests)
	}
}

func TestModule_DenyWorkflowRunClosesPendingApproval(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	if _, err := features.AppendWorkflowDefinition(dir, map[string]any{
		"id":             "wf-deny",
		"name":           "Deny Review",
		"promptTemplate": "Review the source material",
		"approvalMode":   "step_by_step",
		"steps": []map[string]any{
			{"id": "extract", "title": "Extract", "kind": "prompt", "prompt": "Extract the key facts."},
		},
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	host := platform.NewHost(stubConfig{}, platform.NewSQLiteStorage(db), &stubAgentRuntime{}, platform.NoopContextAssembler{}, platform.NewInProcessBus(8))
	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	record, err := module.runWorkflowSync(context.Background(), "wf-deny", nil, "test")
	if err != nil {
		t.Fatalf("runWorkflowSync: %v", err)
	}
	runID, _ := record["id"].(string)
	denied, err := module.denyWorkflowRunRecord(runID)
	if err != nil {
		t.Fatalf("denyWorkflowRunRecord: %v", err)
	}
	if denied["status"] != "denied" {
		t.Fatalf("expected denied status, got %+v", denied)
	}
	if denied["errorMessage"] != "Workflow run denied." {
		t.Fatalf("unexpected deny message: %+v", denied)
	}
}
