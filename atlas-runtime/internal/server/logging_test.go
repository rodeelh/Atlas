package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	runtimestatus "atlas-runtime-go/internal/runtime"
)

func TestRuntimeStatusMiddlewareTracksActiveRequests(t *testing.T) {
	runtimeSvc := runtimestatus.NewService(1984)
	entered := make(chan struct{})
	release := make(chan struct{})

	handler := runtimeStatusMiddleware(runtimeSvc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	status := runtimeSvc.GetStatus(0, 0, 0)
	if status.ActiveRequests != 1 {
		t.Fatalf("active requests = %d, want 1", status.ActiveRequests)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}

	status = runtimeSvc.GetStatus(0, 0, 0)
	if status.ActiveRequests != 0 {
		t.Fatalf("active requests = %d, want 0", status.ActiveRequests)
	}
}

func TestRuntimeStatusMiddlewareRecordsServerErrors(t *testing.T) {
	runtimeSvc := runtimestatus.NewService(1984)
	handler := runtimeStatusMiddleware(runtimeSvc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/explode", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	status := runtimeSvc.GetStatus(0, 0, 0)
	if status.State != string(runtimestatus.StateDegraded) {
		t.Fatalf("state = %q, want %q", status.State, runtimestatus.StateDegraded)
	}
	if status.LastError == nil || *status.LastError == "" {
		t.Fatal("expected LastError to be populated")
	}
}
