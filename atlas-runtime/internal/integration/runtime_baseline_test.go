package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/domain"
	"atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/forge"
	apivalidationmodule "atlas-runtime-go/internal/modules/apivalidation"
	approvalsmodule "atlas-runtime-go/internal/modules/approvals"
	automationsmodule "atlas-runtime-go/internal/modules/automations"
	communicationsmodule "atlas-runtime-go/internal/modules/communications"
	enginemodule "atlas-runtime-go/internal/modules/engine"
	forgemodule "atlas-runtime-go/internal/modules/forge"
	skillsmodule "atlas-runtime-go/internal/modules/skills"
	usagemodule "atlas-runtime-go/internal/modules/usage"
	workflowsmodule "atlas-runtime-go/internal/modules/workflows"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/server"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
)

type runtimeHarness struct {
	server   *httptest.Server
	db       *storage.DB
	authSvc  *auth.Service
	support  string
	cfgStore *config.Store
	bc       *chat.Broadcaster
}

func newRuntimeHarness(t *testing.T) *runtimeHarness {
	t.Helper()

	supportDir := t.TempDir()
	db, err := storage.Open(filepath.Join(supportDir, "atlas.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfgStore := config.NewStoreAt(
		filepath.Join(supportDir, "config.json"),
		filepath.Join(supportDir, "legacy-config.json"),
	)
	if err := cfgStore.Save(config.Defaults()); err != nil {
		t.Fatalf("cfgStore.Save: %v", err)
	}

	authSvc := auth.NewService(db)
	runtimeSvc := runtime.NewService(1984)
	bc := chat.NewBroadcaster()
	_ = os.MkdirAll(filepath.Join(supportDir, "skills"), 0o755)

	var browserMgr *browser.Manager
	skillsRegistry := skills.NewRegistry(supportDir, db, browserMgr)
	chatSvc := chat.NewService(db, cfgStore, bc, skillsRegistry)
	commsSvc := comms.New(cfgStore, db)
	forgeSvc := forge.NewService(supportDir)
	engineMgr := engine.NewManager(supportDir, filepath.Join(supportDir, "models"))
	routerMgr := engine.NewManager(supportDir, filepath.Join(supportDir, "models-router"))

	host := platform.NewHost(
		cfgStore,
		platform.NewSQLiteStorage(db),
		platform.NewChatAgentRuntime(chatSvc),
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(256),
	)
	registry := platform.NewModuleRegistry(host)
	if err := registry.Register(approvalsmodule.New(supportDir)); err != nil {
		t.Fatalf("registry.Register(approvals): %v", err)
	}
	if err := registry.Register(automationsmodule.New(supportDir)); err != nil {
		t.Fatalf("registry.Register(automations): %v", err)
	}
	communicationsModule := communicationsmodule.New(commsSvc)
	if err := registry.Register(communicationsModule); err != nil {
		t.Fatalf("registry.Register(communications): %v", err)
	}
	if err := registry.Register(forgemodule.New(supportDir, forgeSvc, chatSvc, skillsRegistry)); err != nil {
		t.Fatalf("registry.Register(forge): %v", err)
	}
	if err := registry.Register(workflowsmodule.New(supportDir)); err != nil {
		t.Fatalf("registry.Register(workflows): %v", err)
	}
	if err := registry.Register(skillsmodule.New(supportDir)); err != nil {
		t.Fatalf("registry.Register(skills): %v", err)
	}
	if err := registry.Register(apivalidationmodule.New(supportDir)); err != nil {
		t.Fatalf("registry.Register(api validation): %v", err)
	}
	if err := registry.Register(enginemodule.New(engineMgr, routerMgr, cfgStore)); err != nil {
		t.Fatalf("registry.Register(engine): %v", err)
	}
	if err := registry.Register(usagemodule.New(db)); err != nil {
		t.Fatalf("registry.Register(usage): %v", err)
	}
	communicationsModule.SetApprovalResolver(func(toolCallID string, approved bool) error {
		return nil
	})
	if err := registry.StartAll(context.Background()); err != nil {
		t.Fatalf("registry.StartAll: %v", err)
	}
	t.Cleanup(func() {
		_ = registry.StopAll(context.Background())
	})

	handler := server.BuildRouter(
		domain.NewAuthDomain(authSvc, cfgStore, "", 1984),
		domain.NewControlDomain(cfgStore, runtimeSvc, db, nil),
		domain.NewChatDomain(chatSvc, bc, db),
		nil,
		nil,
		authSvc,
		runtimeSvc,
		func() bool { return false },
		func() bool { return false },
		host,
	)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &runtimeHarness{
		server:   srv,
		db:       db,
		authSvc:  authSvc,
		support:  supportDir,
		cfgStore: cfgStore,
		bc:       bc,
	}
}

func (h *runtimeHarness) authedRequest(t *testing.T, method, path string, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	sess := h.authSvc.CreateSession(false)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func (h *runtimeHarness) do(t *testing.T, method, path string, body string) *http.Response {
	t.Helper()
	client := h.server.Client()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	sess := h.authSvc.CreateSession(false)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return out
}

func TestSkillsRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)

	resp := h.do(t, http.MethodGet, "/skills", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var items []map[string]any
	items = decodeJSON[[]map[string]any](t, resp)
	if len(items) == 0 {
		t.Fatal("expected skills payload")
	}
}

func TestForgeInstalledRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)

	resp := h.do(t, http.MethodGet, "/forge/installed", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var items []map[string]any
	items = decodeJSON[[]map[string]any](t, resp)
	if items == nil {
		t.Fatal("expected [] not null")
	}
}

func TestWorkflowRunRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)

	if _, err := features.AppendWorkflowDefinition(h.support, map[string]any{
		"id":          "wf-shape",
		"name":        "Daily Review",
		"description": "Review the day",
		"prompt":      "Review the day",
	}); err != nil {
		t.Fatalf("AppendWorkflowDefinition: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/workflows/wf-shape/run", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	body := decodeJSON[map[string]any](t, resp)
	if body["workflowID"] != "wf-shape" {
		t.Fatalf("unexpected workflow run payload: %+v", body)
	}
	runID, _ := body["id"].(string)
	if runID == "" {
		t.Fatalf("expected workflow run id, got %+v", body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := h.db.ListWorkflowRuns("wf-shape", 10)
		if err == nil {
			for _, run := range runs {
				if run.RunID == runID && run.Status != "" {
					return
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for workflow run %s to finish", runID)
}

func TestEngineStatusRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)
	resp := h.do(t, http.MethodGet, "/engine/status", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeJSON[map[string]any](t, resp)
	if _, ok := body["running"]; !ok {
		t.Fatalf("unexpected engine status payload: %+v", body)
	}
}

func TestUsageSummaryRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)
	resp := h.do(t, http.MethodGet, "/usage/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeJSON[map[string]any](t, resp)
	if _, ok := body["totalTokens"]; !ok {
		t.Fatalf("unexpected usage summary payload: %+v", body)
	}
}

func TestAPIValidationHistoryRoute_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)
	resp := h.do(t, http.MethodGet, "/api-validation/history", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body := decodeJSON[[]map[string]any](t, resp)
	if body == nil {
		t.Fatal("expected [] not null")
	}
}

func TestMessageStream_CurrentSSEBehavior(t *testing.T) {
	h := newRuntimeHarness(t)

	client := h.server.Client()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/message/stream?conversationID=conv-sse", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	sess := h.authSvc.CreateSession(false)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})

	respCh := make(chan *http.Response, 1)
	go func() {
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			t.Errorf("client.Do: %v", reqErr)
			return
		}
		respCh <- resp
	}()

	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out establishing SSE stream")
	}
	defer resp.Body.Close()

	h.bc.Emit("conv-sse", chat.SSEEvent{Type: "assistant_delta", Content: "hello"})

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, "\"type\":\"assistant_delta\"") {
		t.Fatalf("unexpected SSE payload: %q", line)
	}
}

func TestApprovalResolutionFlow_ApproveRouteUpdatesStatus(t *testing.T) {
	h := newRuntimeHarness(t)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	actionID := "browser.click"
	convID := "conv-approval"
	if err := h.db.SaveDeferredExecution(storage.DeferredExecRow{
		DeferredID:          "deferred-1",
		SourceType:          "agent",
		ActionID:            &actionID,
		ToolCallID:          "tool-call-1",
		NormalizedInputJSON: `{"tool_calls":[{"id":"tool-call-1","function":{"arguments":"{\"selector\":\"#save\"}"}}]}`,
		ConversationID:      &convID,
		ApprovalID:          "approval-1",
		Summary:             actionID,
		PermissionLevel:     "execute",
		RiskLevel:           "medium",
		Status:              "pending_approval",
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("SaveDeferredExecution: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/approvals/tool-call-1/approve", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	row, err := h.db.FetchDeferredByToolCallID("tool-call-1")
	if err != nil {
		t.Fatalf("FetchDeferredByToolCallID: %v", err)
	}
	if row == nil || row.Status != "approved" {
		t.Fatalf("expected approved row, got %+v", row)
	}
}

func TestAutomationExecutionFlow_RunRouteCreatesRunRecord(t *testing.T) {
	h := newRuntimeHarness(t)

	item := features.GremlinItem{
		Name:        "Nightly Digest",
		Emoji:       "⚡",
		Prompt:      "Summarize today",
		ScheduleRaw: "every day at 9am",
		IsEnabled:   true,
		SourceType:  "manual",
		CreatedAt:   "2026-04-05",
	}
	if err := features.AppendGremlin(h.support, item); err != nil {
		t.Fatalf("AppendGremlin: %v", err)
	}

	items := features.ParseGremlins(h.support)
	if len(items) != 1 {
		t.Fatalf("expected one automation, got %d", len(items))
	}

	resp := h.do(t, http.MethodPost, fmt.Sprintf("/automations/%s/run", items[0].ID), "{}")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	runID := body["id"]
	if runID == "" {
		t.Fatal("expected run id")
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := h.db.ListGremlinRuns(items[0].ID, 10)
		if err != nil {
			t.Fatalf("ListGremlinRuns: %v", err)
		}
		if len(runs) > 0 && runs[0].RunID == runID && runs[0].Status != "running" {
			if runs[0].Status != "failed" && runs[0].Status != "completed" {
				t.Fatalf("expected finished run, got %+v", runs[0])
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("timed out waiting for automation run completion")
}

func TestCommunicationsManagementRoutes_CurrentShape(t *testing.T) {
	h := newRuntimeHarness(t)

	resp := h.do(t, http.MethodGet, "/communications/channels", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	channels := decodeJSON[[]map[string]any](t, resp)
	if channels == nil {
		t.Fatal("expected [] not null")
	}
}

func TestAuthenticatedRuntimeRouter_StillServesExistingAPI(t *testing.T) {
	h := newRuntimeHarness(t)

	resp := h.do(t, http.MethodGet, "/communications", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("communications route failed with status %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, "/automations", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("automations route failed with status %d", resp.StatusCode)
	}
	resp.Body.Close()
}
