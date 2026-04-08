package dashboards

// resolve.go — turns a Widget.Source into WidgetData.
//
// One resolver per kind. All four are gated by safety.go and bounded by
// context-deadline timeouts. The HTTP handler in module.go is responsible for
// looking up the dashboard and widget; the resolvers here only see the source.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // sqlite driver, already used by internal/storage

	"atlas-runtime-go/internal/skills"
)

// WidgetData is the resolver result returned by POST /dashboards/{id}/resolve.
// Exactly one of Data and Error is meaningful.
type WidgetData struct {
	WidgetID    string `json:"widgetId"`
	Success     bool   `json:"success"`
	Data        any    `json:"data,omitempty"`
	Error       string `json:"error,omitempty"`
	ResolvedAt  string `json:"resolvedAt"`
	SourceKind  string `json:"sourceKind"`
	DurationMs  int64  `json:"durationMs"`
}

// SkillExecutor is the minimum slice of *skills.Registry that the dashboards
// module needs. *skills.Registry satisfies it directly; tests provide a fake.
type SkillExecutor interface {
	PermissionLevel(actionID string) string
	Execute(ctx context.Context, actionID string, args json.RawMessage) (skills.ToolResult, error)
}

// RuntimeFetcher abstracts the runtime-loopback HTTP call so tests can stub
// the runtime without standing up a real server.
type RuntimeFetcher interface {
	Fetch(ctx context.Context, endpoint string, query map[string]string) ([]byte, int, error)
}

// LoopbackFetcher implements RuntimeFetcher by hitting 127.0.0.1:<port>.
// Localhost requests bypass session auth in the runtime, so this works without
// a token bridge.
type LoopbackFetcher struct {
	Port   int
	Client *http.Client
}

// NewLoopbackFetcher returns a fetcher with sensible defaults.
func NewLoopbackFetcher(port int) *LoopbackFetcher {
	return &LoopbackFetcher{
		Port: port,
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (f *LoopbackFetcher) Fetch(ctx context.Context, endpoint string, query map[string]string) ([]byte, int, error) {
	if f.Port <= 0 {
		return nil, 0, errors.New("loopback fetcher: port not configured")
	}
	u := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", f.Port),
		Path:   endpoint,
	}
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// resolveSource is the central dispatch. The Module wires concrete dependencies
// at construction time and calls this from its /resolve handler.
func (m *Module) resolveSource(ctx context.Context, src *DataSource) (any, error) {
	if src == nil {
		return nil, errors.New("widget has no source")
	}
	switch src.Kind {
	case SourceKindRuntime:
		return m.resolveRuntime(ctx, src)
	case SourceKindSkill:
		return m.resolveSkill(ctx, src)
	case SourceKindWeb:
		return m.resolveWeb(ctx, src)
	case SourceKindSQL:
		return m.resolveSQL(ctx, src)
	default:
		return nil, fmt.Errorf("unknown source kind %q", src.Kind)
	}
}

// ── runtime ───────────────────────────────────────────────────────────────────

func (m *Module) resolveRuntime(ctx context.Context, src *DataSource) (any, error) {
	if m.runtime == nil {
		return nil, errors.New("runtime fetcher not configured")
	}
	endpoint := src.Endpoint
	if !strings.HasPrefix(endpoint, "/") {
		return nil, fmt.Errorf("runtime endpoint must start with /, got %q", endpoint)
	}
	if !allowedRuntimeEndpoint(endpoint) {
		return nil, fmt.Errorf("runtime endpoint %q is not on the dashboards allowlist", endpoint)
	}
	body, status, err := m.runtime.Fetch(ctx, endpoint, src.Query)
	if err != nil {
		return nil, fmt.Errorf("runtime fetch %s: %w", endpoint, err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("runtime fetch %s: status %d", endpoint, status)
	}
	// Try to parse as JSON; if it isn't, return the raw text.
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		return parsed, nil
	}
	return string(body), nil
}

// ── skill ─────────────────────────────────────────────────────────────────────

func (m *Module) resolveSkill(ctx context.Context, src *DataSource) (any, error) {
	if m.skills == nil {
		return nil, errors.New("skill executor not configured")
	}
	if src.Action == "" {
		return nil, errors.New("skill action is required")
	}
	level := m.skills.PermissionLevel(src.Action)
	if level == "" {
		return nil, fmt.Errorf("unknown skill action %q", src.Action)
	}
	if level != "read" {
		return nil, fmt.Errorf("skill %q is not read-only (permission_level=%q)", src.Action, level)
	}
	args, err := json.Marshal(src.Args)
	if err != nil {
		return nil, fmt.Errorf("encode skill args: %w", err)
	}
	result, err := m.skills.Execute(ctx, src.Action, args)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", src.Action, err)
	}
	if !result.Success {
		return nil, fmt.Errorf("skill %s failed: %s", src.Action, result.Summary)
	}
	// Prefer structured artifacts when present; otherwise fall back to summary.
	if result.Artifacts != nil {
		return map[string]any{
			"summary":   result.Summary,
			"artifacts": result.Artifacts,
		}, nil
	}
	return map[string]any{"summary": result.Summary}, nil
}

// ── web ───────────────────────────────────────────────────────────────────────

func (m *Module) resolveWeb(ctx context.Context, src *DataSource) (any, error) {
	parsed, err := validateWebURL(src.URL)
	if err != nil {
		return nil, err
	}
	client := m.webClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many redirects")
				}
				// Re-validate every redirect target — a redirect to localhost
				// is exactly the kind of attack we're guarding against.
				if _, err := validateWebURL(req.URL.String()); err != nil {
					return err
				}
				return nil
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Atlas-Dashboards/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("web fetch: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024)) // 256 KB cap
	if err != nil {
		return nil, fmt.Errorf("web read: %w", err)
	}
	// Best-effort JSON; otherwise return text.
	var parsedBody any
	if err := json.Unmarshal(body, &parsedBody); err == nil {
		return parsedBody, nil
	}
	return string(body), nil
}

// ── sql ───────────────────────────────────────────────────────────────────────

func (m *Module) resolveSQL(ctx context.Context, src *DataSource) (any, error) {
	if m.dbPath == "" {
		return nil, errors.New("sql resolver not configured")
	}
	cleaned, err := validateSelectSQL(src.SQL)
	if err != nil {
		return nil, err
	}
	// Append a LIMIT if the user didn't supply one. This is a courtesy cap;
	// the read-only connection is the real safety guarantee.
	if !containsKeyword(strings.ToLower(cleaned), "limit") {
		cleaned += " LIMIT 500"
	}

	// Open a fresh, read-only connection per call. modernc.org/sqlite honors
	// the standard SQLite URI flag mode=ro.
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)", m.dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	rows, err := conn.QueryContext(queryCtx, cleaned)
	if err != nil {
		return nil, fmt.Errorf("sql query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sql columns: %w", err)
	}

	var out []map[string]any
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sql scan: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = sqliteValue(holders[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sql rows: %w", err)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return map[string]any{
		"columns": cols,
		"rows":    out,
	}, nil
}

// sqliteValue normalises driver values: byte slices become strings so JSON
// encoding stays human-readable.
func sqliteValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}
