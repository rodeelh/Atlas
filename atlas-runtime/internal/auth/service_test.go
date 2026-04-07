package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionSetCookieValueForRequest_Secure(t *testing.T) {
	sess := &Session{ID: "session-id"}

	httpReq := httptest.NewRequest("GET", "http://localhost/auth/bootstrap", nil)
	cookie := SessionSetCookieValueForRequest(sess, httpReq)
	if strings.Contains(cookie, "Secure") {
		t.Fatal("did not expect Secure on plain HTTP request")
	}

	proxyReq := httptest.NewRequest("GET", "http://localhost/auth/bootstrap", nil)
	proxyReq.RemoteAddr = "127.0.0.1:12345"
	proxyReq.Header.Set("X-Forwarded-Proto", "https")
	secureCookie := SessionSetCookieValueForRequest(sess, proxyReq)
	if !strings.Contains(secureCookie, "Secure") {
		t.Fatal("expected Secure when X-Forwarded-Proto=https")
	}
}

func TestCSRFTokenValidation(t *testing.T) {
	svc := NewService(nil)
	sessionID := "remote-session-id"

	token := svc.CSRFToken(sessionID)
	if token == "" {
		t.Fatal("expected csrf token")
	}
	if !svc.ValidateCSRF(sessionID, token) {
		t.Fatal("expected csrf token to validate")
	}
	if svc.ValidateCSRF(sessionID, token+"x") {
		t.Fatal("expected mutated csrf token to fail")
	}
}
