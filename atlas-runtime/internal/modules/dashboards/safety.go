package dashboards

// safety.go — allowlists and validators that gate every v2 data-source
// resolver. Every check here exists so dashboards cannot become a
// privilege-escalation vector. Rules are intentionally strict.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ── runtime endpoint allowlist ────────────────────────────────────────────────

// runtimeEndpointAllowlist is the set of GET endpoints widgets may pull from.
// Each entry is matched as either an exact path or a prefix ending in "/".
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
	"/mind/thoughts",
	"/mind/telemetry",
	"/mind/telemetry/summary",
	"/chat/pending-greetings",
}

// allowedRuntimeEndpoint reports whether endpoint is reachable by a widget.
// endpoint must be the path portion only (no query string).
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

// ── SQL validator (read-only, single statement) ──────────────────────────────

// validateSelectSQL ensures the supplied query is a single read-only SELECT
// (or WITH … SELECT). Returns the cleaned single-statement SQL on success.
//
// Relies on a read-only sqlite connection (?mode=ro) as the second line of
// defense; this lexical check is the first.
func validateSelectSQL(sqlText string) (string, error) {
	cleaned := strings.TrimSpace(sqlText)
	if cleaned == "" {
		return "", errors.New("sql query is required")
	}
	cleaned = strings.TrimSuffix(cleaned, ";")
	if strings.Contains(cleaned, ";") {
		return "", errors.New("sql query must contain a single statement")
	}
	lower := strings.ToLower(cleaned)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return "", errors.New("sql query must start with SELECT (or WITH … SELECT)")
	}
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

// ── chat analytics allowlist ─────────────────────────────────────────────────

// chatAnalyticsQueries is the allowlist of chat_analytics queries that widgets
// may request. Each query maps to a fixed SQL built in resolve_chat.go.
var chatAnalyticsQueries = map[string]bool{
	"conversations_per_day":       true,
	"messages_per_day":            true,
	"top_conversations":           true,
	"recent_conversations":        true,
	"message_counts_by_role":      true,
	"token_usage_per_day":         true,
	"token_usage_by_provider":     true,
	"memory_counts_by_category":   true,
	"most_important_memories":     true,
	"recent_memories":             true,
}

// validateAnalyticsQuery reports whether name is a known chat_analytics query.
// On rejection, the error lists the full allowlist so the agent can pick a
// valid one instead of guessing (chat_analytics is strictly Atlas's own
// conversation/memory stats — it is never the right source for news, weather,
// or other external data).
func validateAnalyticsQuery(name string) error {
	if name == "" {
		return fmt.Errorf("chat_analytics query name is required (valid: %s)", listAnalyticsQueries())
	}
	if !chatAnalyticsQueries[name] {
		return fmt.Errorf("unknown chat_analytics query %q (valid: %s)", name, listAnalyticsQueries())
	}
	return nil
}

func listAnalyticsQueries() string {
	names := make([]string, 0, len(chatAnalyticsQueries))
	for k := range chatAnalyticsQueries {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ── live_compute validator ───────────────────────────────────────────────────

// validateLiveCompute checks a live_compute spec has a non-empty prompt and
// an output schema. inputs is optional: omit it (or pass []) for standalone
// AI-generated content; supply source names to feed resolved data into the
// prompt. When inputs are provided every entry must be a non-empty string
// referring to another source name in the same dashboard.
func validateLiveCompute(cfg map[string]any) error {
	promptRaw, ok := cfg["prompt"]
	if !ok {
		return errors.New("live_compute requires prompt")
	}
	prompt, ok := promptRaw.(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return errors.New("live_compute prompt must be a non-empty string")
	}
	inputCount := 0
	// inputs is optional — omit entirely for standalone AI-generated sources.
	if inputsRaw, exists := cfg["inputs"]; exists {
		inputs, ok := inputsRaw.([]any)
		if !ok {
			return errors.New("live_compute inputs must be an array of source names (or omit for standalone mode)")
		}
		inputCount = len(inputs)
		for i, entry := range inputs {
			name, ok := entry.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("live_compute inputs[%d] must be a non-empty string", i)
			}
		}
	}
	if inputCount == 0 && promptNeedsFreshInputs(prompt) {
		return errors.New("live_compute prompts about latest/current/news data must declare inputs from a real source first")
	}
	if _, ok := cfg["outputSchema"]; !ok {
		return errors.New("live_compute requires outputSchema")
	}
	return nil
}

func promptNeedsFreshInputs(prompt string) bool {
	lower := strings.ToLower(prompt)
	// Multi-word phrases: substring match is unambiguous.
	for _, kw := range []string{"this week", "this month"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	// Single-word keywords: require word boundaries to avoid false positives
	// ("now" ⊂ "know", "current" ⊂ "concurrent", etc.)
	for _, kw := range []string{"latest", "current", "today", "recent", "news", "breaking", "now"} {
		if containsWholeWord(lower, kw) {
			return true
		}
	}
	return false
}

// containsWholeWord reports whether word appears as a whole word in s.
// s must already be lowercased; word must be a lowercase ASCII string.
func containsWholeWord(s, word string) bool {
	wlen := len(word)
	for i := 0; i <= len(s)-wlen; i++ {
		if s[i:i+wlen] != word {
			continue
		}
		if i > 0 && s[i-1] >= 'a' && s[i-1] <= 'z' {
			continue
		}
		if end := i + wlen; end < len(s) && s[end] >= 'a' && s[end] <= 'z' {
			continue
		}
		return true
	}
	return false
}

// ── generated TSX validator ──────────────────────────────────────────────────

// tsxForbiddenTokens is a conservative deny-list checked on agent-authored
// widget source before compilation. A match short-circuits compile.
var tsxForbiddenTokens = []string{
	// Dynamic code execution.
	"eval(",
	"new Function(",
	"Function(",
	// Network / storage escape hatches — widgets may only use the data
	// supplied via the sandbox bridge.
	"fetch(",
	"XMLHttpRequest",
	"WebSocket",
	"EventSource",
	"navigator.sendBeacon",
	// DOM storage and cookies — out of scope for a widget.
	"document.cookie",
	"localStorage",
	"sessionStorage",
	"indexedDB",
	// Window / parent escape attempts.
	"window.top",
	"window.parent",
	"window.opener",
	"parent.postMessage",
}

// allowedTSXImportPrefixes is the set of module specifiers a compiled widget
// may import. Anything else is rejected by the esbuild plugin in compile.go,
// but we keep this list here so validation can fail fast before invoking
// esbuild.
var allowedTSXImportPrefixes = []string{
	"@atlas/ui",
	"preact",
	"preact/hooks",
}

// validateGeneratedTSX runs cheap lexical checks over agent TSX source before
// compilation. Returns nil on success. The full import check is enforced in
// compile.go's esbuild plugin; the list here is a fast pre-filter.
func validateGeneratedTSX(src string) error {
	if strings.TrimSpace(src) == "" {
		return errors.New("widget tsx is empty")
	}
	if len(src) > 64*1024 {
		return fmt.Errorf("widget tsx is too large (%d bytes, max 65536)", len(src))
	}
	for _, tok := range tsxForbiddenTokens {
		if strings.Contains(src, tok) {
			return fmt.Errorf("widget tsx contains forbidden token %q", tok)
		}
	}
	return nil
}

// IsImportAllowed reports whether mod is in the TSX import allowlist. Used by
// the esbuild plugin in compile.go and by tests.
func IsImportAllowed(mod string) bool {
	for _, p := range allowedTSXImportPrefixes {
		if mod == p || strings.HasPrefix(mod, p+"/") {
			return true
		}
	}
	return false
}
