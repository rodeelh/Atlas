package dashboards

// safety.go — allowlists and validators that gate every data-source resolver.
//
// Every check here exists to keep the dashboards module from becoming a
// privilege-escalation vector. The rules are intentionally strict and
// duplicated across resolvers; resolve.go is responsible for calling them.

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// runtimeEndpointAllowlist is the set of GET endpoints widgets may pull from.
// Each entry is matched as either an exact path or a prefix ending in "/".
//
// Anything not on this list is rejected with 403 by resolveRuntime — even if
// it would otherwise be a valid runtime route. New widgets that need a new
// endpoint must add it here explicitly.
var runtimeEndpointAllowlist = []string{
	"/status",
	"/logs",
	"/memories",
	"/diary",
	"/mind",
	"/skills",
	"/skills-memory",
	"/workflows",
	"/workflows/",
	"/automations",
	"/automations/",
	"/communications",
	"/communications/",
	"/forge/proposals",
	"/forge/installed",
	"/forge/researching",
	"/usage/summary",
	"/usage/events",
	// Mind-thoughts telemetry — used by the Mind Health dashboard template.
	"/mind/thoughts",
	"/mind/telemetry",
	"/mind/telemetry/summary",
	"/chat/pending-greetings",
}

// allowedRuntimeEndpoint reports whether endpoint is reachable by a widget.
// endpoint must already be the path portion only (no query string).
func allowedRuntimeEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	for _, allowed := range runtimeEndpointAllowlist {
		if strings.HasSuffix(allowed, "/") {
			if strings.HasPrefix(endpoint, allowed) {
				return true
			}
		} else if endpoint == allowed {
			return true
		}
	}
	return false
}

// validateWebURL parses the URL, enforces scheme http(s), and rejects any host
// that resolves to a private, loopback, link-local, or unspecified address.
// Returns the parsed *url.URL on success.
func validateWebURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("web url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid web url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("web url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("web url has no host")
	}
	// Reject literal localhost / .local / loopback names without DNS lookup.
	hostname := u.Hostname()
	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "localhost.localdomain" || strings.HasSuffix(lower, ".local") {
		return nil, errors.New("web url host is local")
	}
	// Resolve all addresses and reject if any is private/loopback/link-local.
	addrs, err := net.LookupIP(hostname)
	if err != nil {
		// If hostname is itself an IP literal, parse it directly.
		if ip := net.ParseIP(hostname); ip != nil {
			addrs = []net.IP{ip}
		} else {
			return nil, fmt.Errorf("dns lookup failed for %q: %w", hostname, err)
		}
	}
	for _, ip := range addrs {
		if isUnsafeIP(ip) {
			return nil, fmt.Errorf("web url host %q resolves to a non-public address", hostname)
		}
	}
	return u, nil
}

// isUnsafeIP returns true for any address class a widget must never reach via
// the web resolver: loopback, link-local, unspecified, multicast, or any of
// the RFC1918 / RFC4193 private ranges.
func isUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsPrivate() {
		return true
	}
	return false
}

// validateSelectSQL ensures the supplied query is a single read-only SELECT.
// Returns the cleaned (single-statement) SQL on success.
//
// The check is intentionally conservative: anything that looks like a write,
// schema mutation, attach/detach, pragma, or transaction control is rejected.
// We rely on a read-only sqlite connection (?mode=ro) as the second line of
// defense; this lexical check is the first.
func validateSelectSQL(sqlText string) (string, error) {
	cleaned := strings.TrimSpace(sqlText)
	if cleaned == "" {
		return "", errors.New("sql query is required")
	}
	// Strip an optional trailing semicolon, but reject any other.
	cleaned = strings.TrimSuffix(cleaned, ";")
	if strings.Contains(cleaned, ";") {
		return "", errors.New("sql query must contain a single statement")
	}
	lower := strings.ToLower(cleaned)
	// Must start with SELECT or WITH (CTE).
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return "", errors.New("sql query must start with SELECT (or WITH … SELECT)")
	}
	// Reject any forbidden keyword as a whole token.
	forbidden := []string{
		"insert", "update", "delete", "drop", "create", "alter", "replace",
		"truncate", "vacuum", "attach", "detach", "pragma", "begin", "commit",
		"rollback", "savepoint", "reindex", "analyze",
	}
	for _, kw := range forbidden {
		if containsKeyword(lower, kw) {
			return "", fmt.Errorf("sql query may not contain %q", kw)
		}
	}
	return cleaned, nil
}

// containsKeyword reports whether s contains keyword as a whole word
// (delimited by start/end-of-string or non-identifier characters).
func containsKeyword(s, keyword string) bool {
	for i := 0; i+len(keyword) <= len(s); i++ {
		if s[i:i+len(keyword)] != keyword {
			continue
		}
		left := i == 0 || !isIdentChar(s[i-1])
		right := i+len(keyword) == len(s) || !isIdentChar(s[i+len(keyword)])
		if left && right {
			return true
		}
	}
	return false
}

func isIdentChar(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}
