package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// tailscaleNet is the pre-parsed Tailscale CGNAT range (100.64.0.0/10).
// Computed once at package init so IsTailscaleIP never allocates per-call.
var tailscaleNet *net.IPNet

func init() {
	_, tailscaleNet, _ = net.ParseCIDR("100.64.0.0/10")
}

// RequireSession is a chi middleware that enforces the Atlas session model:
//   - Requests from localhost — bypass auth (native macOS app AND local browsers).
//   - Requests from a Tailscale IP when tailscaleEnabled() — bypass auth entirely.
//     Tailscale's cryptographic device identity is the trust mechanism; no Atlas token needed.
//   - All other remote requests (LAN) require an Atlas remote session via /auth/remote-gate.
//
// NOTE: browsers omit the Origin header on same-origin GET requests but SEND it
// on POST requests (even same-origin). Previously this bypass only fired when
// Origin was empty, which silently broke every POST from a localhost browser
// after its bootstrap cookie expired. We now bypass on any localhost-hosted
// request whose Origin is either empty or also localhost. Remote LAN requests
// still fall through to session validation because their Host is non-local.
func RequireSession(svc *Service, tailscaleEnabled func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			isLocalReq := IsLocalRequest(r)

			// Cross-origin guard for remote requests: if a browser sends an Origin,
			// it must resolve to the same host as the request target.
			// This blocks credentialed CSRF attempts from unrelated sites.
			if origin != "" && !isLocalReq && !isSameHostOrigin(origin, r.Host) {
				writeError(w, http.StatusForbidden, "Cross-origin request blocked.")
				return
			}

			// Native client or same-origin local browser — bypass auth.
			// Accepts: empty Origin (native URLSession / browser GET) OR
			// an Origin that is itself a localhost URL (browser POST).
			if isLocalReq && (origin == "" || isLocalhostOrigin(origin)) {
				next.ServeHTTP(w, r)
				return
			}

			// Tailscale devices bypass Atlas session auth when Tailscale is enabled.
			if tailscaleEnabled != nil && tailscaleEnabled() && isTailscaleRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Tailscale is disabled (or the bypass above didn't fire) — explicitly
			// reject Tailscale IPs regardless of any session they may hold.
			// This closes the window where a Tailscale user authenticates via the
			// LAN key after the Tailscale toggle is turned off mid-session.
			if isTailscaleRequest(r) {
				writeError(w, http.StatusForbidden, "Tailscale access is not enabled.")
				return
			}

			// Remote LAN auth is only permitted over HTTPS (direct TLS or trusted
			// loopback reverse proxy with X-Forwarded-Proto=https). This prevents
			// plaintext key/cookie exposure on local networks.
			if !IsSecureRequest(r) {
				writeError(w, http.StatusUpgradeRequired,
					"HTTPS is required for remote LAN access.")
				return
			}

			// All other remote requests (LAN) require a valid Atlas session.
			sessionID := SessionIDFromCookie(r.Header.Get("Cookie"))
			if !svc.ValidateSession(sessionID) {
				writeError(w, http.StatusUnauthorized,
					"Not authenticated. Open Atlas on the host Mac or visit / (redirects to /auth/remote-gate).")
				return
			}

			// Non-localhost requests require a remote session specifically.
			isRemoteReq := !isLocalReq || (origin != "" && !isLocalhostOrigin(origin))
			if isRemoteReq {
				sess := svc.SessionDetail(sessionID)
				if sess == nil || !sess.IsRemote {
					writeError(w, http.StatusUnauthorized,
						"Remote access requires authentication via / (redirects to /auth/remote-gate).")
					return
				}
				if requiresCSRF(r.Method) {
					token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
					if !svc.ValidateCSRF(sessionID, token) {
						writeError(w, http.StatusForbidden, "CSRF token invalid or missing.")
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LanGate is a chi middleware that rejects non-localhost requests when remote
// access is disabled. Tailscale connections are allowed through regardless of
// the LAN toggle when tailscaleEnabled() returns true.
// Returns a browser-friendly HTML page for navigation requests so the user
// sees a clear message rather than raw JSON.
func LanGate(remoteEnabled func() bool, tailscaleEnabled func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsLocalRequest(r) && !remoteEnabled() {
				// Tailscale connections bypass the LAN gate — they have their own trust model.
				if tailscaleEnabled != nil && tailscaleEnabled() && isTailscaleRequest(r) {
					next.ServeHTTP(w, r)
					return
				}
				if strings.Contains(r.Header.Get("Accept"), "text/html") {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, lanDisabledHTML())
					return
				}
				writeError(w, http.StatusForbidden,
					"Remote access is not enabled. Enable it in Atlas Settings.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func lanDisabledHTML() string {
	return LanDisabledHTML()
}

// isLocalhostOrigin returns true if origin is http://localhost:* or http://127.0.0.1:*.
func isLocalhostOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1")
}

func requiresCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// isSameHostOrigin reports whether origin resolves to the same canonical host
// as host (ignoring port differences, comparing case-insensitively).
func isSameHostOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(CanonicalHost(u.Host), CanonicalHost(host))
}

// CanonicalHost strips any port and IPv6 brackets so host comparisons are stable
// across localhost forms such as localhost:1984, 127.0.0.1:1984, and [::1]:1984.
func CanonicalHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}

// IsLocalhostHost returns true if host refers to the loopback address.
func IsLocalhostHost(host string) bool {
	switch CanonicalHost(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// isLocalhostHost is the package-local alias retained for internal call sites.
func isLocalhostHost(host string) bool {
	return IsLocalhostHost(host)
}

// isTailscaleRequest returns true if the request originates from a Tailscale
// node IP (100.64.0.0/10 CGNAT range assigned by Tailscale).
func isTailscaleRequest(r *http.Request) bool {
	// Tailscale trust is bound to the immediate network peer address.
	// Do not use forwarded headers here; a local reverse proxy could otherwise
	// let spoofed client IP chains influence trust decisions.
	ip := PeerIP(r)
	if ip == "" {
		return false
	}
	return IsTailscaleIP(ip)
}

// IsTailscaleAddr reports whether addr (host:port or bare IP) is a Tailscale IP.
// Exported so other packages (e.g. domain) can reuse without reimplementing.
func IsTailscaleAddr(addr string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return IsTailscaleIP(host)
	}
	return IsTailscaleIP(addr)
}

// IsTailscaleIP reports whether ipStr is in the Tailscale CGNAT range 100.64.0.0/10.
// Uses the package-level pre-parsed net to avoid per-call allocation.
func IsTailscaleIP(ipStr string) bool {
	if tailscaleNet == nil {
		return false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return tailscaleNet.Contains(ip)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
