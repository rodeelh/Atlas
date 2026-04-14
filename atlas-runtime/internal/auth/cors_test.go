package auth

import (
	"net/http/httptest"
	"testing"
)

func TestIsAllowedCORSOrigin(t *testing.T) {
	t.Run("allows localhost origin on localhost host", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://localhost:1984/status", nil)
		if !IsAllowedCORSOrigin(req, "http://localhost:5173", nil, nil) {
			t.Fatal("expected localhost origin to be allowed for localhost host")
		}
	})

	t.Run("blocks localhost origin on remote host", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://192.168.1.138:1984/status", nil)
		if IsAllowedCORSOrigin(req, "http://localhost:5173", nil, nil) {
			t.Fatal("expected localhost origin to be blocked for remote host")
		}
	})

	t.Run("allows same host origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://192.168.1.138:1984/status", nil)
		if !IsAllowedCORSOrigin(req, "http://192.168.1.138:1984", nil, nil) {
			t.Fatal("expected same-host origin to be allowed")
		}
	})

	t.Run("blocks different host origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://192.168.1.138:1984/status", nil)
		if IsAllowedCORSOrigin(req, "https://evil.example", nil, nil) {
			t.Fatal("expected foreign origin to be blocked")
		}
	})
}

func TestIsSameHostOrigin(t *testing.T) {
	if !isSameHostOrigin("http://192.168.1.138:8080", "192.168.1.138:1984") {
		t.Fatal("expected same canonical host to match")
	}
	if isSameHostOrigin("https://evil.example", "192.168.1.138:1984") {
		t.Fatal("expected different host to fail")
	}
	if isSameHostOrigin("not-a-url", "192.168.1.138:1984") {
		t.Fatal("expected invalid origin to fail")
	}
}
