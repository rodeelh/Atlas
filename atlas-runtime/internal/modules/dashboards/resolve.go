package dashboards

// resolve.go — dispatches a DataSource to its kind-specific resolver and
// exposes the narrow interfaces each resolver depends on.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SkillExecutor is the narrow interface dashboards uses to call read-only
// skills. Satisfied by *skills.Registry.
type SkillExecutor interface {
	Execute(ctx context.Context, actionID string, args json.RawMessage) (skillExecResult, error)
}

// skillExecResult is the shape we need from skills.ToolResult. Declared
// locally so tests can satisfy SkillExecutor without importing skills.
// Artifacts carries the structured data payload; it is preferred over parsing
// Summary because most built-in skills return human-readable text in Summary
// and machine-readable data in Artifacts.
type skillExecResult struct {
	Success   bool           `json:"success"`
	Summary   string         `json:"summary"`
	Artifacts map[string]any `json:"artifacts,omitempty"`
}

// RuntimeFetcher performs a GET against the local runtime over loopback.
type RuntimeFetcher interface {
	Get(ctx context.Context, path string, query map[string]string) ([]byte, int, error)
}

// LiveComputeRunner executes a live_compute spec and returns the parsed
// payload (must conform to the spec's outputSchema). Wired by the module so
// tests can stub the AI call.
type LiveComputeRunner interface {
	Run(ctx context.Context, spec LiveComputeSpec, inputs map[string]any) (any, error)
}

// ProviderResolver returns the current AI provider configuration.
type ProviderResolver func() (providerConfig, error)

// providerConfig is a narrow local alias of agent.ProviderConfig so this
// package does not couple to the agent package at the type level.
type providerConfig struct {
	Type         string
	APIKey       string
	Model        string
	BaseURL      string
	ExtraHeaders map[string]string
}

// LiveComputeSpec is the normalized shape of a live_compute source.
type LiveComputeSpec struct {
	Prompt       string
	Inputs       []string
	OutputSchema map[string]any
}

// ErrSourceMissing is returned by named-source lookups when a binding refers
// to a source that does not exist.
var ErrSourceMissing = errors.New("data source not found")

// resolverDeps carries everything a resolver might need. Constructed once
// per module and passed by value into per-request calls.
type resolverDeps struct {
	runtime          RuntimeFetcher
	skills           SkillExecutor
	db               *sql.DB
	dbPath           string
	liveRunner       LiveComputeRunner
	providerResolver ProviderResolver
}

// resolveSource loads raw data for a single DataSource. It does not consult
// the cache; use resolveCached via refresh.go for cache-aware lookups.
// otherSources is used by live_compute to feed its dependencies.
func resolveSource(ctx context.Context, deps resolverDeps, src DataSource, otherSources map[string]any) (any, error) {
	switch src.Kind {
	case SourceKindRuntime:
		return resolveRuntime(ctx, deps.runtime, src.Config)
	case SourceKindSkill:
		return resolveSkill(ctx, deps.skills, src.Config)
	case SourceKindSQL:
		return resolveSQL(ctx, deps.dbPath, src.Config)
	case SourceKindChatAnalytics:
		return resolveChatAnalytics(ctx, deps.db, src.Config)
	case SourceKindGremlin:
		return resolveGremlin(ctx, deps.db, src.Config)
	case SourceKindLiveCompute:
		return resolveLiveCompute(ctx, deps, src.Config, otherSources)
	default:
		return nil, fmt.Errorf("unknown data source kind: %q", src.Kind)
	}
}

// isPermissionError reports whether err originated from a safety/allowlist
// rejection. Used so HTTP handlers return 403 for sandbox violations.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	patterns := []string{
		"not on the dashboards allowlist",
		"runtime endpoint must start with /",
		"is not read-only",
		"unknown skill action",
		"may not contain",
		"must start with SELECT",
		"must contain a single statement",
		"unknown chat_analytics query",
		"forbidden token",
		"unknown data source kind",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// skillResultFromToolResult converts a skills.ToolResult into the narrow
// local skillExecResult. Kept as a package-visible helper so the module
// wiring can supply an adapter without the resolvers depending on the
// skills package directly.
func skillResultFromToolResult(success bool, summary string) skillExecResult {
	return skillExecResult{Success: success, Summary: summary}
}
