package skills

import (
	"context"
	"encoding/json"
	"fmt"
)

func (r *Registry) registerWebSearch() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "websearch.query",
			Description: "Search the web and return structured results with titles, URLs, snippets, provider, and source metadata.",
			Properties: map[string]ToolParam{
				"query":     {Description: "The search query", Type: "string"},
				"count":     {Description: "Number of results to return (default 5, max 10)", Type: "integer"},
				"freshness": {Description: "Optional freshness window: day, week, month, year", Type: "string"},
				"site":      {Description: "Optional site or hostname to focus on", Type: "string"},
				"domains":   {Description: "Optional domains to prefer, comma-separated", Type: "string"},
				"language":  {Description: "Optional language code for search localization", Type: "string"},
			},
			Required: []string{"query"},
		},
		PermLevel: "read",
		FnResult:  webSearchQuery,
	})
}

func webSearchQuery(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var p struct {
		Query     string `json:"query"`
		Count     int    `json:"count"`
		Freshness string `json:"freshness"`
		Site      string `json:"site"`
		Domains   string `json:"domains"`
		Language  string `json:"language"`
	}
	if err := json.Unmarshal(args, &p); err != nil || p.Query == "" {
		return ToolResult{}, fmt.Errorf("query is required")
	}

	resp, err := searchWebWithFallback(ctx, SearchOptions{
		Query:      p.Query,
		Count:      p.Count,
		Freshness:  p.Freshness,
		Site:       p.Site,
		Domains:    splitQueryList(p.Domains),
		Language:   p.Language,
		SearchType: "web",
	})
	if err != nil {
		return ToolResult{}, err
	}
	if len(resp.Results) == 0 {
		return OKResult("No results found for: "+p.Query, map[string]any{
			"query":      p.Query,
			"provider":   resp.Provider,
			"filters":    resp.Filters,
			"results":    []map[string]any{},
			"fetched_at": resp.FetchedAt,
		}), nil
	}
	results := make([]map[string]any, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, searchResultArtifact(item))
	}
	return OKResult(searchSummary("Web search", p.Query, len(resp.Results), resp.Provider, resp.Cached), map[string]any{
		"query":      p.Query,
		"provider":   resp.Provider,
		"filters":    resp.Filters,
		"results":    results,
		"fetched_at": resp.FetchedAt,
	}), nil
}
