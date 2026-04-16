package dashboards

// resolve_runtime.go — loads data by GETting an allowlisted runtime endpoint
// over loopback. Rejects any path not in runtimeEndpointAllowlist.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// resolveRuntime loads data from a runtime endpoint.
// cfg shape: { "endpoint": "/status", "query": {"k":"v"} }
func resolveRuntime(ctx context.Context, fetcher RuntimeFetcher, cfg map[string]any) (any, error) {
	if fetcher == nil {
		return nil, errors.New("runtime fetcher is not wired")
	}
	endpoint, _ := cfg["endpoint"].(string)
	endpoint = strings.TrimSpace(endpoint)
	if !strings.HasPrefix(endpoint, "/") {
		return nil, errors.New("runtime endpoint must start with /")
	}
	if !allowedRuntimeEndpoint(endpoint) {
		return nil, fmt.Errorf("runtime endpoint %q is not on the dashboards allowlist", endpoint)
	}
	query := map[string]string{}
	if raw, ok := cfg["query"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				query[k] = s
			} else {
				query[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	body, status, err := fetcher.Get(ctx, endpoint, query)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("runtime endpoint %s returned %d", endpoint, status)
	}
	// Parse as JSON when possible; pass through raw text otherwise.
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		return parsed, nil
	}
	return string(body), nil
}

// LoopbackFetcher implements RuntimeFetcher by calling the local runtime
// over 127.0.0.1 on the chosen port.
type LoopbackFetcher struct {
	port   int
	client *http.Client
}

// NewLoopbackFetcher constructs a fetcher targeting 127.0.0.1:port.
func NewLoopbackFetcher(port int) *LoopbackFetcher {
	return &LoopbackFetcher{
		port: port,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Get issues a GET and returns (body, status, err). Context timeouts apply.
func (f *LoopbackFetcher) Get(ctx context.Context, path string, query map[string]string) ([]byte, int, error) {
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", f.port),
		Path:   path,
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
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
