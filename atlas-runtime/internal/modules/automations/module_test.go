package automations

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
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/storage"
)

type stubAgentRuntime struct {
	response chat.MessageResponse
	err      error
	lastReq  chat.MessageRequest
}

func (s *stubAgentRuntime) HandleMessage(_ context.Context, req chat.MessageRequest) (chat.MessageResponse, error) {
	s.lastReq = req
	return s.response, s.err
}

func (s *stubAgentRuntime) Resume(string, bool) {}

type stubConfig struct{}

func (stubConfig) Load() config.RuntimeConfigSnapshot { return config.Defaults() }

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
			if stub.lastReq.Message != "Summarize today" {
				t.Fatalf("unexpected prompt: %+v", stub.lastReq)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for automation run completion")
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
