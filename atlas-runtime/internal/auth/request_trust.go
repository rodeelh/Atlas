package auth

import (
	"net"
	"net/http"
	"strings"
)

// IsLoopbackAddr reports whether addr (host:port or IP) is loopback.
func IsLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// PeerIP returns the immediate TCP peer IP from RemoteAddr.
func PeerIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// IsLocalRequest reports whether the TCP peer is loopback.
func IsLocalRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	ip := ClientIP(r)
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// IsSecureRequest reports whether the request arrived over HTTPS directly or
// via a loopback trusted proxy that sets X-Forwarded-Proto=https.
func IsSecureRequest(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if r == nil {
		return false
	}
	// Only trust forwarded proto when the immediate peer is loopback
	// (local reverse proxy boundary).
	if !IsLoopbackAddr(r.RemoteAddr) {
		return false
	}
	xfp := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-Proto"), ",", 2)[0])
	return strings.EqualFold(xfp, "https")
}

// ClientIP returns the effective client IP for trust decisions.
// We accept X-Forwarded-For only when the immediate peer is loopback, which
// models a trusted local reverse proxy boundary.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	peer := PeerIP(r)
	if IsLoopbackAddr(r.RemoteAddr) {
		if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); raw != "" {
			parts := strings.Split(raw, ",")
			// Use the right-most value to resist spoofed left-most injections when
			// proxies append client IPs to an existing X-Forwarded-For chain.
			fwd := strings.TrimSpace(parts[len(parts)-1])
			if ip := net.ParseIP(strings.Trim(fwd, "[]")); ip != nil {
				return ip.String()
			}
		}
	}
	return peer
}
