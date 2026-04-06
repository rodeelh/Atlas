package approvals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubAgentRuntime struct {
	resumeCalls []struct {
		toolCallID string
		approved   bool
	}
}

func (s *stubAgentRuntime) HandleMessage(context.Context, chat.MessageRequest) (chat.MessageResponse, error) {
	return chat.MessageResponse{}, nil
}

func (s *stubAgentRuntime) Resume(toolCallID string, approved bool) {
	s.resumeCalls = append(s.resumeCalls, struct {
		toolCallID string
		approved   bool
	}{toolCallID: toolCallID, approved: approved})
}

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

func TestModule_RegistersRoutesAndResolvesApproval(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()

	agentRuntime := &stubAgentRuntime{}
	host := platform.NewHost(
		stubConfig{},
		platform.NewSQLiteStorage(db),
		agentRuntime,
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(8),
	)

	module := New(dir)
	if err := module.Register(host); err != nil {
		t.Fatalf("Register: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	actionID := "browser.click"
	convID := "conv-1"
	if err := db.SaveDeferredExecution(storage.DeferredExecRow{
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

	r := chi.NewRouter()
	host.ApplyProtected(r)

	req := httptest.NewRequest(http.MethodPost, "/approvals/tool-call-1/approve", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	row, err := db.FetchDeferredByToolCallID("tool-call-1")
	if err != nil {
		t.Fatalf("FetchDeferredByToolCallID: %v", err)
	}
	if row == nil || row.Status != "approved" {
		t.Fatalf("unexpected deferred row: %+v", row)
	}
	if len(agentRuntime.resumeCalls) != 1 || !agentRuntime.resumeCalls[0].approved {
		t.Fatalf("expected resume callback, got %+v", agentRuntime.resumeCalls)
	}

	var body approvalJSON
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "approved" {
		t.Fatalf("unexpected response: %+v", body)
	}
}
