package agent

import (
	"os"
	"path/filepath"
	"strings"
)

func normalizeSearchResult(result map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"rank", "title", "resolved_title", "url", "domain", "snippet", "provider", "confidence", "published_at", "source_type", "content_preview"} {
		if value, ok := result[key]; ok {
			out[key] = value
		}
	}
	return out
}

func sourceBlockTitle(toolName string) string {
	switch toolName {
	case "web.news":
		return "Recent coverage"
	case "web.search_latest":
		return "Latest sources"
	case "web.search_docs":
		return "Documentation sources"
	case "web.search_entities":
		return "Entity sources"
	case "web.research":
		return "Research sources"
	case "web.multi_search":
		return "Searches"
	default:
		return "Sources"
	}
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

// BlocksFromToolArtifacts converts tool artifacts into persisted/renderable blocks.
func BlocksFromToolArtifacts(toolName string, artifacts map[string]any) []map[string]any {
	if len(artifacts) == 0 {
		return nil
	}
	if mapType, _ := artifacts["map_type"].(string); strings.TrimSpace(mapType) != "" {
		return []map[string]any{{
			"type": "map",
			"card": artifacts,
		}}
	}

	if rawQueries := mapSlice(artifacts["queries"]); len(rawQueries) > 0 {
		groups := make([]map[string]any, 0, len(rawQueries))
		for _, query := range rawQueries {
			rawResults := mapSlice(query["results"])
			results := make([]map[string]any, 0, len(rawResults))
			for _, result := range rawResults {
				results = append(results, normalizeSearchResult(result))
			}
			if len(results) == 0 {
				continue
			}
			groups = append(groups, map[string]any{
				"query":     query["query"],
				"provider":  query["provider"],
				"fetchedAt": query["fetched_at"],
				"results":   results,
			})
		}
		if len(groups) > 0 {
			return []map[string]any{{
				"type":   "multi-source-list",
				"title":  sourceBlockTitle(toolName),
				"groups": groups,
			}}
		}
	}

	if rawResults := mapSlice(artifacts["results"]); len(rawResults) > 0 {
		results := make([]map[string]any, 0, len(rawResults))
		for _, result := range rawResults {
			results = append(results, normalizeSearchResult(result))
		}
		return []map[string]any{{
			"type":      "source-list",
			"title":     sourceBlockTitle(toolName),
			"query":     artifacts["query"],
			"provider":  artifacts["provider"],
			"fetchedAt": artifacts["fetched_at"],
			"results":   results,
		}}
	}

	source, _ := artifacts["source"].(map[string]any)
	sourceURL, _ := artifacts["url"].(string)
	if strings.TrimSpace(sourceURL) == "" && source != nil {
		sourceURL, _ = source["url"].(string)
	}
	if strings.TrimSpace(sourceURL) == "" {
		if canonicalURL, _ := artifacts["canonical_url"].(string); strings.TrimSpace(canonicalURL) != "" {
			sourceURL = canonicalURL
		}
	}
	title, _ := artifacts["title"].(string)
	summary, _ := artifacts["summary"].(string)
	if strings.TrimSpace(summary) == "" {
		summary, _ = artifacts["preview"].(string)
	}
	if strings.TrimSpace(sourceURL) != "" && (strings.TrimSpace(title) != "" || strings.TrimSpace(summary) != "") {
		block := map[string]any{
			"type":         "source-summary",
			"title":        title,
			"url":          sourceURL,
			"description":  artifacts["description"],
			"summary":      summary,
			"headings":     artifacts["headings"],
			"publishedAt":  artifacts["published_at"],
			"canonicalURL": artifacts["canonical_url"],
		}
		if source != nil {
			block["domain"] = source["domain"]
		}
		return []map[string]any{block}
	}

	return nil
}

// FileBlockForPath converts a local artifact path into a persisted file block.
func FileBlockForPath(path string) map[string]any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return map[string]any{
		"type": "file",
		"file": map[string]any{
			"filename": filepath.Base(path),
			"mimeType": MimeTypeForPath(path),
			"fileSize": info.Size(),
			"path":     path,
		},
	}
}
