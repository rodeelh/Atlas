package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := New("http://localhost:1984")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.BaseURL() != "http://localhost:1984" {
		t.Errorf("BaseURL = %q", c.BaseURL())
	}
}

func TestHealthCheckOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.HealthCheck(); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
}

func TestHealthCheckFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.HealthCheck(); err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestAPIErrorMessage(t *testing.T) {
	e := &APIError{Code: 404, Message: "not found"}
	if !strings.Contains(e.Error(), "404") {
		t.Errorf("APIError missing code: %s", e.Error())
	}
}
