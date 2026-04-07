package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSession_DoesNotTrustHostHeaderForLocalBypass(t *testing.T) {
	svc := NewService(nil)
	mw := RequireSession(svc, func() bool { return false })

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	h := mw(next)
	req := httptest.NewRequest(http.MethodGet, "http://localhost:1984/status", nil)
	req.Host = "localhost:1984"          // spoofed host header
	req.RemoteAddr = "192.168.1.50:4444" // actual remote client
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if called {
		t.Fatal("unexpected local bypass via spoofed Host header")
	}
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 for spoofed host; got %d", rr.Code)
	}
}

func TestIsSecureRequest_TrustsForwardedProtoOnlyFromLoopback(t *testing.T) {
	remote := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	remote.RemoteAddr = "192.168.1.50:4444"
	remote.Header.Set("X-Forwarded-Proto", "https")
	if IsSecureRequest(remote) {
		t.Fatal("unexpected secure trust from non-loopback X-Forwarded-Proto")
	}

	localProxy := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	localProxy.RemoteAddr = "127.0.0.1:4444"
	localProxy.Header.Set("X-Forwarded-Proto", "https")
	if !IsSecureRequest(localProxy) {
		t.Fatal("expected loopback proxy X-Forwarded-Proto=https to be trusted")
	}
}

func TestClientIPAndLocality_TrustBoundary(t *testing.T) {
	// Direct remote peer cannot spoof locality with X-Forwarded-For.
	direct := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	direct.RemoteAddr = "192.168.1.50:4444"
	direct.Header.Set("X-Forwarded-For", "127.0.0.1")
	if IsLocalRequest(direct) {
		t.Fatal("unexpected local classification from untrusted forwarded header")
	}

	// Loopback proxy peer may supply the forwarded client IP.
	proxied := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	proxied.RemoteAddr = "127.0.0.1:9000"
	proxied.Header.Set("X-Forwarded-For", "10.0.0.99, 192.168.1.77")
	if IsLocalRequest(proxied) {
		t.Fatal("expected proxied external client to be classified as remote")
	}
	if got := ClientIP(proxied); got != "192.168.1.77" {
		t.Fatalf("unexpected proxied client ip: %q", got)
	}
}

func TestIsTailscaleRequest_UsesPeerIPNotForwarded(t *testing.T) {
	// Direct non-Tailscale peer with spoofed forwarded Tailscale IP must fail.
	req := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	req.RemoteAddr = "192.168.1.50:4444"
	req.Header.Set("X-Forwarded-For", "100.101.102.103")
	if isTailscaleRequest(req) {
		t.Fatal("unexpected tailscale trust from spoofed forwarded header")
	}

	// Local proxy peer with forwarded Tailscale IP must also fail because
	// tailscale trust is direct-peer only.
	proxied := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	proxied.RemoteAddr = "127.0.0.1:9000"
	proxied.Header.Set("X-Forwarded-For", "100.101.102.103")
	if isTailscaleRequest(proxied) {
		t.Fatal("unexpected tailscale trust from proxied forwarded header")
	}

	// Direct Tailscale peer is accepted.
	directTS := httptest.NewRequest(http.MethodGet, "http://atlas.local", nil)
	directTS.RemoteAddr = "100.101.102.103:4444"
	if !isTailscaleRequest(directTS) {
		t.Fatal("expected direct tailscale peer to be trusted")
	}
}
