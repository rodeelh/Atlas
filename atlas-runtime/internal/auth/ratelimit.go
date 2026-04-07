package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RemoteAuthLimiter is a simple sliding-window rate limiter keyed by IP address.
// Default policy: 5 attempts per IP per minute.
// Intended for use on the /auth/remote login endpoint only.
type RemoteAuthLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	limit    int
}

// NewRemoteAuthLimiter creates a limiter with the default policy (5/min).
func NewRemoteAuthLimiter() *RemoteAuthLimiter {
	return &RemoteAuthLimiter{
		attempts: make(map[string][]time.Time),
		window:   time.Minute,
		limit:    5,
	}
}

// Allow returns true if the IP is within the rate limit, false if throttled.
// A denied attempt still counts toward the window.
func (l *RemoteAuthLimiter) Allow(ip string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// Prune timestamps outside the window.
	times := l.attempts[ip]
	fresh := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}

	allowed := len(fresh) < l.limit
	l.attempts[ip] = append(fresh, now)
	return allowed
}

// Middleware wraps h with IP-based rate limiting, writing 429 on breach.
func (l *RemoteAuthLimiter) Middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := remoteIP(r)
		if !l.Allow(ip) {
			writeError(w, http.StatusTooManyRequests,
				"Too many login attempts. Please wait a minute and try again.")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// remoteIP extracts the effective client IP for throttling decisions.
// Forwarded headers are only accepted when the immediate peer is loopback.
func remoteIP(r *http.Request) string {
	ip := ClientIP(r)
	if ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
