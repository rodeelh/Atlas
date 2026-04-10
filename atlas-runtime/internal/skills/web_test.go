package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchPageReturnsStructuredResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><h1>Atlas</h1><p>Structured web fetch test content.</p></body></html>"))
	}))
	defer srv.Close()

	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := webFetchPage(context.Background(), args)
	if err != nil {
		t.Fatalf("webFetchPage: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success result: %+v", res)
	}
	source, ok := res.Artifacts["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured source artifact, got %+v", res.Artifacts["source"])
	}
	if source["url"] != srv.URL {
		t.Fatalf("source.url = %v", source["url"])
	}
	if preview, _ := res.Artifacts["preview"].(string); !strings.Contains(preview, "Structured web fetch test content") {
		t.Fatalf("preview missing fetched content: %q", preview)
	}
}

func TestWebCheckURLReturnsStructuredResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	res, err := webCheckURL(context.Background(), args)
	if err != nil {
		t.Fatalf("webCheckURL: %v", err)
	}
	source, ok := res.Artifacts["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured source artifact, got %+v", res.Artifacts["source"])
	}
	if source["status"] != http.StatusNoContent {
		t.Fatalf("status = %v", source["status"])
	}
}
